-- +goose Up
-- P11 收口：隔离来源可产生 partial run，字段证据绑定不可变 release。

ALTER TABLE data_job_runs DROP CONSTRAINT data_job_runs_status_check;
ALTER TABLE data_job_runs
    ADD CONSTRAINT data_job_runs_status_check CHECK (status IN
        ('running', 'no_change', 'published', 'partial', 'quarantined', 'failed', 'blocked'));

ALTER TABLE part_evidence
    ADD COLUMN release_id UUID REFERENCES data_publications (release_id),
    ADD COLUMN evidence_excerpt TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_part_evidence_release
    ON part_evidence (release_id);

CREATE INDEX idx_part_evidence_release_sku_field
    ON part_evidence (release_id, sku, field_path);

-- +goose Down
DROP INDEX idx_part_evidence_release_sku_field;
DROP INDEX idx_part_evidence_release;

ALTER TABLE part_evidence DROP COLUMN release_id;
ALTER TABLE part_evidence DROP COLUMN evidence_excerpt;

ALTER TABLE data_job_runs DROP CONSTRAINT data_job_runs_status_check;
ALTER TABLE data_job_runs
    ADD CONSTRAINT data_job_runs_status_check CHECK (status IN
        ('running', 'no_change', 'published', 'quarantined', 'failed', 'blocked'));
