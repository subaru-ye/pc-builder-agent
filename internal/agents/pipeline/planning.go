package pipeline

import (
	"encoding/json"
	"fmt"
	"iter"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/subaru-ye/pc-builder-agent/internal/planning"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"google.golang.org/adk/v2/agent"
	adka2a "google.golang.org/adk/v2/server/adka2a/v2"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// NewRemotePlanning keeps the tool loop in buildsvc and returns a typed outcome.
// The product transaction owns persistence; A2A context IDs are never DB owners.
func NewRemotePlanning(cfg Config) (agent.Agent, error) {
	if cfg.BuilderModel == nil || cfg.Store == nil {
		return nil, fmt.Errorf("planning requires model and store")
	}
	r := planning.Runner{Model: cfg.BuilderModel, Embedder: cfg.QueryEmbedder, Catalog: cfg.Store, Web: planning.NewWeb(cfg.Store)}
	return agent.New(agent.Config{Name: "pc_build_service", Description: "自主检索、规划与核验装机方案", Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
		return func(yield func(*session.Event, error) bool) {
			var input schemas.PlanningInput
			if e := json.Unmarshal([]byte(latestUserText(ctx)), &input); e != nil || input.SchemaVersion != 2 {
				yield(nil, fmt.Errorf("planning requires version 2 state snapshot"))
				return
			}
			result, e := r.Run(ctx, input)
			if e != nil {
				// 客户端取消/断连不是生成服务故障:保留已完成候选与草稿供后续
				// 轮次恢复,不得覆盖为 technical_fault(§F1:取消 ≠ 失败)。
				result.Outcome = "interrupted"
				result.Delivery = &planning.Delivery{Status: "not_applicable", Issues: []string{"本轮已取消，尚未完成交付核验"}}
				result.Reply = "本轮生成已取消，已保存已有候选和资料。可以继续对话或重新确认后重试。"
				result.Issues = append(result.Issues, "本轮已取消，进度已保留")
			}
			part, e := PlanningResultPart(result)
			if e != nil {
				yield(nil, e)
				return
			}
			content := genai.NewContentFromText(result.Reply, genai.RoleModel)
			content.Parts = append(content.Parts, part)
			event := &session.Event{Author: "pc_build_service"}
			event.Content = content
			yield(event, nil)
		}
	}})
}

func PlanningResultPart(result planning.Result) (*genai.Part, error) {
	raw, _ := json.Marshal(result)
	var data map[string]any
	_ = json.Unmarshal(raw, &data)
	parts, e := adka2a.ToGenAIParts([]*a2a.Part{a2a.NewDataPart(map[string]any{"type": "pc_builder.planning_result", "schema_version": 1, "result": data})})
	if e != nil {
		return nil, e
	}
	return parts[0], nil
}

func ReadPlanningResult(part *genai.Part) *planning.Result {
	if part == nil || part.InlineData == nil || len(part.InlineData.Data) > 2*1024*1024 {
		return nil
	}
	converted, e := adka2a.ToA2APart(part, nil)
	if e != nil || converted.Data() == nil {
		return nil
	}
	raw, _ := json.Marshal(converted.Data())
	var envelope struct {
		Type    string          `json:"type"`
		Version int             `json:"schema_version"`
		Result  planning.Result `json:"result"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Type != "pc_builder.planning_result" || envelope.Version != 1 {
		return nil
	}
	return &envelope.Result
}
