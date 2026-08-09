package product

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	adka2a "google.golang.org/adk/v2/server/adka2a/v2"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/hostruntime"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

type ScreenKind string

const (
	ScreenQuestion    ScreenKind = "question"
	ScreenRequirement ScreenKind = "requirement"
	ScreenChange      ScreenKind = "change"
	ScreenInvalid     ScreenKind = "invalid"
)

type ScreenResult struct {
	Kind    ScreenKind
	Text    string
	Payload json.RawMessage
	Err     error
}

type RemoteResult struct {
	Text string
}

type AgentGateway interface {
	Screen(context.Context, string, string, string) (ScreenResult, error)
	Remote(context.Context, string, string, json.RawMessage) (RemoteResult, error)
	ContextAvailable(context.Context, string, string) (bool, error)
}

type ADKAgentGateway struct {
	screeningRunner *runner.Runner
	remoteRunner    *runner.Runner
	sessions        session.Service
	remoteName      string
	redisBacked     bool
}

func NewADKAgentGateway(runtime *hostruntime.Runtime, sessions session.Service, redisBacked bool) (*ADKAgentGateway, error) {
	if runtime == nil || runtime.Screening == nil || runtime.Remote == nil || sessions == nil {
		return nil, fmt.Errorf("product agent: runtime/session service 不能为空")
	}
	screeningRunner, err := runner.New(runner.Config{
		AppName: hostruntime.AppName, Agent: runtime.Screening,
		SessionService: sessions, AutoCreateSession: true,
	})
	if err != nil {
		return nil, fmt.Errorf("product agent: 创建 screening runner: %w", err)
	}
	remoteRunner, err := runner.New(runner.Config{
		AppName: hostruntime.AppName, Agent: runtime.Remote,
		SessionService: sessions, AutoCreateSession: true,
	})
	if err != nil {
		return nil, fmt.Errorf("product agent: 创建 remote runner: %w", err)
	}
	return &ADKAgentGateway{
		screeningRunner: screeningRunner,
		remoteRunner:    remoteRunner,
		sessions:        sessions,
		remoteName:      runtime.Remote.Name(),
		redisBacked:     redisBacked,
	}, nil
}

func (g *ADKAgentGateway) Screen(ctx context.Context, userID, sessionID, text string) (ScreenResult, error) {
	lastText, err := collectAgentText(g.screeningRunner.Run(ctx, userID, sessionID,
		genai.NewContentFromText(text, genai.RoleUser), agent.RunConfig{}), "")
	if err != nil {
		return ScreenResult{}, err
	}
	payload := pipeline.ExtractPayload(lastText)
	if payload == nil {
		return ScreenResult{Kind: ScreenQuestion, Text: strings.TrimSpace(lastText)}, nil
	}
	if _, err := schemas.DecodeRequirementSpec(payload); err == nil {
		return ScreenResult{Kind: ScreenRequirement, Text: lastText, Payload: payload}, nil
	}
	if _, err := schemas.DecodeChangeRequest(payload); err == nil {
		return ScreenResult{Kind: ScreenChange, Text: lastText, Payload: payload}, nil
	}
	return ScreenResult{
		Kind: ScreenInvalid, Text: lastText, Payload: payload,
		Err: fmt.Errorf("初筛输出不符合 RequirementSpec/ChangeRequest schema"),
	}, nil
}

func (g *ADKAgentGateway) Remote(ctx context.Context, userID, sessionID string, payload json.RawMessage) (RemoteResult, error) {
	lastText, err := collectAgentText(g.remoteRunner.Run(ctx, userID, sessionID,
		genai.NewContentFromText(string(payload), genai.RoleUser), agent.RunConfig{}), g.remoteName)
	if err != nil {
		return RemoteResult{}, err
	}
	return RemoteResult{Text: strings.TrimSpace(lastText)}, nil
}

// ContextAvailable 校验 host 侧 remote event；Redis 可用时继续核对 buildsvc 的 context session。
func (g *ADKAgentGateway) ContextAvailable(ctx context.Context, userID, sessionID string) (bool, error) {
	resp, err := g.sessions.Get(ctx, &session.GetRequest{
		AppName: hostruntime.AppName, UserID: userID, SessionID: sessionID,
	})
	if err != nil || resp == nil || resp.Session == nil {
		return false, nil
	}
	contextID := ""
	events := resp.Session.Events()
	for i := events.Len() - 1; i >= 0; i-- {
		ev := events.At(i)
		if ev == nil || ev.Author != g.remoteName {
			continue
		}
		_, contextID = adka2a.GetA2ATaskInfo(ev)
		if contextID != "" {
			break
		}
	}
	if contextID == "" {
		return false, nil
	}
	if !g.redisBacked {
		return true, nil
	}
	remoteResp, err := g.sessions.Get(ctx, &session.GetRequest{
		AppName: "pc_build_service", UserID: "A2A_USER_" + contextID, SessionID: contextID,
	})
	return err == nil && remoteResp != nil && remoteResp.Session != nil, nil
}

func collectAgentText(seq func(func(*session.Event, error) bool), author string) (string, error) {
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
			runErr = fmt.Errorf("Agent 执行失败:%s", ev.ErrorMessage)
			return false
		}
		if ev.Partial || ev.Content == nil || (author != "" && ev.Author != author) {
			return true
		}
		var b strings.Builder
		for _, part := range ev.Content.Parts {
			if part != nil && !part.Thought && part.Text != "" {
				b.WriteString(part.Text)
			}
		}
		if text := strings.TrimSpace(b.String()); text != "" {
			last = text
		}
		return true
	})
	if runErr != nil {
		return "", runErr
	}
	if last == "" {
		return "", fmt.Errorf("Agent 未返回面向用户的文本")
	}
	return last, nil
}
