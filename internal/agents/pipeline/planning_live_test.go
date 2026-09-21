package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"
	"github.com/subaru-ye/pc-builder-agent/internal/planning"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

type liveBudgetModel struct {
	model.LLM
	count   *int
	records *[]map[string]any
}

func (m liveBudgetModel) GenerateContent(ctx context.Context, r *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(y func(*model.LLMResponse, error) bool) {
		if *m.count >= 12 {
			y(nil, fmt.Errorf("live smoke model limit reached"))
			return
		}
		*m.count++
		for response, err := range m.LLM.GenerateContent(ctx, r, stream) {
			if response != nil {
				*m.records = append(*m.records, map[string]any{"model": m.Name(), "response": response.Content, "usage": response.UsageMetadata})
			}
			if !y(response, err) {
				return
			}
		}
	}
}

// Explicit, opt-in smoke: two Screening calls and one bounded Builder run.
// No build/session is published; candidates and output are saved as local artifacts.
func TestPlanningLiveBounded(t *testing.T) {
	if os.Getenv("PLANNING_LIVE") != "1" {
		t.Skip("explicit live smoke only")
	}
	dotenv.Load("../../../.env")
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	st, err := store.New(ctx, os.Getenv("PG_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	count := 0
	records := []map[string]any{}
	var result planning.Result
	defer func() {
		raw, _ := json.MarshalIndent(map[string]any{"model_requests": count, "result": result, "records": records}, "", "  ")
		_ = os.MkdirAll("../../../artifacts/planning", 0755)
		_ = os.WriteFile("../../../artifacts/planning/live-smoke.json", raw, 0600)
	}()
	load := func(role modelprovider.Role, expected string) model.LLM {
		cfg, e := modelprovider.Load(role)
		if e != nil {
			t.Fatal(e)
		}
		if cfg.Model != expected || len(cfg.ModelChain) > 0 || cfg.MaxRetries != 0 {
			t.Fatalf("fixed model/zero-retry configuration required for %s", role)
		}
		llm, e := modelprovider.NewChat(ctx, cfg, "planning_smoke")
		if e != nil {
			t.Fatal(e)
		}
		return liveBudgetModel{LLM: llm, count: &count, records: &records}
	}
	screen := screeningGuard{LLM: load(modelprovider.RoleScreening, "deepseek-v4-flash-0731")}
	builder := load(modelprovider.RoleBuilder, "deepseek-v4-flash-0731")
	state := schemas.NewRequirementState()
	for i, text := range []string{"预算6000元，主要剪4K视频，尽量安静", "静音改成必须满足，其他要求不变"} {
		source := schemas.RequirementSource{Kind: "chat", MessageID: fmt.Sprint(i + 1), Quote: text}
		for response, e := range screen.GenerateContent(WithRequirementState(ctx, state, source), &model.LLMRequest{Model: screen.Name()}, false) {
			if e != nil {
				t.Fatal(e)
			}
			update, e := schemas.DecodeRequirementUpdate([]byte(screeningText(response.Content)))
			if e != nil {
				t.Fatal(e)
			}
			state, e = schemas.ApplyRequirementUpdate(state, update, source)
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	if state.Fields["noise_pref"].Strength != "must" {
		t.Fatal("live screening did not preserve must")
	}
	result, err = (planning.Runner{Model: builder, Catalog: st, Web: planning.NewWeb(st)}).Run(ctx, schemas.PlanningInput{SchemaVersion: 2, State: state})
	t.Logf("model_requests=%d tool_calls=%d external_searches=%d outcome=%s tokens=%d duration_ms=%d", count, result.ToolCalls, result.SearchRequests, result.Outcome, result.Tokens, result.DurationMS)
	if err != nil {
		t.Fatal(err)
	}
	if result.ToolCalls == 0 || (result.Outcome == "proposal" && len(result.Candidates) == 0) {
		t.Fatal("live result did not produce tool-backed candidates")
	}
	if count > 12 || result.SearchRequests > 6 {
		t.Fatal("live budget exceeded")
	}
}

// Continue the already-recorded smoke within its original cumulative 12-call cap.
func TestPlanningLiveContinuation(t *testing.T) {
	if os.Getenv("PLANNING_LIVE_CONTINUE") != "1" {
		t.Skip("explicit continuation only")
	}
	dotenv.Load("../../../.env")
	var prior struct {
		Count   int             `json:"model_requests"`
		Result  planning.Result `json:"result"`
		Records []struct {
			Response *genai.Content `json:"response"`
		} `json:"records"`
	}
	raw, err := os.ReadFile("../../../artifacts/planning/live-smoke.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &prior); err != nil {
		t.Fatal(err)
	}
	if prior.Count < 1 || prior.Count >= 12 {
		t.Fatal("no remaining live model budget")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	st, err := store.New(ctx, os.Getenv("PG_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg, err := modelprovider.Load(modelprovider.RoleBuilder)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "qwen3.8-max-0902" || len(cfg.ModelChain) > 0 || cfg.MaxRetries != 0 {
		t.Fatal("model config changed")
	}
	llm, err := modelprovider.NewChat(ctx, cfg, "planning_smoke_continue")
	if err != nil {
		t.Fatal(err)
	}
	state := schemas.NewRequirementState()
	for i, text := range []string{"预算6000元，主要剪4K视频，尽量安静", "静音改成必须满足，其他要求不变"} {
		update, e := schemas.DecodeRequirementUpdate([]byte(screeningText(prior.Records[i].Response)))
		if e != nil {
			t.Fatal(e)
		}
		source := schemas.RequirementSource{Kind: "chat", MessageID: fmt.Sprint(i + 1), Quote: text}
		update = prepareRequirementUpdate(state, update, source)
		state, e = schemas.ApplyRequirementUpdate(state, update, source)
		if e != nil {
			t.Fatal(e)
		}
	}
	records := []map[string]any{}
	count := prior.Count
	previous, _ := json.Marshal(prior.Result)
	result, err := (planning.Runner{Model: liveBudgetModel{LLM: llm, count: &count, records: &records}, Catalog: st, Web: planning.NewWeb(st), MaxTurns: 12 - count}).Run(ctx, schemas.PlanningInput{SchemaVersion: 2, State: state, PreviousProposal: previous})
	output, _ := json.MarshalIndent(map[string]any{"cumulative_model_requests": count, "cumulative_external_searches": prior.Result.SearchRequests + result.SearchRequests, "result": result, "records": records}, "", "  ")
	_ = os.WriteFile("../../../artifacts/planning/live-continuation.json", output, 0600)
	t.Logf("cumulative_models=%d searches=%d outcome=%s candidates=%d tokens=%d", count, prior.Result.SearchRequests+result.SearchRequests, result.Outcome, len(result.Candidates), result.Tokens)
	if err != nil {
		t.Fatal(err)
	}
	if count > 12 || prior.Result.SearchRequests+result.SearchRequests > 6 {
		t.Fatal("cumulative budget exceeded")
	}
	if len(result.Candidates) == 0 {
		t.Fatal("no concrete candidate progress")
	}
}
