package schemas

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func activeField(value string) RequirementField {
	return RequirementField{Status: "active", Value: json.RawMessage(value), Strength: "must", Scope: "session",
		Source: &RequirementSource{Kind: "chat", MessageID: "seed", Quote: "seed"}}
}

func gamingReadyState() RequirementState {
	state := NewRequirementState()
	state.Fields["budget_cny"] = activeField(`8000`)
	state.Fields["use_case.type"] = activeField(`"gaming"`)
	state.Fields["use_case.resolution"] = activeField(`"2K"`)
	state.Fields["existing_parts"] = activeField(`[]`)
	return state
}

func defaultFields(defaults []RequirementDefault) []string {
	out := make([]string, 0, len(defaults))
	for _, d := range defaults {
		out = append(out, d.Field)
	}
	return out
}

func TestReadinessGamingDefaultsOrderAndOrigin(t *testing.T) {
	readiness, err := EvaluateRequirementReadiness(gamingReadyState())
	if err != nil {
		t.Fatal(err)
	}
	if readiness.Status != "ready" || !readiness.ConfirmationEligible {
		t.Fatalf("gaming 齐备应 ready: %+v", readiness)
	}
	want := []string{"budget_flex", "size_pref", "noise_pref", "brand_pref.cpu", "brand_pref.gpu", "configuration_scope", "performance_goal"}
	if !reflect.DeepEqual(defaultFields(readiness.EffectiveDefaults), want) {
		t.Fatalf("默认顺序错误: %v", defaultFields(readiness.EffectiveDefaults))
	}
	for _, d := range readiness.EffectiveDefaults {
		if d.Origin != "system_default" {
			t.Fatalf("默认来源必须标 system_default: %+v", d)
		}
	}
	if string(readiness.EffectiveDefaults[0].Value) != "0.1" || string(readiness.EffectiveDefaults[6].Value) != `"balanced"` {
		t.Fatalf("默认值错误: %+v", readiness.EffectiveDefaults)
	}
	// 已有 active 用户值的字段不再列入默认。
	state := gamingReadyState()
	state.Fields["size_pref"] = activeField(`"itx"`)
	state.Fields["use_case.performance_goal"] = activeField(`"fps_first"`)
	readiness, err = EvaluateRequirementReadiness(state)
	if err != nil {
		t.Fatal(err)
	}
	fields := defaultFields(readiness.EffectiveDefaults)
	if strings.Join(fields, ",") != "budget_flex,noise_pref,brand_pref.cpu,brand_pref.gpu,configuration_scope" {
		t.Fatalf("active 用户字段应从默认中排除: %v", fields)
	}
}

func TestReadinessProductivityDefaultsExcludePerformanceGoal(t *testing.T) {
	state := NewRequirementState()
	state.Fields["budget_cny"] = activeField(`8000`)
	state.Fields["use_case.type"] = activeField(`"productivity"`)
	state.Fields["use_case.titles"] = activeField(`["Blender 渲染"]`)
	state.Fields["existing_parts"] = activeField(`[]`)
	readiness, err := EvaluateRequirementReadiness(state)
	if err != nil || readiness.Status != "ready" {
		t.Fatalf("productivity 齐备应 ready: %+v err=%v", readiness, err)
	}
	if got := defaultFields(readiness.EffectiveDefaults); strings.Join(got, ",") != "budget_flex,size_pref,noise_pref,brand_pref.cpu,brand_pref.gpu,configuration_scope" {
		t.Fatalf("非 gaming 不应展开 performance_goal 默认: %v", got)
	}
}

func TestReadinessConditionalRequirements(t *testing.T) {
	cases := []struct {
		name        string
		mutate      func(*RequirementState)
		wantMissing []string
	}{
		{"gaming 缺分辨率", func(s *RequirementState) { s.Fields["use_case.resolution"] = RequirementField{Status: "unknown"} }, []string{"use_case.resolution"}},
		{"gaming any 不满足", func(s *RequirementState) { s.Fields["use_case.resolution"] = activeField(`"any"`) }, []string{"use_case.resolution"}},
		{"productivity 缺软件任务", func(s *RequirementState) {
			s.Fields["use_case.type"] = activeField(`"productivity"`)
			s.Fields["use_case.resolution"] = RequirementField{Status: "unknown"}
		}, []string{"use_case.titles"}},
		{"productivity 空标题不满足", func(s *RequirementState) {
			s.Fields["use_case.type"] = activeField(`"productivity"`)
			s.Fields["use_case.titles"] = activeField(`[]`)
			s.Fields["use_case.resolution"] = RequirementField{Status: "unknown"}
		}, []string{"use_case.titles"}},
		{"撤销必填按缺失", func(s *RequirementState) { s.Fields["budget_cny"] = RequirementField{Status: "removed"} }, []string{"budget_cny"}},
		{"已有件 unknown 不能默认为空", func(s *RequirementState) { s.Fields["existing_parts"] = RequirementField{Status: "unknown"} }, []string{"existing_parts"}},
		{"已有件缺型号与口径", func(s *RequirementState) { s.Fields["existing_parts"] = activeField(`["gpu"]`) },
			[]string{"owned_parts.gpu.model", "budget_basis"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state := gamingReadyState()
			c.mutate(&state)
			readiness, err := EvaluateRequirementReadiness(state)
			if err != nil {
				t.Fatal(err)
			}
			if readiness.ConfirmationEligible || !reflect.DeepEqual(readiness.MissingFields, c.wantMissing) {
				t.Fatalf("missing=%v want=%v", readiness.MissingFields, c.wantMissing)
			}
		})
	}
}

// 条件必填字段(owned_parts)冲突进入 BlockingConflicts,与强度无关,
// 且不再同时以逐品类缺失的形式出现。
func TestReadinessOwnedConflictClassifiedAsBlocking(t *testing.T) {
	state := gamingReadyState()
	state.Fields["existing_parts"] = activeField(`["gpu"]`)
	state.Fields["budget_basis"] = activeField(`"new_purchase"`)
	state.Fields["owned_parts"] = RequirementField{Status: "conflict", Strength: "prefer"}
	readiness, err := EvaluateRequirementReadiness(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(readiness.MissingFields) != 0 || !reflect.DeepEqual(readiness.BlockingConflicts, []string{"owned_parts"}) || readiness.ConfirmationEligible {
		t.Fatalf("owned 条件必填冲突应单列阻塞: %+v", readiness)
	}
	// 追问计划优先指向冲突。
	question, err := NextRequirementQuestion(state)
	if err != nil || question == nil || question.ReasonCode != "requirement_conflict" || !reflect.DeepEqual(question.Fields, []string{"owned_parts"}) {
		t.Fatalf("追问应先解决 owned 冲突: %+v err=%v", question, err)
	}
}

func TestReadinessBlockingConflicts(t *testing.T) {
	// 必填冲突 → blocking,不出现在 missing。
	state := gamingReadyState()
	state.Fields["budget_cny"] = RequirementField{Status: "conflict", Strength: "must"}
	readiness, err := EvaluateRequirementReadiness(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(readiness.MissingFields) != 0 || !reflect.DeepEqual(readiness.BlockingConflicts, []string{"budget_cny"}) || readiness.ConfirmationEligible {
		t.Fatalf("必填冲突应单列阻塞: %+v", readiness)
	}
	// 普通 prefer 软冲突不阻塞、不投影冲突值。
	state = gamingReadyState()
	state.Fields["noise_pref"] = RequirementField{Status: "conflict", Strength: "prefer"}
	readiness, err = EvaluateRequirementReadiness(state)
	if err != nil || !readiness.ConfirmationEligible || len(readiness.BlockingConflicts) != 0 {
		t.Fatalf("prefer 冲突不应阻塞: %+v err=%v", readiness, err)
	}
	// must 约束冲突(即使是非必填字段)阻塞确认。
	state.Fields["noise_pref"] = RequirementField{Status: "conflict", Strength: "must"}
	readiness, err = EvaluateRequirementReadiness(state)
	if err != nil || readiness.ConfirmationEligible || !reflect.DeepEqual(readiness.BlockingConflicts, []string{"noise_pref"}) {
		t.Fatalf("must 冲突应阻塞: %+v err=%v", readiness, err)
	}
}

func unsupportedObservationState(reason string) RequirementState {
	state := gamingReadyState()
	state.Observations = []RequirementObservation{{
		Field: "notes", Text: "还要配显示器", Reason: reason,
		Source: RequirementSource{Kind: "chat", MessageID: "m1", Quote: "还要配显示器"},
	}}
	return state
}

func TestReadinessUnsupportedCapabilityBlocks(t *testing.T) {
	readiness, err := EvaluateRequirementReadiness(unsupportedObservationState("unsupported_capability:monitor"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(readiness.UnsupportedCapabilities, []string{"monitor"}) || readiness.ConfirmationEligible {
		t.Fatalf("结构化 unsupported 应单列阻塞: %+v", readiness)
	}
	if len(readiness.MissingFields) != 0 || len(readiness.BlockingConflicts) != 0 {
		t.Fatalf("unsupported 不得伪装成 missing/conflict: %+v", readiness)
	}
	// 自由文本 reason 不做关键词推断。
	readiness, err = EvaluateRequirementReadiness(unsupportedObservationState("用户提到想要显示器"))
	if err != nil {
		t.Fatal(err)
	}
	if len(readiness.UnsupportedCapabilities) != 0 || !readiness.ConfirmationEligible {
		t.Fatalf("自由文本不得推断 capability: %+v", readiness)
	}
	// 无关字段更新不得解除阻塞:补充 notes(如"给朋友装机")后 monitor 仍阻塞。
	state := unsupportedObservationState("unsupported_capability:monitor")
	state, err = ApplyRequirementUpdate(state, RequirementUpdate{
		Operations: []RequirementOperation{{Op: "set", Field: "notes", Value: json.RawMessage(`"给朋友装机"`), Quote: "给朋友装机"}},
	}, RequirementSource{Kind: "chat", MessageID: "m2", Quote: "给朋友装机"})
	if err != nil {
		t.Fatal(err)
	}
	readiness, err = EvaluateRequirementReadiness(state)
	if err != nil || !reflect.DeepEqual(readiness.UnsupportedCapabilities, []string{"monitor"}) || readiness.ConfirmationEligible {
		t.Fatalf("无关 notes 更新解除了 unsupported 阻塞: %+v err=%v", readiness, err)
	}
	// 只有能力专属、绑定本轮证据的撤销操作能解除,且不影响其他 capability。
	state.Observations = append(state.Observations, RequirementObservation{
		Field: "notes", Text: "还要配键盘", Reason: "unsupported_capability:keyboard",
		Source: RequirementSource{Kind: "chat", MessageID: "m1", Quote: "还要配键盘"}})
	state, err = ApplyRequirementUpdate(state, RequirementUpdate{
		Operations: []RequirementOperation{{Op: "remove", Field: "unsupported.monitor", Quote: "显示器先不用了"}},
	}, RequirementSource{Kind: "chat", MessageID: "m3", Quote: "显示器先不用了"})
	if err != nil {
		t.Fatal(err)
	}
	readiness, err = EvaluateRequirementReadiness(state)
	if err != nil || !reflect.DeepEqual(readiness.UnsupportedCapabilities, []string{"keyboard"}) || readiness.ConfirmationEligible {
		t.Fatalf("撤销 monitor 后 keyboard 应保持阻塞: %+v err=%v", readiness, err)
	}
	state, err = ApplyRequirementUpdate(state, RequirementUpdate{
		Operations: []RequirementOperation{{Op: "remove", Field: "unsupported.keyboard", Quote: "键盘也有了"}},
	}, RequirementSource{Kind: "chat", MessageID: "m4", Quote: "键盘也有了"})
	if err != nil {
		t.Fatal(err)
	}
	readiness, err = EvaluateRequirementReadiness(state)
	if err != nil || len(readiness.UnsupportedCapabilities) != 0 || !readiness.ConfirmationEligible {
		t.Fatalf("全部 capability 撤销后应解除阻塞: %+v err=%v", readiness, err)
	}
}

func TestReducerRejectsInvalidUnsupportedReasonCode(t *testing.T) {
	source := RequirementSource{Kind: "chat", MessageID: "m1", Quote: "还想配个摄像头"}
	_, err := ApplyRequirementUpdate(NewRequirementState(), RequirementUpdate{
		Observations: []RequirementObservationInput{{Quote: "还想配个摄像头", Reason: "unsupported_capability:webcam"}},
	}, source)
	if err == nil || !strings.Contains(err.Error(), "unsupported_capability") {
		t.Fatalf("未知 capability 名应稳定拒绝: %v", err)
	}
}

func TestReadinessByteStableAndSchemaError(t *testing.T) {
	state := gamingReadyState()
	state.Fields["budget_cny"] = RequirementField{Status: "conflict", Strength: "must"}
	state.Observations = unsupportedObservationState("unsupported_capability:monitor").Observations
	first, err := EvaluateRequirementReadiness(state)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EvaluateRequirementReadiness(state)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("同状态重复求值不稳定:\n%+v\n%+v", first, second)
	}
	// 非法 active 值返回 schema error,不降级 unknown。
	state = gamingReadyState()
	state.Fields["budget_cny"] = activeField(`-1`)
	if _, err := EvaluateRequirementReadiness(state); err == nil {
		t.Fatal("非法 active 值应返回 schema error")
	}
	state = gamingReadyState()
	state.Fields["budget_flex"] = activeField(`0.5`)
	if _, err := EvaluateRequirementReadiness(state); err == nil {
		t.Fatal("budget_flex 超上限应返回 schema error")
	}
}

func TestNextRequirementQuestionPriority(t *testing.T) {
	if q, err := NextRequirementQuestion(gamingReadyState()); err != nil || q != nil {
		t.Fatalf("ready 应返回 nil: %+v err=%v", q, err)
	}
	fresh, err := NextRequirementQuestion(NewRequirementState())
	if err != nil || fresh == nil || fresh.ReasonCode != "missing_use_case_type" || !reflect.DeepEqual(fresh.Fields, []string{"use_case.type"}) {
		t.Fatalf("空状态应先问用途: %+v err=%v", fresh, err)
	}
	// 阻塞冲突优先于缺失。
	state := NewRequirementState()
	state.Fields["budget_cny"] = RequirementField{Status: "conflict", Strength: "must"}
	q, err := NextRequirementQuestion(state)
	if err != nil || q == nil || q.ReasonCode != "requirement_conflict" || !reflect.DeepEqual(q.Fields, []string{"budget_cny"}) {
		t.Fatalf("conflict 应最先追问: %+v err=%v", q, err)
	}
	// unsupported 次之。
	q, err = NextRequirementQuestion(unsupportedObservationState("unsupported_capability:monitor"))
	if err != nil || q == nil || q.ReasonCode != "unsupported_capability" || !reflect.DeepEqual(q.Fields, []string{"monitor"}) {
		t.Fatalf("unsupported 应第二追问: %+v err=%v", q, err)
	}
	// 缺失的已有件型号合并成一个请求组。
	state = NewRequirementState()
	state.Fields["budget_cny"] = activeField(`9000`)
	state.Fields["use_case.type"] = activeField(`"general"`)
	state.Fields["existing_parts"] = activeField(`["gpu","memory"]`)
	q, err = NextRequirementQuestion(state)
	if err != nil || q == nil || q.ReasonCode != "missing_owned_part_models" ||
		!reflect.DeepEqual(q.Fields, []string{"owned_parts.gpu.model", "owned_parts.memory.model"}) {
		t.Fatalf("型号缺失应按品类合并: %+v err=%v", q, err)
	}
}

func TestDecodeRequirementStateRejectsV1(t *testing.T) {
	// v1 外形(含 reply/next_action 键)与显式 schema_version=1 都必须稳定拒绝。
	v1 := []byte(`{"schema_version":1,"revision":3,"fields":{"budget_cny":{"status":"active","value":8000}},"alternatives":[],"changes":[],"history":[],"reply":"旧协议回复","next_action":"confirm"}`)
	if _, err := DecodeRequirementState(v1); err == nil ||
		!(strings.Contains(err.Error(), "不支持的 schema_version") || strings.Contains(err.Error(), "unknown field")) {
		t.Fatalf("v1 状态应以稳定错误拒绝: %v", err)
	}
	v1NoLegacyKeys := []byte(`{"schema_version":1,"revision":3,"fields":{"budget_cny":{"status":"active","value":8000}},"alternatives":[],"changes":[],"history":[]}`)
	if _, err := DecodeRequirementState(v1NoLegacyKeys); err == nil || !strings.Contains(err.Error(), "不支持的 schema_version") {
		t.Fatalf("schema_version=1 应以版本错误拒绝: %v", err)
	}
	if _, err := DecodeRequirementUpdate([]byte(`{"operations":[],"reply":"r","next_action":"plan"}`)); err == nil {
		t.Fatal("领域更新合同不得携带 reply/next_action")
	}
	// reducer 同样拒绝 v1 输入状态。
	var legacy RequirementState
	if err := json.Unmarshal(v1, &legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyRequirementUpdate(legacy, RequirementUpdate{Operations: []RequirementOperation{{Op: "set", Field: "budget_cny", Value: json.RawMessage(`9000`), Quote: "9000"}}},
		RequirementSource{Kind: "chat", MessageID: "m", Quote: "9000"}); err == nil {
		t.Fatal("reducer 应拒绝 v1 状态")
	}
}
