-- +goose Up
-- 装机偏好记忆第一阶段:owner 级原子偏好存储(独立基座,未接入产品调用链)。
-- 归属沿用 owner_id TEXT(与 product_user_owners 对齐,认领后经多 owner 解析可达);
-- subject 区分本人(self)与具名代配对象。会话内 temporary 内容不升级入库,
-- 因此本表没有 scope 字段;易失事实(价格/现货)必须 volatile=true 并带 observed_at。

CREATE TABLE owner_preference_memories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id TEXT NOT NULL,
    subject TEXT NOT NULL CHECK (subject <> '' AND char_length(subject) <= 40),
    field TEXT NOT NULL CHECK (field <> '' AND char_length(field) <= 64),
    value JSONB NOT NULL,
    strength TEXT NOT NULL DEFAULT 'prefer' CHECK (strength IN ('prefer', 'must')),
    evidence TEXT NOT NULL CHECK (evidence IN ('stated', 'accepted_proposal')),
    volatile BOOLEAN NOT NULL DEFAULT false,
    observed_at DATE,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'superseded', 'retracted')),
    supersedes UUID REFERENCES owner_preference_memories(id) ON DELETE SET NULL,
    source JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (volatile = false OR observed_at IS NOT NULL)
);

-- 同一 (owner, subject, field) 至多一条 active:改主意走 supersede 链,DB 层防并发双写。
CREATE UNIQUE INDEX owner_preference_memories_one_active
    ON owner_preference_memories(owner_id, subject, field)
    WHERE status = 'active';

CREATE INDEX owner_preference_memories_recall
    ON owner_preference_memories(owner_id, subject)
    WHERE status = 'active';

-- +goose Down
DROP TABLE owner_preference_memories;
