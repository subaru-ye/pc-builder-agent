-- +goose Up
-- P1 最小数据库(设计方案 §六冻结):仅 parts / price_snapshots / prices 三张表;
-- P3/P4 的表与 embedding 列一律不提前创建。

-- 零件真值目录:specs 为 canonical 字段(§四.1 字段字典),source_meta 记录字段来源。
CREATE TABLE parts (
    sku            TEXT PRIMARY KEY,
    category       TEXT NOT NULL CHECK (category IN
        ('cpu', 'gpu', 'motherboard', 'memory', 'ssd', 'psu', 'case', 'cooler')),
    brand          TEXT NOT NULL,
    model          TEXT NOT NULL,
    schema_version INTEGER NOT NULL DEFAULT 1,
    specs          JSONB NOT NULL,
    source_meta    JSONB NOT NULL DEFAULT '{}'::jsonb,
    active         BOOLEAN NOT NULL DEFAULT TRUE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_parts_category ON parts (category);

-- 价格快照批次:同一天只允许一个批次,文件 SHA256 保证幂等导入可识别。
CREATE TABLE price_snapshots (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    snapshot_date DATE NOT NULL UNIQUE,
    file_sha256   TEXT NOT NULL UNIQUE,
    imported_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 快照内单 SKU 人民币价格;批次删除时级联清理。
CREATE TABLE prices (
    snapshot_id BIGINT NOT NULL REFERENCES price_snapshots (id) ON DELETE CASCADE,
    sku         TEXT NOT NULL REFERENCES parts (sku),
    price_cny   NUMERIC(10, 2) NOT NULL CHECK (price_cny > 0),
    source      TEXT NOT NULL,
    UNIQUE (snapshot_id, sku)
);

-- +goose Down
DROP TABLE prices;
DROP TABLE price_snapshots;
DROP TABLE parts;
