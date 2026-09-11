package pipeline

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// Real user wording; corrected operations are an explicit offline oracle, not a
// newly recorded model response. Tests the production single-call state pipeline.
func TestWorkloadCorrectionReusesStableField(t *testing.T) {
	state := schemas.NewRequirementState()
	state = semanticTurn(t, state, "预算 7000，主要剪2k清晰度的视频", `{"operations":[{"op":"set","field":"budget_cny","value":7000,"strength":"must","kind":"constraint","evidence":"stated","quote":"预算 7000"},{"op":"set","field":"free.workload_resolution","value":"2K清晰度视频素材","strength":"must","kind":"fact","evidence":"stated","quote":"主要剪2k清晰度的视频"}]}`)
	before, _ := json.Marshal(state)
	state = semanticTurn(t, state, "素材分辨率大概1080p吧，游戏只是玩玩英雄联盟，没有太高的要求", `{"operations":[{"op":"set","field":"free.workload_resolution","value":"大约1080p视频素材","strength":"must","kind":"fact","evidence":"stated","quote":"素材分辨率大概1080p吧"},{"op":"set","field":"free.game_workload","value":"英雄联盟，要求不高","strength":"must","kind":"fact","evidence":"stated","quote":"游戏只是玩玩英雄联盟，没有太高的要求"}]}`)
	if strings.Contains(string(state.Fields["free.workload_resolution"].Value), "2K") || !strings.Contains(string(state.Fields["free.workload_resolution"].Value), "1080p") || string(state.Fields["budget_cny"].Value) != "7000" || state.Fields["use_case.resolution"].Status != "unknown" || state.Fields["notes"].Status != "unknown" {
		t.Fatalf("incorrect effective state %+v", state.Fields)
	}
	if !strings.Contains(string(before), "2K") || len(state.History) != 4 {
		t.Fatal("source history lost")
	}
	state = semanticTurn(t, state, "素材分辨率还没定，先别限制", `{"operations":[{"op":"remove","field":"free.workload_resolution","evidence":"stated","quote":"素材分辨率还没定，先别限制"}]}`)
	state = semanticTurn(t, state, "预算改8000", `{"operations":[{"op":"set","field":"budget_cny","value":8000,"evidence":"stated","quote":"预算改8000"}]}`)
	if state.Fields["free.workload_resolution"].Status != "removed" || state.Fields["free.game_workload"].Status != "active" {
		t.Fatal("revived removed workload or erased another fact")
	}
}

func TestWorkloadDuplicateCleanupIsAtomic(t *testing.T) {
	state := schemas.NewRequirementState()
	state.Fields["free.workload_resolution"] = schemas.RequirementField{Status: "active", Kind: "fact", Value: json.RawMessage(`"2K素材"`)}
	state.Fields["notes"] = schemas.RequirementField{Status: "active", Kind: "fact", Value: json.RawMessage(`"2K素材；英雄联盟"`)}
	state = semanticTurn(t, state, "素材改1080p，其他不变", `{"operations":[{"op":"set","field":"free.workload_resolution","value":"1080p素材","kind":"fact","evidence":"stated","quote":"素材改1080p"},{"op":"set","field":"notes","value":"英雄联盟","kind":"fact","evidence":"stated","quote":"素材改1080p，其他不变"}]}`)
	if string(state.Fields["notes"].Value) != `"英雄联盟"` || len(state.Changes) != 2 || state.Changes[0].Revision != state.Changes[1].Revision {
		t.Fatalf("duplicate cleanup not atomic %+v", state)
	}
}
