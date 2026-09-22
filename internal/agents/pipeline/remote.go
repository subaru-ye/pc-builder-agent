package pipeline

// P5 A2A 单跳拆分(服务与状态.md §1):把单进程 Sequential 在初筛与
// 改单预处理之间切一刀。host 进程只跑初筛(NewScreening)+ A2A 远程消费方;
// buildsvc 进程跑「入口回填 → 改单预处理 → 生成校验循环」(NewRemote)。
//
// 跨进程只传结构化 schema(指引 §八.2「只传三样」):host 侧用 BeforeRequestCallbacks
// 把消息裁成唯一一条载荷 JSON;buildsvc 侧 ingest 把它落回 requirement_spec 状态键,
// prep 及其后全部沿用 P4 不改。schema 单一出处仍是 internal/schemas(§九)。

import (
	"encoding/json"
	"fmt"
	"iter"
	"log"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

const ingestAgentName = "ingest_agent"

// NewScreening 装配 host 侧初筛 Agent(单跳拆分的近端):只需初筛低价档模型,
// 不依赖 Store / 生成模型 / 检索。返回单个 agent,由 host 接进 Sequential(screening, remote)。
func NewScreening(m model.LLM) (agent.Agent, error) {
	if m == nil {
		return nil, fmt.Errorf("pipeline: NewScreening 的 ScreeningModel 不能为空")
	}
	return newScreeningAgent(m, llmagent.IncludeContentsDefault)
}

// NewProductScreening 装配产品 API 专用初筛 Agent。产品层会把稳定聊天记录压缩进
// 当前 UserContent，因此这里明确禁止 ADK 再回灌整段会话历史。
func NewProductScreening(m model.LLM) (agent.Agent, error) {
	if m == nil {
		return nil, fmt.Errorf("pipeline: NewProductScreening 的 ScreeningModel 不能为空")
	}
	return newScreeningAgent(m, llmagent.IncludeContentsNone)
}

// NewRemote 装配 buildsvc 侧远程服务根 agent:Sequential(ingest → prep → Loop(生成→校验))。
// 只需生成旗舰档模型 + Store + QueryEmbedder(初筛留在 host,不在本进程)。
func NewRemote(cfg Config) (agent.Agent, error) {
	if cfg.BuilderModel == nil {
		return nil, fmt.Errorf("pipeline: NewRemote 的 BuilderModel 不能为空")
	}
	if cfg.Store == nil {
		return nil, fmt.Errorf("pipeline: NewRemote 的 Store 不能为空")
	}
	if cfg.QueryEmbedder == nil {
		return nil, fmt.Errorf("pipeline: NewRemote 的 QueryEmbedder 不能为空")
	}

	ingest, err := newIngestAgent()
	if err != nil {
		return nil, err
	}
	prep, loop, err := newBuildCore(cfg)
	if err != nil {
		return nil, err
	}

	root, err := sequentialagent.New(sequentialagent.Config{
		AgentConfig: agent.Config{
			Name:        "pc_build_service",
			Description: "装机生成+校验远程服务(A2A):入口回填 → 改单预处理 → 选件生成 → 兼容性校验与报价。",
			SubAgents:   []agent.Agent{ingest, prep, loop},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("pipeline: 构造远程服务 Sequential 失败: %w", err)
	}
	return root, nil
}

// newIngestAgent 确定性入口节点(远程 Sequential 首个子 agent):读最近一条 user 事件
// 文本(host 送来的干净载荷),写入 state 键 requirement_spec —— 等价于 P4 单进程里
// 初筛 Agent 的 OutputKey,让 prep 及其后无需感知拆进程。同时记录 contextID + schema
// 校验结果(用例 G「日志可见跨进程 A2A 消息且 schema 校验通过」)。
func newIngestAgent() (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        ingestAgentName,
		Description: "确定性入口:把入站 A2A 载荷落为 requirement_spec 状态键,记录 contextID 与 schema 校验。",
		Run: func(ictx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				text := latestUserText(ictx)
				payload := extractJSONObject(text)
				contextID := ictx.Session().ID()
				log.Printf("[buildsvc] A2A 入站 contextID=%s 载荷=%d 字节 类型=%s",
					contextID, len(payload), PayloadKind(payload))

				// requirement_spec 沿用 P4 语义:存原始文本,prep 里再 extractJSONObject。
				yield(&session.Event{
					Author: ingestAgentName,
					Actions: session.EventActions{StateDelta: map[string]any{
						stateKeyRequirementSpec: text,
					}},
				}, nil)
			}
		},
	})
}

// latestUserText 取会话里最近一条 user 事件的合并文本(A2A 执行器把入站消息落为 user 事件)。
func latestUserText(ictx agent.InvocationContext) string {
	events := ictx.Session().Events()
	for i := events.Len() - 1; i >= 0; i-- {
		ev := events.At(i)
		if ev.Author != "user" || ev.Content == nil {
			continue
		}
		var b strings.Builder
		for _, p := range ev.Content.Parts {
			if p.Text != "" {
				b.WriteString(p.Text)
			}
		}
		if b.Len() > 0 {
			return b.String()
		}
	}
	return ""
}

// ExtractPayload 从文本里抽出结构化载荷 JSON(host 发送前裁剪 A2A 消息用);
// 复用 extractJSONObject 同款策略(优先取含 schema_version 的顶层对象)。
func ExtractPayload(text string) json.RawMessage {
	return extractJSONObject(text)
}

// PayloadKind 用 internal/schemas 对载荷做类型识别 + schema 校验,返回一行可读结论
// (host 出站前与 buildsvc 入站后共用同一份判定,schema 单一出处,§九)。
func PayloadKind(payload json.RawMessage) string {
	if payload == nil {
		return "无载荷(初筛追问轮)"
	}
	if hasTopLevelKey(payload, "intent") {
		if _, err := schemas.DecodeChangeRequest(payload); err != nil {
			return fmt.Sprintf("ChangeRequest(schema 校验失败:%v)", err)
		}
		return "ChangeRequest(schema 校验通过)"
	}
	if _, err := schemas.DecodeRequirementSpec(payload); err != nil {
		return fmt.Sprintf("RequirementSpec(schema 校验失败:%v)", err)
	}
	return "RequirementSpec(schema 校验通过)"
}
