package producthttp

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/subaru-ye/pc-builder-agent/internal/planning"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// Only model decisions are recorded/oracle output; API, state, tools, pricing,
// compatibility and all persistence use production implementations.
type planningReplayGateway struct {
	*requirementReplayGateway
	mu             sync.Mutex
	runIDs         []string
	previousRunIDs []string
	hook           func(*planning.Result) // 测试注入:在归档前篡改生成侧结果(如 F5 复验分歧)
}

// lastPlanningGateway 由 requirementIntegrationAPI 在 planning 模式下登记,供集成测试观察网关入参。
var lastPlanningGateway *planningReplayGateway

// archiveAndStrip 复现 buildsvc 的 F2 行为:完整产物按 run 归档,A2A 只回控制面。
func (g *planningReplayGateway) archiveAndStrip(ctx context.Context, sessionID string, input schemas.PlanningInput, result planning.Result) (product.RemoteResult, error) {
	g.mu.Lock()
	g.runIDs = append(g.runIDs, input.RunID)
	g.previousRunIDs = append(g.previousRunIDs, input.PreviousRunID)
	g.mu.Unlock()
	if err := planning.Archive(ctx, g.store, input.RunID, sessionID, result); err != nil {
		return product.RemoteResult{}, err
	}
	transport := result.ControlPlane()
	return product.RemoteResult{Text: transport.Reply, Planning: &transport}, nil
}

// The browser harness uses the user's saved complete proposal for selected parts
// and real catalog excerpts. Only model decisions are supplied by the oracle.
func seedPlanningRecording(t *testing.T, conn *pgx.Conn, snapshot int64, fixture *requirementReplay) {
	t.Helper()
	ctx := context.Background()
	raw, err := os.ReadFile("../planning/testdata/complete_proposal_recording.json")
	if err != nil {
		t.Fatal(err)
	}
	var saved struct{ Result planning.Result }
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	fixture.Draft = saved.Result.Draft
	if _, err = conn.Exec(ctx, `UPDATE price_snapshots SET snapshot_date=$2 WHERE id=$1`, snapshot, saved.Result.Quote.SnapshotDate); err != nil {
		t.Fatal(err)
	}
	selected := map[string]bool{}
	for _, c := range saved.Result.Candidates {
		selected[c.ID] = true
		if _, err = conn.Exec(ctx, `INSERT INTO parts(sku,category,brand,model,specs) VALUES($1,$2,$3,$4,$5) ON CONFLICT(sku) DO NOTHING`, c.ID, c.Category, c.Brand, c.Model, c.Specs); err != nil {
			t.Fatal(err)
		}
		if _, err = conn.Exec(ctx, `INSERT INTO prices(snapshot_id,sku,price_cny,source) VALUES($1,$2,$3,'saved-user-recording') ON CONFLICT(snapshot_id,sku) DO UPDATE SET price_cny=EXCLUDED.price_cny,source=EXCLUDED.source`, snapshot, c.ID, c.Price); err != nil {
			t.Fatal(err)
		}
		if c.Category == schemas.CategoryMotherboard {
			// Explicit synthetic gap, confined to this disposable test database.
			var specs map[string]json.RawMessage
			_ = json.Unmarshal(c.Specs, &specs)
			specs["memory_speed_max_mts"] = json.RawMessage(`null`)
			gap, _ := json.Marshal(specs)
			if _, err = conn.Exec(ctx, `INSERT INTO parts(sku,category,brand,model,specs) VALUES('mb-offline-gap',$1,$2,$3,$4)`, c.Category, c.Brand, c.Model, gap); err != nil {
				t.Fatal(err)
			}
			if _, err = conn.Exec(ctx, `INSERT INTO prices(snapshot_id,sku,price_cny,source) VALUES($1,'mb-offline-gap',$2,'synthetic-gap-test')`, snapshot, c.Price); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, e := range saved.Result.Evidence {
		parts := strings.Split(e.Title, " · ")
		if len(parts) < 2 || !selected[parts[0]] {
			continue
		}
		if _, err = conn.Exec(ctx, `INSERT INTO part_evidence(evidence_id,sku,field_path,value,source_id,source_url,raw_sha256,method,evidence_status,captured_at,evidence_excerpt) VALUES($1,$2,$3,'{}','recording',$4,$1,'manual','verified',$5,$6)`, e.ID, parts[0], parts[1], e.URL, e.CapturedAt, e.Text); err != nil {
			t.Fatal(err)
		}
	}
	upgradeRaw, err := os.ReadFile("testdata/cpu_upgrade_recording.json")
	if err != nil {
		t.Fatal(err)
	}
	var upgrade struct{ Candidate planning.Candidate }
	if err = json.Unmarshal(upgradeRaw, &upgrade); err != nil {
		t.Fatal(err)
	}
	c := upgrade.Candidate
	if _, err = conn.Exec(ctx, `INSERT INTO parts(sku,category,brand,model,specs) VALUES($1,$2,$3,$4,$5) ON CONFLICT(sku) DO NOTHING`, c.ID, c.Category, c.Brand, c.Model, c.Specs); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO prices(snapshot_id,sku,price_cny,source) VALUES($1,$2,$3,'saved-local-catalog') ON CONFLICT(snapshot_id,sku) DO NOTHING`, snapshot, c.ID, c.Price); err != nil {
		t.Fatal(err)
	}
}

// Explicit semantic oracle, isolated to offline tests. Production routing never
// compares these phrases; it uses the single Screening model's next_action.
func (g *planningReplayGateway) Screen(ctx context.Context, owner, sessionID string, input product.ScreenInput) (product.ScreenResult, error) {
	output := ""
	switch input.Text {
	case "换成缺规格主板候选继续核实", "读取资料补齐主板规格并继续校验", "按当前方案继续校验":
		output = `{"next_action":"plan","operations":[]}`
	case "预算调整为6500，其他要求保留并继续选配":
		output = `{"next_action":"plan","operations":[{"op":"set","field":"budget_cny","value":6500,"strength":"must","quote":"预算调整为6500"}]}`
	case "预算7000，剪1080p多轨视频，不要求静音":
		output = `{"next_action":"confirm","operations":[{"op":"set","field":"budget_cny","value":7000,"strength":"must","quote":"预算7000"},{"op":"set","field":"free.workload_resolution","value":"1080p多轨视频剪辑","kind":"fact","strength":"must","quote":"剪1080p多轨视频"},{"op":"remove","field":"noise_pref","quote":"不要求静音"}]}`
	case "把处理器换更好的，预算还很充足啊，其他配件尽量不动":
		if input.Conversation.CanPlan && (!input.HasBuild || !strings.Contains(string(input.Conversation.BaseDraft), "cpu-r5-5600") || !strings.Contains(string(input.Conversation.Parts), "Ryzen 5 5600") || len(input.Conversation.Quote) == 0 || input.RequirementState.Fields["free.workload_resolution"].Status != "active") {
			return product.ScreenResult{}, fmt.Errorf("upgrade did not receive actual current configuration and workload")
		}
		output = `{"next_action":"plan","operations":[{"op":"set","field":"priority","value":["cpu"],"strength":"prefer","quote":"把处理器换更好的"},{"op":"set","field":"free.preserve_other_parts","value":"其他配件尽量不动","kind":"constraint","strength":"prefer","quote":"其他配件尽量不动"}]}`
	case "如果换更好的CPU会怎样，先别执行":
		output = `{"next_action":"collect","reply":"可以比较升级方向，尚未更换当前配置。","operations":[{"op":"alternative","field":"priority","value":["cpu"],"quote":"如果换更好的CPU会怎样"}]}`
	case "预算先记8000，先不要重新生成":
		output = `{"next_action":"confirm","reply":"预算已记录，尚未重新生成。","operations":[{"op":"set","field":"budget_cny","value":8000,"quote":"预算先记8000"}]}`
	case "没有具体偏好，直接继续选配":
		output = `{"next_action":"plan","operations":[]}`
	default:
		return g.requirementReplayGateway.Screen(ctx, owner, sessionID, input)
	}
	return replayScreening(ctx, input, output)
}

func TestPlanningSharedQuotaIsAtomic(t *testing.T) {
	_, _, st := requirementIntegrationAPI(t, true)
	var wg sync.WaitGroup
	var accepted atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if st.ReserveSearch(context.Background(), 0, 2) == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 2 {
		t.Fatalf("shared quota accepted %d requests, expected 2", accepted.Load())
	}
	if st.ReserveSearch(context.Background(), 0, 2) == nil {
		t.Fatal("stale provider usage reopened exhausted quota")
	}
}

func (g *planningReplayGateway) Remote(ctx context.Context, _, sessionID string, payload json.RawMessage) (product.RemoteResult, error) {
	var input schemas.PlanningInput
	if e := json.Unmarshal(payload, &input); e != nil {
		return product.RemoteResult{}, e
	}
	m := &planningReplayModel{draft: g.fixture.Draft, input: input}
	if input.Request != nil && (input.Request.Quote == "换成缺规格主板候选继续核实" || input.Request.Quote == "读取资料补齐主板规格并继续校验" || input.Request.Quote == "按当前方案继续校验") {
		return g.supplementReplay(ctx, sessionID, input)
	}
	if input.Request != nil && input.State.Fields["priority"].Status == "active" {
		if len(input.BaseDraft) == 0 || len(input.PreviousProposal) == 0 || input.Request.MessageID == "" {
			return product.RemoteResult{}, fmt.Errorf("missing continuation context")
		}
		var base map[string]json.RawMessage
		if e := json.Unmarshal(input.BaseDraft, &base); e != nil {
			return product.RemoteResult{}, e
		}
		var selection map[string]json.RawMessage
		_ = json.Unmarshal(base["selection"], &selection)
		selection["cpu"] = json.RawMessage(`"cpu-r7-5700x"`)
		base["selection"], _ = json.Marshal(selection)
		var rationale map[string]string
		_ = json.Unmarshal(base["rationale"], &rationale)
		rationale["cpu"] = "离线升级回放：从本地检索到 Ryzen 7 5700X，保留其他配件并重新校验与报价。"
		base["rationale"], _ = json.Marshal(rationale)
		m.draft, _ = json.Marshal(base)
	}
	result, e := (planning.Runner{Model: m, Catalog: g.store}).Run(ctx, input)
	if e != nil {
		return product.RemoteResult{}, e
	}
	if g.hook != nil {
		g.hook(&result)
	}
	return g.archiveAndStrip(ctx, sessionID, input, result)
}

type planningReplayModel struct {
	draft json.RawMessage
	input schemas.PlanningInput
	calls int
}

func (*planningReplayModel) Name() string { return "offline-planning-recording" }
func (m *planningReplayModel) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(y func(*model.LLMResponse, error) bool) {
		m.calls++
		usage := &genai.GenerateContentResponseUsageMetadata{TotalTokenCount: int32(100 + m.calls)}
		if m.input.Request != nil && m.calls == 2 && strings.Contains(string(m.draft), "cpu-r7-5700x") {
			toolResults, _ := json.Marshal(req.Contents[len(req.Contents)-1])
			if !strings.Contains(string(toolResults), "cpu-r7-5700x") {
				y(nil, fmt.Errorf("upgrade candidate was not retrieved by search_local"))
				return
			}
		}
		var content *genai.Content
		if m.calls < 3 {
			action, payload := "search_local", `{"category":"cpu"}`
			if m.calls == 2 {
				action, payload = "evaluate", `{"draft":`+string(m.draft)+`}`
			}
			content = &genai.Content{Role: "model", Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "planning_action", ID: uuid.NewString(), Args: map[string]any{"action": action, "payload": payload}}}}}
		} else {
			outcome := "proposal" // Reproduce a complete plan with a non-delivery model label.
			issues := []string{}
			assessments := []planning.Assessment{}
			for field, v := range m.input.State.Fields {
				if v.Status != "active" {
					continue
				}
				status := "met"
				if (field == "noise_pref" || strings.HasPrefix(field, "free.")) && v.Strength == "must" && v.Kind != "fact" {
					status = "unknown"
					outcome = "proposal"
					issues = append(issues, "需要核对"+schemas.RequirementFieldLabel(field)+"的商品或实测资料")
				}
				var draft schemas.BuildDraft
				_ = json.Unmarshal(m.draft, &draft)
				assessments = append(assessments, planning.Assessment{Field: field, Status: status, Explanation: "离线回放：根据工具返回的规格与价格核验", Evidence: []string{"local:" + draft.Selection.CPU}})
			}
			raw, _ := json.Marshal(map[string]any{"outcome": outcome, "reply": "已整理候选方案，选型与待解决问题可在右侧查看。", "draft": m.draft, "issues": issues, "assessments": assessments, "assumptions": []string{}})
			content = genai.NewContentFromText(string(raw), genai.RoleModel)
		}
		y(&model.LLMResponse{Content: content, UsageMetadata: usage}, nil)
	}
}

func TestPlanningProposalPersistentWorkflow(t *testing.T) {
	_, service, st := requirementIntegrationAPI(t, true)
	ctx := context.Background()
	owner := strings.Repeat("p", 43)
	ws, e := service.CreateSession(ctx, owner, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	edit := func(ops ...schemas.RequirementOperation) product.SessionDetail {
		d, e := service.GetSession(ctx, owner, ws.ID)
		if e != nil {
			t.Fatal(e)
		}
		var state schemas.RequirementState
		_ = json.Unmarshal(d.Session.RequirementState, &state)
		d, e = service.EditRequirement(ctx, owner, ws.ID, uuid.NewString(), product.RequirementEdit{ExpectedRevision: state.Revision, Operations: ops})
		if e != nil {
			t.Fatal(e)
		}
		return d
	}
	confirm := func() product.SessionDetail {
		r, e := service.StartConfirm(ctx, owner, ws.ID, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		for i := 0; i < 200; i++ {
			run, e := service.GetRun(ctx, owner, r.Run.ID)
			if e != nil {
				t.Fatal(e)
			}
			if run.Status != store.RunRunning {
				if run.Status != store.RunSucceeded {
					t.Fatalf("run failed: %s", run.Error)
				}
				d, e := service.GetSession(ctx, owner, ws.ID)
				if e != nil {
					t.Fatal(e)
				}
				return d
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("run timeout")
		return product.SessionDetail{}
	}
	edit(schemas.RequirementOperation{Op: "set", Field: "budget_cny", Value: json.RawMessage(`12000`), Strength: "must"})
	first := confirm()
	if first.Session.VersionCount != 1 {
		t.Fatalf("first build not saved: %s", first.Proposal)
	}
	version, e := st.BuildByVersion(ctx, ws.ID, 1)
	if e != nil {
		t.Fatal(e)
	}
	edit(schemas.RequirementOperation{Op: "set", Field: "noise_pref", Value: json.RawMessage(`"silent"`), Kind: "constraint", Strength: "must"})
	second := confirm()
	if second.Session.VersionCount != 1 || len(second.Proposal) == 0 || second.Session.Phase == store.PhaseError {
		t.Fatalf("proposal failed: %+v", second)
	}
	var proposal struct{ Result planning.Result }
	_ = json.Unmarshal(second.Proposal, &proposal)
	if proposal.Result.Outcome != "proposal" || proposal.Result.ModelCalls != 4 || proposal.Result.ToolCalls != 2 || len(proposal.Result.Candidates) == 0 {
		t.Fatalf("not a tool-backed proposal: %s", second.Proposal)
	}
	read, e := service.GetSession(ctx, owner, ws.ID)
	if e != nil || string(read.Proposal) != string(second.Proposal) {
		t.Fatal("refresh lost proposal")
	}
	after, e := st.BuildByVersion(ctx, ws.ID, 1)
	if e != nil || string(after.Draft) != string(version.Draft) || string(after.CandidateSnapshot) != string(version.CandidateSnapshot) {
		t.Fatal("proposal changed confirmed build")
	}
	edit(schemas.RequirementOperation{Op: "remove", Field: "noise_pref"})
	third := confirm()
	if third.Session.VersionCount != 2 {
		t.Fatalf("did not resume after removal: %s", third.Proposal)
	}
}

func TestPlanningResultArchivedAndTransportDegraded(t *testing.T) {
	_, service, st := requirementIntegrationAPI(t, true)
	ctx := context.Background()
	owner := strings.Repeat("p", 43)
	ws, e := service.CreateSession(ctx, owner, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	edit := func(ops ...schemas.RequirementOperation) {
		d, e := service.GetSession(ctx, owner, ws.ID)
		if e != nil {
			t.Fatal(e)
		}
		var state schemas.RequirementState
		_ = json.Unmarshal(d.Session.RequirementState, &state)
		if _, e = service.EditRequirement(ctx, owner, ws.ID, uuid.NewString(), product.RequirementEdit{ExpectedRevision: state.Revision, Operations: ops}); e != nil {
			t.Fatal(e)
		}
	}
	confirm := func() product.SessionDetail {
		r, e := service.StartConfirm(ctx, owner, ws.ID, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		for i := 0; i < 200; i++ {
			run, e := service.GetRun(ctx, owner, r.Run.ID)
			if e != nil {
				t.Fatal(e)
			}
			if run.Status != store.RunRunning {
				if run.Status != store.RunSucceeded {
					t.Fatalf("run failed: %s", run.Error)
				}
				d, e := service.GetSession(ctx, owner, ws.ID)
				if e != nil {
					t.Fatal(e)
				}
				return d
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("run timeout")
		return product.SessionDetail{}
	}
	gateway := lastPlanningGateway
	if gateway == nil {
		t.Fatal("planning gateway not registered")
	}
	edit(schemas.RequirementOperation{Op: "set", Field: "budget_cny", Value: json.RawMessage(`12000`), Strength: "must"})
	first := confirm()
	if first.Session.VersionCount != 1 {
		t.Fatalf("first build not saved: %s", first.Proposal)
	}
	// F2:产品侧注入 run_id,生成侧按 run 归档完整产物。
	if len(gateway.runIDs) != 1 || gateway.runIDs[0] == "" {
		t.Fatalf("run_id not injected into planning input: %v", gateway.runIDs)
	}
	archived, e := st.PlanningArtifact(ctx, gateway.runIDs[0])
	if e != nil || len(archived) == 0 {
		t.Fatalf("planning artifact missing: len=%d err=%v", len(archived), e)
	}
	var full planning.Result
	if e = json.Unmarshal(archived, &full); e != nil {
		t.Fatal(e)
	}
	// 传输副本必须是归档产物的降级形式,且体积有界。
	edit(schemas.RequirementOperation{Op: "set", Field: "noise_pref", Value: json.RawMessage(`"silent"`), Kind: "constraint", Strength: "must"})
	confirm()
	detail, _ := service.GetSession(ctx, owner, ws.ID)
	var proposal struct {
		Result planning.Result `json:"result"`
	}
	if e = json.Unmarshal(detail.Proposal, &proposal); e != nil {
		t.Fatal(e)
	}
	expected := planning.TransportDegrade(full)
	if len(proposal.Result.Evidence) != len(expected.Evidence) {
		t.Fatalf("evidence count mismatch: %d vs %d", len(proposal.Result.Evidence), len(expected.Evidence))
	}
	for i, ev := range proposal.Result.Evidence {
		if ev.Text != expected.Evidence[i].Text || len([]rune(ev.Text)) > 270 {
			t.Fatalf("transmitted evidence %d not the degraded archive copy: %d runes", i, len([]rune(ev.Text)))
		}
	}
	// session_proposals.result 体积有界:证据正文与字段引文都不得超过降级上限。
	for _, c := range proposal.Result.Candidates {
		for field, quote := range c.FieldQuotes {
			if len([]rune(quote)) > 270 {
				t.Fatalf("field quote %s/%s not bounded: %d runes", c.ID, field, len([]rune(quote)))
			}
		}
	}
	// 下一轮按上一轮 proposal 的 run_id 从归档补全证据正文。
	if len(gateway.previousRunIDs) != 2 || gateway.previousRunIDs[1] != gateway.runIDs[0] {
		t.Fatalf("previous_run_id not threaded: %v", gateway.previousRunIDs)
	}
	if _, e = st.PlanningArtifact(ctx, gateway.runIDs[1]); e != nil {
		t.Fatal(e)
	}
	// F3:一次 planning run 后,单条 SQL 能答出 run 的模型身份与全部计量。
	obs, e := st.RunObservability(ctx, gateway.runIDs[0])
	if e != nil {
		t.Fatal(e)
	}
	if obs.ModelCalls == 0 || obs.ToolCalls == 0 || obs.Tokens == 0 || obs.DurationMS == 0 || obs.CatalogSnapshotI == 0 || obs.BuildsLinked != 1 {
		t.Fatalf("planning run metrics not persisted: %+v", obs)
	}
	if obs.MirroredEvents < 2 {
		t.Fatalf("terminal events not mirrored: %+v", obs)
	}
}

func TestPlanningBrowserServer(t *testing.T) {
	addr := os.Getenv("PLANNING_BROWSER_ADDR")
	if addr == "" {
		t.Skip("explicit offline browser server only")
	}
	api, _, _ := requirementIntegrationAPI(t, true)
	server := &http.Server{Addr: addr, ReadHeaderTimeout: 5 * time.Second}
	mux := http.NewServeMux()
	mux.Handle("/", api.Handler())
	mux.HandleFunc("POST /__offline/shutdown", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
		go func() { _ = server.Shutdown(context.Background()) }()
	})
	server.Handler = mux
	t.Cleanup(func() { _ = server.Close() })
	t.Log("offline planning API ready", addr)
	if e := server.ListenAndServe(); e != http.ErrServerClosed {
		t.Fatal(e)
	}
}

// F5:生成侧 ready 结论与产品侧零 LLM 复验不一致时,降级为 proposal 且不新增版本。
func TestPlanningReplayDegradesDivergentVerification(t *testing.T) {
	_, service, st := requirementIntegrationAPI(t, true)
	ctx := context.Background()
	owner := strings.Repeat("p", 43)
	ws, e := service.CreateSession(ctx, owner, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	lastPlanningGateway.mu.Lock()
	lastPlanningGateway.hook = func(result *planning.Result) {
		if result.Outcome == "ready" && result.Quote != nil {
			tampered := "999999.99"
			result.Quote.TotalCNY = tampered
		}
	}
	lastPlanningGateway.mu.Unlock()
	edit := func(ops ...schemas.RequirementOperation) {
		d, e := service.GetSession(ctx, owner, ws.ID)
		if e != nil {
			t.Fatal(e)
		}
		var state schemas.RequirementState
		_ = json.Unmarshal(d.Session.RequirementState, &state)
		if _, e = service.EditRequirement(ctx, owner, ws.ID, uuid.NewString(), product.RequirementEdit{ExpectedRevision: state.Revision, Operations: ops}); e != nil {
			t.Fatal(e)
		}
	}
	edit(schemas.RequirementOperation{Op: "set", Field: "budget_cny", Value: json.RawMessage(`12000`), Strength: "must"})
	r, e := service.StartConfirm(ctx, owner, ws.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 200; i++ {
		run, e := service.GetRun(ctx, owner, r.Run.ID)
		if e != nil {
			t.Fatal(e)
		}
		if run.Status == store.RunRunning {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if run.Status != store.RunSucceeded {
			t.Fatalf("run failed: %s", run.Error)
		}
		detail, e := service.GetSession(ctx, owner, ws.ID)
		if e != nil {
			t.Fatal(e)
		}
		if detail.Session.VersionCount != 0 {
			t.Fatalf("divergent verification still saved a version: %s", detail.Proposal)
		}
		var proposal struct {
			Result planning.Result `json:"result"`
		}
		if e = json.Unmarshal(detail.Proposal, &proposal); e != nil {
			t.Fatal(e)
		}
		if proposal.Result.Outcome != "proposal" || proposal.Result.Delivery == nil || proposal.Result.Delivery.Status != "unresolved" {
			t.Fatalf("not degraded to unresolved proposal: %+v", proposal.Result.Delivery)
		}
		found := false
		for _, issue := range proposal.Result.Issues {
			found = found || strings.Contains(issue, "服务端复验")
		}
		if !found {
			t.Fatalf("verification issue missing: %v", proposal.Result.Issues)
		}
		obs, e := st.RunObservability(ctx, lastPlanningGateway.runIDs[0])
		if e != nil {
			t.Fatal(e)
		}
		if obs.BuildsLinked != 0 {
			t.Fatalf("degraded proposal created a build row: %+v", obs)
		}
		return
	}
	t.Fatal("run timeout")
}
