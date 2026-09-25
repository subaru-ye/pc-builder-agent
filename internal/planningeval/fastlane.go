// Fastlane 可行性实验：单字段预算快走的零模型审计与有界影子验证。
// 本文件只服务于 cmd/evalfastlane；产品链路（internal/product）零改动，
// Screening/Reducer/Readiness/回复组合的行为不变。快走判定规则链与
// Jev 门控都在这里，且全部默认关闭——只有显式运行评估命令才会执行。
package planningeval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/decision"
	"github.com/subaru-ye/pc-builder-agent/internal/providers/jev"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func regexpMatch(text, pattern string) bool {
	matched, err := regexp.MatchString(pattern, text)
	return err == nil && matched
}

// ---------- 语料 ----------

// FastlaneTurn 是一个被评轮次：本轮原话、金标回放出的先验状态与金标操作。
// 先验状态全部由金标操作经真实 Reducer 重建，不用任何模型输出。
type FastlaneTurn struct {
	Set         string // "corpus" | "setb"
	CaseID      string
	TurnIndex   int
	Split       string // corpus: development|calibration; setb: setb-cal|setb-report
	Quote       string
	PriorState  schemas.RequirementState
	GoldOps     []ReqV2OpGold
	GoldSignals *ReqV2TurnSignals
	LabelFast   bool  // 严格单字段预算快走候选（金标唯一 set budget_cny，无执行请求/歧义信号）
	LabelValue  *int  // 候选轮的金标金额
	LabelReason string
}

// FastlaneSetBCase 是独立标注表（Set B）的一行。标注为 AI 起草，须经人工
// 复核；partition 在运行前冻结，阈值只看 setb-cal。
type FastlaneSetBCase struct {
	ID                string `json:"id"`
	Partition         string `json:"partition"` // cal | report
	Category          string `json:"category"`
	Text              string `json:"text"`
	PriorBudgetActive bool   `json:"prior_budget_active"`
	PriorBudgetValue  int    `json:"prior_budget_value,omitempty"`
	ExpectedFast      bool   `json:"expected_fast"`
	ExpectedValue     *int   `json:"expected_value,omitempty"`
	Rationale         string `json:"rationale"`
}

const fastlaneSetBPath = "fastlane-budget-v1/set-b.json"

// FastlaneSetBPath 返回 Set B 标注表相对数据集根的路径。
func FastlaneSetBPath() string { return fastlaneSetBPath }

// LoadFastlaneSetB 读取并冻结校验 Set B 标注表。
func LoadFastlaneSetB(root string) ([]FastlaneSetBCase, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(fastlaneSetBPath)))
	if err != nil {
		return nil, err
	}
	var cases []FastlaneSetBCase
	if err := decodeStrictReqV2(raw, &cases); err != nil {
		return nil, fmt.Errorf("fastlane set-b: %w", err)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("fastlane set-b: 空标注表")
	}
	for _, c := range cases {
		if c.Partition != "cal" && c.Partition != "report" {
			return nil, fmt.Errorf("fastlane set-b %s: partition 必须是 cal 或 report", c.ID)
		}
		if strings.TrimSpace(c.Text) == "" {
			return nil, fmt.Errorf("fastlane set-b %s: text 必须非空", c.ID)
		}
	}
	return cases, nil
}

// BuildFastlaneCorpus 从 requirement-v2 数据集构建 development+calibration
// 轮次语料。holdout 一律排除，并在结果上断言无 holdout id，防止上游校验
// 放宽时静默泄漏。
func BuildFastlaneCorpus(dataset *ReqV2Dataset) ([]FastlaneTurn, error) {
	var turns []FastlaneTurn
	for _, c := range dataset.Extraction {
		if c.Split == "holdout" {
			continue
		}
		prior, err := replayFastlaneState(c.Seed, c.SeedUserMessage)
		if err != nil {
			return nil, fmt.Errorf("extraction %s: %w", c.ID, err)
		}
		turn := FastlaneTurn{
			Set: "corpus", CaseID: c.ID, Split: c.Split, Quote: c.UserMessage,
			PriorState: prior, GoldOps: c.Expected.Operations, GoldSignals: c.Expected.TurnSignals,
		}
		turn.LabelFast, turn.LabelValue, turn.LabelReason = fastlaneLabelFromGold(c.Expected)
		turns = append(turns, turn)
	}
	for _, c := range dataset.Conversations {
		if c.Split == "holdout" {
			continue
		}
		var priorOps []schemas.RequirementOperation
		for i, t := range c.Turns {
			prior, err := replayFastlaneState(priorOps, t.Text)
			if err != nil {
				return nil, fmt.Errorf("conversation %s#%d: %w", c.ID, i, err)
			}
			turn := FastlaneTurn{
				Set: "corpus", CaseID: c.ID, TurnIndex: i, Split: c.Split, Quote: t.Text,
				PriorState: prior, GoldOps: t.Expected.Operations, GoldSignals: t.Expected.TurnSignals,
			}
			turn.LabelFast, turn.LabelValue, turn.LabelReason = fastlaneLabelFromGold(t.Expected)
			turns = append(turns, turn)
			priorOps = append(priorOps, goldOpsToSeed(t.Expected.Operations)...)
		}
	}
	for _, t := range turns {
		if strings.Contains(t.CaseID, "holdout") || t.Split == "holdout" {
			return nil, fmt.Errorf("fastlane corpus: holdout case %s 泄漏", t.CaseID)
		}
	}
	sort.Slice(turns, func(i, j int) bool {
		if turns[i].Set != turns[j].Set {
			return turns[i].Set < turns[j].Set
		}
		if turns[i].CaseID != turns[j].CaseID {
			return turns[i].CaseID < turns[j].CaseID
		}
		return turns[i].TurnIndex < turns[j].TurnIndex
	})
	return turns, nil
}

// BuildFastlaneSetBTurns 把 Set B 标注行转成被评轮次（先验状态只含预算字段）。
func BuildFastlaneSetBTurns(cases []FastlaneSetBCase) []FastlaneTurn {
	turns := make([]FastlaneTurn, 0, len(cases))
	for _, c := range cases {
		prior := schemas.NewRequirementState()
		if c.PriorBudgetActive {
			value := c.PriorBudgetValue
			if value == 0 {
				value = 7000
			}
			prior, _ = replayFastlaneState([]schemas.RequirementOperation{{
				Op: "set", Field: decision.FastlaneField,
				Value: json.RawMessage(fmt.Sprintf("%d", value)), Evidence: "stated",
			}}, "先验预算")
		}
		split := "setb-report"
		if c.Partition == "cal" {
			split = "setb-cal"
		}
		turn := FastlaneTurn{
			Set: "setb", CaseID: c.ID, Split: split, Quote: c.Text, PriorState: prior,
			LabelFast: c.ExpectedFast, LabelValue: c.ExpectedValue, LabelReason: c.Category,
		}
		turns = append(turns, turn)
	}
	return turns
}

func goldOpsToSeed(gold []ReqV2OpGold) []schemas.RequirementOperation {
	ops := make([]schemas.RequirementOperation, 0, len(gold))
	for _, g := range gold {
		ops = append(ops, schemas.RequirementOperation{
			Op: g.Op, Field: g.Field, Value: g.Value, Strength: g.Strength, Evidence: g.Evidence,
			Quote: g.QuoteContains,
		})
	}
	return ops
}

// replayFastlaneState 用真实 Reducer 从操作列表重建先验状态。replay 用
// edit 来源（金标回放不是用户轮次，不需要 quote 归属校验）。
func replayFastlaneState(ops []schemas.RequirementOperation, quote string) (schemas.RequirementState, error) {
	state := schemas.NewRequirementState()
	if len(ops) == 0 {
		return state, nil
	}
	source := schemas.RequirementSource{Kind: "edit", MessageID: "fastlane-replay", Quote: quote}
	return schemas.ApplyRequirementUpdate(state, schemas.RequirementUpdate{Operations: ops}, source)
}

// fastlaneLabelFromGold 给出严格单字段快走金标：唯一 set budget_cny、无
// 执行请求/追问/歧义信号、无 forbidden 操作。其余轮次一律 label=fallback。
func fastlaneLabelFromGold(gold ReqV2ExtractionGold) (bool, *int, string) {
	if len(gold.ForbiddenOperations) > 0 {
		return false, nil, "gold_forbidden_operations"
	}
	if len(gold.Operations) != 1 || gold.Operations[0].Op != "set" || gold.Operations[0].Field != decision.FastlaneField {
		return false, nil, "gold_not_single_budget_set"
	}
	// 提案接受协议轮（裸"可以"）：值来自未解析建议而不是本轮原话，确定性
	// 快走拿不到候选值，永远回退。
	if gold.Operations[0].Evidence == "accepted_proposal" {
		return false, nil, "gold_proposal_protocol"
	}
	if sig := gold.TurnSignals; sig != nil && (sig.RequestsBuild || sig.AsksQuestion || sig.Ambiguous || sig.RequestsReview) {
		return false, nil, "gold_turn_signal_requires_screening"
	}
	var n int
	if err := json.Unmarshal(gold.Operations[0].Value, &n); err != nil {
		return false, nil, "gold_value_not_integer"
	}
	return true, &n, "gold_single_budget_set"
}

// ---------- 确定性规则链 ----------

// 快走规则链（spec C1–C9）。词表在本处单点定义；C3/C4 语义对齐产品
// verifyAcceptedProposals 的保守词表（询问/拒绝）。C3/C4/C5/C9 属于语义
// 判断，是 R+J 路由交给 Jev 的部分；C6/C7/C8 是确定性范围守卫。
var (
	fastlaneQuestionMarkers  = []string{"够吗", "行吗", "可以吗", "好吗", "是吗", "对吗", "够不够", "行不行", "怎么样", "如何", "多少", "贵不贵", "合适吗"}
	fastlaneRejectMarkers    = []string{"不行", "不要", "先不", "不换", "算了", "拒绝", "再想想", "再考虑", "暂不", "不算", "不定", "不限定", "不设", "取消", "换掉", "还没", "待定"}
	fastlaneReferenceMarkers = []string{"报价", "价格", "价钱", "优惠", "券", "补贴", "别人", "别家", "网上", "店家", "商家", "客服", "京东", "淘宝", "拼多多"}
	fastlaneExecuteMarkers   = []string{"开始配", "开始吧", "配吧", "开始生成", "生成配置", "开始装机", "开工", "直接配", "来一套", "帮我配", "能配", "配出来"}
	// C7 覆盖两类范围事实（非语义情感）：预算语义扩展（封顶/弹性/口径——
	// 金标通常还写 budget_flex/budget_basis，超出单字段合同）与依赖历史
	// 状态的措辞（"原来的/恢复/改回"是 restore 语义，快走只会 plain set）。
	// r1 影子运行暴露"不要超过"与 restore 两处漏拦后补齐（见决策报告）。
	fastlaneBudgetExtMarkers = []string{"不超过", "不要超过", "不能超过", "别超过", "顶多", "最多", "上限", "封顶", "以内", "上浮", "浮动", "弹性", "总价", "口径", "只算", "全包", "包含", "原来的", "恢复", "改回"}
	fastlaneCompoundMarkers  = []string{"机箱", "显卡", "cpu", "内存", "主板", "电源", "固态", "硬盘", "显示器", "键盘", "鼠标", "分辨率", "1080p", "2k", "4k", "静音", "安静", "白色", "黑色", "外观", "风扇", "水冷", "灯光", "办公", "上网", "游戏", "剪辑", "表格", "全新", "新买", "已有", "升级", "坏了"}
	fastlaneCueMarkers       = []string{"预算", "元", "块", "花费", "花销", "控制在"}
	fastlaneAdoptPattern     = `[就那给定按来][按着为个]{0,2}(?:[0-9,，]+|零〇一二两三四五六七八九十百千万)`
	fastlaneAdoptTailPattern = `(?:[0-9,，]+|[零〇一二两三四五六七八九十百千万]+)(?:元|块)?(?:吧|就行|定了|好了|这样)`
)

// FastlaneRuleResult 记录规则链结论；Eligible=false 时 Stage 指出首个拦截点。
type FastlaneRuleResult struct {
	Eligible   bool
	Stage      string
	Value      int
	Candidates []int
}

// FastlaneRules 跑纯规则路由（C1–C9 全过才可快走）。
func FastlaneRules(turn FastlaneTurn) FastlaneRuleResult {
	res := FastlaneRulesThroughC8(turn)
	if !res.Eligible {
		return res
	}
	// C3/C4/C5: 语义词表（询问/拒绝/引用）——纯规则路由的保守层。
	if stage := FastlaneRuleSemanticC345(turn.Quote); stage != "" {
		res.Eligible = false
		res.Stage = stage
		return res
	}
	// C9: 语义线索——有预算词缀，或（预算尚未确立时的）采纳句式。
	if containsAny(turn.Quote, fastlaneCueMarkers) {
		return res
	}
	if !budgetActive(turn.PriorState) && matchAnyPattern(turn.Quote, fastlaneAdoptPattern, fastlaneAdoptTailPattern) {
		return res
	}
	res.Eligible = false
	res.Stage = "c9_no_cue"
	return res
}

// FastlaneRulesThroughC8 是 R+J 路由的确定性范围守卫：候选唯一性、哨兵
// 区间与范围类守卫（执行请求/预算语义扩展/复合字段）。C3 询问、C4 拒绝、
// C5 引用与 C9 采纳句式交给 Jev 判定。
func FastlaneRulesThroughC8(turn FastlaneTurn) FastlaneRuleResult {
	// C1: 候选提取（宽口径金额提及，全部列出）。
	mentions := pipeline.ExtractBudgetCandidates(turn.Quote)
	if len(mentions) == 0 {
		return FastlaneRuleResult{Stage: "c1_no_candidate", Candidates: mentions}
	}
	// C2: 哨兵区间先过滤，再判多值——型号/帧率/助数词等离谱数字不构成
	// 竞争候选；区间外全部出局才是真无候选。
	inRange := make([]int, 0, len(mentions))
	for _, value := range mentions {
		if value >= 500 && value <= 200000 {
			inRange = append(inRange, value)
		}
	}
	if len(inRange) == 0 {
		return FastlaneRuleResult{Stage: "c2_out_of_range", Candidates: mentions}
	}
	if len(inRange) > 1 {
		return FastlaneRuleResult{Stage: "c1_multi_value", Candidates: mentions}
	}
	value := inRange[0]
	// C6/C7/C8: 快走范围守卫（这些轮次按合同不属于单字段快走）。
	if containsAny(turn.Quote, fastlaneExecuteMarkers) {
		return FastlaneRuleResult{Stage: "c6_execute_request", Value: value, Candidates: mentions}
	}
	if containsAny(turn.Quote, fastlaneBudgetExtMarkers) {
		return FastlaneRuleResult{Stage: "c7_budget_semantics", Value: value, Candidates: mentions}
	}
	if containsAnyFold(turn.Quote, fastlaneCompoundMarkers) {
		return FastlaneRuleResult{Stage: "c8_compound_field", Value: value, Candidates: mentions}
	}
	return FastlaneRuleResult{Eligible: true, Stage: "c8_pass", Value: value, Candidates: mentions}
}

// FastlaneRuleSemanticC345 是纯规则路由的语义词表层（C3 询问/C4 拒绝/
// C5 引用），按固定顺序返回首个命中；空串表示未命中。
func FastlaneRuleSemanticC345(quote string) string {
	if containsAny(quote, fastlaneQuestionMarkers) || strings.ContainsAny(quote, "？?") {
		return "c3_question"
	}
	if containsAny(quote, fastlaneRejectMarkers) {
		return "c4_reject"
	}
	if containsAny(quote, fastlaneReferenceMarkers) {
		return "c5_reference"
	}
	return ""
}

func containsAny(text string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

func containsAnyFold(text string, markers []string) bool {
	lower := strings.ToLower(text)
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func matchAnyPattern(text string, patterns ...string) bool {
	for _, p := range patterns {
		if regexpMatch(text, p) {
			return true
		}
	}
	return false
}

func budgetActive(state schemas.RequirementState) bool {
	return state.Fields[decision.FastlaneField].Status == "active"
}

// ---------- 快走落地 ----------

// FastlaneApplyOp 把候选写入经真实 Reducer 落地：op.Quote 取候选金额在原话
// 中的逐字子串，evidence=stated；返回落地后的状态。
func FastlaneApplyOp(prior schemas.RequirementState, quote string, value int) (schemas.RequirementState, error) {
	token := fastlaneAmountToken(quote, value)
	if token == "" {
		return prior, fmt.Errorf("fastlane: 金额 %d 在本轮原话中无逐字依据", value)
	}
	op := schemas.RequirementOperation{
		Op: "set", Field: decision.FastlaneField,
		Value: json.RawMessage(fmt.Sprintf("%d", value)),
		Quote: token, Evidence: "stated",
	}
	source := schemas.RequirementSource{Kind: "chat", MessageID: "fastlane-shadow", Quote: quote}
	return schemas.ApplyRequirementUpdate(prior, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{op}}, source)
}

func fastlaneAmountToken(quote string, value int) string {
	for _, token := range pipeline.FindBudgetAmountTokens(quote) {
		if token.Value == value {
			return token.Text
		}
	}
	return ""
}

// ---------- Jev 适配 ----------

// FastlaneWireState 是发给 Jev 的最小状态对象：本轮原话与候选字段/值。
// 不发送需求状态、聊天历史或任何密钥。
type FastlaneWireState struct {
	CurrentTurn    string          `json:"current_turn"`
	CandidateField string          `json:"candidate_field"`
	CandidateValue json.RawMessage `json:"candidate_value"`
}

// FastlaneQuestion 是绑定到决策域的 Jev 问题定义。
func FastlaneQuestion() jev.Question {
	options := map[string]string{}
	for verdict, text := range decision.FastlaneCriteria() {
		options[string(verdict)] = text
	}
	return jev.Question{ID: "budget_fastlane", Instructions: decision.FastlaneInstructions, Options: options}
}

// FastlaneJevJudge 把 jev.Client 适配为 decision.FastlaneJudge。
type FastlaneJevJudge struct {
	Client *jev.Client
}

// Judge 执行一次有界判定；错误恒为 jev 分类错误，由调用方决定回退。
func (j FastlaneJevJudge) Judge(ctx context.Context, in decision.FastlaneInput) (decision.FastlaneResult, error) {
	state, err := json.Marshal(FastlaneWireState{
		CurrentTurn:    in.CurrentTurn,
		CandidateField: in.CandidateField,
		CandidateValue: in.CandidateValue,
	})
	if err != nil {
		return decision.FastlaneResult{}, fmt.Errorf("fastlane jev state: %w", err)
	}
	choice, err := j.Client.Ask(ctx, state, FastlaneQuestion())
	if err != nil {
		return decision.FastlaneResult{}, err
	}
	res := decision.FastlaneResult{
		Verdict:             decision.FastlaneVerdict(choice.Choice),
		Probabilities:       map[decision.FastlaneVerdict]float64{},
		Confidence:          choice.Confidence,
		SelectedProbability: choice.SelectedProbability,
		ResponseModel:       choice.ResponseModel,
		InputTokens:         choice.InputTokens,
		OutputTokens:        choice.OutputTokens,
		Duration:            choice.Duration,
	}
	for raw, p := range choice.Probabilities {
		res.Probabilities[decision.FastlaneVerdict(raw)] = p
	}
	return res, nil
}

// ---------- 冻结 Screening 基线 ----------

// FrozenBudgetWrite 是 Screening 在一轮里对预算字段的真实写入。
type FrozenBudgetWrite struct {
	Op       string          `json:"op"`
	Field    string          `json:"field"`
	Value    json.RawMessage `json:"value,omitempty"`
	Evidence string          `json:"evidence,omitempty"`
}

// FrozenScreeningRepeat 是一次 repeat 的逐轮预算写入观测。
type FrozenScreeningRepeat struct {
	Repeat       int                 `json:"repeat"`
	Pass         bool                `json:"pass"`
	BudgetWrites []FrozenBudgetWrite `json:"budget_writes"`
}

// FrozenScreeningTurn 是按 (case, turn) 对齐的冻结 Screening 观测。
type FrozenScreeningTurn struct {
	CaseID    string
	TurnIndex int
	Repeats   []FrozenScreeningRepeat
}

// LoadFrozenScreeningBaseline 读取冻结 results.jsonl 并按 (id, turn) 对齐
// extraction/conversations 层的真实 Screening 操作。只读，不重新调用模型。
func LoadFrozenScreeningBaseline(path string) (map[string]FrozenScreeningTurn, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]FrozenScreeningTurn{}
	dec := json.NewDecoder(f)
	for {
		var row struct {
			Layer       string `json:"layer"`
			ID          string `json:"id"`
			Repeat      int    `json:"repeat"`
			Pass        bool   `json:"pass"`
			Observation struct {
				Turns []struct {
					Operations []schemas.RequirementOperation `json:"operations"`
				} `json:"turns"`
			} `json:"observation"`
		}
		if err := dec.Decode(&row); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if row.Layer != "extraction" && row.Layer != "conversations" {
			continue
		}
		for i, t := range row.Observation.Turns {
			key := fmt.Sprintf("%s#%d", row.ID, i)
			entry, ok := out[key]
			if !ok {
				entry = FrozenScreeningTurn{CaseID: row.ID, TurnIndex: i}
			}
			repeat := FrozenScreeningRepeat{Repeat: row.Repeat, Pass: row.Pass}
			for _, op := range t.Operations {
				if op.Field == decision.FastlaneField || strings.HasPrefix(op.Field, "budget") {
					repeat.BudgetWrites = append(repeat.BudgetWrites, FrozenBudgetWrite{
						Op: op.Op, Field: op.Field, Value: op.Value, Evidence: op.Evidence,
					})
				}
			}
			entry.Repeats = append(entry.Repeats, repeat)
			out[key] = entry
		}
	}
	return out, nil
}
