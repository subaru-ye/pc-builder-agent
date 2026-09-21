-- +goose Up
-- F3 可观测性:run 行持久模型身份与计量,版本行关联 run 与目录快照;
-- 终态事件镜像进 PG,让 Redis 事件(24h TTL)过期后仍可重建时间线。
-- 指标口径沿用 planning.Result 既有字段;0 值写 NULL,区分"未知"与"零"。
ALTER TABLE agent_runs
    ADD COLUMN screening_model TEXT,
    ADD COLUMN builder_model TEXT,
    ADD COLUMN model_calls INT,
    ADD COLUMN tool_calls INT,
    ADD COLUMN search_calls INT,
    ADD COLUMN page_calls INT,
    ADD COLUMN tokens INT,
    ADD COLUMN duration_ms BIGINT,
    ADD COLUMN catalog_snapshot_id BIGINT,
    ADD COLUMN retry_count INT;

ALTER TABLE builds
    ADD COLUMN run_id UUID,
    ADD COLUMN catalog_snapshot_id BIGINT;
CREATE INDEX builds_run_idx ON builds (run_id);

CREATE TABLE run_events (
    id         BIGSERIAL PRIMARY KEY,
    run_id     UUID NOT NULL,
    event      TEXT NOT NULL,
    payload    JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX run_events_run_idx ON run_events (run_id, id);

-- +goose Down
DROP TABLE run_events;
DROP INDEX builds_run_idx;
ALTER TABLE builds
    DROP COLUMN catalog_snapshot_id,
    DROP COLUMN run_id;
ALTER TABLE agent_runs
    DROP COLUMN retry_count,
    DROP COLUMN catalog_snapshot_id,
    DROP COLUMN duration_ms,
    DROP COLUMN tokens,
    DROP COLUMN page_calls,
    DROP COLUMN search_calls,
    DROP COLUMN tool_calls,
    DROP COLUMN model_calls,
    DROP COLUMN builder_model,
    DROP COLUMN screening_model;
