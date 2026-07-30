-- +goose Up
-- P1 起 extension 由编号迁移管理(原 db/init/01-extensions.sql 移交至此)。
-- IF NOT EXISTS 保证 P0 旧卷(entrypoint 已建过 vector)与空库都能直接 up。
CREATE EXTENSION IF NOT EXISTS vector;

-- +goose Down
-- 不删除 extension:后续表的 embedding 列(P3)可能依赖,down 保持空操作。
SELECT 1;
