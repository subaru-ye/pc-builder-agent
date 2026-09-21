-- +goose Up
-- F2 第一步:planning 完整产物(含证据全文)按 run 归档,跨进程只传降级预览。
-- 不复用 run_evidence 的四槽位(00012,首写冻结的反馈证据)。
CREATE TABLE planning_artifacts (
    run_id        TEXT PRIMARY KEY,
    session_id    TEXT NOT NULL,
    payload       JSONB NOT NULL,
    payload_bytes BIGINT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX planning_artifacts_session_created_idx ON planning_artifacts (session_id, created_at DESC);

-- +goose Down
DROP TABLE planning_artifacts;
