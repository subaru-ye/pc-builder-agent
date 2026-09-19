package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

type recordedCatalog struct{ store.CatalogSnapshot }

func (c recordedCatalog) ActiveCatalogSnapshot(context.Context) (store.CatalogSnapshot, error) {
	return c.CatalogSnapshot, nil
}

type scriptedModel struct {
	calls   int
	respond func(int, *model.LLMRequest) *genai.Content
}

func (*scriptedModel) Name() string { return "offline-recorded-selection" }
func (m *scriptedModel) GenerateContent(_ context.Context, r *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(y func(*model.LLMResponse, error) bool) {
		m.calls++
		y(&model.LLMResponse{Content: m.respond(m.calls, r)}, nil)
	}
}
func function(action, payload string) *genai.Content {
	return &genai.Content{Role: "model", Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: "test-call", Name: "planning_action", Args: map[string]any{"action": action, "payload": payload}}}}}
}

// Selection, specs and prices come from a saved real conversation. The scripted
// tool trajectory is an offline protocol oracle, not a claim of live reasoning.
func fixture(t *testing.T) (recordedCatalog, json.RawMessage) {
	t.Helper()
	raw, e := os.ReadFile("../producthttp/testdata/requirement_replay.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Draft json.RawMessage
		Parts []struct {
			SKU, Brand, Model, PriceCNY string
			Category                    schemas.Category
			Specs                       json.RawMessage
		}
	}
	if e = json.Unmarshal(raw, &f); e != nil {
		t.Fatal(e)
	}
	c := recordedCatalog{store.CatalogSnapshot{Snapshot: store.Snapshot{SnapshotDate: time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)}}}
	for _, p := range f.Parts {
		price := p.PriceCNY
		c.Candidates = append(c.Candidates, store.Candidate{SKU: p.SKU, Category: p.Category, Brand: p.Brand, Model: p.Model, Specs: p.Specs, PriceCNY: &price})
	}
	return c, f.Draft
}

func TestMandatorySilenceReachesToolsAndSavesProposal(t *testing.T) {
	catalog, draft := fixture(t)
	state := schemas.NewRequirementState()
	state.Fields["noise_pref"] = schemas.RequirementField{Value: json.RawMessage(`"silent"`), Status: "active", Kind: "constraint", Strength: "must"}
	state.Fields["budget_cny"] = schemas.RequirementField{Value: json.RawMessage(`6000`), Status: "active", Kind: "constraint", Strength: "prefer"}
	m := &scriptedModel{respond: func(call int, r *model.LLMRequest) *genai.Content {
		switch call {
		case 1:
			if !strings.Contains(r.Contents[0].Parts[0].Text, `"strength":"must"`) {
				t.Fatal("must requirement missing from actual model input")
			}
			return function("search_local", `{"category":"cpu"}`)
		case 2:
			response := r.Contents[len(r.Contents)-1].Parts[0].FunctionResponse.Response
			if len(response["candidates"].([]Candidate)) == 0 {
				t.Fatal("catalog was prefiltered")
			}
			return function("evaluate", `{"draft":`+string(draft)+`}`)
		default:
			return genai.NewContentFromText(`{"outcome":"proposal","reply":"候选方案已保存，需要进一步核对整机噪声。","draft":`+string(draft)+`,"assessments":[{"field":"noise_pref","status":"unknown","explanation":"缺少整机实测","evidence":[]}],"issues":["整机噪声缺少实测"],"assumptions":[]}`, genai.RoleModel)
		}
	}}
	result, e := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), schemas.PlanningInput{SchemaVersion: 2, State: state})
	if e != nil || result.Outcome != "proposal" || m.calls != 4 || result.ToolCalls != 2 || result.Validation == nil || len(result.Candidates) == 0 {
		t.Fatalf("result=%+v error=%v", result, e)
	}
	if state.Fields["noise_pref"].Strength != "must" {
		t.Fatal("caller requirement mutated")
	}
}

func TestMissingFieldsAndEmptyCatalogStillReachModel(t *testing.T) {
	m := &scriptedModel{respond: func(_ int, r *model.LLMRequest) *genai.Content {
		return genai.NewContentFromText(`{"outcome":"clarify","reply":"你更在意便携还是扩展空间？","issues":[],"assumptions":[]}`, genai.RoleModel)
	}}
	result, e := (Runner{Model: m, Catalog: recordedCatalog{}}).Run(context.Background(), schemas.PlanningInput{SchemaVersion: 2, State: schemas.NewRequirementState()})
	if e != nil || m.calls != 1 || result.Outcome != "clarify" {
		t.Fatalf("%+v %v", result, e)
	}
}

func TestToolLimitReturnsProgress(t *testing.T) {
	catalog, _ := fixture(t)
	m := &scriptedModel{respond: func(_ int, _ *model.LLMRequest) *genai.Content { return function("search_local", `{}`) }}
	result, e := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), schemas.PlanningInput{SchemaVersion: 2, State: schemas.NewRequirementState()})
	if e != nil || m.calls != 8 || result.Outcome != "proposal" || len(result.Issues) == 0 {
		t.Fatalf("%+v %v", result, e)
	}
}

func TestEvidenceRegistrationDoesNotPublishOrInventSpecs(t *testing.T) {
	x := execution{evidence: []Evidence{{ID: "s", Kind: "page", Text: "Example Cooler is 150 mm tall", URL: "https://example.com/spec"}}}
	c := Candidate{ID: "ext-cooler", Category: schemas.CategoryCooler, Model: "Example Cooler", Specs: json.RawMessage(`{"height_mm":150,"cooling_capacity_w":999}`), Evidence: []string{"s"}, FieldEvidence: map[string]string{"model": "s", "height_mm": "s"}, FieldQuotes: map[string]string{"height_mm": "150 mm tall"}}
	if e := x.register(c); e != nil {
		t.Fatal(e)
	}
	if len(x.candidates) != 1 || !strings.Contains(string(x.candidates[0].Specs), "150") || strings.Contains(string(x.candidates[0].Specs), "999") || len(x.candidates[0].Unknown) == 0 {
		t.Fatalf("unverified field accepted: %+v", x.candidates)
	}
}

func TestCompatibilityFailureIsReturnedToModelForRepair(t *testing.T) {
	catalog, draft := fixture(t)
	var badID, goodID string
	for _, c := range catalog.Candidates {
		if c.Category == schemas.CategoryMotherboard {
			goodID = c.SKU
			c.SKU = "test-incompatible-board"
			badID = c.SKU
			var specs map[string]any
			_ = json.Unmarshal(c.Specs, &specs)
			specs["socket"] = "incompatible"
			c.Specs, _ = json.Marshal(specs)
			catalog.Candidates = append(catalog.Candidates, c)
			break
		}
	}
	bad := strings.Replace(string(draft), goodID, badID, 1)
	m := &scriptedModel{respond: func(call int, r *model.LLMRequest) *genai.Content {
		if call == 1 {
			return function("evaluate", `{"draft":`+bad+`}`)
		}
		if call == 2 {
			report := r.Contents[len(r.Contents)-1].Parts[0].FunctionResponse.Response["validation"].(schemas.ValidationReport)
			if report.OverallStatus != schemas.OverallFail {
				t.Fatalf("real socket conflict hidden: %+v", report)
			}
			return function("evaluate", `{"draft":`+string(draft)+`}`)
		}
		return genai.NewContentFromText(`{"outcome":"ready","reply":"已更换为兼容主板","issues":[],"assumptions":[],"assessments":[]}`, genai.RoleModel)
	}}
	result, err := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), schemas.PlanningInput{SchemaVersion: 2, State: schemas.NewRequirementState()})
	if err != nil || result.Outcome != "ready" || result.Validation.OverallStatus != schemas.OverallPass || !strings.Contains(string(result.Draft), goodID) {
		t.Fatalf("repair not applied %+v %v", result, err)
	}
}

func TestLocalQueryRanksWithoutErasingUnmatchedAlternatives(t *testing.T) {
	catalog, _ := fixture(t)
	m := &scriptedModel{respond: func(call int, r *model.LLMRequest) *genai.Content {
		if call == 1 {
			return function("search_local", `{"query":"不存在的品牌 静音","category":"psu"}`)
		}
		got := r.Contents[len(r.Contents)-1].Parts[0].FunctionResponse.Response["candidates"].([]Candidate)
		if len(got) == 0 {
			t.Fatal("query turned user requirement into an empty candidate gate")
		}
		return genai.NewContentFromText(`{"outcome":"clarify","reply":"可先比较这些电源的风扇曲线","issues":["噪声资料待核实"]}`, genai.RoleModel)
	}}
	result, err := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), schemas.PlanningInput{SchemaVersion: 2, State: schemas.NewRequirementState()})
	if err != nil || len(result.Candidates) == 0 {
		t.Fatalf("researched candidates lost %+v %v", result, err)
	}
}

func TestExternalSpecsValidateWithoutImportingWebPrices(t *testing.T) {
	catalog, draft := fixture(t)
	var selected store.Candidate
	for _, c := range catalog.Candidates {
		if c.Category == schemas.CategoryCooler {
			selected = c
			break
		}
	}
	// A saved product/spec/price in an offline HTML fixture: incidental web
	// prices must not become quotes, while valid specifications still validate.
	pageText := selected.Model + " " + string(selected.Specs) + " price " + *selected.PriceCNY
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html><body>"+pageText+"</body></html>") }))
	defer server.Close()
	var specs map[string]json.RawMessage
	_ = json.Unmarshal(selected.Specs, &specs)
	refs := map[string]string{"model": "source-1", "price_cny": "source-1"}
	quotes := map[string]string{"price_cny": pageText}
	for key := range specs {
		refs[key] = "source-1"
		quotes[key] = pageText
	}
	candidate := Candidate{ID: "ext-recorded-cooler", Category: selected.Category, Brand: selected.Brand, Model: selected.Model, Specs: selected.Specs, Price: selected.PriceCNY, Evidence: []string{"source-1"}, FieldEvidence: refs, FieldQuotes: quotes, Merchant: "recorded merchant", Currency: "CNY", PriceObservedAt: "2026-07-28"}
	data, _ := json.Marshal(candidate)
	externalDraft := strings.Replace(string(draft), selected.SKU, candidate.ID, 1)
	m := &scriptedModel{respond: func(call int, r *model.LLMRequest) *genai.Content {
		switch call {
		case 1:
			return function("read_page", `{"url":"`+server.URL+`"}`)
		case 2:
			return function("register_candidate", string(data))
		case 3:
			if r.Contents[len(r.Contents)-1].Parts[0].FunctionResponse.Response["registered"] != candidate.ID {
				t.Fatal("external registration failed")
			}
			return function("evaluate", `{"draft":`+externalDraft+`}`)
		default:
			return genai.NewContentFromText(`{"outcome":"ready","reply":"已核验所选方案","issues":[],"assumptions":[],"assessments":[]}`, genai.RoleModel)
		}
	}}
	result, err := (Runner{Model: m, Catalog: catalog, Web: &Web{Client: server.Client()}}).Run(context.Background(), schemas.PlanningInput{SchemaVersion: 2, State: schemas.NewRequirementState()})
	if err != nil || result.Outcome != "proposal" || result.Validation == nil || result.Validation.OverallStatus != schemas.OverallPass || result.Quote == nil || result.Quote.MissingCount != 1 {
		t.Fatalf("external candidate rejected: %+v %v", result, err)
	}
	found := false
	for _, c := range result.Candidates {
		if c.ID == candidate.ID {
			found = c.External && c.FieldQuotes["height_mm"] != ""
			if c.Price != nil || c.Merchant != "" || c.Currency != "" || c.PriceObservedAt != "" || c.FieldEvidence["price_cny"] != "" || c.FieldQuotes["price_cny"] != "" {
				t.Fatalf("web price entered candidate quote: %+v", c)
			}
		}
	}
	if !found || len(result.Evidence) != 1 {
		t.Fatal("external source snapshot lost")
	}
	for _, c := range catalog.Candidates {
		if c.SKU == selected.SKU && (c.PriceCNY == nil || *c.PriceCNY != *selected.PriceCNY) {
			t.Fatal("local price snapshot changed")
		}
		if c.SKU == candidate.ID {
			t.Fatal("external candidate published globally")
		}
	}
}

func TestOpenRequirementsReachPlanningAndTools(t *testing.T) {
	for _, tc := range []struct {
		name, key, value string
		wantCalls        int
	}{
		// small_budget：模型已用 price_asc 检索，超预算 proposal 由压价求解器
		// 直接终局裁决（无可行替换，追加求解标注），不再走预算门回环。
		{"unlisted_workload", "free.workload", `"制作天文延时与声场测量"`, 3},
		{"small_budget", "budget_cny", `100`, 2},
		{"unknown_existing_model", "free.owned_gpu", `"已有一张老黄卡"`, 3},
		{"outside_catalog_brand", "free.brand", `"希望看看目录外品牌"`, 3},
		{"portable_shape", "free.portable", `"塞进背包且两个网口"`, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog, draft := fixture(t)
			state := schemas.NewRequirementState()
			state.Fields[tc.key] = schemas.RequirementField{Status: "active", Kind: "constraint", Strength: "must", Value: json.RawMessage(tc.value)}
			m := &scriptedModel{respond: func(call int, r *model.LLMRequest) *genai.Content {
				if call == 1 {
					if !strings.Contains(r.Contents[0].Parts[0].Text, tc.value) {
						t.Fatal("requirement missing from actual input")
					}
					return function("search_local", `{"category":"cpu","order_by":"price_asc"}`)
				}
				return genai.NewContentFromText(`{"outcome":"proposal","reply":"先保留候选并继续检索","draft":`+string(draft)+`,"issues":["要求仍需比较核实"],"assessments":[],"assumptions":[]}`, genai.RoleModel)
			}}
			result, err := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), schemas.PlanningInput{SchemaVersion: 2, State: state})
			if err != nil || result.ModelCalls != tc.wantCalls || result.ToolCalls != 1 || len(result.Candidates) == 0 || result.Validation == nil || state.Fields[tc.key].Strength != "must" {
				t.Fatalf("flow blocked or state weakened: %+v %v", result, err)
			}
		})
	}
}
