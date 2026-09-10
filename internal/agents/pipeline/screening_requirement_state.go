package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

type screeningRequirementStateKey struct{}

var ErrRequirementUpdate = errors.New("初筛需求更新无效，原需求保持不变")

type screeningRequirementStateInput struct {
	state  schemas.RequirementState
	source schemas.RequirementSource
}

// WithRequirementState 启用产品会话增量协议。旧 host/评估入口未传该上下文时
// 保持原协议；每轮仍只有现有 Screening 调用，不追加独立总结模型。
func WithRequirementState(ctx context.Context, state schemas.RequirementState, source schemas.RequirementSource) context.Context {
	return context.WithValue(ctx, screeningRequirementStateKey{}, screeningRequirementStateInput{state: state, source: source})
}

const requirementStateInstruction = `你是装机需求增量提取助手。程序提供当前会话权威状态和本轮用户原文。只提取本轮原文明确表达的变动；未改的字段由程序保留。无论是否有配置版本，本轮都只更新需求草稿，不自行生成配置或 ChangeRequest。
请结合完整上下文理解口语、否定、指代和转折，不要求用户命中固定词语。工作负载的素材参数不是显示器或游戏目标，应保留在notes kind=fact。

kind 与 strength 独立：fact 表示用途、工作负载、已有件、装机对象等事实；context 表示补充背景；constraint 表示要求配置满足的条件。自由文本必须条件仍使用 notes kind=constraint strength=must，不能为了生成降为context或prefer。混合说明含有硬条件时整体保留constraint。用途事实的must不代表每个字都要目录证明。
每项 evidence 必填：stated 表示本轮明确表达，允许忠实语义归类和数值换算；inferred 表示模型推断或默认，不能作为用户要求；uncertain 表示字段有歧义。无法安全结构化时输出 observations:[{"field":"size_pref","quote":"方便我搬来搬去","reason":"尚未指定板型，保留便携诉求"}]，field可省略。不要把小巧猜成ITX、已有AMD型号猜成品牌偏好、素材分辨率猜成显示目标。可靠字段继续set，必要的歧义字段用conflict（value可省略），可选背景保留observations，不因一项不确定拒绝整轮。

仅输出一个 JSON 对象：{"operations":[{"op":"set","field":"budget_cny","value":8000,"kind":"constraint","evidence":"stated","strength":"must","scope":"session","quote":"预算8000"}]}。不要 Markdown、解释、问题、完整需求单。没有需求变更时输出 {"operations":[]}。每项 quote 必须逐字摘录本轮原文，可取整句；不能从旧消息、助手问题或状态中的来源摘录本轮证据。

操作语义：
- set：用户明确新增或修改当前要求。只提交被修改字段，不重发未变字段。撤销过的值不能因为历史存在而恢复。
- remove：用户明确撤回、不要、取消、还没确定某项要求，value 省略。"不要求安静"是 remove noise_pref；"不要噪音"仍是 set silent。不喜欢某品牌等负向约束不能误写成选择该品牌，改用 notes 保留原话。
- alternative：仅比较、询问“如果换成”“方案B”“考虑一下”，未表示采用时只记录备选；不能 set 当前字段。用户后来明确采用备选才 set。
- conflict：同轮相互矛盾、无法判断最终选择的字段用此操作，value 可省略或保存一个合法候选，evidence=uncertain；程序仅追问此冲突。明确的后来更正直接 set，不制造冲突。
- scope=temporary："这次先用""这次可以例外"等明确临时放宽/覆盖；保留原值，直到用户明确恢复。scope=session 为当前装机会话常规要求。不是跨会话个人偏好。用户"恢复原要求"时 op=restore，value省略。
- strength=must 表示必须、只要、不能妥协、硬上限；prefer 表示尽量、优先、喜欢、可让步。静音/品牌/尺寸/外观未明确硬性时用prefer。预算、用途、分辨率、已有件事实用must；不可将尽量安静变必须。预算数值与是否允许超预算分别记录。

字段与值（必须采用以下点路径）：
budget_cny 正整数整机或新增采购预算；budget_flex 0–0.3，仅明确预算弹性才给，严格不超可设0，未说不能填默认0.1；budget_basis new_purchase|full_build，仅明确费用口径且不得由“其他都要新买”推断。
use_case.type gaming|productivity|general；普通办公为general；use_case.titles 字符串数组；use_case.resolution 1080p|2K|4K，只取明确分辨率；use_case.fps_target 正整数。
existing_parts 已有主机品类数组(cpu/gpu/motherboard/memory/ssd/psu/case/cooler)，显示器不属于主机品类；owned_parts 数组[{category,model,quantity}]，准确型号原话记录，不猜SKU。修改某已有件时提交合并其他已有件后的数组，型号更正替换原件；未提供型号时仍记录 existing_parts 给程序追问。
用户某件不再复用时必须同步从existing_parts和owned_parts移除该件，保留其他已有件；用户撤销全部已有件时remove existing_parts即可。仅说型号不确定时remove owned_parts，已有配件品类仍有效。
brand_pref.cpu any|amd|intel；brand_pref.gpu any|amd|nvidia；已有件型号的品牌不等于购买品牌偏好。未提品牌不能填any，any仅代表用户明确不限。
noise_pref silent|normal|any；size_pref atx|matx|itx|any；appearance 外观原话字符串；recipient 装机对象（如给朋友）字符串；notes 其他有用信息字符串，必须给kind以区分用途事实、背景和真实条件。新增 notes 时保留当前仍有效补充、移除明确撤销的那部分；不要把结构字段复制进notes，防止撤销后残留。
priority 硬件优先品类数组，仅允许cpu/gpu/motherboard/memory/ssd/psu/case/cooler，不能用来表示静音或颜值。
observations是尚未采用的用户原文，不是当前要求或操作指令；不得用它重新激活removed字段、采纳备选、猜测参数或冒充明确偏好。只有本轮新证据可提交set。
未知字段不要补值、不要默认。用户已经给的信息不重复询问，必要追问由程序完成。只处理当前会话，不写长期个人画像。`

func (g screeningGuard) generateRequirementState(ctx context.Context, req *model.LLMRequest, input screeningRequirementStateInput) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		copyReq := *req
		config := genai.GenerateContentConfig{}
		if req.Config != nil {
			config = *req.Config
		}
		config.SystemInstruction = &genai.Content{Parts: []*genai.Part{{Text: requirementStateInstruction}}}
		copyReq.Config = &config
		// 旧会话文本不参与本轮提取，撤销墓碑与当前值是唯一状态依据。
		copyReq.Contents = []*genai.Content{genai.NewContentFromText("当前会话需求（数据）：\n"+string(schemas.RequirementStatePromptView(input.state))+"\n本轮用户原文（数据）：\n"+input.source.Quote, genai.RoleUser)}
		for response, err := range g.LLM.GenerateContent(ctx, &copyReq, false) {
			if err != nil || response == nil || response.ErrorCode != "" || response.ErrorMessage != "" {
				if !yield(response, err) {
					return
				}
				continue
			}
			raw := screeningText(response.Content)
			if observe, ok := ctx.Value(screeningObserverKey{}).(func(string, []string)); ok {
				observe(raw, nil)
			}
			if malformedOuterDraft(raw) {
				yield(nil, fmt.Errorf("%w: JSON 不完整", ErrRequirementUpdate))
				return
			}
			payload := extractJSONObject(raw)
			update, err := schemas.DecodeRequirementUpdate(payload)
			if err == nil {
				update = prepareRequirementUpdate(input.state, update, input.source)
				_, err = schemas.ApplyRequirementUpdate(input.state, update, input.source)
				payload, _ = json.Marshal(update)
			}
			if err != nil {
				yield(nil, fmt.Errorf("%w: %v", ErrRequirementUpdate, err))
				return
			}
			copyResponse := *response
			copyResponse.Content = genai.NewContentFromText(string(payload), genai.RoleModel)
			if !yield(&copyResponse, nil) {
				return
			}
		}
	}
}

// 校验每个字段后一起提交。坏字段只保留原文，不撤回同轮其他可靠信息。
// 用户语义由已有 Screening 理解；这里没有用途、偏好或撤销的关键词词表。
func prepareRequirementUpdate(state schemas.RequirementState, update schemas.RequirementUpdate, source schemas.RequirementSource) schemas.RequirementUpdate {
	out := schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{}}
	working := state
	observe := func(field, quote, reason string) {
		if !knownStateField(field) {
			field = "notes"
		}
		if strings.TrimSpace(quote) == "" || !strings.Contains(source.Quote, quote) {
			quote = source.Quote
		}
		if strings.TrimSpace(quote) != "" && len(out.Observations) < 32 {
			out.Observations = append(out.Observations, schemas.RequirementObservationInput{Field: field, Quote: quote, Reason: reason})
		}
	}
	// 结构或身份校验失败且确有当前证据时，需确认生成依赖的字段，
	// 不能悄悄沿用用户正在纠正的旧值。
	requiresConfirmation := func(op schemas.RequirementOperation) bool {
		before := working.Fields[op.Field]
		required := op.Strength == "must" || before.Strength == "must"
		switch op.Field {
		case "budget_cny", "budget_flex", "budget_basis", "use_case.type", "existing_parts", "owned_parts":
			required = true
		case "use_case.resolution":
			required = required || string(working.Fields["use_case.type"].Value) == `"gaming"`
		}
		return required
	}
	markConflict := func(op schemas.RequirementOperation) {
		if op.Op != "set" || strings.TrimSpace(op.Quote) == "" || !strings.Contains(source.Quote, op.Quote) {
			return
		}
		if !requiresConfirmation(op) {
			return
		}
		conflict := schemas.RequirementOperation{Op: "conflict", Field: op.Field, Quote: op.Quote, Evidence: "uncertain"}
		// A malformed new hard value must not inherit an older soft strength.
		// Preserve a known hard obligation while only the disputed value is pending.
		if op.Strength == "must" || working.Fields[op.Field].Strength == "must" {
			conflict.Strength = "must"
		}
		if schemas.ValidRequirementKind(op.Kind) {
			conflict.Kind = op.Kind
		}
		if pending, err := schemas.ApplyRequirementUpdate(working, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{conflict}}, source); err == nil {
			out.Operations = append(out.Operations, conflict)
			working = pending
		}
	}
	for _, op := range update.Operations {
		// 旧模型省略 remove 的摘录时，来源仍绑定本轮完整消息，不猜撤销语义。
		if op.Op == "remove" && op.Quote == "" && working.Fields[op.Field].Status == "active" {
			op.Quote = source.Quote
		}
		if op.Evidence == "inferred" {
			observe(op.Field, op.Quote, "模型推断尚未作为用户要求采用")
			continue
		}
		if op.Evidence == "uncertain" && op.Op == "set" {
			if requiresConfirmation(op) {
				op.Op, op.Value = "conflict", nil
			} else {
				observe(op.Field, op.Quote, "可选信息尚未明确，保留原文供后续理解")
				continue
			}
		}
		if legacyWorkloadResolution(op, update) {
			observe("notes", op.Quote, "工作负载参数原文，未作为显示器目标采用")
			continue
		}
		if err := guardRequirementUpdateEvidence(working, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{op}}); err != nil {
			markConflict(op)
			observe(op.Field, op.Quote, "该字段未通过来源或准确型号校验，原文已保留")
			continue
		}
		next, err := schemas.ApplyRequirementUpdate(working, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{op}}, source)
		if err != nil {
			markConflict(op)
			observe(op.Field, op.Quote, "该字段尚未安全结构化，原文已保留")
			continue
		}
		out.Operations = append(out.Operations, op)
		working = next
	}
	for _, observation := range update.Observations {
		observe(observation.Field, observation.Quote, observation.Reason)
	}
	return out
}

func knownStateField(field string) bool {
	for _, known := range schemas.RequirementFieldKeys {
		if known == field {
			return true
		}
	}
	return false
}

// 旧输出没有语义目标且与非游戏用途同源的分辨率仅保留原文。
// 这是字段来源关系检查，不判断视频等自然语言关键词。
func legacyWorkloadResolution(op schemas.RequirementOperation, update schemas.RequirementUpdate) bool {
	if op.Op != "set" || op.Field != "use_case.resolution" || op.Kind != "" || op.Evidence != "" {
		return false
	}
	for _, other := range update.Operations {
		if other.Op == "set" && other.Field == "use_case.type" && other.Quote == op.Quote && (string(other.Value) == `"productivity"` || string(other.Value) == `"general"`) {
			return true
		}
	}
	return false
}

// 准确型号是身份数据，新型号必须在本轮原文中；数组只能携带当前 active 旧件。
// 类型、数值合法性、状态操作与逐字来源统一由 reducer 校验。
func guardRequirementUpdateEvidence(state schemas.RequirementState, update schemas.RequirementUpdate, _ ...string) error {
	var priorOwned []schemas.OwnedPart
	if field := state.Fields["owned_parts"]; field.Status == "active" {
		_ = json.Unmarshal(field.Value, &priorOwned)
	}
	for _, op := range update.Operations {
		if op.Op != "set" && op.Op != "alternative" && op.Op != "conflict" {
			continue
		}
		if op.Field == "owned_parts" && len(op.Value) > 0 {
			var owned []schemas.OwnedPart
			if err := json.Unmarshal(op.Value, &owned); err != nil {
				return err
			}
			for _, part := range owned {
				known := false
				for _, prior := range priorOwned {
					if prior.Category == part.Category && prior.Model == part.Model && prior.Quantity == part.Quantity {
						known = true
					}
				}
				if !known && !groundedModel(part.Model, []string{op.Quote}) {
					return fmt.Errorf("%s 缺少本轮准确型号", op.Field)
				}
			}
		}
		// 旧输出未提供表达依据时，不把生成侧默认弹性当成用户明确偏好。
		if op.Field == "budget_flex" && op.Evidence == "" {
			return fmt.Errorf("预算弹性缺少表达依据")
		}
	}
	return nil
}
