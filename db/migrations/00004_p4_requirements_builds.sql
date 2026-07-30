-- +goose Up
-- P4 版本快照与增量改单(P4-版本快照与增量改单设计.md §2):requirements/builds 落库。
-- builds.parent_id 串起版本树 v1→v2→…;version 由代码派生(parent.version+1),是版本号唯一真值。
-- quote 内含 snapshot_date,报价绑定快照日期(用例 E 导出依赖)。
CREATE TABLE requirements (
    id         BIGSERIAL PRIMARY KEY,
    session_id TEXT NOT NULL,
    spec       JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX requirements_session_idx ON requirements (session_id);

CREATE TABLE builds (
    id             BIGSERIAL PRIMARY KEY,
    session_id     TEXT NOT NULL,
    version        INT NOT NULL CHECK (version >= 1),
    parent_id      BIGINT REFERENCES builds (id),
    requirement_id BIGINT NOT NULL REFERENCES requirements (id),
    change         JSONB,
    draft          JSONB NOT NULL,
    validation     JSONB NOT NULL,
    quote          JSONB NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (session_id, version)
);

-- +goose Down
DROP TABLE builds;
DROP TABLE requirements;
