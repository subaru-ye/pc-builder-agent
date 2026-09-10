-- +goose Up
-- 原始回复保留供证据追溯；用户摘要与对应配置版本随消息原子保存。
ALTER TABLE web_messages ADD COLUMN display_content TEXT;
ALTER TABLE web_messages ADD COLUMN build_version INT CHECK (build_version > 0);

-- +goose Down
ALTER TABLE web_messages DROP COLUMN build_version;
ALTER TABLE web_messages DROP COLUMN display_content;
