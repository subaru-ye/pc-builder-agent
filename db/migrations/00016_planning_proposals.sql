-- +goose Up
CREATE TABLE session_proposals (
    id BIGSERIAL PRIMARY KEY,
    session_id TEXT NOT NULL,
    run_id TEXT NOT NULL UNIQUE,
    requirement JSONB NOT NULL,
    parent_version INTEGER NOT NULL DEFAULT 0,
    result JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX session_proposals_session_idx ON session_proposals(session_id,id DESC);
ALTER TABLE builds ADD COLUMN candidate_snapshot JSONB;
CREATE TABLE search_quota (
    month TEXT PRIMARY KEY,
    reserved INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE search_quota;
ALTER TABLE builds DROP COLUMN candidate_snapshot;
DROP TABLE session_proposals;
