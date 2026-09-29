package planningeval

// 偏好记忆 → Builder 交接评估:追踪用户确认偏好后,值与 must/prefer 强度
// 从 RequirementState 经需求投影(RequirementStateSpec → 核定预览
// RequirementReviewSpec)到确认事务冻结的 Builder 载荷(PlanningBuilderInput →
// PlanningInput.EffectiveConstraints)的确定性传递。scripted builder
// (gateway.Remote)记录确认入口实际派发的完整冻结载荷,断言只读该载荷:
// 未确认记忆不得进入、确认后值与强度保真进入、当前会话明确要求优先、
// 多 owner 冲突未经选择不得进入。
// 全程零模型、零外部网络;需求状态由 scripted screening 经真实产品管道产生。
// 不做自动召回注入,不调用真实 Builder 模型,不检验选件算法(planning-v2 范围);
// 载荷 hash 口径与 V5 一致:实际派发的完整 PlanningInput 规范化 hash 必须等于
// run 冻结的 builder_input_hash。
import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// prefHandoffCategories 是交接评估的四类断言维度;check 模式验证注册表覆盖。
var prefHandoffCategories = []string{"unconfirmed", "confirmed", "current-first", "owner-conflict"}

// prefHandoffCases 是交接评估的场景注册表;复用阶段四的执行器与门禁口径。
var prefHandoffCases = []prefEvalCaseSpec{
	{"PM-BH-01", "unconfirmed", "未确认记忆不得进入冻结载荷:建议在但不确认,冻结载荷该字段按系统默认展开、无强度,记忆行无副作用",
		runPrefHandoff01},
	{"PM-BH-02", "confirmed", "确认后值与强度保真进入:must/prefer 两条确认后,RequirementState 与冻结载荷的值、constraint_strengths 逐项一致",
		runPrefHandoff02},
	{"PM-BH-03", "current-first", "当前会话要求优先:本轮明确表达的值与强度进入冻结载荷,历史记忆不出现在建议与载荷中",
		runPrefHandoff03},
	{"PM-BH-04", "owner-conflict", "多 owner 冲突未经选择不得进入:冲突双选项悬而未决时载荷无该字段;选择后所选值与强度进入",
		runPrefHandoff04},
}

// bhOp 构造带 must/prefer 强度的 stated set 操作;quote 必须是 seed 消息
// 原文的子串,否则经产品管道写入前就被来源核验拒绝。
func bhOp(field string, value any, strength, scope, quote string) schemas.RequirementOperation {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return schemas.RequirementOperation{
		Op: "set", Field: field, Value: raw,
		Strength: strength, Evidence: "stated", Scope: scope, Quote: quote,
	}
}

// seedHandoffBase 预置确认就绪的最小必填需求(gaming/1080p/预算/明确全新
// 无已有件),之后 StartConfirm 才能通过 readiness 与核定预览校验。
// "没有旧件"措辞须命中 existingPartsClearedEvidence 白名单:空 existing_parts
// 是被守卫的关键字段,泛化措辞会被降级为观察、状态不 ready。
func seedHandoffBase(ctx context.Context, d *prefDriver) error {
	return d.seed(ctx, "主要打游戏,1080p 显示器,预算 8000,整机全新没有旧件",
		[]schemas.RequirementOperation{
			bhOp("use_case.type", "gaming", "must", "session", "打游戏"),
			bhOp("use_case.resolution", "1080p", "must", "session", "1080p"),
			bhOp("budget_cny", 8000, "must", "session", "8000"),
			bhOp("existing_parts", []any{}, "must", "session", "没有旧件"),
		})
}

// confirmHandoffPayload 像真实客户端一样走 StartConfirm:确认事务冻结载荷后
// 由 executeRemote 派发给 scripted builder。返回 builder 实际收到的完整
// PlanningInput 与 run 冻结的 builder_input_hash。scripted builder 没有
// oracle 输出,planning 执行以失败终态结束是刻意行为——载荷观测不依赖 run
// 成功;builder 未收到载荷(确认→派发链路断链)按技术故障返回错误。
func confirmHandoffPayload(ctx context.Context, env *prefEvalEnv, d *prefDriver) (*schemas.PlanningInput, string, error) {
	detail, err := env.svc.GetSession(ctx, d.owner, d.session.ID)
	if err != nil {
		return nil, "", err
	}
	env.g.begin(Step{Kind: "confirm"})
	started, err := env.svc.StartConfirm(ctx, d.owner, d.session.ID, uuid.NewString(), confirmRequestFromDetail(detail))
	if err != nil {
		return nil, "", err
	}
	_ = waitRun(ctx, env.svc, d.owner, started.Run.ID, false)
	record := env.g.snapshot()
	if record.PlanningInput == nil {
		run, getErr := env.svc.GetRun(ctx, d.owner, started.Run.ID)
		return nil, "", fmt.Errorf("scripted builder 未收到冻结载荷(run %s, status=%s, error=%q, getErr=%v)",
			started.Run.ID, run.Status, run.Error, getErr)
	}
	after, err := env.svc.GetSession(ctx, d.owner, d.session.ID)
	if err != nil {
		return nil, "", err
	}
	return record.PlanningInput, after.Axes.Build.BuilderInputHash, nil
}

// handoffSpec 解码冻结载荷里的有效选型约束(RequirementSpec v2 线上格式)。
func handoffSpec(payload *schemas.PlanningInput) (schemas.RequirementSpec, error) {
	if payload.EffectiveConstraints == nil {
		return schemas.RequirementSpec{}, fmt.Errorf("冻结载荷缺少 effective_constraints")
	}
	return schemas.DecodeRequirementSpec(payload.EffectiveConstraints.Spec)
}

// checkHandoffPayloadHash 落 V5 口径断言:scripted builder 实际收到的完整
// PlanningInput 规范化 hash 必须等于 run 冻结的 builder_input_hash。
func checkHandoffPayloadHash(r *prefRecorder, payload *schemas.PlanningInput, frozen string) {
	raw, err := json.Marshal(payload)
	if err != nil {
		r.check("payload-hash-matches-frozen", false, fmt.Sprintf("marshal: %v", err))
		return
	}
	r.equal("payload-hash-matches-frozen", normalizedHash(raw), frozen)
}

// handoffDefaultField 报告冻结载荷的默认展开清单是否包含某字段。
func handoffDefaultField(payload *schemas.PlanningInput, field string) bool {
	if payload.EffectiveConstraints == nil {
		return false
	}
	for _, def := range payload.EffectiveConstraints.Defaults {
		if def.Field == field {
			return true
		}
	}
	return false
}

// PM-BH-01 未确认记忆不得进入冻结 Builder 载荷。
func runPrefHandoff01(ctx context.Context, env *prefEvalEnv) ([]PrefEvalAssertion, error) {
	r := &prefRecorder{}
	d, err := env.newDriver(ctx, uuid.NewString())
	if err != nil {
		return r.assertions, err
	}
	if err := d.seed(ctx, "装机必须要静音,太吵不行", []schemas.RequirementOperation{
		bhOp("noise_pref", "silent", "must", "session", "静音"),
	}); err != nil {
		return r.assertions, err
	}
	if _, err := d.save(ctx, "noise_pref", schemas.PreferenceSubjectSelf); err != nil {
		return r.assertions, err
	}
	rows, err := env.memoryRows(ctx, d.owner, schemas.PreferenceSubjectSelf, "noise_pref")
	if err != nil {
		return r.assertions, err
	}
	r.equal("memory-active-before-confirm", rows["active"], 1)

	// 新会话只补必填需求并载入建议,不确认任何记忆。
	s2, err := d.newSession(ctx)
	if err != nil {
		return r.assertions, err
	}
	d.session = s2
	if err := seedHandoffBase(ctx, d); err != nil {
		return r.assertions, err
	}
	sugg, err := env.svc.PreferenceSuggestions(ctx, []string{d.owner}, s2.ID, schemas.PreferenceSubjectSelf)
	if err != nil {
		return r.assertions, err
	}
	noise := findSuggestion(sugg, "noise_pref")
	if noise == nil {
		return r.assertions, fmt.Errorf("bh01: 未返回静音建议")
	}
	r.equal("suggestion-present-unconfirmed", noise.Status, product.PreferenceSuggestionSuggest)
	state, err := d.state(ctx)
	if err != nil {
		return r.assertions, err
	}
	r.equal("state-noise-unknown", state.Fields["noise_pref"].Status, "unknown")

	payload, frozen, err := confirmHandoffPayload(ctx, env, d)
	if err != nil {
		return r.assertions, err
	}
	spec, err := handoffSpec(payload)
	if err != nil {
		return r.assertions, err
	}
	checkHandoffPayloadHash(r, payload, frozen)
	r.equal("payload-noise-default-any", string(spec.NoisePref), "any")
	r.equal("payload-strengths-lack-noise", spec.ConstraintStrengths["noise_pref"], "")
	r.equal("payload-noise-from-system-default", handoffDefaultField(payload, "noise_pref"), true)
	rowsAfter, err := env.memoryRows(ctx, d.owner, schemas.PreferenceSubjectSelf, "noise_pref")
	if err != nil {
		return r.assertions, err
	}
	r.equal("memory-unchanged-without-confirm", rowsAfter["active"], 1)
	return r.assertions, nil
}

// PM-BH-02 确认后值与 must/prefer 强度保真进入冻结 Builder 载荷。
func runPrefHandoff02(ctx context.Context, env *prefEvalEnv) ([]PrefEvalAssertion, error) {
	r := &prefRecorder{}
	d, err := env.newDriver(ctx, uuid.NewString())
	if err != nil {
		return r.assertions, err
	}
	if err := d.seed(ctx, "装机要安静;显卡倾向 A 卡", []schemas.RequirementOperation{
		bhOp("noise_pref", "silent", "must", "session", "安静"),
		bhOp("brand_pref.gpu", "amd", "prefer", "session", "A 卡"),
	}); err != nil {
		return r.assertions, err
	}
	if _, err := d.save(ctx, "noise_pref", schemas.PreferenceSubjectSelf); err != nil {
		return r.assertions, err
	}
	if _, err := d.save(ctx, "brand_pref.gpu", schemas.PreferenceSubjectSelf); err != nil {
		return r.assertions, err
	}

	s2, err := d.newSession(ctx)
	if err != nil {
		return r.assertions, err
	}
	d.session = s2
	if err := seedHandoffBase(ctx, d); err != nil {
		return r.assertions, err
	}
	sugg, err := env.svc.PreferenceSuggestions(ctx, []string{d.owner}, s2.ID, schemas.PreferenceSubjectSelf)
	if err != nil {
		return r.assertions, err
	}
	noise := findSuggestion(sugg, "noise_pref")
	gpu := findSuggestion(sugg, "brand_pref.gpu")
	if noise == nil || gpu == nil {
		return r.assertions, fmt.Errorf("bh02: 建议缺失(noise=%v, gpu=%v)", noise != nil, gpu != nil)
	}
	r.equal("suggestion-noise-must", noise.Choices[0].Strength, schemas.PreferenceStrengthMust)
	r.equal("suggestion-gpu-prefer", gpu.Choices[0].Strength, schemas.PreferenceStrengthPrefer)
	state, err := d.state(ctx)
	if err != nil {
		return r.assertions, err
	}
	confirm, err := env.svc.ConfirmPreferences(ctx, []string{d.owner}, s2.ID, uuid.NewString(), product.PreferenceConfirmInput{
		ExpectedRevision: state.Revision, Subject: schemas.PreferenceSubjectSelf,
		MemoryIDs: []string{noise.Choices[0].ID, gpu.Choices[0].ID},
	})
	if err != nil {
		return r.assertions, err
	}
	r.equal("confirm-applied-two", len(confirm.Applied), 2)
	after, err := d.state(ctx)
	if err != nil {
		return r.assertions, err
	}
	r.equal("state-noise-silent-must", after.Fields["noise_pref"].Status == "active" &&
		fieldValueString(after.Fields["noise_pref"].Value) == "silent" &&
		after.Fields["noise_pref"].Strength == "must", true)
	r.equal("state-gpu-amd-prefer", after.Fields["brand_pref.gpu"].Status == "active" &&
		fieldValueString(after.Fields["brand_pref.gpu"].Value) == "amd" &&
		after.Fields["brand_pref.gpu"].Strength == "prefer", true)

	payload, frozen, err := confirmHandoffPayload(ctx, env, d)
	if err != nil {
		return r.assertions, err
	}
	spec, err := handoffSpec(payload)
	if err != nil {
		return r.assertions, err
	}
	checkHandoffPayloadHash(r, payload, frozen)
	r.equal("payload-noise-silent", string(spec.NoisePref), "silent")
	r.equal("payload-noise-must", spec.ConstraintStrengths["noise_pref"], schemas.PreferenceStrengthMust)
	r.equal("payload-gpu-amd", string(spec.BrandPref.GPU), "amd")
	r.equal("payload-gpu-prefer", spec.ConstraintStrengths["brand_pref.gpu"], schemas.PreferenceStrengthPrefer)
	r.equal("payload-noise-not-default", handoffDefaultField(payload, "noise_pref"), false)
	// 载荷内 RequirementState 同样携带确认后的字段与强度(State 层传递不丢失)。
	payloadNoise := payload.State.Fields["noise_pref"]
	r.equal("payload-state-noise-active-must", payloadNoise.Status == "active" &&
		payloadNoise.Strength == "must" && fieldValueString(payloadNoise.Value) == "silent", true)
	payloadGPU := payload.State.Fields["brand_pref.gpu"]
	r.equal("payload-state-gpu-active-prefer", payloadGPU.Status == "active" &&
		payloadGPU.Strength == "prefer" && fieldValueString(payloadGPU.Value) == "amd", true)
	return r.assertions, nil
}

// PM-BH-03 当前会话明确要求优先:历史记忆不出现在建议与冻结载荷中。
func runPrefHandoff03(ctx context.Context, env *prefEvalEnv) ([]PrefEvalAssertion, error) {
	r := &prefRecorder{}
	d, err := env.newDriver(ctx, uuid.NewString())
	if err != nil {
		return r.assertions, err
	}
	if err := d.seed(ctx, "以后显卡都用 A 卡", []schemas.RequirementOperation{
		bhOp("brand_pref.gpu", "amd", "prefer", "session", "A 卡"),
	}); err != nil {
		return r.assertions, err
	}
	if _, err := d.save(ctx, "brand_pref.gpu", schemas.PreferenceSubjectSelf); err != nil {
		return r.assertions, err
	}

	// 新会话本轮明确表达 N 卡(must):当前需求优先,召回不再建议该字段。
	s2, err := d.newSession(ctx)
	if err != nil {
		return r.assertions, err
	}
	d.session = s2
	if err := seedHandoffBase(ctx, d); err != nil {
		return r.assertions, err
	}
	if err := d.seed(ctx, "这次必须要 N 卡,别的都不考虑", []schemas.RequirementOperation{
		bhOp("brand_pref.gpu", "nvidia", "must", "session", "必须要 N 卡"),
	}); err != nil {
		return r.assertions, err
	}
	sugg, err := env.svc.PreferenceSuggestions(ctx, []string{d.owner}, s2.ID, schemas.PreferenceSubjectSelf)
	if err != nil {
		return r.assertions, err
	}
	r.check("suggestion-excludes-current-field", findSuggestion(sugg, "brand_pref.gpu") == nil,
		"当前会话已明确的字段仍出现在建议里")
	current, err := d.state(ctx)
	if err != nil {
		return r.assertions, err
	}
	r.equal("state-gpu-current-nvidia-must", current.Fields["brand_pref.gpu"].Status == "active" &&
		fieldValueString(current.Fields["brand_pref.gpu"].Value) == "nvidia" &&
		current.Fields["brand_pref.gpu"].Strength == "must", true)

	payload, frozen, err := confirmHandoffPayload(ctx, env, d)
	if err != nil {
		return r.assertions, err
	}
	spec, err := handoffSpec(payload)
	if err != nil {
		return r.assertions, err
	}
	checkHandoffPayloadHash(r, payload, frozen)
	r.equal("payload-gpu-current-nvidia", string(spec.BrandPref.GPU), "nvidia")
	r.equal("payload-gpu-current-must", spec.ConstraintStrengths["brand_pref.gpu"], schemas.PreferenceStrengthMust)
	r.equal("payload-gpu-not-history-amd", string(spec.BrandPref.GPU) != "amd", true)
	payloadGPU := payload.State.Fields["brand_pref.gpu"]
	r.equal("payload-state-gpu-current", payloadGPU.Status == "active" &&
		payloadGPU.Strength == "must" && fieldValueString(payloadGPU.Value) == "nvidia", true)
	return r.assertions, nil
}

// PM-BH-04 多 owner 冲突未经选择不得进入冻结载荷;选择后所选值进入。
func runPrefHandoff04(ctx context.Context, env *prefEvalEnv) ([]PrefEvalAssertion, error) {
	r := &prefRecorder{}
	ownerA, ownerC := uuid.NewString(), uuid.NewString()
	dA, err := env.newDriver(ctx, ownerA)
	if err != nil {
		return r.assertions, err
	}
	if err := dA.seed(ctx, "我必须要 A 卡", []schemas.RequirementOperation{
		bhOp("brand_pref.gpu", "amd", "must", "session", "A 卡"),
	}); err != nil {
		return r.assertions, err
	}
	dC, err := env.newDriver(ctx, ownerC)
	if err != nil {
		return r.assertions, err
	}
	if err := dC.seed(ctx, "我必须要 N 卡", []schemas.RequirementOperation{
		bhOp("brand_pref.gpu", "nvidia", "must", "session", "N 卡"),
	}); err != nil {
		return r.assertions, err
	}
	for _, d := range []*prefDriver{dA, dC} {
		if _, err := d.save(ctx, "brand_pref.gpu", schemas.PreferenceSubjectSelf); err != nil {
			return nil, err
		}
	}
	owners := []string{ownerA, ownerC}

	// 双身份新会话:召回得到冲突双选项,不确认任何一方,直接确认需求单。
	s, err := dA.newSession(ctx)
	if err != nil {
		return r.assertions, err
	}
	dA.session = s
	if err := seedHandoffBase(ctx, dA); err != nil {
		return r.assertions, err
	}
	sugg, err := env.svc.PreferenceSuggestions(ctx, owners, s.ID, schemas.PreferenceSubjectSelf)
	if err != nil {
		return r.assertions, err
	}
	gpu := findSuggestion(sugg, "brand_pref.gpu")
	if gpu == nil {
		return r.assertions, fmt.Errorf("bh04: 冲突建议缺失")
	}
	r.equal("conflict-status", gpu.Status, product.PreferenceSuggestionConflict)
	r.equal("conflict-two-choices", len(gpu.Choices), 2)

	payload, frozen, err := confirmHandoffPayload(ctx, env, dA)
	if err != nil {
		return r.assertions, err
	}
	spec, err := handoffSpec(payload)
	if err != nil {
		return r.assertions, err
	}
	checkHandoffPayloadHash(r, payload, frozen)
	r.equal("payload-gpu-any-unchosen", string(spec.BrandPref.GPU), "any")
	r.equal("payload-strengths-lack-gpu", spec.ConstraintStrengths["brand_pref.gpu"], "")
	r.equal("payload-gpu-from-system-default", handoffDefaultField(payload, "brand_pref.gpu"), true)

	// 对照:同身份新会话选择 nvidia 后,所选值与强度进入冻结载荷。
	s3, err := dA.newSession(ctx)
	if err != nil {
		return r.assertions, err
	}
	dA.session = s3
	if err := seedHandoffBase(ctx, dA); err != nil {
		return r.assertions, err
	}
	sugg3, err := env.svc.PreferenceSuggestions(ctx, owners, s3.ID, schemas.PreferenceSubjectSelf)
	if err != nil {
		return r.assertions, err
	}
	gpu3 := findSuggestion(sugg3, "brand_pref.gpu")
	if gpu3 == nil {
		return r.assertions, fmt.Errorf("bh04: 对照会话冲突建议缺失")
	}
	var picked string
	for _, c := range gpu3.Choices {
		if fieldValueString(c.Value) == "nvidia" {
			picked = c.ID
		}
	}
	if picked == "" {
		return r.assertions, fmt.Errorf("bh04: 冲突选项中未找到 nvidia")
	}
	state3, err := dA.state(ctx)
	if err != nil {
		return r.assertions, err
	}
	confirm, err := env.svc.ConfirmPreferences(ctx, owners, s3.ID, uuid.NewString(), product.PreferenceConfirmInput{
		ExpectedRevision: state3.Revision, Subject: schemas.PreferenceSubjectSelf, MemoryIDs: []string{picked},
	})
	if err != nil {
		return r.assertions, err
	}
	r.equal("chosen-confirm-applied", len(confirm.Applied), 1)
	payload3, frozen3, err := confirmHandoffPayload(ctx, env, dA)
	if err != nil {
		return r.assertions, err
	}
	spec3, err := handoffSpec(payload3)
	if err != nil {
		return r.assertions, err
	}
	checkHandoffPayloadHash(r, payload3, frozen3)
	r.equal("payload-gpu-chosen-nvidia", string(spec3.BrandPref.GPU), "nvidia")
	r.equal("payload-gpu-chosen-must", spec3.ConstraintStrengths["brand_pref.gpu"], schemas.PreferenceStrengthMust)
	return r.assertions, nil
}

// CheckPreferenceHandoffEval 是零网络自检:验证交接场景注册表四类断言维度
// 各有用例、ID 唯一;不读取任何 DSN。
func CheckPreferenceHandoffEval() error {
	seen := map[string]bool{}
	cats := map[string]int{}
	for _, c := range prefHandoffCases {
		if c.ID == "" || c.Summary == "" || c.Run == nil {
			return fmt.Errorf("preference handoff eval: 用例 %q 字段不完整", c.ID)
		}
		if seen[c.ID] {
			return fmt.Errorf("preference handoff eval: 重复用例 ID %q", c.ID)
		}
		seen[c.ID] = true
		cats[c.Category]++
	}
	for _, cat := range prefHandoffCategories {
		if cats[cat] == 0 {
			return fmt.Errorf("preference handoff eval: 分类 %q 缺少用例", cat)
		}
	}
	return nil
}

// RunPreferenceHandoffEval 在一次性临时库(peval_handoff_ 前缀)上执行四类
// 交接场景并落门禁判定;serverDSN 只接受 localhost 服务器。
func RunPreferenceHandoffEval(ctx context.Context, serverDSN string) (*PrefEvalReport, error) {
	if err := CheckPreferenceHandoffEval(); err != nil {
		return nil, err
	}
	env, cleanup, err := setupPrefEvalEnv(ctx, serverDSN, "peval_handoff_")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	report := &PrefEvalReport{
		SchemaVersion: 1, GeneratedAt: time.Now().UTC(), Mode: "deterministic-handoff", Database: env.dbName,
		Limitations: []string{
			"零模型:scripted builder 只记录确认事务冻结并派发的完整 PlanningInput,不执行真实选件,不评估 Builder 输出质量(planning-v2 范围)。",
			"只评估偏好经确认进入冻结 Builder 载荷的确定性传递;不做自动召回注入、初筛注入或 Builder 模型注入。",
			"载荷 hash 口径与 V5 一致:scripted builder 实际收到的完整 PlanningInput 规范化 hash 必须等于 run 冻结的 builder_input_hash。",
			"多 owner 冲突以服务层双 owner 直连构造,未经过 auth 认领流程(同阶段四基线)。",
		},
	}
	for _, c := range prefHandoffCases {
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
	executePrefEvalGates(report, prefHandoffCategories, "=4")
	return report, nil
}
