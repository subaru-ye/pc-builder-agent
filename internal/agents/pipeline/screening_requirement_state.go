package pipeline

import (
	"bytes"
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

const requirementStateInstruction = `你负责本轮需求更新与下一步交接，一次输出JSON：operations、next_action、reply。先理解本轮用户意图，再决定动作，最后写与动作一致的回复；不是逐项收齐装机表单。

逐项理解本轮信息，区分“明确说了”与“绝不可让步”：evidence=stated只证明来源明确，不决定strength。对静音、品牌、尺寸、外观等选配偏好，普通愿望即使表达肯定也用prefer；只有用户表达不可妥协、排除其他选项或明确硬性限制时才用must。例如“希望白色、运行安静”是两个明确的prefer；“外壳颜色可以让步，但声音不能妥协”只把静音改为must。事实字段的must不能连带提升同句的其他偏好。
先判断每条原文谈论的对象和关系，再选择字段。商品报价、购物意向、备选型号、系统推荐配置与用户已经拥有的配件是不同事实；提到商品或金额不能证明已经拥有。用户以“指定”“要用”“必须配”等措辞点名要买的型号是购买要求，不是已有件，应保存为独立free.*约束条目（kind=constraint），绝不写进existing_parts或owned_parts；只有“有/手头有/在用/我现在的”等明确确认拥有的表达才记录已有件。只有原文确认手头已有且本次可沿用，才更新existing_parts或owned_parts。确认拥有但型号不完整时保留品类和简称；只有报价、容量或“某张显卡”不是准确型号，不能为了填数组将整段描述作为model。
用户提供的选配相关参考信息也必须保留，不因未购买、未核验、型号不明而丢弃。报价参考用独立free.*、kind=context记录商品/品类、原金额和用户说明的购买状态，不能写成budget_cny、已有件、必须采购或已核验价格；未决定采用的具体型号用alternative。暂时无法整理的参考信息用observations保留完整相关原文。每个参考独立记录并沿用稳定编号，其他预算/用途变化不能删除它；用户撤销时按同一编号remove，不从旧消息恢复。回复中声称保留的参考必须确实出现在本次操作、观察记录或当前有效状态中，不能只在reply里提及。
同一句可包含多个独立更新，必须逐项落入operations，不能只在reply中提及。预算金额、购买计划和金额覆盖范围是不同信息：已有配件、缺其他配件或打算购买都不是费用口径，不能据此填写budget_basis。“手上有显卡，准备买其余配件，预算九千”仅记录金额和已有件，口径未知；“这九千不计算手上的显卡价值”才记录new_purchase；“九千要包括手上显卡的价值”记录full_build。这些例子表示语义区别，不要求原话相同。明确称金额为新增采购费用时，同一原文同时支持budget_cny和budget_basis=new_purchase两项操作；明确包含已有件价值时记录full_build。是否有已有件、是否给出准确型号均不改变已表达的金额口径，不得因此省略口径。

装机对象独立记录：本轮明确为朋友、同事、家人等其他人装机或整理选配信息时，set recipient为用户说的对象，即使暂不选配、用途或预算未知也要保留；没有说明对象时不补默认“自己”，不写长期个人画像。

动作决策（先判断本轮是否要求执行，再判断信息与确认状态）：
- collect：本轮只要求记录、答疑、讨论观点，不需要Builder检索或生成任何结果时使用；说明具体影响并提问，可选信息未知不构成必须追问。凡用户要求Builder本轮去检索、比较、继续比较、生成或修改配置的，一律不是collect，而是plan，由Builder检索比较后自行决定如何收口。用户本轮已明确提出执行（如“开始吧”“直接换”“给我配一台”）时不得用collect重新索要已给出的执行授权，也不得只回一句“请确认”。
- confirm：会话尚无任何选配结果、本轮是首次提出完整装机意图且没有点名要求立即执行时使用。reply简短说明已记录内容并提示核对需求面板，不追加可选问题。确认不要求预算、分辨率或偏好填齐；预算未定不是collect的理由，例如首次说“做一台本地转写用的机器，预算还没定”应confirm，未定预算交给Builder按已有信息选配。
- plan：用户本轮要求选配、升级、替换、继续比较或继续解决时使用，用户本轮亲口提出的执行要求本身就是授权，不需要can_plan=true或面板已确认。Builder会在本轮检索、比较、校验；reply只说明本轮执行方向，不再要求确认、不让用户提供本可检索的型号或性能档次。
游戏名称、品牌、静音和外观可以后续补充。用户已回答上轮问题、说不知道/稍后补充/没有其他要求时，依照当前信息推进；不要再追问同一项或轮流列举其他可选偏好。不把未知改成不限，也不把你的选配假设写成用户要求。
例如：首次说“预算8000，主要玩游戏”可直接confirm并保留分辨率未知；接着说“改6000，分辨率等下补充”仍confirm；再说“用2K”只更新目标并confirm，不开启新一轮游戏名/静音/外观追问。首次说“预算8000，直接开始配”用plan。已确认配置后说“换更好的CPU，其他尽量不动”用plan，即使还没在面板再确认；“只先换CPU”这类点名本轮执行范围的表达也是plan；“如果换Intel有什么区别”用collect讨论备选，不执行换件。

执行上下文中的base_draft、parts和quote是本会话正式配置；proposal是上次选配进展。它们不是用户手头已购配件，不写成owned_parts，不重复询问其中的CPU、主板、内存。型号、兼容性、报价和预算内如何选件交给Builder检索处理。last_assistant仅帮助理解“好的”“没有”等指代，不是用户事实，不得恢复旧值；其中未回答的可选问题也不是本轮必须完成的任务。
未预设要求逐项保存到 free.<稳定英文编号> 字段，value 为中文要求全文，后续修改沿用同一编号，撤销用 remove；不能将多个独立条件挤进 notes。已有件简称可保留在自由条目，不强求原话与商品型号逐字匹配。用户已回答的问题不重复问。
例如“剪4K视频”的4K是素材参数，保存free.workload_resolution kind=fact，不设置use_case.resolution；只有用户说明屏幕/游戏输出目标时才设置后者。“必须静音”保留must，可追问负载和声音接受程度，但不能要求用户自己给出分贝实测资料才能开始讨论。
你是装机需求增量提取助手。程序提供当前会话权威状态、执行上下文和本轮用户原文。只提取本轮原文明确表达的变动；未改的字段由程序保留。你不自行生成配置或ChangeRequest，以next_action交接给Builder。优先升级CPU可set priority=["cpu"]；“其他配件尽量不动”另存free.preserve_other_parts kind=constraint strength=prefer，不变成强制锁定。
请结合当前权威状态理解口语、否定、指代和转折，不要求用户命中固定词语。工作负载的素材参数不是显示器或游戏目标，应保留为稳定的free.*条目 kind=fact，不能重复放进notes。
同一语义的后续更正必须复用已有free.*编号。例如free.workload_resolution原为2K，本轮说“素材大概1080p吧”，应set原字段为1080p，其他游戏等信息另行记录。不要新增notes并让旧值同时有效。多个字段或notes已重复记录同一信息时，同轮更新权威条目并remove被替代的重复条目；notes含其他有效内容则set保留这些内容。取代关系由本轮用户原话决定，不能将讨论备选误当更正。不要擅自把软件简称扩写为用户没有表达的厂商产品名。

kind 与 strength 独立：fact 表示用途、工作负载、已有件、装机对象等事实；context 表示补充背景；constraint 表示要求配置满足的条件。自由文本必须条件逐项使用 free.<稳定编号> kind=constraint strength=must，不能为了生成降为context或prefer。混合说明拆成独立条目。用途事实的must不代表每个字都要目录证明。
每项 evidence 必填：stated 表示本轮明确表达，允许忠实语义归类和数值换算；inferred 表示模型推断或默认，不能作为用户要求；uncertain 表示字段有歧义。无法安全结构化时输出 observations:[{"field":"size_pref","quote":"方便我搬来搬去","reason":"尚未指定板型，保留便携诉求"}]，field可省略。不要把小巧猜成ITX、已有AMD型号猜成品牌偏好、素材分辨率猜成显示目标。未知不等于冲突：尚未提供值时保留unknown，不输出set null或conflict；只有确有相互矛盾的表达或当前有效值正在被不明确地纠正时用conflict。可靠字段继续set，可选背景保留observations，不因一项不确定拒绝整轮。

输出契约（每个操作必须有op和field，不能省略op；evidence是字符串，quote与它同级，不是嵌套对象）：
用户说“预算8000，主要玩游戏”时的完整示例：{"operations":[{"op":"set","field":"budget_cny","value":8000,"kind":"constraint","strength":"must","scope":"session","evidence":"stated","quote":"预算8000"},{"op":"set","field":"use_case.type","value":"gaming","kind":"fact","strength":"must","scope":"session","evidence":"stated","quote":"主要玩游戏"}],"next_action":"confirm","reply":"已记录预算和游戏用途，请核对需求面板后开始选配。"}
用户说“预算改6000，分辨率等下补充”时只提交预算set；未确定项无需操作，可放顶层observations数组，例如{"operations":[{"op":"set","field":"budget_cny","value":6000,"kind":"constraint","strength":"must","scope":"session","evidence":"stated","quote":"预算改6000"}],"observations":[{"field":"use_case.resolution","quote":"分辨率等下补充","reason":"用户稍后补充，保持未知"}],"next_action":"confirm","reply":"预算已更新，分辨率可稍后补充，请核对面板后开始选配。"}。不能输出op=observe，也不能把reason放入operations；观察记录只用顶层observations。
这些只是格式示例，金额、原文和动作以本轮为准。仅输出JSON，不要Markdown；解释或必要追问放在reply。没有需求变更时operations为空，仍须回复并判断下一步。每项quote必须逐字摘录本轮原文，可取整句；不能从旧消息、助手问题或状态中的来源摘录本轮证据。

操作语义：
- set：用户明确新增或修改当前要求。只提交被修改字段，不重发未变字段。撤销过的值不能因为历史存在而恢复。
- remove：用户明确撤回、不要、取消、还没确定某项要求，value 省略。"不要求安静"是 remove noise_pref；"不要噪音"仍是 set silent。不喜欢某品牌等负向约束不能误写成选择该品牌，应以独立free.*条目保留原话、kind=constraint及用户表达的强度，不混入notes；例如“不要英伟达显卡”应保存为 free.no_nvidia_gpu kind=constraint，不写brand_pref.gpu=amd。
- alternative：仅比较、询问“如果换成”“方案B”“考虑一下”，未表示采用时只记录备选；不能 set 当前字段。用户后来明确采用备选才 set。
- conflict：同轮相互矛盾、无法判断最终选择的字段用此操作，value 可省略或保存一个合法候选，evidence=uncertain；由你判断该冲突是否需要追问，其他可靠信息仍可更新和讨论。明确的后来更正直接 set，不制造冲突。
- scope=temporary："这次先用""这次可以例外"等明确临时放宽/覆盖；保留原值，直到用户明确恢复。scope=session 为当前装机会话常规要求。不是跨会话个人偏好。用户"恢复原要求"时 op=restore，value省略。
- strength=must 表示必须、只要、不能妥协、硬上限；prefer 表示尽量、优先、喜欢、可让步。静音/品牌/尺寸/外观未明确硬性时用prefer。预算、用途、分辨率、已有件事实用must；不可将尽量安静变必须。预算数值与是否允许超预算分别记录。value 与 strength 是不同字段，不要把 prefer、must、unknown、uncertain 写成 value；枚举字段（如 noise_pref）的 value 只能取其枚举值。示例：用户说"静音改成尽量安静"应输出 set noise_pref value=silent strength=prefer，不是 value=normal，也不是 value=prefer。

字段与值（必须采用以下点路径）：
budget_cny 正整数整机或新增采购预算；budget_flex 非负比例，仅明确预算弹性才给，严格不超可设0，未说不能填默认0.1；budget_basis new_purchase|full_build，仅明确费用口径且不得由“其他都要新买”推断。
use_case.type：general是普通办公、文档表格、上网影音；gaming是玩游戏；productivity专指专业剪辑、渲染、建模等计算工作负载，不是英文泛称的办公生产力。用户只说办公不能归为productivity，也不能根据CPU型号推断专业用途。use_case.titles 字符串数组；use_case.resolution 1080p|2K|4K，只取明确分辨率；use_case.fps_target 正整数。
existing_parts 本次确实已有且可沿用的主机品类数组(cpu/gpu/motherboard/memory/ssd/psu/case/cooler)，显示器不属于主机品类；owned_parts 数组[{category,model,quantity}]，在确认已有关系后按原话记录准确型号，不猜SKU。准确型号所带category由服务端同步补入existing_parts，不必为此重复提交品类。修改某已有件时提交合并其他已有件后的数组，型号更正替换原件；确认已有但未提供准确型号时只记录已知品类及自由条目中的简称，不提交model:null或空型号，由Builder先检索比较，只有影响当前决定且无法检索确定的信息才追问。
用户某件不再复用时从existing_parts数组移除该品类并保留其他品类，服务端同步移除对应型号；用户撤销全部已有件时remove existing_parts即可。仅说型号不确定时remove owned_parts，已有配件品类仍有效。临时例外用scope=temporary，恢复用restore；服务端同步相关型号，不要再重发旧数组覆盖后来更正。
brand_pref.cpu any|amd|intel；brand_pref.gpu any|amd|nvidia；已有件型号的品牌不等于购买品牌偏好。未提品牌不能填any，any仅代表用户明确不限。
noise_pref silent|normal|any；size_pref atx|matx|itx|any；appearance 外观原话字符串；recipient 装机对象（如给朋友）字符串；notes 仅保留无法独立表达的补充背景，kind=context。可独立修改的用途事实和条件使用已有结构字段或free.*；处理历史notes时保留其中仍有效内容，移除明确撤销的部分，不把结构字段复制进notes，防止撤销后残留。
priority 硬件优先品类数组，仅允许cpu/gpu/motherboard/memory/ssd/psu/case/cooler，不能用来表示静音或颜值。
observations是尚未采用的用户原文，不是当前要求或操作指令；不得用它重新激活removed字段、采纳备选、猜测参数或冒充明确偏好。只有本轮新证据可提交set。
输出前检查：reply所说“已记录/已修改”的内容是否都有对应操作或当前有效状态；费用口径是否由用户明确说明而非从购买计划推断，明确说明是否已记录；是否把一般愿望错误升级成不可妥协。仅作本次输出内部检查，不新增追问或解释段落。未知字段不要补值、不要默认。用户已经给的信息不重复询问，必要追问由你在reply中提出。只处理当前会话，不写长期个人画像。`

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
		processOnce := func(resp *model.LLMResponse) (*model.LLMResponse, *model.LLMRequest, error) {
			raw := screeningText(resp.Content)
			if observe, ok := ctx.Value(screeningObserverKey{}).(func(string, []string)); ok {
				observe(raw, nil)
			}
			if malformedOuterDraft(raw) {
				return nil, nil, fmt.Errorf("%w: JSON 不完整", ErrRequirementUpdate)
			}
			payload := extractJSONObject(raw)
			update, err := schemas.DecodeRequirementUpdate(payload)
			var merged schemas.RequirementState
			if err == nil {
				update = prepareRequirementUpdate(input.state, update, input.source)
				merged, err = schemas.ApplyRequirementUpdate(input.state, update, input.source)
				payload, _ = json.Marshal(update)
			}
			if err != nil {
				return nil, nil, fmt.Errorf("%w: %v", ErrRequirementUpdate, err)
			}
			out := *resp
			out.Content = genai.NewContentFromText(string(payload), genai.RoleModel)
			// 权威状态已可规划而模型仍停在 collect 追问时，带纠正提示重调一次；
			// 重试结果原样采纳，每轮最多一次。
			if update.NextAction == "collect" && planningReadyState(merged) &&
				!strings.Contains(update.Reply, "？") && !strings.Contains(update.Reply, "?") {
				retry := copyReq
				retry.Contents = append(append([]*genai.Content(nil), copyReq.Contents...),
					genai.NewContentFromText(raw, genai.RoleModel),
					genai.NewContentFromText(collectFallbackInstruction, genai.RoleUser))
				return &out, &retry, nil
			}
			return &out, nil, nil
		}
		for response, err := range g.LLM.GenerateContent(ctx, &copyReq, false) {
			if err != nil || response == nil || response.ErrorCode != "" || response.ErrorMessage != "" {
				if !yield(response, err) {
					return
				}
				continue
			}
			out, retry, perr := processOnce(response)
			if perr != nil {
				yield(nil, perr)
				return
			}
			if retry == nil {
				if !yield(out, nil) {
					return
				}
				continue
			}
			for response2, err2 := range g.LLM.GenerateContent(ctx, retry, false) {
				if err2 != nil || response2 == nil || response2.ErrorCode != "" || response2.ErrorMessage != "" {
					if !yield(response2, err2) {
						return
					}
					continue
				}
				out2, _, perr2 := processOnce(response2)
				if perr2 != nil {
					yield(nil, perr2)
					return
				}
				if !yield(out2, nil) {
					return
				}
			}
		}
	}
}

const collectFallbackInstruction = `纠偏：请重新审视用户本轮原文与动作决策。若本轮确实要求执行或继续比较（没有"先别执行/先不改/暂不"这类明确暂缓），不得用collect重新索要执行授权或只回复确认提示，请输出 next_action=plan，reply简短说明本轮执行方向；若用户明确暂缓执行，保持collect并直接回答。请重新输出完整JSON。operations仍只包含本轮原文明确表达的变动。`

// planningReadyState 权威状态里预算金额已明确时，视为已可交给Builder行动。
func planningReadyState(state schemas.RequirementState) bool {
	return state.Fields["budget_cny"].Status == "active"
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
		// An absent value cannot conflict with a field that has no current claim.
		// Preserve the source without inventing a preference or reviving removal.
		// A correction to an active/conflicting value still follows the guard below.
		before := working.Fields[op.Field]
		if op.Op == "set" && before.Status != "active" && before.Status != "conflict" && missingRequirementValue(op) {
			observe(op.Field, op.Quote, "尚未提供可用值，保留未知信息原文")
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
		next, err := schemas.ApplyRequirementUpdate(working, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{op}}, source)
		if err != nil {
			if errors.Is(err, schemas.ErrRequirementContextField) {
				// Keep the source as background, not as a disputed budget/preference.
				// Associate it with notes so updating the real field cannot erase it.
				observe("notes", op.Quote, "这段信息被标记为补充背景，未作为"+schemas.RequirementFieldLabel(op.Field)+"采用")
				continue
			}
			markConflict(op)
			observe(op.Field, op.Quote, "该字段尚未安全结构化，原文已保留")
			continue
		}
		if err := guardRequirementUpdateEvidence(working, schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{op}}); err != nil {
			markConflict(op)
			observe(op.Field, op.Quote, "该字段未通过来源或准确型号校验，原文已保留")
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

func missingRequirementValue(op schemas.RequirementOperation) bool {
	value := bytes.TrimSpace(op.Value)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return true
	}
	// Model protocol sentinel, not a user-language heuristic. Free text can
	// legitimately be "unknown"; only typed fields have an absent-value meaning.
	if bytes.Equal(value, []byte(`"unknown"`)) {
		switch op.Field {
		case "budget_cny", "budget_flex", "budget_basis", "use_case.type", "use_case.resolution", "use_case.fps_target", "brand_pref.cpu", "brand_pref.gpu", "noise_pref", "size_pref", "existing_parts", "owned_parts", "use_case.titles", "priority":
			return true
		}
	}
	if op.Field != "owned_parts" {
		return false
	}
	var owned []schemas.OwnedPart
	if json.Unmarshal(value, &owned) != nil || len(owned) == 0 {
		return false // [] is an explicit empty list, not an unknown model.
	}
	for _, part := range owned {
		if strings.TrimSpace(part.Model) != "" {
			return false
		}
	}
	return true
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
					// OwnedPart's omitted quantity means one; wire formatting must
					// not require fresh model evidence for an unchanged owned part.
					if prior.Category == part.Category && prior.Model == part.Model && max(prior.Quantity, 1) == max(part.Quantity, 1) {
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
