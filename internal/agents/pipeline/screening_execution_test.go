package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"google.golang.org/adk/v2/model"
)

// Real upgrade wording, corrected offline oracle. Verify actual prompt input,
// not just the reducer: free requirements used to disappear from this view.
func TestScreeningUpgradeHasCurrentConfigurationAndFreeRequirements(t *testing.T) {
	state := schemas.NewRequirementState()
	state.Fields["free.workload_resolution"] = schemas.RequirementField{Status: "active", Kind: "fact", Value: json.RawMessage(`"1080p素材"`)}
	state.Fields["free.removed"] = schemas.RequirementField{Status: "removed"}
	state.Fields["noise_pref"] = schemas.RequirementField{Status: "removed"}
	source := schemas.RequirementSource{Kind: "chat", MessageID: "upgrade", Quote: "把处理器换更好的，预算还很充足啊，其他配件尽量不动"}
	conversation := schemas.ScreeningConversation{CanPlan: true, BuildVersion: 1, BaseDraft: json.RawMessage(`{"selection":{"cpu":"cpu-r5-5600","motherboard":"mb-msi-b550m-pro-vdh-wifi"}}`), Quote: json.RawMessage(`{"total_cny":"4579.90"}`), LastAssistant: "可在现有配置基础上继续升级。"}
	m := &stateProtocolModel{output: `{"next_action":"plan","operations":[{"op":"set","field":"priority","value":["cpu"],"strength":"prefer","evidence":"stated","quote":"把处理器换更好的"},{"op":"set","field":"free.preserve_other_parts","value":"其他配件尽量不动","kind":"constraint","strength":"prefer","evidence":"stated","quote":"其他配件尽量不动"}]}`}
	ctx := WithRequirementState(context.Background(), state, source, conversation)
	var update schemas.RequirementUpdate
	for response, err := range (screeningGuard{LLM: m}).GenerateContent(ctx, &model.LLMRequest{}, false) {
		if err != nil {
			t.Fatal(err)
		}
		// 传输外形仍是 legacy turn(含 reply/next_action);领域更新取剥离结果。
		turn, err := DecodeLegacyRequirementTurn([]byte(screeningText(response.Content)))
		if err != nil {
			t.Fatal(err)
		}
		update = turn.Update()
	}
	input := screeningText(m.request.Contents[0])
	for _, expected := range []string{`"can_plan":true`, "cpu-r5-5600", "mb-msi-b550m-pro-vdh-wifi", "4579.90", "1080p素材", "free.removed", "可在现有配置基础上继续升级"} {
		if !strings.Contains(input, expected) {
			t.Fatalf("missing context %s in %s", expected, input)
		}
	}
	next, err := schemas.ApplyRequirementUpdate(state, update, source)
	if err != nil {
		t.Fatal(err)
	}
	if m.calls != 1 || string(next.Fields["priority"].Value) != `["cpu"]` || next.Fields["free.preserve_other_parts"].Strength != "prefer" || next.Fields["noise_pref"].Status != "removed" || next.Fields["owned_parts"].Status != "unknown" {
		t.Fatalf("upgrade semantics changed: %+v", next)
	}
}
