package store

import (
	"context"
	"encoding/json"
)

// AppendRunEvent 把终态事件镜像进 PG。SSE 热路径仍走 Redis(24h TTL);
// 该镜像让过期之后仍可重建 run 时间线(仅四类终态事件)。
func (s *Store) AppendRunEvent(ctx context.Context, runID, event string, payload json.RawMessage) error {
	_, e := s.pool.Exec(ctx, `INSERT INTO run_events(run_id,event,payload) VALUES($1,$2,$3)`, runID, event, payload)
	return e
}

// RunObservability 汇总单个 run 的模型身份、计量与镜像状态;NULL 计量归零(未知 ≠ 零由列本身区分)。
type RunObservability struct {
	BuilderModel     string
	ModelCalls       int
	ToolCalls        int
	SearchCalls      int
	PageCalls        int
	Tokens           int
	DurationMS       int64
	CatalogSnapshotI int64
	BuildsLinked     int
	MirroredEvents   int
}

func (s *Store) RunObservability(ctx context.Context, runID string) (RunObservability, error) {
	var o RunObservability
	e := s.pool.QueryRow(ctx, `SELECT COALESCE(builder_model,''), COALESCE(model_calls,0), COALESCE(tool_calls,0),
		COALESCE(search_calls,0), COALESCE(page_calls,0), COALESCE(tokens,0), COALESCE(duration_ms,0), COALESCE(catalog_snapshot_id,0),
		(SELECT count(*) FROM builds WHERE run_id = r.id AND catalog_snapshot_id > 0),
		(SELECT count(*) FROM run_events WHERE run_id = r.id AND event IN ('run.completed','run.failed','build.saved','requirement.ready'))
		FROM agent_runs r WHERE id = $1`, runID).Scan(
		&o.BuilderModel, &o.ModelCalls, &o.ToolCalls, &o.SearchCalls, &o.PageCalls,
		&o.Tokens, &o.DurationMS, &o.CatalogSnapshotI, &o.BuildsLinked, &o.MirroredEvents)
	return o, e
}
