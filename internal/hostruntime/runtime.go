// Package hostruntime 装配产品 API 与评估命令共用的初筛和 A2A 远程 Agent。
// 模型、提示词、A2A 超时和出站裁剪只在本包维护，避免各入口静默漂移。
package hostruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/remoteagent/v2"
	"google.golang.org/adk/v2/session"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"
)

const (
	// AppName 固定为既有 host 根 Agent 名；产品 API 使用同名 ADK session 命名空间。
	AppName = "pc_builder_host"

	DefaultScreeningModel  = modelprovider.DefaultScreeningModel
	DefaultBuildsvcURL     = "http://localhost:8081"
	BuildsvcRequestTimeout = 10 * time.Minute
)

// Config 是 host/API 共享的运行时连接配置。
type Config struct {
	Screening   modelprovider.Config
	BuildsvcURL string
}

// ConfigFromEnv 读取初筛供应商与 buildsvc 配置并补齐公共默认值。
func ConfigFromEnv() (Config, error) {
	screening, err := modelprovider.Load(modelprovider.RoleScreening)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{Screening: screening, BuildsvcURL: os.Getenv("BUILDSVC_URL")}
	if cfg.BuildsvcURL == "" {
		cfg.BuildsvcURL = DefaultBuildsvcURL
	}
	return cfg, nil
}

// Runtime 提供产品 API 分阶段调用的两个 Agent。
type Runtime struct {
	ProductScreening agent.Agent
	Remote           agent.Agent
}

// New 装配共享 Agent runtime。调用方负责提供 session.Service 给 runner。
func New(ctx context.Context, cfg Config) (*Runtime, error) {
	if cfg.BuildsvcURL == "" {
		cfg.BuildsvcURL = DefaultBuildsvcURL
	}

	screeningModel, err := modelprovider.NewChat(ctx, cfg.Screening, "api")
	if err != nil {
		return nil, fmt.Errorf("创建初筛模型失败: %w", err)
	}

	productScreening, err := pipeline.NewProductScreening(screeningModel)
	if err != nil {
		return nil, fmt.Errorf("装配产品初筛 Agent 失败: %w", err)
	}

	remote, err := remoteagent.NewA2A(remoteagent.A2AConfig{
		Name:              "build_service",
		Description:       "远程装机生成+校验服务(A2A 单跳):RequirementSpec/ChangeRequest 进,配置单+校验报告出。",
		AgentCardProvider: remoteagent.NewAgentCardProvider(cfg.BuildsvcURL),
		ClientProvider: remoteagent.NewA2AClientProvider(a2aclient.NewFactory(
			a2aclient.WithJSONRPCTransport(&http.Client{Timeout: BuildsvcRequestTimeout}),
		)),
		BeforeRequestCallbacks: []remoteagent.BeforeA2ARequestCallback{trimToPayload},
	})
	if err != nil {
		return nil, fmt.Errorf("装配 A2A 远程消费方失败(buildsvc=%s): %w", cfg.BuildsvcURL, err)
	}

	return &Runtime{ProductScreening: productScreening, Remote: remote}, nil
}

// trimToPayload 发送前把 A2A 消息裁成唯一一条结构化载荷 JSON：
// ADK 默认转发自上次远程响应以来的会话事件，本回调只保留 RequirementSpec/
// ChangeRequest。产品 API 的匿名 owner 是固定 256-bit base64url 值；对该入口把
// ContextID 固定为 Web session ID，使 buildsvc 落库版本和产品会话使用同一 session_id。
// 非产品入口的 user ID 保持 A2A 自动 contextID 行为。
func trimToPayload(ctx agent.Context, req *a2a.SendMessageRequest) (*session.Event, error) {
	if req == nil || req.Message == nil {
		return nil, nil
	}
	var b strings.Builder
	for _, p := range req.Message.Parts {
		b.WriteString(p.Text())
	}
	currentText := originalUserText(ctx)
	payload := preferCurrentPayload(currentText, b.String())
	if payload == nil {
		log.Printf("[host] A2A 出站:无结构化载荷,原样转发(初筛追问轮)")
		return nil, nil
	}
	if sanitized, changed := sanitizeInferredBrandPrefs(payload, currentText); changed {
		payload = sanitized
		log.Printf("[host] 初筛纠偏:移除用户未明确指定的 CPU/GPU 品牌偏好")
	}
	if isProductOwnerID(ctx.UserID()) {
		req.Message.ContextID = ctx.SessionID()
	}
	log.Printf("[host] A2A 出站:contextID=%s 载荷类型=%s", req.Message.ContextID, pipeline.PayloadKind(payload))
	req.Message.Parts = a2a.ContentParts{a2a.NewTextPart(string(payload))}
	return nil, nil
}

// preferCurrentPayload 避免 A2A 聚合消息中的旧 RequirementSpec 覆盖本次确认前编辑。
// 产品 API 的 Remote 调用会把已确认 JSON 作为当前 UserContent;当前输入仍是自然
// 语言时,回退到聚合消息中的结构化载荷兜底。
func preferCurrentPayload(currentText, aggregateText string) json.RawMessage {
	if payload := pipeline.ExtractPayload(currentText); payload != nil {
		return payload
	}
	return pipeline.ExtractPayload(aggregateText)
}

func isProductOwnerID(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == 32
}

// sanitizeInferredBrandPrefs 防止初筛模型把用途、静音或风格偏好误推成品牌偏好。
// ChangeRequest 的 target_hint 保持原样。
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
