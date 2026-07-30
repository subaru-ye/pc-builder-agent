// P5 buildsvc:装机「生成 + 校验」流水线的 A2A 远程服务(独立进程,P5-A2A单跳拆分设计.md)。
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
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	adka2a "google.golang.org/adk/v2/server/adka2a/v2"

	"google.golang.org/adk/v2/model/openaimodel"
	"google.golang.org/adk/v2/runner"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/embedding"
	"github.com/subaru-ye/pc-builder-agent/internal/redisstore"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// 模型分档(ADR-004:型号只写代码常量):生成旗舰档,型号迁到本服务进程。
const builderModelName = "qwen3.7-max"

// 百炼 OpenAI 兼容端点(公共默认);工作空间专属 Host 用 DASHSCOPE_BASE_URL 覆盖。
const defaultBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"

// A2A invoke 路径与默认监听地址(agent card 与 host 的 BUILDSVC_URL 对齐)。
const (
	invokePath  = "/a2a/v1/invoke"
	defaultAddr = ":8081"
)

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
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		log.Fatal("PG_DSN 未设置:生成服务需要零件库(docker-compose up -d 后见 .env.example)")
	}
	addr := os.Getenv("BUILDSVC_ADDR")
	if addr == "" {
		addr = defaultAddr
	}

	st, err := store.New(ctx, dsn)
	if err != nil {
		log.Fatalf("连接零件库失败: %v", err)
	}
	defer st.Close()

	clientCfg := &openaimodel.ClientConfig{APIKey: apiKey, BaseURL: baseURL}
	builderModel, err := openaimodel.NewModel(ctx, builderModelName, clientCfg)
	if err != nil {
		log.Fatalf("创建生成模型失败: %v", err)
	}

	// P6:会话热上下文 + embedding 缓存统一过 Redis(无 REDIS_ADDR 时降级,见 redisstore.Open)。
	backend := redisstore.Open(ctx)
	defer func() { _ = backend.Close() }()

	// P3 语义检索的查询向量化客户端(与 cmd/embedparts 同模型/同维度),外包一层 Redis 缓存。
	embedder := backend.WrapEmbedder(
		embedding.NewClient(baseURL, apiKey, embedding.DefaultModel, store.EmbeddingDims),
		embedding.DefaultModel,
	)

	root, err := pipeline.NewRemote(pipeline.Config{
		BuilderModel:  builderModel,
		Store:         st,
		QueryEmbedder: embedder,
	})
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

	log.Printf("[buildsvc] A2A 服务启动:监听 %s,card=%s,invoke=%s",
		addr, cardURL.JoinPath(a2asrv.WellKnownAgentCardPath).String(), cardURL.JoinPath(invokePath).String())
	if err := http.ListenAndServe(addr, logMiddleware(mux)); err != nil {
		log.Fatalf("[buildsvc] A2A 服务退出: %v", err)
	}
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
