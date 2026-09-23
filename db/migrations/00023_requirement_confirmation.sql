-- +goose Up
-- 确认快照与 Builder 载荷冻结(requirement-confirmation-builder-gate-v2):
-- 快照表 insert-only 不可变,再次确认产生新行;build run 永久记录其快照与仅
-- run 标识不同的完整执行载荷及 hash。web_sessions 旧 confirmed_* 列删除,
-- 当前确认由 confirmation_id 指针表达,不保留双轨。
CREATE TABLE requirement_confirmations (
    id UUID PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES web_sessions(id),
    requirement_spec JSONB NOT NULL,
    requirement_state JSONB NOT NULL,
    review_hash TEXT NOT NULL,
    revision INT NOT NULL,
    configuration_scope JSONB NOT NULL,
    schema_version INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX requirement_confirmations_session_idx
    ON requirement_confirmations(session_id, created_at DESC);

ALTER TABLE agent_runs
    ADD COLUMN request_fingerprint TEXT,
    ADD COLUMN confirmation_id UUID REFERENCES requirement_confirmations(id),
    ADD COLUMN builder_input_payload JSONB,
    ADD COLUMN builder_input_hash TEXT,
    ADD COLUMN retry_of_run_id UUID;

ALTER TABLE web_sessions ADD COLUMN confirmation_id UUID REFERENCES requirement_confirmations(id);

-- 侧栏草稿编辑的幂等记录:独立原子编辑事务使用,同一会话同一键只允许同一请求。
CREATE TABLE requirement_edit_requests (
    session_id TEXT NOT NULL REFERENCES web_sessions(id),
    request_id TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, request_id)
);

ALTER TABLE web_sessions
    DROP COLUMN confirmed_requirement,
    DROP COLUMN confirmed_requirement_state,
    DROP COLUMN confirmed_at;

-- +goose Down
ALTER TABLE web_sessions
    ADD COLUMN confirmed_requirement JSONB,
    ADD COLUMN confirmed_requirement_state JSONB,
    ADD COLUMN confirmed_at TIMESTAMPTZ;
DROP TABLE requirement_edit_requests;
ALTER TABLE web_sessions DROP COLUMN confirmation_id;
ALTER TABLE agent_runs
    DROP COLUMN retry_of_run_id,
    DROP COLUMN builder_input_hash,
    DROP COLUMN builder_input_payload,
    DROP COLUMN confirmation_id,
    DROP COLUMN request_fingerprint;
DROP INDEX requirement_confirmations_session_idx;
DROP TABLE requirement_confirmations;
