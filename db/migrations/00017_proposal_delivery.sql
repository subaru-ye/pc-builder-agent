-- +goose Up
ALTER TABLE session_proposals ADD COLUMN build_id BIGINT REFERENCES builds(id);
CREATE UNIQUE INDEX session_proposals_build_idx ON session_proposals(build_id) WHERE build_id IS NOT NULL;

-- +goose Down
DROP INDEX session_proposals_build_idx;
ALTER TABLE session_proposals DROP COLUMN build_id;
