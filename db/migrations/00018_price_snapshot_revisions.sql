-- +goose Up
-- A date is an observation grouping, not an immutable release identifier.
-- Existing IDs and rows remain unchanged; a new same-day batch gets a new ID.
ALTER TABLE price_snapshots DROP CONSTRAINT price_snapshots_snapshot_date_key;
CREATE INDEX idx_price_snapshots_date_id ON price_snapshots(snapshot_date DESC, id DESC);

-- +goose Down
-- Downgrade fails if same-day revisions exist rather than deleting history.
DROP INDEX idx_price_snapshots_date_id;
ALTER TABLE price_snapshots ADD CONSTRAINT price_snapshots_snapshot_date_key UNIQUE(snapshot_date);
