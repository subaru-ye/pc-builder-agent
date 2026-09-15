package producthttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/planning"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// Synthetic page and explicit oracle exercise the production read/register/
// evaluate flow. The transport has no network fallback or provider credentials.
type supplementPageTransport struct{ body string }

func (p supplementPageTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() != "https://spec.eval.invalid/motherboard" {
		return nil, fmt.Errorf("unrecorded page")
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader(p.body)), Request: r}, nil
}

func (g *planningReplayGateway) supplementReplay(ctx context.Context, input schemas.PlanningInput) (product.RemoteResult, error) {
	if _, err := schemas.DecodeBuildDraft(input.BaseDraft); err != nil {
		return product.RemoteResult{}, err
	}
	var draft map[string]json.RawMessage
	_ = json.Unmarshal(input.BaseDraft, &draft)
	var selection map[string]json.RawMessage
	_ = json.Unmarshal(draft["selection"], &selection)
	selection["motherboard"] = json.RawMessage(`"mb-offline-gap"`)
	draft["selection"], _ = json.Marshal(selection)
	raw, _ := json.Marshal(draft)
	catalog, err := g.store.ActiveCatalogSnapshot(ctx)
	if err != nil {
		return product.RemoteResult{}, err
	}
	var candidate planning.Candidate
	var original json.RawMessage
	for _, c := range catalog.Candidates {
		if c.SKU == "mb-offline-gap" {
			candidate = planning.Candidate{ID: c.SKU, Category: c.Category, Brand: c.Brand, Model: c.Model, Specs: json.RawMessage(`{"memory_speed_max_mts":3200}`)}
			original = append(json.RawMessage(nil), c.Specs...)
		}
	}
	m := &supplementReplayModel{draft: raw, candidate: candidate, read: input.Request.Quote == "读取资料补齐主板规格并继续校验"}
	web := &planning.Web{Client: &http.Client{Transport: supplementPageTransport{body: "<html><body><h1>Offline synthetic specification: " + candidate.Model + "</h1><p>Memory speed 3200 MT/s. This is an explicit synthetic test page for the isolated browser acceptance test, not a manufacturer specification or published catalog evidence.</p></body></html>"}}}
	result, err := (planning.Runner{Model: m, Catalog: g.store, Web: web}).Run(ctx, input)
	if err != nil {
		return product.RemoteResult{}, err
	}
	after, err := g.store.ActiveCatalogSnapshot(ctx)
	if err != nil {
		return product.RemoteResult{}, err
	}
	for _, c := range after.Candidates {
		if c.SKU == candidate.ID && string(c.Specs) != string(original) {
			return product.RemoteResult{}, fmt.Errorf("global catalog changed during session supplement")
		}
	}
	return product.RemoteResult{Text: result.Reply, Planning: &result}, nil
}

type supplementReplayModel struct {
	draft     json.RawMessage
	candidate planning.Candidate
	read      bool
	calls     int
}

func (*supplementReplayModel) Name() string { return "offline-synthetic-supplement" }
func (m *supplementReplayModel) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(y func(*model.LLMResponse, error) bool) {
		m.calls++
		action, payload := "", ""
		if m.read && m.calls == 1 {
			action, payload = "read_page", `{"url":"https://spec.eval.invalid/motherboard","method":"http"}`
		} else if m.read && m.calls == 2 {
			var sources []planning.Evidence
			for _, part := range req.Contents[len(req.Contents)-1].Parts {
				if part.FunctionResponse != nil {
					raw, _ := json.Marshal(part.FunctionResponse.Response["sources"])
					_ = json.Unmarshal(raw, &sources)
				}
			}
			if len(sources) != 1 {
				y(nil, fmt.Errorf("page body not read"))
				return
			}
			id := sources[0].ID
			m.candidate.Evidence = []string{id}
			m.candidate.FieldEvidence = map[string]string{"model": id, "memory_speed_max_mts": id}
			m.candidate.FieldQuotes = map[string]string{"memory_speed_max_mts": "Memory speed 3200 MT/s"}
			raw, _ := json.Marshal(m.candidate)
			action, payload = "register_candidate", string(raw)
		} else if (m.read && m.calls == 3) || (!m.read && m.calls == 1) {
			action, payload = "evaluate", `{"draft":`+string(m.draft)+`}`
		}
		var content *genai.Content
		if action != "" {
			content = &genai.Content{Role: "model", Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "planning_action", Args: map[string]any{"action": action, "payload": payload}}}}}
		} else {
			raw, _ := json.Marshal(map[string]any{"outcome": "proposal", "reply": "离线规格补充流程已执行，请查看实际核验结果。", "draft": m.draft, "issues": []string{}, "assessments": []planning.Assessment{}})
			content = genai.NewContentFromText(string(raw), genai.RoleModel)
		}
		y(&model.LLMResponse{Content: content}, nil)
	}
}
