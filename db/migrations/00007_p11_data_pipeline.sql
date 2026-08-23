-- +goose Up
-- P11 本机数据自动化：目录状态、运行检查点、不可变发布和字段证据。
-- 这些表只服务数据运维，不改变产品 API DTO、Agent schema 或历史 build。

ALTER TABLE parts
    ADD COLUMN catalog_state TEXT NOT NULL DEFAULT 'active_core'
        CHECK (catalog_state IN ('active_core', 'catalog_only', 'retired')),
    ADD COLUMN spec_fingerprint TEXT;

CREATE INDEX idx_parts_catalog_state_category
    ON parts (catalog_state, category);

CREATE TABLE data_job_runs (
    run_id          UUID PRIMARY KEY,
    profile         TEXT NOT NULL CHECK (profile IN ('health', 'weekly', 'monthly', 'retry')),
    trigger_kind    TEXT NOT NULL CHECK (trigger_kind IN ('manual', 'schedule', 'startup_catch_up')),
    scheduled_for   TIMESTAMPTZ NOT NULL,
    status          TEXT NOT NULL CHECK (status IN
        ('running', 'no_change', 'published', 'quarantined', 'failed', 'blocked')),
    model_used      BOOLEAN NOT NULL DEFAULT FALSE,
    manifest_sha256 TEXT,
    error_code      TEXT,
    summary         JSONB NOT NULL DEFAULT '{}'::jsonb,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at     TIMESTAMPTZ,
    UNIQUE (profile, scheduled_for)
);

CREATE TABLE source_checkpoints (
    source_id         TEXT PRIMARY KEY,
    etag              TEXT,
    last_modified     TEXT,
    content_sha256    TEXT,
    last_attempt_at   TIMESTAMPTZ,
    last_success_at   TIMESTAMPTZ,
    terms_reviewed_at DATE,
    status            TEXT NOT NULL DEFAULT 'unknown',
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE data_publications (
    release_id          UUID PRIMARY KEY,
    previous_release_id UUID REFERENCES data_publications (release_id),
    run_id              UUID,
    manifest_sha256     TEXT NOT NULL UNIQUE,
    input_sha256        TEXT NOT NULL,
    decision            TEXT NOT NULL CHECK (decision IN ('auto', 'manual', 'bootstrap')),
    stats               JSONB NOT NULL DEFAULT '{}'::jsonb,
    published_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_data_publications_published_at
    ON data_publications (published_at DESC);

CREATE TABLE part_evidence (
    evidence_id     TEXT PRIMARY KEY,
    sku             TEXT NOT NULL REFERENCES parts (sku),
    field_path      TEXT NOT NULL,
    value           JSONB NOT NULL,
    source_id       TEXT NOT NULL,
    source_url      TEXT NOT NULL,
    raw_sha256      TEXT NOT NULL,
    method          TEXT NOT NULL CHECK (method IN ('deterministic', 'model_assisted', 'manual')),
    evidence_status TEXT NOT NULL CHECK (evidence_status IN ('verified', 'candidate', 'conflict', 'rejected')),
    captured_at     TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (sku, field_path, source_id, raw_sha256)
);

CREATE INDEX idx_part_evidence_sku_field
    ON part_evidence (sku, field_path);

-- 迁移前的目录即 P0-P10 已验收基线；保留 active_core，不猜造历史 evidence。
UPDATE parts SET catalog_state = 'active_core' WHERE active;
UPDATE parts SET catalog_state = 'retired' WHERE NOT active;

-- +goose Down
DROP TABLE part_evidence;
DROP TABLE data_publications;
DROP TABLE source_checkpoints;
DROP TABLE data_job_runs;

DROP INDEX idx_parts_catalog_state_category;
ALTER TABLE parts DROP COLUMN spec_fingerprint;
ALTER TABLE parts DROP COLUMN catalog_state;
