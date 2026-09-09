// 初筛阶段的 ADK 驱动:每条 screening 用例独立 InMemory 会话,收集最终文本后
// 将可见回复原文留存,由断言复用产品 API 的 JSON 提取口径。
package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
	"github.com/subaru-ye/pc-builder-agent/internal/hostruntime"
	"github.com/subaru-ye/pc-builder-agent/internal/modelprovider"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type adkScreeningRunner struct {
	runner         *runner.Runner
	dialogueRunner *runner.Runner
	mu             sync.Mutex
	seq            int
}

func newADKScreeningRunner(ctx context.Context, cfg modelprovider.Config, decorators ...func(model.LLM) model.LLM) (*adkScreeningRunner, error) {
	screeningModel, err := modelprovider.NewChat(ctx, cfg, "eval-screening")
	if err != nil {
		return nil, fmt.Errorf("创建初筛模型失败: %w", err)
	}
	for _, decorate := range decorators {
		screeningModel = decorate(screeningModel)
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
	productAgent, err := pipeline.NewProductScreening(screeningModel)
	if err != nil {
		return nil, err
	}
	dialogue, err := runner.New(runner.Config{AppName: hostruntime.AppName, Agent: productAgent, SessionService: session.InMemoryService(), AutoCreateSession: true})
	if err != nil {
		return nil, err
	}
	return &adkScreeningRunner{runner: r, dialogueRunner: dialogue}, nil
}

// RunDialogue 使用真实前轮回复构建产品有界上下文，不把 fixture 期望或助手 JSON 当用户事实。
// 这里评估需求确认前的对话；不模拟 Builder、数据库持久化或浏览器交互。
func (a *adkScreeningRunner) RunDialogue(ctx context.Context, inputs []string) ([]evalsuite.ScreeningOutput, error) {
	a.mu.Lock()
	a.seq++
	n := a.seq
	a.mu.Unlock()
	var history []store.WebMessage
	var outputs []evalsuite.ScreeningOutput
	for _, text := range inputs {
		input := product.BuildScreenInput(history, text)
		output := evalsuite.ScreeningOutput{Context: input.Context, UserSources: input.UserSources}
		turnCtx := pipeline.WithScreeningSources(ctx, input.UserSources)
		turnCtx = pipeline.WithScreeningBuildState(turnCtx, false)
		turnCtx = pipeline.WithScreeningObserver(turnCtx, func(raw string, missing []string) {
			output.ModelAttempts = append(output.ModelAttempts, raw)
			output.ModelText = raw
			output.MissingFields = append([]string(nil), missing...)
		})
		guardText, err := collectEvents(a.dialogueRunner.Run(turnCtx, "eval-user", fmt.Sprintf("eval-dialogue-%d", n), genai.NewContentFromText(input.Context, genai.RoleUser), agent.RunConfig{}))
		output.Text = guardText
		if err != nil {
			outputs = append(outputs, output)
			return outputs, err
		}
		parsed, err := product.ParseScreeningResult(guardText, text)
		if err != nil {
			outputs = append(outputs, output)
			return outputs, err
		}
		assistant := parsed.Text
		if parsed.Kind == product.ScreenRequirement {
			output.GuardText = guardText
			output.Text = string(parsed.Payload)
			assistant = product.ScreeningReadyMessage
		}
		history = append(history, store.WebMessage{Role: "user", Content: text}, store.WebMessage{Role: "assistant", Content: assistant})
		outputs = append(outputs, output)
	}
	return outputs, nil
}

func (a *adkScreeningRunner) Run(ctx context.Context, input string) (string, error) {
	output, err := a.RunDetailed(ctx, input)
	return output.Text, err
}

func (a *adkScreeningRunner) RunDetailed(ctx context.Context, input string) (evalsuite.ScreeningOutput, error) {
	var output evalsuite.ScreeningOutput
	ctx = pipeline.WithScreeningBuildState(ctx, false)
	ctx = pipeline.WithScreeningObserver(ctx, func(raw string, missing []string) {
		output.ModelAttempts = append(output.ModelAttempts, raw)
		output.ModelText = raw
		output.MissingFields = append([]string(nil), missing...)
	})
	a.mu.Lock()
	a.seq++
	n := a.seq
	a.mu.Unlock()
	userID, sessionID := "eval-user", fmt.Sprintf("eval-screening-%d", n)
	text, err := collectEvents(a.runner.Run(ctx, userID, sessionID,
		genai.NewContentFromText(input, genai.RoleUser), agent.RunConfig{}))
	output.Text = text
	return output, err
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
