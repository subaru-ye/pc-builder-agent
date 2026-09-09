-- +goose Up
-- 本机反馈与待复核案例：证据槽首次写入后冻结，反馈不得成为自动金标。
CREATE TABLE run_evidence (
    run_id UUID NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    slot TEXT NOT NULL CHECK (slot IN ('screening_input','screening_output','build_input','build_output')),
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, slot)
);

CREATE TABLE run_feedback (
    id UUID PRIMARY KEY,
    run_id UUID NOT NULL UNIQUE REFERENCES agent_runs(id) ON DELETE CASCADE,
    reason TEXT NOT NULL CHECK (reason IN ('unnecessary_question','requirement_mismatch','configuration_issue','price_issue','unclear_explanation','other')),
    comment TEXT NOT NULL DEFAULT '' CHECK (char_length(comment) <= 2000),
    evidence JSONB NOT NULL,
    fingerprint TEXT NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE run_feedback;
DROP TABLE run_evidence;
