// Package embedding 实现 OpenAI 兼容 /embeddings 的最小客户端。
// 供 cmd/embedparts(全量生成)与 buildsvc(查询向量化)共用；供应商、模型和端点
// 由 internal/modelprovider 统一解析。
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/evalmetrics"
	"github.com/subaru-ye/pc-builder-agent/internal/upstream"
)

// DefaultModel 端点 embedding 模型(2026-07-30 实测输出 1024 维)。
const DefaultModel = "qwen3.7-text-embedding"

// batchSize 单请求文本数上限(百炼 embedding 批量限制,保守取 10)。
const batchSize = 10

// Client OpenAI 兼容 /embeddings 客户端。
type Client struct {
	baseURL  string
	apiKey   string
	model    string
	dims     int // 期望维度,响应不符即报错(不静默截断/填充)
	hc       *http.Client
	provider string
	role     string
}

// Options 是供应商层传入的 embedding 连接参数。
type Options struct {
	BaseURL    string
	APIKey     string
	Model      string
	Dimensions int
	Timeout    time.Duration
	Provider   string
	Role       string
}

// NewClient 构造客户端;dims 为期望向量维度(与 store.EmbeddingDims 对齐)。
func NewClient(baseURL, apiKey, model string, dims int) *Client {
	return NewClientWithOptions(Options{
		BaseURL: baseURL, APIKey: apiKey, Model: model, Dimensions: dims,
		Timeout: 60 * time.Second, Provider: "bailian", Role: "embedding",
	})
}

// NewClientWithOptions 构造带供应商身份和可配置超时的客户端。
func NewClientWithOptions(opts Options) *Client {
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
	return &Client{
		baseURL:  opts.BaseURL,
		apiKey:   opts.APIKey,
		model:    opts.Model,
		dims:     opts.Dimensions,
		hc:       &http.Client{Timeout: opts.Timeout},
		provider: opts.Provider,
		role:     opts.Role,
	}
}

// Embed 批量向量化;返回顺序与入参一致。任一批失败即整体报错,不静默跳过。
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += batchSize {
		end := min(start+batchSize, len(texts))
		vecs, err := c.embedBatch(ctx, texts[start:end])
		if err != nil {
			return nil, fmt.Errorf("embedding: 批次 [%d:%d) 失败: %w", start, end, err)
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// EmbedOne 单条向量化(查询场景)。
func (c *Client) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	vecs, err := c.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

func (c *Client) embedBatch(ctx context.Context, texts []string) (out [][]float32, retErr error) {
	started := time.Now()
	var usagePrompt, usageTotal int
	defer func() {
		fields := map[string]any{
			"provider": c.provider, "role": c.role, "model": c.model,
			"duration_ms": time.Since(started).Milliseconds(), "input_count": len(texts),
			"prompt_tokens": usagePrompt, "total_tokens": usageTotal, "status": "succeeded",
		}
		if retErr != nil {
			fields["status"] = "failed"
		}
		var upstreamErr *upstream.Error
		if errors.As(retErr, &upstreamErr) {
			fields["status"] = "failed"
			fields["error_class"] = upstreamErr.Kind
			if upstreamErr.HTTPStatus > 0 {
				fields["http_status"] = upstreamErr.HTTPStatus
			}
		}
		evalmetrics.Record("buildsvc", "embedding.call", fields)
	}()
	body, err := json.Marshal(embedRequest{Model: c.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("编码请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, upstream.New(c.provider, c.role, upstream.Classify(0, "", err), 0, "", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		code := embeddingErrorCode(raw)
		cause := errors.New("embedding endpoint returned non-200")
		return nil, upstream.New(c.provider, c.role,
			upstream.Classify(resp.StatusCode, code, cause), resp.StatusCode, code, cause)
	}

	var er embedResponse
	if err := json.Unmarshal(raw, &er); err != nil {
		return nil, upstream.New(c.provider, c.role, upstream.KindProtocol, resp.StatusCode, "malformed_response", err)
	}
	usagePrompt, usageTotal = er.Usage.PromptTokens, er.Usage.TotalTokens
	if len(er.Data) != len(texts) {
		return nil, upstream.New(c.provider, c.role, upstream.KindProtocol, resp.StatusCode,
			"embedding_count_mismatch", errors.New("embedding count mismatch"))
	}

	// 按 index 归位(响应顺序不做假设),并校验维度。
	out = make([][]float32, len(texts))
	for _, d := range er.Data {
		if d.Index < 0 || d.Index >= len(texts) {
			return nil, upstream.New(c.provider, c.role, upstream.KindProtocol, resp.StatusCode,
				"embedding_index_invalid", errors.New("embedding index invalid"))
		}
		if len(d.Embedding) != c.dims {
			return nil, upstream.New(c.provider, c.role, upstream.KindProtocol, resp.StatusCode,
				"embedding_dimension_mismatch", errors.New("embedding dimension mismatch"))
		}
		out[d.Index] = d.Embedding
	}
	for _, v := range out {
		if v == nil {
			return nil, upstream.New(c.provider, c.role, upstream.KindProtocol, resp.StatusCode,
				"embedding_index_missing", errors.New("embedding index missing"))
		}
	}
	return out, nil
}

func embeddingErrorCode(raw []byte) string {
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if payload.Error.Code != "" {
		return payload.Error.Code
	}
	return payload.Code
}
