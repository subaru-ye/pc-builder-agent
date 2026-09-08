// 初筛阶段的 ADK 驱动:每条 screening 用例独立 InMemory 会话,收集最终文本后
// 将可见回复原文留存,由断言复用产品 API 的 JSON 提取口径。
package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/hostruntime"
	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"
)

type adkScreeningRunner struct {
	runner *runner.Runner
	mu     sync.Mutex
	seq    int
}

func newADKScreeningRunner(ctx context.Context, cfg modelprovider.Config) (*adkScreeningRunner, error) {
	screeningModel, err := modelprovider.NewChat(ctx, cfg, "eval-screening")
	if err != nil {
		return nil, fmt.Errorf("创建初筛模型失败: %w", err)
	}
	screeningAgent, err := pipeline.NewScreening(screeningModel)
	if err != nil {
		return nil, fmt.Errorf("装配初筛 Agent 失败: %w", err)
	}
	r, err := runner.New(runner.Config{
		AppName: hostruntime.AppName, Agent: screeningAgent,
		SessionService: session.InMemoryService(), AutoCreateSession: true,
	})
	if err != nil {
		return nil, fmt.Errorf("创建初筛 runner: %w", err)
	}
	return &adkScreeningRunner{runner: r}, nil
}

func (a *adkScreeningRunner) Run(ctx context.Context, input string) (string, error) {
	a.mu.Lock()
	a.seq++
	n := a.seq
	a.mu.Unlock()
	userID, sessionID := "eval-user", fmt.Sprintf("eval-screening-%d", n)
	text, err := collectEvents(a.runner.Run(ctx, userID, sessionID,
		genai.NewContentFromText(input, genai.RoleUser), agent.RunConfig{}))
	return text, err
}

func collectEvents(seq func(func(*session.Event, error) bool)) (string, error) {
	last := ""
	var runErr error
	seq(func(ev *session.Event, err error) bool {
		if err != nil {
			runErr = err
			return false
		}
		if ev == nil {
			return true
		}
		if ev.ErrorMessage != "" {
			runErr = fmt.Errorf("agent 执行失败:%s", ev.ErrorMessage)
			return false
		}
		if ev.Partial || ev.Content == nil {
			return true
		}
		var b strings.Builder
		for _, part := range ev.Content.Parts {
			if part != nil && !part.Thought && part.Text != "" {
				b.WriteString(part.Text)
			}
		}
		if text := b.String(); strings.TrimSpace(text) != "" {
			last = text
		}
		return true
	})
	return last, runErr
}
