package pipeline

import (
	"encoding/json"
	"fmt"
	"iter"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/evalmetrics"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

const harnessV2AgentName = "harness_v2_agent"

// NewRemoteV2 装配 Agent Harness 2.0：入口与改单预处理保持不变，生成阶段改为
// 确定性候选准备 → 无工具模型选配 → 确定性校验 → 最多两次定向修复。
func NewRemoteV2(cfg Config) (agent.Agent, error) {
	if cfg.BuilderModel == nil {
		return nil, fmt.Errorf("pipeline: NewRemoteV2 的 BuilderModel 不能为空")
	}
	if cfg.Store == nil {
		return nil, fmt.Errorf("pipeline: NewRemoteV2 的 Store 不能为空")
	}
	if cfg.QueryEmbedder == nil {
		return nil, fmt.Errorf("pipeline: NewRemoteV2 的 QueryEmbedder 不能为空")
	}
	planner, err := buildharness.NewCandidatePlanner(cfg.Store, cfg.QueryEmbedder)
	if err != nil {
		return nil, err
	}
	harness, err := buildharness.New(buildharness.Config{
		Model: cfg.BuilderModel, Planner: planner, Repairer: buildharness.NewRepairPlanner(),
		Eval: validate.New(cfg.Store), Trace: buildharness.EvalTraceSink{Component: "buildsvc"},
	})
	if err != nil {
		return nil, err
	}
	ingest, err := newIngestAgent()
	if err != nil {
		return nil, err
	}
	prep, err := newChangePrepAgent()
	if err != nil {
		return nil, err
	}
	executor, err := newHarnessV2Agent(harness, cfg.Store)
	if err != nil {
		return nil, err
	}
	root, err := sequentialagent.New(sequentialagent.Config{AgentConfig: agent.Config{
		Name:        "pc_build_service",
		Description: "装机生成+校验远程服务(A2A):入口回填 → 改单预处理 → Harness v2 确定性候选与定向修复。",
		SubAgents:   []agent.Agent{ingest, prep, executor},
	}})
	if err != nil {
		return nil, fmt.Errorf("pipeline: 构造 Harness v2 远程服务失败: %w", err)
	}
	return root, nil
}

func newHarnessV2Agent(harness buildharness.Harness, saver BuildSaver) (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        harnessV2AgentName,
		Description: "Harness v2:确定性准备候选、无工具选配、校验和定向修复。",
		Run: func(ictx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				st := ictx.Session().State()
				chg := loadChangeCtx(st)
				if chg == nil || len(chg.ActiveSpec) == 0 {
					message := "等待有效需求确认后再生成配置。"
					if chg != nil && chg.Invalid != "" {
						message = "改单请求无法执行:" + chg.Invalid
					}
					event := &session.Event{Author: harnessV2AgentName}
					event.Content = genai.NewContentFromText(message, genai.RoleModel)
					yield(event, nil)
					return
				}
				spec, err := schemas.DecodeRequirementSpec(chg.ActiveSpec)
				if err != nil {
					yield(nil, fmt.Errorf("harness_v2: 生效需求单非法: %w", err))
					return
				}
				var change *schemas.ChangeRequest
				if len(chg.Change) > 0 {
					decoded, err := schemas.DecodeChangeRequest(chg.Change)
					if err != nil {
						yield(nil, fmt.Errorf("harness_v2: ChangeRequest 非法: %w", err))
						return
					}
					change = &decoded
				}
				var base *schemas.BuildSelection
				if len(chg.BaseSelection) > 0 {
					decoded, err := decodeStateSelection(chg.BaseSelection, chg.BaseVersion)
					if err != nil {
						yield(nil, fmt.Errorf("harness_v2: 基版本 selection 非法: %w", err))
						return
					}
					base = &decoded
				}
				result, err := harness.Run(ictx, buildharness.BuildInput{
					Requirement: spec, Change: change, BaseSelection: base, Locked: chg.Locked,
					TraceID: evalmetrics.Fingerprint(ictx.Session().ID()),
				})
				if err != nil {
					yield(nil, fmt.Errorf("harness_v2: %w", err))
					return
				}
				v, err := harnessVerdict(result)
				if err != nil {
					yield(nil, err)
					return
				}

				delta := map[string]any{stateKeyRound: result.Attempts}
				if result.Draft.SchemaVersion != 0 {
					delta[stateKeyBuildDraft] = string(draftWireJSON(result.Draft))
					delta[stateKeyLastSelection] = canonicalSelection(result.Draft.Selection)
				}
				if v.reportJSON != "" {
					delta[stateKeyReport] = v.reportJSON
				}
				message := result.Message
				if result.Succeeded {
					message = deliveryMessage(result.Draft, result.Result)
					suffix, stateJSON := persistVersion(ictx, saver, ictx.Session().ID(), chg, v,
						stateString(st, stateKeyBuildState))
					message += suffix
					if stateJSON != "" {
						delta[stateKeyBuildState] = stateJSON
					}
				}
				event := &session.Event{
					Author:  harnessV2AgentName,
					Actions: session.EventActions{StateDelta: delta},
				}
				event.Content = genai.NewContentFromText(message, genai.RoleModel)
				yield(event, nil)
			}
		},
	})
}

// harnessVerdict 把 v2 的结构化结果桥接到 legacy/v2 共用的版本保存核心。
// reportJSON 是 SaveBuildVersion 的必填原子输入，不能只写 ADK state。
func harnessVerdict(result buildharness.BuildResult) (verdict, error) {
	v := verdict{deliver: result.Succeeded, draft: result.Draft, res: result.Result}
	if result.Result.Report.BuildRef == "" {
		return v, nil
	}
	reportJSON, err := json.Marshal(result.Result.Report)
	if err != nil {
		return verdict{}, fmt.Errorf("harness_v2: 序列化校验报告失败: %w", err)
	}
	v.reportJSON = string(reportJSON)
	return v, nil
}

func decodeStateSelection(raw json.RawMessage, version int) (schemas.BuildSelection, error) {
	wire := map[string]any{
		"schema_version": schemas.BuildSelectionSchemaVersion,
		"build_ref":      fmt.Sprintf("v%d", version),
		"parts":          raw,
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return schemas.BuildSelection{}, err
	}
	return schemas.DecodeBuildSelection(encoded)
}
