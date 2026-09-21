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

// TransportDegrade 返回跨进程传输副本：证据正文与字段引文降为预览。
// finalize 判定与报价核验已在 Run 内用全文完成，降级不影响结论；
// 完整产物由 ArchiveAndDegrade 按 run 归档，供下一轮补全与诊断。
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

// ArchiveAndDegrade 先归档完整产物再返回降级传输副本。归档失败只降不挡：
// 交付不因旁路归档故障失败，由调用方记录错误。
func ArchiveAndDegrade(ctx context.Context, st ArtifactStore, runID, sessionID string, result Result) (Result, error) {
	var err error
	if st != nil && runID != "" {
		raw, e := json.Marshal(result)
		if e != nil {
			err = e
		} else {
			err = st.SavePlanningArtifact(ctx, store.SavePlanningArtifactParams{RunID: runID, SessionID: sessionID, Payload: raw})
		}
	}
	return TransportDegrade(result), err
}
