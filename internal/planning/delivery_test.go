package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func completeRecording(t *testing.T) (schemas.PlanningInput, Result, recordedCatalog) {
	t.Helper()
	raw, err := os.ReadFile("testdata/complete_proposal_recording.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Input  schemas.PlanningInput
		Result Result
	}
	if err = json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	c := recordedCatalog{store.CatalogSnapshot{Snapshot: store.Snapshot{SnapshotDate: time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)}}}
	for _, p := range f.Result.Candidates {
		c.Candidates = append(c.Candidates, store.Candidate{SKU: p.ID, Category: p.Category, Brand: p.Brand, Model: p.Model, Specs: p.Specs, PriceCNY: p.Price})
	}
	return f.Input, f.Result, c
}

func TestSavedCompleteProposalAutomaticallyDelivers(t *testing.T) {
	input, record, catalog := completeRecording(t)
	if record.Outcome != "proposal" || len(record.Evidence) != 100 || record.SearchCalls != 0 {
		t.Fatal("recording changed")
	}
	m := &scriptedModel{respond: func(_ int, r *model.LLMRequest) *genai.Content {
		if !strings.Contains(r.Contents[0].Parts[0].Text, `free.workload_resolution`) {
			t.Fatal("state not sent to builder")
		}
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), input)
	if err != nil || got.Outcome != "ready" || got.ModelOutcome != "proposal" || got.Delivery.Status != "eligible" || got.ModelCalls != 1 || got.Quote.TotalCNY != "4579.90" || len(got.Validation.Checks) != 12 || len(got.Issues) != 0 {
		t.Fatalf("not delivered: %+v %v", got, err)
	}
}

func TestDeliveryHonorsProblemsButNotUnstatedPreferences(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*schemas.PlanningInput, *Result, *recordedCatalog)
		want   string
	}{
		{"unrequested-noise", func(_ *schemas.PlanningInput, r *Result, _ *recordedCatalog) {
			r.Assessments = append(r.Assessments, Assessment{Field: "noise_pref", Status: "unknown"})
		}, "ready"},
		{"soft-noise", func(i *schemas.PlanningInput, r *Result, _ *recordedCatalog) {
			i.State.Fields["noise_pref"] = schemas.RequirementField{Status: "active", Kind: "constraint", Strength: "prefer", Value: json.RawMessage(`"silent"`)}
			r.Assessments = append(r.Assessments, Assessment{Field: "noise_pref", Status: "unknown", Explanation: "不保证静音"})
		}, "ready"},
		{"must-noise", func(i *schemas.PlanningInput, _ *Result, _ *recordedCatalog) {
			i.State.Fields["noise_pref"] = schemas.RequirementField{Status: "active", Kind: "constraint", Strength: "must", Value: json.RawMessage(`"silent"`)}
		}, "proposal"},
		{"budget-verified-without-model-assessment", func(_ *schemas.PlanningInput, r *Result, _ *recordedCatalog) { r.Assessments = nil }, "ready"},
		{"missing-price", func(_ *schemas.PlanningInput, _ *Result, c *recordedCatalog) { c.Candidates[0].PriceCNY = nil }, "proposal"},
		{"compatibility-conflict", func(_ *schemas.PlanningInput, _ *Result, c *recordedCatalog) {
			for n := range c.Candidates {
				if c.Candidates[n].Category == schemas.CategoryMotherboard {
					c.Candidates[n].Specs = json.RawMessage(strings.Replace(string(c.Candidates[n].Specs), "AM4", "AM5", 1))
				}
			}
		}, "proposal"},
		{"necessary-question", func(_ *schemas.PlanningInput, r *Result, _ *recordedCatalog) {
			r.Outcome = "clarify"
			r.Reply = "旧硬盘是否需要保留？"
		}, "clarify"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, record, catalog := completeRecording(t)
			tc.change(&input, &record, &catalog)
			if record.Outcome == "proposal" {
				record.Outcome = "ready"
			} // A false ready claim cannot override facts.
			m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
				raw, _ := json.Marshal(record)
				return genai.NewContentFromText(string(raw), genai.RoleModel)
			}}
			got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
			if err != nil || got.Outcome != tc.want || (tc.want == "proposal" && len(got.Issues) == 0) {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

func TestEmptyProposalFeedbackCanFillAssessment(t *testing.T) {
	input, record, catalog := completeRecording(t)
	// Non-accounting must conditions still need a supported assessment.
	input.State.Fields["brand_pref.gpu"] = schemas.RequirementField{Status: "active", Kind: "constraint", Strength: "must", Value: json.RawMessage(`"amd"`)}
	record.Assessments = append(record.Assessments, Assessment{Field: "brand_pref.gpu", Status: "met", Evidence: []string{"local:gpu-sapphire-6600-pulse"}})
	m := &scriptedModel{respond: func(n int, r *model.LLMRequest) *genai.Content {
		copy := record
		if n == 1 {
			copy.Assessments = nil
		} else if !strings.Contains(r.Contents[len(r.Contents)-1].Parts[0].Text, "显卡品牌仍待确认") {
			t.Fatal("missing assessment was not returned as feedback")
		}
		raw, _ := json.Marshal(copy)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), input)
	if err != nil || got.Outcome != "ready" || got.ModelCalls != 2 || len(got.Issues) != 0 {
		t.Fatalf("stale issues survived repair: %+v %v", got, err)
	}
}

func TestNonemptyProposalCanSearchAndRepairAfterDeliveryFeedback(t *testing.T) {
	input, record, catalog := completeRecording(t)
	var expensiveSKU string
	for _, c := range catalog.Candidates {
		if c.Category == schemas.CategoryCPU {
			c.SKU = "cpu-test-expensive"
			price := "20000"
			c.PriceCNY = &price
			catalog.Candidates = append(catalog.Candidates, c)
			expensiveSKU = c.SKU
			break
		}
	}
	var bad map[string]any
	if err := json.Unmarshal(record.Draft, &bad); err != nil {
		t.Fatal(err)
	}
	bad["selection"].(map[string]any)["cpu"] = expensiveSKU
	m := &scriptedModel{respond: func(n int, req *model.LLMRequest) *genai.Content {
		copy := record
		switch n {
		case 1:
			copy.Draft, _ = json.Marshal(bad)
			copy.Issues = []string{"当前候选超预算"}
		case 2:
			feedback := req.Contents[len(req.Contents)-1].Parts[0].Text
			// 超预算交付现在先过预算自纠门（替代价目反馈），或走既有一次性核验。
			if (!strings.Contains(feedback, "候选价格超过已表达的预算范围") && !strings.Contains(feedback, "预算自纠反馈")) || len(req.Config.Tools) == 0 {
				t.Fatalf("proposal ended without actual budget feedback and repair tools: %s", feedback)
			}
			return function("search_local", `{"category":"cpu"}`)
		case 3:
			return function("evaluate", `{"draft":`+string(record.Draft)+`}`)
		}
		raw, _ := json.Marshal(copy)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), input)
	if err != nil || got.Outcome != "ready" || got.Quote.TotalCNY != "4579.90" || got.ModelCalls != 4 || got.ToolCalls != 2 || len(got.Issues) != 0 {
		t.Fatalf("proposal was not repaired: outcome=%s calls=%d issues=%v error=%v", got.Outcome, got.ModelCalls, got.Issues, err)
	}
}

func TestUnresolvedProposalReviewIsBoundedAndDoesNotRelaxMust(t *testing.T) {
	for _, turns := range []int{1, 2, 3, 8} {
		t.Run(fmt.Sprint(turns), func(t *testing.T) {
			input, record, catalog := completeRecording(t)
			input.State.Fields["noise_pref"] = schemas.RequirementField{Status: "active", Kind: "constraint", Strength: "must", Value: json.RawMessage(`"silent"`)}
			record.Issues = []string{"整机噪声缺少实测"}
			record.Assessments = append(record.Assessments, Assessment{Field: "noise_pref", Status: "unknown"})
			m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
				raw, _ := json.Marshal(record)
				return genai.NewContentFromText(string(raw), genai.RoleModel)
			}}
			got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: turns}).Run(context.Background(), input)
			wantCalls := 1
			if turns >= 3 {
				wantCalls = 2
			}
			if err != nil || got.ModelCalls != wantCalls || got.Outcome != "proposal" || !slices.Contains(got.Issues, "整机噪声缺少实测") || input.State.Fields["noise_pref"].Strength != "must" {
				t.Fatalf("review loop lost bounds or facts: calls=%d issues=%v error=%v", got.ModelCalls, got.Issues, err)
			}
		})
	}
}

func TestUnfinishedDeliveryRetainsExternalAlternatives(t *testing.T) {
	input, record, _ := completeRecording(t)
	record.Issues = []string{"需要讨论取舍"}
	x := execution{input: input, result: record, candidates: append([]Candidate{}, record.Candidates...), evidence: record.Evidence}
	alternative := record.Candidates[0]
	alternative.ID = "ext-alternative"
	alternative.External = true
	x.candidates = append(x.candidates, alternative)
	got := x.finish()
	if got.Outcome != "proposal" || len(got.Candidates) != len(record.Candidates)+1 {
		t.Fatal("lost external research progress")
	}
	x.result.Issues = nil
	got = x.finish()
	if got.Outcome != "ready" || len(got.Candidates) != len(record.Candidates) {
		t.Fatal("unselected alternative entered formal build")
	}
}

// 已有件零匹配且品类被选中：保留 vs 改购是计价取舍，必须 clarify 并点名
// 用户型号（v2 合同；旧断言"降级为非阻塞 note 仍可交付"正是 BV2-104 账实
// 不一致的根因语义，已修订）。
func TestUnmatchedOwnedSelectedForcesTradeoffClarify(t *testing.T) {
	input, record, catalog := completeRecording(t)
	input.State.Fields["owned_parts"] = schemas.RequirementField{
		Status: "active",
		Value:  json.RawMessage(`[{"category":"cooler","model":"旧风冷A"},{"category":"psu","model":"旧电源B"}]`),
	}
	record.Outcome = "ready" // 模型直接宣称 ready 也必须被结构化检查拦截。
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != "clarify" {
		t.Fatalf("zero-match owned parts in selected categories must clarify: %+v", got)
	}
	joined := strings.Join(got.Issues, "; ")
	for _, want := range []string{"计价取舍", "散热器：旧风冷A", "电源：旧电源B"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("clarify issue must name the tradeoff (%q): %v", want, got.Issues)
		}
	}
}

// 措辞独立性：BV2-104 的真实措辞（"继续沿用"，无"替身/沿用用户"）且模型
// 直接宣称 ready，也必须被结构化检查拦截强转 clarify。
func TestOwnershipTradeoffIgnoresReplyWordingAndReadyClaim(t *testing.T) {
	input, record, catalog := completeRecording(t)
	input.State.Fields["owned_parts"] = schemas.RequirementField{
		Status: "active", Kind: "fact", Strength: "must",
		Value: json.RawMessage(`[{"category":"memory","model":"G.Skill Ripjaws V 32GB DDR4-3200","quantity":1}]`),
	}
	record.Outcome = "ready"
	record.Reply = "已为您完成方案，已有的 32GB DDR4 内存继续沿用，新增采购合计约 4xxx 元。"
	record.Issues = nil
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != "clarify" {
		t.Fatalf("ready claim with 继续沿用 wording must still clarify: %+v", got)
	}
	if !strings.Contains(strings.Join(got.Issues, "; "), "计价取舍") {
		t.Fatalf("tradeoff issue missing: %v", got.Issues)
	}
}

// SSD 豁免需要型号对应（v3.1 收紧：仅数量一致不再豁免）加数量合计一致。
// 同型号且数量一致 → 豁免；同型号数量不一致 → 计价（超额是采购）。
func TestSSDOwnershipAccountingFollowsCorrespondence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selectQty int
		wantOwned bool
	}{
		{"same-model-quantity-match-exempts", 1, true},
		{"same-model-quantity-mismatch-prices", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, record, catalog := completeRecording(t)
			input.State.Fields["owned_parts"] = schemas.RequirementField{
				Status: "active", Kind: "fact", Strength: "must",
				Value: json.RawMessage(`[{"category":"ssd","model":"P3 Plus 1TB NVMe M.2","quantity":1}]`),
			}
			var draft map[string]any
			if err := json.Unmarshal(record.Draft, &draft); err != nil {
				t.Fatal(err)
			}
			sel := draft["selection"].(map[string]any)
			sel["ssd"] = []map[string]any{{"sku": "ssd-crucial-p3plus-1tb", "quantity": tc.selectQty}}
			raw, _ := json.Marshal(draft)
			record.Draft = raw
			record.Outcome = "proposal"
			m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
				raw, _ := json.Marshal(record)
				return genai.NewContentFromText(string(raw), genai.RoleModel)
			}}
			got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			owned := false
			for _, line := range got.Quote.Lines {
				if strings.Contains(line.SKU, "ssd") {
					owned = line.Owned
				}
			}
			if owned != tc.wantOwned {
				t.Fatalf("ssd owned exemption = %v, want %v (quote=%+v)", owned, tc.wantOwned, got.Quote)
			}
		})
	}
}

func TestUserIssuesKeepChineseDetailButDropInternalRuleCodes(t *testing.T) {
	input, record, catalog := completeRecording(t)
	record.Outcome = "proposal"
	record.Issues = []string{"DISPLAY_OUTPUT_FAIL：当前CPU无核显且未配置独立显卡，整机无显示输出"}
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got.Issues {
		if strings.Contains(s, "DISPLAY_OUTPUT_FAIL") {
			t.Fatalf("internal rule code leaked to user issues: %q", s)
		}
	}
	if got.Reply != "" && strings.Contains(got.Reply, "DISPLAY_OUTPUT_FAIL") {
		t.Fatalf("internal rule code leaked to user reply: %q", got.Reply)
	}
	if !slices.ContainsFunc(got.Issues, func(s string) bool { return strings.Contains(s, "整机无显示输出") }) {
		t.Fatalf("chinese detail lost: %v", got.Issues)
	}
}

// 边界①（BV2-104 同类）：用户已有 SSD A，draft 选中不同型号 SSD B——数量
// 相同也不得把 B 免计为已有件；实际选中件与已有件不对应即计价并升级为取舍。
func TestSSDDifferentModelMustBePricedEvenWithSameQuantity(t *testing.T) {
	input, record, catalog := completeRecording(t)
	input.State.Fields["owned_parts"] = schemas.RequirementField{
		Status: "active", Kind: "fact", Strength: "must",
		Value: json.RawMessage(`[{"category":"ssd","model":"某旧SSD","quantity":1}]`),
	}
	record.Outcome = "proposal"
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range got.Quote.Lines {
		if strings.Contains(line.SKU, "ssd") && line.Owned {
			t.Fatalf("different-model ssd must not be exempted as owned: %+v", got.Quote.Lines)
		}
	}
	if got.Outcome != "clarify" || !strings.Contains(strings.Join(got.Issues, "; "), "计价取舍") {
		t.Fatalf("ssd keep-vs-buy tradeoff must clarify: outcome=%s issues=%v", got.Outcome, got.Issues)
	}
}

// 边界②：目录存在用户已有型号 A，但 draft 选同品类 B——没有明确的改购授权
// （状态合同只能通过移除已有件表达授权），不得直接交付 ready。
func TestCatalogMatchedOwnedReplacedByDifferentSKUForcesTradeoff(t *testing.T) {
	input, record, catalog := completeRecording(t)
	input.State.Fields["owned_parts"] = schemas.RequirementField{
		Status: "active", Kind: "fact", Strength: "must",
		Value: json.RawMessage(`[{"category":"memory","model":"G.Skill Ripjaws V 32GB (2x16GB) DDR4-3200 CL16","quantity":1}]`),
	}
	memOther := store.Candidate{
		SKU: "mem-other", Category: schemas.CategoryMemory, Brand: "Corsair",
		Model: "Vengeance LPX 32GB (2x16GB) DDR4-3200 CL16", PriceCNY: strPtr("1759.00"),
	}
	for _, c := range catalog.Candidates {
		if c.SKU == "mem-gskill-ripjawsv-32-3200" {
			memOther.Specs = c.Specs // 复制内存规格，保证校验可完整执行
		}
	}
	catalog.Candidates = append(catalog.Candidates, memOther)
	var draft map[string]any
	if err := json.Unmarshal(record.Draft, &draft); err != nil {
		t.Fatal(err)
	}
	draft["selection"].(map[string]any)["memory"] = "mem-other"
	raw, _ := json.Marshal(draft)
	record.Draft = raw
	record.Outcome = "proposal"
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != "clarify" || !strings.Contains(strings.Join(got.Issues, "; "), "计价取舍") {
		t.Fatalf("unauthorized replacement of catalog-matched owned part must clarify: outcome=%s issues=%v", got.Outcome, got.Issues)
	}
}

// 数量变化：同一型号但数量不一致（1 有 2 选）时，超额部分是采购，不豁免。
func TestSSDSameModelQuantityMismatchMustNotExempt(t *testing.T) {
	input, record, catalog := completeRecording(t)
	input.State.Fields["owned_parts"] = schemas.RequirementField{
		Status: "active", Kind: "fact", Strength: "must",
		Value: json.RawMessage(`[{"category":"ssd","model":"P3 Plus 1TB NVMe M.2","quantity":1}]`),
	}
	var draft map[string]any
	if err := json.Unmarshal(record.Draft, &draft); err != nil {
		t.Fatal(err)
	}
	draft["selection"].(map[string]any)["ssd"] = []map[string]any{{"sku": "ssd-crucial-p3plus-1tb", "quantity": 2}}
	raw, _ := json.Marshal(draft)
	record.Draft = raw
	record.Outcome = "proposal"
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range got.Quote.Lines {
		if strings.Contains(line.SKU, "ssd") && line.Owned {
			t.Fatalf("quantity mismatch must not exempt the ssd line: %+v", got.Quote.Lines)
		}
	}
}

// 授权改单：用户已明确移除旧件（owned_parts 不再含该品类）后选新件，属已
// 授权路径——计价交付，不触发取舍 clarify。
func TestAuthorizedReplacementAfterOwnedRemovalDelivers(t *testing.T) {
	input, record, catalog := completeRecording(t)
	input.State.Fields["owned_parts"] = schemas.RequirementField{
		Status: "active", Kind: "fact", Strength: "must",
		Value: json.RawMessage(`[{"category":"cpu","model":"AMD Ryzen 5 5600","quantity":1}]`),
	}
	memOther := store.Candidate{
		SKU: "mem-other", Category: schemas.CategoryMemory, Brand: "Corsair",
		Model: "Vengeance LPX 32GB (2x16GB) DDR4-3200 CL16", PriceCNY: strPtr("1759.00"),
	}
	for _, c := range catalog.Candidates {
		if c.SKU == "mem-gskill-ripjawsv-32-3200" {
			memOther.Specs = c.Specs // 复制内存规格，保证校验可完整执行
		}
	}
	catalog.Candidates = append(catalog.Candidates, memOther)
	var draft map[string]any
	if err := json.Unmarshal(record.Draft, &draft); err != nil {
		t.Fatal(err)
	}
	draft["selection"].(map[string]any)["memory"] = "mem-other"
	raw, _ := json.Marshal(draft)
	record.Draft = raw
	record.Outcome = "proposal"
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome == "clarify" {
		t.Fatalf("authorized replacement after owned removal must not clarify: %+v", got)
	}
	for _, line := range got.Quote.Lines {
		if strings.Contains(line.SKU, "mem-other") && line.Owned {
			t.Fatalf("newly bought memory must be priced: %+v", got.Quote.Lines)
		}
	}
}

// 多 SSD 部分对应（已有 A+B 各 1，选中 A+C 各 1，总数量相同）：WithOwnership
// 按品类豁免，只把对应件 A 放入 OwnedParts 会让 C 一并免费——部分对应必须
// 整品类计价并 clarify，最终不得 ready。
func TestSSDPartialCorrespondencePricesWholeCategory(t *testing.T) {
	input, record, catalog := completeRecording(t)
	input.State.Fields["owned_parts"] = schemas.RequirementField{
		Status: "active", Kind: "fact", Strength: "must",
		Value: json.RawMessage(`[{"category":"ssd","model":"SSD Model A","quantity":1},{"category":"ssd","model":"SSD Model B","quantity":1}]`),
	}
	var specs *store.Candidate
	for _, c := range catalog.Candidates {
		if c.SKU == "ssd-crucial-p3plus-1tb" {
			cc := c
			specs = &cc
		}
	}
	catalog.Candidates = append(catalog.Candidates,
		store.Candidate{SKU: "ssd-a", Category: schemas.CategorySSD, Brand: "TestA", Model: "SSD Model A", Specs: specs.Specs, PriceCNY: strPtr("400.00")},
		store.Candidate{SKU: "ssd-c", Category: schemas.CategorySSD, Brand: "TestC", Model: "SSD Model C", Specs: specs.Specs, PriceCNY: strPtr("450.00")},
	)
	var draft map[string]any
	if err := json.Unmarshal(record.Draft, &draft); err != nil {
		t.Fatal(err)
	}
	draft["selection"].(map[string]any)["ssd"] = []map[string]any{
		{"sku": "ssd-a", "quantity": 1},
		{"sku": "ssd-c", "quantity": 1},
	}
	raw, _ := json.Marshal(draft)
	record.Draft = raw
	record.Outcome = "proposal"
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != "clarify" {
		t.Fatalf("partial ssd correspondence must clarify: outcome=%s", got.Outcome)
	}
	for _, line := range got.Quote.Lines {
		if !strings.Contains(line.SKU, "ssd") {
			continue
		}
		if line.Owned {
			t.Fatalf("partial correspondence must price the whole category, but %s stayed owned: %+v", line.SKU, got.Quote.Lines)
		}
	}
}

// 正例：已有 A+B 与选中 A+B 完全对应（型号与各自数量）→ 整品类豁免，无取舍。
func TestSSDFullCorrespondenceExemptsWholeCategory(t *testing.T) {
	input, record, catalog := completeRecording(t)
	input.State.Fields["owned_parts"] = schemas.RequirementField{
		Status: "active", Kind: "fact", Strength: "must",
		Value: json.RawMessage(`[{"category":"ssd","model":"SSD Model A","quantity":1},{"category":"ssd","model":"SSD Model B","quantity":1}]`),
	}
	var specs *store.Candidate
	for _, c := range catalog.Candidates {
		if c.SKU == "ssd-crucial-p3plus-1tb" {
			cc := c
			specs = &cc
		}
	}
	catalog.Candidates = append(catalog.Candidates,
		store.Candidate{SKU: "ssd-a", Category: schemas.CategorySSD, Brand: "TestA", Model: "SSD Model A", Specs: specs.Specs, PriceCNY: strPtr("400.00")},
		store.Candidate{SKU: "ssd-b", Category: schemas.CategorySSD, Brand: "TestB", Model: "SSD Model B", Specs: specs.Specs, PriceCNY: strPtr("420.00")},
	)
	var draft map[string]any
	if err := json.Unmarshal(record.Draft, &draft); err != nil {
		t.Fatal(err)
	}
	draft["selection"].(map[string]any)["ssd"] = []map[string]any{
		{"sku": "ssd-a", "quantity": 1},
		{"sku": "ssd-b", "quantity": 1},
	}
	raw, _ := json.Marshal(draft)
	record.Draft = raw
	record.Outcome = "proposal"
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome == "clarify" {
		t.Fatalf("full ssd correspondence must not clarify: %+v", got)
	}
	for _, line := range got.Quote.Lines {
		if strings.Contains(line.SKU, "ssd") && !line.Owned {
			t.Fatalf("fully corresponding ssd selection must stay exempt: %+v", got.Quote.Lines)
		}
	}
}

// 聚焦反例（BV2-110 无依据断言剔除的边界）：整行关键词剔除只允许发生在
// 服务端核验过完整合计之后。合计缺价时算术未核验，"可能超出预算"未被权威
// 核算矛盾，必须保留——静默删除会掩盖缺价件推高合计的方向性提示。
func TestBudgetClaimDropRequiresVerifiedCompleteQuote(t *testing.T) {
	input, record, catalog := completeRecording(t)
	record.Issues = []string{"整机合计可能超出预算，若超出需用户确认降档或加预算"}
	for n := range catalog.Candidates {
		if catalog.Candidates[n].Category == schemas.CategoryGPU {
			catalog.Candidates[n].PriceCNY = nil
		}
	}
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog, MaxTurns: 1}).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Quote == nil || got.Quote.MissingCount == 0 {
		t.Fatalf("fixture must produce a missing price: %+v", got.Quote)
	}
	if !strings.Contains(strings.Join(got.Issues, "\n"), "可能超出预算") {
		t.Fatalf("合计未核验时不得剔除模型预算提示: %v", got.Issues)
	}
}

// 聚焦反例（掩盖取舍）：合计已核验且在有效上限内时，含"超出硬上限"的无依据
// 行按整行剔除；被删行同时承载预算取舍时，服务端必须以 note 如实复述真实
// 事实（报价高于陈述金额、上限内交付不构成超支），不得静默抹掉预算张力。
func TestInCeilingClaimDropRestatesBudgetTensionAsNote(t *testing.T) {
	input, record, catalog := completeRecording(t)
	budget := input.State.Fields["budget_cny"]
	budget.Value = json.RawMessage(`4500`)
	input.State.Fields["budget_cny"] = budget
	record.Issues = []string{"整机总价4579.90元超出硬上限，如需压回4500以内可降内存档位，请您确认取舍"}
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content {
		raw, _ := json.Marshal(record)
		return genai.NewContentFromText(string(raw), genai.RoleModel)
	}}
	got, err := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(got.Issues, "\n"), "超出硬上限") {
		t.Fatalf("in-ceiling 无依据断言必须剔除: %v", got.Issues)
	}
	if got.Outcome != "ready" {
		t.Fatalf("上限内方案必须交付 ready，got %s (issues=%v)", got.Outcome, got.Issues)
	}
	if got.Delivery == nil {
		t.Fatal("delivery missing")
	}
	notes := strings.Join(got.Delivery.Notes, "\n")
	if !strings.Contains(notes, "4579.90") || !strings.Contains(notes, "4500") || !strings.Contains(notes, "4950") {
		t.Fatalf("剔除断言后必须以 note 复述真实预算张力: %v", got.Delivery.Notes)
	}
}
