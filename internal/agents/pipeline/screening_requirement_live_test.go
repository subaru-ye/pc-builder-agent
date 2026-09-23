package pipeline

// 显式、有界的真实模型冒烟。普通测试默认跳过；只装配当前 Screening，
// 不创建 Builder/Embedding，不允许模型链，不重试，不改写 .env。
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/upstream"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

const requirementLiveCallLimit = 4

type requirementLiveCall struct {
	Index      int                                         `json:"index"`
	DurationMS int64                                       `json:"duration_ms"`
	Output     string                                      `json:"output,omitempty"`
	Usage      *genai.GenerateContentResponseUsageMetadata `json:"usage,omitempty"`
	Error      string                                      `json:"error,omitempty"`
}

type requirementLiveModel struct {
	inner model.LLM
	calls []requirementLiveCall
}

func (m *requirementLiveModel) Name() string { return m.inner.Name() }
func (m *requirementLiveModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if len(m.calls) >= requirementLiveCallLimit {
			yield(nil, errors.New("Screening 调用硬上限已达到"))
			return
		}
		m.calls = append(m.calls, requirementLiveCall{Index: len(m.calls) + 1})
		call := &m.calls[len(m.calls)-1]
		started := time.Now()
		defer func() { call.DurationMS = time.Since(started).Milliseconds() }()
		for response, err := range m.inner.GenerateContent(ctx, req, stream) {
			if response != nil {
				call.Output = screeningText(response.Content)
				if response.UsageMetadata != nil {
					usage := *response.UsageMetadata
					call.Usage = &usage
				}
			}
			if err != nil {
				call.Error = safeRequirementLiveError(err)
			}
			if !yield(response, err) {
				return
			}
		}
	}
}

func safeRequirementLiveError(err error) string {
	var typed *upstream.Error
	if errors.As(err, &typed) {
		return fmt.Sprintf("provider=%s role=%s kind=%s status=%d code=%s", typed.Provider, typed.Role, typed.Kind, typed.HTTPStatus, upstream.SafeCode(typed.Code))
	}
	// 非供应商错误只保存类别；避免意外把连接信息写入证据。
	return fmt.Sprintf("%T", err)
}

func TestRequirementStateLiveSmoke(t *testing.T) {
	if os.Getenv("REQUIREMENT_STATE_LIVE_SMOKE") != "1" {
		t.Skip("显式设置 REQUIREMENT_STATE_LIVE_SMOKE=1；每次最多4次当前Screening调用，0次Builder/Embedding")
	}
	root := filepath.Join("..", "..", "..")
	dotenv.Load(filepath.Join(root, ".env"))
	cfg, err := modelprovider.Load(modelprovider.RoleScreening)
	if err != nil {
		t.Fatal("当前 Screening 配置无法加载，未调用模型")
	}
	if len(cfg.ModelChain) != 0 {
		t.Fatal("固定模型冒烟不允许 SCREENING_MODEL_CHAIN，未调用模型且未修改配置")
	}
	// 本次运行的 SDK 重试固定为零；持久化模型名、密钥和端点配置均不修改。
	cfg.MaxRetries = 0
	inner, err := modelprovider.NewChat(context.Background(), cfg, "requirement-state-live-smoke")
	if err != nil {
		t.Fatal("当前 Screening 无法装配，未调用模型")
	}
	counted := &requirementLiveModel{inner: inner}
	started := time.Now().UTC()
	dir := filepath.Join(root, "artifacts", "requirements-state", "live-smoke", started.Format("20060102T150405.000000000Z"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	type turnEvidence struct {
		Input string                   `json:"input"`
		State schemas.RequirementState `json:"state"`
		Spec  json.RawMessage          `json:"spec"`
	}
	turns := []turnEvidence{}
	failure := ""
	completed := false
	priorCalls := 0
	resumeFrom := os.Getenv("REQUIREMENT_STATE_LIVE_RESUME")
	protocol, _ := os.ReadFile("screening_requirement_state.go")
	hash := sha256.Sum256(protocol)
	defer func() {
		var inputTokens, outputTokens, totalTokens int64
		for _, call := range counted.calls {
			if call.Usage != nil {
				inputTokens += int64(call.Usage.PromptTokenCount)
				outputTokens += int64(call.Usage.CandidatesTokenCount)
				totalTokens += int64(call.Usage.TotalTokenCount)
			}
		}
		report := map[string]any{"started_at": started, "finished_at": time.Now().UTC(), "model": cfg.Model, "provider": cfg.Provider,
			"call_limit": requirementLiveCallLimit, "screening_calls": len(counted.calls), "builder_calls": 0, "embedding_calls": 0,
			"sdk_retries": 0, "model_chain": false, "passed": !t.Failed() && completed,
			"input_tokens": inputTokens, "output_tokens": outputTokens, "total_tokens": totalTokens,
			"protocol_sha256": hex.EncodeToString(hash[:]), "calls": counted.calls, "turns": turns, "failure": failure,
			"prior_screening_calls": priorCalls, "new_screening_calls": len(counted.calls) - priorCalls, "resumed_from": resumeFrom}
		raw, marshalErr := json.MarshalIndent(report, "", "  ")
		if marshalErr != nil {
			t.Errorf("无法序列化冒烟证据: %v", marshalErr)
			return
		}
		if err := os.WriteFile(filepath.Join(dir, "result.json"), append(raw, '\n'), 0o644); err != nil {
			t.Errorf("无法保存冒烟证据: %v", err)
		}
		t.Logf("model=%s screening_calls=%d builder_calls=0 embedding_calls=0 input_tokens=%d output_tokens=%d total_tokens=%d evidence=%s", cfg.Model, len(counted.calls), inputTokens, outputTokens, totalTokens, dir)
	}()
	inputs := []string{
		"预算8000元，主要2K游戏，尽量安静，显卡尽量用N卡，这次帮朋友装机。",
		"预算改成6000元，其他条件不变。",
		"取消显卡品牌偏好，不再要求N卡，其他条件保留。",
		"如果换成4K会怎样？只是讨论备选，分辨率先不改。",
	}
	state := schemas.NewRequirementState()
	firstIndex := 0
	if resumeFrom != "" {
		resumePath := resumeFrom
		if !filepath.IsAbs(resumePath) {
			resumePath = filepath.Join(root, resumePath)
		}
		raw, err := os.ReadFile(resumePath)
		var prior struct {
			Model  string                `json:"model"`
			Passed bool                  `json:"passed"`
			Calls  []requirementLiveCall `json:"calls"`
			Turns  []turnEvidence        `json:"turns"`
		}
		if err != nil || json.Unmarshal(raw, &prior) != nil || prior.Model != cfg.Model || prior.Passed || len(prior.Calls) != 3 || len(prior.Turns) != 2 {
			t.Fatal("续跑只允许从同一模型的3次调用、2轮成功状态恢复；未新增调用")
		}
		for _, call := range prior.Calls {
			if call.Error != "" {
				t.Fatal("上次包含供应商错误，禁止续跑")
			}
		}
		marker, err := os.OpenFile(filepath.Join(filepath.Dir(resumePath), "resume-used"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal("本次证据的唯一剩余调用已使用，禁止重复续跑")
		}
		_ = marker.Close()
		counted.calls, turns = prior.Calls, prior.Turns
		priorCalls = len(prior.Calls)
		state = prior.Turns[1].State
		firstIndex = 3
		inputs = []string{"取消显卡品牌偏好，不再要求N卡，其他条件保留。另外，如果换成4K会怎样？只是讨论备选，分辨率先不改。"}
	}
	for offset, input := range inputs {
		index := firstIndex + offset
		before := state
		source := schemas.RequirementSource{Kind: "chat", MessageID: fmt.Sprintf("live-smoke-turn-%d", index+1), Quote: input}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		ctx = WithRequirementState(ctx, state, source)
		var delivered string
		var callErr error
		for response, err := range (screeningGuard{LLM: counted}).GenerateContent(ctx, &model.LLMRequest{Model: cfg.Model}, false) {
			if err != nil {
				callErr = err
				break
			}
			if response != nil {
				delivered = screeningText(response.Content)
			}
		}
		cancel()
		if callErr != nil {
			failure = fmt.Sprintf("turn=%d upstream_or_guard_rejected: %s", index+1, safeRequirementLiveError(callErr))
			if strings.HasPrefix(callErr.Error(), "初筛需求更新") {
				failure = fmt.Sprintf("turn=%d guard_rejected: %s", index+1, callErr.Error())
			}
			t.Fatalf("turn=%d Screening 失败，立即停止且不重试: %s", index+1, safeRequirementLiveError(callErr))
		}
		update, err := schemas.DecodeRequirementUpdate([]byte(delivered))
		if err != nil {
			t.Fatalf("turn=%d 增量协议无效", index+1)
		}
		state, err = schemas.ApplyRequirementUpdate(state, update, source)
		if err != nil {
			t.Fatalf("turn=%d 状态归并失败: %v", index+1, err)
		}
		// 持久化序列化之后再投影，下一轮也只使用恢复后的服务端状态。
		stored, _ := json.Marshal(state)
		if err := json.Unmarshal(stored, &state); err != nil {
			t.Fatal(err)
		}
		raw, readiness, err := schemas.RequirementStateSpec(state)
		turns = append(turns, turnEvidence{Input: input, State: state, Spec: raw})
		if err != nil || len(readiness.MissingFields) > 0 || schemas.RequirementStateQuestions(state) != "" {
			t.Fatalf("turn=%d 已知需求仍被追问: readiness.MissingFields=%v err=%v", index+1, readiness.MissingFields, err)
		}
		spec, err := schemas.DecodeRequirementSpec(raw)
		if err != nil {
			t.Fatal(err)
		}
		budget := 6000
		if index == 0 {
			budget = 8000
		}
		if spec.BudgetCNY != budget || spec.UseCase.Type != schemas.UseCaseGaming || spec.UseCase.Resolution != schemas.Resolution2K || spec.NoisePref != schemas.NoisePrefSilent || spec.ConstraintStrengths["noise_pref"] != "prefer" || !strings.Contains(string(spec.RequirementDetails["recipient"]), "朋友") {
			t.Fatalf("turn=%d 当前需求/强度/装机对象错误", index+1)
		}
		for _, key := range []string{"budget_flex", "budget_basis", "brand_pref.cpu", "size_pref", "appearance"} {
			if state.Fields[key].Status != "unknown" {
				t.Fatalf("turn=%d 未说明字段 %s 被推断为用户事实", index+1, key)
			}
		}
		for _, change := range state.Changes {
			if change.Source.MessageID != source.MessageID || change.Source.Kind != "chat" || !strings.Contains(input, change.Source.Quote) {
				t.Fatalf("turn=%d 本轮变化来源错误", index+1)
			}
		}
		if index > 0 {
			for _, key := range []string{"noise_pref", "use_case.type", "use_case.resolution", "recipient"} {
				if !reflect.DeepEqual(before.Fields[key], state.Fields[key]) {
					t.Fatalf("turn=%d 未变字段 %s 或其来源被覆盖", index+1, key)
				}
			}
		}
		if index < 2 && (spec.BrandPref.GPU != schemas.GPUBrandNvidia || spec.ConstraintStrengths["brand_pref.gpu"] != "prefer") {
			t.Fatalf("turn=%d N卡软偏好丢失或变成硬条件", index+1)
		}
		if index >= 2 && (state.Fields["brand_pref.gpu"].Status != "removed" || spec.BrandPref.GPU != schemas.GPUBrandAny || spec.ConstraintStrengths["brand_pref.gpu"] != "") {
			t.Fatalf("turn=%d 已撤销的N卡偏好仍生效", index+1)
		}
		if index >= 2 && (strings.Contains(spec.Notes, "N卡") || strings.Contains(strings.ToLower(spec.Notes), "nvidia") || strings.Contains(spec.Notes, "英伟达")) {
			t.Fatalf("turn=%d 已撤销品牌在补充说明中残留", index+1)
		}
		if index == 3 {
			found := false
			for _, alternative := range state.Alternatives {
				found = found || alternative.Field == "use_case.resolution" && string(alternative.Value) == `"4K"` && alternative.Source.MessageID == source.MessageID
			}
			if !found {
				t.Fatal("4K备选没有独立记录")
			}
		}
		t.Logf("turn=%d assertions=passed", index+1)
	}
	if len(counted.calls) != requirementLiveCallLimit {
		t.Fatalf("实际调用数=%d，应为每轮一次", len(counted.calls))
	}
	completed = true
}
