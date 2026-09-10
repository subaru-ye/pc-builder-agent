package presenter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func TestConversationSummaryUsesHumanNamesAndHistoricalVersion(t *testing.T) {
	v1 := fixture(t, 1, 8000, "8181.83", "gpu-amd")
	v2 := fixture(t, 2, 8000, "7651.83", "gpu-amd")
	v2.ParentID = &v1.ID
	reader := fakeReader{builds: []store.BuildVersion{v1, v2}, specs: map[int64]json.RawMessage{1: requirement(t, 8000), 2: requirement(t, 8000)}, names: map[string]string{"cpu-1": "AMD Ryzen 5 7600", "gpu-amd": "RX 7700 XT", "memory-1": "32GB DDR5"}}
	s := New(reader)
	one, err := s.ConversationSummary(context.Background(), "s1", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Ryzen 5 7600", "32GB DDR5", "8181.83", "超出预算 ¥181.83", "2026-08-09"} {
		if !strings.Contains(one, want) {
			t.Fatalf("missing %q: %s", want, one)
		}
	}
	for _, forbidden := range []string{"build_ref", "cpu-1", "pass", "已落库", "7651.83"} {
		if strings.Contains(one, forbidden) {
			t.Fatalf("internal/latest data leaked: %s", one)
		}
	}
	two, err := s.ConversationSummary(context.Background(), "s1", 2)
	if err != nil || !strings.Contains(two, "配件保持不变") || !strings.Contains(two, "减少 **¥530.00**") {
		t.Fatalf("change summary: %s %v", two, err)
	}
}

func TestConversationWarningsAndBudgetBasis(t *testing.T) {
	view := BuildView{Summary: BuildSummary{Version: 2}, Quote: QuoteView{TotalCNY: "9000.00", BudgetDeltaCNY: "-100.00", SnapshotDate: "2026-07-28", MissingCount: 1}, Validation: ValidationView{OverallStatus: schemas.OverallFail, Checks: []schemas.CheckResult{{RuleID: schemas.RuleGPUClearance, Outcome: schemas.OutcomeFail}, {RuleID: schemas.RulePSUHeadroom, Outcome: schemas.OutcomeUnknown}}}}
	spec, err := schemas.DecodeRequirementSpec(requirement(t, 8000))
	if err != nil {
		t.Fatal(err)
	}
	spec.NoisePref = "silent"
	spec.ConstraintStrengths = map[string]string{"noise_pref": "must"}
	view.Requirement, _ = schemas.EncodeRequirementSpec(spec)
	text := RenderConversation(view, nil)
	for _, want := range []string{"已知价格小计", "1 项价格缺失", "显卡安装空间", "电源功率余量", "静音是必须条件", "不能确认满足"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing warning %q: %s", want, text)
		}
	}
	amount := "1200.00"
	view.Quote.BudgetBasis = "new_purchase"
	view.Quote.PurchaseTotalCNY = &amount
	view.Quote.MissingCount = 0
	parent := view
	parent.Quote.BudgetBasis = "full_build"
	text = RenderConversation(view, &parent)
	if !strings.Contains(text, "本次新购参考总价 **¥1200.00**") || !strings.Contains(text, "不直接比较金额") || strings.Contains(text, "减少") {
		t.Fatal(text)
	}
}

func TestConversationChangesOnlyListsChangedParts(t *testing.T) {
	old := BuildView{Summary: BuildSummary{Version: 1}, Quote: QuoteView{TotalCNY: "8000.00"}, Validation: ValidationView{OverallStatus: schemas.OverallPass}, Parts: []PartLine{{Category: schemas.CategoryCPU, SKU: "cpu", Name: "处理器型号", Quantity: 1}, {Category: schemas.CategoryMemory, SKU: "ram-old", Name: "旧内存", Quantity: 1}}}
	current := old
	current.Summary.Version = 2
	current.Parts = append([]PartLine(nil), old.Parts...)
	current.Parts[1] = PartLine{Category: schemas.CategoryMemory, SKU: "ram-new", Name: "新内存", Quantity: 2}
	current.Quote.TotalCNY = "7500.00"
	text := RenderConversation(current, &old)
	if !strings.Contains(text, "旧内存 → 新内存 ×2") || strings.Contains(text, "处理器型号") || strings.Contains(text, "ram-new") {
		t.Fatal(text)
	}
	old.Requirement = requirement(t, 8000)
	current.Requirement = requirement(t, 7500)
	text = RenderConversation(current, &old)
	if !strings.Contains(text, "预算由 ¥8000 调整为 ¥7500") {
		t.Fatal(text)
	}
}
