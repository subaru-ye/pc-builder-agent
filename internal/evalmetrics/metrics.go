// Package evalmetrics 提供 P10 验收期间的可选、脱敏 JSONL 指标。
// P10_METRICS_DIR 未设置时全部操作为 no-op,不改变正常产品路径。
package evalmetrics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	openai "github.com/openai/openai-go/v3"
	"google.golang.org/adk/v2/model"
)

var (
	writeMu sync.Mutex
	callSeq atomic.Uint64
)

// Event 是一条不含用户原文和模型内容的评测指标。
type Event struct {
	SchemaVersion int            `json:"schema_version"`
	Timestamp     time.Time      `json:"timestamp"`
	Component     string         `json:"component"`
	Name          string         `json:"name"`
	Fields        map[string]any `json:"fields,omitempty"`
}

// Enabled 表示显式配置了 P10 指标目录。
func Enabled() bool { return os.Getenv("P10_METRICS_DIR") != "" }

// Fingerprint 产生可关联但不可逆的短指纹,不把 session/run ID 写入验收文件。
func Fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}

// Record 向 {P10_METRICS_DIR}/{component}.jsonl 追加一条指标。
// 验收记录失败不应中断产品 run,但会明确写日志使 P10 门禁失败。
func Record(component, name string, fields map[string]any) {
	dir := os.Getenv("P10_METRICS_DIR")
	if dir == "" {
		return
	}
	if filepath.Base(component) != component || component == "." || component == "" {
		log.Printf("[p10] 拒绝非法 metrics component=%q", component)
		return
	}
	line, err := json.Marshal(Event{
		SchemaVersion: 1,
		Timestamp:     time.Now().UTC(),
		Component:     component,
		Name:          name,
		Fields:        fields,
	})
	if err != nil {
		log.Printf("[p10] 序列化 metrics 失败: %v", err)
		return
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("[p10] 创建 metrics 目录失败: %v", err)
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, component+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("[p10] 打开 metrics 文件失败: %v", err)
		return
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(line, '\n')); err != nil {
		log.Printf("[p10] 写 metrics 失败: %v", err)
	}
}

type measuredModel struct {
	component string
	inner     model.LLM
}

// WrapModel 记录每次顶层 GenerateContent 的耗时、状态和 Token 计数。
func WrapModel(component string, inner model.LLM) model.LLM {
	if inner == nil || !Enabled() {
		return inner
	}
	return &measuredModel{component: component, inner: inner}
}

func (m *measuredModel) Name() string { return m.inner.Name() }

func (m *measuredModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		started := time.Now()
		id := callSeq.Add(1)
		status := "succeeded"
		var promptTokens, candidateTokens, totalTokens int32
		var callErr error
		responseErrorCode := ""
		m.inner.GenerateContent(ctx, req, stream)(func(resp *model.LLMResponse, err error) bool {
			if err != nil {
				status = "failed"
				callErr = err
			}
			if resp != nil {
				if resp.ErrorCode != "" || resp.ErrorMessage != "" {
					status = "failed"
					responseErrorCode = resp.ErrorCode
				}
				if usage := resp.UsageMetadata; usage != nil {
					promptTokens = usage.PromptTokenCount
					candidateTokens = usage.CandidatesTokenCount
					totalTokens = usage.TotalTokenCount
				}
			}
			return yield(resp, err)
		})
		fields := map[string]any{
			"call_id": id, "model": m.inner.Name(), "status": status,
			"duration_ms": time.Since(started).Milliseconds(), "stream": stream,
			"prompt_tokens": promptTokens, "candidate_tokens": candidateTokens, "total_tokens": totalTokens,
		}
		for key, value := range safeModelErrorFields(callErr, responseErrorCode) {
			fields[key] = value
		}
		Record(m.component, "model.call", fields)
	}
}

var safeErrorCodeRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,96}$`)

// safeModelErrorFields 只保留上游状态码和机器错误 Code,不记录可能含请求正文的 error message。
func safeModelErrorFields(err error, responseCode string) map[string]any {
	fields := map[string]any{}
	code := responseCode
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode > 0 {
			fields["http_status"] = apiErr.StatusCode
		}
		if code == "" {
			code = apiErr.Code
		}
	}
	if code != "" {
		if !safeErrorCodeRE.MatchString(code) {
			code = "redacted"
		}
		fields["error_code"] = code
	}
	return fields
}

// ValidateDirectory 供验收命令预检指标目录是否可写。
func ValidateDirectory() error {
	dir := os.Getenv("P10_METRICS_DIR")
	if dir == "" {
		return fmt.Errorf("P10_METRICS_DIR 未设置")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return nil
}
