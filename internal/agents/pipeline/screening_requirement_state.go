package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"math"
	"regexp"
	"strconv"
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
“剪4K视频/剪片子”属于productivity；素材分辨率不是显示器或游戏分辨率。此时将“剪4K视频”等工作负载原文保留在notes，不填use_case.resolution，除非用户另行明确显示器/游戏分辨率。

仅输出一个 JSON 对象：{"operations":[{"op":"set","field":"budget_cny","value":8000,"strength":"must","scope":"session","quote":"预算8000"}]}。不要 Markdown、解释、问题、完整需求单。没有需求变更时输出 {"operations":[]}。每项 quote 必须逐字摘录本轮原文，可取整句；不能从旧消息、助手问题或状态中的来源摘录本轮证据。

操作语义：
- set：用户明确新增或修改当前要求。只提交被修改字段，不重发未变字段。撤销过的值不能因为历史存在而恢复。
- remove：用户明确撤回、不要、取消、还没确定某项要求，value 省略。"不要求安静"是 remove noise_pref；"不要噪音"仍是 set silent。不喜欢某品牌等负向约束不能误写成选择该品牌，改用 notes 保留原话。
- alternative：仅比较、询问“如果换成”“方案B”“考虑一下”，未表示采用时只记录备选；不能 set 当前字段。用户后来明确采用备选才 set。
- conflict：同轮相互矛盾、无法判断最终选择的字段用此操作，value 保存一个合法候选；程序仅追问此冲突。明确的后来更正直接 set，不制造冲突。
- scope=temporary："这次先用""这次可以例外"等明确临时放宽/覆盖；保留原值，直到用户明确恢复。scope=session 为当前装机会话常规要求。不是跨会话个人偏好。用户"恢复原要求"时 op=restore，value省略。
- strength=must 表示必须、只要、不能妥协、硬上限；prefer 表示尽量、优先、喜欢、可让步。静音/品牌/尺寸/外观未明确硬性时用prefer。预算、用途、分辨率、已有件事实用must；不可将尽量安静变必须。预算数值与是否允许超预算分别记录。

字段与值（必须采用以下点路径）：
budget_cny 正整数整机或新增采购预算；budget_flex 0–0.3，仅明确预算弹性才给，严格不超可设0，未说不能填默认0.1；budget_basis new_purchase|full_build，仅明确费用口径且不得由“其他都要新买”推断。
use_case.type gaming|productivity|general；普通办公为general；use_case.titles 字符串数组；use_case.resolution 1080p|2K|4K，只取明确分辨率；use_case.fps_target 正整数。
existing_parts 已有主机品类数组(cpu/gpu/motherboard/memory/ssd/psu/case/cooler)，显示器不属于主机品类；owned_parts 数组[{category,model,quantity}]，准确型号原话记录，不猜SKU。修改某已有件时提交合并其他已有件后的数组，型号更正替换原件；未提供型号时仍记录 existing_parts 给程序追问。
用户某件不再复用时必须同步从existing_parts和owned_parts移除该件，保留其他已有件；用户撤销全部已有件时remove existing_parts即可。仅说型号不确定时remove owned_parts，已有配件品类仍有效。
brand_pref.cpu any|amd|intel；brand_pref.gpu any|amd|nvidia；已有件型号的品牌不等于购买品牌偏好。未提品牌不能填any，any仅代表用户明确不限。
noise_pref silent|normal|any；size_pref atx|matx|itx|any；appearance 外观原话字符串；recipient 装机对象（如给朋友）字符串；notes 其他有用信息字符串。新增 notes 时保留当前仍有效补充、移除明确撤销的那部分；不要把结构字段复制进notes，防止撤销后残留。
priority 硬件优先品类数组，仅允许cpu/gpu/motherboard/memory/ssd/psu/case/cooler，不能用来表示静音或颜值。
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
			if err == nil && bindExplicitRemovalSources(&update, input.source.Quote) {
				payload, err = json.Marshal(update)
			}
			if err == nil && normalizeVideoWorkload(&update, input.state, input.source.Quote) {
				payload, err = json.Marshal(update)
			}
			if err == nil {
				_, err = schemas.ApplyRequirementUpdate(input.state, update, input.source)
			}
			if err == nil {
				err = guardRequirementUpdateEvidence(input.state, update, input.source.Quote)
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

// 真实模型偶尔为 remove 省略 quote。仅对整句明确撤销对应字段的用户操作
// 绑定服务端已有原文；不补 set/restore/备选证据，也不从历史推断撤销意图。
func bindExplicitRemovalSources(update *schemas.RequirementUpdate, userText string) bool {
	changed := false
	for index := range update.Operations {
		op := &update.Operations[index]
		if op.Op == "remove" && strings.TrimSpace(op.Quote) == "" && explicitFieldRemoval(op.Field, userText) {
			op.Quote = userText
			changed = true
		}
	}
	return changed
}

var removalTopics = map[string]string{
	"budget_cny":          `(?:预算|预算金额)`,
	"budget_flex":         `(?:预算弹性|预算浮动|超预算幅度)`,
	"budget_basis":        `(?:预算口径|采购口径)`,
	"use_case.type":       `(?:用途|主要用途)`,
	"use_case.titles":     `(?:游戏名称|游戏或软件|游戏和软件|应用名称)`,
	"use_case.resolution": `(?:分辨率|游戏分辨率)`,
	"use_case.fps_target": `(?:目标帧率|帧率目标)`,
	"existing_parts":      `(?:已有配件|已有件|全部已有配件)`,
	"owned_parts":         `(?:已有配件型号|已有件型号)`,
	"brand_pref.cpu":      `(?:cpu|处理器)品牌`,
	"brand_pref.gpu":      `(?:gpu|显卡)品牌`,
	"noise_pref":          `(?:静音|安静|低噪)`,
	"size_pref":           `(?:尺寸|机箱尺寸|板型)`,
	"appearance":          `(?:外观|外观风格)`,
	"notes":               `补充说明`,
	"recipient":           `装机对象`,
	"priority":            `(?:优先投入|配件优先级)`,
}

func explicitFieldRemoval(field, userText string) bool {
	topic := removalTopics[field]
	if topic == "" {
		return false
	}
	// 整个分句必须是直接撤销指令；“不要取消”“如果取消”“可以取消吗”
	// 等否定、假设或问题不会匹配。品牌必须指明 CPU/显卡字段，不能串台。
	lead := `(?:请|先|现在|这次|本次|帮我|请帮我)*`
	qualifier := `(?:刚才的|之前的|原来的|已记录的|我的)?`
	noun := topic + `(?:偏好|要求|限制|记录|信息|条件)?`
	forward := regexp.MustCompile(`^` + lead + `(?:取消|撤销|撤回|不再要求|不要求|不再考虑|不考虑)` + qualifier + noun + `(?:了|吧)?$`)
	reverse := regexp.MustCompile(`^` + lead + noun + `(?:先)?(?:取消|撤销|撤回|不再要求|不用考虑|不考虑|不要求)(?:了|吧)?$`)
	for _, clause := range preferenceClauses.Split(strings.ToLower(userText), -1) {
		clause = strings.Join(strings.Fields(clause), "")
		if forward.MatchString(clause) || reverse.MatchString(clause) {
			return true
		}
	}
	return false
}

var updatedBudgetPrefix = regexp.MustCompile(`预算(?:改为|改成|调整为|调整到|增加到|降到|降为|提高到|升到|变成)`)

// 已有 grounding 继续保护金额、分辨率和准确型号，不因引入增量协议退回纯提示词。
// 数组操作允许携带仍有效旧件，但新型号必须出现在本轮证据；旧消息不能复活旧值。
func guardRequirementUpdateEvidence(state schemas.RequirementState, update schemas.RequirementUpdate, userTexts ...string) error {
	var priorOwned []schemas.OwnedPart
	if field := state.Fields["owned_parts"]; field.Status == "active" {
		_ = json.Unmarshal(field.Value, &priorOwned)
	}
	for _, op := range update.Operations {
		if op.Op != "set" && op.Op != "alternative" && op.Op != "conflict" {
			continue
		}
		grounded := true
		if op.Op == "set" && explicitlyOnlyDiscussion(op, userTexts) {
			return fmt.Errorf("%s 仍在讨论备选，不能改写当前要求", op.Field)
		}
		switch op.Field {
		case "budget_cny":
			var budget int
			_ = json.Unmarshal(op.Value, &budget)
			grounded = groundedBudget(budget, []string{updatedBudgetPrefix.ReplaceAllString(op.Quote, "预算")})
		case "budget_flex":
			var flex float64
			_ = json.Unmarshal(op.Value, &flex)
			grounded = groundedBudgetFlex(flex, op.Quote)
		case "brand_pref.cpu", "brand_pref.gpu", "noise_pref", "size_pref", "use_case.type":
			var value string
			_ = json.Unmarshal(op.Value, &value)
			grounded = groundedStatePreference(op.Field, value, op.Quote)
		case "use_case.resolution":
			var resolution string
			_ = json.Unmarshal(op.Value, &resolution)
			quote := strings.NewReplacer("改成", "用", "改为", "用", "换成", "用").Replace(op.Quote)
			grounded = groundedResolution(resolution, []string{quote})
		case "budget_basis":
			var basis string
			_ = json.Unmarshal(op.Value, &basis)
			grounded = explicitBudgetBasis([]string{op.Quote}) == basis
		case "owned_parts":
			var owned []schemas.OwnedPart
			_ = json.Unmarshal(op.Value, &owned)
			for _, part := range owned {
				known := false
				for _, prior := range priorOwned {
					if prior.Category == part.Category && prior.Model == part.Model && prior.Quantity == part.Quantity {
						known = true
					}
				}
				if !known && !groundedModel(part.Model, []string{op.Quote}) {
					grounded = false
				}
			}
		}
		if !grounded {
			return fmt.Errorf("%s 的值缺少本轮明确证据", op.Field)
		}
	}
	return nil
}

var explicitFlexPercent = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*[%％]|百分之([零〇一二两三四五六七八九十百]+)`)
var explicitFlexRatio = regexp.MustCompile(`预算弹性(?:为|是|设为|设置为|[:：=\s])*([0-9]+(?:\.[0-9]+)?)`)

func groundedBudgetFlex(flex float64, quote string) bool {
	if flex > 0 && containsAny(quote, "严格不超", "严格不能超", "严格封顶", "严格上限") {
		return false
	}
	for _, match := range explicitFlexPercent.FindAllStringSubmatch(quote, -1) {
		value, _ := strconv.ParseFloat(match[1], 64)
		if match[2] != "" {
			value = chineseBudgetNumber(match[2])
		}
		if math.Abs(flex-value/100) < 0.000001 {
			return true
		}
	}
	if explicitFlexPercent.MatchString(quote) {
		return false
	}
	if match := explicitFlexRatio.FindStringSubmatch(quote); len(match) > 1 {
		value, _ := strconv.ParseFloat(match[1], 64)
		return math.Abs(flex-value) < 0.000001
	}
	// “不超过8000”支持严格上限0，不支持模型自行挑选10%或30%的放宽。
	return flex == 0 && containsAny(quote, "不能超", "不要超", "不超", "封顶", "上限", "严格", "最多")
}

var preferenceClauses = regexp.MustCompile(`[，,。！？!?；;\n]`)
var videoEditingClaim = regexp.MustCompile(`(?:剪(?:辑)?|编辑|制作)(?:1080p|2k|4k|8k|高清|超清)?(?:视频|片子|短片|素材)`)

// Model output may confuse source-media resolution with the user's display.
// Preserve the exact current-message evidence as a workload note, never a screen preference.
func normalizeVideoWorkload(update *schemas.RequirementUpdate, state schemas.RequirementState, userText string) bool {
	changed := false
	for i := range update.Operations {
		op := &update.Operations[i]
		if op.Op != "set" || op.Field != "use_case.resolution" || op.Quote == "" || !strings.Contains(userText, op.Quote) {
			continue
		}
		compact := strings.ToLower(strings.Join(strings.Fields(op.Quote), ""))
		var resolution string
		_ = json.Unmarshal(op.Value, &resolution)
		if !videoEditingClaim.MatchString(compact) || groundedResolution(resolution, []string{op.Quote}) {
			continue
		}
		note := op.Quote
		var prior string
		if field := state.Fields["notes"]; field.Status == "active" {
			_ = json.Unmarshal(field.Value, &prior)
		}
		if prior != "" {
			if strings.Contains(prior, note) {
				note = prior
			} else {
				note = prior + "；" + note
			}
		}
		// Merge into an existing notes operation to keep one operation per field.
		for j := range update.Operations {
			other := &update.Operations[j]
			if other.Op == "set" && other.Field == "notes" {
				var value string
				if json.Unmarshal(other.Value, &value) != nil {
					continue
				}
				if !strings.Contains(value, op.Quote) {
					value += "；" + op.Quote
				}
				other.Value, _ = json.Marshal(value)
				other.Quote = userText
				update.Operations = append(update.Operations[:i], update.Operations[i+1:]...)
				return true
			}
		}
		op.Field = "notes"
		op.Value, _ = json.Marshal(note)
		changed = true
	}
	return changed
}

func groundedStatePreference(field, value, quote string) bool {
	for _, clause := range preferenceClauses.Split(strings.ToLower(quote), -1) {
		clause = strings.Join(strings.Fields(clause), "")
		if containsAny(clause, "撤销", "撤回", "取消", "不再要求", "不要求", "不追求", "不用再追求", "不要再追求", "不必", "不用考虑") {
			continue
		}
		if field == "brand_pref.cpu" && containsAny(clause, "显卡", "gpu", "n卡", "a卡") && !containsAny(clause, "cpu", "处理器") {
			continue
		}
		if field == "brand_pref.gpu" && containsAny(clause, "cpu", "处理器") && !containsAny(clause, "gpu", "显卡") {
			continue
		}
		if (field == "brand_pref.cpu" || field == "brand_pref.gpu") && containsAny(clause, "已有", "已经有", "手里有") && !containsAny(clause, "品牌", "偏好", "新买", "新购") {
			continue
		}
		switch field {
		case "brand_pref.cpu", "brand_pref.gpu":
			aliases := map[string][]string{"amd": {"amd", "超微", "a卡"}, "intel": {"intel", "英特尔"}, "nvidia": {"nvidia", "英伟达", "n卡"}}
			negated := false
			for _, alias := range aliases[value] {
				for _, prefix := range []string{"不要", "不用", "不选", "排除", "不能用", "不想用", "不考虑"} {
					if strings.Contains(clause, prefix+alias) {
						negated = true
					}
				}
			}
			if negated {
				continue
			}
			switch value {
			case "any":
				if containsAny(clause, "品牌", "cpu", "处理器", "gpu", "显卡") && containsAny(clause, "不限", "无偏好", "没偏好", "都行", "无所谓", "随意") {
					return true
				}
			case "amd":
				if containsAny(clause, "amd", "超微") || (field == "brand_pref.gpu" && strings.Contains(clause, "a卡")) {
					return true
				}
			case "intel":
				if containsAny(clause, "intel", "英特尔") {
					return true
				}
			case "nvidia":
				if containsAny(clause, "nvidia", "英伟达", "n卡") {
					return true
				}
			}
		case "noise_pref":
			if value == "silent" && containsAny(clause, "安静", "静音", "低噪", "不要噪音", "不能吵", "别太吵", "不要太吵") {
				return true
			}
			if value == "normal" && containsAny(clause, "普通噪音", "正常噪音", "正常就行", "噪音正常", "正常声音") {
				return true
			}
			if value == "any" && containsAny(clause, "噪音", "静音", "声音", "吵") && containsAny(clause, "不限", "都行", "无所谓", "不在意", "无要求", "随意") {
				return true
			}
		case "size_pref":
			if containsAny(clause, "不要"+value, "不用"+value, "不选"+value, "排除"+value) {
				continue
			}
			switch value {
			case "any":
				if containsAny(clause, "尺寸", "大小", "机箱", "板型") && containsAny(clause, "不限", "都行", "无所谓", "无要求", "随意") {
					return true
				}
			case "itx":
				if strings.Contains(clause, "itx") {
					return true
				}
			case "matx":
				if containsAny(clause, "matx", "m-atx", "m_atx") {
					return true
				}
			case "atx":
				if strings.Contains(clause, "atx") && !containsAny(clause, "matx", "m-atx", "m_atx") {
					return true
				}
			}
		case "use_case.type":
			switch value {
			case "gaming":
				if containsAny(clause, "游戏", "电竞", "玩", "黑神话", "cs2", "csgo", "永劫", "原神") {
					return true
				}
			case "general":
				if containsAny(clause, "办公", "上网", "影音", "看电影", "家用", "日常", "浏览网页") {
					return true
				}
			case "productivity":
				if videoEditingClaim.MatchString(clause) && !containsAny(clause, "不剪", "不用剪", "不需要剪", "不做视频", "不制作视频") {
					return true
				}
				if containsAny(clause, "剪辑", "渲染", "建模", "编译", "开发", "训练", "生产力", "深度学习", "视频制作", "pr", "blender", "cad", "达芬奇") {
					return true
				}
			}
		}
	}
	return false
}

// 对用户明确“先不改”的备选讨论加确定性保护，防止模型摘掉否定句后提交set。
// 混合消息按最后的“如果/比较”讨论片段及字段主题定位，不拦截独立预算修改。
func explicitlyOnlyDiscussion(op schemas.RequirementOperation, userTexts []string) bool {
	texts := append(append([]string(nil), userTexts...), op.Quote)
	for _, text := range texts {
		if !containsAny(text, "先不改", "暂时不改", "不修改", "只是比较", "只比较", "仅作比较", "只是对比", "只是讨论", "只讨论", "只是备选") {
			continue
		}
		start := -1
		for _, marker := range []string{"如果", "假设", "比较", "对比", "备选"} {
			if at := strings.LastIndex(text, marker); at > start {
				start = at
			}
		}
		if start >= 0 {
			text = text[start:]
		}
		lower := strings.ToLower(text)
		switch op.Field {
		case "budget_cny", "budget_flex", "budget_basis":
			if strings.Contains(lower, "预算") {
				return true
			}
		case "use_case.resolution":
			if containsAny(lower, "分辨率", "4k", "2k", "1080", "显示器") {
				return true
			}
		case "brand_pref.cpu":
			if containsAny(lower, "cpu", "处理器", "intel", "英特尔") {
				return true
			}
		case "brand_pref.gpu":
			if containsAny(lower, "显卡", "gpu", "nvidia", "n卡", "a卡", "英伟达") {
				return true
			}
		case "noise_pref":
			if containsAny(lower, "静音", "安静", "噪音") {
				return true
			}
		case "size_pref":
			if containsAny(lower, "尺寸", "大小", "itx", "atx") {
				return true
			}
		case "appearance":
			if containsAny(lower, "外观", "白色", "黑色", "rgb", "海景房") {
				return true
			}
		default:
			return true
		}
	}
	return false
}
