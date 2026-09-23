package store

// 助手需求建议的持久化事务测试:同轮原子保存、紧邻轮消费、跨轮/跨会话
// 失效、并发接受互斥。PG_TEST_DSN 未设置时整体跳过。
import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
)

type proposalSessionFixture struct {
	session, owner string
	store          *Store
}

func newProposalSession(t *testing.T, s *Store, name string) proposalSessionFixture {
	t.Helper()
	ctx := context.Background()
	owner := uuid.NewString()
	ws, err := s.CreateWebSession(ctx, uuid.NewString(), owner, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	return proposalSessionFixture{session: ws.ID, owner: owner, store: s}
}

func (f proposalSessionFixture) run(t *testing.T, requestID, messageID, text string) (AgentRun, error) {
	t.Helper()
	r, duplicate, err := f.store.StartMessageRun(context.Background(), StartMessageRunParams{
		OwnerID: f.owner, SessionID: f.session, RequestID: requestID,
		RunID: uuid.NewString(), MessageID: messageID, Text: text, ForceScreening: true,
	})
	if err != nil || duplicate {
		return r, err
	}
	return r, nil
}

// 同轮原子保存 + 紧邻轮消费 + 接受后失效 + 非紧邻轮不可用。
func TestRequirementProposalAdjacentLifecycle(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	fixture := newProposalSession(t, s, "lifecycle")

	// 轮1:问预算。
	first, err := fixture.run(t, "10000000-0000-4000-8000-000000000001", "11111111-1111-4111-8111-111111111111", "7500够吗？")
	if err != nil {
		t.Fatal(err)
	}
	// 轮1 的助手回复带建议;文本未展示的建议必须被丢弃。
	_, err = s.CompleteRun(ctx, CompleteRunParams{
		RunID: first.ID, SessionID: fixture.session,
		AssistantMessageID: "22222222-2222-4222-8222-222222222222",
		AssistantContent:  "参考预算档如下。\n按 7500 元的预算继续可以吗？",
		Status:            RunSucceeded, Phase: PhaseCollecting,
		SaveProposals: []RequirementProposalSave{
			{Field: "budget_cny", Value: json.RawMessage("7500"), Text: "按 7500 元的预算继续可以吗？"},
			{Field: "noise_pref", Value: json.RawMessage(`"silent"`), Text: "没有展示过的建议"},
		},
		ConsumeMessageID: "11111111-1111-4111-8111-111111111111",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 轮2:"可以" — 建议被消费并只对本轮有效。
	second, err := fixture.run(t, "10000000-0000-4000-8000-000000000002", "33333333-3333-4333-8333-333333333333", "可以")
	if err != nil {
		t.Fatal(err)
	}
	active, err := s.ActiveRequirementProposals(ctx, fixture.session, "33333333-3333-4333-8333-333333333333")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].Field != "budget_cny" {
		t.Fatalf("只有展示过的建议、且只对紧邻轮可用: %+v", active)
	}
	// 轮2 接受并完成:标记 resolved。
	if _, err := s.CompleteRun(ctx, CompleteRunParams{
		RunID: second.ID, SessionID: fixture.session,
		AssistantMessageID: "44444444-4444-4444-8444-444444444444", AssistantContent: "已按建议记录。",
		Status: RunSucceeded, Phase: PhaseCollecting,
		ConsumeMessageID:  "33333333-3333-4333-8333-333333333333",
		ResolveProposals:  []RequirementProposalAccept{{Field: "budget_cny", Value: json.RawMessage("7500")}},
	}); err != nil {
		t.Fatal(err)
	}
	if active, err = s.ActiveRequirementProposals(ctx, fixture.session, "33333333-3333-4333-8333-333333333333"); err != nil || len(active) != 0 {
		t.Fatalf("接受后的建议必须失效: %+v err=%v", active, err)
	}

	// 轮3:再"可以" — 建议已消费,resolved,不得复用。
	if _, err := fixture.run(t, "10000000-0000-4000-8000-000000000003", "55555555-5555-4555-8555-555555555555", "可以"); err != nil {
		t.Fatal(err)
	}
	if active, err = s.ActiveRequirementProposals(ctx, fixture.session, "55555555-5555-4555-8555-555555555555"); err != nil || len(active) != 0 {
		t.Fatalf("跨轮建议不得复用: %+v err=%v", active, err)
	}
}

// 不同会话的建议互不可见;proposal 消费按会话隔离。
func TestRequirementProposalSessionIsolation(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	left := newProposalSession(t, s, "left")
	right := newProposalSession(t, s, "right")

	leftRun, err := left.run(t, "10000000-0000-4000-8000-000000000011", "66666666-6666-4666-8666-666666666661", "预算多少合适")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteRun(ctx, CompleteRunParams{
		RunID: leftRun.ID, SessionID: left.session,
		AssistantMessageID: "77777777-7777-4777-8777-777777777771", AssistantContent: "按 8000 元的预算继续可以吗？",
		Status: RunSucceeded, Phase: PhaseCollecting,
		SaveProposals:    []RequirementProposalSave{{Field: "budget_cny", Value: json.RawMessage("8000"), Text: "按 8000 元的预算继续可以吗？"}},
		ConsumeMessageID: "66666666-6666-4666-8666-666666666661",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := right.run(t, "10000000-0000-4000-8000-000000000012", "88888888-8888-4888-8888-888888888881", "可以"); err != nil {
		t.Fatal(err)
	}
	active, err := s.ActiveRequirementProposals(ctx, right.session, "88888888-8888-4888-8888-888888888881")
	if err != nil || len(active) != 0 {
		t.Fatalf("不同会话不得消费他人建议: %+v err=%v", active, err)
	}
}

// 同会话并发消息:唯一 running 索引保证串行,同一建议不会被两个用户消息
// 同时消费(紧邻语义的单消费者证明)。
func TestRequirementProposalConcurrentConsumeIsExclusive(t *testing.T) {
	s := setupStore(t)
	fixture := newProposalSession(t, s, "concurrent")
	ctx := context.Background()
	seedRun, err := fixture.run(t, "10000000-0000-4000-8000-000000000021", "99999999-9999-4999-8999-999999999991", "预算多少合适")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteRun(ctx, CompleteRunParams{
		RunID: seedRun.ID, SessionID: fixture.session,
		AssistantMessageID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", AssistantContent: "按 6000 元的预算继续可以吗？",
		Status: RunSucceeded, Phase: PhaseCollecting,
		SaveProposals:    []RequirementProposalSave{{Field: "budget_cny", Value: json.RawMessage("6000"), Text: "按 6000 元的预算继续可以吗？"}},
		ConsumeMessageID: "99999999-9999-4999-8999-999999999991",
	}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	busy := make([]bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := s.StartMessageRun(ctx, StartMessageRunParams{
				OwnerID: fixture.owner, SessionID: fixture.session,
				RequestID: uuid.NewString(), RunID: uuid.NewString(),
				MessageID: uuid.NewString(), Text: "可以", ForceScreening: true,
			})
			busy[i] = err == ErrSessionBusy
		}(i)
	}
	wg.Wait()
	if !busy[0] && !busy[1] {
		t.Fatal("并发同会话消息必须被运行互斥拦截")
	}
}
