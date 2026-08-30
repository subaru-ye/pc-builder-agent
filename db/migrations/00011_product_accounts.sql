-- +goose Up
-- 产品账号只保存 Supabase Auth 身份投影，不读取或修改 auth schema。

CREATE TABLE product_users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    auth_subject UUID NOT NULL UNIQUE,
    email TEXT NOT NULL,
    display_name TEXT NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 40),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE product_user_owners (
    user_id UUID NOT NULL REFERENCES product_users(id) ON DELETE CASCADE,
    owner_id TEXT NOT NULL UNIQUE,
    is_primary BOOLEAN NOT NULL DEFAULT false,
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, owner_id)
);

CREATE UNIQUE INDEX product_user_owners_one_primary
    ON product_user_owners(user_id)
    WHERE is_primary;

CREATE INDEX product_user_owners_user_claimed
    ON product_user_owners(user_id, claimed_at);

-- +goose Down
DROP TABLE product_user_owners;
DROP TABLE product_users;
