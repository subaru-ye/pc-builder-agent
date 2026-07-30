// P2 流水线 host:初筛 → 生成 → 校验三 Agent 流水线 + 内置 launcher(dev UI)。
// 运行方式(从仓库根,需先起 docker-compose 的 postgres 并导入零件/价格数据):
//
//	go run ./cmd/host                # console 模式,快速验证全链路
//	go run ./cmd/host web api webui  # Web UI(三个子命令缺一不可),http://localhost:8080
package main

import (
	"context"
	"log"
	"os"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/cmd/launcher"
	"google.golang.org/adk/v2/cmd/launcher/full"
	"google.golang.org/adk/v2/model/openaimodel"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// 模型分档(P2 流水线设计 §5 成本三件套):初筛低价档、生成旗舰档。
// 型号以本常量为准,不写进文档(技术选型 ADR-004)。
const (
	screeningModelName = "qwen-flash"
	builderModelName   = "qwen3.7-max"
)

// 百炼 OpenAI 兼容端点(公共默认);工作空间专属 Host 用 DASHSCOPE_BASE_URL 覆盖。
// ADK 的 openaimodel 走 Responses API,百炼已支持。
const defaultBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"

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
		log.Fatal("PG_DSN 未设置:流水线需要零件库(docker-compose up -d 后见 .env.example)")
	}

	st, err := store.New(ctx, dsn)
	if err != nil {
		log.Fatalf("连接零件库失败: %v", err)
	}
	defer st.Close()

	clientCfg := &openaimodel.ClientConfig{APIKey: apiKey, BaseURL: baseURL}
	screeningModel, err := openaimodel.NewModel(ctx, screeningModelName, clientCfg)
	if err != nil {
		log.Fatalf("创建初筛模型失败: %v", err)
	}
	builderModel, err := openaimodel.NewModel(ctx, builderModelName, clientCfg)
	if err != nil {
		log.Fatalf("创建生成模型失败: %v", err)
	}

	root, err := pipeline.New(pipeline.Config{
		ScreeningModel: screeningModel,
		BuilderModel:   builderModel,
		Store:          st,
	})
	if err != nil {
		log.Fatalf("装配流水线失败: %v", err)
	}

	l := full.NewLauncher()
	config := &launcher.Config{AgentLoader: agent.NewSingleLoader(root)}
	if err := l.Execute(ctx, config, os.Args[1:]); err != nil {
		log.Fatalf("运行失败: %v\n\n%s", err, l.CommandLineSyntax())
	}
}
