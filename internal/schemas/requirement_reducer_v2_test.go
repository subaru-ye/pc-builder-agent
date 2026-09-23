package schemas

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func chatSource(quote string) RequirementSource {
	return RequirementSource{Kind: "chat", MessageID: "m", Quote: quote}
}

func TestReducerEmptyBatchIsByteLevelNoOp(t *testing.T) {
	state := gamingReadyState()
	state.Revision = 5
	state.Changes = []RequirementChange{{Revision: 5, Op: "set", Field: "budget_cny"}}
	state.Observations = []RequirementObservation{{Text: "历史观察", Reason: "背景", Source: RequirementSource{Kind: "chat"}}}
	before, _ := json.Marshal(state)
	next, err := ApplyRequirementUpdate(state, RequirementUpdate{Operations: []RequirementOperation{}}, chatSource("无变更"))
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(next)
	if !bytes.Equal(before, after) {
		t.Fatalf("空批应是字节级 no-op:\n%s\n%s", before, after)
	}
}

func TestReducerBatchIncrementsRevisionOnce(t *testing.T) {
	source := chatSource("预算9000，全部新买")
	next, err := ApplyRequirementUpdate(gamingReadyState(), RequirementUpdate{Operations: []RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage(`9000`), Quote: "预算9000"},
		{Op: "set", Field: "noise_pref", Value: json.RawMessage(`"silent"`), Strength: "prefer", Quote: "全部新买"},
	}}, source)
	if err != nil {
		t.Fatal(err)
	}
	if next.Revision != 1 || len(next.Changes) != 2 {
		t.Fatalf("非空成功批次 revision 只增一次: revision=%d changes=%d", next.Revision, len(next.Changes))
	}
	for _, change := range next.Changes {
		if change.Revision != next.Revision {
			t.Fatalf("同批 changes 必须共用同一 revision: %+v", change)
		}
	}
}

func TestReducerAtomicBatchKeepsInputUnchanged(t *testing.T) {
	state := gamingReadyState()
	state.Revision = 7
	before, _ := json.Marshal(state)
	source := chatSource("打游戏，预算-5")
	_, err := ApplyRequirementUpdate(state, RequirementUpdate{Operations: []RequirementOperation{
		{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"gaming"`), Quote: "打游戏"},
		{Op: "set", Field: "budget_cny", Value: json.RawMessage(`-5`), Quote: "预算-5"},
	}}, source)
	if err == nil {
		t.Fatal("第二个操作非法时整批应拒绝")
	}
	after, _ := json.Marshal(state)
	if !bytes.Equal(before, after) {
		t.Fatal("被拒绝批次修改了输入状态")
	}
}

func TestReducerNormalizesListValues(t *testing.T) {
	source := chatSource("玩CS2和CS2，配件全部新买，还想用旧显卡和旧内存")
	next, err := ApplyRequirementUpdate(NewRequirementState(), RequirementUpdate{Operations: []RequirementOperation{
		{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"gaming"`), Quote: "玩"},
		{Op: "set", Field: "use_case.titles", Value: json.RawMessage(`[" CS2 ","CS2","无畏契约 "]`), Quote: "玩"},
		{Op: "set", Field: "existing_parts", Value: json.RawMessage(`["memory","gpu"]`), Quote: "旧显卡和旧内存"},
	}}, source)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	if err := json.Unmarshal(next.Fields["use_case.titles"].Value, &titles); err != nil ||
		!reflect.DeepEqual(titles, []string{"CS2", "无畏契约"}) {
		t.Fatalf("titles 应 trim 后去重保序: %s", next.Fields["use_case.titles"].Value)
	}
	var existing []Category
	if err := json.Unmarshal(next.Fields["existing_parts"].Value, &existing); err != nil ||
		!reflect.DeepEqual(existing, []Category{CategoryGPU, CategoryMemory}) {
		t.Fatalf("existing_parts 应按领域固定顺序去重: %s", next.Fields["existing_parts"].Value)
	}
}

func TestReducerOwnershipInvariant(t *testing.T) {
	source := chatSource("已有显卡，后来决定全部新买")
	state, err := ApplyRequirementUpdate(NewRequirementState(), RequirementUpdate{Operations: []RequirementOperation{
		{Op: "set", Field: "existing_parts", Value: json.RawMessage(`["gpu"]`), Quote: "已有显卡"},
		{Op: "set", Field: "owned_parts", Value: json.RawMessage(`[{"category":"gpu","model":"RTX 4060"}]`), Quote: "已有显卡"},
	}}, source)
	if err != nil {
		t.Fatal(err)
	}
	state, err = ApplyRequirementUpdate(state, RequirementUpdate{Operations: []RequirementOperation{
		{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`), Quote: "决定全部新买"},
	}}, chatSource("后来决定全部新买"))
	if err != nil {
		t.Fatal(err)
	}
	var owned []OwnedPart
	if state.Fields["owned_parts"].Status != "active" || json.Unmarshal(state.Fields["owned_parts"].Value, &owned) != nil || len(owned) != 0 {
		t.Fatalf("existing 清空后 owned 必须同步清空: %+v", state.Fields["owned_parts"])
	}
	if err := validateOwnershipInvariant(state); err != nil {
		t.Fatalf("清空后不变量应满足: %v", err)
	}
}

func TestProjectionDoesNotMutateInput(t *testing.T) {
	state := gamingReadyState()
	state.Fields["appearance"] = RequirementField{Status: "active", Value: json.RawMessage(`"白色"`), Strength: "prefer", Kind: "constraint"}
	state.Fields["free.workload"] = RequirementField{Status: "active", Value: json.RawMessage(`"4K 素材剪辑"`), Kind: "fact", Strength: "prefer"}
	before, _ := json.Marshal(state)
	raw, readiness, err := RequirementStateSpec(state)
	if err != nil || len(readiness.MissingFields) != 0 || raw == nil {
		t.Fatalf("投影失败: %v %v", readiness.MissingFields, err)
	}
	after, _ := json.Marshal(state)
	if !bytes.Equal(before, after) {
		t.Fatal("投影修改了输入状态")
	}
	spec, err := DecodeRequirementSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(spec.RequirementDetails["appearance"]) != `"白色"` || string(spec.RequirementDetails["free.workload"]) != `"4K 素材剪辑"` {
		t.Fatalf("free.*/appearance 未进入 requirement_details: %+v", spec.RequirementDetails)
	}
	if spec.ConstraintStrengths["free.workload"] != "prefer" || spec.RequirementSemantics["free.workload"] != "fact" {
		t.Fatalf("free.* 强度/语义未随投影: %+v", spec)
	}
	if spec.SchemaVersion != RequirementSpecSchemaVersion || spec.ConfigurationScope[0] != ConfigurationScopeTower {
		t.Fatalf("投影必须注入 v2 与 configuration_scope: %+v", spec)
	}
	// 系统默认由投影注入但不写回状态:对应字段仍为 unknown。
	if state.Fields["budget_flex"].Status != "unknown" || state.Fields["use_case.performance_goal"].Status != "unknown" {
		t.Fatal("系统默认不得写成 active 用户字段")
	}
}
