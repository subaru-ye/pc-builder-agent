package pipeline

// P4 增量改单的确定性预处理(服务与状态.md §3):
// 初筛载荷分类(RequirementSpec / ChangeRequest)、派生需求单、锁定清单、
// 生成 Agent 改单指令渲染,全部由代码计算,不让 LLM 自己统计(docs/tech/开发约定.md)。

import (
	"encoding/json"
	"fmt"
	"iter"
	"sort"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// P4 新增会话状态键(P4 设计 §5;buildState 即 P6 Redis 热上下文要存的东西)。
const (
	stateKeyBuildState    = "build_state"         // 改单状态块 JSON(校验节点交付时写)
	stateKeyChangeContext = "change_context"      // 生成 Agent 改单指令文本(prep 写)
	stateKeyChangeCtx     = "pipeline_change_ctx" // prep → validator 的内部上下文 JSON
)

const changePrepAgentName = "change_prep_agent"

// buildState 改单状态块:代码维护、显式注入,覆盖改单会用到的全部维度
// (docs/tech/开发约定.md;字段新增当"改表结构"级别对待)。
type buildState struct {
	SessionID          string          `json:"session_id"`
	Version            int             `json:"version"`
	BuildID            int64           `json:"build_id"`
	RequirementID      int64           `json:"requirement_id"`
	Spec               json.RawMessage `json:"requirement_spec"` // 当前生效需求单
	Selection          json.RawMessage `json:"selection"`        // 当前版本 selection(wire 格式)
	TotalCNY           string          `json:"total_cny"`
	SnapshotDate       string          `json:"snapshot_date"`
	BudgetCNY          int             `json:"budget_cny"`                     // 0 = 需求单解不出预算
	BudgetRemainingCNY string          `json:"budget_remaining_cny,omitempty"` // 空 = 无法计算
	History            []string        `json:"history"`                        // 修改历史摘要,如 "v2(adjust_budget)合计 ¥7291.00"
}

// changeCtx prep → validator 的确定性上下文:落库入参与锁定校验所需的全部真值。
type changeCtx struct {
	Change        json.RawMessage    `json:"change,omitempty"`          // ChangeRequest 原文;nil = 整单生成
	ActiveSpec    json.RawMessage    `json:"active_spec,omitempty"`     // 当前生效需求单(派生后)
	ReuseReqID    *int64             `json:"reuse_req_id,omitempty"`    // 非 nil = 复用基版本需求单(swap_part)
	ParentBuildID *int64             `json:"parent_build_id,omitempty"` // 非 nil = 新版本挂在此版本之下
	BaseVersion   int                `json:"base_version,omitempty"`
	BaseSelection json.RawMessage    `json:"base_selection,omitempty"` // 基版本 selection(wire 格式)
	Locked        []schemas.Category `json:"locked,omitempty"`         // 硬锁定品类(确定性校验强制)
	Invalid       string             `json:"invalid,omitempty"`        // 非空 = 改单请求无效原因(如实转告用户)
}

// prepare 预处理核心(纯函数):初筛载荷 + 当前状态块 → 内部上下文 + 生成 Agent 改单指令。
// 载荷分类规则:顶层含 "intent" 键 = ChangeRequest,否则按 RequirementSpec 处理。
func prepare(payloadText, stateJSON string) (changeCtx, string) {
	raw := extractJSONObject(payloadText)
	if raw == nil {
		// 初筛还在追问,无载荷:清空改单上下文。
		return changeCtx{}, ""
	}

	var bs *buildState
	if stateJSON != "" {
		var parsed buildState
		if err := json.Unmarshal([]byte(stateJSON), &parsed); err == nil && parsed.BuildID != 0 {
			bs = &parsed
		}
	}

	if !hasTopLevelKey(raw, "intent") {
		// 整单生成:会话已有版本时新版本仍挂链(整单重生成也是演化)。
		ctx := changeCtx{ActiveSpec: raw}
		if bs != nil {
			ctx.ParentBuildID = &bs.BuildID
			ctx.BaseVersion = bs.Version
		}
		return ctx, ""
	}

	// 改单载荷。
	if bs == nil {
		ctx := changeCtx{Invalid: "会话内没有已落库的配置版本,无法增量改单"}
		return ctx, invalidDirective(ctx.Invalid)
	}
	cr, err := schemas.DecodeChangeRequest(raw)
	if err != nil {
		ctx := changeCtx{Invalid: fmt.Sprintf("改单请求不符合 ChangeRequest schema:%v", err)}
		return ctx, invalidDirective(ctx.Invalid)
	}

	ctx := changeCtx{
		Change:        raw,
		ParentBuildID: &bs.BuildID,
		BaseVersion:   bs.Version,
		BaseSelection: bs.Selection,
		Locked:        cr.HardLocked(),
	}
	switch cr.Intent {
	case schemas.IntentSwapPart:
		ctx.ReuseReqID = &bs.RequirementID
		ctx.ActiveSpec = bs.Spec
	default:
		spec, err := deriveSpec(bs.Spec, cr)
		if err != nil {
			ctx = changeCtx{Invalid: fmt.Sprintf("派生新需求单失败:%v", err)}
			return ctx, invalidDirective(ctx.Invalid)
		}
		ctx.ActiveSpec = spec
	}
	return ctx, changeDirective(ctx, cr)
}

// deriveSpec 按 ChangeRequest 从基需求单派生新需求单(代码计算,LLM 不参与):
// adjust_budget 改 budget_cny;change_constraint 逐键覆盖;结果重新严格解码兜底。
func deriveSpec(base json.RawMessage, cr schemas.ChangeRequest) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		return nil, fmt.Errorf("基需求单非法 JSON: %w", err)
	}

	switch cr.Intent {
	case schemas.IntentAdjustBudget:
		baseSpec, err := schemas.DecodeRequirementSpec(base)
		if err != nil {
			return nil, fmt.Errorf("基需求单不符合 RequirementSpec schema: %w", err)
		}
		newBudget := baseSpec.BudgetCNY + *cr.BudgetDeltaCNY
		if newBudget <= 0 {
			return nil, fmt.Errorf("调整后预算 %d 元不为正(基预算 %d,增量 %d)", newBudget, baseSpec.BudgetCNY, *cr.BudgetDeltaCNY)
		}
		m["budget_cny"] = json.RawMessage(fmt.Sprintf("%d", newBudget))
	case schemas.IntentChangeConstraint:
		var patch map[string]json.RawMessage
		if err := json.Unmarshal(cr.ConstraintPatch, &patch); err != nil {
			return nil, fmt.Errorf("constraint_patch 非法: %w", err)
		}
		for k, v := range patch {
			m[k] = v
		}
	default:
		return nil, fmt.Errorf("意图 %s 不派生需求单", cr.Intent)
	}

	out, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("序列化新需求单失败: %w", err)
	}
	if _, err := schemas.DecodeRequirementSpec(out); err != nil {
		return nil, fmt.Errorf("派生结果不符合 RequirementSpec schema: %w", err)
	}
	return out, nil
}

// changeDirective 渲染生成 Agent 的改单指令(注入 {change_context?} 占位)。
func changeDirective(ctx changeCtx, cr schemas.ChangeRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "改单模式:基于已落库版本 v%d 增量改单。\n", ctx.BaseVersion)
	switch cr.Intent {
	case schemas.IntentSwapPart:
		fmt.Fprintf(&b, "- 意图:换件(swap_part),目标品类 %s", cr.Swap.Category)
		if cr.Swap.TargetHint != "" {
			fmt.Fprintf(&b, ",方向:%s", cr.Swap.TargetHint)
		}
		b.WriteString("。\n")
	case schemas.IntentAdjustBudget:
		fmt.Fprintf(&b, "- 意图:调整预算(adjust_budget),增量 %d 元;改动尽量少的品类满足新预算,其余保持原 SKU。\n", *cr.BudgetDeltaCNY)
	case schemas.IntentChangeConstraint:
		fmt.Fprintf(&b, "- 意图:调整约束(change_constraint),补丁:%s;只为满足新约束换必要的品类。\n", string(cr.ConstraintPatch))
	}
	if cr.Notes != "" {
		fmt.Fprintf(&b, "- 用户补充:%s\n", cr.Notes)
	}
	fmt.Fprintf(&b, "- 生效需求单(预算与约束以此为准):%s\n", string(ctx.ActiveSpec))
	fmt.Fprintf(&b, "- 基版本 selection:%s\n", string(ctx.BaseSelection))
	locked := make([]string, 0, len(ctx.Locked))
	for _, c := range ctx.Locked {
		locked = append(locked, string(c))
	}
	fmt.Fprintf(&b, "- 硬锁定品类(必须照抄基版本 SKU 一字不差,违者校验直接打回):[%s]\n", strings.Join(locked, ", "))
	return b.String()
}

// invalidDirective 改单请求无效时给生成 Agent 的指令:只转述,不出配置。
func invalidDirective(reason string) string {
	return fmt.Sprintf("改单请求无法执行:%s。请不要调用工具、不要输出 JSON,只输出一句话向用户转述这个问题。", reason)
}

// extractJSONObject 提取文本中第一个可解析的顶层 JSON 对象(优先取含
// schema_version 键的,跳过思考文字里的花括号碎片;与 extractBuildDraft 同一容忍策略)。
func extractJSONObject(text string) json.RawMessage {
	var fallback json.RawMessage
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(text[i:]))
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			continue
		}
		if hasTopLevelKey(raw, "schema_version") {
			return raw
		}
		if fallback == nil {
			fallback = raw
		}
		i += int(dec.InputOffset()) - 1
	}
	return fallback
}

// hasTopLevelKey 判定 JSON 对象顶层是否含指定键。
func hasTopLevelKey(raw json.RawMessage, key string) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

// selectionWireJSON BuildSelection → §四.1 wire 格式 JSON(状态块与落库草稿共用)。
func selectionWireJSON(sel schemas.BuildSelection) json.RawMessage {
	m := map[string]any{
		"cpu": sel.CPU, "motherboard": sel.Motherboard, "memory": sel.Memory,
		"ssd": sel.SSDs, "gpu": sel.GPU,
		"psu": sel.PSU, "case": sel.Case, "cooler": sel.Cooler,
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return b
}

// wireSelection wire 格式 selection 的解码形态(锁定比对用)。
type wireSelection struct {
	CPU         string                 `json:"cpu"`
	Motherboard string                 `json:"motherboard"`
	Memory      string                 `json:"memory"`
	SSD         []schemas.SSDSelection `json:"ssd"`
	GPU         *string                `json:"gpu"`
	PSU         string                 `json:"psu"`
	Case        string                 `json:"case"`
	Cooler      string                 `json:"cooler"`
}

// lockedViolations 确定性锁定校验(FR-402 不靠提示词):逐硬锁定品类比对
// 新 selection 与基版本 selection,返回被改动品类的违规描述。
func lockedViolations(locked []schemas.Category, baseRaw json.RawMessage, cur schemas.BuildSelection) ([]string, error) {
	var base wireSelection
	if err := json.Unmarshal(baseRaw, &base); err != nil {
		return nil, fmt.Errorf("基版本 selection 解析失败: %w", err)
	}

	skuOf := func(w wireSelection, c schemas.Category) string {
		switch c {
		case schemas.CategoryCPU:
			return w.CPU
		case schemas.CategoryMotherboard:
			return w.Motherboard
		case schemas.CategoryMemory:
			return w.Memory
		case schemas.CategoryPSU:
			return w.PSU
		case schemas.CategoryCase:
			return w.Case
		case schemas.CategoryCooler:
			return w.Cooler
		case schemas.CategoryGPU:
			if w.GPU == nil {
				return "null"
			}
			return *w.GPU
		case schemas.CategorySSD:
			return canonicalSSDs(w.SSD)
		}
		return ""
	}
	curWire := wireSelection{
		CPU: cur.CPU, Motherboard: cur.Motherboard, Memory: cur.Memory,
		SSD: cur.SSDs, GPU: cur.GPU, PSU: cur.PSU, Case: cur.Case, Cooler: cur.Cooler,
	}

	var out []string
	for _, c := range locked {
		if b, n := skuOf(base, c), skuOf(curWire, c); b != n {
			out = append(out, fmt.Sprintf("%s: 基版本 %s → 提交 %s", c, b, n))
		}
	}
	return out, nil
}

// canonicalSSDs SSD 列表的规范键(按 SKU 排序,数量参与比对)。
func canonicalSSDs(ssds []schemas.SSDSelection) string {
	cp := make([]schemas.SSDSelection, len(ssds))
	copy(cp, ssds)
	sort.Slice(cp, func(i, j int) bool { return cp[i].SKU < cp[j].SKU })
	parts := make([]string, len(cp))
	for i, s := range cp {
		parts[i] = fmt.Sprintf("%s×%d", s.SKU, s.Quantity)
	}
	return strings.Join(parts, ",")
}

// newChangePrepAgent 确定性改单预处理节点(Sequential 中初筛之后、Loop 之前):
// 写改单上下文两个状态键,并重置轮数/上轮 selection(P2 遗留:这两个键跨用户
// 轮次累计,多轮改单会话中会误判"轮数用尽"/"死循环")。
func newChangePrepAgent() (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        changePrepAgentName,
		Description: "确定性改单预处理:载荷分类、派生需求单、锁定清单;不产出对话文本。",
		Run: func(ictx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				st := ictx.Session().State()
				ctx, directive := prepare(
					stateString(st, stateKeyRequirementSpec),
					stateString(st, stateKeyBuildState))

				ctxJSON, err := json.Marshal(ctx)
				if err != nil {
					yield(nil, fmt.Errorf("change_prep: 序列化改单上下文失败: %w", err))
					return
				}
				yield(&session.Event{
					Author: changePrepAgentName,
					Actions: session.EventActions{StateDelta: map[string]any{
						stateKeyChangeCtx:     string(ctxJSON),
						stateKeyChangeContext: directive,
						stateKeyRound:         0,
						stateKeyLastSelection: "",
					}},
				}, nil)
			}
		},
	})
}
