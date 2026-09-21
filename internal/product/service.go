package product

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"github.com/subaru-ye/pc-builder-agent/internal/upstream"
)

// envInt 读取非负整数环境变量;缺失或非法返回 fallback(F7 预算,0 = 不限)。
func envInt(key string) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
		return n
	}
	return 0
}

// screeningModelFor 返回执行环境的初筛模型口径;非 screening run 返回空。
// 与 builder 不同,初筛同进程执行,身份来自执行环境而非远端回传。
func (s *Service) screeningModelFor(kind store.RunKind) string {
	if kind != store.RunScreening {
		return ""
	}
	if cfg, err := modelprovider.Load(modelprovider.RoleScreening); err == nil {
		return cfg.ModelDescription()
	}
	return ""
}

// RunTimeout 与 agent_runs.expires_at(store.RunLifetime)保持单一来源。
const RunTimeout = store.RunLifetime

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
	ScreeningMessages(context.Context, string, store.RunKind) ([]store.WebMessage, error)
	ActiveRun(context.Context, string) (*store.AgentRun, error)
	RunByOwner(context.Context, string, string) (store.AgentRun, error)
	MessageRunByRequest(context.Context, string, string, string, string, ...string) (store.AgentRun, bool, error)
	ReplacePendingRequirement(context.Context, string, string, json.RawMessage) error
	StartMessageRun(context.Context, store.StartMessageRunParams) (store.AgentRun, bool, error)
	StartConfirmRun(context.Context, store.StartConfirmRunParams) (store.AgentRun, json.RawMessage, bool, error)
	CompleteRun(context.Context, store.CompleteRunParams) (*store.WebMessage, error)
	LatestBuildVersion(context.Context, string) (int, bool, error)
	InterruptRunning(context.Context, json.RawMessage) ([]store.InterruptedRun, error)
	RequestRunCancel(context.Context, string, string, string) (store.AgentRun, bool, error)
	ReclaimStaleRuns(context.Context, string) ([]store.InterruptedRun, error)
}

type SessionDetail struct {
	Proposal          json.RawMessage
	Session           store.WebSession
	Messages          []store.WebMessage
	ActiveRun         *store.AgentRun
	Degraded          bool
	RequirementStatus string
	MissingFields     []string
}

type StartResult struct {
	Run       store.AgentRun
	Duplicate bool
}

type Service struct {
	store  ProductStore
	agent  AgentGateway
	events EventSink

	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	cancels sync.Map // runID → context.CancelFunc,用户取消与 Shutdown 共用
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
	status, missing := requirementLifecycle(ws)
	s.presentMessages(ctx, sessionID, messages)
	var proposal json.RawMessage
	if st, ok := s.store.(proposalStore); ok {
		proposal, err = st.LatestProposal(ctx, sessionID)
		if err != nil {
			return SessionDetail{}, err
		}
	}
	return SessionDetail{Proposal: proposal, Session: ws, Messages: messages, ActiveRun: active, Degraded: s.events.Degraded(), RequirementStatus: status, MissingFields: missing}, nil
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
	ws, err := s.store.WebSessionByOwner(ctx, ownerID, sessionID)
	if err != nil {
		return err
	}
	if len(ws.RequirementState) > 0 {
		if ws.Phase != store.PhaseRequirementReady && !(ws.Phase == store.PhaseError && ws.RecoveryPhase != nil && *ws.RecoveryPhase == store.PhaseRequirementReady) {
			return store.ErrInvalidSessionPhase
		}
		state, err := decodeSessionRequirements(ws.RequirementState)
		if err != nil {
			return err
		}
		operations, err := requirementReplacementOperations(ws.PendingRequirement, spec)
		if err != nil {
			return err
		}
		if len(operations) == 0 {
			return nil
		}
		_, err = s.EditRequirement(ctx, ownerID, sessionID, uuid.NewString(), RequirementEdit{ExpectedRevision: state.Revision, Operations: operations})
		return err
	}
	return s.store.ReplacePendingRequirement(ctx, ownerID, sessionID, spec)
}

func (s *Service) StartMessage(ctx context.Context, ownerID, sessionID, requestID, text string) (StartResult, error) {
	s.reclaimStale(ctx, sessionID)
	if existing, found, err := s.store.MessageRunByRequest(ctx, ownerID, sessionID, requestID, text); err != nil {
		return StartResult{}, err
	} else if found {
		return StartResult{Run: existing, Duplicate: true}, nil
	}
	_, err := s.store.WebSessionByOwner(ctx, ownerID, sessionID)
	if err != nil {
		return StartResult{}, err
	}
	runID := uuid.NewString()
	r, duplicate, err := s.store.StartMessageRun(ctx, store.StartMessageRunParams{
		OwnerID: ownerID, SessionID: sessionID, RequestID: requestID, RunID: runID,
		MessageID: uuid.NewString(), Text: text, Title: TitleFromText(text),
		ForceScreening: true,
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
	s.reclaimStale(ctx, sessionID)
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
	// WithoutCancel:用户取消只走本 run 的 cancelFunc;Shutdown 显式取消所有
	// 已注册 run 后再等 wg,进程关闭语义保持由 InterruptRunning 收尾。
	// 注册在 launch 同步完成,保证 StartMessage 返回后 cancel 端点必能命中。
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), RunTimeout)
	s.cancels.Store(r.ID, cancel)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.cancels.Delete(r.ID)
			cancel()
		}()
		s.execute(ctx, r, ownerID, text, payload)
	}()
}

// RequestCancel 记录用户取消意图并取消本进程内的执行 ctx;runID 不在本进程
// (进程重启后的遗留行)时仅置位 DB 标志,由 ReclaimStaleRuns 兜底回收。
func (s *Service) RequestCancel(ctx context.Context, ownerID, sessionID, runID string) (store.AgentRun, bool, error) {
	r, requested, err := s.store.RequestRunCancel(ctx, ownerID, sessionID, runID)
	if err != nil || !requested {
		return r, requested, err
	}
	if cancel, ok := s.cancels.Load(runID); ok {
		cancel.(context.CancelFunc)()
	}
	return r, true, nil
}

// ReclaimStaleRuns 回收全部会话的遗留 running 行(启动时调用,与
// RecoverInterrupted 互为兜底;每次发消息前的会话级回收见 reclaimStale)。
func (s *Service) ReclaimStaleRuns(ctx context.Context) {
	s.reclaimStale(ctx, "")
}

// reclaimStale 回收本会话已取消/已超时的遗留 running 行并补发终态事件,
// 让被进程死亡锁死的会话在下一条消息前恢复可用。
func (s *Service) reclaimStale(ctx context.Context, sessionID string) {
	items, err := s.store.ReclaimStaleRuns(ctx, sessionID)
	if err != nil {
		log.Printf("[api] 会话 %s 回收过期运行失败:%v", sessionID, err)
		return
	}
	for _, item := range items {
		problem := NewProblem("run_cancelled", "已停止本次生成", 409,
			"本次生成已取消，此前的对话与数据保持不变，可重新发起请求。", item.ID)
		if item.CancelRequestedAt == nil {
			problem = NewProblem("run_interrupted", "运行已中断", 409,
				"运行超过时限已回收，此前的对话与数据保持不变，请使用新请求重试。", item.ID)
		}
		s.publish(ctx, item.ID, "run.cancelled", problem)
		s.publish(ctx, item.ID, "run.completed", map[string]any{"status": "interrupted"})
	}
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
	input, err := s.screenInput(ctx, r, text)
	if err == nil {
		err = s.attachRequirementState(ctx, ownerID, r, &input)
	}
	if err != nil {
		s.failInternal(ctx, r, store.PhaseCollecting, "")
		return
	}
	s.captureEvidence(ctx, r.ID, "screening_input", screeningEvidence(input))
	result, err := s.agent.Screen(ctx, ownerID, r.SessionID, input)
	if err == nil {
		s.captureEvidence(ctx, r.ID, "screening_output", map[string]any{"kind": result.Kind, "text": result.Text, "payload": result.Payload, "requirement_update": result.RequirementUpdate})
	}
	if err != nil {
		s.failFromError(ctx, r, err, store.PhaseCollecting, "")
		return
	}
	if input.RequirementState != nil {
		if result.RequirementUpdate == nil {
			s.fail(ctx, r, NewProblem("schema_validation_failed", "需求更新未保存", 422, "初筛没有返回有效的本轮需求操作，请重试。", r.ID), store.PhaseCollecting, "")
			return
		}
		state, mergeErr := schemas.ApplyRequirementUpdate(*input.RequirementState, *result.RequirementUpdate, input.RequirementSource)
		if mergeErr == nil && state.NextAction == "plan" && input.Conversation.CanPlan {
			// A follow-up chat command authorizing execution in the user's own
			// words is the confirmation; continue the same run when the store
			// supports it. First messages keep the explicit confirmation.
			continuable := false
			if _, ok := s.store.(interface {
				ContinueScreeningRun(context.Context, string, string, string, schemas.RequirementState) (store.AgentRun, json.RawMessage, error)
			}); ok {
				continuable = true
			}
			if continuable {
				mergeErr = s.planFromScreening(ctx, ownerID, r, state, input.RequirementSource)
				if mergeErr != nil {
					s.failInternal(ctx, r, store.PhaseCollecting, "")
				}
				return
			}
		}
		if mergeErr == nil && state.NextAction == "plan" && !input.Conversation.CanPlan {
			// Initial requirement confirmation remains explicit. A model action
			// cannot silently skip it or claim execution before it starts.
			state.NextAction, state.Reply = "confirm", ScreeningReadyMessage
		}
		if mergeErr == nil {
			mergeErr = s.completeRequirementState(ctx, ownerID, r, state, result.RetryCount)
		}
		if mergeErr != nil {
			s.fail(ctx, r, NewProblem("schema_validation_failed", "需求更新未保存", 422, mergeErr.Error(), r.ID), store.PhaseCollecting, "")
		}
		return
	}
	switch result.Kind {
	case ScreenQuestion:
		s.succeed(ctx, r, store.PhaseCollecting, nil, false, result.Text, 0)
	case ScreenRequirement:
		message := ScreeningReadyMessage
		s.succeedRequirement(ctx, r, result.Payload, message, result.RetryCount)
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

func (s *Service) succeedRequirement(ctx context.Context, r store.AgentRun, requirement json.RawMessage, assistant string, retries int) {
	msg, err := s.store.CompleteRun(context.WithoutCancel(ctx), store.CompleteRunParams{
		RunID: r.ID, SessionID: r.SessionID, AssistantMessageID: uuid.NewString(),
		AssistantContent: assistant, Status: store.RunSucceeded, Phase: store.PhaseRequirementReady,
		PendingRequirement: requirement, SetPending: true,
		ScreeningModel: s.screeningModelFor(r.Kind), RetryCount: retries,
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
		payload, err := s.deterministicChangePayload(ctx, r.SessionID, struct {
			SchemaVersion  int                  `json:"schema_version"`
			BaseBuildRef   string               `json:"base_build_ref"`
			Intent         schemas.ChangeIntent `json:"intent"`
			BudgetDeltaCNY int                  `json:"budget_delta_cny"`
		}{
			SchemaVersion:  schemas.ChangeRequestSchemaVersion,
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
	if target, ok := parseDeterministicGPUBrandSwap(text); ok {
		locked := make([]schemas.Category, 0, len(schemas.AllCategories)-1)
		for _, category := range schemas.AllCategories {
			if category != schemas.CategoryGPU {
				locked = append(locked, category)
			}
		}
		payload, err := s.deterministicChangePayload(ctx, r.SessionID, struct {
			SchemaVersion    int                  `json:"schema_version"`
			BaseBuildRef     string               `json:"base_build_ref"`
			Intent           schemas.ChangeIntent `json:"intent"`
			Swap             map[string]string    `json:"swap"`
			LockedCategories []schemas.Category   `json:"locked_categories"`
		}{
			SchemaVersion: schemas.ChangeRequestSchemaVersion,
			Intent:        schemas.IntentSwapPart,
			Swap: map[string]string{
				"category": "gpu", "target_hint": target,
			},
			LockedCategories: locked,
		})
		if err != nil {
			s.failInternal(ctx, r, store.PhaseReady, "")
			return
		}
		s.executeRemote(ctx, r, ownerID, payload)
		return
	}
	input, err := s.screenInput(ctx, r, text)
	if err != nil {
		s.failInternal(ctx, r, store.PhaseReady, "")
		return
	}
	s.captureEvidence(ctx, r.ID, "screening_input", screeningEvidence(input))
	result, err := s.agent.Screen(ctx, ownerID, r.SessionID, input)
	if err == nil {
		s.captureEvidence(ctx, r.ID, "screening_output", map[string]any{"kind": result.Kind, "text": result.Text, "payload": result.Payload})
	}
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

func (s *Service) deterministicChangePayload(ctx context.Context, sessionID string, value any) (json.RawMessage, error) {
	version, found, err := s.store.LatestBuildVersion(ctx, sessionID)
	if err != nil || !found {
		if err == nil {
			err = fmt.Errorf("当前会话没有可改单版本")
		}
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	base, err := json.Marshal(fmt.Sprintf("v%d", version))
	if err != nil {
		return nil, err
	}
	object["base_build_ref"] = base
	return json.Marshal(object)
}

const (
	maxScreenMessages = 6
	maxScreenRunes    = 4000
)

// screenInput 从稳定产品消息生成有界上下文。Store 会排除 RunBuild，以及产生
// build 或失败的 change assistant 交付；ADK 工具和 A2A event 从未进入 web_messages。
func (s *Service) screenInput(ctx context.Context, r store.AgentRun, current string) (ScreenInput, error) {
	messages, err := s.store.ScreeningMessages(ctx, r.SessionID, r.Kind)
	if err != nil {
		return ScreenInput{}, err
	}
	prior := make([]store.WebMessage, 0, len(messages))
	for _, message := range messages {
		if message.RunID != nil && *message.RunID == r.ID && message.Role == "user" {
			continue
		}
		prior = append(prior, message)
	}
	input := BuildScreenInput(prior, current)
	input.HasBuild = r.Kind == store.RunChange
	return input, nil
}

// BuildScreenInput 让产品请求和多轮评估共用同一上下文边界；调用方先过滤运行类型。
func BuildScreenInput(prior []store.WebMessage, current string) ScreenInput {
	if len(prior) > maxScreenMessages-1 {
		prior = prior[len(prior)-(maxScreenMessages-1):]
	}
	var lines, sources []string
	for _, message := range prior {
		role := "用户"
		if message.Role == "assistant" {
			role = "助手"
		} else {
			sources = append(sources, message.Content)
		}
		lines = append(lines, role+"："+message.Content)
	}
	lines = append(lines, "用户："+current)
	sources = append(sources, current)
	contextText := strings.Join(lines, "\n")
	runes := []rune(contextText)
	if len(runes) > maxScreenRunes {
		contextText = string(runes[len(runes)-maxScreenRunes:])
	}
	// 核验来源不得比模型所见上下文更宽；过长而未完整出现的旧消息不作证据。
	visibleSources := make([]string, 0, len(sources))
	for _, source := range sources {
		if strings.Contains(contextText, "用户："+source) {
			visibleSources = append(visibleSources, source)
		}
	}
	return ScreenInput{Text: current, Context: contextText, UserSources: visibleSources}
}

var deterministicBudgetDeltaRE = regexp.MustCompile(`^(?:(?:把)?预算)?(?:再)?(降低|减少|下调|降|减|增加|提高|上调|加)([1-9][0-9]{0,5})(?:元)?$`)
var deterministicGPUBrandRE = regexp.MustCompile(`^(?:把)?(?:显卡)?(?:换成|换为|改成|改为)(?:一张|一个)?(A卡|AMD(?:显卡)?|N卡|NVIDIA(?:显卡)?)$`)

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

// parseDeterministicGPUBrandSwap 只接管肯定、完整且无歧义的品牌改单。
// 否定、比较、多个条件或具体型号仍交给 screening 模型理解。
func parseDeterministicGPUBrandSwap(text string) (string, bool) {
	normalized := strings.ToUpper(strings.Join(strings.Fields(text), ""))
	for _, negation := range []string{"不要", "不是", "并非", "取消", "别", "不"} {
		if strings.Contains(normalized, negation) {
			return "", false
		}
	}
	match := deterministicGPUBrandRE.FindStringSubmatch(normalized)
	if len(match) != 2 {
		return "", false
	}
	if strings.HasPrefix(match[1], "A") {
		return "AMD 显卡", true
	}
	return "NVIDIA 显卡", true
}

func (s *Service) executeRemote(ctx context.Context, r store.AgentRun, ownerID string, payload json.RawMessage) {
	// F7:日 token 预算硬限(DAILY_TOKEN_BUDGET,0 = 不限);超限拒绝新规划运行。
	if budget := envInt("DAILY_TOKEN_BUDGET"); budget > 0 {
		if used, ok := s.store.(interface {
			TokensUsedToday(context.Context) (int, error)
		}); ok {
			if consumed, err := used.TokensUsedToday(ctx); err == nil && consumed >= budget {
				s.fail(ctx, r, NewProblem("daily_budget_exceeded", "今日生成额度已用完", 429,
					"今天的生成任务已达预算上限，明天可继续；已有对话与配置保持不变。", r.ClientRequestID), recoveryFor(r.Kind), "")
				return
			}
		}
	}
	s.progress(ctx, r.ID, "remote_processing", "正在生成并校验配置", 2)
	before, _, err := s.store.LatestBuildVersion(ctx, r.SessionID)
	if err != nil {
		s.failInternal(ctx, r, recoveryFor(r.Kind), "")
		return
	}
	payload, err = s.planningContext(ctx, r.SessionID, r.ID, payload)
	if err != nil {
		s.failInternal(ctx, r, recoveryFor(r.Kind), "")
		return
	}
	s.captureEvidence(ctx, r.ID, "build_input", map[string]any{"payload": payload})
	result, err := s.agent.Remote(ctx, ownerID, r.SessionID, payload)
	if err != nil {
		s.failFromError(ctx, r, err, recoveryFor(r.Kind), "")
		return
	}
	if result.Planning != nil {
		if err := s.completePlanning(ctx, r, payload, *result.Planning, before); err != nil {
			s.failInternal(ctx, r, recoveryFor(r.Kind), result.Text)
		}
		return
	}
	s.progress(ctx, r.ID, "finalizing", "正在保存最终结果", 3)
	after, found, err := s.store.LatestBuildVersion(ctx, r.SessionID)
	if err != nil {
		s.failInternal(ctx, r, recoveryFor(r.Kind), result.Text)
		return
	}
	if found && after == before+1 {
		// legacy 诊断路径（dev UI/评估）：版本由生成侧落库，按计数验收。
		s.captureEvidence(ctx, r.ID, "build_output", map[string]any{"text": result.Text, "build_version": after})
		s.succeed(ctx, r, store.PhaseReady, nil, false, result.Text, after)
		return
	}
	if result.Decision != nil {
		s.captureEvidence(ctx, r.ID, "build_output", map[string]any{"text": result.Text, "decision": result.Decision, "build_version": nil})
		s.fail(ctx, r, buildFailureProblem(result.Decision, r.ID), recoveryFor(r.Kind), result.Text)
		return
	}
	// F2:planning 结果未按协议返回（解码失败或超出传输限制）。显式失败并记录诊断，
	// 不再以"版本行是否新增"作为 planning 结果的验收依据。
	log.Printf("[api] run %s planning 结果缺失:请求载荷 %d 字节,回传文本 %d 字节", r.ID, len(payload), len(result.Text))
	s.fail(ctx, r, NewProblem("generation_failed", "生成结果传输异常", 422,
		fmt.Sprintf("生成结果未能按协议保存（运行 %s，载荷 %d 字节）。当前需求保持不变，可重试。", r.ID, len(payload)),
		r.ID), recoveryFor(r.Kind), result.Text)
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
	display := ""
	if buildVersion > 0 {
		display = s.buildMessage(ctx, r.SessionID, buildVersion)
	}
	msg, err := s.store.CompleteRun(context.WithoutCancel(ctx), store.CompleteRunParams{
		RunID: r.ID, SessionID: r.SessionID, AssistantMessageID: uuid.NewString(),
		AssistantContent: assistant, Status: store.RunSucceeded, Phase: phase,
		DisplayContent: display, BuildVersion: buildVersion,
		PendingRequirement: pending, SetPending: setPending,
		ScreeningModel: s.screeningModelFor(r.Kind),
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
	var upstreamErr *upstream.Error
	if errors.Is(err, pipeline.ErrRequirementUpdate) {
		s.fail(ctx, r, NewProblem("generation_failed", "需求暂未整理完成", 422,
			"没能可靠整理这条需求，原有需求和配置保持不变。可以重试，或分开描述预算、用途和偏好。", r.ID), recovery, assistant)
		return
	}
	if errors.As(err, &upstreamErr) && (r.Kind == store.RunScreening || r.Kind == store.RunChange) {
		switch upstreamErr.Kind {
		case upstream.KindQuota:
			s.fail(ctx, r, NewProblem("model_quota_exhausted", "模型额度已用尽", 503,
				"初筛模型额度不足；系统不会自动切换到其他模型或付费供应商。", r.ID), recovery, assistant)
			return
		case upstream.KindAuthentication:
			s.fail(ctx, r, NewProblem("model_authentication_failed", "模型鉴权失败", 503,
				"初筛模型凭据无效或无权访问当前模型，请检查供应商配置。", r.ID), recovery, assistant)
			return
		case upstream.KindRateLimit:
			s.fail(ctx, r, NewProblem("model_rate_limited", "模型请求受限", 503,
				"初筛模型触发限流，请稍后使用新请求重试。", r.ID), recovery, assistant)
			return
		case upstream.KindTimeout:
			s.fail(ctx, r, NewProblem("model_timeout", "模型响应超时", 504,
				"初筛模型未在限定时间内响应，请稍后使用新请求重试。", r.ID), recovery, assistant)
			return
		}
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
	// 用户取消不是故障:统一改记 interrupted,任何上游错误形态都不得落成
	// technical_fault/内部错误(不变量:取消 ≠ 失败)。
	status := store.RunFailed
	if s.ctx.Err() == nil && errors.Is(ctx.Err(), context.Canceled) {
		problem = NewProblem("run_cancelled", "已停止本次生成", 409,
			"本次生成已取消，此前的对话与数据保持不变，可重新发起请求。", r.ClientRequestID)
		status = store.RunInterrupted
	}
	// request_id 语义:HTTP 请求 id 归 request_id,run id 单列,日志与 DB 可对齐。
	problem.RunID = r.ID
	problem.RequestID = r.ClientRequestID
	display := problem.Title + "。"
	if problem.Detail != "" {
		display += "\n\n" + problem.Detail
	}
	// Persist a safe explanation even if the remote service returned no text.
	// Original remote content remains available as evidence when present.
	if assistant == "" {
		assistant = display
	}
	msg, err := s.store.CompleteRun(context.WithoutCancel(ctx), store.CompleteRunParams{
		RunID: r.ID, SessionID: r.SessionID, AssistantMessageID: uuid.NewString(),
		AssistantContent: assistant, Status: status, Phase: store.PhaseError,
		DisplayContent: display,
		RecoveryPhase:  &recovery, Error: problem.JSON(),
	})
	if err != nil {
		log.Printf("[api] run %s 失败状态落库失败:%v", r.ID, err)
		return
	}
	if msg != nil {
		s.publish(ctx, r.ID, "assistant.completed", map[string]any{"message": messagePayload(*msg)})
	}
	if status == store.RunInterrupted {
		s.publish(ctx, r.ID, "run.cancelled", problem)
	} else {
		s.publish(ctx, r.ID, "run.failed", problem)
	}
	s.publish(ctx, r.ID, "run.completed", map[string]any{"status": status})
}

func (s *Service) progress(ctx context.Context, runID, stage, label string, sequence int) {
	s.publish(ctx, runID, "run.progress", map[string]any{
		"stage": stage, "label": label, "sequence": sequence,
	})
}

// mirrorEvents 是要镜像进 PG 的终态事件;SSE 热路径仍走 Redis。
var mirrorEvents = map[string]bool{
	"run.completed": true, "run.failed": true, "build.saved": true, "requirement.ready": true,
}

func (s *Service) publish(ctx context.Context, runID, event string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[api] run %s 事件 %s 序列化失败:%v", runID, event, err)
		return
	}
	if _, err := s.events.Append(context.WithoutCancel(ctx), runID, event, raw); err != nil {
		log.Printf("[api] run %s 写事件 %s 失败:%v", runID, event, err)
	}
	if mirrorEvents[event] {
		if st, ok := s.store.(interface {
			AppendRunEvent(context.Context, string, string, json.RawMessage) error
		}); ok {
			if err := st.AppendRunEvent(context.WithoutCancel(ctx), runID, event, raw); err != nil {
				log.Printf("[api] run %s 镜像事件 %s 失败:%v", runID, event, err)
			}
		}
	}
}

func messagePayload(m store.WebMessage) map[string]any {
	return map[string]any{
		"schema_version": 1, "id": m.ID, "role": m.Role, "content": m.Content,
		"display_content": m.DisplayContent,
		"run_id":          m.RunID, "created_at": m.CreatedAt.UTC().Format(time.RFC3339Nano),
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
	s.cancels.Range(func(_, value any) bool {
		value.(context.CancelFunc)()
		return true
	})
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
