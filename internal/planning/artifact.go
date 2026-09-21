package planning

import (
	"context"
	"encoding/json"

	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// ArtifactStore 是生成侧归档完整产物的最小接口（buildsvc 与产品共享同一 PG）。
type ArtifactStore interface {
	SavePlanningArtifact(context.Context, store.SavePlanningArtifactParams) error
}

// ArtifactReader 供 Runner 从归档补全上一轮降级副本的正文。
type ArtifactReader interface {
	PlanningArtifact(context.Context, string) (json.RawMessage, error)
}

// ControlPlane 返回跨进程传输副本:只留控制面字段(F2 第二步)。
// 候选、证据、评估等重载荷不进 A2A;产品侧按 run_id 从 planning_artifacts 读回。
func (result Result) ControlPlane() Result {
	out := result
	out.Candidates = nil
	out.Evidence = nil
	out.Assessments = nil
	out.Assumptions = nil
	out.ReadAttempts = nil
	return out
}

// TransportDegrade 返回跨进程传输副本：证据正文与字段引文降为预览。
// finalize 判定与报价核验已在 Run 内用全文完成，降级不影响结论；
// 完整产物由 Archive 按 run 归档，供下一轮补全、产品合并与诊断。
func TransportDegrade(result Result) Result {
	for i := range result.Evidence {
		result.Evidence[i] = evidencePreview(result.Evidence[i])
	}
	for i := range result.Candidates {
		for field, quote := range result.Candidates[i].FieldQuotes {
			q := []rune(quote)
			if len(q) > 240 {
				result.Candidates[i].FieldQuotes[field] = string(q[:240]) + " [引文未完整展示]"
			}
		}
	}
	return result
}

// Archive 按 run 归档完整产物。归档失败不阻塞交付,仅返回错误由调用方记录;
// 跨进程传输副本由调用方基于 ControlPlane/TransportDegrade 构造。
func Archive(ctx context.Context, st ArtifactStore, runID, sessionID string, result Result) error {
	if st == nil || runID == "" {
		return nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return st.SavePlanningArtifact(ctx, store.SavePlanningArtifactParams{RunID: runID, SessionID: sessionID, Payload: raw})
}
