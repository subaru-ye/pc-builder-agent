package product

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

const RunTimeout = 10 * time.Minute

type EventSink interface {
	Append(context.Context, string, string, any) (string, error)
	Has(context.Context, string) (bool, error)
	Degraded() bool
}

type ProductStore interface {
	CreateWebSession(context.Context, string, string, string) (store.WebSession, error)
	WebSessionsByOwner(context.Context, string) ([]store.WebSession, error)
	WebSessionByOwner(context.Context, string, string) (store.WebSession, error)
	WebMessages(context.Context, string) ([]store.WebMessage, error)
	ActiveRun(context.Context, string) (*store.AgentRun, error)
	RunByOwner(context.Context, string, string) (store.AgentRun, error)
	MessageRunByRequest(context.Context, string, string, string, string) (store.AgentRun, bool, error)
	ReplacePendingRequirement(context.Context, string, string, json.RawMessage) error
	StartMessageRun(context.Context, store.StartMessageRunParams) (store.AgentRun, bool, error)
	StartConfirmRun(context.Context, store.StartConfirmRunParams) (store.AgentRun, json.RawMessage, bool, error)
	CompleteRun(context.Context, store.CompleteRunParams) (*store.WebMessage, error)
	LatestBuildVersion(context.Context, string) (int, bool, error)
	InterruptRunning(context.Context, json.RawMessage) ([]store.InterruptedRun, error)
}

type SessionDetail struct {
	Session   store.WebSession
	Messages  []store.WebMessage
	ActiveRun *store.AgentRun
	Degraded  bool
}

type StartResult struct {
	Run       store.AgentRun
	Duplicate bool
}

type Service struct {
	store  ProductStore
	agent  AgentGateway
	events EventSink

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewService(parent context.Context, st ProductStore, agent AgentGateway, events EventSink) (*Service, error) {
	if st == nil || agent == nil || events == nil {
		return nil, fmt.Errorf("product service: store/agent/events 不能为空")
	}
	ctx, cancel := context.WithCancel(parent)
	return &Service{store: st, agent: agent, events: events, ctx: ctx, cancel: cancel}, nil
}

func (s *Service) CreateSession(ctx context.Context, ownerID, requestID string) (store.WebSession, error) {
	return s.store.CreateWebSession(ctx, uuid.NewString(), ownerID, requestID)
}

func (s *Service) ListSessions(ctx context.Context, ownerID string) ([]store.WebSession, error) {
	return s.store.WebSessionsByOwner(ctx, ownerID)
}

func (s *Service) GetSession(ctx context.Context, ownerID, sessionID string) (SessionDetail, error) {
	ws, err := s.store.WebSessionByOwner(ctx, ownerID, sessionID)
	if err != nil {
		return SessionDetail{}, err
	}
	messages, err := s.store.WebMessages(ctx, sessionID)
	if err != nil {
		return SessionDetail{}, err
	}
	active, err := s.store.ActiveRun(ctx, sessionID)
	if err != nil {
		return SessionDetail{}, err
	}
	return SessionDetail{Session: ws, Messages: messages, ActiveRun: active, Degraded: s.events.Degraded()}, nil
}

func (s *Service) GetRun(ctx context.Context, ownerID, runID string) (store.AgentRun, error) {
	return s.store.RunByOwner(ctx, ownerID, runID)
}

// OwnSession 只验证匿名归属，供不需要聊天记录的配置读取接口使用。
func (s *Service) OwnSession(ctx context.Context, ownerID, sessionID string) error {
	_, err := s.store.WebSessionByOwner(ctx, ownerID, sessionID)
	return err
}

func (s *Service) ReplaceRequirement(ctx context.Context, ownerID, sessionID string, spec json.RawMessage) error {
	return s.store.ReplacePendingRequirement(ctx, ownerID, sessionID, spec)
}

func (s *Service) StartMessage(ctx context.Context, ownerID, sessionID, requestID, text string) (StartResult, error) {
	if existing, found, err := s.store.MessageRunByRequest(ctx, ownerID, sessionID, requestID, text); err != nil {
		return StartResult{}, err
	} else if found {
		return StartResult{Run: existing, Duplicate: true}, nil
	}
	ws, err := s.store.WebSessionByOwner(ctx, ownerID, sessionID)
	if err != nil {
		return StartResult{}, err
	}
	recoveryReady := ws.Phase == store.PhaseError && ws.RecoveryPhase != nil && *ws.RecoveryPhase == store.PhaseReady
	if ws.Phase == store.PhaseReady || recoveryReady {
		ok, err := s.agent.ContextAvailable(ctx, ownerID, sessionID)
		if err != nil {
			return StartResult{}, err
		}
		if !ok {
			return StartResult{}, NewProblem("context_expired", "Agent 上下文已过期", 409,
				"无法保证增量改单锁定语义，请基于当前需求新建会话整单生成。", requestID)
		}
	}
	runID := uuid.NewString()
	r, duplicate, err := s.store.StartMessageRun(ctx, store.StartMessageRunParams{
		OwnerID: ownerID, SessionID: sessionID, RequestID: requestID, RunID: runID,
		MessageID: uuid.NewString(), Text: text, Title: TitleFromText(text),
	})
	if err != nil {
		return StartResult{}, err
	}
	if duplicate {
		return StartResult{Run: r, Duplicate: true}, nil
	}
	if _, err := s.events.Append(ctx, r.ID, "run.started", map[string]any{"kind": r.Kind}); err != nil {
		log.Printf("[api] run %s 写 run.started 失败:%v", r.ID, err)
	}
	s.launch(r, ownerID, text, nil)
	return StartResult{Run: r}, nil
}

func (s *Service) StartConfirm(ctx context.Context, ownerID, sessionID, requestID string) (StartResult, error) {
	r, pending, duplicate, err := s.store.StartConfirmRun(ctx, store.StartConfirmRunParams{
		OwnerID: ownerID, SessionID: sessionID, RequestID: requestID, RunID: uuid.NewString(),
	})
	if err != nil {
		return StartResult{}, err
	}
	if duplicate {
		return StartResult{Run: r, Duplicate: true}, nil
	}
	if _, err := s.events.Append(ctx, r.ID, "run.started", map[string]any{"kind": r.Kind}); err != nil {
		log.Printf("[api] run %s 写 run.started 失败:%v", r.ID, err)
	}
	s.launch(r, ownerID, "", pending)
	return StartResult{Run: r}, nil
}

func (s *Service) launch(r store.AgentRun, ownerID, text string, payload json.RawMessage) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(s.ctx, RunTimeout)
		defer cancel()
		s.execute(ctx, r, ownerID, text, payload)
	}()
}

func (s *Service) execute(ctx context.Context, r store.AgentRun, ownerID, text string, payload json.RawMessage) {
	switch r.Kind {
	case store.RunScreening:
		s.executeScreening(ctx, r, ownerID, text)
	case store.RunBuild:
		s.executeRemote(ctx, r, ownerID, payload)
	case store.RunChange:
		s.executeChange(ctx, r, ownerID, text)
	}
}

func (s *Service) executeScreening(ctx context.Context, r store.AgentRun, ownerID, text string) {
	s.progress(ctx, r.ID, "screening", "正在整理需求", 1)
	result, err := s.agent.Screen(ctx, ownerID, r.SessionID, text)
	if err != nil {
		s.failFromError(ctx, r, err, store.PhaseCollecting, "")
		return
	}
	switch result.Kind {
	case ScreenQuestion:
		s.succeed(ctx, r, store.PhaseCollecting, nil, false, result.Text, 0)
	case ScreenRequirement:
		message := "需求已经整理好，请确认或编辑后再生成配置。"
		s.succeedRequirement(ctx, r, result.Payload, message)
	case ScreenChange:
		s.fail(ctx, r, NewProblem("schema_validation_failed", "初筛结果类型错误", 422,
			"首次需求阶段不能产生 ChangeRequest。", r.ID), store.PhaseCollecting, "")
	case ScreenInvalid:
		detail := "初筛输出不符合 RequirementSpec schema。"
		if result.Err != nil {
			detail = result.Err.Error()
		}
		s.fail(ctx, r, NewProblem("schema_validation_failed", "需求解析失败", 422, detail, r.ID),
			store.PhaseCollecting, "")
	}
}

func (s *Service) succeedRequirement(ctx context.Context, r store.AgentRun, requirement json.RawMessage, assistant string) {
	msg, err := s.store.CompleteRun(context.WithoutCancel(ctx), store.CompleteRunParams{
		RunID: r.ID, SessionID: r.SessionID, AssistantMessageID: uuid.NewString(),
		AssistantContent: assistant, Status: store.RunSucceeded, Phase: store.PhaseRequirementReady,
		PendingRequirement: requirement, SetPending: true,
	})
	if err != nil {
		log.Printf("[api] run %s 完成需求落库失败:%v", r.ID, err)
		return
	}
	if msg != nil {
		s.publish(ctx, r.ID, "assistant.completed", map[string]any{"message": messagePayload(*msg)})
	}
	s.publish(ctx, r.ID, "requirement.ready", json.RawMessage(requirement))
	s.publish(ctx, r.ID, "run.completed", map[string]any{"status": "succeeded"})
}

func (s *Service) executeChange(ctx context.Context, r store.AgentRun, ownerID, text string) {
	s.progress(ctx, r.ID, "screening", "正在理解改单要求", 1)
	if delta, ok := parseDeterministicBudgetDelta(text); ok {
		version, found, err := s.store.LatestBuildVersion(ctx, r.SessionID)
		if err != nil || !found {
			s.failInternal(ctx, r, store.PhaseReady, "")
			return
		}
		payload, err := json.Marshal(struct {
			SchemaVersion  int                  `json:"schema_version"`
			BaseBuildRef   string               `json:"base_build_ref"`
			Intent         schemas.ChangeIntent `json:"intent"`
			BudgetDeltaCNY int                  `json:"budget_delta_cny"`
		}{
			SchemaVersion:  schemas.ChangeRequestSchemaVersion,
			BaseBuildRef:   fmt.Sprintf("v%d", version),
			Intent:         schemas.IntentAdjustBudget,
			BudgetDeltaCNY: delta,
		})
		if err != nil {
			s.failInternal(ctx, r, store.PhaseReady, "")
			return
		}
		s.executeRemote(ctx, r, ownerID, payload)
		return
	}
	result, err := s.agent.Screen(ctx, ownerID, r.SessionID, text)
	if err != nil {
		s.failFromError(ctx, r, err, store.PhaseReady, "")
		return
	}
	switch result.Kind {
	case ScreenQuestion:
		s.succeed(ctx, r, store.PhaseReady, nil, false, result.Text, 0)
		return
	case ScreenRequirement:
		s.succeed(ctx, r, store.PhaseReady, nil, false,
			"这个修改需要整单重生成。请新建会话，并基于当前需求重新确认。", 0)
		return
	case ScreenInvalid:
		s.fail(ctx, r, NewProblem("schema_validation_failed", "改单解析失败", 422,
			"初筛输出不符合 ChangeRequest schema。", r.ID), store.PhaseReady, "")
		return
	case ScreenChange:
		s.executeRemote(ctx, r, ownerID, result.Payload)
	}
}

var deterministicBudgetDeltaRE = regexp.MustCompile(`^(?:(?:把)?预算)?(?:再)?(降低|减少|下调|降|减|增加|提高|上调|加)([1-9][0-9]{0,5})(?:元)?$`)

// parseDeterministicBudgetDelta 只接管无歧义的整数金额升降表达,其余语句仍交给 screening Agent。
func parseDeterministicBudgetDelta(text string) (int, bool) {
	normalized := strings.Join(strings.Fields(text), "")
	match := deterministicBudgetDeltaRE.FindStringSubmatch(normalized)
	if len(match) != 3 {
		return 0, false
	}
	amount, err := strconv.Atoi(match[2])
	if err != nil || amount <= 0 {
		return 0, false
	}
	switch match[1] {
	case "降低", "减少", "下调", "降", "减":
		return -amount, true
	default:
		return amount, true
	}
}

func (s *Service) executeRemote(ctx context.Context, r store.AgentRun, ownerID string, payload json.RawMessage) {
	s.progress(ctx, r.ID, "remote_processing", "正在生成并校验配置", 2)
	before, _, err := s.store.LatestBuildVersion(ctx, r.SessionID)
	if err != nil {
		s.failInternal(ctx, r, recoveryFor(r.Kind), "")
		return
	}
	result, err := s.agent.Remote(ctx, ownerID, r.SessionID, payload)
	if err != nil {
		s.failFromError(ctx, r, err, recoveryFor(r.Kind), "")
		return
	}
	s.progress(ctx, r.ID, "finalizing", "正在保存最终结果", 3)
	after, found, err := s.store.LatestBuildVersion(ctx, r.SessionID)
	if err != nil {
		s.failInternal(ctx, r, recoveryFor(r.Kind), result.Text)
		return
	}
	if !found || after != before+1 {
		s.fail(ctx, r, NewProblem("generation_failed", "没有生成可保存的配置", 422,
			"远程流程已经结束，但数据库没有新增预期版本。", r.ID), recoveryFor(r.Kind), result.Text)
		return
	}
	s.succeed(ctx, r, store.PhaseReady, nil, false, result.Text, after)
}

func recoveryFor(kind store.RunKind) store.SessionPhase {
	if kind == store.RunBuild {
		return store.PhaseRequirementReady
	}
	if kind == store.RunChange {
		return store.PhaseReady
	}
	return store.PhaseCollecting
}

func (s *Service) succeed(ctx context.Context, r store.AgentRun, phase store.SessionPhase,
	pending json.RawMessage, setPending bool, assistant string, buildVersion int) *store.WebMessage {
	msg, err := s.store.CompleteRun(context.WithoutCancel(ctx), store.CompleteRunParams{
		RunID: r.ID, SessionID: r.SessionID, AssistantMessageID: uuid.NewString(),
		AssistantContent: assistant, Status: store.RunSucceeded, Phase: phase,
		PendingRequirement: pending, SetPending: setPending,
	})
	if err != nil {
		log.Printf("[api] run %s 完成落库失败:%v", r.ID, err)
		return nil
	}
	if msg != nil {
		s.publish(ctx, r.ID, "assistant.completed", map[string]any{"message": messagePayload(*msg)})
	}
	if buildVersion > 0 {
		s.publish(ctx, r.ID, "build.saved", map[string]any{
			"version":   buildVersion,
			"build_url": fmt.Sprintf("/api/v1/sessions/%s/builds/%d", r.SessionID, buildVersion),
		})
	}
	s.publish(ctx, r.ID, "run.completed", map[string]any{"status": "succeeded"})
	return msg
}

func (s *Service) failFromError(ctx context.Context, r store.AgentRun, err error, recovery store.SessionPhase, assistant string) {
	if s.ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return // 进程关闭由 InterruptRunning 统一落 interrupted。
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		s.fail(ctx, r, NewProblem("run_timeout", "运行超时", 504,
			"运行超过 10 分钟，已停止本次处理，可使用新请求重试。", r.ID), recovery, assistant)
		return
	}
	s.fail(ctx, r, NewProblem("upstream_unavailable", "Agent 服务不可用", 503,
		"上游 Agent 暂时不可用，请稍后使用新请求重试。", r.ID), recovery, assistant)
}

func (s *Service) failInternal(ctx context.Context, r store.AgentRun, recovery store.SessionPhase, assistant string) {
	s.fail(ctx, r, NewProblem("internal_error", "服务内部错误", 500,
		"配置版本状态无法核对，请稍后使用新请求重试。", r.ID), recovery, assistant)
}

func (s *Service) fail(ctx context.Context, r store.AgentRun, problem Problem,
	recovery store.SessionPhase, assistant string) {
	msg, err := s.store.CompleteRun(context.WithoutCancel(ctx), store.CompleteRunParams{
		RunID: r.ID, SessionID: r.SessionID, AssistantMessageID: uuid.NewString(),
		AssistantContent: assistant, Status: store.RunFailed, Phase: store.PhaseError,
		RecoveryPhase: &recovery, Error: problem.JSON(),
	})
	if err != nil {
		log.Printf("[api] run %s 失败状态落库失败:%v", r.ID, err)
		return
	}
	if msg != nil {
		s.publish(ctx, r.ID, "assistant.completed", map[string]any{"message": messagePayload(*msg)})
	}
	s.publish(ctx, r.ID, "run.failed", problem)
	s.publish(ctx, r.ID, "run.completed", map[string]any{"status": "failed"})
}

func (s *Service) progress(ctx context.Context, runID, stage, label string, sequence int) {
	s.publish(ctx, runID, "run.progress", map[string]any{
		"stage": stage, "label": label, "sequence": sequence,
	})
}

func (s *Service) publish(ctx context.Context, runID, event string, payload any) {
	if _, err := s.events.Append(context.WithoutCancel(ctx), runID, event, payload); err != nil {
		log.Printf("[api] run %s 写事件 %s 失败:%v", runID, event, err)
	}
}

func messagePayload(m store.WebMessage) map[string]any {
	return map[string]any{
		"schema_version": 1, "id": m.ID, "role": m.Role, "content": m.Content,
		"run_id": m.RunID, "created_at": m.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func (s *Service) RecoverInterrupted(ctx context.Context) error {
	problem := NewProblem("run_interrupted", "运行已中断", 409,
		"API 进程在运行完成前退出，请使用新请求显式重试。", "startup")
	items, err := s.store.InterruptRunning(ctx, problem.JSON())
	if err != nil {
		return err
	}
	for _, item := range items {
		has, err := s.events.Has(ctx, item.ID)
		if err != nil || !has {
			continue
		}
		s.publish(ctx, item.ID, "run.failed", problem)
		s.publish(ctx, item.ID, "run.completed", map[string]any{"status": "interrupted"})
	}
	return nil
}

func (s *Service) Shutdown(ctx context.Context) error {
	s.cancel()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.RecoverInterrupted(context.WithoutCancel(ctx))
}

func TitleFromText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "新会话"
	}
	if utf8.RuneCountInString(text) <= 32 {
		return text
	}
	runes := []rune(text)
	return string(runes[:32])
}
