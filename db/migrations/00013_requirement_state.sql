-- +goose Up
-- 会话需求草稿与最近确认快照；历史 requirements/builds 保持不可变。
-- 旧会话保持 NULL，不把旧默认值迁移成用户已表达的偏好。
ALTER TABLE web_sessions
    ADD COLUMN requirement_state JSONB,
    ADD COLUMN confirmed_requirement_state JSONB,
    ADD COLUMN confirmed_requirement JSONB,
    ADD COLUMN confirmed_at TIMESTAMPTZ;

-- 编辑请求的幂等性比较使用完整操作指纹，不依赖展示文案。
ALTER TABLE web_messages ADD COLUMN request_fingerprint TEXT;

-- +goose Down
ALTER TABLE web_messages DROP COLUMN request_fingerprint;
ALTER TABLE web_sessions
    DROP COLUMN confirmed_at,
    DROP COLUMN confirmed_requirement,
    DROP COLUMN confirmed_requirement_state,
    DROP COLUMN requirement_state;
