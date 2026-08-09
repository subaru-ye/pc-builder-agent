// Package producthttp 实现 P7 产品 HTTP transport；业务状态转换全部委托 product.Service。
package producthttp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/subaru-ye/pc-builder-agent/internal/presenter"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/runevents"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/sharing"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

const (
	anonymousCookieName = "pcb_anonymous_id"
	maxJSONBody         = 64 << 10
	maxMessageRunes     = 4000
)

type ProductService interface {
	CreateSession(context.Context, string, string) (store.WebSession, error)
	ListSessions(context.Context, string) ([]store.WebSession, error)
	GetSession(context.Context, string, string) (product.SessionDetail, error)
	GetRun(context.Context, string, string) (store.AgentRun, error)
	OwnSession(context.Context, string, string) error
	ReplaceRequirement(context.Context, string, string, json.RawMessage) error
	StartMessage(context.Context, string, string, string, string) (product.StartResult, error)
	StartConfirm(context.Context, string, string, string) (product.StartResult, error)
}

type BuildPresenter interface {
	Builds(context.Context, string) ([]presenter.BuildSummary, error)
	Build(context.Context, string, int) (presenter.BuildView, error)
	Diff(context.Context, string, int, int) (presenter.BuildDiff, error)
	Markdown(context.Context, string, int) (string, error)
}

type ShareService interface {
	Create(context.Context, string, string, int, string) (sharing.Share, bool, error)
	List(context.Context, string, string, int) ([]sharing.ShareRecord, error)
	RevokeByID(context.Context, string, string, int, string) error
	RevokeByToken(context.Context, string, string) error
	Public(context.Context, string) (sharing.PublicBuildView, error)
	PublicMarkdown(context.Context, string) (string, int, string, error)
}

type DatabaseHealth interface{ Ping(context.Context) error }
type RedisHealth interface {
	Available() bool
	Ping(context.Context) error
}

type Config struct {
	PublicWebBaseURL string
	AllowedOrigin    string
	BuildsvcURL      string
}

type API struct {
	service ProductService
	builds  BuildPresenter
	shares  ShareService
	events  runevents.Store
	db      DatabaseHealth
	redis   RedisHealth
	cfg     Config
	secure  bool
	client  *http.Client
}

func New(service ProductService, builds BuildPresenter, shares ShareService, events runevents.Store, db DatabaseHealth, redis RedisHealth, cfg Config) (*API, error) {
	if service == nil || builds == nil || shares == nil || events == nil || db == nil || redis == nil {
		return nil, fmt.Errorf("product http: service/builds/shares/events/db/redis 不能为空")
	}
	if cfg.PublicWebBaseURL == "" {
		cfg.PublicWebBaseURL = "http://localhost:3000"
	}
	u, err := url.Parse(cfg.PublicWebBaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("product http: PUBLIC_WEB_BASE_URL 无效:%q", cfg.PublicWebBaseURL)
	}
	if cfg.AllowedOrigin == "" {
		cfg.AllowedOrigin = u.Scheme + "://" + u.Host
	}
	return &API{
		service: service, builds: builds, shares: shares, events: events, db: db, redis: redis, cfg: cfg,
		secure: u.Scheme == "https", client: &http.Client{Timeout: 2 * time.Second},
	}, nil
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /readyz", a.ready)
	mux.HandleFunc("POST /api/v1/sessions", a.createSession)
	mux.HandleFunc("GET /api/v1/sessions", a.listSessions)
	mux.HandleFunc("GET /api/v1/sessions/{session_id}", a.getSession)
	mux.HandleFunc("POST /api/v1/sessions/{session_id}/messages", a.createMessageRun)
	mux.HandleFunc("PATCH /api/v1/sessions/{session_id}/requirement", a.replaceRequirement)
	mux.HandleFunc("POST /api/v1/sessions/{session_id}/requirement/confirm", a.confirmRequirement)
	mux.HandleFunc("GET /api/v1/runs/{run_id}", a.getRun)
	mux.HandleFunc("GET /api/v1/runs/{run_id}/events", a.streamRunEvents)
	mux.HandleFunc("GET /api/v1/sessions/{session_id}/builds", a.listBuilds)
	mux.HandleFunc("GET /api/v1/sessions/{session_id}/builds/{version}", a.getBuild)
	mux.HandleFunc("GET /api/v1/sessions/{session_id}/diff", a.getDiff)
	mux.HandleFunc("GET /api/v1/sessions/{session_id}/builds/{version}/export.md", a.exportBuild)
	mux.HandleFunc("POST /api/v1/sessions/{session_id}/builds/{version}/shares", a.createBuildShare)
	mux.HandleFunc("GET /api/v1/sessions/{session_id}/builds/{version}/shares", a.listBuildShares)
	mux.HandleFunc("DELETE /api/v1/sessions/{session_id}/builds/{version}/shares/{share_id}", a.revokeBuildShareByID)
	mux.HandleFunc("DELETE /api/v1/shares/{token}", a.revokeBuildShareByToken)
	mux.HandleFunc("GET /api/v1/public/shares/{token}", a.getPublicBuildShare)
	mux.HandleFunc("GET /api/v1/public/shares/{token}/export.md", a.exportPublicBuildShare)
	return a.requestMiddleware(a.corsMiddleware(mux))
}

type contextKey string

const requestIDKey contextKey = "request_id"

func (a *API) requestMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.NewString()
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, requestID)))
	})
}

func (a *API) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if a.cfg.AllowedOrigin == "" || origin != a.cfg.AllowedOrigin {
				a.writeProblem(w, r, product.NewProblem("invalid_request", "不允许的跨域来源", 403,
					"Origin 不在 WEB_ALLOWED_ORIGIN 中。", requestID(r)))
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key, Last-Event-ID")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestID(r *http.Request) string {
	v, _ := r.Context().Value(requestIDKey).(string)
	return v
}

func (a *API) owner(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(anonymousCookieName); err == nil && validOwnerID(c.Value) {
		return c.Value
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	owner := base64.RawURLEncoding.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name: anonymousCookieName, Value: owner, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: a.secure,
		MaxAge: 365 * 24 * 60 * 60, Expires: time.Now().Add(365 * 24 * time.Hour),
	})
	return owner
}

func validOwnerID(value string) bool {
	b, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(b) == 32
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": 1, "status": "ok"})
}

func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	deps := map[string]string{"postgres": "ok", "redis": "ok", "buildsvc": "ok"}
	status := "ready"
	if err := a.db.Ping(ctx); err != nil {
		deps["postgres"] = "unavailable"
		status = "unavailable"
	}
	if !a.redis.Available() {
		deps["redis"] = "degraded"
		if status == "ready" {
			status = "degraded"
		}
	} else if err := a.redis.Ping(ctx); err != nil {
		deps["redis"] = "unavailable"
		status = "unavailable"
	}
	buildsvcURL := strings.TrimRight(a.cfg.BuildsvcURL, "/") + "/.well-known/agent-card.json"
	resp, err := a.client.Get(buildsvcURL)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		deps["buildsvc"] = "unavailable"
		status = "unavailable"
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
	code := http.StatusOK
	if status != "ready" {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"schema_version": 1, "status": status, "dependencies": deps})
}

func (a *API) createSession(w http.ResponseWriter, r *http.Request) {
	key, ok := a.idempotencyKey(w, r)
	if !ok {
		return
	}
	ws, err := a.service.CreateSession(r.Context(), a.owner(w, r), key)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	detail, err := a.service.GetSession(r.Context(), ws.OwnerID, ws.ID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toSession(detail))
}

func (a *API) listSessions(w http.ResponseWriter, r *http.Request) {
	items, err := a.service.ListSessions(r.Context(), a.owner(w, r))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	out := make([]sessionSummaryDTO, 0, len(items))
	for _, item := range items {
		out = append(out, toSessionSummary(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": 1, "sessions": out})
}

func (a *API) getSession(w http.ResponseWriter, r *http.Request) {
	detail, err := a.service.GetSession(r.Context(), a.owner(w, r), r.PathValue("session_id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toSession(detail))
}

func (a *API) createMessageRun(w http.ResponseWriter, r *http.Request) {
	key, ok := a.idempotencyKey(w, r)
	if !ok {
		return
	}
	var body struct {
		SchemaVersion int    `json:"schema_version"`
		Text          string `json:"text"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	body.Text = strings.TrimSpace(body.Text)
	if body.SchemaVersion != 1 || body.Text == "" || utf8.RuneCountInString(body.Text) > maxMessageRunes {
		a.writeProblem(w, r, product.NewProblem("invalid_request", "消息格式无效", 400,
			"schema_version 必须为 1，text 长度必须为 1–4000 个字符。", requestID(r)))
		return
	}
	result, err := a.service.StartMessage(r.Context(), a.owner(w, r), r.PathValue("session_id"), key, body.Text)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.writeStartResult(w, result)
}

func (a *API) replaceRequirement(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.idempotencyKey(w, r); !ok {
		return
	}
	raw, ok := a.readRawJSON(w, r)
	if !ok {
		return
	}
	if _, err := schemas.DecodeRequirementSpec(raw); err != nil {
		a.writeProblem(w, r, product.NewProblem("schema_validation_failed", "需求单校验失败", 422,
			err.Error(), requestID(r)))
		return
	}
	if err := a.service.ReplaceRequirement(r.Context(), a.owner(w, r), r.PathValue("session_id"), raw); err != nil {
		a.writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (a *API) confirmRequirement(w http.ResponseWriter, r *http.Request) {
	key, ok := a.idempotencyKey(w, r)
	if !ok {
		return
	}
	result, err := a.service.StartConfirm(r.Context(), a.owner(w, r), r.PathValue("session_id"), key)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.writeStartResult(w, result)
}

func (a *API) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := a.service.GetRun(r.Context(), a.owner(w, r), r.PathValue("run_id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toRun(run))
}

func (a *API) listBuilds(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	if !a.ownsSession(w, r, sessionID) {
		return
	}
	items, err := a.builds.Builds(r.Context(), sessionID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": 1, "builds": items})
}

func (a *API) getBuild(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	if !a.ownsSession(w, r, sessionID) {
		return
	}
	version, ok := a.positiveInt(w, r, r.PathValue("version"), "version")
	if !ok {
		return
	}
	view, err := a.builds.Build(r.Context(), sessionID, version)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) getDiff(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	if !a.ownsSession(w, r, sessionID) {
		return
	}
	from, ok := a.positiveInt(w, r, r.URL.Query().Get("from"), "from")
	if !ok {
		return
	}
	to, ok := a.positiveInt(w, r, r.URL.Query().Get("to"), "to")
	if !ok {
		return
	}
	diff, err := a.builds.Diff(r.Context(), sessionID, from, to)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

func (a *API) exportBuild(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	if !a.ownsSession(w, r, sessionID) {
		return
	}
	version, ok := a.positiveInt(w, r, r.PathValue("version"), "version")
	if !ok {
		return
	}
	view, err := a.builds.Build(r.Context(), sessionID, version)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	markdown, err := a.builds.Markdown(r.Context(), sessionID, version)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", markdownDisposition(version, view.Quote.SnapshotDate))
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, markdown)
}

func (a *API) createBuildShare(w http.ResponseWriter, r *http.Request) {
	key, ok := a.idempotencyKey(w, r)
	if !ok {
		return
	}
	version, ok := a.positiveInt(w, r, r.PathValue("version"), "version")
	if !ok {
		return
	}
	share, created, err := a.shares.Create(r.Context(), a.owner(w, r), r.PathValue("session_id"), version, key)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, share)
}

func (a *API) listBuildShares(w http.ResponseWriter, r *http.Request) {
	version, ok := a.positiveInt(w, r, r.PathValue("version"), "version")
	if !ok {
		return
	}
	shares, err := a.shares.List(r.Context(), a.owner(w, r), r.PathValue("session_id"), version)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": 1, "shares": shares})
}

func (a *API) revokeBuildShareByID(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.idempotencyKey(w, r); !ok {
		return
	}
	version, ok := a.positiveInt(w, r, r.PathValue("version"), "version")
	if !ok {
		return
	}
	err := a.shares.RevokeByID(r.Context(), a.owner(w, r), r.PathValue("session_id"), version, r.PathValue("share_id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) revokeBuildShareByToken(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.idempotencyKey(w, r); !ok {
		return
	}
	if err := a.shares.RevokeByToken(r.Context(), a.owner(w, r), r.PathValue("token")); err != nil {
		a.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) getPublicBuildShare(w http.ResponseWriter, r *http.Request) {
	view, err := a.shares.Public(r.Context(), r.PathValue("token"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, view)
}

func (a *API) exportPublicBuildShare(w http.ResponseWriter, r *http.Request) {
	markdown, version, snapshot, err := a.shares.PublicMarkdown(r.Context(), r.PathValue("token"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", markdownDisposition(version, snapshot))
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, markdown)
}

func markdownDisposition(version int, snapshot string) string {
	if len(snapshot) != len("2006-01-02") {
		snapshot = "unknown-date"
	}
	return fmt.Sprintf(`attachment; filename="pc-build-v%d-%s.md"`, version, snapshot)
}

func (a *API) ownsSession(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	if err := a.service.OwnSession(r.Context(), a.owner(w, r), sessionID); err != nil {
		a.writeError(w, r, err)
		return false
	}
	return true
}

func (a *API) positiveInt(w http.ResponseWriter, r *http.Request, raw, name string) (int, bool) {
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		a.writeProblem(w, r, product.NewProblem("invalid_request", "版本参数无效", 400,
			name+" 必须是正整数。", requestID(r)))
		return 0, false
	}
	return value, true
}

func (a *API) writeStartResult(w http.ResponseWriter, result product.StartResult) {
	status := http.StatusAccepted
	if result.Duplicate && result.Run.Status != store.RunRunning {
		status = http.StatusOK
	}
	writeJSON(w, status, toRun(result.Run))
}

func (a *API) idempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := r.Header.Get("Idempotency-Key")
	if _, err := uuid.Parse(key); err != nil {
		a.writeProblem(w, r, product.NewProblem("invalid_request", "缺少有效幂等键", 400,
			"Idempotency-Key 必须是 UUID。", requestID(r)))
		return "", false
	}
	return key, true
}

func (a *API) decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		a.writeProblem(w, r, product.NewProblem("invalid_request", "Content-Type 无效", 415,
			"请求体必须使用 application/json。", requestID(r)))
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		a.writeProblem(w, r, product.NewProblem("invalid_request", "JSON 请求无效", 400, err.Error(), requestID(r)))
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		a.writeProblem(w, r, product.NewProblem("invalid_request", "JSON 请求无效", 400,
			"请求体只能包含一个 JSON 对象。", requestID(r)))
		return false
	}
	return true
}

func (a *API) readRawJSON(w http.ResponseWriter, r *http.Request) (json.RawMessage, bool) {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		a.writeProblem(w, r, product.NewProblem("invalid_request", "Content-Type 无效", 415,
			"请求体必须使用 application/json。", requestID(r)))
		return nil, false
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if err != nil || !json.Valid(b) {
		a.writeProblem(w, r, product.NewProblem("invalid_request", "JSON 请求无效", 400,
			"请求体必须是单个合法 JSON 对象。", requestID(r)))
		return nil, false
	}
	return json.RawMessage(b), true
}

func (a *API) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var p product.Problem
	if errors.As(err, &p) {
		if p.RequestID == "" {
			p.RequestID = requestID(r)
		}
		a.writeProblem(w, r, p)
		return
	}
	switch {
	case errors.Is(err, store.ErrWebSessionNotFound), errors.Is(err, store.ErrRunNotFound), errors.Is(err, store.ErrBuildNotFound), errors.Is(err, store.ErrShareNotFound):
		a.writeProblem(w, r, product.NewProblem("not_found", "资源不存在", 404, "", requestID(r)))
	case errors.Is(err, store.ErrSessionBusy):
		a.writeProblem(w, r, product.NewProblem("session_busy", "会话正在处理中", 409,
			"同一会话一次只允许一个活动运行。", requestID(r)))
	case errors.Is(err, store.ErrInvalidSessionPhase):
		detail := err.Error()
		a.writeProblem(w, r, product.NewProblem("invalid_session_phase", "当前会话阶段不允许该操作", 409,
			detail, requestID(r)))
	case errors.Is(err, store.ErrIdempotencyConflict), errors.Is(err, store.ErrShareTokenMismatch):
		a.writeProblem(w, r, product.NewProblem("invalid_request", "幂等键冲突", 409,
			"同一个 Idempotency-Key 已用于不同请求。", requestID(r)))
	default:
		a.writeProblem(w, r, product.NewProblem("internal_error", "服务内部错误", 500,
			"请求未完成，请稍后使用新幂等键重试。", requestID(r)))
	}
}

func (a *API) writeProblem(w http.ResponseWriter, _ *http.Request, p product.Problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
