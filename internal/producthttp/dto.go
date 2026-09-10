package producthttp

import (
	"encoding/json"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type sessionSummaryDTO struct {
	SchemaVersion int                `json:"schema_version"`
	ID            string             `json:"id"`
	Title         string             `json:"title"`
	Phase         store.SessionPhase `json:"phase"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	VersionCount  int                `json:"version_count"`
}

type messageDTO struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	Role          string    `json:"role"`
	Content       string    `json:"content"`
	RunID         *string   `json:"run_id"`
	CreatedAt     time.Time `json:"created_at"`
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

type sessionDTO struct {
	sessionSummaryDTO
	Messages                  []messageDTO        `json:"messages"`
	PendingRequirement        json.RawMessage     `json:"pending_requirement"`
	RequirementState          json.RawMessage     `json:"requirement_state"`
	ConfirmedRequirementState json.RawMessage     `json:"confirmed_requirement_state"`
	ConfirmedRequirement      json.RawMessage     `json:"confirmed_requirement"`
	ConfirmedAt               *time.Time          `json:"confirmed_at"`
	RequirementStatus         string              `json:"requirement_status"`
	MissingFields             []string            `json:"missing_fields"`
	ActiveRun                 *runDTO             `json:"active_run"`
	LastError                 json.RawMessage     `json:"last_error"`
	RecoveryPhase             *store.SessionPhase `json:"recovery_phase"`
	Degraded                  bool                `json:"degraded"`
}

func toSessionSummary(s store.WebSession) sessionSummaryDTO {
	return sessionSummaryDTO{
		SchemaVersion: 1, ID: s.ID, Title: s.Title, Phase: s.Phase,
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
			RunID: m.RunID, CreatedAt: m.CreatedAt,
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
	lastError := detail.Session.LastError
	if len(lastError) == 0 {
		lastError = json.RawMessage("null")
	}
	return sessionDTO{
		sessionSummaryDTO:         toSessionSummary(detail.Session),
		Messages:                  messages,
		PendingRequirement:        pending,
		RequirementState:          detail.Session.RequirementState,
		ConfirmedRequirementState: detail.Session.ConfirmedRequirementState,
		ConfirmedRequirement:      detail.Session.ConfirmedRequirement,
		ConfirmedAt:               detail.Session.ConfirmedAt,
		RequirementStatus:         detail.RequirementStatus,
		MissingFields:             detail.MissingFields,
		ActiveRun:                 active,
		LastError:                 lastError,
		RecoveryPhase:             detail.Session.RecoveryPhase,
		Degraded:                  detail.Degraded,
	}
}
