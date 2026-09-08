package modelprovider

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/http"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/openaimodel"

	"github.com/subaru-ye/pc-builder-agent/internal/embedding"
	"github.com/subaru-ye/pc-builder-agent/internal/evalmetrics"
	"github.com/subaru-ye/pc-builder-agent/internal/upstream"
)

// NewChat 创建带供应商参数、零自动重试、错误分类和脱敏指标的 ADK 模型。
// 配置了 *_MODEL_CHAIN 时返回额度降级链(chain.go):候选按序自动切换。
func NewChat(ctx context.Context, cfg Config, component string) (model.LLM, error) {
	if cfg.Role != RoleScreening && cfg.Role != RoleBuilder {
		return nil, fmt.Errorf("modelprovider: %s 不是 chat 角色", cfg.Role)
	}
	if len(cfg.ModelChain) > 0 {
		chain := &chainModel{role: cfg.Role, candidates: make([]*chainCandidate, 0, len(cfg.ModelChain))}
		for _, name := range cfg.ModelChain {
			memberCfg := cfg
			memberCfg.Model = name
			candidate := &chainCandidate{name: name}
			candidate.build = func(ctx context.Context) (model.LLM, error) {
				return buildChat(ctx, memberCfg, component)
			}
			chain.candidates = append(chain.candidates, candidate)
		}
		return evalmetrics.WrapModelWithMeta(component, chain, string(cfg.Provider), string(cfg.Role)), nil
	}
	inner, err := buildChat(ctx, cfg, component)
	if err != nil {
		return nil, err
	}
	return evalmetrics.WrapModelWithMeta(component, inner, string(cfg.Provider), string(cfg.Role)), nil
}

// buildChat 装配单个模型名的已分类 ADK 模型(不含指标包装)。
func buildChat(ctx context.Context, cfg Config, component string) (model.LLM, error) {
	hc := &http.Client{Timeout: cfg.Timeout}
	opts := []option.RequestOption{option.WithMaxRetries(cfg.MaxRetries)}
	if cfg.ReasoningEffort != "" {
		opts = append(opts, option.WithJSONSet("reasoning.effort", cfg.ReasoningEffort))
	}
	if cfg.Provider == ProviderBailian && cfg.SessionCache {
		opts = append(opts, option.WithHeader("x-dashscope-session-cache", "enable"))
	}
	if cfg.Provider == ProviderMiMo {
		// MiMo Responses 官方使用 api-key；OpenAI SDK 自带的 Authorization
		// 仍保留以兼容网关，但服务端鉴权以此头为准。
		opts = append(opts, option.WithHeader("api-key", cfg.APIKey))
	}
	inner, err := openaimodel.NewModel(ctx, cfg.Model, &openaimodel.ClientConfig{
		APIKey: cfg.APIKey, BaseURL: cfg.BaseURL, HTTPClient: hc, Options: opts,
	})
	if err != nil {
		return nil, err
	}
	return &classifiedModel{inner: inner, provider: cfg.Provider, role: cfg.Role}, nil
}

// NewEmbedding 创建通用 /embeddings 客户端；MiMo 已在 Load 阶段拒绝。
func NewEmbedding(cfg Config) (*embedding.Client, error) {
	if cfg.Role != RoleEmbedding {
		return nil, fmt.Errorf("modelprovider: %s 不是 embedding 角色", cfg.Role)
	}
	return embedding.NewClientWithOptions(embedding.Options{
		BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Model: cfg.Model,
		Dimensions: cfg.Dimensions, Timeout: cfg.Timeout,
		Provider: string(cfg.Provider), Role: string(cfg.Role),
	}), nil
}

type classifiedModel struct {
	inner    model.LLM
	provider Provider
	role     Role
}

func (m *classifiedModel) Name() string { return m.inner.Name() }

func (m *classifiedModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.inner.GenerateContent(ctx, req, stream)(func(resp *model.LLMResponse, err error) bool {
			if err == nil {
				if resp == nil || (resp.ErrorCode == "" && resp.ErrorMessage == "") {
					return yield(resp, nil)
				}
				kind := upstream.Classify(0, resp.ErrorCode, errors.New("provider returned response error"))
				return yield(nil, upstream.New(string(m.provider), string(m.role), kind, 0,
					resp.ErrorCode, errors.New("provider returned response error")))
			}
			status, code := 0, ""
			var apiErr *openai.Error
			if errors.As(err, &apiErr) {
				status, code = apiErr.StatusCode, apiErr.Code
			}
			wrapped := upstream.New(string(m.provider), string(m.role), upstream.Classify(status, code, err), status, code, err)
			return yield(resp, wrapped)
		})
	}
}
