package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
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
	ownedRun, err := s.RunByOwner(ctx, owner, run1)
	if err != nil || ownedRun.ID != run1 {
		t.Fatalf("按所有者读取 run 失败:run=%+v err=%v", ownedRun, err)
	}
	if _, err := s.RunByOwner(ctx, "other-owner", run1); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("越权读取 run 应返回 not found,得到 %v", err)
	}
	byRequest, found, err := s.MessageRunByRequest(ctx, owner, sessionID, messageKey, "8000 元 2K 玩黑神话")
	if err != nil || !found || byRequest.ID != run1 {
		t.Fatalf("预检前读取幂等 run 失败:run=%+v found=%v err=%v", byRequest, found, err)
	}
	if _, _, err := s.MessageRunByRequest(ctx, owner, sessionID, messageKey, "不同文本"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("预检前同 key 不同文本应冲突,得到 %v", err)
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

	state, err := schemas.ApplyRequirementUpdate(schemas.NewRequirementState(), schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{
		{Op: "set", Field: "budget_cny", Value: json.RawMessage(`8000`)},
		{Op: "set", Field: "use_case.type", Value: json.RawMessage(`"gaming"`)},
		{Op: "set", Field: "use_case.resolution", Value: json.RawMessage(`"2K"`)},
		{Op: "set", Field: "existing_parts", Value: json.RawMessage(`[]`)},
	}}, schemas.RequirementSource{Kind: "edit", MessageID: message1, Quote: "8000 元 2K 玩黑神话，配件全部新买"})
	if err != nil {
		t.Fatal(err)
	}
	stateJSON, _ := json.Marshal(state)
	spec, _, err := schemas.RequirementStateSpec(state)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := s.CompleteRun(ctx, CompleteRunParams{
		RunID: run1, SessionID: sessionID, AssistantMessageID: assistant1,
		AssistantContent: "需求已经整理好,请确认。", Status: RunSucceeded,
		Phase: PhaseRequirementReady, PendingRequirement: spec, SetPending: true,
		RequirementState: stateJSON, SetRequirementState: true,
	})
	if err != nil || msg == nil || msg.Role != "assistant" {
		t.Fatalf("完成 screening 失败:msg=%+v err=%v", msg, err)
	}
	screeningMessages, err := s.ScreeningMessages(ctx, sessionID, RunScreening)
	if err != nil || len(screeningMessages) != 2 {
		t.Fatalf("同类初筛消息=%v err=%v", screeningMessages, err)
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
		RecoveryPhase: &recovery, Error: problem, AssistantMessageID: "00000000-0000-4000-8000-000000000010",
		AssistantContent: "不应进入下一轮 screening 的 builder 输出",
	}); err != nil {
		t.Fatalf("失败完成:%v", err)
	}
	screeningMessages, err = s.ScreeningMessages(ctx, sessionID, RunScreening)
	if err != nil || len(screeningMessages) != 2 {
		t.Fatalf("build 输出污染同类初筛消息:%v err=%v", screeningMessages, err)
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

func TestRunCancelAndReclaim(t *testing.T) {
	s := setupStore(t)
	ctx := context.Background()
	const (
		owner     = "owner-cancel"
		sessionID = "session-cancel"
	)
	if _, err := s.CreateWebSession(ctx, sessionID, owner, "20000000-0000-4000-8000-000000000001"); err != nil {
		t.Fatal(err)
	}
	start := func(requestID, runID, messageID, text string) AgentRun {
		t.Helper()
		r, _, err := s.StartMessageRun(ctx, StartMessageRunParams{
			OwnerID: owner, SessionID: sessionID, RequestID: requestID, RunID: runID,
			MessageID: messageID, Text: text, Title: text, ForceScreening: true,
		})
		if err != nil {
			t.Fatalf("StartMessageRun:%v", err)
		}
		return r
	}
	run := start("20000000-0000-4000-8000-000000000002", "20000000-0000-4000-8000-000000000003", "20000000-0000-4000-8000-000000000004", "取消测试")
	if run.ExpiresAt == nil || run.ExpiresAt.Sub(run.StartedAt) < 9*time.Minute {
		t.Fatalf("expires_at 未按 RunLifetime 写入:%+v", run)
	}
	// 仍在 running 且未取消未到期的行不被回收。
	if items, err := s.ReclaimStaleRuns(ctx, ""); err != nil || len(items) != 0 {
		t.Fatalf("未过期 running 不应回收:%+v err=%v", items, err)
	}

	// 重复取消幂等;错误归属与会话直接 404。
	cancelled, requested, err := s.RequestRunCancel(ctx, owner, sessionID, run.ID)
	if err != nil || !requested || cancelled.CancelRequestedAt == nil {
		t.Fatalf("RequestRunCancel=%+v requested=%v err=%v", cancelled, requested, err)
	}
	if _, requested, err := s.RequestRunCancel(ctx, owner, sessionID, run.ID); err != nil || !requested {
		t.Fatalf("重复取消应幂等:%v", err)
	}
	if _, _, err := s.RequestRunCancel(ctx, owner, sessionID, "30000000-0000-4000-8000-000000000001"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("不存在 run 应 404:%v", err)
	}
	if _, _, err := s.RequestRunCancel(ctx, "other-owner", sessionID, run.ID); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("他人 run 应 404:%v", err)
	}

	// 取消标志行回收为 interrupted,会话回到可恢复 error 阶段。
	items, err := s.ReclaimStaleRuns(ctx, sessionID)
	if err != nil || len(items) != 1 || items[0].ID != run.ID || items[0].RecoveryPhase != PhaseCollecting {
		t.Fatalf("取消行回收=%+v err=%v", items, err)
	}
	if reclaimed, err := s.RunByOwner(ctx, owner, run.ID); err != nil || reclaimed.Status != RunInterrupted || reclaimed.FinishedAt == nil {
		t.Fatalf("回收后 run 应为 interrupted:%+v err=%v", reclaimed, err)
	}
	ws, _ := s.WebSessionByOwner(ctx, owner, sessionID)
	if ws.Phase != PhaseError || ws.RecoveryPhase == nil || *ws.RecoveryPhase != PhaseCollecting {
		t.Fatalf("回收后会话不正确:%+v", ws)
	}
	if _, requested, err := s.RequestRunCancel(ctx, owner, sessionID, run.ID); err != nil || requested {
		t.Fatalf("终态 run 不可再取消:%v", err)
	}
	// 回收后会话解锁:同会话可再发消息。
	next := start("20000000-0000-4000-8000-000000000005", "20000000-0000-4000-8000-000000000006", "20000000-0000-4000-8000-000000000007", "取消后再发")
	if next.Status != RunRunning {
		t.Fatalf("回收后新 run 应可启动:%+v", next)
	}

	// 超时行回收:回写 expires_at 模拟超期。
	if _, err := s.pool.Exec(ctx, `UPDATE agent_runs SET expires_at = now() - interval '1 minute' WHERE id=$1`, next.ID); err != nil {
		t.Fatal(err)
	}
	items, err = s.ReclaimStaleRuns(ctx, "")
	if err != nil || len(items) != 1 || items[0].ID != next.ID {
		t.Fatalf("超时行回收=%+v err=%v", items, err)
	}
	if reclaimed, err := s.RunByOwner(ctx, owner, next.ID); err != nil || reclaimed.Status != RunInterrupted {
		t.Fatalf("超时回收后应为 interrupted:%+v err=%v", reclaimed, err)
	}
	var code string
	if err := s.pool.QueryRow(ctx, `SELECT error->>'code' FROM agent_runs WHERE id=$1`, next.ID).Scan(&code); err != nil || code != "run_interrupted" {
		t.Fatalf("超时回收 problem 不正确:%s err=%v", code, err)
	}
}
