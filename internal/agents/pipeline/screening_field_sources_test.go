package pipeline

import (
	"context"
	"encoding/json"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
	"iter"
	"reflect"
	"strings"
	"testing"
)

func TestExplicitFieldChangesAndRetractions(t *testing.T) {
	if explicitBudgetBasis([]string{"新增购买预算6000元", "更正：6000元是包含已有件价值的整机参考总价"}) != "full_build" {
		t.Fatal("new basis did not supersede old basis")
	}
	if explicitBudgetBasis([]string{"预算6000元既是新增预算。也是整机总预算。"}) != "" {
		t.Fatal("conflicting same-message basis accepted")
	}
	for _, source := range []string{"先取消刚才8000元的预算，金额现在还没定", "预算还没确定"} {
		if groundedBudget(8000, []string{"预算8000元", source}) {
			t.Fatal("retracted budget still grounded")
		}
		if !groundedBudget(6000, []string{"预算8000元", source, "预算6000元"}) {
			t.Fatal("new budget not restored")
		}
	}
	for _, source := range []string{"显示器是2K的", "用2K分辨率", "玩2K游戏", "分辨率2560x1440", "显示器是 2K 的", "8000 元预算,2K 分辨率玩黑神话", "8000 块,2K 玩游戏"} {
		if !groundedResolution("2K", []string{source}) {
			t.Fatalf("resolution rejected: %s", source)
		}
	}
	if groundedResolution("2K", []string{"预算8000元，打游戏"}) || groundedResolution("2K", []string{"显示器2K", "分辨率还没定"}) {
		t.Fatal("invented/retracted resolution accepted")
	}
	if !groundedResolution("4K", []string{"显示器2K", "分辨率改为4K"}) || groundedResolution("2K", []string{"显示器2K", "分辨率改为4K"}) {
		t.Fatal("resolution update ignored")
	}
	_, missing := guardOwnedScreening(`{"budget_cny":8000,"use_case":{"type":"gaming","resolution":"1080p"}}`, []string{"预算8000元的新主机，主要玩游戏。"})
	if !reflect.DeepEqual(missing, []string{"resolution"}) {
		t.Fatalf("invented resolution escaped: %v", missing)
	}
}

type screeningInstructionProbe struct {
	guardTestModel
	instruction string
}

func (m *screeningInstructionProbe) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	m.instruction = screeningText(req.Config.SystemInstruction)
	return m.guardTestModel.GenerateContent(ctx, req, stream)
}
func TestProgramBuildStateSelectsProtocolWithoutMutatingRequest(t *testing.T) {
	original := &model.LLMRequest{Config: &genai.GenerateContentConfig{SystemInstruction: genai.NewContentFromText("actual change protocol", genai.RoleUser)}}
	for _, hasBuild := range []bool{false, true} {
		m := &screeningInstructionProbe{guardTestModel: guardTestModel{text: "请提供预算。"}}
		for _, err := range (screeningGuard{LLM: m}).GenerateContent(WithScreeningBuildState(context.Background(), hasBuild), original, false) {
			if err != nil {
				t.Fatal(err)
			}
		}
		if hasBuild && m.instruction != "actual change protocol" {
			t.Fatal("existing build lost change protocol")
		}
		if !hasBuild && !strings.Contains(m.instruction, "当前没有生成或保存任何配置版本") {
			t.Fatal("new draft still ambiguous")
		}
		if screeningText(original.Config.SystemInstruction) != "actual change protocol" {
			t.Fatal("shared request mutated")
		}
	}
}

func TestProgramOnlySuppliesMissingProtocolVersion(t *testing.T) {
	raw := `{"budget_cny":6000,"use_case":{"type":"gaming","resolution":"2K"}}`
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(normalizeDraftProtocol(raw)), &fields); err != nil || string(fields["schema_version"]) != "1" || string(fields["budget_cny"]) != "6000" {
		t.Fatalf("normalization: %v %v", fields, err)
	}
	for _, s := range []string{`{"schema_version":2,"budget_cny":6000}`, `{"intent":"adjust_budget"}`, "请提供预算。"} {
		if normalizeDraftProtocol(s) != s {
			t.Fatalf("overwrote explicit/other protocol: %s", s)
		}
	}
	normalized := normalizeDraftProtocol(`{"schema_version":"1","budget_cny":6000,"notes":[]}`)
	if err := json.Unmarshal([]byte(normalized), &fields); err != nil || string(fields["schema_version"]) != "1" || string(fields["notes"]) != `""` || string(fields["budget_cny"]) != "6000" {
		t.Fatalf("empty protocol normalization: %s %v", normalized, err)
	}
	invalidNotes := `{"schema_version":1,"budget_cny":6000,"notes":["用户说明"]}`
	if normalizeDraftProtocol(invalidNotes) != invalidNotes {
		t.Fatal("nonempty notes silently discarded")
	}
}
