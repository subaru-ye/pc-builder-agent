package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestProductSessionRunLifecycle(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()

	const (
		owner       = "owner-test"
		sessionID   = "session-product-test"
		createKey   = "00000000-0000-4000-8000-000000000001"
		messageKey  = "00000000-0000-4000-8000-000000000002"
		run1        = "00000000-0000-4000-8000-000000000003"
		message1    = "00000000-0000-4000-8000-000000000004"
		assistant1  = "00000000-0000-4000-8000-000000000005"
		confirmKey  = "00000000-0000-4000-8000-000000000006"
		run2        = "00000000-0000-4000-8000-000000000007"
		confirmKey2 = "00000000-0000-4000-8000-000000000008"
		run3        = "00000000-0000-4000-8000-000000000009"
	)

	ws, err := s.CreateWebSession(ctx, sessionID, owner, createKey)
	if err != nil {
		t.Fatalf("CreateWebSession: %v", err)
	}
	if ws.Phase != PhaseCollecting || ws.Title != "新会话" {
		t.Fatalf("初始会话不正确:%+v", ws)
	}
	repeated, err := s.CreateWebSession(ctx, "ignored-new-id", owner, createKey)
	if err != nil || repeated.ID != sessionID {
		t.Fatalf("创建幂等未返回原会话:%+v err=%v", repeated, err)
	}
	if _, err := s.WebSessionByOwner(ctx, "other-owner", sessionID); !errors.Is(err, ErrWebSessionNotFound) {
		t.Fatalf("跨 owner 应隐藏为 not found,得到 %v", err)
	}

	r, duplicate, err := s.StartMessageRun(ctx, StartMessageRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: messageKey, RunID: run1,
		MessageID: message1, Text: "8000 元 2K 玩黑神话", Title: "8000 元 2K 玩黑神话",
	})
	if err != nil || duplicate || r.Kind != RunScreening {
		t.Fatalf("启动 screening 失败:run=%+v duplicate=%v err=%v", r, duplicate, err)
	}
	repeatedRun, duplicate, err := s.StartMessageRun(ctx, StartMessageRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: messageKey, RunID: "00000000-0000-4000-8000-000000000099",
		MessageID: "00000000-0000-4000-8000-000000000098", Text: "8000 元 2K 玩黑神话", Title: "ignored",
	})
	if err != nil || !duplicate || repeatedRun.ID != run1 {
		t.Fatalf("消息幂等失败:run=%+v duplicate=%v err=%v", repeatedRun, duplicate, err)
	}
	_, _, err = s.StartMessageRun(ctx, StartMessageRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: messageKey, RunID: run2,
		MessageID: assistant1, Text: "同 key 不同文本", Title: "ignored",
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("同 key 不同文本应冲突,得到 %v", err)
	}

	spec := json.RawMessage(`{"schema_version":1,"budget_cny":8000,"use_case":{"type":"gaming","resolution":"2K"}}`)
	msg, err := s.CompleteRun(ctx, CompleteRunParams{
		RunID: run1, SessionID: sessionID, AssistantMessageID: assistant1,
		AssistantContent: "需求已经整理好,请确认。", Status: RunSucceeded,
		Phase: PhaseRequirementReady, PendingRequirement: spec, SetPending: true,
	})
	if err != nil || msg == nil || msg.Role != "assistant" {
		t.Fatalf("完成 screening 失败:msg=%+v err=%v", msg, err)
	}
	ws, err = s.WebSessionByOwner(ctx, owner, sessionID)
	if err != nil || ws.Phase != PhaseRequirementReady || len(ws.PendingRequirement) == 0 || ws.Title == "新会话" {
		t.Fatalf("requirement_ready 会话不正确:%+v err=%v", ws, err)
	}

	if err := s.ReplacePendingRequirement(ctx, owner, sessionID, spec); err != nil {
		t.Fatalf("ReplacePendingRequirement:%v", err)
	}
	buildRun, pending, duplicate, err := s.StartConfirmRun(ctx, StartConfirmRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: confirmKey, RunID: run2,
	})
	if err != nil || duplicate || buildRun.Kind != RunBuild || len(pending) == 0 {
		t.Fatalf("启动 build 失败:run=%+v duplicate=%v err=%v", buildRun, duplicate, err)
	}

	problem := json.RawMessage(`{"type":"/problems/generation_failed","title":"生成失败","status":422,"code":"generation_failed","request_id":"test"}`)
	recovery := PhaseRequirementReady
	if _, err := s.CompleteRun(ctx, CompleteRunParams{
		RunID: run2, SessionID: sessionID, Status: RunFailed, Phase: PhaseError,
		RecoveryPhase: &recovery, Error: problem,
	}); err != nil {
		t.Fatalf("失败完成:%v", err)
	}
	ws, _ = s.WebSessionByOwner(ctx, owner, sessionID)
	if ws.Phase != PhaseError || ws.RecoveryPhase == nil || *ws.RecoveryPhase != PhaseRequirementReady {
		t.Fatalf("error/recovery 未持久化:%+v", ws)
	}
	if _, _, duplicate, err := s.StartConfirmRun(ctx, StartConfirmRunParams{
		OwnerID: owner, SessionID: sessionID, RequestID: confirmKey2, RunID: run3,
	}); err != nil || duplicate {
		t.Fatalf("error 状态显式重试失败:duplicate=%v err=%v", duplicate, err)
	}
}

func TestInterruptRunning(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	_, err := s.CreateWebSession(ctx, "session-interrupt", "owner", "10000000-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.StartMessageRun(ctx, StartMessageRunParams{
		OwnerID: "owner", SessionID: "session-interrupt",
		RequestID: "10000000-0000-4000-8000-000000000002",
		RunID:     "10000000-0000-4000-8000-000000000003",
		MessageID: "10000000-0000-4000-8000-000000000004",
		Text:      "测试中断", Title: "测试中断",
	})
	if err != nil {
		t.Fatal(err)
	}
	problem := json.RawMessage(`{"code":"run_interrupted"}`)
	items, err := s.InterruptRunning(ctx, problem)
	if err != nil || len(items) != 1 || items[0].RecoveryPhase != PhaseCollecting {
		t.Fatalf("InterruptRunning=%+v err=%v", items, err)
	}
	ws, _ := s.WebSessionByOwner(ctx, "owner", "session-interrupt")
	if ws.Phase != PhaseError || ws.RecoveryPhase == nil || *ws.RecoveryPhase != PhaseCollecting {
		t.Fatalf("中断后会话不正确:%+v", ws)
	}
}
