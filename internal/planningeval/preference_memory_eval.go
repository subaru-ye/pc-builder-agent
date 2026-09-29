package planningeval

// 偏好记忆评估(阶段四):把 docs/eval/preference-memory/README.md 的六类场景
// 冻结为可复现的确定性产品闭环评估。需求状态由 scripted screening 轮经真实
// 产品管道(StartMessage → pipeline → Reducer → store)产生,零模型、零网络;
// 保存/召回/确认/删除全部走真实 product.Service 与临时 peval_ 库。
// 本评估只覆盖用户显式保存、按显式 subject 手动召回、逐项确认的产品合同,
// 不含自动提取、初筛注入或 Builder 模型注入;PM-STALE 仅验证旧价格/规格经
// 保存入口被白名单拒绝,不把易失事实新鲜度窗口当作已有产品能力。
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	migrations "github.com/subaru-ye/pc-builder-agent/db"
	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// PrefEvalAssertion 是一条确定性断言:负向断言(不得写入、不得召回)同样
// 编码为 Pass/FAIL,不与正向断言抵消。
type PrefEvalAssertion struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// PrefEvalCaseResult 是一个场景的完整结果;Error 非空表示评估设施自身故障
// (technical fault),门禁零容忍,不计入产品行为失败。
type PrefEvalCaseResult struct {
	ID         string              `json:"id"`
	Category   string              `json:"category"`
	Summary    string              `json:"summary"`
	Assertions []PrefEvalAssertion `json:"assertions"`
	Error      string              `json:"error,omitempty"`
}

type PrefEvalGateVerdict struct {
	Name      string `json:"name"`
	Actual    string `json:"actual"`
	Threshold string `json:"threshold"`
	Pass      bool   `json:"pass"`
}

type PrefEvalReport struct {
	SchemaVersion int                  `json:"schema_version"`
	GeneratedAt   time.Time            `json:"generated_at"`
	Mode          string               `json:"mode"`
	Database      string               `json:"database"`
	Cases         []PrefEvalCaseResult `json:"cases"`
	GateVerdicts  []PrefEvalGateVerdict
	GatePassed    bool     `json:"gate_passed"`
	Limitations   []string `json:"limitations"`
}

// prefEvalCategories 是六类场景的冻结分类;check 模式验证注册表覆盖。
var prefEvalCategories = []string{"iso", "tmp", "chg", "del", "stale", "conf"}

// prefEvalEnv 是一次运行的共享设施:一个 Service/gateway 服务全部场景,
// conn 直连临时库做存储层核验(supersede 墓碑、物理删除)。
type prefEvalEnv struct {
	svc  *product.Service
	g    *gateway
	sink *recordingEventSink
	conn *pgx.Conn
}

type prefDriver struct {
	env     *prefEvalEnv
	owner   string
	session store.WebSession
}

// recorder 收集断言;detail 只在失败时填充观测,保持报告可读。
type prefRecorder struct{ assertions []PrefEvalAssertion }

func (r *prefRecorder) check(name string, pass bool, detail string) {
	r.assertions = append(r.assertions, PrefEvalAssertion{Name: name, Pass: pass, Detail: detail})
}

func (r *prefRecorder) equal(name string, got, want any) {
	r.check(name, fmt.Sprint(got) == fmt.Sprint(want), fmt.Sprintf("got %v, want %v", got, want))
}

// newPrefDriver 为 owner 建新会话;同 owner 可再开新会话模拟"以后再来"。
func (e *prefEvalEnv) newDriver(ctx context.Context, owner string) (*prefDriver, error) {
	ws, err := e.svc.CreateSession(ctx, owner, uuid.NewString())
	if err != nil {
		return nil, err
	}
	return &prefDriver{env: e, owner: owner, session: ws}, nil
}

func (d *prefDriver) newSession(ctx context.Context) (store.WebSession, error) {
	return d.env.svc.CreateSession(ctx, d.owner, uuid.NewString())
}

// prefOp 构造一个 stated 证据的 set 操作;quote 必须是 seed 用户消息原文的
// 子串,否则后续保存会被来源核验拒绝(该核验本身就是被评估的合同)。
func prefOp(field string, value any, scope, quote string) schemas.RequirementOperation {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return schemas.RequirementOperation{
		Op: "set", Field: field, Value: raw,
		Evidence: "stated", Scope: scope, Quote: quote,
	}
}

// seed 用 scripted screening 输出驱动一轮真实消息:ops 经产品管道写入
// RequirementState(来源 kind=chat、message_id 由服务端注入)。
func (d *prefDriver) seed(ctx context.Context, text string, ops []schemas.RequirementOperation) error {
	raw, err := json.Marshal(pipeline.RequirementTurnResult{Operations: ops})
	if err != nil {
		return err
	}
	d.env.g.begin(Step{Kind: "message", Text: text, Screen: raw})
	started, err := d.env.svc.StartMessage(ctx, d.owner, d.session.ID, uuid.NewString(), text)
	if err != nil {
		return err
	}
	return waitRun(ctx, d.env.svc, d.owner, started.Run.ID, false)
}

func (d *prefDriver) state(ctx context.Context) (schemas.RequirementState, error) {
	detail, err := d.env.svc.GetSession(ctx, d.owner, d.session.ID)
	if err != nil {
		return schemas.RequirementState{}, err
	}
	st, err := schemas.DecodeRequirementState(detail.Session.RequirementState)
	if err != nil {
		return schemas.RequirementState{}, err
	}
	return st, nil
}

func (d *prefDriver) save(ctx context.Context, field, subject string) (product.PreferenceSaveResult, error) {
	return d.env.svc.SaveSessionPreference(ctx, d.owner, d.session.ID, product.PreferenceSave{Field: field, Subject: subject})
}

// memoryRows 按 status 分组计数,直接核验存储层(active/superseded/物理删除)。
func (e *prefEvalEnv) memoryRows(ctx context.Context, owner, subject, field string) (map[string]int, error) {
	rows, err := e.conn.Query(ctx,
		`SELECT status, count(*) FROM owner_preference_memories WHERE owner_id=$1 AND subject=$2 AND field=$3 GROUP BY status`,
		owner, subject, field)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		out[status] = n
	}
	return out, rows.Err()
}

// problemCode 提取产品稳定问题码;非 Problem 错误返回空串。
func problemCode(err error) string {
	var problem product.Problem
	if errors.As(err, &problem) {
		return problem.Code
	}
	return ""
}

// findSuggestion 在召回结果里定位字段;未出现返回 nil。
func findSuggestion(suggestions []product.PreferenceSuggestion, field string) *product.PreferenceSuggestion {
	for i := range suggestions {
		if suggestions[i].Field == field {
			return &suggestions[i]
		}
	}
	return nil
}

func fieldValueString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// prefEvalCases 是六类场景的冻结注册表;ID 对应 docs/eval/preference-memory/README.md。
var prefEvalCases = []struct {
	ID, Category, Summary string
	Run                   func(ctx context.Context, env *prefEvalEnv) ([]PrefEvalAssertion, error)
}{
	{"PM-ISO-01", "iso", "本人与代配对象偏好隔离:按显式 subject 召回各只返回对应建议,确认不越权写入另一 subject 的字段",
		runPrefEvalISO01},
	{"PM-ISO-02", "iso", "多 owner 冲突:同字段不同值列为 conflict 选项,每个候选值的来源原话可辨认,确认只写入用户所选",
		runPrefEvalISO02},
	{"PM-TMP-01", "tmp", "临时要求不升级为长期偏好:temporary 拒存,存储层无新长期记录,新会话召回仍是被覆盖前的长期值",
		runPrefEvalTMP01},
	{"PM-CHG-01", "chg", "用户更改主意:值变化构成 supersede 链(旧值墓碑),新会话只召回新值,重申同值不产生新记录",
		runPrefEvalCHG01},
	{"PM-DEL-01", "del", "删除后不再召回:物理删除 active 记录与全部旧值/墓碑,重复删除稳定 404",
		runPrefEvalDEL01},
	{"PM-STALE-01", "stale", "旧价格/规格拒绝保存:白名单外字段(预算、用途、free.*)经保存入口被拒,不产生可召回记录",
		runPrefEvalSTALE01},
	{"PM-CONF-01", "conf", "召回建议须确认才生效:未确认不写入 RequirementState,当前会话已明确的值优先不被历史覆盖",
		runPrefEvalCONF01},
}

// PM-ISO-01 本人(self)与代配对象(小王)偏好隔离。
func runPrefEvalISO01(ctx context.Context, env *prefEvalEnv) ([]PrefEvalAssertion, error) {
	r := &prefRecorder{}
	d, err := env.newDriver(ctx, uuid.NewString())
	if err != nil {
		return nil, err
	}
	if err := d.seed(ctx, "我想要安静一点的机子", []schemas.RequirementOperation{
		prefOp("noise_pref", "silent", "session", "安静"),
	}); err != nil {
		return nil, err
	}
	saveSelf, err := d.save(ctx, "noise_pref", schemas.PreferenceSubjectSelf)
	if err != nil {
		return nil, err
	}
	r.equal("save-self-created", saveSelf.Action, product.PreferenceActionCreated)
	if err := d.seed(ctx, "这台是给朋友小王配的,他要 ITX 小机箱", []schemas.RequirementOperation{
		prefOp("size_pref", "itx", "session", "ITX"),
	}); err != nil {
		return nil, err
	}
	saveFriend, err := d.save(ctx, "size_pref", "小王")
	if err != nil {
		return nil, err
	}
	r.equal("save-friend-created", saveFriend.Action, product.PreferenceActionCreated)

	s2, err := d.newSession(ctx)
	if err != nil {
		return nil, err
	}
	d.session = s2
	selfSugg, err := env.svc.PreferenceSuggestions(ctx, []string{d.owner}, s2.ID, schemas.PreferenceSubjectSelf)
	if err != nil {
		return nil, err
	}
	r.equal("recall-self-only-noise", len(selfSugg) == 1 && selfSugg[0].Field == "noise_pref", true)
	r.check("recall-self-excludes-friend-field", findSuggestion(selfSugg, "size_pref") == nil,
		"self 召回返回了小王的 size_pref 建议")
	friendSugg, err := env.svc.PreferenceSuggestions(ctx, []string{d.owner}, s2.ID, "小王")
	if err != nil {
		return nil, err
	}
	r.equal("recall-friend-only-size", len(friendSugg) == 1 && friendSugg[0].Field == "size_pref", true)
	r.check("recall-friend-excludes-self-field", findSuggestion(friendSugg, "noise_pref") == nil,
		"小王召回返回了本人的 noise_pref 建议")

	// 负向断言:确认 self 建议不得把小王的字段写入当前需求。
	state, err := d.state(ctx)
	if err != nil {
		return nil, err
	}
	confirm, err := env.svc.ConfirmPreferences(ctx, []string{d.owner}, s2.ID, uuid.NewString(), product.PreferenceConfirmInput{
		ExpectedRevision: state.Revision, Subject: schemas.PreferenceSubjectSelf,
		MemoryIDs: []string{selfSugg[0].Choices[0].ID},
	})
	if err != nil {
		return nil, err
	}
	r.equal("confirm-self-applied", len(confirm.Applied), 1)
	after, err := d.state(ctx)
	if err != nil {
		return nil, err
	}
	r.equal("confirm-self-noise-active", after.Fields["noise_pref"].Status == "active" &&
		fieldValueString(after.Fields["noise_pref"].Value) == "silent", true)
	r.equal("confirm-self-keeps-friend-unknown", after.Fields["size_pref"].Status, "unknown")
	return r.assertions, nil
}

// PM-ISO-02 多 owner(匿名→认领后的双身份)同字段不同值冲突。
func runPrefEvalISO02(ctx context.Context, env *prefEvalEnv) ([]PrefEvalAssertion, error) {
	r := &prefRecorder{}
	ownerA, ownerC := uuid.NewString(), uuid.NewString()
	dA, err := env.newDriver(ctx, ownerA)
	if err != nil {
		return nil, err
	}
	if err := dA.seed(ctx, "我想要安静一点的机子", []schemas.RequirementOperation{
		prefOp("noise_pref", "silent", "session", "安静"),
	}); err != nil {
		return nil, err
	}
	dC, err := env.newDriver(ctx, ownerC)
	if err != nil {
		return nil, err
	}
	if err := dC.seed(ctx, "先看普通噪音的方案就行", []schemas.RequirementOperation{
		prefOp("noise_pref", "normal", "session", "普通噪音"),
	}); err != nil {
		return nil, err
	}
	for _, d := range []*prefDriver{dA, dC} {
		if _, err := d.save(ctx, "noise_pref", schemas.PreferenceSubjectSelf); err != nil {
			return nil, err
		}
	}
	owners := []string{ownerA, ownerC}
	s, err := dA.newSession(ctx)
	if err != nil {
		return nil, err
	}
	dA.session = s
	sugg, err := env.svc.PreferenceSuggestions(ctx, owners, s.ID, schemas.PreferenceSubjectSelf)
	if err != nil {
		return nil, err
	}
	noise := findSuggestion(sugg, "noise_pref")
	if noise == nil {
		return nil, fmt.Errorf("conflict case: noise_pref 建议缺失")
	}
	r.equal("conflict-status", noise.Status, product.PreferenceSuggestionConflict)
	r.equal("conflict-two-choices", len(noise.Choices), 2)
	values := map[string]bool{}
	quotes := map[string]bool{}
	for _, c := range noise.Choices {
		values[fieldValueString(c.Value)] = true
		quotes[c.Source.Quote] = true
	}
	r.equal("conflict-values-distinct", values["silent"] && values["normal"], true)
	// 来源可辨认:每个候选值携带自己的原话,两条原话互不相同且非空。
	r.equal("conflict-quotes-distinguishable", len(quotes) == 2, true)
	for _, c := range noise.Choices {
		r.check("conflict-quote-nonempty-"+fieldValueString(c.Value), c.Source.Quote != "", "候选值缺少来源原话")
		r.check("conflict-quote-in-owner-message-"+fieldValueString(c.Value),
			strings.Contains(map[bool]string{true: "我想要安静一点的机子", false: "先看普通噪音的方案就行"}[fieldValueString(c.Value) == "silent"], c.Source.Quote),
			"候选原话与归属身份的消息不符")
	}

	// 确认用户挑选的 silent:只写入选中值,另一选项不写入。
	state, err := dA.state(ctx)
	if err != nil {
		return nil, err
	}
	var picked string
	for _, c := range noise.Choices {
		if fieldValueString(c.Value) == "silent" {
			picked = c.ID
		}
	}
	confirm, err := env.svc.ConfirmPreferences(ctx, owners, s.ID, uuid.NewString(), product.PreferenceConfirmInput{
		ExpectedRevision: state.Revision, Subject: schemas.PreferenceSubjectSelf, MemoryIDs: []string{picked},
	})
	if err != nil {
		return nil, err
	}
	r.equal("confirm-picked-applied", len(confirm.Applied), 1)
	after, err := dA.state(ctx)
	if err != nil {
		return nil, err
	}
	r.equal("confirm-picked-value", fieldValueString(after.Fields["noise_pref"].Value), "silent")
	return r.assertions, nil
}

// PM-TMP-01 临时要求(temporary)只在本次会话生效,保存入口拒收。
func runPrefEvalTMP01(ctx context.Context, env *prefEvalEnv) ([]PrefEvalAssertion, error) {
	r := &prefRecorder{}
	d, err := env.newDriver(ctx, uuid.NewString())
	if err != nil {
		return nil, err
	}
	if err := d.seed(ctx, "我平时都认 A 卡", []schemas.RequirementOperation{
		prefOp("brand_pref.gpu", "amd", "session", "A 卡"),
	}); err != nil {
		return nil, err
	}
	if _, err := d.save(ctx, "brand_pref.gpu", schemas.PreferenceSubjectSelf); err != nil {
		return nil, err
	}
	if err := d.seed(ctx, "这次先试试 N 卡,就这一次", []schemas.RequirementOperation{
		prefOp("brand_pref.gpu", "nvidia", "temporary", "N 卡"),
	}); err != nil {
		return nil, err
	}
	state, err := d.state(ctx)
	if err != nil {
		return nil, err
	}
	// 本次会话遵循临时要求:会话需求层 temporary 生效。
	r.equal("temporary-active-in-session", state.Fields["brand_pref.gpu"].Status == "active" &&
		fieldValueString(state.Fields["brand_pref.gpu"].Value) == "nvidia" &&
		state.Fields["brand_pref.gpu"].Scope == "temporary", true)
	_, err = d.save(ctx, "brand_pref.gpu", schemas.PreferenceSubjectSelf)
	r.check("temporary-save-rejected", problemCode(err) == "preference_not_savable",
		fmt.Sprintf("temporary 保存未被拒绝: %v", err))
	rows, err := env.memoryRows(ctx, d.owner, schemas.PreferenceSubjectSelf, "brand_pref.gpu")
	if err != nil {
		return nil, err
	}
	r.equal("storage-single-longterm-amd", rows["active"] == 1 && rows["superseded"] == 0, true)

	s2, err := d.newSession(ctx)
	if err != nil {
		return nil, err
	}
	sugg, err := env.svc.PreferenceSuggestions(ctx, []string{d.owner}, s2.ID, schemas.PreferenceSubjectSelf)
	if err != nil {
		return nil, err
	}
	gpu := findSuggestion(sugg, "brand_pref.gpu")
	r.check("recall-still-amd", gpu != nil && gpu.Status == product.PreferenceSuggestionSuggest &&
		fieldValueString(gpu.Choices[0].Value) == "amd", "新会话召回不是长期 A 卡建议")
	return r.assertions, nil
}

// PM-CHG-01 显式改正构成 supersede 链;重申同值幂等。
func runPrefEvalCHG01(ctx context.Context, env *prefEvalEnv) ([]PrefEvalAssertion, error) {
	r := &prefRecorder{}
	d, err := env.newDriver(ctx, uuid.NewString())
	if err != nil {
		return nil, err
	}
	if err := d.seed(ctx, "以后显卡都用 A 卡", []schemas.RequirementOperation{
		prefOp("brand_pref.gpu", "amd", "session", "A 卡"),
	}); err != nil {
		return nil, err
	}
	first, err := d.save(ctx, "brand_pref.gpu", schemas.PreferenceSubjectSelf)
	if err != nil {
		return nil, err
	}
	r.equal("first-save-created", first.Action, product.PreferenceActionCreated)
	if err := d.seed(ctx, "以后都改用 N 卡了", []schemas.RequirementOperation{
		prefOp("brand_pref.gpu", "nvidia", "session", "N 卡"),
	}); err != nil {
		return nil, err
	}
	second, err := d.save(ctx, "brand_pref.gpu", schemas.PreferenceSubjectSelf)
	if err != nil {
		return nil, err
	}
	r.equal("second-save-superseded", second.Action, product.PreferenceActionSupersede)
	rows, err := env.memoryRows(ctx, d.owner, schemas.PreferenceSubjectSelf, "brand_pref.gpu")
	if err != nil {
		return nil, err
	}
	r.equal("storage-supersede-chain", rows["active"] == 1 && rows["superseded"] == 1, true)
	var chain bool
	err = env.conn.QueryRow(ctx,
		`SELECT m.supersedes IS NOT NULL AND m.supersedes = p.id FROM owner_preference_memories m
		 JOIN owner_preference_memories p ON p.id = m.supersedes
		 WHERE m.owner_id=$1 AND m.subject=$2 AND m.field=$3 AND m.status='active'`,
		d.owner, schemas.PreferenceSubjectSelf, "brand_pref.gpu").Scan(&chain)
	if err != nil {
		return nil, err
	}
	r.equal("storage-chain-links-prev-active", chain, true)

	s2, err := d.newSession(ctx)
	if err != nil {
		return nil, err
	}
	sugg, err := env.svc.PreferenceSuggestions(ctx, []string{d.owner}, s2.ID, schemas.PreferenceSubjectSelf)
	if err != nil {
		return nil, err
	}
	gpu := findSuggestion(sugg, "brand_pref.gpu")
	r.check("recall-new-value-only", gpu != nil && len(gpu.Choices) == 1 &&
		fieldValueString(gpu.Choices[0].Value) == "nvidia", "新会话召回了已 superseded 的 A 卡或值不符")

	if err := d.seed(ctx, "还是 N 卡,没变", []schemas.RequirementOperation{
		prefOp("brand_pref.gpu", "nvidia", "session", "还是 N 卡"),
	}); err != nil {
		return nil, err
	}
	repeat, err := d.save(ctx, "brand_pref.gpu", schemas.PreferenceSubjectSelf)
	if err != nil {
		return nil, err
	}
	r.equal("repeat-save-unchanged", repeat.Action, product.PreferenceActionUnchanged)
	rowsAfter, err := env.memoryRows(ctx, d.owner, schemas.PreferenceSubjectSelf, "brand_pref.gpu")
	if err != nil {
		return nil, err
	}
	r.equal("repeat-save-no-new-rows", rowsAfter["active"]+rowsAfter["superseded"], 2)
	return r.assertions, nil
}

// PM-DEL-01 显式删除:物理删除含墓碑,之后任何会话不再召回。
func runPrefEvalDEL01(ctx context.Context, env *prefEvalEnv) ([]PrefEvalAssertion, error) {
	r := &prefRecorder{}
	d, err := env.newDriver(ctx, uuid.NewString())
	if err != nil {
		return nil, err
	}
	if err := d.seed(ctx, "喜欢白色侧透机箱", []schemas.RequirementOperation{
		prefOp("appearance", "白色侧透", "session", "白色侧透"),
	}); err != nil {
		return nil, err
	}
	saved, err := d.save(ctx, "appearance", schemas.PreferenceSubjectSelf)
	if err != nil {
		return nil, err
	}
	// 追加一次改正,验证删除把 supersede 链一并清掉。
	if err := d.seed(ctx, "其实黑色也行,就黑色吧", []schemas.RequirementOperation{
		prefOp("appearance", "黑色", "session", "黑色"),
	}); err != nil {
		return nil, err
	}
	if _, err := d.save(ctx, "appearance", schemas.PreferenceSubjectSelf); err != nil {
		return nil, err
	}
	rows, err := env.memoryRows(ctx, d.owner, schemas.PreferenceSubjectSelf, "appearance")
	if err != nil {
		return nil, err
	}
	r.equal("storage-before-delete", rows["active"]+rows["superseded"], 2)
	if err := env.svc.DeletePreference(ctx, []string{d.owner}, saved.Preference.ID); err != nil {
		return nil, err
	}
	rowsAfter, err := env.memoryRows(ctx, d.owner, schemas.PreferenceSubjectSelf, "appearance")
	if err != nil {
		return nil, err
	}
	r.equal("storage-physically-deleted", len(rowsAfter), 0)
	s2, err := d.newSession(ctx)
	if err != nil {
		return nil, err
	}
	sugg, err := env.svc.PreferenceSuggestions(ctx, []string{d.owner}, s2.ID, schemas.PreferenceSubjectSelf)
	if err != nil {
		return nil, err
	}
	r.equal("recall-after-delete-empty", len(sugg), 0)
	err = env.svc.DeletePreference(ctx, []string{d.owner}, saved.Preference.ID)
	r.check("repeat-delete-not-found", errors.Is(err, store.ErrPreferenceMemoryNotFound),
		fmt.Sprintf("重复删除未返回稳定 404: %v", err))
	return r.assertions, nil
}

// PM-STALE-01 旧价格/规格经保存入口被白名单拒绝(本阶段仅验证拒绝写入,
// 不评估易失事实的新鲜度窗口——那需要另立的写入合同)。
func runPrefEvalSTALE01(ctx context.Context, env *prefEvalEnv) ([]PrefEvalAssertion, error) {
	r := &prefRecorder{}
	d, err := env.newDriver(ctx, uuid.NewString())
	if err != nil {
		return nil, err
	}
	if err := d.seed(ctx, "促销期那台整机 6999 挺划算,预算就按 6999", []schemas.RequirementOperation{
		prefOp("budget_cny", 6999, "session", "6999"),
	}); err != nil {
		return nil, err
	}
	_, budgetErr := d.save(ctx, "budget_cny", schemas.PreferenceSubjectSelf)
	r.check("budget-save-rejected", problemCode(budgetErr) == "preference_not_savable",
		fmt.Sprintf("预算保存未被拒绝: %v", budgetErr))
	if err := d.seed(ctx, "主要拿来剪视频,要生产力机", []schemas.RequirementOperation{
		prefOp("use_case.type", "productivity", "session", "剪视频"),
	}); err != nil {
		return nil, err
	}
	_, useCaseErr := d.save(ctx, "use_case.type", schemas.PreferenceSubjectSelf)
	r.check("usecase-save-rejected", problemCode(useCaseErr) == "preference_not_savable",
		fmt.Sprintf("用途保存未被拒绝: %v", useCaseErr))
	if err := d.seed(ctx, "机箱里想要 ARGB 风扇海", []schemas.RequirementOperation{
		prefOp("free.风扇", "ARGB 风扇海", "session", "ARGB 风扇海"),
	}); err != nil {
		return nil, err
	}
	_, freeErr := d.save(ctx, "free.风扇", schemas.PreferenceSubjectSelf)
	r.check("free-field-save-rejected", problemCode(freeErr) == "preference_not_savable",
		fmt.Sprintf("free.* 保存未被拒绝: %v", freeErr))

	rows, err := env.memoryRows(ctx, d.owner, schemas.PreferenceSubjectSelf, "budget_cny")
	if err != nil {
		return nil, err
	}
	var total int
	if err := env.conn.QueryRow(ctx,
		`SELECT count(*) FROM owner_preference_memories WHERE owner_id=$1`, d.owner).Scan(&total); err != nil {
		return nil, err
	}
	r.equal("storage-no-records", rows["active"]+rows["superseded"]+total, 0)
	s2, err := d.newSession(ctx)
	if err != nil {
		return nil, err
	}
	sugg, err := env.svc.PreferenceSuggestions(ctx, []string{d.owner}, s2.ID, schemas.PreferenceSubjectSelf)
	if err != nil {
		return nil, err
	}
	r.equal("recall-empty-after-rejections", len(sugg), 0)
	// 负向断言:预算仍由当前会话需求表达,不被历史说法拼接。
	state, err := d.state(ctx)
	if err != nil {
		return nil, err
	}
	r.equal("budget-from-current-session-only", state.Fields["budget_cny"].Status == "active" &&
		fieldValueString(state.Fields["budget_cny"].Value) == "6999" &&
		state.Fields["budget_cny"].Scope == "session", true)
	return r.assertions, nil
}

// PM-CONF-01 召回建议须确认才生效;当前会话已明确的值优先。
func runPrefEvalCONF01(ctx context.Context, env *prefEvalEnv) ([]PrefEvalAssertion, error) {
	r := &prefRecorder{}
	d, err := env.newDriver(ctx, uuid.NewString())
	if err != nil {
		return nil, err
	}
	if err := d.seed(ctx, "我更喜欢 A 卡", []schemas.RequirementOperation{
		prefOp("brand_pref.gpu", "amd", "session", "A 卡"),
	}); err != nil {
		return nil, err
	}
	if _, err := d.save(ctx, "brand_pref.gpu", schemas.PreferenceSubjectSelf); err != nil {
		return nil, err
	}

	// 当前需求优先:会话里已明确 N 卡,召回不再建议该字段(历史不覆盖本轮)。
	s2, err := d.newSession(ctx)
	if err != nil {
		return nil, err
	}
	d.session = s2
	if err := d.seed(ctx, "这次要 N 卡", []schemas.RequirementOperation{
		prefOp("brand_pref.gpu", "nvidia", "session", "N 卡"),
	}); err != nil {
		return nil, err
	}
	sugg, err := env.svc.PreferenceSuggestions(ctx, []string{d.owner}, s2.ID, schemas.PreferenceSubjectSelf)
	if err != nil {
		return nil, err
	}
	r.check("recall-excludes-active-field", findSuggestion(sugg, "brand_pref.gpu") == nil,
		"当前会话已明确的字段仍出现在建议里")
	current, err := d.state(ctx)
	if err != nil {
		return nil, err
	}
	r.equal("current-need-not-overridden", fieldValueString(current.Fields["brand_pref.gpu"].Value), "nvidia")

	// 确认前不生效:干净会话载入建议但不确认,需求状态不被写入。
	s3, err := d.newSession(ctx)
	if err != nil {
		return nil, err
	}
	sugg3, err := env.svc.PreferenceSuggestions(ctx, []string{d.owner}, s3.ID, schemas.PreferenceSubjectSelf)
	if err != nil {
		return nil, err
	}
	gpu := findSuggestion(sugg3, "brand_pref.gpu")
	if gpu == nil {
		return nil, fmt.Errorf("confirm case: 干净会话未返回显卡建议")
	}
	detail, err := env.svc.GetSession(ctx, d.owner, s3.ID)
	if err != nil {
		return nil, err
	}
	fresh, err := schemas.DecodeRequirementState(detail.Session.RequirementState)
	if err != nil {
		return nil, err
	}
	r.equal("unconfirmed-not-applied", fresh.Fields["brand_pref.gpu"].Status, "unknown")

	// 白名单外预算:保存被拒,干净会话预算不受历史说法影响(负向断言)。
	if err := d.seed(ctx, "预算通常 1 万", []schemas.RequirementOperation{
		prefOp("budget_cny", 10000, "session", "1 万"),
	}); err != nil {
		return nil, err
	}
	_, budgetSaveErr := d.save(ctx, "budget_cny", schemas.PreferenceSubjectSelf)
	r.check("budget-memory-save-rejected", problemCode(budgetSaveErr) == "preference_not_savable",
		fmt.Sprintf("预算保存未被拒绝: %v", budgetSaveErr))

	// 确认后写入:面板编辑(kind=edit)口径,历史原话不伪装成本轮消息。
	s4, err := d.newSession(ctx)
	if err != nil {
		return nil, err
	}
	d.session = s4
	sugg4, err := env.svc.PreferenceSuggestions(ctx, []string{d.owner}, s4.ID, schemas.PreferenceSubjectSelf)
	if err != nil {
		return nil, err
	}
	gpu4 := findSuggestion(sugg4, "brand_pref.gpu")
	if gpu4 == nil {
		return nil, fmt.Errorf("confirm case: 确认会话未返回显卡建议")
	}
	state4, err := d.state(ctx)
	if err != nil {
		return nil, err
	}
	r.equal("s4-budget-unknown-before-confirm", state4.Fields["budget_cny"].Status, "unknown")
	confirm, err := env.svc.ConfirmPreferences(ctx, []string{d.owner}, s4.ID, uuid.NewString(), product.PreferenceConfirmInput{
		ExpectedRevision: state4.Revision, Subject: schemas.PreferenceSubjectSelf,
		MemoryIDs: []string{gpu4.Choices[0].ID},
	})
	if err != nil {
		return nil, err
	}
	r.equal("confirm-applied", len(confirm.Applied), 1)
	after, err := d.state(ctx)
	if err != nil {
		return nil, err
	}
	gpuField := after.Fields["brand_pref.gpu"]
	r.equal("confirm-writes-field", gpuField.Status == "active" && fieldValueString(gpuField.Value) == "amd", true)
	r.equal("confirm-edit-source", gpuField.Source != nil && gpuField.Source.Kind == "edit", true)
	r.equal("confirm-keeps-budget-unknown", after.Fields["budget_cny"].Status, "unknown")
	return r.assertions, nil
}

// CheckPreferenceMemoryEval 是零网络自检:验证场景注册表覆盖六类分类、
// ID 唯一且每类至少一个用例;不读取任何 DSN。
func CheckPreferenceMemoryEval() error {
	seen := map[string]bool{}
	cats := map[string]int{}
	for _, c := range prefEvalCases {
		if c.ID == "" || c.Summary == "" || c.Run == nil {
			return fmt.Errorf("preference eval: 用例 %q 字段不完整", c.ID)
		}
		if seen[c.ID] {
			return fmt.Errorf("preference eval: 重复用例 ID %q", c.ID)
		}
		seen[c.ID] = true
		cats[c.Category]++
	}
	for _, cat := range prefEvalCategories {
		if cats[cat] == 0 {
			return fmt.Errorf("preference eval: 分类 %q 缺少用例", cat)
		}
	}
	return nil
}

// RunPreferenceMemoryEval 在一次性临时库上执行全部场景并落门禁判定。
// serverDSN 只提供服务器(必须 localhost);临时库名以 peval_prefeval_ 开头,
// 迁移后执行,结束物理删除,不触碰主库。
func RunPreferenceMemoryEval(ctx context.Context, serverDSN string) (*PrefEvalReport, error) {
	if err := CheckPreferenceMemoryEval(); err != nil {
		return nil, err
	}
	u, err := url.Parse(serverDSN)
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		return nil, fmt.Errorf("preference eval: 需要指向 localhost 的 PostgreSQL 服务器 DSN")
	}
	admin, err := pgx.Connect(ctx, serverDSN)
	if err != nil {
		return nil, fmt.Errorf("preference eval: 服务器不可达: %w", err)
	}
	name := fmt.Sprintf("peval_prefeval_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		_ = admin.Close(ctx)
		return nil, fmt.Errorf("preference eval: 创建临时库失败: %w", err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		_ = admin.Close(ctx)
	}()
	evalURL := *u
	evalURL.Path = "/" + name
	dsn := evalURL.String()
	dbh, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	goose.SetBaseFS(migrations.Migrations)
	goose.SetLogger(goose.NopLogger())
	_ = goose.SetDialect("postgres")
	err = goose.UpContext(ctx, dbh, "migrations")
	_ = dbh.Close()
	if err != nil {
		return nil, fmt.Errorf("preference eval: 迁移失败: %w", err)
	}

	st, err := store.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(ctx) }()
	g := &gateway{store: st}
	sink := newRecordingEventSink()
	svc, err := product.NewService(ctx, st, g, sink)
	if err != nil {
		return nil, err
	}
	env := &prefEvalEnv{svc: svc, g: g, sink: sink, conn: conn}

	report := &PrefEvalReport{
		SchemaVersion: 1, GeneratedAt: time.Now().UTC(), Mode: "deterministic", Database: name,
		Limitations: []string{
			"零模型:需求状态由 scripted screening 经真实产品管道产生,不评估模型提取效果。",
			"只覆盖显式保存/手动召回/逐项确认产品合同;无自动提取、初筛注入或 Builder 模型注入。",
			"PM-STALE-01 仅验证旧价格/规格经保存入口被白名单拒绝;易失事实新鲜度窗口(7 天)是召回侧防御,不是可保存的产品能力,未评估。",
			"多 owner 冲突以服务层双 owner 直连构造,未经过 auth 认领流程(该流程由浏览器走查与 auth 测试覆盖)。",
		},
	}
	for _, c := range prefEvalCases {
		result := PrefEvalCaseResult{ID: c.ID, Category: c.Category, Summary: c.Summary, Assertions: []PrefEvalAssertion{}}
		assertions, runErr := func() (assertions []PrefEvalAssertion, err error) {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("panic: %v", p)
				}
			}()
			return c.Run(ctx, env)
		}()
		if runErr != nil {
			result.Error = runErr.Error()
		}
		result.Assertions = assertions
		report.Cases = append(report.Cases, result)
	}
	_ = svc.Shutdown(ctx)

	failed := 0
	faults := 0
	for _, c := range report.Cases {
		if c.Error != "" {
			faults++
			continue
		}
		for _, a := range c.Assertions {
			if !a.Pass {
				failed++
			}
		}
	}
	failedCases := 0
	for _, c := range report.Cases {
		if c.Error != "" {
			continue
		}
		for _, a := range c.Assertions {
			if !a.Pass {
				failedCases++
				break
			}
		}
	}
	sort.Slice(report.Cases, func(i, j int) bool { return report.Cases[i].ID < report.Cases[j].ID })
	report.GateVerdicts = []PrefEvalGateVerdict{
		{Name: "technical_fault", Actual: fmt.Sprint(faults), Threshold: "=0", Pass: faults == 0},
		{Name: "failed_assertions", Actual: fmt.Sprint(failed), Threshold: "=0", Pass: failed == 0},
		{Name: "failed_cases", Actual: fmt.Sprint(failedCases), Threshold: "=0", Pass: failedCases == 0},
		{Name: "categories_covered", Actual: fmt.Sprint(len(prefEvalCategories)), Threshold: "=6", Pass: len(prefEvalCategories) == 6},
	}
	report.GatePassed = true
	for _, v := range report.GateVerdicts {
		if !v.Pass {
			report.GatePassed = false
		}
	}
	return report, nil
}

// WritePrefEvalReport 落盘 report.json 与 report.md;目录必须不存在。
func WritePrefEvalReport(dir string, report *PrefEvalReport, command string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), raw, 0o644); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# 偏好记忆确定性评估报告\n\n")
	fmt.Fprintf(&b, "- 生成时间:%s\n- 临时库:%s(运行后已删除)\n- 门禁:%s\n",
		report.GeneratedAt.Format(time.RFC3339), report.Database, map[bool]string{true: "PASS", false: "FAIL"}[report.GatePassed])
	if command != "" {
		fmt.Fprintf(&b, "- 复现命令:`%s`\n", command)
	}
	b.WriteString("\n## 边界\n\n")
	for _, l := range report.Limitations {
		fmt.Fprintf(&b, "- %s\n", l)
	}
	b.WriteString("\n## 场景结果\n\n| 用例 | 分类 | 断言 | 结果 |\n|---|---|---|---|\n")
	for _, c := range report.Cases {
		passed := 0
		for _, a := range c.Assertions {
			if a.Pass {
				passed++
			}
		}
		verdict := fmt.Sprintf("%d/%d", passed, len(c.Assertions))
		if c.Error != "" {
			verdict = "ERROR"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", c.ID, c.Category, verdict,
			map[bool]string{true: "✅", false: "❌"}[c.Error == "" && passed == len(c.Assertions)])
	}
	var failures []string
	for _, c := range report.Cases {
		if c.Error != "" {
			failures = append(failures, fmt.Sprintf("- %s: %s", c.ID, c.Error))
			continue
		}
		for _, a := range c.Assertions {
			if !a.Pass {
				failures = append(failures, fmt.Sprintf("- %s / %s: %s", c.ID, a.Name, a.Detail))
			}
		}
	}
	if len(failures) > 0 {
		b.WriteString("\n## 失败明细\n\n")
		b.WriteString(strings.Join(failures, "\n"))
		b.WriteString("\n")
	}
	return os.WriteFile(filepath.Join(dir, "report.md"), []byte(b.String()), 0o644)
}
