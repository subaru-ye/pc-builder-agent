package product

// 三轴状态(requirement_readiness / requirement_confirmation / build_relation)
// 的唯一服务端派生:Session DTO、核定预览与 admission 都消费这里的结论。
// readiness 完全来自确定性 schemas 规则;confirmation/build 是当前草稿投影、
// 确认快照与 build run 冻结关联之间的纯函数比较,不引入任何模型判断。
import (
	"encoding/json"
	"math"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type ConfirmationStatus string

const (
	ConfirmationUnconfirmed ConfirmationStatus = "unconfirmed"
	ConfirmationConfirmed   ConfirmationStatus = "confirmed"
	ConfirmationModified    ConfirmationStatus = "modified"
)

type BuildRelationStatus string

const (
	BuildNone     BuildRelationStatus = "none"
	BuildRunning  BuildRelationStatus = "running"
	BuildCurrent  BuildRelationStatus = "current"
	BuildOutdated BuildRelationStatus = "outdated"
	BuildFailed   BuildRelationStatus = "failed"
)

type ConfirmationView struct {
	Status              ConfirmationStatus
	ConfirmedRevision   *int
	ConfirmedAt         *time.Time
	ConfirmedReviewHash string
	// ReviewDiff 是当前核定预览相对确认快照的字段级差异;nil 表示不可比较
	//(无快照或缺少可投影预览),空列表表示可比较且无差异。
	ReviewDiff []schemas.RequirementReviewDiffEntry
}

type BuildRelationView struct {
	Status           BuildRelationStatus
	Version          *int
	SnapshotID       string
	ReviewHash       string
	BuilderInputHash string
	// RetryRunID 是失败重试目标 run ID;空表示当前不具备失败重试资格,
	// 前端不得从聊天消息或版本号自行寻找重试目标。
	RetryRunID string
}

// RequirementAxes 是一次会话读取派生出的完整三轴结论。ReviewSpec/ReviewHash
// 是当前草稿的核定预览;incomplete 时不伪造可确认预览(两者为空)。
type RequirementAxes struct {
	Readiness        *schemas.RequirementReadiness
	NextQuestion     *schemas.RequirementQuestion
	ReviewSpec       json.RawMessage
	ReviewHash       string
	BudgetCeilingCNY *int
	Confirmation     ConfirmationView
	Build            BuildRelationView
}

// requirementAxes 从会话行、当前确认快照与 build 运行列表派生三轴。
func requirementAxes(ws store.WebSession, confirmation store.RequirementConfirmation, hasConfirmation bool, runs []store.BuildRun, active *store.AgentRun) (RequirementAxes, error) {
	axes := RequirementAxes{}
	if len(ws.RequirementState) == 0 {
		return axes, schemas.ErrRequirementStateUnsupported
	}
	state, err := schemas.DecodeRequirementState(ws.RequirementState)
	if err != nil {
		return axes, err
	}
	spec, readiness, err := schemas.RequirementReviewSpec(state)
	if err != nil {
		return axes, err
	}
	axes.Readiness = &readiness
	if question, err := schemas.NextRequirementQuestion(state); err == nil {
		axes.NextQuestion = question
	}
	axes.ReviewSpec = spec
	if len(spec) > 0 {
		if axes.ReviewHash, err = schemas.CanonicalHash(spec); err != nil {
			return axes, err
		}
		axes.BudgetCeilingCNY = budgetCeilingCNY(spec)
	}
	// confirmation:confirmed 表示当前草稿规范化预览与最近确认快照相同;
	// 草稿不同、或已不足以投影(incomplete)时是 modified。
	if hasConfirmation {
		axes.Confirmation = ConfirmationView{
			Status: ConfirmationModified, ConfirmedRevision: &confirmation.Revision,
			ConfirmedAt: &confirmation.CreatedAt, ConfirmedReviewHash: confirmation.ReviewHash,
		}
		if axes.ReviewHash != "" && axes.ReviewHash == confirmation.ReviewHash {
			axes.Confirmation.Status = ConfirmationConfirmed
		}
	} else {
		axes.Confirmation = ConfirmationView{Status: ConfirmationUnconfirmed}
	}
	// review_diff 只在已有快照且当前预览可投影时提供;快照损坏等异常下降级
	// 为不可用(nil),不阻塞会话读取,也不编造差异。
	if hasConfirmation && len(axes.ReviewSpec) > 0 {
		if diff, err := schemas.RequirementReviewDiff(confirmation.RequirementSpec, axes.ReviewSpec); err == nil {
			axes.Confirmation.ReviewDiff = diff
		}
	}
	axes.Build = buildRelation(hasConfirmation, confirmation, axes.ReviewHash, runs, active, readiness.ConfirmationEligible)
	return axes, nil
}

// buildRelation 派生 build 关联:活动 Builder 优先;最近一次运行终态决定
// current/outdated/failed。outdated 仅在已有成功配置时成立;失败与 modified
// confirmation 可以并存。current 要求最新成功 run 关联当前确认快照且草稿
// 未修改。失败重试目标仅在最新 build run 确实失败(非取消中断)、确认仍有效
// (readiness 可确认)且当前预览与该 run 快照一致时给出,与确认入口的
// invalid_retry_target 校验保持同一口径。
func buildRelation(hasConfirmation bool, confirmation store.RequirementConfirmation, draftHash string, runs []store.BuildRun, active *store.AgentRun, confirmationEligible bool) BuildRelationView {
	view := BuildRelationView{Status: BuildNone}
	if hasConfirmation {
		view.SnapshotID, view.ReviewHash = confirmation.ID, confirmation.ReviewHash
	}
	if len(runs) == 0 {
		return view
	}
	last := runs[0]
	latestSuccess := store.BuildRun{}
	for _, run := range runs {
		if run.Status == store.RunSucceeded {
			latestSuccess = run
			break
		}
	}
	var version *int
	if latestSuccess.Version > 0 {
		v := latestSuccess.Version
		version = &v
	}
	view.Version = version
	view.BuilderInputHash = last.BuilderInputHash
	if active != nil && active.Kind == store.RunBuild {
		// 活动即最新 build run;其快照与确认指针一致(运行中不能再次确认)。
		view.Status = BuildRunning
		view.SnapshotID, view.ReviewHash, view.BuilderInputHash = last.ConfirmationID, last.ReviewHash, last.BuilderInputHash
		return view
	}
	switch last.Status {
	case store.RunFailed, store.RunInterrupted:
		// 最近一次 Builder 失败或取消中断;确认快照仍有效,历史成功配置
		// 经 version 继续可见,不误称为本次成功。
		view.Status = BuildFailed
		// 重试目标只给确实失败的 run:取消中断与确认入口的
		// invalid_retry_target 校验同口径,不提供重试 ID。
		if last.Status == store.RunFailed && confirmationEligible && hasConfirmation &&
			last.ConfirmationID != "" && last.BuilderInputHash != "" &&
			draftHash != "" && last.ReviewHash == draftHash {
			view.RetryRunID = last.ID
		}
		return view
	case store.RunRunning:
		// 无活动 run 却存在 running 行:读取竞态,按 running 呈现。
		view.Status = BuildRunning
		return view
	}
	if latestSuccess.ID == "" {
		view.Status = BuildFailed
		return view
	}
	confirmedAndUnchanged := hasConfirmation && latestSuccess.ConfirmationID == confirmation.ID &&
		draftHash != "" && draftHash == confirmation.ReviewHash
	if confirmedAndUnchanged {
		view.Status = BuildCurrent
		return view
	}
	view.Status = BuildOutdated
	return view
}

// budgetCeilingCNY 是核定面板的展示计算:budget_cny ×(1+budget_flex) 取整;
// 预算缺失时为 nil。生成侧会计使用精确有理数,这里只提供展示口径。
func budgetCeilingCNY(reviewSpec json.RawMessage) *int {
	spec, err := schemas.DecodeRequirementSpec(reviewSpec)
	if err != nil || spec.BudgetCNY <= 0 {
		return nil
	}
	ceiling := int(math.Round(float64(spec.BudgetCNY) * (1 + spec.BudgetFlex)))
	return &ceiling
}
