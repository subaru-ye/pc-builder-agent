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

// SSD 按品类内数量核账（存储可互换）：数量一致则豁免并保留透明 note，
// 数量不一致时超额部分计价，不得整品类免计价。
func TestSSDOwnershipAccountingFollowsQuantity(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ownedQty  int
		selectQty int
		wantOwned bool
	}{
		{"quantity-match-exempts", 1, 1, true},
		{"quantity-mismatch-prices", 1, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, record, catalog := completeRecording(t)
			input.State.Fields["owned_parts"] = schemas.RequirementField{
				Status: "active", Kind: "fact", Strength: "must",
				Value: json.RawMessage(fmt.Sprintf(`[{"category":"ssd","model":"某旧SSD","quantity":%d}]`, tc.ownedQty)),
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
			if tc.wantOwned {
				// 数量豁免保留透明 note：说明沿用了用户的旧 SSD。
				if len(got.Delivery.Notes) == 0 || !strings.Contains(got.Delivery.Notes[0], "某旧SSD") {
					t.Fatalf("quantity-exempt ssd must keep the transparency note: %+v", got.Delivery)
				}
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
