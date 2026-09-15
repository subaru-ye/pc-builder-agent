package planningeval

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	migrations "github.com/subaru-ye/pc-builder-agent/db"
	"github.com/subaru-ye/pc-builder-agent/internal/planning"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/runevents"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func Hash(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func Load(raw []byte) (Suite, error) {
	var s Suite
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return s, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return s, fmt.Errorf("suite must contain exactly one JSON document")
	}
	if s.Version == "" || s.Provenance == "" || len(s.Cases) == 0 || len(s.Catalog.Candidates) == 0 {
		return s, fmt.Errorf("suite identity, catalog and cases required")
	}
	seen := map[string]bool{}
	for _, c := range s.Cases {
		if c.ID == "" || seen[c.ID] || c.Source == "" || len(c.Steps) == 0 {
			return s, fmt.Errorf("invalid or duplicate case %q", c.ID)
		}
		seen[c.ID] = true
		if c.PreviousBuild != nil {
			if err := validatePreviousBuild(*c.PreviousBuild); err != nil {
				return s, fmt.Errorf("%s previous build: %w", c.ID, err)
			}
		}
		for _, step := range c.Steps {
			if s.Live && (len(step.Screen) != 0 || len(step.Builder) != 0) {
				return s, fmt.Errorf("live suite must not contain model oracles")
			}
			switch step.Kind {
			case "message":
				if !s.Live && len(step.Screen) == 0 {
					return s, fmt.Errorf("missing screening oracle in %s", c.ID)
				}
			case "confirm", "edit", "refresh", "retry":
			default:
				return s, fmt.Errorf("unknown step kind %q", step.Kind)
			}
		}
	}
	return s, nil
}

// VerifyProvenance rejects fixture drift before any database or model work.
func VerifyProvenance(raw, provenance []byte) error {
	var p struct {
		SuiteSHA256 string `json:"suite_sha256"`
	}
	if err := json.Unmarshal(provenance, &p); err != nil {
		return err
	}
	if p.SuiteSHA256 == "" || p.SuiteSHA256 != Hash(raw) {
		return fmt.Errorf("frozen suite SHA256 differs from provenance; create and review a new fixture revision")
	}
	return nil
}

// Prepare only accepts a fresh, explicitly isolated database. The launcher
// creates its own container; no DATABASE_URL, PG_TEST_DSN or .env is consumed.
func Prepare(ctx context.Context, dsn string, fixture CatalogFixture) error {
	u, err := url.Parse(dsn)
	if err != nil || !strings.HasPrefix(u.Path, "/peval_") || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		return fmt.Errorf("isolated localhost peval_ database required")
	}
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("cannot connect to isolated evaluation database")
	}
	defer c.Close(ctx)
	var n int
	if err = c.QueryRow(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'").Scan(&n); err != nil || n != 0 {
		return fmt.Errorf("evaluation database must be empty")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	goose.SetBaseFS(migrations.Migrations)
	goose.SetLogger(goose.NopLogger())
	_ = goose.SetDialect("postgres")
	err = goose.UpContext(ctx, db, "migrations")
	_ = db.Close()
	if err != nil {
		return err
	}
	var snap int64
	if err = c.QueryRow(ctx, "INSERT INTO price_snapshots(snapshot_date,file_sha256) VALUES($1,$2) RETURNING id", fixture.Date, "planning-eval-frozen").Scan(&snap); err != nil {
		return err
	}
	for _, p := range fixture.Candidates {
		if _, err = c.Exec(ctx, "INSERT INTO parts(sku,category,brand,model,specs) VALUES($1,$2,$3,$4,$5)", p.ID, p.Category, p.Brand, p.Model, p.Specs); err != nil {
			return err
		}
		if p.Price != nil {
			metadata, ok := fixture.PriceMetadata[p.ID]
			if !ok {
				metadata = PriceFixture{Source: "frozen-eval-recording", AvailabilityBasis: "unknown"}
			}
			if _, err = c.Exec(ctx, "INSERT INTO prices(snapshot_id,sku,price_cny,source,observed_at,price_type,availability_basis) VALUES($1,$2,$3,$4,$5,$6,$7)", snap, p.ID, *p.Price, metadata.Source, metadata.ObservedAt, metadata.PriceType, metadata.AvailabilityBasis); err != nil {
				return err
			}
		}
	}
	for _, e := range fixture.Evidence {
		id, field := e.CandidateID, e.Field
		if id == "" {
			parts := strings.Split(e.Title, " · ")
			if len(parts) < 2 {
				continue
			}
			id, field = parts[0], parts[1]
		}
		if _, err = c.Exec(ctx, `INSERT INTO part_evidence(evidence_id,sku,field_path,value,source_id,source_url,raw_sha256,method,evidence_status,captured_at,evidence_excerpt) VALUES($1,$2,$3,'{}','recording',$4,$1,'manual','verified',$5,$6)`, e.ID, id, field, e.URL, e.CapturedAt, e.Text); err != nil {
			return err
		}
	}
	return nil
}
func Run(ctx context.Context, dsn string, suite Suite, raw []byte, models Models) (Report, error) {
	start := time.Now()
	catalogRaw, _ := json.Marshal(suite.Catalog)
	report := Report{SchemaVersion: 1, SuiteVersion: suite.Version, SuiteSHA256: Hash(raw), CatalogSHA256: Hash(catalogRaw), Mode: "offline_oracle", Classifications: map[string]int{}, Limitations: []string{"Oracle outputs verify execution contracts, not live semantic accuracy.", "Web/search are fixture-only. Latency excludes real model and external service latency.", "Tokens/cash are unknown unless reported by a real provider; protocol calls are counted separately.", "No UI or production capacity claim. Exact selections are frozen regression oracles, not the only acceptable live solutions."}}
	if models.Screening != nil || models.Builder != nil {
		report.Mode = "live_models_offline_tools"
		report.Limitations = []string{"Real fixed chat models, fixture-only web; embedding disabled.", "Graded against predeclared live expectations, not oracle outputs.", "Cash cost is unknown; actual provider token usage is retained.", "No UI or production capacity claim."}
	}
	liveRequested := models.Screening != nil || models.Builder != nil
	if suite.Live != liveRequested || (suite.Live && (models.Screening == nil || models.Builder == nil || models.MaxCalls <= 0)) {
		return report, fmt.Errorf("live suite requires both real models and a positive shared call limit; replay requires offline suite")
	}
	if err := Prepare(ctx, dsn, suite.Catalog); err != nil {
		return report, err
	}
	st, err := store.New(ctx, dsn)
	if err != nil {
		return report, err
	}
	defer st.Close()
	g := &gateway{store: st, pages: suite.Pages, models: models}
	for _, c := range suite.Cases {
		cr := CaseRecord{ID: c.ID, Pass: true}
		svc, err := product.NewService(ctx, st, g, runevents.NewMemory())
		if err != nil {
			return report, err
		}
		owner := uuid.NewString()
		ws, err := svc.CreateSession(ctx, owner, uuid.NewString())
		if err != nil {
			return report, err
		}
		if c.PreviousBuild != nil {
			base, e := seedPreviousBuild(ctx, st, ws.ID, *c.PreviousBuild)
			if e == nil {
				e = g.journal(map[string]any{"event": "historical_precondition", "case_id": c.ID, "source": c.PreviousBuild.Source, "build": base})
			}
			if e != nil {
				_ = svc.Shutdown(ctx)
				return report, e
			}
		}
		var lastStart product.StartResult
		var lastStep Step
		var lastRequest string
		for _, step := range c.Steps {
			stepStart := time.Now()
			g.begin(step)
			if e := g.journal(map[string]any{"event": "step_started", "case_id": c.ID, "step": len(cr.Steps) + 1, "kind": step.Kind, "text": step.Text}); e != nil {
				_ = svc.Shutdown(ctx)
				return report, e
			}
			before, err := svc.GetSession(ctx, owner, ws.ID)
			if err != nil {
				return report, err
			}
			history := map[int]store.BuildVersion{}
			for v := 1; v <= before.Session.VersionCount; v++ {
				b, e := st.BuildByVersion(ctx, ws.ID, v)
				if e != nil {
					return report, e
				}
				history[v] = b
			}
			requestID := uuid.NewString()
			var started product.StartResult
			switch step.Kind {
			case "message":
				started, err = svc.StartMessage(ctx, owner, ws.ID, requestID, step.Text)
			case "confirm":
				started, err = svc.StartConfirm(ctx, owner, ws.ID, requestID)
			case "edit":
				var state schemas.RequirementState
				_ = json.Unmarshal(before.Session.RequirementState, &state)
				_, err = svc.EditRequirement(ctx, owner, ws.ID, requestID, product.RequirementEdit{ExpectedRevision: state.Revision, Operations: step.Edit})
			case "retry":
				if lastStep.Kind == "message" {
					started, err = svc.StartMessage(ctx, owner, ws.ID, lastRequest, lastStep.Text)
				} else {
					started, err = svc.StartConfirm(ctx, owner, ws.ID, lastRequest)
				}
			case "refresh": // The following GetSession exercises the production read model.
			}
			if err == nil && started.Run.ID != "" {
				err = waitRun(ctx, svc, owner, started.Run.ID, suite.Live)
			}
			record := g.snapshot()
			record.DurationMS = time.Since(stepStart).Milliseconds()
			if err != nil {
				record.Error = err.Error()
			}
			after, readErr := svc.GetSession(ctx, owner, ws.ID)
			if readErr != nil {
				return report, readErr
			}
			record.Versions = after.Session.VersionCount
			_ = json.Unmarshal(after.Session.RequirementState, &record.State)
			if len(after.Messages) > 0 {
				record.Reply = after.Messages[len(after.Messages)-1].Content
			}
			var envelope struct{ Result planning.Result }
			if len(after.Proposal) > 0 && json.Unmarshal(after.Proposal, &envelope) == nil {
				record.Result = &envelope.Result
			}
			for v, b := range history {
				now, e := st.BuildByVersion(ctx, ws.ID, v)
				record.Checks = append(record.Checks, Check{fmt.Sprintf("immutable_version:%d", v), e == nil && reflect.DeepEqual(now, b), "historical build, evidence, requirement and quote snapshot"})
			}
			if record.PlanningInput != nil && before.Session.VersionCount > 0 {
				base := history[before.Session.VersionCount]
				record.Checks = append(record.Checks, Check{"builder_received_base", jsonEqual(record.PlanningInput.BaseDraft, base.Draft), "actual Builder input must retain the latest formal configuration"})
				if record.Versions == before.Session.VersionCount+1 {
					now, e := st.BuildByVersion(ctx, ws.ID, record.Versions)
					record.Checks = append(record.Checks, Check{"formal_parent_link", e == nil && now.ParentID != nil && *now.ParentID == base.ID, "new formal version must link its actual prior build"})
				}
			}
			if step.Kind == "retry" {
				record.Checks = append(record.Checks, Check{"idempotent_run", started.Duplicate && started.Run.ID == lastStart.Run.ID, "retry must not create another run"})
			}
			if step.Kind == "refresh" || step.Kind == "retry" {
				record.Checks = append(record.Checks, Check{"read_without_execution", len(record.Trace) == 0 && reflect.DeepEqual(before.Session.RequirementState, after.Session.RequirementState) && reflect.DeepEqual(before.Proposal, after.Proposal), "no model call or snapshot rewrite"})
			}
			var previous *StepRecord
			if len(cr.Steps) > 0 {
				previous = &cr.Steps[len(cr.Steps)-1]
			}
			Grade(&record, step.Expect, previous)
			for _, check := range record.Checks {
				cr.Pass = cr.Pass && check.Pass
			}
			cr.Steps = append(cr.Steps, record)
			if e := g.journal(map[string]any{"event": "step_completed", "case_id": c.ID, "step": len(cr.Steps), "record": record}); e != nil {
				_ = svc.Shutdown(ctx)
				return report, e
			}
			if step.Kind == "message" || step.Kind == "confirm" {
				lastStart, lastStep, lastRequest = started, step, requestID
			}
		}
		// A new session for the same owner must not inherit a friend's requirements.
		fresh, e := svc.CreateSession(ctx, owner, uuid.NewString())
		if e != nil {
			return report, e
		}
		d, e := svc.GetSession(ctx, owner, fresh.ID)
		if e != nil {
			return report, e
		}
		var freshState schemas.RequirementState
		_ = json.Unmarshal(d.Session.RequirementState, &freshState)
		isolated := true
		for _, f := range freshState.Fields {
			if f.Status == "active" {
				isolated = false
			}
		}
		last := &cr.Steps[len(cr.Steps)-1]
		last.Checks = append(last.Checks, Check{"new_session_has_no_personal_requirements", isolated, "same owner, new session"})
		cr.Pass = cr.Pass && isolated
		_ = svc.Shutdown(ctx)
		if cr.Pass {
			report.Passed++
		}
		report.Cases = append(report.Cases, cr)
	}
	var totalTokens int64
	allTokens := true
	for _, c := range report.Cases {
		for _, r := range c.Steps {
			report.Classifications[r.Classification]++
			for _, t := range r.Trace {
				if t.ProviderCalled {
					report.ActualModelRequests++
				}
				if t.Role == "screening" {
					report.ScreeningCalls++
				} else {
					report.BuilderCalls++
				}
				if t.Tokens == nil {
					allTokens = false
				} else {
					totalTokens += int64(*t.Tokens)
				}
			}
			if r.PlanningInput != nil && r.Result != nil {
				report.ToolCalls += r.Result.ToolCalls
				report.SearchCalls += r.Result.SearchCalls
				report.PageCalls += r.Result.PageCalls
			}
		}
	}
	if allTokens {
		report.Tokens = &totalTokens
	}
	report.DurationMS = time.Since(start).Milliseconds()
	return report, nil
}
func waitRun(ctx context.Context, svc *product.Service, owner, id string, live bool) error {
	limit := 30 * time.Second
	if live {
		limit = 11 * time.Minute // Product timeout is 10 minutes; observe its terminal state.
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		r, err := svc.GetRun(ctx, owner, id)
		if err != nil {
			return err
		}
		if r.Status != store.RunRunning {
			if r.Status != store.RunSucceeded {
				return fmt.Errorf("product run %s: %s", r.Status, r.Error)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("evaluation run timed out")
		case <-ticker.C:
		}
	}
}
