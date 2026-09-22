// P5 buildsvc:装机「生成 + 校验」流水线的 A2A 远程服务(独立进程,服务与状态.md)。
// 持有生成旗舰档模型 + 零件库 Store + 查询向量化;承载重试回路(Loop 留在服务进程内)。
// 运行方式(从仓库根,需先起 docker-compose 的 postgres 并导入零件/价格数据):
//
//	go run ./cmd/buildsvc          # 监听 BUILDSVC_ADDR(默认 :8081),先于 cmd/host 启动
//
// 诚实标注(指引 §八.5):单跳拆分是 A2A 学习目的,非性能需要;多 Agent token 开销数倍。
package main

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	adka2a "google.golang.org/adk/v2/server/adka2a/v2"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"
	"github.com/subaru-ye/pc-builder-agent/internal/planning"
	"github.com/subaru-ye/pc-builder-agent/internal/redisstore"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// A2A invoke 路径与默认监听地址(agent card 与 host 的 BUILDSVC_URL 对齐)。
const (
	invokePath  = "/a2a/v1/invoke"
	defaultAddr = ":8081"
)

func main() {
	ctx := context.Background()

	dotenv.Load(".env")

	builderCfg, err := modelprovider.Load(modelprovider.RoleBuilder)
	if err != nil {
		log.Fatal(err)
	}
	embeddingCfg, err := modelprovider.Load(modelprovider.RoleEmbedding)
	if err != nil {
		log.Fatal(err)
	}
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		log.Fatal("PG_DSN 未设置:生成服务需要零件库(docker-compose up -d 后见 .env.example)")
	}
	addr := os.Getenv("BUILDSVC_ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	harnessMode, err := buildharness.ParseMode(os.Getenv("BUILD_HARNESS_MODE"))
	if err != nil {
		log.Fatal(err)
	}

	st, err := store.New(ctx, dsn)
	if err != nil {
		log.Fatalf("连接零件库失败: %v", err)
	}
	defer st.Close()

	builderModel, err := modelprovider.NewChat(ctx, builderCfg, "buildsvc")
	if err != nil {
		log.Fatalf("创建生成模型失败: %v", err)
	}

	// P6:会话热上下文 + embedding 缓存统一过 Redis(无 REDIS_ADDR 时降级,见 redisstore.Open)。
	backend := redisstore.Open(ctx)
	defer func() { _ = backend.Close() }()

	// P3 语义检索的查询向量化客户端(与 cmd/embedparts 同模型/同维度),外包一层 Redis 缓存。
	embeddingClient, err := modelprovider.NewEmbedding(embeddingCfg)
	if err != nil {
		log.Fatalf("创建 embedding 客户端失败: %v", err)
	}
	embedder := backend.WrapEmbedder(embeddingClient, embeddingCfg.CacheIdentity(),
		string(embeddingCfg.Provider), embeddingCfg.Model)

	pipelineConfig := pipeline.Config{
		BuilderModel:    builderModel,
		Store:           st,
		QueryEmbedder:   embedder,
		BuilderIdentity: &planning.BuilderIdentity{Provider: string(builderCfg.Provider), Role: string(builderCfg.Role), Model: builderCfg.Model},
		TokenBudget:     envInt("RUN_TOKEN_BUDGET", 0),
	}
	var root agent.Agent
	switch harnessMode {
	case buildharness.ModePlanning:
		root, err = pipeline.NewRemotePlanning(pipelineConfig)
	case buildharness.ModeV2:
		root, err = pipeline.NewRemoteV2(pipelineConfig)
	default:
		root, err = pipeline.NewRemote(pipelineConfig)
	}
	if err != nil {
		log.Fatalf("装配远程服务流水线失败: %v", err)
	}

	// agent card 对外声明 invoke 端点(host 从 well-known 路径解析);
	// 本地开发用 localhost + 监听端口,与 host 的 BUILDSVC_URL 默认值对齐。
	cardURL := publicBaseURL(addr)
	agentCard := &a2a.AgentCard{
		Name:        root.Name(),
		Description: root.Description(),
		SupportedInterfaces: []*a2a.AgentInterface{
			{
				URL:             cardURL.JoinPath(invokePath).String(),
				ProtocolBinding: a2a.TransportProtocolJSONRPC,
				ProtocolVersion: a2a.Version,
			},
		},
		Version:            "1.0.0",
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills:             adka2a.BuildAgentSkills(root),
		Capabilities:       a2a.AgentCapabilities{Streaming: true},
	}

	// A2A 执行器:把根 agent 包成远程执行器。会话按 contextID 映射到 Redis 会话服务
	// (与 host 共享同一 Redis),让 build_state 跨轮持久、kill host 重启后能按同一 contextID 续上。
	executor := adka2a.NewExecutor(adka2a.ExecutorConfig{
		RunnerConfig: runner.Config{
			AppName:        root.Name(),
			Agent:          root,
			SessionService: backend.SessionService(),
		},
	})

	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(agentCard))
	mux.Handle(invokePath, a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor)))

	log.Printf("[buildsvc] A2A 服务启动:监听 %s,card=%s,invoke=%s,harness=%s,builder=%s/%s,embedding=%s/%s,run_token_budget=%d",
		addr, cardURL.JoinPath(a2asrv.WellKnownAgentCardPath).String(), cardURL.JoinPath(invokePath).String(),
		harnessMode, builderCfg.Provider, builderCfg.Model, embeddingCfg.Provider, embeddingCfg.Model, pipelineConfig.TokenBudget)
	if harnessMode != buildharness.ModePlanning {
		log.Printf("[buildsvc] 注意:harness=%s 仅供 dev UI 与评估;产品 HTTP 入口仅支持 planning", harnessMode)
	}
	if err := http.ListenAndServe(addr, logMiddleware(mux)); err != nil {
		log.Fatalf("[buildsvc] A2A 服务退出: %v", err)
	}
}

// envInt 读取非负整数环境变量;缺失或非法返回 fallback(F7 预算,0 = 不限)。
func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
		return n
	}
	return fallback
}

// publicBaseURL 由监听地址推出对外基址(":8081" → http://localhost:8081)。
func publicBaseURL(addr string) *url.URL {
	host := addr
	if strings.HasPrefix(addr, ":") {
		host = "localhost" + addr
	}
	return &url.URL{Scheme: "http", Host: host}
}

// logMiddleware 记录每个入站 HTTP 请求(满足用例 G「日志可见跨进程 A2A 消息」的传输层;
// 载荷类型与 schema 校验由 ingest 节点补充记录)。
func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[buildsvc] 入站 %s %s from=%s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}
