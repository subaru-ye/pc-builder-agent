-- +goose Up
-- P12A 价格观察与安全选价：保留历史快照兼容，同时为新发布建立可审计证据链。

CREATE TABLE price_observations (
    observation_id   TEXT PRIMARY KEY CHECK (observation_id ~ '^[0-9a-f]{64}$'),
    sku              TEXT NOT NULL REFERENCES parts (sku),
    source_id        TEXT NOT NULL,
    product_id       TEXT NOT NULL,
    source_url       TEXT,
    seller           TEXT NOT NULL,
    price_cny        NUMERIC(10, 2) NOT NULL CHECK (price_cny > 0),
    currency         TEXT NOT NULL DEFAULT 'CNY' CHECK (currency = 'CNY'),
    price_type       TEXT NOT NULL CHECK (price_type IN
        ('regular', 'sale', 'coupon', 'member', 'msrp', 'deposit', 'installment', 'bundle', 'unknown')),
    stock_status     TEXT NOT NULL CHECK (stock_status IN ('in_stock', 'out_of_stock', 'unknown')),
    variant_match    TEXT NOT NULL CHECK (variant_match IN ('exact', 'mismatch', 'unknown')),
    observed_at      TIMESTAMPTZ NOT NULL,
    raw_sha256       TEXT NOT NULL CHECK (raw_sha256 ~ '^[0-9a-f]{64}$'),
    decision_status  TEXT NOT NULL CHECK (decision_status IN ('qualified', 'observed_only', 'rejected', 'quarantined')),
    rejection_reasons JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (sku, source_id, product_id, price_cny, observed_at, raw_sha256)
);

CREATE INDEX idx_price_observations_sku_observed
    ON price_observations (sku, observed_at DESC);
CREATE INDEX idx_price_observations_source_observed
    ON price_observations (source_id, observed_at DESC);
CREATE INDEX idx_price_observations_decision
    ON price_observations (decision_status, observed_at DESC);

-- +goose StatementBegin
CREATE FUNCTION reject_price_observation_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'price_observations are immutable';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER price_observations_immutable_update
    BEFORE UPDATE ON price_observations
    FOR EACH ROW EXECUTE FUNCTION reject_price_observation_mutation();
CREATE TRIGGER price_observations_immutable_delete
    BEFORE DELETE ON price_observations
    FOR EACH ROW EXECUTE FUNCTION reject_price_observation_mutation();

ALTER TABLE price_snapshots
    ADD COLUMN release_id UUID,
    ADD COLUMN previous_snapshot_id BIGINT REFERENCES price_snapshots (id),
    ADD COLUMN run_id UUID REFERENCES data_job_runs (run_id),
    ADD COLUMN manifest_sha256 TEXT,
    ADD COLUMN publication_policy TEXT NOT NULL DEFAULT 'bootstrap'
        CHECK (publication_policy IN ('bootstrap', 'manual', 'automatic')),
    ADD COLUMN stats JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE UNIQUE INDEX idx_price_snapshots_manifest
    ON price_snapshots (manifest_sha256) WHERE manifest_sha256 IS NOT NULL;
CREATE UNIQUE INDEX idx_price_snapshots_release
    ON price_snapshots (release_id) WHERE release_id IS NOT NULL;

ALTER TABLE prices
    ADD COLUMN observation_id TEXT REFERENCES price_observations (observation_id),
    ADD COLUMN observed_at TIMESTAMPTZ,
    ADD COLUMN price_type TEXT CHECK (price_type IN ('regular', 'sale', 'bootstrap')),
    ADD COLUMN carried_forward BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN source_snapshot_id BIGINT REFERENCES price_snapshots (id);

-- 历史四列 CSV 只作为 bootstrap。其来源标签不等同于可验证商品证据。
UPDATE price_snapshots
SET publication_policy = 'bootstrap',
    stats = jsonb_build_object('legacy', true, 'verified_observations', 0)
WHERE manifest_sha256 IS NULL;

UPDATE prices p
SET observed_at = s.snapshot_date::timestamptz,
    price_type = 'bootstrap',
    source_snapshot_id = p.snapshot_id
FROM price_snapshots s
WHERE s.id = p.snapshot_id AND p.observed_at IS NULL;

-- 三列保持 nullable，兼容旧测试夹具和历史只读快照；产品层将无法关联者标为 unknown。

-- +goose Down
ALTER TABLE prices
    DROP COLUMN source_snapshot_id,
    DROP COLUMN carried_forward,
    DROP COLUMN price_type,
    DROP COLUMN observed_at,
    DROP COLUMN observation_id;

DROP INDEX idx_price_snapshots_release;
DROP INDEX idx_price_snapshots_manifest;
ALTER TABLE price_snapshots
    DROP COLUMN stats,
    DROP COLUMN publication_policy,
    DROP COLUMN manifest_sha256,
    DROP COLUMN run_id,
    DROP COLUMN previous_snapshot_id,
    DROP COLUMN release_id;

DROP TRIGGER price_observations_immutable_delete ON price_observations;
DROP TRIGGER price_observations_immutable_update ON price_observations;
DROP FUNCTION reject_price_observation_mutation;
DROP TABLE price_observations;
