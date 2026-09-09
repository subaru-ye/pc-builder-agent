package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrFeedbackInvalid = errors.New("反馈内容无效")
var ErrFeedbackRunActive = errors.New("运行尚未结束")
var ErrFeedbackNotFound = errors.New("反馈不存在")

type Feedback struct {
	ID          string    `json:"id"`
	RunID       string    `json:"run_id"`
	Reason      string    `json:"reason"`
	Comment     string    `json:"comment"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func ValidFeedback(reason, comment string) bool {
	if !utf8.ValidString(comment) || utf8.RuneCountInString(comment) > 2000 {
		return false
	}
	switch reason {
	case "unnecessary_question", "requirement_mismatch", "configuration_issue", "price_issue", "unclear_explanation":
		return true
	case "other":
		return strings.TrimSpace(comment) != ""
	default:
		return false
	}
}

// CaptureRunEvidence 按执行阶段首次落盘；重复调用不覆盖原输入/输出。
func (s *Store) CaptureRunEvidence(ctx context.Context, runID, slot string, payload json.RawMessage) error {
	if slot == "build_output" {
		var data map[string]json.RawMessage
		if err := json.Unmarshal(payload, &data); err != nil {
			return err
		}
		var version int
		_ = json.Unmarshal(data["build_version"], &version)
		if version > 0 {
			var build json.RawMessage
			if err := s.pool.QueryRow(ctx, `SELECT jsonb_build_object('version',b.version,'requirement',q.spec,'change',b.change,'draft',b.draft,'validation',b.validation,'quote',b.quote)
			FROM builds b JOIN requirements q ON q.id=b.requirement_id JOIN agent_runs r ON r.session_id=b.session_id WHERE r.id=$1 AND b.version=$2`, runID, version).Scan(&build); err != nil {
				return err
			}
			data["build"] = build
			payload, _ = json.Marshal(data)
		}
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO run_evidence(run_id,slot,payload) VALUES($1,$2,$3) ON CONFLICT(run_id,slot) DO NOTHING`, runID, slot, payload)
	return err
}

const feedbackColumns = `id::text,run_id::text,reason,comment,fingerprint,created_at,updated_at`

func scanFeedback(row interface{ Scan(...any) error }) (Feedback, error) {
	var f Feedback
	err := row.Scan(&f.ID, &f.RunID, &f.Reason, &f.Comment, &f.Fingerprint, &f.CreatedAt, &f.UpdatedAt)
	return f, err
}

func (s *Store) FeedbackByOwner(ctx context.Context, ownerID, runID string) (*Feedback, error) {
	if _, err := s.RunByOwner(ctx, ownerID, runID); err != nil {
		return nil, err
	}
	f, err := scanFeedback(s.pool.QueryRow(ctx, `SELECT `+feedbackColumns+` FROM run_feedback WHERE run_id=$1`, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &f, err
}

// SubmitFeedback 在终态运行锁内检查归属并冻结证据；修改反馈不更换证据。
func (s *Store) SubmitFeedback(ctx context.Context, ownerID, runID, reason, comment string) (Feedback, error) {
	comment = strings.TrimSpace(comment)
	if !ValidFeedback(reason, comment) {
		return Feedback{}, ErrFeedbackInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Feedback{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	if err := tx.QueryRow(ctx, `SELECT r.status FROM agent_runs r JOIN web_sessions s ON s.id=r.session_id WHERE r.id=$1 AND s.owner_id=$2 FOR UPDATE OF r`, runID, ownerID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Feedback{}, ErrRunNotFound
		}
		return Feedback{}, err
	}
	if status == string(RunRunning) {
		return Feedback{}, ErrFeedbackRunActive
	}
	var evidence json.RawMessage
	// 不读取当前 pending_requirement 或环境变量。模型与需求仅来自当次捕获的证据槽。
	err = tx.QueryRow(ctx, `SELECT jsonb_build_object(
		'schema_version',1,'kind',r.kind,'status',r.status,'error',r.error,
		'started_at',r.started_at,'finished_at',r.finished_at,
		'messages',COALESCE((SELECT jsonb_agg(jsonb_build_object('role',m.role,'content',m.content) ORDER BY m.created_at,m.id) FROM web_messages m WHERE m.run_id=r.id),'[]'::jsonb),
		'captured',COALESCE((SELECT jsonb_object_agg(e.slot,e.payload) FROM run_evidence e WHERE e.run_id=r.id),'{}'::jsonb)) FROM agent_runs r WHERE r.id=$1`, runID).Scan(&evidence)
	if err != nil {
		return Feedback{}, err
	}
	var envelope struct {
		Kind     string                     `json:"kind"`
		Captured map[string]json.RawMessage `json:"captured"`
	}
	if err := json.Unmarshal(evidence, &envelope); err != nil {
		return Feedback{}, err
	}
	slots := []string{"screening_input", "screening_output"}
	if envelope.Kind == string(RunBuild) {
		slots = []string{"build_input", "build_output"}
	}
	if envelope.Kind == string(RunChange) {
		// 程序可直接处理改单，也可能先初筛并停在追问；只按实际进入的分支标缺。
		if _, building := envelope.Captured["build_input"]; building {
			slots = []string{"build_input", "build_output"}
			if _, screened := envelope.Captured["screening_input"]; screened {
				slots = append(slots, "screening_input", "screening_output")
			}
		}
	}
	missing := []string{}
	for _, slot := range slots {
		if _, ok := envelope.Captured[slot]; !ok {
			missing = append(missing, slot)
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(evidence, &fields); err != nil {
		return Feedback{}, err
	}
	fields["missing_evidence"], _ = json.Marshal(missing)
	evidence, err = json.Marshal(fields)
	if err != nil {
		return Feedback{}, err
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(evidence))
	f, err := scanFeedback(tx.QueryRow(ctx, `INSERT INTO run_feedback(id,run_id,reason,comment,evidence,fingerprint) VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT(run_id) DO UPDATE SET reason=EXCLUDED.reason,comment=EXCLUDED.comment,
		updated_at=CASE WHEN run_feedback.reason=EXCLUDED.reason AND run_feedback.comment=EXCLUDED.comment THEN run_feedback.updated_at ELSE now() END
		RETURNING `+feedbackColumns, uuid.NewString(), runID, reason, comment, evidence, fingerprint))
	if err != nil {
		return Feedback{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Feedback{}, err
	}
	return f, nil
}

// FeedbackCandidate 只供显式本机导出；尚未脱敏的证据不能直接成为正式题目。
func (s *Store) FeedbackCandidate(ctx context.Context, id string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := s.pool.QueryRow(ctx, `SELECT jsonb_build_object('schema_version',1,'review_status','pending_review','fingerprint',fingerprint,
		'feedback',jsonb_build_object('reason',reason,'comment',comment,'created_at',created_at,'updated_at',updated_at),
		'evidence',evidence,'expected',NULL,'review',jsonb_build_object('redacted',false,'deduplicated',false,'attribution',NULL,'reviewer',NULL)) FROM run_feedback WHERE id=$1`, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrFeedbackNotFound
	}
	return raw, err
}
