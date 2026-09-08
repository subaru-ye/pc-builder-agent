// Package modelprovider 统一装配 screening、builder 与 embedding 使用的模型端点。
// 支持百炼、MiMo 和实现 OpenAI Responses API 的通用兼容服务;不做跨供应商自动回退。
// chat 角色可选配置额度降级链(*_MODEL_CHAIN):链内候选按序自动切换,见 chain.go。
package modelprovider

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/embedding"
)

type Provider string
type Role string

const (
	ProviderBailian          Provider = "bailian"
	ProviderMiMo             Provider = "mimo"
	ProviderOpenAICompatible Provider = "openai_compatible"

	RoleScreening Role = "screening"
	RoleBuilder   Role = "builder"
	RoleEmbedding Role = "embedding"

	DefaultBailianBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	DefaultMiMoBaseURL    = "https://api.xiaomimimo.com/v1"
	DefaultScreeningModel = "qwen3.7-plus"
	DefaultBuilderModel   = "qwen3.7-plus-2026-05-26"
)

// Config 是一个模型角色解析完成后的独立连接配置。
type Config struct {
	Provider        Provider
	Role            Role
	APIKey          string
	BaseURL         string
	Model           string
	ReasoningEffort string
	SessionCache    bool
	MaxRetries      int
	Timeout         time.Duration
	Dimensions      int
	// ModelChain 是可选的额度降级链(仅 chat 角色):按序尝试,候选 403/404
	// 或配额耗尽时进程内自动切下一个;非空时 Model 恒为链首。
	ModelChain []string
}

// ModelDescription 返回可进报告/日志的模型口径描述;链模式标出链首与候选数。
func (c Config) ModelDescription() string {
	if len(c.ModelChain) == 0 {
		return string(c.Provider) + "/" + c.Model
	}
	return fmt.Sprintf("%s/chain:%s(共 %d 个候选)", c.Provider, c.ModelChain[0], len(c.ModelChain))
}

// Redacted 返回可安全记录的运行配置；凭据永远不包含在结果中。
func (c Config) Redacted() map[string]any {
	return map[string]any{
		"provider": c.Provider, "role": c.Role, "model": c.Model,
		"base_host": endpointHost(c.BaseURL), "reasoning_effort": c.ReasoningEffort,
		"session_cache": c.SessionCache, "max_retries": c.MaxRetries,
	}
}

// Load 从环境变量解析指定角色。旧的百炼 DASHSCOPE_* 配置继续兼容。
func Load(role Role) (Config, error) {
	prefix, err := rolePrefix(role)
	if err != nil {
		return Config{}, err
	}
	provider := Provider(strings.TrimSpace(os.Getenv(prefix + "_PROVIDER")))
	if provider == "" {
		provider = ProviderBailian
	}
	if !validProvider(provider) {
		return Config{}, fmt.Errorf("%s_PROVIDER=%q 无效:须为 bailian|mimo|openai_compatible", prefix, provider)
	}
	if role == RoleEmbedding && provider == ProviderMiMo {
		return Config{}, fmt.Errorf("EMBEDDING_PROVIDER=mimo 不受支持:MiMo 预设不提供本项目所需的 1024 维 embedding")
	}

	maxRetries, err := loadRetries()
	if err != nil {
		return Config{}, err
	}
	timeout, err := roleTimeout(role)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{Provider: provider, Role: role, MaxRetries: maxRetries, Timeout: timeout}
	cfg.APIKey = firstNonEmpty(os.Getenv(prefix+"_API_KEY"), providerAPIKey(provider))
	cfg.BaseURL = strings.TrimRight(firstNonEmpty(os.Getenv(prefix+"_BASE_URL"), providerBaseURL(provider)), "/")
	cfg.Model = strings.TrimSpace(os.Getenv(prefix + "_MODEL"))
	if cfg.Model == "" && provider == ProviderBailian {
		switch role {
		case RoleScreening:
			cfg.Model = DefaultScreeningModel
		case RoleBuilder:
			cfg.Model = DefaultBuilderModel
		case RoleEmbedding:
			cfg.Model = embedding.DefaultModel
		}
	}
	// 额度降级链(P13):仅 chat 角色;链生效时 Model 恒为链首,报表与指标
	// 以链首为口径,实际服务者由 chain 在运行时决定并记日志。
	if role != RoleEmbedding {
		if chain := parseModelChain(os.Getenv(prefix + "_MODEL_CHAIN")); len(chain) > 0 {
			cfg.ModelChain = chain
			cfg.Model = chain[0]
		}
	}
	if cfg.APIKey == "" {
		return Config{}, fmt.Errorf("%s 缺少 API Key:设置 %s_API_KEY 或对应供应商 Key", role, prefix)
	}
	if cfg.BaseURL == "" {
		return Config{}, fmt.Errorf("%s 缺少 Base URL:openai_compatible 必须设置 %s_BASE_URL", role, prefix)
	}
	if cfg.Model == "" {
		return Config{}, fmt.Errorf("%s 缺少模型 Code:设置 %s_MODEL", role, prefix)
	}
	if err := validateBaseURL(cfg.BaseURL); err != nil {
		return Config{}, fmt.Errorf("%s_BASE_URL: %w", prefix, err)
	}

	if role != RoleEmbedding {
		cfg.ReasoningEffort = strings.TrimSpace(os.Getenv(prefix + "_REASONING_EFFORT"))
		if cfg.ReasoningEffort == "" {
			switch {
			case provider == ProviderBailian && role == RoleScreening:
				cfg.ReasoningEffort = "none"
			case provider == ProviderBailian && role == RoleBuilder && len(cfg.ModelChain) > 0:
				// 链成员是异构模型,统一 none 是跨模型最稳口径;需要别的档位
				// 用 BUILDER_REASONING_EFFORT 显式覆盖。
				cfg.ReasoningEffort = "none"
			case provider == ProviderBailian && role == RoleBuilder && cfg.Model == DefaultBuilderModel:
				// 2026-08-12 真实 Responses 检查:该固定版本传 low 会返回
				// 400 InvalidParameter，none 的 function calling 正常。
				cfg.ReasoningEffort = "none"
			case provider == ProviderBailian && role == RoleBuilder:
				cfg.ReasoningEffort = "low"
			case provider == ProviderMiMo:
				cfg.ReasoningEffort = "none"
			}
		}
		if !validReasoning(cfg.ReasoningEffort) {
			return Config{}, fmt.Errorf("%s_REASONING_EFFORT=%q 无效", prefix, cfg.ReasoningEffort)
		}
		if provider == ProviderMiMo && role == RoleBuilder && cfg.ReasoningEffort != "none" {
			return Config{}, fmt.Errorf("MiMo builder 当前只允许 reasoning.effort=none:ADK 无法无损回传工具轮次 reasoning history")
		}
		if provider == ProviderBailian && role == RoleBuilder && cfg.Model == DefaultBuilderModel && cfg.ReasoningEffort != "none" {
			return Config{}, fmt.Errorf("%s 当前 Responses 实测只允许 reasoning.effort=none", cfg.Model)
		}
	}
	if role == RoleEmbedding {
		cfg.Dimensions = 1024
	}
	if provider == ProviderBailian {
		cfg.SessionCache, err = envBool("BAILIAN_SESSION_CACHE", true)
		if err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

// CacheIdentity 是 embedding Redis key 使用的供应商/端点/模型/维度身份，不含凭据。
func (c Config) CacheIdentity() string {
	return fmt.Sprintf("%s:%s:%s:%d", c.Provider, endpointHost(c.BaseURL), c.Model, c.Dimensions)
}

func endpointHost(baseURL string) string {
	u, _ := url.Parse(baseURL)
	return strings.ToLower(u.Host)
}

func rolePrefix(role Role) (string, error) {
	switch role {
	case RoleScreening:
		return "SCREENING", nil
	case RoleBuilder:
		return "BUILDER", nil
	case RoleEmbedding:
		return "EMBEDDING", nil
	default:
		return "", fmt.Errorf("未知模型角色 %q", role)
	}
}

func validProvider(provider Provider) bool {
	return provider == ProviderBailian || provider == ProviderMiMo || provider == ProviderOpenAICompatible
}

func providerAPIKey(provider Provider) string {
	switch provider {
	case ProviderBailian:
		return os.Getenv("DASHSCOPE_API_KEY")
	case ProviderMiMo:
		return os.Getenv("MIMO_API_KEY")
	default:
		return ""
	}
}

func providerBaseURL(provider Provider) string {
	switch provider {
	case ProviderBailian:
		return firstNonEmpty(os.Getenv("DASHSCOPE_BASE_URL"), DefaultBailianBaseURL)
	case ProviderMiMo:
		return firstNonEmpty(os.Getenv("MIMO_BASE_URL"), DefaultMiMoBaseURL)
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func loadRetries() (int, error) {
	value := strings.TrimSpace(os.Getenv("MODEL_MAX_RETRIES"))
	if value == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 || n > 1 {
		return 0, fmt.Errorf("MODEL_MAX_RETRIES=%q 无效:须为 0 或 1", value)
	}
	return n, nil
}

func roleTimeout(role Role) (time.Duration, error) {
	defaultTimeout := 60 * time.Second
	if role == RoleBuilder {
		defaultTimeout = 3 * time.Minute
	}
	prefix, _ := rolePrefix(role)
	value := strings.TrimSpace(os.Getenv(prefix + "_MODEL_TIMEOUT"))
	if value == "" {
		return defaultTimeout, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s_MODEL_TIMEOUT=%q 无效:须为正 duration", prefix, value)
	}
	return d, nil
}

func validateBaseURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("须为有效 http(s) URL")
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
		return fmt.Errorf("非本机端点必须使用 HTTPS")
	}
	return nil
}

func validReasoning(value string) bool {
	switch value {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func envBool(name string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s=%q 无效:须为 true 或 false", name, value)
	}
	return parsed, nil
}
