package producthttp

import (
	"encoding/json"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type sessionSummaryDTO struct {
	StatusLabel   string             `json:"status_label,omitempty"`
	SchemaVersion int                `json:"schema_version"`
	ID            string             `json:"id"`
	Title         string             `json:"title"`
	Archived      bool               `json:"archived"`
	Phase         store.SessionPhase `json:"phase"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	VersionCount  int                `json:"version_count"`
}

type messageDTO struct {
	SchemaVersion  int       `json:"schema_version"`
	ID             string    `json:"id"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	DisplayContent string    `json:"display_content,omitempty"`
	RunID          *string   `json:"run_id"`
	CreatedAt      time.Time `json:"created_at"`
}

type runDTO struct {
	SchemaVersion int             `json:"schema_version"`
	ID            string          `json:"id"`
	SessionID     string          `json:"session_id"`
	Kind          store.RunKind   `json:"kind"`
	Status        store.RunStatus `json:"status"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    *time.Time      `json:"finished_at"`
	Error         json.RawMessage `json:"error"`
	EventsURL     string          `json:"events_url"`
}

// requirementReadinessDTO 是后端计算的 readiness 真值;前端只渲染,不重算。
type requirementReadinessDTO struct {
	Status                  string                          `json:"status"`
	MissingFields           []string                        `json:"missing_fields"`
	BlockingConflicts       []string                        `json:"blocking_conflicts"`
	UnsupportedCapabilities []string                        `json:"unsupported_capabilities"`
	NextQuestion            *schemas.RequirementQuestion    `json:"next_question"`
	ConfirmationEligible    bool                            `json:"confirmation_eligible"`
	EffectiveDefaults       []schemas.RequirementDefault    `json:"effective_defaults"`
}

type requirementConfirmationDTO struct {
	Status              string     `json:"status"` // unconfirmed | confirmed | modified
	ConfirmedRevision   *int       `json:"confirmed_revision"`
	ConfirmedAt         *time.Time `json:"confirmed_at"`
	ConfirmedReviewHash string     `json:"confirmed_review_hash,omitempty"`
}

type buildRelationDTO struct {
	Status           string `json:"status"` // none | running | current | outdated | failed
	Version          *int   `json:"version"`
	SnapshotID       string `json:"snapshot_id,omitempty"`
	ReviewHash       string `json:"review_hash,omitempty"`
	BuilderInputHash string `json:"builder_input_hash,omitempty"`
}

type sessionDTO struct {
	Proposal json.RawMessage `json:"proposal,omitempty"`
	sessionSummaryDTO
	Messages                  []messageDTO              `json:"messages"`
	PendingRequirement        json.RawMessage           `json:"pending_requirement"`
	RequirementState          json.RawMessage           `json:"requirement_state"`
	RequirementReadiness      *requirementReadinessDTO  `json:"requirement_readiness"`
	ReviewSpec                json.RawMessage           `json:"review_spec"`
	ReviewHash                *string                   `json:"review_hash"`
	EffectiveBudgetCeilingCNY *int                      `json:"effective_budget_ceiling_cny"`
	RequirementConfirmation   requirementConfirmationDTO `json:"requirement_confirmation"`
	BuildRelation             buildRelationDTO          `json:"build_relation"`
	ActiveRun                 *runDTO                   `json:"active_run"`
	LastError                 json.RawMessage           `json:"last_error"`
	RecoveryPhase             *store.SessionPhase       `json:"recovery_phase"`
	Degraded                  bool                      `json:"degraded"`
}

func toSessionSummary(s store.WebSession) sessionSummaryDTO {
	return sessionSummaryDTO{
		StatusLabel:   s.StatusLabel,
		SchemaVersion: 1, ID: s.ID, Title: s.Title, Phase: s.Phase, Archived: s.Archived,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, VersionCount: s.VersionCount,
	}
}

func toRun(r store.AgentRun) runDTO {
	errorJSON := r.Error
	if len(errorJSON) == 0 {
		errorJSON = json.RawMessage("null")
	}
	return runDTO{
		SchemaVersion: 1, ID: r.ID, SessionID: r.SessionID, Kind: r.Kind, Status: r.Status,
		StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Error: errorJSON,
		EventsURL: "/api/v1/runs/" + r.ID + "/events",
	}
}

func toSession(detail product.SessionDetail) sessionDTO {
	messages := make([]messageDTO, 0, len(detail.Messages))
	for _, m := range detail.Messages {
		messages = append(messages, messageDTO{
			SchemaVersion: 1, ID: m.ID, Role: m.Role, Content: m.Content,
			DisplayContent: m.DisplayContent,
			RunID:          m.RunID, CreatedAt: m.CreatedAt,
		})
	}
	var active *runDTO
	if detail.ActiveRun != nil {
		r := toRun(*detail.ActiveRun)
		active = &r
	}
	pending := detail.Session.PendingRequirement
	if len(pending) == 0 {
		pending = json.RawMessage("null")
	}
	reviewSpec := detail.Axes.ReviewSpec
	if len(reviewSpec) == 0 {
		reviewSpec = json.RawMessage("null")
	}
	readiness := detail.Axes.Readiness
	out := sessionDTO{
		Proposal:           detail.Proposal,
		sessionSummaryDTO:  toSessionSummary(detail.Session),
		Messages:           messages,
		PendingRequirement: pending,
		RequirementState:   detail.Session.RequirementState,
		ReviewSpec:         reviewSpec,
		EffectiveBudgetCeilingCNY: detail.Axes.BudgetCeilingCNY,
		RequirementConfirmation: requirementConfirmationDTO{
			Status: string(detail.Axes.Confirmation.Status), ConfirmedRevision: detail.Axes.Confirmation.ConfirmedRevision,
			ConfirmedAt: detail.Axes.Confirmation.ConfirmedAt, ConfirmedReviewHash: detail.Axes.Confirmation.ConfirmedReviewHash,
		},
		BuildRelation: buildRelationDTO{
			Status: string(detail.Axes.Build.Status), Version: detail.Axes.Build.Version,
			SnapshotID: detail.Axes.Build.SnapshotID, ReviewHash: detail.Axes.Build.ReviewHash,
			BuilderInputHash: detail.Axes.Build.BuilderInputHash,
		},
		ActiveRun:     active,
		LastError:     detail.Session.LastError,
		RecoveryPhase: detail.Session.RecoveryPhase,
		Degraded:      detail.Degraded,
	}
	if readiness != nil {
		out.RequirementReadiness = &requirementReadinessDTO{
			Status: readiness.Status, MissingFields: readiness.MissingFields,
			BlockingConflicts: readiness.BlockingConflicts, UnsupportedCapabilities: readiness.UnsupportedCapabilities,
			NextQuestion: detail.Axes.NextQuestion, ConfirmationEligible: readiness.ConfirmationEligible,
			EffectiveDefaults: readiness.EffectiveDefaults,
		}
	}
	if detail.Axes.ReviewHash != "" {
		hash := detail.Axes.ReviewHash
		out.ReviewHash = &hash
	}
	return out
}
