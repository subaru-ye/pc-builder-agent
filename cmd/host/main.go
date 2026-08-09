// P5 host:装机初筛(近端)+ A2A 远程消费方。初筛把自然语言整理成 RequirementSpec/
// ChangeRequest JSON,再经 A2A 单跳把结构化载荷送到 cmd/buildsvc 生成 + 校验(P5-A2A单跳拆分设计.md)。
// 运行方式(从仓库根;需先 `go run ./cmd/buildsvc` 起远程服务):
//
//	go run ./cmd/host                # console 模式,快速验证全链路
//	go run ./cmd/host web --write-timeout=10m api --sse-write-timeout=10m webui  # Web UI,http://localhost:8080/ui/
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/remoteagent/v2"
	"google.golang.org/adk/v2/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/v2/cmd/launcher"
	"google.golang.org/adk/v2/cmd/launcher/full"
	"google.golang.org/adk/v2/model/openaimodel"
	"google.golang.org/adk/v2/session"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/redisstore"
)

// 模型分档(ADR-004:型号只写代码常量):初筛低价档留在 host;生成旗舰档迁 cmd/buildsvc。
const screeningModelName = "qwen-flash"

// 百炼 OpenAI 兼容端点(公共默认);工作空间专属 Host 用 DASHSCOPE_BASE_URL 覆盖。
const defaultBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"

// buildsvc 远程服务默认地址(与 cmd/buildsvc 的 BUILDSVC_ADDR 默认值对齐)。
const defaultBuildsvcURL = "http://localhost:8081"

// 生成 Agent 在语义选件场景可能需要多轮检索与校验;a2a-go 默认 3 分钟会在远端
// 已落库但响应尚未返回时提前断开。这里放宽到 10 分钟,仍保留有限超时避免永久挂起。
const buildsvcRequestTimeout = 10 * time.Minute

func main() {
	ctx := context.Background()

	dotenv.Load(".env")

	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	if apiKey == "" {
		log.Fatal("DASHSCOPE_API_KEY 未设置:复制 .env.example 为 .env 并填入百炼 key")
	}
	baseURL := os.Getenv("DASHSCOPE_BASE_URL")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	buildsvcURL := os.Getenv("BUILDSVC_URL")
	if buildsvcURL == "" {
		buildsvcURL = defaultBuildsvcURL
	}

	clientCfg := &openaimodel.ClientConfig{APIKey: apiKey, BaseURL: baseURL}
	screeningModel, err := openaimodel.NewModel(ctx, screeningModelName, clientCfg)
	if err != nil {
		log.Fatalf("创建初筛模型失败: %v", err)
	}

	screening, err := pipeline.NewScreening(screeningModel)
	if err != nil {
		log.Fatalf("装配初筛 Agent 失败: %v", err)
	}

	// A2A 远程消费方:发送前用 trimToPayload 把消息裁成唯一一条结构化载荷(只传三样,§八.2)。
	remote, err := remoteagent.NewA2A(remoteagent.A2AConfig{
		Name:              "build_service",
		Description:       "远程装机生成+校验服务(A2A 单跳):RequirementSpec/ChangeRequest 进,配置单+校验报告出。",
		AgentCardProvider: remoteagent.NewAgentCardProvider(buildsvcURL),
		ClientProvider: remoteagent.NewA2AClientProvider(a2aclient.NewFactory(
			a2aclient.WithJSONRPCTransport(&http.Client{Timeout: buildsvcRequestTimeout}),
		)),
		BeforeRequestCallbacks: []remoteagent.BeforeA2ARequestCallback{trimToPayload},
	})
	if err != nil {
		log.Fatalf("装配 A2A 远程消费方失败(buildsvc=%s): %v", buildsvcURL, err)
	}

	root, err := sequentialagent.New(sequentialagent.Config{
		AgentConfig: agent.Config{
			Name:        "pc_builder_host",
			Description: "装机 host:需求初筛 → A2A 远程生成校验服务。",
			SubAgents:   []agent.Agent{screening, remote},
		},
	})
	if err != nil {
		log.Fatalf("装配 host 流水线失败: %v", err)
	}

	// P6:会话热上下文迁 Redis(带 TTL、两进程共享),让 kill host 重启后同一会话可续;
	// 无 REDIS_ADDR 时降级回退进程内 InMemory(见 redisstore.Open)。
	backend := redisstore.Open(ctx)
	defer func() { _ = backend.Close() }()

	log.Printf("[host] 已接线 A2A 远程服务:%s", buildsvcURL)
	l := full.NewLauncher()
	config := &launcher.Config{
		AgentLoader:    agent.NewSingleLoader(root),
		SessionService: backend.SessionService(),
	}
	if err := l.Execute(ctx, config, os.Args[1:]); err != nil {
		log.Fatalf("运行失败: %v\n\n%s", err, l.CommandLineSyntax())
	}
}

// trimToPayload 发送前把 A2A 消息裁成唯一一条结构化载荷 JSON(指引 §八.2「只传三样」):
// ADK 默认转发「自上次远程响应以来的全部会话事件」(含用户自然语言 + 初筛推理),此处
// 从其中抽出 RequirementSpec/ChangeRequest 对象作为唯一 part,丢弃对话全文与推理。
// ContextID 不动以保证跨轮连续。抽不出结构化载荷(初筛追问轮)时原样放行,由服务端兜底。
func trimToPayload(ctx agent.Context, req *a2a.SendMessageRequest) (*session.Event, error) {
	if req == nil || req.Message == nil {
		return nil, nil
	}
	var b strings.Builder
	for _, p := range req.Message.Parts {
		b.WriteString(p.Text())
	}
	payload := pipeline.ExtractPayload(b.String())
	if payload == nil {
		log.Printf("[host] A2A 出站:无结构化载荷,原样转发(初筛追问轮)")
		return nil, nil
	}
	if sanitized, changed := sanitizeInferredBrandPrefs(payload, originalUserText(ctx)); changed {
		payload = sanitized
		log.Printf("[host] 初筛纠偏:移除用户未明确指定的 CPU/GPU 品牌偏好")
	}
	log.Printf("[host] A2A 出站:contextID=%s 载荷类型=%s", req.Message.ContextID, pipeline.PayloadKind(payload))
	req.Message.Parts = a2a.ContentParts{a2a.NewTextPart(string(payload))}
	return nil, nil
}

// sanitizeInferredBrandPrefs 防止初筛模型把用途、静音或风格偏好误推成 CPU/GPU
// 品牌偏好。只处理 RequirementSpec;ChangeRequest 的 target_hint 保持原样。
func sanitizeInferredBrandPrefs(payload json.RawMessage, userText string) (json.RawMessage, bool) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(payload, &root); err != nil {
		return payload, false
	}
	if _, isChange := root["intent"]; isChange {
		return payload, false
	}

	var pref map[string]json.RawMessage
	if err := json.Unmarshal(root["brand_pref"], &pref); err != nil {
		return payload, false
	}

	changed := false
	if !mentionsCPUBrand(userText) {
		if _, ok := pref["cpu"]; ok {
			delete(pref, "cpu")
			changed = true
		}
	}
	if !mentionsGPUBrand(userText) {
		if _, ok := pref["gpu"]; ok {
			delete(pref, "gpu")
			changed = true
		}
	}
	if !changed {
		return payload, false
	}

	if len(pref) == 0 {
		delete(root, "brand_pref")
	} else if raw, err := json.Marshal(pref); err == nil {
		root["brand_pref"] = raw
	} else {
		return payload, false
	}
	updated, err := json.Marshal(root)
	if err != nil {
		return payload, false
	}
	return updated, true
}

func originalUserText(ctx agent.Context) string {
	if ctx == nil || ctx.UserContent() == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range ctx.UserContent().Parts {
		b.WriteString(part.Text)
	}
	return b.String()
}

func mentionsCPUBrand(text string) bool {
	s := strings.Join(strings.Fields(strings.ToLower(text)), "")
	return containsAny(s, "amd", "ryzen", "锐龙", "intel", "英特尔", "酷睿")
}

func mentionsGPUBrand(text string) bool {
	s := strings.Join(strings.Fields(strings.ToLower(text)), "")
	return containsAny(s, "amd", "radeon", "镭龙", "a卡", "nvidia", "英伟达", "geforce", "rtx", "gtx", "n卡")
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
