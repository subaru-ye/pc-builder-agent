package product

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
)

func TestScreeningValidationIsNotServiceUnavailable(t *testing.T) {
	st := newFakeProductStore()
	agent := &fakeAgent{store: st, screenErr: fmt.Errorf("%w: private field detail", pipeline.ErrRequirementUpdate)}
	sink := newFakeSink()
	svc, err := NewService(context.Background(), st, agent, sink)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.StartMessage(context.Background(), "owner-1", "session-1", "00000000-0000-4000-8000-000000000027", "预算6000，剪4K视频")
	if err != nil {
		t.Fatal(err)
	}
	sink.wait(t)
	var problem Problem
	if err := json.Unmarshal(st.session.LastError, &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "generation_failed" || problem.Status != 422 || strings.Contains(problem.Detail, "private") || agent.screenCalls != 1 {
		t.Fatalf("incorrect or unsafe failure: %+v", problem)
	}
}
