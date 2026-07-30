package tools

import (
	"context"
	"fmt"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// BuildEvaluator 校验 tool 对确定性校验核心的依赖面(*validate.Node 实现之)。
type BuildEvaluator interface {
	Evaluate(context.Context, schemas.BuildSelection) (validate.Result, error)
}

// ValidateBuildArgs 校验入参:完整 BuildDraft JSON 文本(§四.2 契约)。
// 用原文透传而非松散 map,让 DecodeBuildDraft 的严格解码(未知字段/缺字段)全量生效。
type ValidateBuildArgs struct {
	BuildDraftJSON string `json:"build_draft_json"`
}

// ValidateBuildResult 校验出参:P1 ValidationReport + 报价块(§4.2)。
type ValidateBuildResult struct {
	Report schemas.ValidationReport `json:"report"`
	Quote  validate.Quote           `json:"quote"`
}

// validateBuildDescription 从生成 Agent 视角描述边界与协作(工程实践指引 §四.2)。
const validateBuildDescription = `对一份完整 BuildDraft 执行 12 条兼容性规则校验(纯规则引擎,非模型判断),并按最新价格快照报价。

边界:
- build_draft_json 必须是完整 BuildDraft 的 JSON 字符串(schema_version=1,含 requirement_ref/build_ref/selection)。
- schema 不合法、SKU 不在库中都会直接报错;报错信息指明具体问题,修正后重试。
- 返回的 report.overall_status:pass=可交付;review=有 warning/unknown,可交付但需提示;fail=存在错误级违规,必须按 checks 里的失败项定向换件。
- quote 中缺价零件不计入合计,missing_count/missing_skus 如实标注。

示例:
- {"build_draft_json":"{\"schema_version\":1,\"requirement_ref\":\"req_001\",...}"}

协作:本工具的结论是最终判定,覆盖任何自评;selection 里的 sku 必须来自 search_parts 的返回。`

// NewValidateBuild 构造规则校验 tool(确定性,零 LLM)。
func NewValidateBuild(e BuildEvaluator) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "validate_build",
		Description: validateBuildDescription,
	}, func(ctx agent.Context, args ValidateBuildArgs) (ValidateBuildResult, error) {
		return runValidateBuild(ctx, e, args)
	})
}

// runValidateBuild 严格解码 BuildDraft → 确定性校验;schema error / 未知 SKU 显式上抛(§4.2 保真)。
func runValidateBuild(ctx context.Context, e BuildEvaluator, args ValidateBuildArgs) (ValidateBuildResult, error) {
	draft, err := schemas.DecodeBuildDraft([]byte(args.BuildDraftJSON))
	if err != nil {
		return ValidateBuildResult{}, fmt.Errorf("tools: BuildDraft 不合法: %w", err)
	}
	res, err := e.Evaluate(ctx, draft.Selection)
	if err != nil {
		return ValidateBuildResult{}, err
	}
	return ValidateBuildResult{Report: res.Report, Quote: res.Quote}, nil
}
