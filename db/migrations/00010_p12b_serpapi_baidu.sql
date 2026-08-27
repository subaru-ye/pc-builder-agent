-- +goose Up
-- P12B 搜索平台报价：区分采集器与底层商家，并显式表达“仅观察到报价”。

ALTER TABLE data_job_runs DROP CONSTRAINT data_job_runs_profile_check;
ALTER TABLE data_job_runs ADD CONSTRAINT data_job_runs_profile_check
    CHECK (profile IN ('health', 'price-daily', 'weekly', 'monthly', 'retry'));

ALTER TABLE price_observations
    ADD COLUMN collector_id TEXT NOT NULL DEFAULT 'manual_price_csv',
    ADD COLUMN availability_basis TEXT NOT NULL DEFAULT 'confirmed_stock'
        CHECK (availability_basis IN ('confirmed_stock', 'search_listing', 'unknown'));

ALTER TABLE price_observations DROP CONSTRAINT price_observations_price_type_check;
ALTER TABLE price_observations ADD CONSTRAINT price_observations_price_type_check
    CHECK (price_type IN
        ('regular', 'sale', 'listing', 'coupon', 'member', 'msrp', 'deposit', 'installment', 'bundle', 'unknown'));

CREATE INDEX idx_price_observations_collector_observed
    ON price_observations (collector_id, observed_at DESC);

ALTER TABLE prices
    ADD COLUMN availability_basis TEXT NOT NULL DEFAULT 'unknown'
        CHECK (availability_basis IN ('confirmed_stock', 'search_listing', 'unknown'));

ALTER TABLE prices DROP CONSTRAINT prices_price_type_check;
ALTER TABLE prices ADD CONSTRAINT prices_price_type_check
    CHECK (price_type IN ('regular', 'sale', 'listing', 'bootstrap'));

UPDATE prices p
SET availability_basis = CASE
    WHEN p.price_type IN ('regular', 'sale') AND p.observation_id IS NOT NULL THEN 'confirmed_stock'
    ELSE 'unknown'
END;

-- +goose Down
ALTER TABLE data_job_runs DROP CONSTRAINT data_job_runs_profile_check;
ALTER TABLE data_job_runs ADD CONSTRAINT data_job_runs_profile_check
    CHECK (profile IN ('health', 'weekly', 'monthly', 'retry'));

ALTER TABLE prices DROP CONSTRAINT prices_price_type_check;
ALTER TABLE prices ADD CONSTRAINT prices_price_type_check
    CHECK (price_type IN ('regular', 'sale', 'bootstrap'));
ALTER TABLE prices DROP COLUMN availability_basis;

DROP INDEX idx_price_observations_collector_observed;
ALTER TABLE price_observations DROP CONSTRAINT price_observations_price_type_check;
ALTER TABLE price_observations ADD CONSTRAINT price_observations_price_type_check
    CHECK (price_type IN
        ('regular', 'sale', 'coupon', 'member', 'msrp', 'deposit', 'installment', 'bundle', 'unknown'));
ALTER TABLE price_observations
    DROP COLUMN availability_basis,
    DROP COLUMN collector_id;
