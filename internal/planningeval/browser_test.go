package planningeval

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/subaru-ye/pc-builder-agent/internal/presenter"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/producthttp"
	"github.com/subaru-ye/pc-builder-agent/internal/runevents"
	"github.com/subaru-ye/pc-builder-agent/internal/sharing"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/genai"
)

type browserOfflineRedis struct{}

func (browserOfflineRedis) Available() bool            { return false }
func (browserOfflineRedis) Ping(context.Context) error { return nil }

// Explicit test server only. The launcher supplies a fresh peval_ database;
// The legacy mode replays seven real outputs; current-catalog mode uses the
// explicitly authored C123-002 oracle. Neither mode calls a provider.
func TestRecordedBudgetBrowserServer(t *testing.T) {
	currentCatalog := os.Getenv("PLANNING_CURRENT_BROWSER") == "1"
	if os.Getenv("PLANNING_RECORDED_BROWSER") != "1" && !currentCatalog {
		t.Skip("explicit browser replay only")
	}
	ctx := context.Background()
	raw, err := os.ReadFile("../planning/testdata/budget_clarification_success_recording_20260915.json")
	if err != nil {
		t.Fatal(err)
	}
	var recording struct {
		CatalogFile string `json:"catalog_file"`
		CatalogSHA  string `json:"catalog_sha256"`
		Responses   []*genai.Content
	}
	if err = json.Unmarshal(raw, &recording); err != nil {
		t.Fatal(err)
	}
	catalogRaw, err := os.ReadFile(recording.CatalogFile)
	if err != nil || Hash(catalogRaw) != recording.CatalogSHA {
		t.Fatal("frozen catalog changed", err)
	}
	suite, err := Load(catalogRaw)
	if err != nil {
		t.Fatal(err)
	}
	var c Case
	for _, candidate := range suite.Cases {
		if candidate.ID == "L2-104" {
			c = candidate
		}
	}
	if c.PreviousBuild == nil || len(recording.Responses) != 7 {
		t.Fatal("recorded case missing")
	}
	step := Step{Kind: "confirm", Builder: recording.Responses}
	expectedCalls, expectedTotal := 7, "6754.00"
	if currentCatalog {
		raw, err = os.ReadFile("testdata/current-123-20260915-r2/mechanisms/suite.json")
		if err != nil {
			t.Fatal(err)
		}
		suite, err = Load(raw)
		if err != nil {
			t.Fatal(err)
		}
		c = suite.Cases[1] // Retired historical parts -> current priced alternatives.
		if c.ID != "C123-002" || c.PreviousBuild.Snapshot == nil {
			t.Fatal("current historical fixture changed")
		}
		step = c.Steps[1]
		expectedCalls, expectedTotal = 4, "6483.00"
	}
	dsn := os.Getenv("PLANNING_EVAL_DSN")
	if err = Prepare(ctx, dsn, suite.Catalog); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	g := &gateway{store: st, pages: suite.Pages, models: Models{MaxCalls: expectedCalls}}
	g.begin(step)
	events := runevents.NewMemory()
	svc, err := product.NewService(ctx, st, g, events)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Shutdown(ctx)
	owner := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	ws, err := svc.CreateSession(ctx, owner, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	base, err := seedPreviousBuild(ctx, st, ws.ID, *c.PreviousBuild)
	if err != nil {
		t.Fatal(err)
	}
	if currentCatalog {
		if err = seedSnapshotConfirmation(ctx, st, svc, owner, ws.ID, *c.PreviousBuild); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err = svc.EditRequirement(ctx, owner, ws.ID, uuid.NewString(), product.RequirementEdit{Operations: c.Steps[0].Edit}); err != nil {
			t.Fatal(err)
		}
	}
	p := presenter.New(st)
	codec, err := sharing.NewTokenCodec(base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("s", 32))))
	if err != nil {
		t.Fatal(err)
	}
	const webURL = "http://127.0.0.1:3107"
	shares, err := sharing.New(st, p, codec, webURL)
	if err != nil {
		t.Fatal(err)
	}
	api, err := producthttp.New(svc, p, shares, events, st, browserOfflineRedis{}, producthttp.Config{PublicWebBaseURL: webURL, BuildsvcURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Addr: "127.0.0.1:18089", ReadHeaderTimeout: 5 * time.Second}
	mux := http.NewServeMux()
	mux.Handle("/", api.Handler())
	mux.HandleFunc("GET /__offline/start", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "pcb_anonymous_id", Value: owner, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, webURL+"/s/"+ws.ID, http.StatusSeeOther)
	})
	mux.HandleFunc("GET /__offline/observations", func(w http.ResponseWriter, _ *http.Request) {
		g.mu.Lock()
		calls := g.calls
		g.mu.Unlock()
		detail, e := svc.GetSession(ctx, owner, ws.ID)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"session_id": ws.ID, "protocol_calls": calls, "actual_model_requests": 0, "detail": detail})
	})
	mux.HandleFunc("POST /__offline/shutdown", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		go server.Shutdown(ctx)
	})
	server.Handler = mux
	defer server.Close()
	if err = server.ListenAndServe(); err != http.ErrServerClosed {
		t.Fatal(err)
	}
	detail, err := svc.GetSession(ctx, owner, ws.ID)
	if err != nil || detail.Session.VersionCount != 2 || g.calls != expectedCalls {
		t.Fatalf("browser did not complete v2 exactly once: calls=%d versions=%d err=%v", g.calls, detail.Session.VersionCount, err)
	}
	after, err := st.BuildByVersion(ctx, ws.ID, 1)
	if err != nil || !reflect.DeepEqual(base, after) {
		t.Fatal("historical version changed", err)
	}
	latest, err := st.BuildByVersion(ctx, ws.ID, 2)
	if err != nil || !strings.Contains(string(latest.Quote), expectedTotal) {
		t.Fatal("wrong delivered quote", err)
	}
	t.Log("browser completed: offline responses, v2 persisted, v1 unchanged", expectedCalls, ws.ID)
}
