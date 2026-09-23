package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestLegacyCannotSilentlySoftenDynamicRequirementStrengths(t *testing.T) {
	for _, draft := range []string{draftJSON, "", "模型正在等待"} {
		eval := &fakeEval{res: passResult()}
		v := decide(context.Background(), eval, draft, "", 1, &changeCtx{ActiveSpec: json.RawMessage(`{"schema_version": 2, "configuration_scope": ["tower"],"budget_cny":8000,"use_case":{"type":"general"},"noise_pref":"silent","constraint_strengths":{"noise_pref":"must"}}`)})
		if v.deliver || !v.escalate || !strings.Contains(v.message, "legacy 尚不支持") || eval.gotSel.CPU != "" {
			t.Fatalf("legacy softened or evaluated unsupported constraints: %+v", v)
		}
	}
}
