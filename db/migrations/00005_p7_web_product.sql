-- +goose Up
-- P7 产品 API:匿名 Web 会话、稳定消息、后台 run 与 P9 分享表预建。
-- 旧 dev UI builds 不强制补 web_sessions；Web 所有权查询先校验本表再读取 builds。
CREATE TABLE web_sessions (
    id                  TEXT PRIMARY KEY,
    owner_id            TEXT NOT NULL,
    create_request_id   UUID NOT NULL,
    title               TEXT NOT NULL DEFAULT '新会话',
    phase               TEXT NOT NULL CHECK (phase IN
        ('collecting', 'requirement_ready', 'building', 'ready', 'changing', 'error')),
    recovery_phase      TEXT CHECK (recovery_phase IN
        ('collecting', 'requirement_ready', 'ready')),
    pending_requirement JSONB,
    last_error          JSONB,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_id, create_request_id)
);
CREATE INDEX web_sessions_owner_updated_idx ON web_sessions (owner_id, updated_at DESC);

CREATE TABLE agent_runs (
    id                UUID PRIMARY KEY,
    session_id        TEXT NOT NULL REFERENCES web_sessions (id) ON DELETE CASCADE,
    client_request_id UUID NOT NULL,
    kind              TEXT NOT NULL CHECK (kind IN ('screening', 'build', 'change')),
    status            TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'interrupted')),
    error             JSONB,
    started_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at       TIMESTAMPTZ,
    UNIQUE (session_id, client_request_id)
);
CREATE UNIQUE INDEX agent_runs_one_running_per_session_idx
    ON agent_runs (session_id) WHERE status = 'running';
CREATE INDEX agent_runs_session_started_idx ON agent_runs (session_id, started_at DESC);

CREATE TABLE web_messages (
    id                UUID PRIMARY KEY,
    session_id        TEXT NOT NULL REFERENCES web_sessions (id) ON DELETE CASCADE,
    client_message_id UUID,
    role              TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
    content           TEXT NOT NULL CHECK (length(content) > 0),
    run_id            UUID REFERENCES agent_runs (id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX web_messages_client_id_idx
    ON web_messages (session_id, client_message_id) WHERE client_message_id IS NOT NULL;
CREATE INDEX web_messages_session_created_idx ON web_messages (session_id, created_at, id);

-- P9 才启用分享 handler；P7 只锁定不可变 build 版本和 token 哈希存储形态。
CREATE TABLE build_shares (
    id         BIGSERIAL PRIMARY KEY,
    build_id   BIGINT NOT NULL REFERENCES builds (id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ
);
CREATE INDEX build_shares_build_idx ON build_shares (build_id);

-- +goose Down
DROP TABLE build_shares;
DROP TABLE web_messages;
DROP TABLE agent_runs;
DROP TABLE web_sessions;
