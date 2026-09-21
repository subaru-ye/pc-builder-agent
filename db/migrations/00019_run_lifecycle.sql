-- +goose Up
-- F1 run 生命周期:用户可请求取消,超时行可被回收。
-- expires_at 在创建 run 时写入(started_at + 服务端 RunTimeout),历史行保持 NULL,
-- 回收谓词对 NULL 过期时间的旧行不生效(启动时 RecoverInterrupted 已统一处理)。
ALTER TABLE agent_runs
    ADD COLUMN cancel_requested_at TIMESTAMPTZ,
    ADD COLUMN expires_at TIMESTAMPTZ;
CREATE INDEX agent_runs_reclaim_idx
    ON agent_runs (session_id, expires_at)
    WHERE status = 'running';

-- +goose Down
DROP INDEX agent_runs_reclaim_idx;
ALTER TABLE agent_runs
    DROP COLUMN expires_at,
    DROP COLUMN cancel_requested_at;
