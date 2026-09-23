package product

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	adka2a "google.golang.org/adk/v2/server/adka2a/v2"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type stoppedBuildAgent struct {
	*fakeAgent
	result RemoteResult
}

func (a *stoppedBuildAgent) Remote(context.Context, string, string, json.RawMessage) (RemoteResult, error) {
	return a.result, nil
}

func TestBuildBusinessFailurePersistsSafeReasonAndKeepsVersions(t *testing.T) {
	st := newFakeProductStore()
	st.latest = 1
	st.session.Phase = store.PhaseRequirementReady
	st.session.PendingRequirement = json.RawMessage(`{"schema_version": 2, "configuration_scope": ["tower"],"budget_cny":6000,"use_case":{"type":"productivity"}}`)
	agent := &stoppedBuildAgent{fakeAgent: &fakeAgent{store: st}, result: RemoteResult{
		Text: "internal raw diagnostic", Decision: &buildharness.Decision{Kind: "data_unavailable", Reason: "requirement_evidence_missing", Fields: []string{"noise_pref"}, Message: "private validation output"},
	}}
	sink := newFakeSink()
	svc, err := NewService(context.Background(), st, agent, sink)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.Shutdown(context.Background()) }()
	_, err = svc.StartConfirm(context.Background(), "owner-1", "session-1", "00000000-0000-4000-8000-000000000028")
	if err != nil {
		t.Fatal(err)
	}
	sink.wait(t)
	if st.latest != 1 || string(st.session.PendingRequirement) == "" {
		t.Fatal("failure changed saved build or requirement")
	}
	var problem Problem
	if err = json.Unmarshal(st.session.LastError, &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Title != "必须条件仍需核验" || !strings.Contains(problem.Detail, "静音要求") || !strings.Contains(problem.Detail, "已保存") {
		t.Fatalf("lost concrete reason: %+v", problem)
	}
	last := st.messages[len(st.messages)-1]
	if last.Content != agent.result.Text || !strings.Contains(last.DisplayContent, problem.Detail) || strings.Contains(last.DisplayContent, "private") || strings.Contains(last.DisplayContent, "internal") {
		t.Fatalf("unsafe or missing persistent display: %+v", last)
	}
}

func TestBuildFailureNeverDisplaysUnstructuredOrModelValidationText(t *testing.T) {
	for _, decision := range []*buildharness.Decision{nil, {Kind: "invalid_output", Reason: "invalid_model_output", Message: "private token and schema dump"}, {Kind: "data_unavailable", Reason: "unknown", Fields: []string{"secret-field"}, Message: "private"}} {
		problem := buildFailureProblem(decision, "r")
		if strings.Contains(problem.Detail, "private") || strings.Contains(problem.Detail, "secret") || !strings.Contains(problem.Detail, "当前需求已保存") {
			t.Fatalf("unsafe fallback: %+v", problem)
		}
	}
}

func TestRemoteDecisionDataPartSurvivesTransportCollector(t *testing.T) {
	d := &buildharness.Decision{Kind: "data_unavailable", Reason: "requirement_evidence_missing", Fields: []string{"noise_pref"}, Scope: "current_catalog", Message: "具体原因"}
	part, err := pipeline.BuildDecisionPart(d)
	if err != nil {
		t.Fatal(err)
	}
	a2aParts, err := adka2a.ToA2AParts([]*genai.Part{part}, nil)
	if err != nil {
		t.Fatal(err)
	}
	remoteParts, err := adka2a.ToGenAIParts(a2aParts)
	if err != nil {
		t.Fatal(err)
	}
	seq := func(yield func(*session.Event, error) bool) {
		ev := &session.Event{Author: "remote"}
		ev.Content = genai.NewContentFromText("原始说明", genai.RoleModel)
		ev.Content.Parts = append(ev.Content.Parts, remoteParts...)
		yield(ev, nil)
	}
	got, err := collectRemoteResult(seq, "remote")
	if err != nil || got.Text != "原始说明" || got.Decision == nil || got.Decision.Reason != d.Reason {
		t.Fatalf("result=%+v err=%v", got, err)
	}
	if pipeline.ReadBuildDecisionPart(genai.NewPartFromText(`{"type":"pc_builder.build_decision","schema_version":1}`)) != nil {
		t.Fatal("free-form text accepted as trusted decision")
	}
}
