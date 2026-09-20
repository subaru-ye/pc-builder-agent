package modelprovider

import (
	"strings"
	"testing"
)

var modelEnvKeys = []string{
	"SCREENING_PROVIDER", "SCREENING_MODEL", "SCREENING_API_KEY", "SCREENING_BASE_URL", "SCREENING_REASONING_EFFORT", "SCREENING_MODEL_TIMEOUT",
	"BUILDER_PROVIDER", "BUILDER_MODEL", "BUILDER_API_KEY", "BUILDER_BASE_URL", "BUILDER_REASONING_EFFORT", "BUILDER_MODEL_TIMEOUT",
	"EMBEDDING_PROVIDER", "EMBEDDING_MODEL", "EMBEDDING_API_KEY", "EMBEDDING_BASE_URL", "EMBEDDING_MODEL_TIMEOUT",
	"DASHSCOPE_API_KEY", "DASHSCOPE_BASE_URL", "MIMO_API_KEY", "MIMO_BASE_URL",
	"BAILIAN_SESSION_CACHE", "MODEL_MAX_RETRIES",
}

func cleanModelEnv(t *testing.T) {
	t.Helper()
	for _, key := range modelEnvKeys {
		t.Setenv(key, "")
	}
}

func TestLoadBailianDefaultsAndRoleOverrides(t *testing.T) {
	cleanModelEnv(t)
	t.Setenv("DASHSCOPE_API_KEY", "provider-key")
	cfg, err := Load(RoleScreening)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != ProviderBailian || cfg.Model != DefaultScreeningModel ||
		cfg.ReasoningEffort != "none" || !cfg.SessionCache || cfg.MaxRetries != 0 {
		t.Fatalf("screening defaults=%+v", cfg.Redacted())
	}

	t.Setenv("SCREENING_API_KEY", "role-key")
	t.Setenv("SCREENING_BASE_URL", "https://role.example/v1")
	t.Setenv("SCREENING_MODEL", "role-model")
	cfg, err = Load(RoleScreening)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "role-key" || cfg.BaseURL != "https://role.example/v1" || cfg.Model != "role-model" {
		t.Fatalf("角色配置未优先:%+v", cfg.Redacted())
	}
}

func TestLoadPinnedBailianBuilderReasoningEffort(t *testing.T) {
	cleanModelEnv(t)
	t.Setenv("DASHSCOPE_API_KEY", "provider-key")
	cfg, err := Load(RoleBuilder)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != DefaultBuilderModel || cfg.ReasoningEffort != "none" {
		t.Fatalf("builder defaults=%+v", cfg.Redacted())
	}
	// 2026-09-20 起业务空间端点接受 low（旧网关 400 的守卫已随证据移除），
	// 显式提档不再被静态拒绝。
	t.Setenv("BUILDER_REASONING_EFFORT", "low")
	cfg, err = Load(RoleBuilder)
	if err != nil || cfg.ReasoningEffort != "low" {
		t.Fatalf("explicit low must load: %+v %v", cfg.Redacted(), err)
	}
}

func TestLoadMiMoAndGenericRequireExplicitModels(t *testing.T) {
	cleanModelEnv(t)
	t.Setenv("MIMO_API_KEY", "mimo-key")
	t.Setenv("BUILDER_PROVIDER", "mimo")
	if _, err := Load(RoleBuilder); err == nil || !strings.Contains(err.Error(), "模型 Code") {
		t.Fatalf("MiMo 未指定模型应失败,got %v", err)
	}
	t.Setenv("BUILDER_MODEL", "mimo-v2.5")
	cfg, err := Load(RoleBuilder)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReasoningEffort != "none" || cfg.BaseURL != DefaultMiMoBaseURL {
		t.Fatalf("MiMo defaults=%+v", cfg.Redacted())
	}

	cleanModelEnv(t)
	t.Setenv("SCREENING_PROVIDER", "openai_compatible")
	t.Setenv("SCREENING_API_KEY", "generic-key")
	if _, err := Load(RoleScreening); err == nil {
		t.Fatal("generic 缺少 base/model 应失败")
	}
}

func TestLoadRejectsUnsupportedAndUnsafeConfigurations(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T)
		role  Role
		want  string
	}{
		{name: "mimo embedding", role: RoleEmbedding, want: "不受支持", setup: func(t *testing.T) {
			t.Setenv("EMBEDDING_PROVIDER", "mimo")
		}},
		{name: "mimo builder reasoning", role: RoleBuilder, want: "只允许", setup: func(t *testing.T) {
			t.Setenv("BUILDER_PROVIDER", "mimo")
			t.Setenv("BUILDER_MODEL", "mimo-v2.5")
			t.Setenv("MIMO_API_KEY", "key")
			t.Setenv("BUILDER_REASONING_EFFORT", "low")
		}},
		{name: "retry invalid", role: RoleScreening, want: "MODEL_MAX_RETRIES", setup: func(t *testing.T) {
			t.Setenv("DASHSCOPE_API_KEY", "key")
			t.Setenv("MODEL_MAX_RETRIES", "2")
		}},
		{name: "cache invalid", role: RoleScreening, want: "BAILIAN_SESSION_CACHE", setup: func(t *testing.T) {
			t.Setenv("DASHSCOPE_API_KEY", "key")
			t.Setenv("BAILIAN_SESSION_CACHE", "sometimes")
		}},
		{name: "plaintext remote", role: RoleScreening, want: "HTTPS", setup: func(t *testing.T) {
			t.Setenv("DASHSCOPE_API_KEY", "key")
			t.Setenv("SCREENING_BASE_URL", "http://models.example/v1")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cleanModelEnv(t)
			tc.setup(t)
			_, err := Load(tc.role)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want contains %q", err, tc.want)
			}
		})
	}
}

func TestCacheIdentitySeparatesProviderHostModelAndDimensions(t *testing.T) {
	base := Config{Provider: ProviderBailian, BaseURL: "https://a.example/v1", Model: "embed", Dimensions: 1024}
	values := []string{
		base.CacheIdentity(),
		(Config{Provider: ProviderOpenAICompatible, BaseURL: base.BaseURL, Model: base.Model, Dimensions: base.Dimensions}).CacheIdentity(),
		(Config{Provider: base.Provider, BaseURL: "https://b.example/v1", Model: base.Model, Dimensions: base.Dimensions}).CacheIdentity(),
		(Config{Provider: base.Provider, BaseURL: base.BaseURL, Model: "other", Dimensions: base.Dimensions}).CacheIdentity(),
		(Config{Provider: base.Provider, BaseURL: base.BaseURL, Model: base.Model, Dimensions: 1536}).CacheIdentity(),
	}
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			t.Fatalf("cache identity collision: %q", value)
		}
		seen[value] = true
	}
}
