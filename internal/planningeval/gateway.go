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
	"github.com/subaru-ye/pc-builder-agent/internal/decision"
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
// Intent is the optional Jev observer; it is accounted with its own cap and
// never changes the product result of a step.
type Models struct {
	Screening, Builder model.LLM
	MaxCalls           int
	Intent             decision.IntentClassifier
	MaxIntentCalls     int
	// Explicitly recorded CNY price rates per million tokens; zero means the
	// rate was not supplied and no cash estimate is reported.
	IntentInputPricePerMTok  float64
	IntentOutputPricePerMTok float64
	// Journal runs before a provider request and after each response/step. An
	// evidence write error stops execution instead of spending without a record.
	Journal func(any) error
	// BuilderHold 让 Remote 在 planning 前阻塞到 ctx 取消（v2 并发 admission
	// 观测的适配器脚本）；不产生 provider 调用。
	BuilderHold bool
}
type gateway struct {
	mu          sync.Mutex
	step        Step
	record      StepRecord
	store       *store.Store
	pages       map[string]string
	models      Models
	calls       int
	intentCalls int
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
		// 非 provider 模型（v2 阻塞 oracle）不消耗预算、不记 provider 调用。
		providerBacked := true
		if p, ok := m.live.(interface{ ProviderBacked() bool }); ok {
			providerBacked = p.ProviderBacked()
		}
		m.g.mu.Lock()
		exhausted := providerBacked && m.g.models.MaxCalls > 0 && m.g.calls >= m.g.models.MaxCalls
		if !exhausted && providerBacked {
			m.g.calls++
		}
		call := m.g.calls
		m.g.mu.Unlock()
		if exhausted {
			trace.Error = "evaluation model-call limit reached"
			yield(nil, fmt.Errorf("%s", trace.Error))
			return
		}
		if m.live != nil && providerBacked {
			if err := m.g.journal(map[string]any{"event": "model_request", "call": call, "role": m.role, "request_sha256": Hash(raw), "request": json.RawMessage(raw)}); err != nil {
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
	if g.models.Intent != nil {
		if err := g.observeIntent(ctx, &input); err != nil {
			return product.ScreenResult{}, err
		}
	}
	screenOutputs := []*genai.Content{genai.NewContentFromText(string(step.Screen), genai.RoleModel)}
	if len(step.ScreenFallback) > 0 {
		screenOutputs = append(screenOutputs, genai.NewContentFromText(string(step.ScreenFallback), genai.RoleModel))
	}
	// scripted oracle 优先于 live：v2 的种子轮是适配器输入，不花真实调用。
	live := g.models.Screening
	if len(step.Screen) > 0 {
		live = nil
	}
	m := &tracedModel{g: g, role: "screening", live: live, outputs: screenOutputs}
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

// observeIntent shadows the Screening decision point with one bounded Jev
// call. Provider/API failure is recorded on the step and leaves the product
// case untouched; an exhausted call cap or a failed pre-request journal write
// stops the harness instead of spending without accounting.
func (g *gateway) observeIntent(ctx context.Context, input *product.ScreenInput) error {
	g.mu.Lock()
	exhausted := g.models.MaxIntentCalls > 0 && g.intentCalls >= g.models.MaxIntentCalls
	if !exhausted {
		g.intentCalls++
	}
	g.mu.Unlock()
	if exhausted {
		return fmt.Errorf("Jev call limit reached (%d)", g.models.MaxIntentCalls)
	}
	in := intentInputFrom(input)
	if err := g.journal(map[string]any{"event": "intent_request", "input": in}); err != nil {
		return fmt.Errorf("persist intent request evidence: %w", err)
	}
	result, err := g.models.Intent.Classify(ctx, in)
	if jErr := g.journal(map[string]any{"event": "intent_response", "result": result, "error": fmt.Sprint(err)}); jErr != nil {
		return fmt.Errorf("persist intent response evidence: %w", jErr)
	}
	obs := &IntentObservation{RequestedModel: result.RequestedModel, DurationMS: result.Duration.Milliseconds(), Probabilities: map[string]float64{}}
	if err != nil {
		obs.ErrorClass = errClass(err)
		obs.Error = truncateErr(err)
	} else {
		obs.Prediction = string(result.Intent)
		for intent, p := range result.Probabilities {
			obs.Probabilities[string(intent)] = p
		}
		obs.Confidence = result.Confidence
		obs.SelectedProbability = result.SelectedProbability
		obs.ResponseModel = result.ResponseModel
		obs.InputTokens = result.InputTokens
		obs.OutputTokens = result.OutputTokens
	}
	g.mu.Lock()
	g.record.Intent = obs
	g.mu.Unlock()
	return nil
}

// intentInputFrom builds the bounded domain input from the exact pre-Screening
// capture. The provider never sees product.ScreenInput, quotes, parts,
// proposals, traces or prior raw user messages.
func intentInputFrom(input *product.ScreenInput) decision.IntentInput {
	turn := input.RequirementSource.Quote
	if turn == "" {
		turn = input.Text
	}
	var state json.RawMessage
	if input.RequirementState != nil {
		state = schemas.RequirementStatePromptView(*input.RequirementState)
	}
	return decision.IntentInput{
		CurrentTurn:      turn,
		RequirementState: state,
		HasBuild:         input.HasBuild,
		CanPlan:          input.Conversation.CanPlan,
		BuildVersion:     input.Conversation.BuildVersion,
		LastAssistant:    input.Conversation.LastAssistant,
	}
}
func (g *gateway) Remote(ctx context.Context, _, _ string, payload json.RawMessage) (product.RemoteResult, error) {
	var input schemas.PlanningInput
	if err := json.Unmarshal(payload, &input); err != nil {
		return product.RemoteResult{}, err
	}
	if g.models.BuilderHold {
		<-ctx.Done()
		return product.RemoteResult{}, ctx.Err()
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
