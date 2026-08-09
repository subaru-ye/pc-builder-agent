-- +goose Up
-- P9 分享管理:公开 UUID 用于所有者侧撤销，client_request_id 保证创建幂等。
-- 原始 token 永不落库；token_hash 继续作为公开读取的唯一索引。
ALTER TABLE build_shares
    ADD COLUMN public_id UUID NOT NULL DEFAULT gen_random_uuid(),
    ADD COLUMN client_request_id UUID;

ALTER TABLE build_shares
    ADD CONSTRAINT build_shares_public_id_key UNIQUE (public_id);

CREATE UNIQUE INDEX build_shares_build_request_idx
    ON build_shares (build_id, client_request_id)
    WHERE client_request_id IS NOT NULL;

CREATE INDEX build_shares_build_created_idx
    ON build_shares (build_id, created_at DESC);

CREATE INDEX build_shares_active_token_idx
    ON build_shares (token_hash)
    WHERE revoked_at IS NULL;

-- +goose Down
DROP INDEX build_shares_active_token_idx;
DROP INDEX build_shares_build_created_idx;
DROP INDEX build_shares_build_request_idx;
ALTER TABLE build_shares DROP CONSTRAINT build_shares_public_id_key;
ALTER TABLE build_shares DROP COLUMN client_request_id;
ALTER TABLE build_shares DROP COLUMN public_id;
