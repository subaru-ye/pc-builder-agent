package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"math"
	"strconv"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/tools"
	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/evalmetrics"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// 会话状态键(P2 流水线设计 §6;P4/P6 扩展时字段口径直接搬)。
const (
	stateKeyRequirementSpec = "requirement_spec"          // 初筛 Agent OutputKey
	stateKeyBuildDraft      = "build_draft"               // 生成 Agent OutputKey
	stateKeyReport          = "validation_report"         // 最新 ValidationReport JSON
	stateKeyLastSelection   = "pipeline_last_selection"   // 上一轮 selection 规范键(死循环检测)
	stateKeyRound           = "pipeline_validation_round" // 已完成的校验轮数
)

// maxLoopRounds Loop 最大轮数 N(P2 流水线设计 §5、§9:定为代码常量)。
const maxLoopRounds = 3

const validatorAgentName = "validator_agent"

// verdict 校验节点单轮裁决:message 进会话历史(回喂生成 Agent 或面向用户交付);
// escalate=true 让 LoopAgent 出栈(三停止条件之一命中);deliver=true 表示本轮交付
// 有效配置(pass/review),需落库为新版本(P4),draft/res 为落库素材。
type verdict struct {
	message    string // 空则本轮不产出文本(如无草稿可校验时静默出栈)
	reportJSON string // 非空则写入 validation_report 状态键
	selection  string // 非空则更新 pipeline_last_selection
	escalate   bool
	deliver    bool
	draft      schemas.BuildDraft
	res        validate.Result
}

// decide 校验节点决策核心(纯函数,可无 ADK 单测)。
// 错误分类 → 策略映射(P2 流水线设计 §5):
//   - 无草稿(初筛在追问 / 生成 Agent 等待需求)→ 静默出栈;
//   - schema 错 / 未知 SKU / 规则 fail → 回喂具体问题,计入轮数;
//   - 连续两轮完全相同 selection → 死循环,立即出栈;
//   - 轮数用尽 → 带最后一版报告如实出栈,不展示假成功(§5 熔断纪律);
//   - 其他错误(DB 不可达等)→ 不可恢复,立即熔断;
//   - 改单模式下硬锁定品类被改动 → 确定性锁定校验打回(FR-402,不靠提示词),计入轮数;
//   - pass → 交付配置单 + 报价 + 快照日期,出栈;
//   - review 中含可消除的 unknown 且尚有轮次 → 回馈缺失字段,要求定向换用数据完整候选;
//   - 纯 warning review 或最后一轮 review → 如实交付并列出注意项。
func decide(ctx context.Context, eval tools.BuildEvaluator, draftText, lastSelection string, round int, chg *changeCtx) verdict {
	// legacy 的检索工具没有 must/prefer 合约，不能静默将当前会话硬条件软化。
	// 在任何交付或规则核验之前明确停止，旧的无强度需求维持原行为。
	if chg != nil && len(chg.ActiveSpec) > 0 {
		if spec, err := schemas.DecodeRequirementSpec(chg.ActiveSpec); err == nil && len(spec.ConstraintStrengths) > 0 {
			return verdict{message: "当前需求包含必须满足与尽量满足的约束，请使用默认 Harness v2 继续。legacy 尚不支持该约束合约，本轮不交付配置。", escalate: true}
		}
	}
	finalRound := round >= maxLoopRounds

	draft, err := extractBuildDraft(draftText)
	if errors.Is(err, errNoDraft) {
		// 生成 Agent 没有产出 JSON(需求未确认等),没有可校验对象,流水线本轮结束。
		return verdict{escalate: true}
	}
	if err != nil {
		if finalRound {
			return verdict{
				message:  fmt.Sprintf("配置生成失败:第 %d/%d 轮产出的 BuildDraft 仍不符合 schema(%v),重试轮数用尽,如实终止。", round, maxLoopRounds, err),
				escalate: true,
			}
		}
		return verdict{
			message: fmt.Sprintf("BuildDraft schema 不合法(第 %d/%d 轮):%v。请修正后重新只输出一个符合 schema 的 JSON 对象;到第 %d 轮仍失败将如实终止。", round, maxLoopRounds, err, maxLoopRounds),
		}
	}

	selKey := canonicalSelection(draft.Selection)

	// P4 锁定校验(先于规则引擎):硬锁定品类必须照抄基版本 SKU。
	if chg != nil && len(chg.Locked) > 0 && len(chg.BaseSelection) > 0 {
		viol, err := lockedViolations(chg.Locked, chg.BaseSelection, draft.Selection)
		if err != nil {
			return verdict{
				message:  fmt.Sprintf("校验节点内部错误(锁定比对失败),流水线熔断:%v", err),
				escalate: true,
			}
		}
		if len(viol) > 0 {
			detail := strings.Join(viol, ";")
			switch {
			case selKey == lastSelection:
				return verdict{
					message:   "连续两轮提交了完全相同的配置且仍改动锁定品类,判定死循环,终止流水线。违规项:" + detail,
					selection: selKey,
					escalate:  true,
				}
			case finalRound:
				return verdict{
					message:   fmt.Sprintf("改单失败:第 %d/%d 轮仍改动了硬锁定品类(%s),重试轮数用尽,如实终止。", round, maxLoopRounds, detail),
					selection: selKey,
					escalate:  true,
				}
			default:
				return verdict{
					message:   fmt.Sprintf("锁定校验未通过(第 %d/%d 轮):%s。硬锁定品类必须照抄基版本 selection 中的 SKU 一字不差,只对解锁品类重新选件。", round, maxLoopRounds, detail),
					selection: selKey,
				}
			}
		}
	}

	// adjust_budget 的产品语义是“尽量少改”。只写在提示词中会随模型波动,
	// 因此在校验层硬性限制单次最多改动两个品类;零换件也是合法的最小改动。
	if chg != nil && len(chg.Change) > 0 && len(chg.BaseSelection) > 0 {
		cr, err := schemas.DecodeChangeRequest(chg.Change)
		if err != nil {
			return verdict{
				message:  fmt.Sprintf("校验节点内部错误(重新解析改单失败),流水线熔断:%v", err),
				escalate: true,
			}
		}
		if cr.Intent == schemas.IntentAdjustBudget {
			changed, err := lockedViolations(schemas.AllCategories, chg.BaseSelection, draft.Selection)
			if err != nil {
				return verdict{
					message:  fmt.Sprintf("校验节点内部错误(预算改单比对失败),流水线熔断:%v", err),
					escalate: true,
				}
			}
			if len(changed) > 2 {
				detail := strings.Join(changed, ";")
				switch {
				case selKey == lastSelection:
					return verdict{
						message:   "连续提交了相同配置且预算改单仍改动超过两个品类,判定死循环,终止流水线。改动项:" + detail,
						selection: selKey,
						escalate:  true,
					}
				case finalRound:
					return verdict{
						message:   fmt.Sprintf("预算改单失败:第 %d/%d 轮仍改动了 %d 个品类,超过最多两类的限制,本轮不保存版本。改动项:%s", round, maxLoopRounds, len(changed), detail),
						selection: selKey,
						escalate:  true,
					}
				default:
					return verdict{
						message:   fmt.Sprintf("预算改单改动过多(第 %d/%d 轮):当前改了 %d 个品类(%s)。请保留基版本其余 SKU,最多只重选两个对预算影响最大的品类;若原配置已在新预算弹性区间内,直接照抄整份 selection。", round, maxLoopRounds, len(changed), detail),
						selection: selKey,
					}
				}
			}
		}
	}

	if chg != nil && len(chg.ActiveSpec) > 0 {
		spec, err := schemas.DecodeRequirementSpec(chg.ActiveSpec)
		if err == nil && (len(spec.ExistingParts) > 0 || len(spec.OwnedParts) > 0) {
			return verdict{message: "已有配件需要在默认 Harness v2 路径核验型号、锁定和采购预算；legacy 本轮不交付。", escalate: true}
		}
	}
	res, err := eval.Evaluate(ctx, draft.Selection)
	if err != nil {
		if errors.Is(err, store.ErrUnknownSKU) {
			switch {
			case selKey == lastSelection:
				return verdict{
					message:   fmt.Sprintf("连续两轮提交了完全相同的配置且 SKU 仍不在零件库中(%v),判定死循环,终止流水线。", err),
					selection: selKey,
					escalate:  true,
				}
			case finalRound:
				return verdict{
					message:   fmt.Sprintf("配置生成失败:第 %d/%d 轮仍引用不在零件库中的 SKU(%v),重试轮数用尽,如实终止。", round, maxLoopRounds, err),
					selection: selKey,
					escalate:  true,
				}
			default:
				return verdict{
					message:   fmt.Sprintf("SKU 不在零件库中(第 %d/%d 轮):%v。selection 里的每个 sku 必须一字不差来自 search_parts 的返回,请换用真实候选重新提交。", round, maxLoopRounds, err),
					selection: selKey,
				}
			}
		}
		// DB 不可达等不可恢复错误:立即熔断,如实报告(§5 错误分类表)。
		return verdict{
			message:  fmt.Sprintf("校验节点内部错误,流水线熔断:%v", err),
			escalate: true,
		}
	}

	reportJSON, err := json.Marshal(res.Report)
	if err != nil {
		return verdict{
			message:  fmt.Sprintf("校验节点内部错误(报告序列化失败),流水线熔断:%v", err),
			escalate: true,
		}
	}

	if res.Report.OverallStatus != schemas.OverallFail {
		budgetMessage, err := budgetWindowMessage(chg, res.Quote)
		if err != nil {
			return verdict{
				message:  fmt.Sprintf("预算窗口校验内部错误,流水线熔断:%v", err),
				escalate: true,
			}
		}
		if budgetMessage != "" {
			switch {
			case selKey == lastSelection:
				return verdict{
					message:    "连续两轮提交了完全相同的配置且预算窗口仍未通过,判定死循环,终止流水线。" + budgetMessage,
					reportJSON: string(reportJSON),
					selection:  selKey,
					escalate:   true,
				}
			case finalRound:
				return verdict{
					message:    fmt.Sprintf("配置生成失败:第 %d/%d 轮预算窗口仍未通过,重试轮数用尽,本轮不保存版本。%s", round, maxLoopRounds, budgetMessage),
					reportJSON: string(reportJSON),
					selection:  selKey,
					escalate:   true,
				}
			default:
				return verdict{
					message:    fmt.Sprintf("预算窗口未通过(第 %d/%d 轮):%s 请在保持兼容的前提下定向调整一到两个优先品类,不要从头乱换;下一轮仍须只输出 BuildDraft JSON。", round, maxLoopRounds, budgetMessage),
					reportJSON: string(reportJSON),
					selection:  selKey,
				}
			}
		}
		if unknownMessage := retryableUnknownMessage(res.Report); unknownMessage != "" && !finalRound {
			prefix := fmt.Sprintf("存在可消除的数据不足(review,第 %d/%d 轮):", round, maxLoopRounds)
			if selKey == lastSelection {
				prefix = fmt.Sprintf("连续提交了相同配置且数据不足仍未消除(review,第 %d/%d 轮):", round, maxLoopRounds)
			}
			return verdict{
				message:    prefix + unknownMessage + " 请只更换对应品类,优先选择这些字段非 null 的真实候选,然后重新校验;下一轮仍须只输出 BuildDraft JSON。",
				reportJSON: string(reportJSON),
				selection:  selKey,
			}
		}
		return verdict{
			message:    deliveryMessage(draft, res),
			reportJSON: string(reportJSON),
			selection:  selKey,
			escalate:   true,
			deliver:    true,
			draft:      draft,
			res:        res,
		}
	}

	switch {
	case selKey == lastSelection:
		return verdict{
			message:    "连续两轮提交了完全相同的配置且校验仍为 fail,判定死循环,终止流水线。最后一版失败项:\n" + failSummary(res.Report),
			reportJSON: string(reportJSON),
			selection:  selKey,
			escalate:   true,
		}
	case finalRound:
		return verdict{
			message:    fmt.Sprintf("重试轮数用尽(%d/%d),如实交付失败报告,未能得到可行配置。最后一版失败项:\n%s", round, maxLoopRounds, failSummary(res.Report)),
			reportJSON: string(reportJSON),
			selection:  selKey,
			escalate:   true,
		}
	default:
		return verdict{
			message:    fmt.Sprintf("兼容性校验未通过(fail,第 %d/%d 轮)。必须定向修复以下失败项——换掉冲突零件,不要从头乱换:\n%s\n到第 %d 轮仍失败将如实交付失败报告。", round, maxLoopRounds, failSummary(res.Report), maxLoopRounds),
			reportJSON: string(reportJSON),
			selection:  selKey,
		}
	}
}

// retryableUnknownMessage 只摘要规则引擎明确标记为 unknown 的字段。
// 数据缺失可能通过换用同品类的字段完整候选消除,因此在还有轮次时先给生成 Agent
// 一次定向修正机会;最后一轮仍由调用方如实交付 review。
func retryableUnknownMessage(report schemas.ValidationReport) string {
	var issues []string
	for _, check := range report.Checks {
		if check.Outcome != schemas.OutcomeUnknown {
			continue
		}
		fields := strings.Join(check.MissingFields, ",")
		if fields == "" {
			fields = "未列明缺失字段"
		}
		issues = append(issues, fmt.Sprintf("%s 缺少 %s", check.RuleID, fields))
	}
	return strings.Join(issues, ";")
}

// budgetWindowMessage 是兼容性规则之外的产品交付门禁。ValidationReport 仍只承载
// 固定 12 条硬件规则;无缺价时,配置总价还必须落入需求预算弹性窗口,否则不落库。
func budgetWindowMessage(chg *changeCtx, quote validate.Quote) (string, error) {
	if chg == nil || len(chg.ActiveSpec) == 0 || quote.MissingCount > 0 {
		return "", nil
	}
	spec, err := schemas.DecodeRequirementSpec(chg.ActiveSpec)
	if err != nil {
		return "", fmt.Errorf("解析生效需求单失败:%w", err)
	}
	totalFen, ok := parseCNYFen(quote.TotalCNY)
	if !ok {
		return "", fmt.Errorf("解析报价合计失败:%q", quote.TotalCNY)
	}
	budgetFen := int64(spec.BudgetCNY) * 100
	flexFen := int64(math.Round(float64(budgetFen) * spec.BudgetFlex))
	lower, upper := budgetFen-flexFen, budgetFen+flexFen
	if totalFen >= lower && totalFen <= upper {
		return "", nil
	}
	return fmt.Sprintf("当前合计 ¥%s,需求允许区间 ¥%s–¥%s。", quote.TotalCNY, formatFen64(lower), formatFen64(upper)), nil
}

// deliveryMessage 交付文本:结论 + 配置单清单 + 报价合计 + 快照日期(用例 A DoD)。
func deliveryMessage(draft schemas.BuildDraft, res validate.Result) string {
	var b strings.Builder
	if res.Report.OverallStatus == schemas.OverallPass {
		b.WriteString("兼容性校验全部通过(pass)。\n")
	} else {
		b.WriteString("兼容性校验通过但有注意项(review):\n")
		for _, c := range res.Report.Checks {
			if c.Outcome == schemas.OutcomeFail || c.Outcome == schemas.OutcomeUnknown {
				fmt.Fprintf(&b, "- %s(%s): %s\n", c.RuleID, c.Outcome, c.Detail)
			}
		}
	}
	fmt.Fprintf(&b, "配置单(build_ref: %s):\n", draft.BuildRef)
	for _, ln := range res.Quote.Lines {
		price := "缺价"
		if ln.UnitPriceCNY != nil {
			price = "¥" + *ln.UnitPriceCNY
		}
		if ln.Quantity > 1 {
			fmt.Fprintf(&b, "- %s: %s ×%d(单价 %s)\n", ln.Category, ln.SKU, ln.Quantity, price)
		} else {
			fmt.Fprintf(&b, "- %s: %s(%s)\n", ln.Category, ln.SKU, price)
		}
		if ln.Owned {
			b.WriteString("  用户已有，无需购买；以上为参考价。\n")
		}
		if r, ok := draft.Rationale[string(ln.Category)]; ok && r != "" {
			fmt.Fprintf(&b, "  理由:%s\n", r)
		}
	}
	if res.Quote.PurchaseTotalCNY != nil {
		fmt.Fprintf(&b, "新增购买合计:¥%s；整机参考", *res.Quote.PurchaseTotalCNY)
	}
	fmt.Fprintf(&b, "合计:¥%s", res.Quote.TotalCNY)
	if res.Quote.SnapshotDate != "" {
		fmt.Fprintf(&b, "(价格快照 %s)", res.Quote.SnapshotDate)
	}
	if res.Quote.MissingCount > 0 {
		fmt.Fprintf(&b, "\n注意:%d 件零件缺价未计入合计(%s)。", res.Quote.MissingCount, strings.Join(res.Quote.MissingSKUs, ", "))
	}
	return b.String()
}

// failSummary 列出 error 级失败项,回喂生成 Agent 做定向修复。
func failSummary(report schemas.ValidationReport) string {
	var b strings.Builder
	for _, c := range report.Checks {
		if c.Outcome == schemas.OutcomeFail && c.Severity == schemas.SeverityError {
			fmt.Fprintf(&b, "- %s: %s\n", c.RuleID, c.Detail)
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// canonicalSelection selection 的规范键,用于连续两轮重复检测(§5)。
func canonicalSelection(sel schemas.BuildSelection) string {
	b, err := json.Marshal(sel)
	if err != nil {
		return fmt.Sprintf("%+v", sel)
	}
	return string(b)
}

// errNoDraft 草稿文本里找不到任何 JSON 对象(初筛追问/生成 Agent 等待需求的纯文本)。
var errNoDraft = errors.New("pipeline: 草稿中无 JSON 对象")

// extractBuildDraft 从生成 Agent 的输出文本中提取 BuildDraft:容忍 markdown
// 围栏与思考文字混排(Pass@k 回归发现的真实失败模式)——逐个尝试文本里的
// 顶层 JSON 对象,返回第一个能通过严格解码的;都通不过则报第一个 schema 错
// (回喂给生成 Agent),完全没有 JSON 对象则报 errNoDraft。
func extractBuildDraft(text string) (schemas.BuildDraft, error) {
	var firstErr error
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(text[i:]))
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			continue // 这个 '{' 不是合法 JSON 起点,逐字节前进
		}
		draft, err := schemas.DecodeBuildDraft(raw)
		if err == nil {
			return draft, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		i += int(dec.InputOffset()) - 1 // 跳过整个已解析对象,避免重复尝试内层 '{'
	}
	if firstErr != nil {
		return schemas.BuildDraft{}, firstErr
	}
	return schemas.BuildDraft{}, errNoDraft
}

// stateString 读会话状态里的字符串键;缺失或类型不符按空串(可选键语义)。
func stateString(st session.State, key string) string {
	v, err := st.Get(key)
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// stateInt 读会话状态里的整数键;缺失按 0(轮数从未写过 = 第 0 轮已完成)。
func stateInt(st session.State, key string) int {
	v, err := st.Get(key)
	if err != nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// BuildSaver 版本落库接口(*store.Store 实现;单测可 fake)。
type BuildSaver interface {
	SaveBuildVersion(ctx context.Context, p store.SaveBuildVersionParams) (store.SavedBuild, error)
}

// loadChangeCtx 读 prep 节点写入的改单上下文;缺失或非法按 nil(无上下文)。
func loadChangeCtx(st session.State) *changeCtx {
	raw := stateString(st, stateKeyChangeCtx)
	if raw == "" {
		return nil
	}
	var c changeCtx
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil
	}
	return &c
}

// draftWireJSON BuildDraft → 设计方案 §四.2 wire 格式 JSON(落库用)。
func draftWireJSON(d schemas.BuildDraft) json.RawMessage {
	m := map[string]any{
		"schema_version":  d.SchemaVersion,
		"requirement_ref": d.RequirementRef,
		"build_ref":       d.BuildRef,
		"selection":       json.RawMessage(selectionWireJSON(d.Selection)),
	}
	if d.Rationale != nil {
		m["rationale"] = d.Rationale
	}
	if d.BudgetAllocation != nil {
		m["budget_allocation"] = d.BudgetAllocation
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return b
}

// changeIntentLabel 版本历史摘要里的意图标签。
func changeIntentLabel(change json.RawMessage) string {
	if len(change) == 0 {
		return "整单生成"
	}
	var w struct {
		Intent string `json:"intent"`
	}
	if err := json.Unmarshal(change, &w); err != nil || w.Intent == "" {
		return "改单"
	}
	return w.Intent
}

// remainingCNY 预算余额 = budget - total(按分整数运算,不用浮点);解不动返回空串。
func remainingCNY(budgetCNY int, total string) string {
	if budgetCNY <= 0 || total == "" {
		return ""
	}
	totalFen, ok := parseCNYFen(total)
	if !ok {
		return ""
	}
	remain := int64(budgetCNY)*100 - totalFen
	return formatFen64(remain)
}

func parseCNYFen(total string) (int64, bool) {
	neg := strings.HasPrefix(total, "-")
	s := strings.TrimPrefix(total, "-")
	intPart, frac := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, frac = s[:i], s[i+1:]
	}
	for len(frac) < 2 {
		frac += "0"
	}
	if len(frac) > 2 {
		return 0, false
	}
	yuan, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil || yuan < 0 {
		return 0, false
	}
	fen, err := strconv.ParseInt(frac, 10, 64)
	if err != nil || fen < 0 || fen > 99 {
		return 0, false
	}
	totalFen := yuan*100 + fen
	if neg {
		totalFen = -totalFen
	}
	return totalFen, true
}

func formatFen64(fen int64) string {
	sign := ""
	if fen < 0 {
		sign = "-"
		fen = -fen
	}
	return fmt.Sprintf("%s%d.%02d", sign, fen/100, fen%100)
}

// persistVersion 交付分支的版本落库 + 状态块更新(P4 设计 §3/§5)。
// 返回交付文本后缀与新 build_state JSON(落库失败时后缀如实报错、状态块不更新)。
func persistVersion(ctx context.Context, saver BuildSaver, sessionID string, chg *changeCtx, v verdict, prevStateJSON string) (string, string) {
	if chg == nil || (len(chg.ActiveSpec) == 0 && chg.ReuseReqID == nil) {
		return "\n注意:配置有效但版本落库失败(缺少需求上下文,change_prep 未运行),本版本未计入版本树。", ""
	}

	quoteJSON, err := json.Marshal(v.res.Quote)
	if err != nil {
		return fmt.Sprintf("\n注意:配置有效但版本落库失败(报价序列化:%v),本版本未计入版本树。", err), ""
	}
	saved, err := saver.SaveBuildVersion(ctx, store.SaveBuildVersionParams{
		SessionID:       sessionID,
		ParentID:        chg.ParentBuildID,
		RequirementID:   chg.ReuseReqID,
		RequirementSpec: chg.ActiveSpec,
		Change:          chg.Change,
		Draft:           draftWireJSON(v.draft),
		Validation:      json.RawMessage(v.reportJSON),
		Quote:           quoteJSON,
	})
	if err != nil {
		return fmt.Sprintf("\n注意:配置有效但版本落库失败(%v),本版本未计入版本树。", err), ""
	}

	// 状态块更新:代码维护的真值,下一轮改单的基准。
	spec := chg.ActiveSpec
	budget := 0
	if rs, err := schemas.DecodeRequirementSpec(spec); err == nil {
		budget = rs.BudgetCNY
	}
	var history []string
	if prevStateJSON != "" {
		var prev buildState
		if err := json.Unmarshal([]byte(prevStateJSON), &prev); err == nil {
			history = prev.History
		}
	}
	history = append(history, fmt.Sprintf("v%d(%s)合计 ¥%s", saved.Version, changeIntentLabel(chg.Change), v.res.Quote.TotalCNY))

	bs := buildState{
		SessionID:          sessionID,
		Version:            saved.Version,
		BuildID:            saved.ID,
		RequirementID:      saved.RequirementID,
		Spec:               spec,
		Selection:          selectionWireJSON(v.draft.Selection),
		TotalCNY:           v.res.Quote.TotalCNY,
		SnapshotDate:       v.res.Quote.SnapshotDate,
		BudgetCNY:          budget,
		BudgetRemainingCNY: remainingCNY(budget, v.res.Quote.TotalCNY),
		History:            history,
	}
	bsJSON, err := json.Marshal(bs)
	if err != nil {
		return fmt.Sprintf("\n已落库:版本 v%d(会话 %s)。注意:状态块序列化失败(%v),后续改单可能受影响。", saved.Version, sessionID, err), ""
	}
	return fmt.Sprintf("\n已落库:版本 v%d(会话 %s)。", saved.Version, sessionID), string(bsJSON)
}

// newValidatorAgent 把确定性校验核心包装成 ADK 自定义 Run agent(Loop 内第二子节点)。
// 事件同时承载:文本(进会话历史回喂生成 Agent / 面向用户)、StateDelta(轮数/报告/
// 上轮 selection/状态块)、Escalate(命中停止条件时让 LoopAgent 出栈);
// 交付分支同步落库新版本(P4,落库失败要响不静默)。
func newValidatorAgent(eval tools.BuildEvaluator, saver BuildSaver) (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        validatorAgentName,
		Description: "确定性校验节点:P1 规则引擎 + 最新快照报价,结论覆盖上游任何 Agent 自评。",
		Run: func(ictx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				st := ictx.Session().State()
				round := stateInt(st, stateKeyRound) + 1
				chg := loadChangeCtx(st)
				v := decide(ictx, eval,
					stateString(st, stateKeyBuildDraft),
					stateString(st, stateKeyLastSelection),
					round, chg)
				evalmetrics.Record("buildsvc", "validation.round", map[string]any{
					"session_fingerprint": evalmetrics.Fingerprint(ictx.Session().ID()),
					"round":               round, "deliver": v.deliver, "escalate": v.escalate,
				})

				delta := map[string]any{stateKeyRound: round}
				if v.reportJSON != "" {
					delta[stateKeyReport] = v.reportJSON
				}
				if v.selection != "" {
					delta[stateKeyLastSelection] = v.selection
				}
				if v.deliver {
					suffix, bsJSON := persistVersion(ictx, saver, ictx.Session().ID(), chg, v,
						stateString(st, stateKeyBuildState))
					v.message += suffix
					if bsJSON != "" {
						delta[stateKeyBuildState] = bsJSON
					}
				}

				ev := &session.Event{
					Author:  validatorAgentName,
					Actions: session.EventActions{StateDelta: delta, Escalate: v.escalate},
				}
				if v.message != "" {
					ev.Content = genai.NewContentFromText(v.message, genai.RoleModel)
				}
				yield(ev, nil)
			}
		},
	})
}
