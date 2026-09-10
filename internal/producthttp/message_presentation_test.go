package producthttp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/subaru-ye/pc-builder-agent/internal/presenter"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func TestLegacyMessageSummaryRetainsOriginalAndHistoricalVersion(t *testing.T) {
	_, service, st := requirementIntegrationAPI(t)
	ctx := context.Background()
	owner := "legacy-summary-owner"
	ws, err := service.CreateSession(ctx, owner, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/requirement_replay.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture requirementReplay
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	quote, _ := json.Marshal(fixture.Quote)
	params := store.SaveBuildVersionParams{SessionID: ws.ID, RequirementSpec: json.RawMessage(`{"schema_version":1,"budget_cny":8000,"use_case":{"type":"gaming","resolution":"2K","titles":[]}}`), Draft: fixture.Draft, Quote: quote, Validation: json.RawMessage(`{"overall_status":"pass","checks":[]}`)}
	v1, err := st.SaveBuildVersion(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	var draft presenter.WireDraft
	_ = json.Unmarshal(fixture.Draft, &draft)
	run, _, err := st.StartMessageRun(ctx, store.StartMessageRunParams{OwnerID: owner, SessionID: ws.ID, RequestID: uuid.NewString(), RunID: uuid.NewString(), MessageID: uuid.NewString(), Text: "生成方案", Title: "测试"})
	if err != nil {
		t.Fatal(err)
	}
	legacy := fmt.Sprintf("兼容性校验全部通过(pass)。配置单(build_ref: %s):\n已落库:版本 v1(会话 legacy)。", draft.BuildRef)
	_, err = st.CompleteRun(ctx, store.CompleteRunParams{RunID: run.ID, SessionID: ws.ID, AssistantMessageID: uuid.NewString(), AssistantContent: legacy, Status: store.RunSucceeded, Phase: store.PhaseReady})
	if err != nil {
		t.Fatal(err)
	}
	params.ParentID = &v1.ID
	if _, err = st.SaveBuildVersion(ctx, params); err != nil {
		t.Fatal(err)
	}
	detail, err := service.GetSession(ctx, owner, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := detail.Messages[len(detail.Messages)-1]
	if m.Content != legacy || !strings.Contains(m.DisplayContent, "v1") || strings.Contains(m.DisplayContent, "v2") || strings.Contains(m.DisplayContent, "build_ref") {
		t.Fatalf("bad historical summary: %+v", m)
	}
	stored, err := st.WebMessages(ctx, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored[len(stored)-1].DisplayContent != "" || stored[len(stored)-1].Content != legacy {
		t.Fatal("read path rewrote history")
	}
	// 无法关联到准确版本的旧记录不猜测；用户文字也不作技术词过滤。
	run, _, err = st.StartMessageRun(ctx, store.StartMessageRunParams{OwnerID: owner, SessionID: ws.ID, RequestID: uuid.NewString(), RunID: uuid.NewString(), MessageID: uuid.NewString(), Text: legacy, Title: "测试"})
	if err != nil {
		t.Fatal(err)
	}
	unmatched := strings.Replace(legacy, "v1(", "v99(", 1)
	_, err = st.CompleteRun(ctx, store.CompleteRunParams{RunID: run.ID, SessionID: ws.ID, AssistantMessageID: uuid.NewString(), AssistantContent: unmatched, Status: store.RunSucceeded, Phase: store.PhaseReady})
	if err != nil {
		t.Fatal(err)
	}
	detail, err = service.GetSession(ctx, owner, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range detail.Messages {
		if msg.Content == unmatched || msg.Role == "user" {
			if msg.DisplayContent != "" {
				t.Fatal("unmatched/user message was rewritten")
			}
		}
	}
}
