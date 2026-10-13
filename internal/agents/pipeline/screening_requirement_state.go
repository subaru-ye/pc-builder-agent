package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"strconv"
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

const requirementStateInstruction = `你负责本轮需求语义解析与回答，一次输出JSON：operations、observations、turn_signals、proposals、answer。程序会确定性更新需求并决定后续动作；你不判断需求是否完整，不宣布确认、核定或生成，也不交接生成任务。

逐项理解本轮信息，区分“明确说了”与“绝不可让步”：evidence=stated只证明来源明确，不决定strength。对静音、品牌、尺寸、外观等选配偏好，普通愿望即使表达肯定也用prefer；只有用户表达不可妥协、排除其他选项或明确硬性限制时才用must。例如“希望白色、运行安静”是两个明确的prefer；“外壳颜色可以让步，但声音不能妥协”只把静音改为must。事实字段的must不能连带提升同句的其他偏好。
先判断每条原文谈论的对象和关系，再选择字段。商品报价、购物意向、备选型号、系统推荐配置与用户已经拥有的配件是不同事实；提到商品或金额不能证明已经拥有。用户以“指定”“要用”“必须配”等措辞点名要买的型号是购买要求，不是已有件，应保存为独立free.*约束条目（kind=constraint），绝不写进existing_parts或owned_parts；只有“有/手头有/在用/我现在的”等明确确认拥有的表达才记录已有件。确认拥有但型号不完整时保留品类和简称；只有报价、容量或“某张显卡”不是准确型号，不能为了填数组将整段描述作为model。
用户提供的选配相关参考信息也必须保留，不因未购买、未核验、型号不明而丢弃。报价参考用独立free.*、kind=context记录商品/品类、原金额和用户说明的购买状态，不能写成budget_cny、已有件、必须采购或已核验价格；未决定采用的具体型号用alternative。暂时无法整理的参考信息用observations保留完整相关原文。每个参考独立记录并沿用稳定编号，其他预算/用途变化不能删除它；用户撤销时按同一编号remove，不从旧消息恢复。
同一句可包含多个独立更新，必须逐项落入operations，不能只在answer中提及。预算金额、购买计划和金额覆盖范围是不同信息：已有配件、缺其他配件或打算购买都不是费用口径，不能据此填写budget_basis。“手上有显卡，准备买其余配件，预算九千”仅记录金额和已有件，口径未知；“这九千不计算手上的显卡价值”才记录new_purchase；“九千要包括手上显卡的价值”记录full_build。明确称金额为新增采购费用时，同一原文同时支持budget_cny和budget_basis=new_purchase两项操作；明确包含已有件价值时记录full_build。是否有已有件、是否给出准确型号均不改变已表达的金额口径，不得因此省略口径。
装机对象独立记录：仅在明确的赠送/代装表达（“给我弟弟装”“给朋友配”“为父母装一台”）时 set recipient为用户说的对象；“家里老人上网用”是用途描述不是代装表达，不设 recipient；没有说明对象时不补默认“自己”。
执行上下文中的“最近一条助手回复”仅帮助理解“好的”“可以”等指代，不是用户事实，不得恢复旧值。确定性参考中的 effective_defaults（system_default）是程序已应用的默认值，不是用户事实：禁止照抄进 operations，除非本轮用户原话明确出现该值。型号、兼容性、报价和预算内如何选件不在你的职责内，不猜测、不比较具体配件。
未预设要求逐项保存到 free.<稳定英文编号> 字段，value 为中文要求全文，后续修改沿用同一编号，撤销用 remove；不能将多个独立条件挤进 notes。同一语义的后续更正必须复用已有free.*编号；多个字段或notes已重复记录同一信息时，同轮更新权威条目并remove被替代的重复条目。
kind 与 strength 独立且 kind 只允许 fact、context、constraint 三个值（没有"preference"这类值，偏好程度用 strength=prefer 表达）：fact 表示用途、工作负载、已有件、装机对象等事实；constraint 表示要求配置满足的条件（含外观、预算、口径等结构化字段，如 set appearance="白色" kind=constraint、set budget_basis kind=constraint）；context 只允许用于 notes、recipient 和 free.* 条目，结构化字段（appearance、budget_basis、noise_pref 等）绝不能用 context，否则会被拒绝。自由文本必须条件逐项使用 free.<稳定编号> kind=constraint strength=must。混合说明拆成独立条目。
每项 evidence 必填：stated 表示本轮明确表达，允许忠实语义归类和数值换算；inferred 的操作会被程序直接丢弃，不要输出——推断或默认不能作为用户要求，确需保留的参考信息改用 observations 记录原话；uncertain 表示字段有歧义（仅可用于conflict）；accepted_proposal 表示用户明确接受上一轮助手建议的具体值。无法安全结构化时输出 observations:[{"field":"size_pref","quote":"方便我搬来搬去","reason":"尚未指定板型，保留便携诉求"}]，field可省略。不要把小巧猜成ITX、已有AMD型号猜成品牌偏好、素材分辨率猜成显示目标。未知不等于冲突：尚未提供值时保留unknown，不输出set null或conflict；只有确有相互矛盾的表达或当前有效值正在被不明确地纠正时用conflict。

操作语义：
- set：用户明确新增或修改当前要求，或明确接受建议值。只提交被修改字段，不重发未变字段；同轮先说错又说对的更正（“预算6000吧，哦不对，7000”）只输出最终值的一个 set；预算金额变化不撤销 budget_flex（remove 只用于用户明确撤销该要求）。
- remove：用户明确撤回、不要、取消某项要求，value 省略。“不要求安静”是 remove noise_pref；“不要噪音”仍是 set silent。
- alternative：用户明确讨论假设性备选（“如果改成一万呢”“方案B”这类"如果…呢"假设）时记录备选值，不 set 当前字段；单纯询问行情（“7500够吗？”“这个价位行不行”）是问题不是决定，不输出任何 operation 或 observation，数字只在 answer 中讨论；用户后来明确采用才 set。
- conflict：同轮相互矛盾、无法判断最终选择时用此操作，evidence=uncertain，value 可省略。多人给出不一致要求（“我爸说预算8000，我妈说不能超过6000”）或同句前后矛盾都属此类；禁止替用户选更严的一方或自创折中。明确的后来更正直接 set，不制造冲突。
- restore：用户明确恢复被临时例外的原要求（“还是按原来的7500来”“恢复原来的要求”），value省略；不要把恢复表达成新的 set。
- scope=temporary：“这次先用”“这次可以例外”等明确临时放宽/覆盖；保留原值，直到用户明确恢复。
- 用户明确接受“未解决建议”中同字段同值的建议时（如对“按7500元的预算继续可以吗？”只回答“可以”“就按这个来”），输出 set 且 evidence=accepted_proposal，quote 逐字摘录用户接受的原话；程序会做服务器端核验。用户复述了具体数值（“可以，就按7500来”）时 evidence=stated——用户亲口给出的值就是明确表达，不是对建议的裸接受。判定顺序：先看本轮原话是否自己给出了完整值，“那就7500吧”“就按1080p来”都是用户亲口的值，一律 evidence=stated，哪怕上一轮助手提过同样的值；只有采纳上一轮助手的建议且该建议确实存在时才用 accepted_proposal。上一轮没有相应建议却标 accepted_proposal，程序会按合同拒收（值不保存、只留观察），不会自动改判 stated。
- 用户明确要求本次一并购买或配置显示器、键盘、鼠标时，不写任何对应字段，把原话放入顶层 observations 数组（元素只有 field/quote/reason 三个键，field 可省略）：{"quote":"要带显示器","reason":"unsupported_capability:monitor"}（keyboard、mouse 同理）；operations 里不得出现 op=observation 之类的伪操作。“我有显示器”“1080p显示器够用”等已有背景陈述不是购买请求，按普通信息处理，不确定时保留普通 observation。reason 必须逐字是 unsupported_capability:monitor、unsupported_capability:keyboard 或 unsupported_capability:mouse 之一（每个能力一条观察，禁止合并成 monitor/keyboard/mouse 或逗号列表）。用户明确放弃某项时输出 {"op":"remove","field":"unsupported.monitor","quote":"显示器先不要了"} 这类撤销操作。
- “预算7500”是 set budget_cny=7500；“预算不要超过7500”是 set budget_cny=7500 且 set budget_flex=0（严格不超）。普通“预算7500”没有任何弹性表达，不写 budget_flex（保持未知，由系统默认展开）；用户百分比表达（“最多上浮5%”“可以超10%”）必须先换算为比例（0.05、0.1）再提交，不提交百分数值本身。

turn_signals 是本轮请求事实的多标签，不是授权，同句可并存：
- asks_question：用户在问问题、需要回答。
- requests_review：用户想查看或核对需求。
- requests_build：用户表达开始生成的意愿（“开始配吧”“直接生成”“开始吧”）。“帮我配台电脑”“帮我配一台”只在整句没有预算/用途/已有件/采购范围等新信息时才算：“帮我配台电脑，要带显示器”是纯执行+能力观察，requests_build=true；“帮我配台全新的主机，配件都新买”在表达采购范围，requests_build=false。
- ambiguous：无法安全理解当前动作；字段证据充分的 operations 仍可输出。
单独一句“可以”“就这样”且“未解决建议”中没有同字段同值可接受项时，不编造字段操作，ambiguous=true。

proposals 只在你准备向用户建议一个具体字段值时输出（通常在回答预算/行情咨询之后）：[{"field":"budget_cny","value":7500,"text":"按7500元的预算继续可以吗？"}]。text 必须是包含字段含义、具体值、可直接回答“可以”的完整建议问句。用户明确接受前它不是需求；不给系统默认值、不给无依据的猜测值。没有建议时省略或输出空数组。

answer 只回答用户当前问题，可为空：不重复已保存信息，不宣称需求已完整，不得声称已开始/正在生成配置，不得承诺配置显示器、键盘、鼠标等主机外品类。用户询问预算是否够而预算未定时，可给有依据的参考区间并说明只是参考；建议值放入 proposals，不写进 operations。

字段与值（必须采用以下点路径）：
budget_cny 正整数预算金额；budget_flex 非负比例（5%是0.05、10%是0.1，不是5或10），仅明确预算弹性才给，严格不超设0；budget_basis new_purchase|full_build，仅当用户明确说明金额覆盖口径（“不含已有件价值”“整机总价”“这九千要包括手上显卡的价值”）才设置；“帮我配台全新的主机，配件都新买”只 set existing_parts=[]，绝不设 budget_basis——全新/都新买/打算购买/缺什么配件都不是口径表达。
use_case.type：general是普通办公、文档表格、上网影音；gaming是玩游戏；productivity专指专业剪辑、渲染、建模等计算工作负载。“帮我配台电脑”这类泛购买表达不含用途信息，不得猜测任何值。use_case.titles 字符串数组，提到具体游戏名时必须逐个收录（“玩DOTA2和LOL”→["DOTA2","LOL"]，同时 set use_case.type=gaming）；use_case.resolution 1080p|2K|4K，只取明确的显示器/游戏输出目标，“剪4K视频”的4K是素材参数（存free.*）；use_case.performance_goal balanced|fps_first|quality_first：用户表达帧率诉求（“希望帧率高一点”“帧率越高越好”）即 fps_first，不生成具体FPS；画质优先才 quality_first；use_case.fps_target 正整数，用户给出明确帧率数字（“帧数至少144”）时设置，此时不再设 performance_goal。
existing_parts 本次确实已有且可沿用的主机品类数组(cpu/gpu/motherboard/memory/ssd/psu/case/cooler)，显示器不属于主机品类，“全部新买”是 set []，本轮原话没有全部新买或无已有件的明确表达时禁止输出空数组（程序会拒收无证据的空已有件）。“旧配件能不能复用还得回家确认”“先别当作全部新买”等否定或待确认表达不是全新购买证据：existing_parts 保持 unknown，用一条 observation 保留完整原话，绝不输出空数组或 remove。“还有块旧的固态也能用”“我有个旧电源”是明确的已有表达：立即把品类追加进 existing_parts；准确型号未知只影响 owned_parts 的追问，不能把“型号未知”当作“已有关系未知”。明确“全部新买”或不再复用旧件时输出 set existing_parts=[]（程序会自动清空关联型号），不要对 owned_parts 单独 remove。configuration_scope 是系统注入字段，任何情况下都不得出现在你的输出中；owned_parts 是数组字段，确认已有关系且用户给出准确型号后一次提交合并后的完整数组（如[{"category":"gpu","model":"4070 Super"}]），首轮同一句就说出品类和准确型号（“我有一张4070 Super显卡”）时必须同轮提交、不等追问，禁止 owned_parts.gpu.model 或 owned_parts.gpu 点路径（误用时程序会按形状纠偏，但不要依赖），不猜SKU；existing_parts（品类）与 owned_parts（准确型号）并存且各自独立记录，把型号提交进 owned_parts 不代表撤销已有件，remove existing_parts 仅用于用户明确表达不再保留旧件（如“旧件都不要了”）；quantity 仅在用户明确表达数量（多根内存、两块硬盘）时携带，单件省略；型号更正替换原件；确认已有但未提供准确型号时只 set existing_parts 品类数组（型号留空待追问），绝不提交 owned_parts——数组项没有 model 就是非法输出。用户某件不再复用时从existing_parts数组移除该品类；撤销全部已有件时remove existing_parts。
brand_pref.cpu any|amd|intel；brand_pref.gpu any|amd|nvidia；未提品牌不能填any。noise_pref silent|normal|any(“都行/无所谓”且无其他取向词才是any；“声音无所谓，正常就行”有明确取向词，取 normal)；size_pref 只能取 atx|matx|itx|any（没有"small"等自造值），“机箱尽量小/要小机箱”→itx；appearance 是颜色、灯效等外观，用提炼后的简洁表达（如“白色”），完整原话留给 quote；“机箱最好是白色的”是 set appearance="白色"，与尺寸无关；recipient 装机对象字符串；notes 仅保留无法结构化进其他字段的补充背景，kind=context；用途已能表达时（“家里老人上网用”是 set use_case.type=general）不要再把原话复述进 notes 或 observations。priority 硬件优先品类数组，“优先把显卡配好/预算紧先保CPU”这类优先级表达用 priority（如["gpu"]），不要自创 free.* 条目重复记录。
observations只保留无法可靠结构化的用户原话，不要把你自己的推理、取舍说明或可结构化信息的复述存成观察（金标口径：能进 operations 的不进 observations，没有内容就输出空数组）；同一句原文只用一条观察保留（可取整句），不把一句话拆成多条重复观察；不得用观察重新激活removed字段、采纳备选或冒充明确偏好。

输出契约（每个操作必须有op和field；evidence是字符串，quote与它同级，不是嵌套对象；turn_signals四项必须全部给出布尔值）：
用户说“预算8000，主要玩游戏”时：{"operations":[{"op":"set","field":"budget_cny","value":8000,"kind":"constraint","strength":"must","scope":"session","evidence":"stated","quote":"预算8000"},{"op":"set","field":"use_case.type","value":"gaming","kind":"fact","strength":"must","scope":"session","evidence":"stated","quote":"主要玩游戏"}],"observations":[],"turn_signals":{"asks_question":false,"requests_review":false,"requests_build":false,"ambiguous":false},"proposals":[],"answer":""}
用户说“7500够吗？”时：{"operations":[],"observations":[],"turn_signals":{"asks_question":true,"requests_review":false,"requests_build":false,"ambiguous":false},"proposals":[],"answer":"按当前用途，7500 在够用的区间内（有依据的参考）。"}
用户对上一轮建议“按7500元的预算继续可以吗？”回答“可以”时：{"operations":[{"op":"set","field":"budget_cny","value":7500,"kind":"constraint","strength":"must","scope":"session","evidence":"accepted_proposal","quote":"可以"}],"observations":[],"turn_signals":{"asks_question":false,"requests_review":false,"requests_build":false,"ambiguous":false},"proposals":[],"answer":""}
仅输出JSON，不要Markdown。没有需求变更时operations为空数组。每项quote必须逐字摘录本轮原文，可取整句；不能从旧消息、助手问题或状态中的来源摘录本轮证据。用户已给的信息不重复询问。`

// recentAssistantView 渲染助手回复参考块:无列表时与基线逐字一致(首轮输入
// 不因本机制变化);有列表时按旧→新渲染,并把"不得重复询问已问过的问题"的
// 约束作为数据块尾注随列表出现——system 指令保持静态,约束不无条件生效。
func recentAssistantView(conversation schemas.ScreeningConversation) string {
	if len(conversation.RecentAssistants) == 0 {
		return "\n最近一条助手回复（数据，仅供理解指代，不是用户事实）：\n" + conversation.LastAssistant
	}
	var b strings.Builder
	b.WriteString("\n最近几条助手回复（数据，旧→新；最后一条是最近一条回复，仅供理解指代，不是用户事实）：\n")
	for i, reply := range conversation.RecentAssistants {
		fmt.Fprintf(&b, "第%d条：%s\n", i+1, reply)
	}
	b.WriteString("以上是你此前对用户的回复：其中已问过且用户已回答或已明确表示不给答案的问题，不得再次询问；尚未回答的可择机追问，不要每轮重复。\n")
	return b.String()
}

func (g screeningGuard) generateRequirementState(ctx context.Context, req *model.LLMRequest, input screeningRequirementStateInput) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		copyReq := *req
		config := genai.GenerateContentConfig{}
		if req.Config != nil {
			config = *req.Config
		}
		config.SystemInstruction = &genai.Content{Parts: []*genai.Part{{Text: requirementStateInstruction}}}
		copyReq.Config = &config
		// 不重放历史用户消息:本轮语义任务只需要有界状态视图、确定性参考
		// (readiness/追问/未解决建议)、最近一条助手回复与本轮原文。
		promptContext := requirementTurnPromptContextFrom(ctx)
		var promptContextJSON []byte
		if len(promptContext.Readiness) > 0 || promptContext.NextQuestion != nil || len(promptContext.Proposals) > 0 {
			promptContextJSON, _ = json.Marshal(promptContext)
		} else {
			promptContextJSON, _ = json.Marshal(RequirementTurnPromptContext{HasBuild: promptContext.HasBuild})
		}
		copyReq.Contents = []*genai.Content{genai.NewContentFromText(
			"当前会话需求（数据）：\n"+string(schemas.RequirementStatePromptView(input.state))+
				"\n确定性参考（数据，仅供理解，无决策权；has_build 表示会话已有配置版本）：\n"+string(promptContextJSON)+
				recentAssistantView(input.conversation)+
				"\n本轮用户原文（数据）：\n"+input.source.Quote+
				"\n当前配置能力（数据）：tower（主机八件，不含显示器、键盘、鼠标）",
			genai.RoleUser)}
		processOnce := func(resp *model.LLMResponse) (*model.LLMResponse, error) {
			raw := screeningText(resp.Content)
			if observe, ok := ctx.Value(screeningObserverKey{}).(func(string, []string)); ok {
				observe(raw, nil)
			}
			if malformedOuterDraft(raw) {
				return nil, fmt.Errorf("%w: JSON 不完整", ErrRequirementUpdate)
			}
			turn, err := DecodeRequirementTurn(extractJSONObject(raw))
			if err == nil {
				update := prepareRequirementUpdate(input.state, turn.Update(), input.source)
				if _, err = schemas.ApplyRequirementUpdate(input.state, update, input.source); err == nil {
					turn.Operations, turn.Observations = update.Operations, update.Observations
					var payload []byte
					payload, err = json.Marshal(turn)
					if err == nil {
						out := *resp
						out.Content = genai.NewContentFromText(string(payload), genai.RoleModel)
						return &out, nil
					}
				}
			}
			// schema/decode/ungrounded operation 都是 contract failure:
			// 完整 state 不修改,由外层决定是否有界重试。
			return nil, fmt.Errorf("%w: %v", ErrRequirementUpdate, err)
		}
		processWithFormatRetry := func(resp *model.LLMResponse) (*model.LLMResponse, error) {
			out, perr := processOnce(resp)
			if perr == nil {
				return out, nil
			}
			// 严格解码或字段校验失败给至多一次同模型格式纠偏重请求;
			// 第二次仍失败才失败。不切换模型,不放宽逐 op 核验。
			raw := screeningText(resp.Content)
			if strings.TrimSpace(raw) == "" {
				return nil, perr
			}
			recordScreeningRetry(ctx)
			retryReq := copyReq
			retryReq.Contents = append(append([]*genai.Content(nil), copyReq.Contents...),
				genai.NewContentFromText(raw, genai.RoleModel),
				genai.NewContentFromText(formatFallbackInstruction, genai.RoleUser))
			for response2, err2 := range g.LLM.GenerateContent(ctx, &retryReq, false) {
				if err2 != nil || response2 == nil || response2.ErrorCode != "" || response2.ErrorMessage != "" {
					if !yield(response2, err2) {
						return nil, perr
					}
					continue
				}
				out2, perr2 := processOnce(response2)
				if perr2 != nil {
					return nil, perr2
				}
				return out2, nil
			}
			return nil, perr
		}
		for response, err := range g.LLM.GenerateContent(ctx, &copyReq, false) {
			if err != nil || response == nil || response.ErrorCode != "" || response.ErrorMessage != "" {
				if !yield(response, err) {
					return
				}
				continue
			}
			out, perr := processWithFormatRetry(response)
			if perr != nil {
				yield(nil, perr)
				return
			}
			if !yield(out, nil) {
				return
			}
		}
	}
}

const formatFallbackInstruction = `纠偏：上一次输出不符合 JSON 协议（operations/observations/turn_signals/proposals/answer 契约、字段值合法性，或 quote 未逐字摘录本轮原文）。请重新输出完整 JSON：仅输出 JSON，不要 Markdown；每个操作必须有 op 和 field，quote 逐字摘录本轮原文，evidence 与 quote 同级；turn_signals 四项全部给出布尔值。字段语义与操作语义均按原要求执行，不新增未表达的内容。`

type screeningRetryCounterKey struct{}

// WithScreeningRetryCounter 让调用方收集 guard 发起的同模型重请求次数(F3 retry_count)。
func WithScreeningRetryCounter(ctx context.Context, counter *int) context.Context {
	return context.WithValue(ctx, screeningRetryCounterKey{}, counter)
}

func recordScreeningRetry(ctx context.Context) {
	if counter, ok := ctx.Value(screeningRetryCounterKey{}).(*int); ok && counter != nil {
		*counter++
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
			// unsupported_capability 观察是结构化能力记录,合同要求每个能力
			// 一条:同一句原文会合法产生 monitor/keyboard/mouse 多条,不参与
			// 去重。自由文本观察则同一段原文不重复记录:quote 完全相同
			// (任意字段)或同字段互为包含片段时合并,保留覆盖更完整的摘录。
			if !strings.HasPrefix(reason, "unsupported_capability:") {
				for j, existing := range out.Observations {
					sameQuote := existing.Quote == quote
					overlap := existing.Field == field && (strings.Contains(existing.Quote, quote) || strings.Contains(quote, existing.Quote))
					if sameQuote || overlap {
						if strings.Contains(quote, existing.Quote) && len(quote) > len(existing.Quote) {
							out.Observations[j] = schemas.RequirementObservationInput{Field: field, Quote: quote, Reason: reason}
						}
						return
					}
				}
			}
			out.Observations = append(out.Observations, schemas.RequirementObservationInput{Field: field, Quote: quote, Reason: reason})
		}
	}
	// statedByOtherOperation:推断操作的原话片段是否已被本轮明确表达的操作
	// 覆盖(quote 互为包含)。已结构化的信息不再降级为观察,独立片段保留。
	statedByOtherOperation := func(index int, quote string) bool {
		if strings.TrimSpace(quote) == "" {
			return false
		}
		for i, other := range update.Operations {
			if i == index || other.Op != "set" || other.Evidence == "inferred" {
				continue
			}
			if strings.Contains(quote, other.Quote) || strings.Contains(other.Quote, quote) {
				return true
			}
		}
		return false
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
	for i, op := range update.Operations {
		// 模型重发与当前 active 值完全相同的字段是 no-op:跳过,不重复计入
		// 本轮变更(值与强度/语义都未变时状态本就不变)。
		if op.Op == "set" {
			before := working.Fields[op.Field]
			if before.Status == "active" && sameJSONValue(before.Value, op.Value) &&
				(op.Strength == "" || op.Strength == before.Strength) &&
				(op.Kind == "" || op.Kind == before.Kind) {
				continue
			}
		}
		// 型号更正的摘录出错但型号确实出现在本轮原文时,把证据绑定到本轮
		// 完整原文(服务器验证型号 grounding,不采用模型幻觉的旧句子)。
		if op.Field == "owned_parts" && op.Op == "set" && len(op.Value) > 0 &&
			(op.Quote == "" || !strings.Contains(source.Quote, op.Quote)) && ownedModelsGrounded(op.Value, source.Quote) {
			op.Quote = source.Quote
		}
		// 形状纠偏:确定性参考的追问字段是 owned_parts.<category>.model,
		// 模型会模仿该形状(含单段 owned_parts.<category> 携带对象/字符串);
		// 规范化为 owned_parts 数组合并操作,值仍来自本轮原话。
		if normalized, ok := normalizeOwnedModelPath(working, op, source); ok {
			update.Operations[i] = normalized
			op = normalized
		}
		// 证据红线(按授权合同拦截空已有件,不触发纠偏重试):
		// 空 existing_parts 必须有本轮"全新购买/无已有件"的明确表达——
		// 泛购买语句("帮我配台电脑")不是采购范围证据。原文处于已有件
		// 讨论语境(提到旧件/新买/复用等)时保留完整原文观察供后续追问
		// (conversations15 cv-ownership 金标依赖);原文与已有件完全无关
		// (如"预算9000，开始配吧")时,观察对追问没有信息量,静默丢弃
		// (ex-composite 金标:能进 operations 的信息不进 observations)。
		if op.Op == "set" && op.Field == "existing_parts" && isEmptyJSONArray(op.Value) && !existingPartsClearedEvidence(source.Quote) {
			if existingPartsMentioned(source.Quote) {
				observe(op.Field, source.Quote, "本轮未表达全部新买或无已有件，不写入空已有件")
			}
			continue
		}
		// 百分比换算仅在证据匹配时进行:模型把"最多上浮5%"输出为数值 5 时,
		// 只有本轮原话出现对应百分比字面量才换算为比例,不任意对数值除100。
		if normalized, ok := normalizePercentBudgetFlex(op, source.Quote); ok {
			op.Value = normalized
			update.Operations[i] = op
		}
		// 零弹性的证据红线:普通预算金额("预算7500")不是"严格不超"表达,
		// 该写入是模型幻觉,直接丢弃(金额已由 budget_cny 记录,降级观察
		// 没有新信息,只会污染本轮观察)。整句或模型摘录的严格表达任一
		// 命中即放行。
		if op.Op == "set" && op.Field == "budget_flex" && rawNumberIs(op.Value, 0) && !strictBudgetFlexEvidence(source.Quote, op.Quote) {
			continue
		}
		// 数值型系统默认(如 budget_flex 0.1)已由程序应用,照抄默认不是用户
		// 事实;仅当本轮原话出现该值字面量时才接受写入。拦截后静默丢弃:
		// 该值已由确定性默认生效,降级观察没有新信息,只污染本轮观察
		// (金标口径:能进 operations 的信息不进 observations)。
		if op.Op == "set" && defaultCopiedWithoutEvidence(working, op, source.Quote) {
			continue
		}
		// 无证据的 remove existing_parts 同样是关键字段错写:撤销全部已有件
		// 只能来自用户明确的撤回表达(意图动词+旧件/已有对象)。
		// ponytail: 白名单不覆盖"显卡不再沿用"等单品类口语,漏报路径是保留
		// observation 等追问补证,不会误删用户事实。
		if op.Op == "remove" && op.Field == "existing_parts" {
			// 明确"全部新买"表达下,remove 的领域语义是赋空值:撤销已有关系
			// 的标准表达是 active [](可核定的明确值),不是墓碑;程序会级联
			// 清空关联型号。纯撤销表达("旧件都不要了")仍走 remove。
			if existingPartsClearedEvidence(source.Quote) {
				quote := op.Quote
				if quote == "" || !strings.Contains(source.Quote, quote) {
					quote = source.Quote
				}
				op = schemas.RequirementOperation{Op: "set", Field: "existing_parts", Value: []byte("[]"),
					Quote: quote, Kind: "fact", Strength: "must", Evidence: "stated"}
				update.Operations[i] = op
			} else if !existingPartsRemovalEvidence(source.Quote) {
				observe(op.Field, op.Quote, "本轮未表达撤销已有件，不执行移除")
				continue
			}
		}
		// owned_parts 是 existing_parts 的型号明细:撤销全部已有件由
		// set existing_parts=[] 的确定性级联完成,独立 remove 只会把级联后
		// 的明确空值退化成墓碑。本轮有"全部新买"证据时静默跳过(与操作
		// 顺序无关);无该证据的单独 remove 降级观察,旧型号保留待更正。
		if op.Op == "remove" && op.Field == "owned_parts" {
			if existingPartsClearedEvidence(source.Quote) {
				continue
			}
			observe(op.Field, op.Quote, "本轮未表达撤销已有件型号，不执行移除")
			continue
		}
		// notes 只承载背景记录:模型自作主张"顺手清空备注"是实录中的噪声
		// 操作,静默跳过(需求真值无损)。用户真要删备注会给出明确表达,
		// 那时 quote 含撤销语境词,照常放行。
		// ponytail: 词表外的等价表达会被跳过一轮,升级路径是显式的备注
		// 编辑入口,不在此扩 NLU。
		if op.Op == "remove" && op.Field == "notes" && !notesRemovalEvidence(source.Quote) {
			continue
		}
		// 模型省略 remove 的摘录时，来源仍绑定本轮完整消息，不猜撤销语义。
		if op.Op == "remove" && op.Quote == "" && working.Fields[op.Field].Status == "active" {
			op.Quote = source.Quote
		}
		if op.Evidence == "inferred" {
			// 推断不能作为用户要求:原话已被同轮明确操作结构化时静默跳过
			// (降级观察只是重复);独立片段降级为观察保留原文供追问。
			if !statedByOtherOperation(i, op.Quote) {
				observe(op.Field, op.Quote, "模型推断尚未作为用户要求采用")
			}
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
		if sameSourceWorkloadResolution(op, update) {
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
	statedByActiveOperation := func(quote string) bool {
		if strings.TrimSpace(quote) == "" {
			return false
		}
		for _, op := range out.Operations {
			if op.Op != "set" || strings.TrimSpace(op.Quote) == "" {
				continue
			}
			if strings.Contains(quote, op.Quote) || strings.Contains(op.Quote, quote) {
				return true
			}
		}
		return false
	}
	for _, observation := range update.Observations {
		// 金标口径"能进 operations 的不进 observations"的代码侧兜底:观察
		// 的原文已被本轮实际生效的明确操作结构化(quote 互为包含)时丢弃
		// ——该信息已是需求真值,复述成观察(常见形态:reason 里声明"撤销
		// 之前的观察"之类的元操作)只是噪声。被拦截/推断的操作没有成为
		// 真值,不参与该判定,保留观察等追问。
		if !strings.HasPrefix(observation.Reason, "unsupported_capability:") && statedByActiveOperation(observation.Quote) {
			continue
		}
		observe(observation.Field, observation.Quote, observation.Reason)
	}
	return out
}

// normalizeOwnedModelPath 把 owned_parts.<category>.model="M" 与单段
// owned_parts.<category>（值为 {"category","model"} 对象或型号字符串）统一
// 规范化为 owned_parts 数组的合并操作(保留既有其它品类,替换同类目型号)。
// 值只能来自本轮 quote;不生成、不猜测任何事实;型号 grounding 由 reducer 校验。
func normalizeOwnedModelPath(state schemas.RequirementState, op schemas.RequirementOperation, source schemas.RequirementSource) (schemas.RequirementOperation, bool) {
	rest, ok := strings.CutPrefix(op.Field, "owned_parts.")
	if !ok || op.Op != "set" {
		return op, false
	}
	category, model, ok := ownedPathTarget(rest, op.Value)
	if !ok {
		return op, false
	}
	quote := op.Quote
	if !strings.Contains(source.Quote, quote) {
		return op, false
	}
	parts := []schemas.OwnedPart{}
	if field := state.Fields["owned_parts"]; field.Status == "active" {
		_ = json.Unmarshal(field.Value, &parts)
	}
	merged := []schemas.OwnedPart{}
	replaced := false
	for _, part := range parts {
		if string(part.Category) == category {
			if !replaced {
				merged = append(merged, schemas.OwnedPart{Category: schemas.Category(category), Model: model, Quantity: part.Quantity})
				replaced = true
			}
			continue
		}
		merged = append(merged, part)
	}
	if !replaced {
		merged = append(merged, schemas.OwnedPart{Category: schemas.Category(category), Model: model})
	}
	value, err := json.Marshal(merged)
	if err != nil {
		return op, false
	}
	kind := op.Kind
	if kind == "" {
		kind = "fact"
	}
	return schemas.RequirementOperation{Op: "set", Field: "owned_parts", Value: value,
		Strength: op.Strength, Scope: op.Scope, Quote: quote, Kind: kind, Evidence: op.Evidence}, true
}

// ownedPathTarget 归一两类点路径形状为 (category, model):
//   - "<category>.model"：值为型号字符串（追问字段形状）;
//   - "<category>"：值为型号字符串或 {"category","model"} 对象
//     （对象 category 与路径不一致时拒绝,不静默改写）。
func ownedPathTarget(rest string, value json.RawMessage) (string, string, bool) {
	if category, suffix, hasDot := strings.Cut(rest, "."); hasDot && suffix == "model" && !strings.Contains(category, ".") {
		var model string
		if containsCategory(category) && len(value) > 0 && value[0] == '"' && json.Unmarshal(value, &model) == nil && strings.TrimSpace(model) != "" {
			return category, model, true
		}
		return "", "", false
	}
	if !containsCategory(rest) {
		return "", "", false
	}
	if len(value) > 0 && value[0] == '"' {
		var model string
		if json.Unmarshal(value, &model) != nil || strings.TrimSpace(model) == "" {
			return "", "", false
		}
		return rest, model, true
	}
	var part schemas.OwnedPart
	if json.Unmarshal(value, &part) != nil || strings.TrimSpace(part.Model) == "" {
		return "", "", false
	}
	if part.Category != "" && string(part.Category) != rest {
		return "", "", false
	}
	return rest, part.Model, true
}

// isEmptyJSONArray 判断值是否为空 JSON 数组（existing_parts=[] 的"全部新买"语义）。
func isEmptyJSONArray(value json.RawMessage) bool {
	var items []any
	return len(value) > 0 && json.Unmarshal(value, &items) == nil && len(items) == 0
}

// existingPartsClearedEvidence 是保守的中文证据启发式：空已有件只能来自用户
// 明确表达全新购买或没有可沿用旧件。否定/待确认语境优先排除("先别当作全部
// 新买""还得回家确认"表达"尚未决定",清空会过早满足必填并允许核定)。
// ponytail: 白名单不覆盖口语变体（如"一件不留"），漏报路径是保留
// observation 等追问补证，不会误写用户事实。
func existingPartsClearedEvidence(quote string) bool {
	for _, marker := range []string{"先别", "先不", "别当作", "不当作", "待确认", "还得", "尚未", "还没", "不确定", "说不好", "不一定"} {
		if strings.Contains(quote, marker) {
			return false
		}
	}
	for _, marker := range []string{"全新", "新买", "全部新的", "都是新的", "全是新的", "没有已有", "没有旧", "无已有", "不用旧", "不使用旧", "不要旧"} {
		if strings.Contains(quote, marker) {
			return true
		}
	}
	return false
}

// existingPartsMentioned 判断本轮原文是否处于已有件讨论语境:只有语境内
// 的空已有件拦截才值得把原文保留为观察(待确认/否定语义供追问),无关
// 语境下的拦截是纯幻觉,观察无信息量。ponytail: 词表外的等价表达会走
// 静默路径,升级条件是金标出现依赖该观察的新语境失败。
func existingPartsMentioned(quote string) bool {
	for _, marker := range []string{"旧", "新买", "全新", "复用", "沿用", "已有", "手头", "现有的", "留用", "配件都"} {
		if strings.Contains(quote, marker) {
			return true
		}
	}
	return false
}

// notesRemovalEvidence:撤销备注需要撤销意图动词与备注对象的明确共现。
func notesRemovalEvidence(quote string) bool {
	verb, object := false, false
	for _, marker := range []string{"不要", "不用", "删掉", "删除", "去掉", "撤销", "撤掉", "清除"} {
		verb = verb || strings.Contains(quote, marker)
	}
	for _, marker := range []string{"备注", "补充说明", "背景", "说明"} {
		object = object || strings.Contains(quote, marker)
	}
	return verb && object
}

// strictBudgetFlexEvidence 要求零弹性来自本轮明确的严格上限表达;普通预算
// 金额不是零弹性证据。整句与模型摘录的 quote 任一命中即视为有据——摘录是
// 模型自己的证据主张,整句兜底防止摘录过窄漏掉真实上限语气。
// ponytail: 口语变体("卡死在")不在词表,漏报路径是丢弃该次幻觉写入,
// 用户下轮可再表达,不会留下错误状态。
func strictBudgetFlexEvidence(quotes ...string) bool {
	for _, quote := range quotes {
		for _, marker := range []string{"不能超", "不要超", "不得超过", "不得超", "别超过", "别超", "不超", "不超过", "严格", "上限", "封顶", "一分都不能多", "一分都不能超"} {
			if strings.Contains(quote, marker) {
				return true
			}
		}
	}
	return false
}

// rawNumberIs 判断 JSON 值是否为指定数值。
func rawNumberIs(value json.RawMessage, want float64) bool {
	var n float64
	return len(value) > 0 && json.Unmarshal(value, &n) == nil && n == want
}

// normalizePercentBudgetFlex 把百分比表达的数值(模型把"最多上浮5%"输出为 5)
// 换算为比例:仅当值超出 schema 上限、不超过 100 且本轮原话出现与该数值精确
// 对应的百分比字面量("5%"或"百分之5")才换算,不任意对所有数值除100。
func normalizePercentBudgetFlex(op schemas.RequirementOperation, quote string) (json.RawMessage, bool) {
	if op.Op != "set" || op.Field != "budget_flex" {
		return nil, false
	}
	var n float64
	if len(op.Value) == 0 || json.Unmarshal(op.Value, &n) != nil || n <= schemas.MaxBudgetFlex || n > 100 {
		return nil, false
	}
	compact := strconv.FormatFloat(n, 'f', -1, 64)
	if !strings.Contains(quote, compact+"%") && !strings.Contains(quote, "百分之"+compact) {
		return nil, false
	}
	return json.RawMessage(strconv.FormatFloat(n/100, 'f', -1, 64)), true
}

// existingPartsRemovalEvidence 要求撤销意图动词与旧件/已有对象同时出现，防止
// 模型把型号并入 owned_parts 时顺手移除用户的 existing_parts 记录（认证修复
// 二跑实录的关键字段错写）。
func existingPartsRemovalEvidence(quote string) bool {
	verb, object := false, false
	for _, marker := range []string{"不要", "不用", "不使用", "撤掉", "撤销", "放弃", "移除", "删掉", "不再用", "不再使用", "不留"} {
		verb = verb || strings.Contains(quote, marker)
	}
	for _, marker := range []string{"旧件", "已有", "旧的", "之前的"} {
		object = object || strings.Contains(quote, marker)
	}
	return verb && object
}

// defaultCopiedWithoutEvidence 拦截把数值型系统默认值照抄成用户事实（当前唯
// 一数值默认是 budget_flex 0.1）。仅约束 JSON 数值默认；枚举默认（any 等）是
// 合法语义映射，不做字面量检查。ponytail: 口语数值表达（如"一成"）不在字面
// 量检查内，漏报降级为一条可撤销的 prefer 事实，不触发 veto。
func defaultCopiedWithoutEvidence(state schemas.RequirementState, op schemas.RequirementOperation, quote string) bool {
	if op.Evidence != "stated" {
		return false
	}
	readiness, err := schemas.EvaluateRequirementReadiness(state)
	if err != nil {
		return false
	}
	for _, def := range readiness.EffectiveDefaults {
		if def.Field != op.Field || len(def.Value) == 0 {
			continue
		}
		if c := def.Value[0]; c != '-' && (c < '0' || c > '9') {
			continue // 仅数值默认参与字面量核对
		}
		if !sameJSONValue(def.Value, op.Value) {
			continue
		}
		return !strings.Contains(quote, strings.TrimSpace(string(def.Value)))
	}
	return false
}

// sameJSONValue 语义比较两个 JSON 值(键序无关)。
func sameJSONValue(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	var left, right any
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && reflect.DeepEqual(left, right)
}

// ownedModelsGrounded 检查数组中每个型号都真实出现在本轮原文。
func ownedModelsGrounded(value json.RawMessage, sourceQuote string) bool {
	var owned []schemas.OwnedPart
	if json.Unmarshal(value, &owned) != nil {
		return false
	}
	for _, part := range owned {
		if !groundedModel(part.Model, []string{sourceQuote}) {
			return false
		}
	}
	return len(owned) > 0
}

func containsCategory(name string) bool {
	for _, category := range schemas.AllCategories {
		if string(category) == name {
			return true
		}
	}
	return false
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
		case "budget_cny", "budget_flex", "budget_basis", "use_case.type", "use_case.resolution", "use_case.performance_goal", "use_case.fps_target", "brand_pref.cpu", "brand_pref.gpu", "noise_pref", "size_pref", "existing_parts", "owned_parts", "use_case.titles", "priority":
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
		// 未提供表达依据时，不把生成侧默认弹性当成用户明确偏好。
		if op.Field == "budget_flex" && op.Evidence == "" {
			return fmt.Errorf("预算弹性缺少表达依据")
		}
	}
	return nil
}

// sameSourceWorkloadResolution 是字段来源关系检查,不判断自然语言关键词:
// 与非游戏用途同句、且缺少语义标注的分辨率写入只保留原文。v2 模型输出
// 总是携带 evidence,该防护只拦截无标注的旧输出形状。
func sameSourceWorkloadResolution(op schemas.RequirementOperation, update schemas.RequirementUpdate) bool {
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
