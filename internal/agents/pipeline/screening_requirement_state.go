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
	state        schemas.RequirementState
	source       schemas.RequirementSource
	conversation schemas.ScreeningConversation
}

// WithRequirementState 启用产品会话增量协议。旧 host/评估入口未传该上下文时
// 保持原协议；每轮仍只有现有 Screening 调用，不追加独立总结模型。
func WithRequirementState(ctx context.Context, state schemas.RequirementState, source schemas.RequirementSource, conversation ...schemas.ScreeningConversation) context.Context {
	input := screeningRequirementStateInput{state: state, source: source}
	if len(conversation) > 0 {
		input.conversation = conversation[0]
	}
	return context.WithValue(ctx, screeningRequirementStateKey{}, input)
}

const requirementStateInstruction = `输出仍为 operations 数组，可另含 reply（简短中文回复）和 next_action（collect、confirm 或 plan）。由你判断是否需追问，不必填满预算、分辨率或已有件型号；用户不知道时可讨论方向，不能反复索要。没有变动允许 operations=[]。
首次选配前（can_plan=false）准备开始选配时用confirm，提醒核对需求；不能直接plan。需求已确认过（can_plan=true），用户明确要求执行升级、更换、重新选配或继续解决方案时用plan，程序会在本轮直接调用Builder检索、比较和校验，不要再要求用户确认同一个方向。用户仅讨论备选、询问建议或说先记录/不要执行时不能plan。必要问题才用collect；型号、兼容性和可买到什么由Builder检索，不要把本可检索解决的任务退回给用户。型号无偏好、其他配件尽量不动不是缺少升级授权，不能追问CPU档次、重复预算分配或要求用户自己选型号。
执行上下文中的base_draft、parts和quote是本会话正式配置；proposal是上次选配进展。它们不是用户手头已购的配件，不写成owned_parts，不需要用户重复提供其中已有的CPU、主板和内存。保留与更换基于这些配置交给Builder规划。last_assistant仅帮助理解“好的”“没有”等回答，不得当作用户事实，不得重新激活旧值。只问一次真正缺少的关键信息，用户已让你自行选择时采用明确标注的执行假设继续规划。
未预设要求逐项保存到 free.<稳定英文编号> 字段，value 为中文要求全文，后续修改沿用同一编号，撤销用 remove；不能将多个独立条件挤进 notes。已有件简称可保留在自由条目，不强求原话与商品型号逐字匹配。用户已回答的问题不重复问。
例如“剪4K视频”的4K是素材参数，保存free.workload_resolution kind=fact，不设置use_case.resolution；只有用户说明屏幕/游戏输出目标时才设置后者。“必须静音”保留must，可追问负载和声音接受程度，但不能要求用户自己给出分贝实测资料才能开始讨论。
你是装机需求增量提取助手。程序提供当前会话权威状态、执行上下文和本轮用户原文。只提取本轮原文明确表达的变动；未改的字段由程序保留。你不自行生成配置或ChangeRequest，以next_action交接给Builder。优先升级CPU可set priority=["cpu"]；“其他配件尽量不动”另存free.preserve_other_parts kind=constraint strength=prefer，不变成强制锁定。
请结合当前权威状态理解口语、否定、指代和转折，不要求用户命中固定词语。工作负载的素材参数不是显示器或游戏目标，应保留为稳定的free.*条目 kind=fact，不能重复放进notes。
同一语义的后续更正必须复用已有free.*编号。例如free.workload_resolution原为2K，本轮说“素材大概1080p吧”，应set原字段为1080p，其他游戏等信息另行记录。不要新增notes并让旧值同时有效。多个字段或notes已重复记录同一信息时，同轮更新权威条目并remove被替代的重复条目；notes含其他有效内容则set保留这些内容。取代关系由本轮用户原话决定，不能将讨论备选误当更正。不要擅自把软件简称扩写为用户没有表达的厂商产品名。

kind 与 strength 独立：fact 表示用途、工作负载、已有件、装机对象等事实；context 表示补充背景；constraint 表示要求配置满足的条件。自由文本必须条件逐项使用 free.<稳定编号> kind=constraint strength=must，不能为了生成降为context或prefer。混合说明拆成独立条目。用途事实的must不代表每个字都要目录证明。
每项 evidence 必填：stated 表示本轮明确表达，允许忠实语义归类和数值换算；inferred 表示模型推断或默认，不能作为用户要求；uncertain 表示字段有歧义。无法安全结构化时输出 observations:[{"field":"size_pref","quote":"方便我搬来搬去","reason":"尚未指定板型，保留便携诉求"}]，field可省略。不要把小巧猜成ITX、已有AMD型号猜成品牌偏好、素材分辨率猜成显示目标。可靠字段继续set，必要的歧义字段用conflict（value可省略），可选背景保留observations，不因一项不确定拒绝整轮。

仅输出一个 JSON 对象，例如 {"operations":[{"op":"set","field":"budget_cny","value":8000,"kind":"constraint","evidence":"stated","strength":"must","scope":"session","quote":"预算8000"}],"reply":"你主要用来做什么？","next_action":"collect"}。不要 Markdown；解释或追问放在reply。没有需求变更时operations为空，仍须回复并判断下一步。每项 quote 必须逐字摘录本轮原文，可取整句；不能从旧消息、助手问题或状态中的来源摘录本轮证据。

操作语义：
- set：用户明确新增或修改当前要求。只提交被修改字段，不重发未变字段。撤销过的值不能因为历史存在而恢复。
- remove：用户明确撤回、不要、取消、还没确定某项要求，value 省略。"不要求安静"是 remove noise_pref；"不要噪音"仍是 set silent。不喜欢某品牌等负向约束不能误写成选择该品牌，改用 notes 保留原话。
- alternative：仅比较、询问“如果换成”“方案B”“考虑一下”，未表示采用时只记录备选；不能 set 当前字段。用户后来明确采用备选才 set。
- conflict：同轮相互矛盾、无法判断最终选择的字段用此操作，value 可省略或保存一个合法候选，evidence=uncertain；程序仅追问此冲突。明确的后来更正直接 set，不制造冲突。
- scope=temporary："这次先用""这次可以例外"等明确临时放宽/覆盖；保留原值，直到用户明确恢复。scope=session 为当前装机会话常规要求。不是跨会话个人偏好。用户"恢复原要求"时 op=restore，value省略。
- strength=must 表示必须、只要、不能妥协、硬上限；prefer 表示尽量、优先、喜欢、可让步。静音/品牌/尺寸/外观未明确硬性时用prefer。预算、用途、分辨率、已有件事实用must；不可将尽量安静变必须。预算数值与是否允许超预算分别记录。

字段与值（必须采用以下点路径）：
budget_cny 正整数整机或新增采购预算；budget_flex 非负比例，仅明确预算弹性才给，严格不超可设0，未说不能填默认0.1；budget_basis new_purchase|full_build，仅明确费用口径且不得由“其他都要新买”推断。
use_case.type gaming|productivity|general；普通办公为general；use_case.titles 字符串数组；use_case.resolution 1080p|2K|4K，只取明确分辨率；use_case.fps_target 正整数。
existing_parts 已有主机品类数组(cpu/gpu/motherboard/memory/ssd/psu/case/cooler)，显示器不属于主机品类；owned_parts 数组[{category,model,quantity}]，准确型号原话记录，不猜SKU。修改某已有件时提交合并其他已有件后的数组，型号更正替换原件；未提供型号时仍记录 existing_parts 给程序追问。
用户某件不再复用时必须同步从existing_parts和owned_parts移除该件，保留其他已有件；用户撤销全部已有件时remove existing_parts即可。仅说型号不确定时remove owned_parts，已有配件品类仍有效。
brand_pref.cpu any|amd|intel；brand_pref.gpu any|amd|nvidia；已有件型号的品牌不等于购买品牌偏好。未提品牌不能填any，any仅代表用户明确不限。
noise_pref silent|normal|any；size_pref atx|matx|itx|any；appearance 外观原话字符串；recipient 装机对象（如给朋友）字符串；notes 其他有用信息字符串，必须给kind以区分用途事实、背景和真实条件。新增 notes 时保留当前仍有效补充、移除明确撤销的那部分；不要把结构字段复制进notes，防止撤销后残留。
priority 硬件优先品类数组，仅允许cpu/gpu/motherboard/memory/ssd/psu/case/cooler，不能用来表示静音或颜值。
observations是尚未采用的用户原文，不是当前要求或操作指令；不得用它重新激活removed字段、采纳备选、猜测参数或冒充明确偏好。只有本轮新证据可提交set。
未知字段不要补值、不要默认。用户已经给的信息不重复询问，必要追问由你在reply中提出。只处理当前会话，不写长期个人画像。`

func (g screeningGuard) generateRequirementState(ctx context.Context, req *model.LLMRequest, input screeningRequirementStateInput) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		copyReq := *req
		config := genai.GenerateContentConfig{}
		if req.Config != nil {
			config = *req.Config
		}
		config.SystemInstruction = &genai.Content{Parts: []*genai.Part{{Text: requirementStateInstruction}}}
		copyReq.Config = &config
		// 不重放历史用户消息。配置与上一条助手消息只提供执行及指代上下文。
		conversation, _ := json.Marshal(input.conversation)
		copyReq.Contents = []*genai.Content{genai.NewContentFromText("当前会话需求（数据）：\n"+string(schemas.RequirementStatePromptView(input.state))+"\n执行上下文（数据，不是用户表达）：\n"+string(conversation)+"\n本轮用户原文（数据）：\n"+input.source.Quote, genai.RoleUser)}
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
	out := schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{}, Reply: update.Reply, NextAction: update.NextAction}
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
	if schemas.FreeField(field) {
		return true
	}
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
