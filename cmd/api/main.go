// P7 产品 API:匿名会话、需求确认、后台 run、SSE 与配置读模型。
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/hostruntime"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/producthttp"
	"github.com/subaru-ye/pc-builder-agent/internal/redisstore"
	"github.com/subaru-ye/pc-builder-agent/internal/runevents"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

const defaultAPIAddr = ":8082"

func main() {
	dotenv.Load(".env")
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		log.Fatal("PG_DSN 未设置:产品 API 需要 PostgreSQL")
	}
	st, err := store.New(rootCtx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	backend := redisstore.Open(rootCtx)
	defer func() { _ = backend.Close() }()
	sessions := backend.SessionService()

	runtimeCfg, err := hostruntime.ConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	runtime, err := hostruntime.New(rootCtx, runtimeCfg)
	if err != nil {
		log.Fatal(err)
	}
	agentGateway, err := product.NewADKAgentGateway(runtime, sessions, backend.Available())
	if err != nil {
		log.Fatal(err)
	}
	eventTTL := durationEnv("RUN_EVENT_TTL", 24*time.Hour)
	events := runevents.New(backend.Client(), eventTTL)
	service, err := product.NewService(rootCtx, st, agentGateway, events)
	if err != nil {
		log.Fatal(err)
	}
	if err := service.RecoverInterrupted(rootCtx); err != nil {
		log.Fatalf("恢复遗留运行失败:%v", err)
	}

	httpAPI, err := producthttp.New(service, events, st, backend, producthttp.Config{
		PublicWebBaseURL: envOr("PUBLIC_WEB_BASE_URL", "http://localhost:3000"),
		AllowedOrigin:    os.Getenv("WEB_ALLOWED_ORIGIN"),
		BuildsvcURL:      runtimeCfg.BuildsvcURL,
	})
	if err != nil {
		log.Fatal(err)
	}
	addr := envOr("API_ADDR", defaultAPIAddr)
	server := &http.Server{
		Addr: addr, Handler: httpAPI.Handler(),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 2 * time.Minute,
		MaxHeaderBytes: 1 << 20,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("[api] 产品 API 启动:监听 %s(redis_degraded=%v)", addr, events.Degraded())
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-rootCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		if err := service.Shutdown(shutdownCtx); err != nil {
			log.Printf("[api] 后台运行关闭未完整完成:%v", err)
		}
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[api] HTTP 服务退出:%v", err)
		}
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		log.Printf("[api] %s=%q 无效,使用默认 %s", key, value, fallback)
		return fallback
	}
	return d
}
