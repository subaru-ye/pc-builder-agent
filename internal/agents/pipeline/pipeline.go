// Package pipeline 装配 P2 单进程三 Agent 流水线(P2 流水线设计 §2 拓扑,冻结):
//
//	Sequential(初筛 llmagent → Loop(生成 llmagent → 校验确定性节点), max=3)
//
// 模型实例由 cmd/host 注入(型号常量只写在 host,ADR-004),本包不 import 模型 SDK;
// 提示词见 prompts.go(随代码入 Git,改动后须重跑用例 A 回归)。
package pipeline

import (
	"fmt"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/agent/workflowagents/loopagent"
	"google.golang.org/adk/v2/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/tools"
	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// Config 流水线装配参数:两档模型(初筛低价 / 生成旗舰)+ 数据层 + 查询向量化。
type Config struct {
	ScreeningModel model.LLM
	BuilderModel   model.LLM
	Store          *store.Store
	QueryEmbedder  tools.QueryEmbedder // P3 语义检索的 query 向量化(host 注入端点实现)
}

// New 装配完整流水线根 agent(挂给 launcher)。
func New(cfg Config) (agent.Agent, error) {
	if cfg.ScreeningModel == nil || cfg.BuilderModel == nil {
		return nil, fmt.Errorf("pipeline: ScreeningModel/BuilderModel 不能为空")
	}
	if cfg.Store == nil {
		return nil, fmt.Errorf("pipeline: Store 不能为空")
	}
	if cfg.QueryEmbedder == nil {
		return nil, fmt.Errorf("pipeline: QueryEmbedder 不能为空")
	}

	node := validate.New(cfg.Store)

	searchTool, err := tools.NewSearchParts(cfg.Store)
	if err != nil {
		return nil, fmt.Errorf("pipeline: 构造 search_parts 失败: %w", err)
	}
	semanticTool, err := tools.NewSearchPartsSemantic(cfg.QueryEmbedder, cfg.Store)
	if err != nil {
		return nil, fmt.Errorf("pipeline: 构造 search_parts_semantic 失败: %w", err)
	}
	validateTool, err := tools.NewValidateBuild(node)
	if err != nil {
		return nil, fmt.Errorf("pipeline: 构造 validate_build 失败: %w", err)
	}

	screening, err := llmagent.New(llmagent.Config{
		Name:                     "requirement_agent",
		Model:                    cfg.ScreeningModel,
		Description:              "初筛 Agent:把用户自然语言装机需求整理成 RequirementSpec JSON,信息不足时追问。",
		Instruction:              screeningInstruction,
		OutputKey:                stateKeyRequirementSpec,
		DisallowTransferToParent: true,
		DisallowTransferToPeers:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("pipeline: 构造初筛 Agent 失败: %w", err)
	}

	// 生成 Agent 挂 validate_build 供输出前自查,但最终判定仍由 validator_agent
	// 确定性节点做(工程实践指引 §四.1:真值不采信模型自报)。
	builder, err := llmagent.New(llmagent.Config{
		Name:                     "builder_agent",
		Model:                    cfg.BuilderModel,
		Description:              "生成 Agent:按 RequirementSpec 用 search_parts 从零件库选件,产出 BuildDraft JSON。",
		Instruction:              builderInstruction,
		Tools:                    []tool.Tool{searchTool, semanticTool, validateTool},
		OutputKey:                stateKeyBuildDraft,
		DisallowTransferToParent: true,
		DisallowTransferToPeers:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("pipeline: 构造生成 Agent 失败: %w", err)
	}

	validator, err := newValidatorAgent(node)
	if err != nil {
		return nil, fmt.Errorf("pipeline: 构造校验节点失败: %w", err)
	}

	loop, err := loopagent.New(loopagent.Config{
		MaxIterations: maxLoopRounds,
		AgentConfig: agent.Config{
			Name:        "build_loop",
			Description: "生成→校验循环:校验通过或命中停止条件(轮数/死循环/熔断)即出栈。",
			SubAgents:   []agent.Agent{builder, validator},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("pipeline: 构造 Loop 失败: %w", err)
	}

	root, err := sequentialagent.New(sequentialagent.Config{
		AgentConfig: agent.Config{
			Name:        "pc_builder_pipeline",
			Description: "装机配置单流水线:需求初筛 → 选件生成 → 兼容性校验与报价。",
			SubAgents:   []agent.Agent{screening, loop},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("pipeline: 构造 Sequential 失败: %w", err)
	}
	return root, nil
}
