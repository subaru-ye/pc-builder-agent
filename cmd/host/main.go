// P5 host:装机初筛(近端)+ A2A 远程消费方。共享装配位于 internal/hostruntime，
// cmd/host 只保留 ADK launcher 入口(服务与状态.md)。
// 运行方式(从仓库根;需先 `go run ./cmd/buildsvc` 起远程服务):
//
//	go run ./cmd/host                # console 模式,快速验证全链路
//	go run ./cmd/host web --write-timeout=10m api --sse-write-timeout=10m webui  # Web UI,http://localhost:8080/ui/
package main

import (
	"context"
	"log"
	"os"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/cmd/launcher"
	"google.golang.org/adk/v2/cmd/launcher/full"

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/hostruntime"
	"github.com/subaru-ye/pc-builder-agent/internal/redisstore"
)

func main() {
	ctx := context.Background()
	dotenv.Load(".env")

	cfg, err := hostruntime.ConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	runtime, err := hostruntime.New(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("[host] 模型配置 provider=%s role=%s model=%s reasoning=%s retries=%d cache=%v",
		cfg.Screening.Provider, cfg.Screening.Role, cfg.Screening.Model,
		cfg.Screening.ReasoningEffort, cfg.Screening.MaxRetries, cfg.Screening.SessionCache)

	// P6:会话热上下文迁 Redis(带 TTL、两进程共享),让 kill host 重启后同一会话可续;
	// 无 REDIS_ADDR 时降级回退进程内 InMemory(见 redisstore.Open)。
	backend := redisstore.Open(ctx)
	defer func() { _ = backend.Close() }()

	log.Printf("[host] 已接线 A2A 远程服务:%s", cfg.BuildsvcURL)
	l := full.NewLauncher()
	config := &launcher.Config{
		AgentLoader:    agent.NewSingleLoader(runtime.Root),
		SessionService: backend.SessionService(),
	}
	if err := l.Execute(ctx, config, os.Args[1:]); err != nil {
		log.Fatalf("运行失败: %v\n\n%s", err, l.CommandLineSyntax())
	}
}
