package producthttp

// 离线端到端验证：真实 PostgreSQL、产品 service、HTTP、规则/价格核算和 presenter。
// 只有 Screening 提取与 Builder 选件使用明确标注的录制/人工 oracle，不访问模型。
import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	migrations "github.com/subaru-ye/pc-builder-agent/db"
	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/presenter"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/runevents"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

type requirementReplay struct {
	Source string          `json:"source"`
	Draft  json.RawMessage `json:"draft"`
	Parts  []struct {
		SKU, Brand, Model string
		Category          schemas.Category
		Specs             json.RawMessage
		PriceCNY          string
	} `json:"parts"`
	Quote validate.Quote `json:"quote"`
}

type requirementReplayGateway struct {
	store   *store.Store
	fixture requirementReplay
}

// The only model implementation in this harness is an in-process recording.
// No provider, credentials, HTTP client, or retry transport is constructed.
type requirementRecordedModel struct {
	output string
	calls  int
}

func (m *requirementRecordedModel) Name() string { return "offline-recording-no-provider" }
func (m *requirementRecordedModel) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.calls++
		yield(&model.LLMResponse{Content: genai.NewContentFromText(m.output, genai.RoleModel)}, nil)
	}
}

func replayScreening(ctx context.Context, input product.ScreenInput, output string) (product.ScreenResult, error) {
	m := &requirementRecordedModel{output: output}
	screening, err := pipeline.NewProductScreening(m)
	if err != nil {
		return product.ScreenResult{}, err
	}
	r, err := runner.New(runner.Config{AppName: "offline-requirements", Agent: screening, SessionService: session.InMemoryService(), AutoCreateSession: true})
	if err != nil {
		return product.ScreenResult{}, err
	}
	ctx = pipeline.WithRequirementState(ctx, *input.RequirementState, input.RequirementSource)
	var text string
	for event, err := range r.Run(ctx, "offline", uuid.NewString(), genai.NewContentFromText(input.Context, genai.RoleUser), agent.RunConfig{}) {
		if err != nil {
			return product.ScreenResult{}, err
		}
		if event != nil && !event.Partial && event.Content != nil {
			for _, part := range event.Content.Parts {
				if part != nil && !part.Thought {
					text += part.Text
				}
			}
		}
	}
	if m.calls != 1 {
		return product.ScreenResult{}, fmt.Errorf("offline screening calls=%d, expected one", m.calls)
	}
	update, err := schemas.DecodeRequirementUpdate(pipeline.ExtractPayload(text))
	return product.ScreenResult{Kind: product.ScreenRequirement, Text: text, RequirementUpdate: &update}, err
}

func (g *requirementReplayGateway) ContextAvailable(context.Context, string, string) (bool, error) {
	return true, nil
}
func (g *requirementReplayGateway) Screen(ctx context.Context, _, _ string, input product.ScreenInput) (product.ScreenResult, error) {
	op := func(field string, value any, strength string) schemas.RequirementOperation {
		raw, _ := json.Marshal(value)
		return schemas.RequirementOperation{Op: "set", Field: field, Value: raw, Strength: strength, Quote: input.Text}
	}
	var ops []schemas.RequirementOperation
	switch input.Text {
	case "预算 6000，主要剪 4K 视频，尽量安静":
		raw, err := os.ReadFile("../agents/pipeline/testdata/requirement_video_editing.json")
		if err != nil {
			return product.ScreenResult{}, err
		}
		var output string
		if err := json.Unmarshal(raw, &output); err != nil {
			return product.ScreenResult{}, err
		}
		return replayScreening(ctx, input, output)
	case "预算8000，玩游戏，要安静一点，尽量用N卡，帮朋友装机":
		ops = []schemas.RequirementOperation{op("budget_cny", 8000, "must"), op("use_case.type", "gaming", "must"), op("noise_pref", "silent", "prefer"), op("brand_pref.gpu", "nvidia", "prefer"), op("recipient", "朋友", "must")}
	case "预算改成6000":
		ops = []schemas.RequirementOperation{op("budget_cny", 6000, "must")}
	case "用2K":
		ops = []schemas.RequirementOperation{op("use_case.resolution", "2K", "must")}
	case "取消显卡品牌偏好":
		ops = []schemas.RequirementOperation{{Op: "remove", Field: "brand_pref.gpu", Quote: input.Text}}
	case "如果换4K会怎样？先不改":
		o := op("use_case.resolution", "4K", "must")
		o.Op = "alternative"
		ops = []schemas.RequirementOperation{o}
	case "这次临时用AMD显卡":
		o := op("brand_pref.gpu", "amd", "prefer")
		o.Scope = "temporary"
		ops = []schemas.RequirementOperation{o}
	case "恢复之前的显卡要求":
		ops = []schemas.RequirementOperation{{Op: "restore", Field: "brand_pref.gpu", Quote: input.Text}}
	case "静音还是尽量满足就好":
		o := op("noise_pref", "silent", "prefer")
		o.Kind, o.Evidence = "constraint", "stated"
		ops = []schemas.RequirementOperation{o}
	default:
		return product.ScreenResult{}, fmt.Errorf("离线验证仅支持已登记的对话；自由修改请使用需求面板")
	}
	output, err := json.Marshal(schemas.RequirementUpdate{Operations: ops})
	if err != nil {
		return product.ScreenResult{}, err
	}
	return replayScreening(ctx, input, string(output))
}
func (g *requirementReplayGateway) Remote(ctx context.Context, _, sessionID string, payload json.RawMessage) (product.RemoteResult, error) {
	spec, err := schemas.DecodeRequirementSpec(payload)
	if err != nil {
		return product.RemoteResult{}, err
	}
	draftJSON := g.fixture.Draft
	if spec.UseCase.Type == schemas.UseCaseProductivity {
		// Synthetic lower-priced GPU oracle for the reported budget. The saved
		// historical build and its price snapshot remain byte-for-byte unchanged.
		draftJSON = json.RawMessage(strings.ReplaceAll(string(draftJSON), "gpu-gb-5070-windforce-sff", "offline-gpu-4060"))
	}
	planner, err := buildharness.NewCandidatePlannerWithOptions(g.store, nil, buildharness.PlannerOptions{DisableSemantic: true})
	if err != nil {
		return product.RemoteResult{}, err
	}
	harness, err := buildharness.New(buildharness.Config{Model: &requirementRecordedModel{output: string(draftJSON)}, Planner: planner, Repairer: buildharness.NewRepairPlanner(), Eval: validate.New(g.store)})
	if err != nil {
		return product.RemoteResult{}, err
	}
	result, err := harness.Run(ctx, buildharness.BuildInput{Requirement: spec})
	if err != nil {
		return product.RemoteResult{}, err
	}
	if !result.Succeeded {
		return product.RemoteResult{Text: result.Message, Decision: result.Decision}, nil
	}
	report, _ := json.Marshal(result.Result.Report)
	quote, _ := json.Marshal(result.Result.Quote)
	var parent *int64
	if versions, err := g.store.BuildsBySession(ctx, sessionID); err == nil && len(versions) > 0 {
		parent = &versions[len(versions)-1].ID
	}
	_, err = g.store.SaveBuildVersion(ctx, store.SaveBuildVersionParams{SessionID: sessionID, ParentID: parent, RequirementSpec: payload, Draft: draftJSON, Validation: report, Quote: quote})
	return product.RemoteResult{Text: "离线验证：录制初筛与选件输出经过真实需求合并、候选准备、Harness、规则、报价和版本保存；选件为测试 oracle，不代表真实模型选配效果。"}, err
}

func requirementIntegrationAPI(t *testing.T) (*API, *product.Service, *store.Store) {
	t.Helper()
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("设置 PG_TEST_DSN 运行真实数据库离线验证")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	dbName := fmt.Sprintf("requirement_test_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+dbName+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + dbName
	conn, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations.Migrations)
	goose.SetLogger(goose.NopLogger())
	if err = goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err = goose.Up(conn, "migrations"); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	raw, err := os.ReadFile("testdata/requirement_replay.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture requirementReplay
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	seed, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	var snapshot int64
	if err = seed.QueryRow(ctx, "INSERT INTO price_snapshots(snapshot_date,file_sha256) VALUES($1,'offline-replay') RETURNING id", fixture.Quote.SnapshotDate).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	for _, part := range fixture.Parts {
		if _, err = seed.Exec(ctx, "INSERT INTO parts(sku,category,brand,model,specs) VALUES($1,$2,$3,$4,$5)", part.SKU, part.Category, part.Brand, part.Model, part.Specs); err != nil {
			t.Fatal(err)
		}
		if _, err = seed.Exec(ctx, "INSERT INTO prices(snapshot_id,sku,price_cny,source) VALUES($1,$2,$3,'offline-replay')", snapshot, part.SKU, part.PriceCNY); err != nil {
			t.Fatal(err)
		}
	}
	// Explicit synthetic candidate, isolated to the disposable test database.
	if _, err = seed.Exec(ctx, `INSERT INTO parts(sku,category,brand,model,specs)
		SELECT 'offline-gpu-4060',category,'Offline fixture','GeForce RTX 4060 (synthetic fixture)',specs || '{"tdp_w":115}'::jsonb
		FROM parts WHERE sku='gpu-gb-5070-windforce-sff'`); err != nil {
		t.Fatal(err)
	}
	if _, err = seed.Exec(ctx, "INSERT INTO prices(snapshot_id,sku,price_cny,source) VALUES($1,'offline-gpu-4060',2299,'synthetic-offline-fixture')", snapshot); err != nil {
		t.Fatal(err)
	}
	_ = seed.Close(ctx)
	st, err := store.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	events := runevents.NewMemory()
	gateway := &requirementReplayGateway{store: st, fixture: fixture}
	service, err := product.NewService(ctx, st, gateway, events)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	webURL := os.Getenv("REQUIREMENT_BROWSER_WEB_URL")
	if webURL == "" {
		webURL = "http://127.0.0.1:3102"
	}
	api, err := New(service, presenter.New(st), &fakeShareService{}, events, st, fakeRedis{}, Config{PublicWebBaseURL: webURL})
	if err != nil {
		t.Fatal(err)
	}
	return api, service, st
}

func TestRequirementStatePersistentWorkflow(t *testing.T) {
	api, service, st := requirementIntegrationAPI(t)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	ctx := context.Background()
	owner := "offline-requirement-owner"
	ws, err := service.CreateSession(ctx, owner, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	chat := func(text string) product.SessionDetail {
		t.Helper()
		started, err := service.StartMessage(ctx, owner, ws.ID, uuid.NewString(), text)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 100; i++ {
			run, err := service.GetRun(ctx, owner, started.Run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if run.Status != store.RunRunning {
				if run.Status != store.RunSucceeded {
					t.Fatalf("chat failed: %s", run.Error)
				}
				detail, err := service.GetSession(ctx, owner, ws.ID)
				if err != nil {
					t.Fatal(err)
				}
				return detail
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("run timed out")
		return product.SessionDetail{}
	}
	stateOf := func(detail product.SessionDetail) schemas.RequirementState {
		var state schemas.RequirementState
		if err := json.Unmarshal(detail.Session.RequirementState, &state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	detail := chat("预算8000，玩游戏，要安静一点，尽量用N卡，帮朋友装机")
	if !reflect.DeepEqual(detail.MissingFields, []string{"use_case.resolution"}) {
		t.Fatalf("重复追问已知字段: %v", detail.MissingFields)
	}
	detail = chat("预算改成6000")
	state := stateOf(detail)
	if string(state.Fields["budget_cny"].Value) != "6000" || string(state.Fields["noise_pref"].Value) != `"silent"` || string(state.Fields["recipient"].Value) != `"朋友"` {
		t.Fatal("预算修改丢失有效偏好")
	}
	detail = chat("用2K")
	detail = chat("取消显卡品牌偏好")
	detail = chat("如果换4K会怎样？先不改")
	state = stateOf(detail)
	if string(state.Fields["use_case.resolution"].Value) != `"2K"` || state.Fields["brand_pref.gpu"].Status != "removed" {
		t.Fatal("备选或撤销污染当前需求")
	}
	detail = chat("这次临时用AMD显卡")
	detail = chat("恢复之前的显卡要求")
	state = stateOf(detail)
	if state.Fields["brand_pref.gpu"].Status != "removed" {
		t.Fatal("恢复临时例外未恢复撤销状态")
	}
	edit := product.RequirementEdit{ExpectedRevision: state.Revision, Operations: []schemas.RequirementOperation{{Op: "set", Field: "budget_cny", Value: json.RawMessage(`8500`), Strength: "must"}}}
	key := uuid.NewString()
	detail, err = service.EditRequirement(ctx, owner, ws.ID, key, edit)
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.EditRequirement(ctx, owner, ws.ID, key, edit)
	if err != nil || !reflect.DeepEqual(detail.Session.RequirementState, again.Session.RequirementState) {
		t.Fatal("编辑重试不是幂等")
	}
	different := edit
	different.Operations = append([]schemas.RequirementOperation(nil), edit.Operations...)
	different.Operations[0].Op = "alternative"
	if _, err := service.EditRequirement(ctx, owner, ws.ID, key, different); err != store.ErrIdempotencyConflict {
		t.Fatalf("展示值相同但操作不同应幂等冲突: %v", err)
	}
	if _, err := service.EditRequirement(ctx, owner, ws.ID, uuid.NewString(), edit); err != store.ErrRequirementRevision {
		t.Fatalf("旧页面应冲突:%v", err)
	}
	confirmKey := uuid.NewString()
	confirmed, err := service.StartConfirm(ctx, owner, ws.ID, confirmKey)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		r, _ := service.GetRun(ctx, owner, confirmed.Run.ID)
		if r.Status != store.RunRunning {
			if r.Status != store.RunSucceeded {
				t.Fatalf("confirm:%s", r.Error)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	old, err := st.BuildByVersion(ctx, ws.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	original, err := st.RequirementSpecByID(ctx, old.RequirementID)
	if err != nil {
		t.Fatal(err)
	}
	storedMessages, err := st.WebMessages(ctx, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := storedMessages[len(storedMessages)-1]
	if last.BuildVersion != 1 || last.DisplayContent == "" || last.DisplayContent == last.Content {
		t.Fatalf("配置摘要未与原始回复及版本一起持久化: %+v", last)
	}
	detail = chat("预算改成6000")
	if detail.RequirementStatus != "modified" || detail.Session.VersionCount != 1 {
		t.Fatal("未确认修改覆盖了已有版本")
	}
	unchanged, _ := st.RequirementSpecByID(ctx, old.RequirementID)
	if !reflect.DeepEqual(original, unchanged) {
		t.Fatal("历史需求被覆盖")
	}
	var confirmedSpec schemas.RequirementState
	_ = json.Unmarshal(detail.Session.ConfirmedRequirementState, &confirmedSpec)
	if string(confirmedSpec.Fields["budget_cny"].Value) != "8500" {
		t.Fatal("确认快照被覆写")
	}
	// 新 service 实例读取相同存储，模拟进程重启/刷新；无内存记忆前置条件。
	restored, err := product.NewService(ctx, st, &requirementReplayGateway{store: st}, runevents.NewMemory())
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Shutdown(ctx)
	fresh, err := restored.GetSession(ctx, owner, ws.ID)
	if err != nil || !reflect.DeepEqual(fresh.Session.RequirementState, detail.Session.RequirementState) {
		t.Fatal("刷新未恢复服务端真值")
	}
	state = stateOf(detail)
	withdrawn, err := service.EditRequirement(ctx, owner, ws.ID, uuid.NewString(), product.RequirementEdit{
		ExpectedRevision: state.Revision, Operations: []schemas.RequirementOperation{{Op: "remove", Field: "budget_cny"}},
	})
	if err != nil || len(withdrawn.Session.PendingRequirement) > 0 {
		t.Fatalf("撤销预算应清除待确认投影: %v", err)
	}
	retry, err := service.StartConfirm(ctx, owner, ws.ID, confirmKey)
	if err != nil || !retry.Duplicate || retry.Run.ID != confirmed.Run.ID {
		t.Fatalf("新草稿不足不应影响旧确认重试: %+v, %v", retry, err)
	}
	unchanged, _ = st.RequirementSpecByID(ctx, old.RequirementID)
	if !reflect.DeepEqual(unchanged, original) {
		t.Fatal("撤销后的确认重试改写历史版本")
	}
	other, err := restored.CreateSession(ctx, owner, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	var clean schemas.RequirementState
	_ = json.Unmarshal(other.RequirementState, &clean)
	if len(clean.Fields) > 0 || clean.Revision != 0 {
		t.Fatal("朋友需求进入其他会话")
	}
}

// Reproduce the reported path through recorded Screening, UI strength edit,
// confirmation, real Harness/validator, PostgreSQL and the persisted read model.
func TestVideoRequirementConfirmationReplay(t *testing.T) {
	_, service, st := requirementIntegrationAPI(t)
	ctx := context.Background()
	owner := "offline-video-owner"
	ws, err := service.CreateSession(ctx, owner, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	wait := func(runID string, status store.RunStatus) product.SessionDetail {
		t.Helper()
		for i := 0; i < 300; i++ {
			run, err := service.GetRun(ctx, owner, runID)
			if err != nil {
				t.Fatal(err)
			}
			if run.Status != store.RunRunning {
				if run.Status != status {
					t.Fatalf("run status=%s, expected=%s: %s", run.Status, status, run.Error)
				}
				detail, err := service.GetSession(ctx, owner, ws.ID)
				if err != nil {
					t.Fatal(err)
				}
				return detail
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("offline run timeout")
		return product.SessionDetail{}
	}
	stateOf := func(detail product.SessionDetail) schemas.RequirementState {
		t.Helper()
		var state schemas.RequirementState
		if err := json.Unmarshal(detail.Session.RequirementState, &state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	started, err := service.StartMessage(ctx, owner, ws.ID, uuid.NewString(), "预算 6000，主要剪 4K 视频，尽量安静")
	if err != nil {
		t.Fatal(err)
	}
	detail := wait(started.Run.ID, store.RunSucceeded)
	state := stateOf(detail)
	if len(detail.MissingFields) != 0 || state.Fields["budget_flex"].Status != "unknown" || state.Fields["use_case.resolution"].Status == "active" {
		t.Fatalf("invented preference or unnecessary question: %+v", detail)
	}
	detail, err = service.EditRequirement(ctx, owner, ws.ID, uuid.NewString(), product.RequirementEdit{ExpectedRevision: state.Revision,
		Operations: []schemas.RequirementOperation{{Op: "set", Field: "budget_cny", Value: json.RawMessage(`6000`), Strength: "prefer"}}})
	if err != nil {
		t.Fatal(err)
	}
	state = stateOf(detail)
	if state.Fields["noise_pref"].Strength != "prefer" || string(state.Fields["use_case.type"].Value) != `"productivity"` || state.Fields["budget_flex"].Status != "unknown" {
		t.Fatal("budget strength edit changed other facts or user-unknown flex")
	}
	confirmed, err := service.StartConfirm(ctx, owner, ws.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	detail = wait(confirmed.Run.ID, store.RunSucceeded)
	if detail.Session.VersionCount != 1 {
		t.Fatal("confirmed video requirement did not save a configuration")
	}
	v1, err := st.BuildByVersion(ctx, ws.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	savedSpec, err := st.RequirementSpecByID(ctx, v1.RequirementID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(savedSpec), "4K") || !strings.Contains(string(savedSpec), "silent") {
		t.Fatal("workload evidence or quiet preference never reached saved build requirement")
	}
	frozen := append(json.RawMessage(nil), detail.Session.ConfirmedRequirementState...)
	fresh, err := service.GetSession(ctx, owner, ws.ID)
	if err != nil || !reflect.DeepEqual(fresh.Session.RequirementState, detail.Session.RequirementState) {
		t.Fatal("refresh lost authoritative state")
	}
	state = stateOf(fresh)
	detail, err = service.EditRequirement(ctx, owner, ws.ID, uuid.NewString(), product.RequirementEdit{ExpectedRevision: state.Revision,
		Operations: []schemas.RequirementOperation{{Op: "set", Field: "noise_pref", Value: json.RawMessage(`"silent"`), Strength: "must", Kind: "constraint"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(frozen, detail.Session.ConfirmedRequirementState) {
		t.Fatal("draft edit overwrote confirmed snapshot")
	}
	failed, err := service.StartConfirm(ctx, owner, ws.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	detail = wait(failed.Run.ID, store.RunFailed)
	if detail.Session.VersionCount != 1 || stateOf(detail).Fields["noise_pref"].Strength != "must" {
		t.Fatal("hard condition was silently relaxed or saved as success")
	}
	messages, err := st.WebMessages(ctx, ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := messages[len(messages)-1]
	if !strings.Contains(last.DisplayContent, "静音") || !strings.Contains(last.DisplayContent, "已保存") {
		t.Fatalf("specific failure or saved-state information swallowed: %s", last.DisplayContent)
	}
	after, err := st.RequirementSpecByID(ctx, v1.RequirementID)
	if err != nil || !reflect.DeepEqual(savedSpec, after) {
		t.Fatal("failed new build overwrote v1 requirement history")
	}
	// A user can continue the same conversation after a failed confirmation.
	// This explicit change is the user's choice; the failure never relaxes it.
	continued, err := service.StartMessage(ctx, owner, ws.ID, uuid.NewString(), "静音还是尽量满足就好")
	if err != nil {
		t.Fatal(err)
	}
	detail = wait(continued.Run.ID, store.RunSucceeded)
	if stateOf(detail).Fields["noise_pref"].Strength != "prefer" {
		t.Fatal("chat did not apply explicit correction after failed confirmation")
	}
	confirmed, err = service.StartConfirm(ctx, owner, ws.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	detail = wait(confirmed.Run.ID, store.RunSucceeded)
	v2, err := st.BuildByVersion(ctx, ws.ID, 2)
	if err != nil || detail.Session.VersionCount != 2 || v2.ParentID == nil || *v2.ParentID != v1.ID {
		t.Fatal("new confirmation did not append a child version")
	}
	after, err = st.RequirementSpecByID(ctx, v1.RequirementID)
	if err != nil || !reflect.DeepEqual(savedSpec, after) {
		t.Fatal("v2 generation rewrote v1 requirement")
	}
}

// 显式离线浏览器入口，只存在于测试二进制，运行结束清理独立临时数据库。
func TestRequirementStateBrowserServer(t *testing.T) {
	addr := os.Getenv("REQUIREMENT_BROWSER_ADDR")
	if addr == "" {
		t.Skip("未启用浏览器离线验证服务器")
	}
	api, _, _ := requirementIntegrationAPI(t)
	server := &http.Server{Addr: addr, ReadHeaderTimeout: 5 * time.Second}
	mux := http.NewServeMux()
	mux.Handle("/", api.Handler())
	// Test-binary-only shutdown allows Cleanup to remove the disposable database.
	mux.HandleFunc("POST /__offline/shutdown", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		go func() { _ = server.Shutdown(context.Background()) }()
	})
	server.Handler = mux
	t.Cleanup(func() { _ = server.Close() })
	t.Logf("离线真实业务API %s；0模型调用，录制产物仅验证链路", addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
