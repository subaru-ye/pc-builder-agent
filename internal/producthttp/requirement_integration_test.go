package producthttp

// 离线端到端验证：真实 PostgreSQL、产品 service、HTTP、规则/价格核算和 presenter。
// 只有 Screening 提取与 Builder 选件使用明确标注的录制/人工 oracle，不访问模型。
import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	migrations "github.com/subaru-ye/pc-builder-agent/db"
	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
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

func (g *requirementReplayGateway) ContextAvailable(context.Context, string, string) (bool, error) {
	return true, nil
}
func (g *requirementReplayGateway) Screen(_ context.Context, _, _ string, input product.ScreenInput) (product.ScreenResult, error) {
	op := func(field string, value any, strength string) schemas.RequirementOperation {
		raw, _ := json.Marshal(value)
		return schemas.RequirementOperation{Op: "set", Field: field, Value: raw, Strength: strength, Quote: input.Text}
	}
	var ops []schemas.RequirementOperation
	switch input.Text {
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
	default:
		return product.ScreenResult{}, fmt.Errorf("离线验证仅支持已登记的对话；自由修改请使用需求面板")
	}
	return product.ScreenResult{Kind: product.ScreenRequirement, RequirementUpdate: &schemas.RequirementUpdate{Operations: ops}}, nil
}
func (g *requirementReplayGateway) Remote(ctx context.Context, _, sessionID string, payload json.RawMessage) (product.RemoteResult, error) {
	if _, err := schemas.DecodeRequirementSpec(payload); err != nil {
		return product.RemoteResult{}, err
	}
	draft, err := schemas.DecodeBuildDraft(g.fixture.Draft)
	if err != nil {
		return product.RemoteResult{}, err
	}
	result, err := validate.New(g.store).Evaluate(ctx, draft.Selection)
	if err != nil {
		return product.RemoteResult{}, err
	}
	report, _ := json.Marshal(result.Report)
	quote, _ := json.Marshal(result.Quote)
	var parent *int64
	if versions, err := g.store.BuildsBySession(ctx, sessionID); err == nil && len(versions) > 0 {
		parent = &versions[len(versions)-1].ID
	}
	_, err = g.store.SaveBuildVersion(ctx, store.SaveBuildVersionParams{SessionID: sessionID, ParentID: parent, RequirementSpec: payload, Draft: g.fixture.Draft, Validation: report, Quote: quote})
	return product.RemoteResult{Text: "离线验证：已回放保存的真实配置产物，并重新执行规则与报价核算。此产物只用于验证需求确认和历史版本链路。"}, err
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
	api, err := New(service, presenter.New(st), &fakeShareService{}, events, st, fakeRedis{}, Config{PublicWebBaseURL: "http://127.0.0.1:3100"})
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

// 显式离线浏览器入口，只存在于测试二进制，运行结束清理独立临时数据库。
func TestRequirementStateBrowserServer(t *testing.T) {
	addr := os.Getenv("REQUIREMENT_BROWSER_ADDR")
	if addr == "" {
		t.Skip("未启用浏览器离线验证服务器")
	}
	api, _, _ := requirementIntegrationAPI(t)
	server := &http.Server{Addr: addr, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second}
	t.Cleanup(func() { _ = server.Close() })
	t.Logf("离线真实业务API %s；0模型调用，录制产物仅验证链路", addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
