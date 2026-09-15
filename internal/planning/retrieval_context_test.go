package planning

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestOwnedFactsReachModelWithoutGuessingOrChangingState(t *testing.T) {
	for _, status := range []string{"active", "removed", "unknown", "conflict"} {
		t.Run(status, func(t *testing.T) {
			catalog := recordedCatalog{store.CatalogSnapshot{Candidates: []store.Candidate{
				{SKU: "sample-1", Category: schemas.CategoryCPU, Model: "Sample 1"},
				{SKU: "sample-2", Category: schemas.CategoryCPU, Model: "Sample 2"},
				{SKU: "exact-a", Category: schemas.CategoryCPU, Brand: "AMD", Model: "Ryzen 5 7600", Specs: json.RawMessage(`{"socket":"AM5"}`)},
				{SKU: "exact-b", Category: schemas.CategoryCPU, Brand: "AMD", Model: "Ryzen 5 7600", Specs: json.RawMessage(`{"socket":"AM5"}`)},
				{SKU: "wrong-category", Category: schemas.CategoryGPU, Brand: "AMD", Model: "Ryzen 5 7600"},
			}}}
			state := schemas.NewRequirementState()
			state.Fields["owned_parts"] = schemas.RequirementField{Status: status, Strength: "must", Value: json.RawMessage(`[{"category":"cpu","model":"AMD Ryzen 5 7600","quantity":1},{"category":"memory","model":"金百达银爵","quantity":2}]`)}
			before, _ := json.Marshal(state)
			m := &scriptedModel{respond: func(_ int, req *model.LLMRequest) *genai.Content {
				var input struct {
					Initial []Candidate `json:"initial_candidates"`
					Owned   []struct {
						Part    schemas.OwnedPart `json:"owned_part"`
						Matches []Candidate       `json:"matches"`
					} `json:"owned_candidates"`
				}
				if err := json.Unmarshal([]byte(req.Contents[0].Parts[0].Text), &input); err != nil {
					t.Fatal(err)
				}
				for _, candidate := range input.Initial {
					if candidate.ID == "exact-a" || candidate.ID == "exact-b" {
						t.Fatal("test must place owned matches outside initial sample")
					}
				}
				if status != "active" {
					if len(input.Owned) != 0 {
						t.Fatal("inactive ownership restored")
					}
				} else {
					if len(input.Owned) != 2 || len(input.Owned[0].Matches) != 2 || len(input.Owned[1].Matches) != 0 || input.Owned[1].Part.Quantity != 2 {
						t.Fatalf("ambiguous matches or unresolved abbreviation lost: %+v", input.Owned)
					}
					for _, candidate := range input.Owned[0].Matches {
						if candidate.Price != nil || string(candidate.Specs) != `{"socket":"AM5"}` {
							t.Fatal("missing price invented or known specification lost")
						}
					}
				}
				return genai.NewContentFromText(`{"outcome":"collect","reply":"已取得已有件资料","issues":[]}`, genai.RoleModel)
			}}
			result, err := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), schemas.PlanningInput{SchemaVersion: 2, State: state})
			after, _ := json.Marshal(state)
			if err != nil || !reflect.DeepEqual(before, after) || result.ToolCalls != 0 || result.ModelCalls != 1 {
				t.Fatalf("context altered state or added calls: %+v %v", result, err)
			}
		})
	}
}

func TestLocalBatchLeavesTurnsForRealValidation(t *testing.T) {
	catalog, draft := fixture(t)
	queries := []localSearchQuery{}
	for _, category := range schemas.AllCategories {
		queries = append(queries, localSearchQuery{Category: string(category), Query: "不存在的偏好词"})
	}
	payload, _ := json.Marshal(map[string]any{"queries": queries})
	m := &scriptedModel{respond: func(call int, req *model.LLMRequest) *genai.Content {
		switch call {
		case 1:
			return function("search_local_batch", string(payload))
		case 2:
			response := req.Contents[len(req.Contents)-1].Parts[0].FunctionResponse.Response
			results := response["results"].([]map[string]any)
			if len(results) != 8 || response["remaining"].(map[string]int)["tool_calls"] != 16 {
				t.Fatalf("batch not charged per query: %+v", response)
			}
			for i, item := range results {
				found := item["result"].(map[string]any)["candidates"].([]Candidate)
				if item["index"] != i || len(found) == 0 {
					t.Fatal("query result lost or preferences filtered all candidates")
				}
				for _, c := range found {
					if string(c.Category) != queries[i].Category {
						t.Fatal("batch mixed category results")
					}
				}
			}
			return function("evaluate", `{"draft":`+string(draft)+`}`)
		default:
			response := req.Contents[len(req.Contents)-1].Parts[0].FunctionResponse.Response
			if response["validation"].(schemas.ValidationReport).OverallStatus != schemas.OverallPass {
				t.Fatal("real validation not returned to model")
			}
			return genai.NewContentFromText(`{"outcome":"ready","reply":"完成选配","issues":[]}`, genai.RoleModel)
		}
	}}
	result, err := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), schemas.PlanningInput{SchemaVersion: 2, State: schemas.NewRequirementState()})
	if err != nil || result.Outcome != "ready" || result.ToolCalls != 9 || result.ModelCalls != 3 || result.SearchRequests != 0 || result.PageCalls != 0 {
		t.Fatalf("batch did not reach delivery within original budget: %+v %v", result, err)
	}
}

func TestLocalBatchHonorsRemainingBudgetAndPreservesPendingQueries(t *testing.T) {
	catalog, _ := fixture(t)
	m := &scriptedModel{respond: func(call int, req *model.LLMRequest) *genai.Content {
		if call == 1 {
			return function("search_local", `{"category":"cpu"}`)
		}
		if call <= 4 {
			return function("search_local_batch", `{"queries":[{},{},{},{},{},{},{},{}]}`)
		}
		response := req.Contents[len(req.Contents)-1].Parts[0].FunctionResponse.Response
		if response["executed_queries"] != 7 || len(response["pending_queries"].([]*localSearchQuery)) != 1 || response["remaining"].(map[string]int)["tool_calls"] != 0 {
			t.Fatalf("partial batch exceeded cap or lost pending query: %+v", response)
		}
		return genai.NewContentFromText(`{"outcome":"collect","reply":"保留已检索候选","issues":[]}`, genai.RoleModel)
	}}
	result, err := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), schemas.PlanningInput{SchemaVersion: 2, State: schemas.NewRequirementState()})
	if err != nil || result.ToolCalls != 24 || result.ModelCalls != 5 {
		t.Fatalf("cap failed: %+v %v", result, err)
	}
}

func TestLocalSearchCoverageDoesNotRemoveRepeatedCandidates(t *testing.T) {
	x := execution{seen: map[string]bool{}, candidates: []Candidate{
		{ID: "cpu-a", Category: schemas.CategoryCPU}, {ID: "cpu-b", Category: schemas.CategoryCPU}, {ID: "gpu-a", Category: schemas.CategoryGPU},
	}}
	for i, payload := range []string{`{"category":"cpu","limit":1}`, `{"category":"cpu","limit":1}`, `{"category":"cpu","offset":1}`, `{"category":"gpu"}`} {
		response := x.call(context.Background(), map[string]any{"action": "search_local", "payload": payload})
		if len(response["candidates"].([]Candidate)) != 1 || response["new_count"] != []int{1, 0, 1, 1}[i] || response["seen_in_scope"] != []int{1, 1, 2, 1}[i] {
			t.Fatalf("incorrect search coverage: %+v", response)
		}
	}
}

func TestInvalidLocalBatchDoesNotRunQueries(t *testing.T) {
	for _, payload := range []string{`{}`, `{"queries":[]}`, `{"queries":[{},null]}`, `{"queries":[{},{},{},{},{},{},{},{},{}]}`} {
		x := execution{seen: map[string]bool{}}
		response := x.call(context.Background(), map[string]any{"action": "search_local_batch", "payload": payload})
		if response["error"] == nil || len(x.seen) != 0 || len(x.result.StageMS) != 0 {
			t.Fatalf("invalid batch ran: %+v", response)
		}
	}
}

func TestPriceOrderingAndRangeKeepUnpricedAlternatives(t *testing.T) {
	price := func(value string) *string { return &value }
	candidates := []Candidate{
		{ID: "target", Category: schemas.CategoryMemory, Model: "DDR5", Price: price("100.00")},
		{ID: "cheap", Category: schemas.CategoryMemory, Model: "DDR4", Price: price("9.99")},
		{ID: "high-a", Category: schemas.CategoryMemory, Price: price("9007199254740993.01")},
		{ID: "high-b", Category: schemas.CategoryMemory, Price: price("9007199254740993.02")},
		{ID: "unknown", Category: schemas.CategoryMemory},
		{ID: "outside", Category: schemas.CategoryCPU, Price: price("1.00")},
	}
	for _, tc := range []struct {
		order string
		ids   []string
	}{
		{"relevance", []string{"target", "cheap", "high-a", "high-b", "unknown"}},
		{"price_asc", []string{"cheap", "target", "high-a", "high-b", "unknown"}},
		{"price_desc", []string{"high-b", "high-a", "target", "cheap", "unknown"}},
	} {
		x := execution{candidates: candidates, seen: map[string]bool{}}
		payload, _ := json.Marshal(map[string]any{"category": "memory", "query": "DDR5", "order_by": tc.order})
		response := x.call(context.Background(), map[string]any{"action": "search_local", "payload": string(payload)})
		ids := []string{}
		for _, candidate := range response["candidates"].([]Candidate) {
			ids = append(ids, candidate.ID)
		}
		if !reflect.DeepEqual(ids, tc.ids) {
			t.Fatalf("%s order=%v", tc.order, ids)
		}
		rangeInfo := response["price_range_cny"].(map[string]any)
		if *rangeInfo["min"].(*string) != "9.99" || *rangeInfo["max"].(*string) != "9007199254740993.02" || rangeInfo["priced_count"] != 4 || rangeInfo["unknown_count"] != 1 {
			t.Fatalf("wrong category-wide range: %+v", rangeInfo)
		}
	}
}

func TestRecordedExpensiveMemoryQueryCanExploreAnotherPlatform(t *testing.T) {
	raw, err := os.ReadFile("../planningeval/testdata/legacy-retrieval-recheck-20260915/suite.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Catalog struct{ Candidates []Candidate }
	}
	if err = json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	x := execution{candidates: suite.Catalog.Candidates, seen: map[string]bool{}}
	// Keep the failed real query's words. Only request price ordering; the
	// service must not infer a DDR5 requirement or hide another platform.
	response := x.call(context.Background(), map[string]any{"action": "search_local", "payload": `{"category":"memory","query":"DDR5 32GB 6000 CL30 价格低","order_by":"price_asc","limit":1}`})
	found := response["candidates"].([]Candidate)
	if len(found) != 1 || found[0].Price == nil || *found[0].Price != "569.0" && *found[0].Price != "569" {
		t.Fatalf("cheaper real candidate missing: %+v", found)
	}
	var specs struct{ Generation string }
	if json.Unmarshal(found[0].Specs, &specs) != nil || specs.Generation != "ddr4" {
		t.Fatalf("unrequested platform filter applied: %+v", found)
	}
	if response["total"] != 21 || response["truncated"] != 20 {
		t.Fatalf("catalog paths removed: %+v", response)
	}
}

func TestBatchPreservesIndependentPriceOrderAndNullRange(t *testing.T) {
	value := "10.00"
	catalog := recordedCatalog{store.CatalogSnapshot{Candidates: []store.Candidate{
		{SKU: "priced", Category: schemas.CategoryCPU, PriceCNY: &value},
		{SKU: "unpriced", Category: schemas.CategoryCPU},
		{SKU: "only-unknown", Category: schemas.CategoryMemory},
	}}}
	m := &scriptedModel{respond: func(n int, req *model.LLMRequest) *genai.Content {
		if n == 1 {
			return function("search_local_batch", `{"queries":[{"category":"cpu","order_by":"price_desc","limit":1},{"category":"memory","order_by":"price_asc"},{"category":"gpu","order_by":"price_asc"}]}`)
		}
		results := req.Contents[len(req.Contents)-1].Parts[0].FunctionResponse.Response["results"].([]map[string]any)
		first := results[0]["result"].(map[string]any)
		if first["candidates"].([]Candidate)[0].ID != "priced" || first["order_by"] != "price_desc" {
			t.Fatal("batch ignored ordering")
		}
		for _, item := range results[1:] {
			rangeInfo := item["result"].(map[string]any)["price_range_cny"].(map[string]any)
			if rangeInfo["min"].(*string) != nil || rangeInfo["max"].(*string) != nil || rangeInfo["priced_count"] != 0 {
				t.Fatal("unknown price became zero")
			}
		}
		return genai.NewContentFromText(`{"outcome":"collect","reply":"保留不同路径供比较","issues":[]}`, genai.RoleModel)
	}}
	result, err := (Runner{Model: m, Catalog: catalog}).Run(context.Background(), schemas.PlanningInput{SchemaVersion: 2, State: schemas.NewRequirementState()})
	if err != nil || result.ToolCalls != 3 || result.ModelCalls != 2 {
		t.Fatalf("extra calls: %+v %v", result, err)
	}
	x := execution{seen: map[string]bool{}}
	response := x.call(context.Background(), map[string]any{"action": "search_local", "payload": `{"order_by":"cheapest"}`})
	if response["error"] == nil {
		t.Fatal("invalid order silently accepted")
	}
}
