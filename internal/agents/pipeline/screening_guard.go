package pipeline

import (
	"context"
	"encoding/json"
	"iter"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

type screeningSourcesKey struct{}
type screeningObserverKey struct{}

// WithScreeningSources 由产品层提供有界的真实用户消息；助手示例不能成为字段证据。
func WithScreeningSources(ctx context.Context, sources []string) context.Context {
	return context.WithValue(ctx, screeningSourcesKey{}, append([]string{}, sources...))
}

// WithScreeningObserver 仅供显式评估保存原始输出及拦截原因，不写普通产品日志。
func WithScreeningObserver(ctx context.Context, observe func(string, []string)) context.Context {
	return context.WithValue(ctx, screeningObserverKey{}, observe)
}

// screeningGuard 在 ADK OutputKey 和可见事件写入前核验，产品、dev UI 和评估共用。
// 单次非流式调用避免未经核验的半成品 JSON 提前出现在用户界面。
type screeningGuard struct{ model.LLM }

func (g screeningGuard) GenerateContent(ctx context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		sources, supplied := ctx.Value(screeningSourcesKey{}).([]string)
		if !supplied {
			for _, content := range req.Contents {
				if content != nil && content.Role == string(genai.RoleUser) {
					sources = append(sources, screeningText(content))
				}
			}
		}
		for response, err := range g.LLM.GenerateContent(ctx, req, false) {
			if err != nil || response == nil || response.ErrorCode != "" || response.ErrorMessage != "" {
				if !yield(response, err) {
					return
				}
				continue
			}
			raw := screeningText(response.Content)
			text, missing := guardOwnedScreening(raw, sources)
			if observe, ok := ctx.Value(screeningObserverKey{}).(func(string, []string)); ok {
				observe(raw, missing)
			}
			if text != raw {
				copyResponse := *response
				copyResponse.Content = genai.NewContentFromText(text, genai.RoleModel)
				response = &copyResponse
			}
			if !yield(response, nil) {
				return
			}
		}
	}
}

func screeningText(content *genai.Content) string {
	var b strings.Builder
	if content != nil {
		for _, part := range content.Parts {
			if part != nil && !part.Thought && part.Text != "" {
				b.WriteString(part.Text)
			}
		}
	}
	return b.String()
}

// 读取所有新装机草稿的必填字段，即使半成品尚不能通过完整 schema 也能明确追问。
// ChangeRequest 和模型未遵守草稿协议的自然语言输出仍走原路径。
func guardOwnedScreening(text string, sources []string) (string, []string) {
	raw := extractJSONObject(text)
	if raw == nil || hasTopLevelKey(raw, "intent") {
		return text, nil
	}
	var input struct {
		Budget  int `json:"budget_cny"`
		UseCase struct {
			Type       string `json:"type"`
			Resolution string `json:"resolution"`
		} `json:"use_case"`
		Existing []schemas.Category  `json:"existing_parts"`
		Owned    []schemas.OwnedPart `json:"owned_parts"`
		Basis    string              `json:"budget_basis"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return text, nil
	}
	declared := map[schemas.Category]bool{}
	models := map[schemas.Category]string{}
	for _, category := range input.Existing {
		declared[category] = true
	}
	for _, part := range input.Owned {
		declared[part.Category], models[part.Category] = true, part.Model
	}
	var missing, questions, names []string
	for _, category := range schemas.AllCategories {
		if declared[category] && !groundedModel(models[category], sources) {
			missing = append(missing, "owned_parts."+string(category)+".model")
			names = append(names, ownedCategoryName(category))
		}
	}
	if len(names) > 0 {
		questions = append(questions, "请提供已有"+strings.Join(names, "、")+"的完整型号。")
	}
	basis := explicitBudgetBasis(sources)
	if len(declared) > 0 && basis == "" {
		missing = append(missing, "budget_basis")
		questions = append(questions, "这笔预算是只用于新增购买配件，还是包含已有配件价值的整机参考总价？")
	}
	if input.Budget <= 0 || !groundedBudget(input.Budget, sources) {
		missing = append(missing, "budget_cny")
		questions = append(questions, "请提供预算金额，单位元。")
	}
	if input.UseCase.Type == "" {
		missing = append(missing, "use_case")
		questions = append(questions, "这台电脑主要用于什么用途？")
	} else if input.UseCase.Type == "gaming" && input.UseCase.Resolution == "" {
		missing = append(missing, "resolution")
		questions = append(questions, "主要使用的游戏分辨率是1080p、2K还是4K？")
	}
	if len(missing) == 0 {
		// 明确口径被模型遗漏或填反时，按用户原话修正，不重复索要已知信息。
		if len(declared) > 0 && input.Basis != basis {
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) == nil {
				fields["budget_basis"], _ = json.Marshal(basis)
				if corrected, err := json.Marshal(fields); err == nil {
					return string(corrected), nil
				}
			}
		}
		return text, nil
	}
	return strings.Join(questions, ""), missing
}

// 金额必须在用户消息中有明确依据；不把型号或分辨率里的数字当作预算。
// 覆盖阿拉伯数字（含千分位、小数、千/万/k）和常见中文整数表达。
// 这是保守的金额表达识别，未知写法要求确认，不猜测默认预算。
const budgetNumberPattern = `((?:[0-9]{1,3}(?:[,，][0-9]{3})+|[0-9]+)(?:\.[0-9]+)?|[零〇一二两三四五六七八九十百千万]+)\s*([千万k]?)`

var budgetAmount = regexp.MustCompile(`(?i)(?:预算(?:金额)?(?:为|是|大约|约|只有|最多|不超过|控制在|上限|[:：=\s])*|^\s*)` + budgetNumberPattern)
var moneyAmount = regexp.MustCompile(`(?i)` + budgetNumberPattern + `\s*(?:元|块钱|块)`)

func groundedBudget(budget int, sources []string) bool {
	for _, source := range sources {
		for _, pattern := range []*regexp.Regexp{budgetAmount, moneyAmount} {
			for _, match := range pattern.FindAllStringSubmatch(source, -1) {
				number := strings.NewReplacer(",", "", "，", "").Replace(match[1])
				value, err := strconv.ParseFloat(number, 64)
				if err != nil {
					value = chineseBudgetNumber(number)
				}
				switch strings.ToLower(match[2]) {
				case "千", "k":
					value *= 1000
				case "万":
					value *= 10000
				}
				if value == float64(budget) {
					return true
				}
			}
		}
	}
	return false
}

func chineseBudgetNumber(text string) float64 {
	digits := map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	total, section, digit := 0, 0, 0
	for _, r := range text {
		if n, ok := digits[r]; ok {
			digit = n
			continue
		}
		unit := map[rune]int{'十': 10, '百': 100, '千': 1000, '万': 10000}[r]
		if unit == 10000 {
			total += (section + digit) * unit
			section, digit = 0, 0
		} else {
			if digit == 0 {
				digit = 1
			}
			section += digit * unit
			digit = 0
		}
	}
	return float64(total + section + digit)
}

func ownedCategoryName(category schemas.Category) string {
	names := map[schemas.Category]string{
		schemas.CategoryCPU: "CPU", schemas.CategoryGPU: "显卡", schemas.CategoryMotherboard: "主板",
		schemas.CategoryMemory: "内存", schemas.CategorySSD: "SSD", schemas.CategoryPSU: "电源",
		schemas.CategoryCase: "机箱", schemas.CategoryCooler: "散热器",
	}
	return names[category]
}

func normalizedModel(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, text)
}

func groundedModel(name string, sources []string) bool {
	name = normalizedModel(name)
	// 排除品类占位符；型号未必有数字（例如 Fractal Design Terra），不臆造格式要求。
	if name == "" {
		return false
	}
	for _, category := range schemas.AllCategories {
		if name == normalizedModel(string(category)) || name == normalizedModel(ownedCategoryName(category)) {
			return false
		}
	}
	for _, source := range sources {
		if strings.Contains(normalizedModel(source), name) {
			return true
		}
	}
	return false
}

var budgetClauses = regexp.MustCompile(`[。！？!?；;\n]`)

// 只识别明确的费用口径；金额、其余件要新买、助手建议均不能推出 new_purchase。
// 这是保守的中文表达白名单，不是通用语义判断；未知或冲突表达保留追问。
func explicitBudgetBasis(sources []string) string {
	var basis string
	for _, source := range sources {
		for _, clause := range budgetClauses.Split(source, -1) {
			clause = strings.Join(strings.Fields(clause), "")
			if containsAny(clause, "不是", "不只", "不单", "不要", "不能", "不用", "无需", "不需要", "并非", "是否", "还是", "如果", "假如") {
				continue
			}
			newPurchase := containsAny(clause, "新增购买预算", "新增预算", "新购预算", "只算新购", "只算新增", "只算新买", "预算只用于新", "预算仅用于新", "只用于新增购买", "不包含已有", "不包括已有", "不含已有", "不计已有", "不计入已有")
			fullBuild := containsAny(clause, "包含已有", "包括已有", "计入已有", "整机参考总价", "整机总预算")
			// “不包含已有”只能支持新增口径，不同时当成包含已有。
			if containsAny(clause, "不包含已有", "不包括已有", "不含已有", "不计已有", "不计入已有") {
				fullBuild = false
			}
			candidate := ""
			if newPurchase && !fullBuild {
				candidate = "new_purchase"
			}
			if fullBuild && !newPurchase {
				candidate = "full_build"
			}
			if (newPurchase && fullBuild) || (basis != "" && candidate != "" && basis != candidate) {
				return ""
			}
			if candidate != "" {
				basis = candidate
			}
		}
	}
	return basis
}

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}
