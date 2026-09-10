-- +goose Up
-- 归档仅影响会话列表；手动命名后不再以首条消息覆盖标题。
ALTER TABLE web_sessions ADD COLUMN archived BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE web_sessions ADD COLUMN title_custom BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE web_sessions DROP COLUMN title_custom;
ALTER TABLE web_sessions DROP COLUMN archived;
