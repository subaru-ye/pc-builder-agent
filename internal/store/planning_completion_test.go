package store

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func planningCompletionFixture(t *testing.T, s *Store) CompletePlanningParams {
	t.Helper()
	ctx := context.Background()
	id := uuid.NewString()
	run := uuid.NewString()
	if _, err := s.CreateWebSession(ctx, id, strings.Repeat("p", 43), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO agent_runs(id,session_id,client_request_id,kind,status) VALUES($1,$2,$3,'build','running')`, run, id, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	return CompletePlanningParams{Completion: CompleteRunParams{RunID: run, SessionID: id, AssistantMessageID: uuid.NewString(), AssistantContent: "录制选型说明", Status: RunSucceeded, Phase: PhaseReady}, Requirement: json.RawMessage(`{"schema_version":2,"requirement_state":{"revision":0}}`), Result: json.RawMessage(`{"outcome":"ready","model_outcome":"proposal","issues":[]}`), Build: &SaveBuildVersionParams{SessionID: id, RequirementSpec: json.RawMessage(`{"schema_version":2}`), Draft: json.RawMessage(`{"build_ref":"recording"}`), Validation: json.RawMessage(`{"overall_status":"pass"}`), Quote: json.RawMessage(`{"total_cny":"4579.90"}`)}}
}

func TestPlanningCompletionAtomicAndIdempotent(t *testing.T) {
	s := setupStore(t)
	p := planningCompletionFixture(t, s)
	ctx := context.Background()
	var wg sync.WaitGroup
	for n := 0; n < 4; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := s.CompletePlanningRun(ctx, p)
			if err != nil || out.Version != 1 {
				t.Errorf("completion=%+v %v", out, err)
			}
		}()
	}
	wg.Wait()
	var builds, proposals, messages, linked int
	for query, target := range map[string]*int{"SELECT count(*) FROM builds": &builds, "SELECT count(*) FROM session_proposals": &proposals, "SELECT count(*) FROM web_messages": &messages, "SELECT count(*) FROM session_proposals p JOIN builds b ON b.id=p.build_id AND b.session_id=p.session_id": &linked} {
		if err := s.pool.QueryRow(ctx, query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if builds != 1 || proposals != 1 || messages != 1 || linked != 1 {
		t.Fatalf("non-atomic counts %d %d %d %d", builds, proposals, messages, linked)
	}
	var status, phase, reply string
	if err := s.pool.QueryRow(ctx, `SELECT r.status,s.phase,m.content FROM agent_runs r JOIN web_sessions s ON s.id=r.session_id JOIN web_messages m ON m.run_id=r.id WHERE r.id=$1`, p.Completion.RunID).Scan(&status, &phase, &reply); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || phase != "ready" || !strings.Contains(reply, "v1") {
		t.Fatal(status, phase, reply)
	}
}

func TestPlanningCompletionRollbackAndStaleState(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	p := planningCompletionFixture(t, s)
	p.Completion.AssistantMessageID = "not-a-uuid" // Fail after writing build + proposal inside the transaction.
	if _, err := s.CompletePlanningRun(ctx, p); err == nil {
		t.Fatal("expected message insert failure")
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM builds)+(SELECT count(*) FROM requirements)+(SELECT count(*) FROM session_proposals)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial writes %d %v", count, err)
	}
	p.Completion.AssistantMessageID = uuid.NewString()
	if _, err := s.pool.Exec(ctx, `UPDATE web_sessions SET requirement_state=jsonb_set(requirement_state,'{revision}','1'),phase='requirement_ready' WHERE id=$1`, p.Completion.SessionID); err != nil {
		t.Fatal(err)
	}
	out, err := s.CompletePlanningRun(ctx, p)
	if err != nil || out.Version != 0 || !strings.Contains(string(out.Result), `"stale"`) {
		t.Fatalf("stale delivered %+v %v", out, err)
	}
	var revision int
	if err := s.pool.QueryRow(ctx, `SELECT (requirement_state->>'revision')::int FROM web_sessions WHERE id=$1`, p.Completion.SessionID).Scan(&revision); err != nil || revision != 1 {
		t.Fatal("current state overwritten", err)
	}
}
