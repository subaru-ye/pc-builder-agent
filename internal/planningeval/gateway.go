package planningeval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/planning"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// Models are optional: nil means recorded/oracle responses. Supplying a model
// never exposes expectations or oracle responses to it. Web stays offline.
type Models struct {
	Screening, Builder model.LLM
	MaxCalls           int
	// Journal runs before a provider request and after each response/step. An
	// evidence write error stops execution instead of spending without a record.
	Journal func(any) error
}
type gateway struct {
	mu     sync.Mutex
	step   Step
	record StepRecord
	store  *store.Store
	pages  map[string]string
	models Models
	calls  int
}

func (g *gateway) begin(step Step) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.step = step
	g.record = StepRecord{Kind: step.Kind, Text: step.Text}
}
func (g *gateway) snapshot() StepRecord {
	g.mu.Lock()
	defer g.mu.Unlock()
	raw, _ := json.Marshal(g.record)
	var r StepRecord
	_ = json.Unmarshal(raw, &r)
	return r
}

type tracedModel struct {
	g       *gateway
	role    string
	live    model.LLM
	outputs []*genai.Content
	index   int
}

func (m *tracedModel) Name() string {
	if m.live != nil {
		return m.live.Name()
	}
	return "offline-oracle-no-provider"
}
func (m *tracedModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		start := time.Now()
		raw, _ := json.Marshal(req)
		trace := Trace{Role: m.role, Model: m.Name(), Request: raw}
		defer func() {
			trace.DurationMS = time.Since(start).Milliseconds()
			m.g.mu.Lock()
			m.g.record.Trace = append(m.g.record.Trace, trace)
			m.g.mu.Unlock()
		}()
		m.g.mu.Lock()
		exhausted := m.g.models.MaxCalls > 0 && m.g.calls >= m.g.models.MaxCalls
		if !exhausted {
			m.g.calls++
		}
		call := m.g.calls
		m.g.mu.Unlock()
		if exhausted {
			trace.Error = "evaluation model-call limit reached"
			yield(nil, fmt.Errorf("%s", trace.Error))
			return
		}
		if m.live != nil {
			if err := m.g.journal(map[string]any{"event": "model_request", "call": call, "role": m.role, "request": json.RawMessage(raw)}); err != nil {
				trace.Error = err.Error()
				yield(nil, err)
				return
			}
			trace.ProviderCalled = true
			for response, err := range m.live.GenerateContent(ctx, req, stream) {
				if journalErr := m.g.journal(map[string]any{"event": "model_response", "call": call, "response": response, "error": fmt.Sprint(err)}); journalErr != nil {
					trace.Error = journalErr.Error()
					yield(nil, journalErr)
					return
				}
				if err != nil {
					trace.Error = err.Error()
				}
				if response != nil {
					trace.Response = response.Content
					if response.UsageMetadata != nil {
						n := response.UsageMetadata.TotalTokenCount
						trace.Tokens = &n
						input, output := response.UsageMetadata.PromptTokenCount, response.UsageMetadata.CandidatesTokenCount
						trace.InputTokens, trace.OutputTokens = &input, &output
					}
				}
				if !yield(response, err) {
					return
				}
			}
			return
		}
		if m.index >= len(m.outputs) {
			trace.Error = "offline oracle exhausted; unexpected model invocation"
			yield(nil, fmt.Errorf("%s", trace.Error))
			return
		}
		trace.Response = m.outputs[m.index]
		m.index++
		yield(&model.LLMResponse{Content: trace.Response}, nil)
	}
}

func (g *gateway) journal(value any) error {
	if g.models.Journal != nil {
		return g.models.Journal(value)
	}
	return nil
}
func (g *gateway) ContextAvailable(context.Context, string, string) (bool, error) { return true, nil }
func (g *gateway) Screen(ctx context.Context, owner, id string, input product.ScreenInput) (product.ScreenResult, error) {
	g.mu.Lock()
	g.record.ScreenInput = &input
	step := g.step
	g.mu.Unlock()
	m := &tracedModel{g: g, role: "screening", live: g.models.Screening, outputs: []*genai.Content{genai.NewContentFromText(string(step.Screen), genai.RoleModel)}}
	a, err := pipeline.NewProductScreening(m)
	if err != nil {
		return product.ScreenResult{}, err
	}
	r, err := runner.New(runner.Config{AppName: "planning-eval", Agent: a, SessionService: session.InMemoryService(), AutoCreateSession: true})
	if err != nil {
		return product.ScreenResult{}, err
	}
	ctx = pipeline.WithRequirementState(ctx, *input.RequirementState, input.RequirementSource, input.Conversation)
	var text string
	for event, err := range r.Run(ctx, owner, uuid.NewString(), genai.NewContentFromText(input.Context, genai.RoleUser), agent.RunConfig{}) {
		if err != nil {
			return product.ScreenResult{}, err
		}
		if event != nil && !event.Partial && event.Content != nil {
			for _, p := range event.Content.Parts {
				if p != nil && !p.Thought {
					text += p.Text
				}
			}
		}
	}
	update, err := schemas.DecodeRequirementUpdate(pipeline.ExtractPayload(text))
	return product.ScreenResult{Kind: product.ScreenRequirement, Text: text, RequirementUpdate: &update}, err
}
func (g *gateway) Remote(ctx context.Context, _, _ string, payload json.RawMessage) (product.RemoteResult, error) {
	var input schemas.PlanningInput
	if err := json.Unmarshal(payload, &input); err != nil {
		return product.RemoteResult{}, err
	}
	g.mu.Lock()
	g.record.PlanningInput = &input
	step := g.step
	g.mu.Unlock()
	m := &tracedModel{g: g, role: "builder", live: g.models.Builder, outputs: step.Builder}
	// Construct an explicit fixture-only transport; never read .env or inherit
	// SerpAPI/Crawl4AI credentials. Unknown requests fail closed without dialing.
	w := &planning.Web{Client: &http.Client{Transport: fixtureTransport{g.pages}}, Key: "offline-fixture", BaseURL: "https://search.eval.invalid", Budget: 1000, Quota: g.store}
	result, err := (planning.Runner{Model: m, Catalog: g.store, Web: w}).Run(ctx, input)
	g.mu.Lock()
	g.record.PlanningAttempt = &result
	g.mu.Unlock()
	if err != nil {
		return product.RemoteResult{Planning: &result}, err
	}
	// Exercise the structured A2A part codec as well as the real planning loop.
	part, err := pipeline.PlanningResultPart(result)
	if err != nil {
		return product.RemoteResult{}, err
	}
	decoded := pipeline.ReadPlanningResult(part)
	if decoded == nil {
		return product.RemoteResult{}, fmt.Errorf("planning A2A result lost")
	}
	return product.RemoteResult{Text: decoded.Reply, Planning: decoded}, nil
}

type fixtureTransport struct{ pages map[string]string }

func (t fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, ok := t.pages[req.URL.String()]
	contentType := "text/html; charset=utf-8"
	if req.URL.Host == "search.eval.invalid" {
		contentType = "application/json"
		if req.URL.Path == "/account.json" {
			body = `{"plan_name":"Free","this_month_usage":0,"total_searches_left":1000}`
			ok = true
		}
		if req.URL.Path == "/search.json" {
			body, ok = t.pages["search:"+req.URL.Query().Get("q")]
		}
	}
	if !ok {
		return nil, fmt.Errorf("offline web fixture missing; network not attempted")
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}
