-- +goose Up
-- 助手需求建议(requirement proposal):非 active、只对紧接着的下一条用户
-- 消息有效。绑定真实 assistant message id;consumed_by_message_id 是首个
-- 消费该建议的用户消息,此后建议失效,不得跨轮或并发复用。
CREATE TABLE requirement_proposals (
    id bigserial PRIMARY KEY,
    session_id text NOT NULL REFERENCES web_sessions (id),
    assistant_message_id uuid NOT NULL REFERENCES web_messages (id),
    field text NOT NULL,
    value jsonb NOT NULL,
    text text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    consumed_by_message_id uuid REFERENCES web_messages (id),
    resolved_at timestamptz,
    UNIQUE (assistant_message_id, field)
);
CREATE INDEX requirement_proposals_session_idx ON requirement_proposals (session_id, id DESC);

-- +goose Down
DROP TABLE requirement_proposals;
