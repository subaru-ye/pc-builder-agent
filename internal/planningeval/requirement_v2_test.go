package planningeval

// Requirement v2 评估设施自测：fixture 完整性、manifest/split 校验、
// grader 金丝雀、确定性层基线红绿分布、replay 与 compare。
// 数据库门控的 policy/ui 基线见 PG_TEST_DSN 版测试。
import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

func loadReqV2(t *testing.T) *ReqV2Dataset {
	t.Helper()
	data, err := LoadRequirementV2("testdata/requirement-v2")
	if err != nil {
		t.Fatalf("load dataset: %v", err)
	}
	return data
}

// TestReqV2CheckFrozenDataset 等价于 `-mode check`：fixture、manifest、
// split、gates 与 grader 金丝雀零网络全过。
func TestReqV2CheckFrozenDataset(t *testing.T) {
	data := loadReqV2(t)
	if data.Manifest.GraderVersion != ReqV2GraderVersion {
		t.Fatalf("grader version drift: %s", data.Manifest.GraderVersion)
	}
	for _, split := range []string{"development", "calibration", "holdout"} {
		if len(data.Manifest.SplitSessions[split]) == 0 {
			t.Fatalf("split %s empty", split)
		}
	}
	counts := data.CasesPerLayer(map[string]bool{"development": true, "calibration": true, "holdout": true})
	for _, layer := range ReqV2Layers {
		if counts[layer] == 0 {
			t.Fatalf("layer %s has no cases", layer)
		}
	}
	// 每个 veto 都有反例，每个判定族都有通过样例。
	vetoes := map[string]bool{}
	passSamples := 0
	for _, e := range data.Selftest {
		if e.Expect == "fail" {
			vetoes[e.Veto] = true
		} else {
			passSamples++
		}
	}
	for _, v := range data.Gates.Vetoes {
		if !vetoes[v.ID] {
			t.Fatalf("veto %s lacks a grader counterexample", v.ID)
		}
	}
	if passSamples == 0 {
		t.Fatal("no pass samples; grader could be fail-only")
	}
}

func TestReqV2GraderSelftest(t *testing.T) {
	data := loadReqV2(t)
	if err := GradeReqV2Selftest(data.Selftest); err != nil {
		t.Fatalf("selftest: %v", err)
	}
}

func TestReqV2ManifestTamper(t *testing.T) {
	data := loadReqV2(t)
	dir := t.TempDir()
	for _, name := range []string{"manifest.json", "provenance.json", "gates.json"} {
		raw, err := os.ReadFile(filepath.Join("testdata/requirement-v2", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range data.Manifest.Files {
		raw, err := os.ReadFile(filepath.Join("testdata/requirement-v2", filepath.FromSlash(f.Path)))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// 单字节改动必须让 manifest 校验失败。
	target := filepath.Join(dir, "reducer", "cases.json")
	raw, _ := os.ReadFile(target)
	raw = append(raw, ' ')
	if err := os.WriteFile(target, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRequirementV2(dir); err == nil || !strings.Contains(err.Error(), "SHA256") {
		t.Fatalf("expected SHA256 mismatch, got %v", err)
	}
}

func TestReqV2SplitLeak(t *testing.T) {
	data := loadReqV2(t)
	dir := t.TempDir()
	for _, name := range []string{"manifest.json", "provenance.json", "gates.json"} {
		raw, _ := os.ReadFile(filepath.Join("testdata/requirement-v2", name))
		_ = os.WriteFile(filepath.Join(dir, name), raw, 0600)
	}
	for _, f := range data.Manifest.Files {
		raw, _ := os.ReadFile(filepath.Join("testdata/requirement-v2", filepath.FromSlash(f.Path)))
		target := filepath.Join(dir, filepath.FromSlash(f.Path))
		_ = os.MkdirAll(filepath.Dir(target), 0755)
		_ = os.WriteFile(target, raw, 0600)
	}
	// 把一个 holdout session 同时列进 development：泄漏检查必须拒绝。
	manifestPath := filepath.Join(dir, "manifest.json")
	raw, _ := os.ReadFile(manifestPath)
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["split_sessions"].(map[string]any)["development"] = append(
		manifest["split_sessions"].(map[string]any)["development"].([]any), "rd-hold-1")
	patched, _ := json.Marshal(manifest)
	_ = os.WriteFile(manifestPath, patched, 0600)
	if _, err := LoadRequirementV2(dir); err == nil ||
		!(strings.Contains(err.Error(), "spans splits") || strings.Contains(err.Error(), "contradicts") || strings.Contains(err.Error(), "both")) {
		t.Fatalf("expected split leak rejection, got %v", err)
	}
}

// TestReqV2DeterministicBaseline 冻结 v2 确定性层形状：reducer/readiness 在
// development+calibration 必须 100% 通过且无 veto(评估设施回归即时暴露)。
// 2026-09-23 金标定向修正后恢复全量严格断言,不再保留任何单题豁免。
func TestReqV2DeterministicBaseline(t *testing.T) {
	data := loadReqV2(t)
	report, err := RunRequirementV2(context.Background(), data, ReqV2RunOptions{
		Splits: map[string]bool{"development": true, "calibration": true}, Repeats: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range report.Cases {
		if c.Layer != "reducer" && c.Layer != "readiness" {
			continue
		}
		key := c.Layer + "/" + c.ID
		seen[key] = true
		if len(c.Vetoes) > 0 {
			t.Errorf("%s veto: %v", key, c.Vetoes)
		}
		if !c.Pass {
			for _, a := range c.Assertions {
				if !a.Pass {
					t.Errorf("%s red: %s [%s] %s", key, a.Name, a.Classification, a.Detail)
				}
			}
		}
		for _, a := range c.Assertions {
			if !a.Pass && a.Classification == reqV2ClassFault {
				t.Errorf("%s technical fault: %s %s", key, a.Name, a.Detail)
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("no deterministic cases executed")
	}
}
func TestReqV2UpgradeStateIsMechanical(t *testing.T) {
	data := loadReqV2(t)
	for _, c := range data.Reducer {
		if len(c.InitialState) == 0 {
			continue
		}
		var before schemas.RequirementState
		if err := json.Unmarshal(c.InitialState, &before); err != nil {
			t.Fatal(err)
		}
		if _, err := schemas.DecodeRequirementState(c.InitialState); err == nil {
			t.Fatalf("%s: 产品 decoder 必须拒绝冻结 v1 外形", c.ID)
		}
		after := reqV2UpgradeState(before)
		if after.SchemaVersion != schemas.RequirementStateSchemaVersion {
			t.Fatalf("%s: 适配器应只升 schema_version", c.ID)
		}
		before.SchemaVersion = after.SchemaVersion
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("%s: 适配器改写了字段或生成了用户事实", c.ID)
		}
		break // 任一冻结外形足以证明机械性
	}
}

// TestReqV2DeterministicByteStable 同一状态重复求值字节级稳定(v2 契约)。
func TestReqV2DeterministicByteStable(t *testing.T) {
	data := loadReqV2(t)
	options := ReqV2RunOptions{Splits: map[string]bool{"development": true, "calibration": true}, Repeats: 1}
	first, err := RunRequirementV2(context.Background(), data, options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunRequirementV2(context.Background(), data, options)
	if err != nil {
		t.Fatal(err)
	}
	for i := range first.Cases {
		a, b := first.Cases[i], second.Cases[i]
		if a.Layer != b.Layer || a.ID != b.ID || a.Pass != b.Pass {
			t.Fatalf("repeated run unstable at %d: %+v vs %+v", i, a, b)
		}
	}
}
func TestReqV2ReplayDeterministic(t *testing.T) {
	data := loadReqV2(t)
	report, err := RunRequirementV2(context.Background(), data, ReqV2RunOptions{
		Splits: map[string]bool{"development": true, "calibration": true}, Repeats: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var lines [][]byte
	for _, c := range report.Cases {
		if c.Layer != "reducer" && c.Layer != "readiness" {
			continue
		}
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, raw)
	}
	outcome, err := ReplayRequirementV2(data, lines, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Changed) != 0 {
		t.Fatalf("replay changed verdicts: %v", outcome.Changed)
	}
	if len(outcome.Report.Cases) != len(lines) {
		t.Fatalf("replay case count %d != %d", len(outcome.Report.Cases), len(lines))
	}
}

func TestReqV2Compare(t *testing.T) {
	data := loadReqV2(t)
	line := func(layer, id string, repeat int, pass bool) []byte {
		raw, _ := json.Marshal(map[string]any{"layer": layer, "id": id, "repeat": repeat, "pass": pass})
		return raw
	}
	// 确定性层不进入配对；模型层按独立 case 折叠 repeat（a 有 2 个 repeat，
	// 第二次失败 → case 失败）。
	baseline := [][]byte{
		line("reducer", "r1", 1, true), line("readiness", "r2", 1, false),
		line("extraction", "a", 1, true), line("extraction", "a", 2, true),
		line("extraction", "b", 1, false), line("conversations", "c", 1, true),
	}
	candidate := [][]byte{
		line("reducer", "r1", 1, false), line("readiness", "r2", 1, false),
		line("extraction", "a", 1, true), line("extraction", "a", 2, false),
		line("extraction", "b", 1, false), line("conversations", "c", 1, true),
	}
	outcome, err := CompareRequirementV2(data.Gates, baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	// 模型层独立 case：a（repeat 折叠后 fail）、b、c。
	if outcome.ModelPairs != 3 || outcome.StillPass != 1 || outcome.StillFail != 1 || len(outcome.Regressed) != 1 || len(outcome.NewlyPassing) != 0 {
		t.Fatalf("unexpected model pairing: %+v", outcome)
	}
	// 确定性层单独报告且不改变配对数。
	if d := outcome.Deterministic["reducer"]; d.Total != 1 || d.BaselinePass != 1 || d.CandidatePass != 0 {
		t.Fatalf("deterministic delta wrong: %+v", d)
	}
	if outcome.SampleNote == "" {
		t.Fatal("below-minimum model sample must be declared as insufficient, not an improvement claim")
	}
}

func TestReqV2McNemarExact(t *testing.T) {
	// b=8, c=2：精确二项双侧 p ≈ 0.109。
	if p := mcnemarExact(8, 2); p < 0.08 || p > 0.14 {
		t.Fatalf("mcnemar(8,2)=%v out of range", p)
	}
	if p := mcnemarExact(0, 0); p != 1 {
		t.Fatalf("mcnemar(0,0)=%v", p)
	}
}

// TestReqV2PolicyUIBaseline（PG_TEST_DSN 门控）冻结当前 policy/ui 层基线。
// 2026-09-23 Spec 2 落地后：聊天不再启动 Builder（V4 消除），confirm 的
// Builder 输入与确认快照哈希一致（V5 保持 green）；剩余 red 逐项归属后续
// change——presentation_action/edit-while-building 与 V9(scope 外承诺经
// 模型 reply 透传)归 Screening 收集 v2；ui 的结构化 readiness 块与
// confirm payload 归确认快照/Builder gate v2。
func TestReqV2PolicyUIBaseline(t *testing.T) {
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("PG_TEST_DSN 未设置，跳过 requirement-v2 数据库层基线")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Skipf("PG_TEST_DSN 不可达: %v", err)
	}
	name := fmt.Sprintf("peval_reqv2test_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		_ = conn.Close(context.Background())
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	data := loadReqV2(t)
	report, err := RunRequirementV2(ctx, data, ReqV2RunOptions{
		DSN: u.String(), Splits: map[string]bool{"development": true, "calibration": true}, Repeats: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	expect := map[string]struct {
		pass   bool
		vetoes []string
	}{
		"policy/pol-ready-confirm":              {pass: true},
		"policy/pol-second-builder-running":     {pass: true},
		"policy/pol-stale-revision-edit":        {pass: true},
		"policy/pol-incomplete-chat-start":      {pass: false},
		"policy/pol-ready-chat-start":           {pass: false},
		"policy/pol-monitor-promise-guard":      {pass: false, vetoes: []string{"V9"}},
		"policy/pol-edit-while-running":         {pass: false},
		"policy/pol-edit-after-confirm-rebuild": {pass: false},
		"ui-contract/ui-collecting-fresh":       {pass: false},
		"ui-contract/ui-ready-unconfirmed":      {pass: false},
		"ui-contract/ui-incomplete-shown-ready": {pass: false},
	}
	seen := map[string]bool{}
	for _, c := range report.Cases {
		if c.Layer != "policy" && c.Layer != "ui-contract" {
			continue
		}
		key := c.Layer + "/" + c.ID
		seen[key] = true
		want, ok := expect[key]
		if !ok {
			t.Fatalf("unpinned %s; 冻结基线需要显式登记或移除 fixture", key)
		}
		if c.Pass != want.pass {
			t.Errorf("%s pass=%v want %v（基线漂移或金标被修改）", key, c.Pass, want.pass)
		}
		if len(c.Vetoes) != len(want.vetoes) {
			t.Errorf("%s vetoes=%v want %v", key, c.Vetoes, want.vetoes)
		} else {
			for i, v := range want.vetoes {
				if c.Vetoes[i] != v {
					t.Errorf("%s vetoes=%v want %v", key, c.Vetoes, want.vetoes)
				}
			}
		}
		for _, a := range c.Assertions {
			if !a.Pass && a.Classification == reqV2ClassFault {
				t.Errorf("%s technical fault: %s %s", key, a.Name, a.Detail)
			}
		}
	}
	for key := range expect {
		if !seen[key] {
			t.Errorf("pinned case %s missing", key)
		}
	}
}

// TestReqV2GatesMustBeFrozen 验证 check 拒绝 pending、缺字段与非法阈值。
func TestReqV2GatesMustBeFrozen(t *testing.T) {
	data := loadReqV2(t)
	copyDataset := func(mutate func(gates map[string]any)) string {
		dir := t.TempDir()
		for _, name := range []string{"manifest.json", "provenance.json", "gates.json"} {
			raw, _ := os.ReadFile(filepath.Join("testdata/requirement-v2", name))
			_ = os.WriteFile(filepath.Join(dir, name), raw, 0600)
		}
		for _, f := range data.Manifest.Files {
			raw, _ := os.ReadFile(filepath.Join("testdata/requirement-v2", filepath.FromSlash(f.Path)))
			target := filepath.Join(dir, filepath.FromSlash(f.Path))
			_ = os.MkdirAll(filepath.Dir(target), 0755)
			_ = os.WriteFile(target, raw, 0600)
		}
		raw, _ := os.ReadFile(filepath.Join(dir, "gates.json"))
		var gates map[string]any
		if err := json.Unmarshal(raw, &gates); err != nil {
			t.Fatal(err)
		}
		mutate(gates)
		patched, _ := json.Marshal(gates)
		_ = os.WriteFile(filepath.Join(dir, "gates.json"), patched, 0600)
		// manifest 只登记 case 文件；gates 哈希不进 manifest，可直接改。
		return dir
	}
	cases := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr string
	}{
		{"pending", func(g map[string]any) {
			g["model_quality_thresholds"].(map[string]any)["status"] = "pending_calibration"
		}, "frozen"},
		{"precision below recall", func(g map[string]any) {
			q := g["model_quality_thresholds"].(map[string]any)["extraction"].(map[string]any)
			q["operation_precision_min"] = 0.5
		}, "优先"},
		{"forbidden tolerance", func(g map[string]any) {
			q := g["model_quality_thresholds"].(map[string]any)["extraction"].(map[string]any)
			q["forbidden_op_total_max"] = 2
		}, "零容忍"},
		{"missing threshold", func(g map[string]any) {
			delete(g["model_quality_thresholds"].(map[string]any)["extraction"].(map[string]any), "operation_recall_min")
		}, "ratio"},
		{"missing min_signal_turns", func(g map[string]any) {
			delete(g["model_quality_thresholds"].(map[string]any)["extraction"].(map[string]any), "min_signal_turns")
		}, "min_signal_turns"},
		{"missing case_success_min", func(g map[string]any) {
			delete(g["model_quality_thresholds"].(map[string]any)["extraction"].(map[string]any), "case_success_min")
		}, "ratios in (0,1]"},
		{"illegal latency cap", func(g map[string]any) {
			g["model_quality_thresholds"].(map[string]any)["extraction"].(map[string]any)["latency_p95_max_ms"] = 0
		}, "必须为正"},
		{"illegal per-turn call cap", func(g map[string]any) {
			g["model_quality_thresholds"].(map[string]any)["extraction"].(map[string]any)["max_model_calls_per_turn"] = -1
		}, "必须为正"},
		{"repeated question nonzero", func(g map[string]any) {
			g["model_quality_thresholds"].(map[string]any)["conversations"].(map[string]any)["repeated_question_max_per_case"] = 1
		}, "零容忍"},
		{"key field tolerance nonzero", func(g map[string]any) {
			g["model_quality_thresholds"].(map[string]any)["key_field_wrong_write_total_max"] = 1
		}, "零容忍"},
		{"empty key fields", func(g map[string]any) {
			g["model_quality_thresholds"].(map[string]any)["key_fields"] = []string{}
		}, "清单不得为空"},
	}
	for _, tc := range cases {
		dir := copyDataset(tc.mutate)
		if _, err := LoadRequirementV2(dir); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: expected gates rejection containing %q, got %v", tc.name, tc.wantErr, err)
		}
	}
}

// TestReqV2PassKFold：repeat1=true、repeat2=false、repeat3=true 必须 AND 折叠为失败。
func TestReqV2PassKFold(t *testing.T) {
	report := &ReqV2Report{
		PerLayer:     map[string]ReqV2LayerSummary{},
		ModelQuality: map[string]ReqV2ModelQuality{"extraction": {}, "conversations": {}},
		Usage:        ReqV2Usage{ProviderErrors: map[string]int{}},
	}
	for repeat, pass := range map[int]bool{1: true, 2: false, 3: true} {
		report.Cases = append(report.Cases, ReqV2CaseResult{
			Layer: "extraction", ID: "case-a", Repeat: repeat, Pass: pass,
		})
	}
	finalizeReport(report)
	if s := report.PerLayer["extraction"]; s.Cases != 1 || s.Passed != 0 {
		t.Fatalf("pass^k fold wrong: %+v", s)
	}
	if q := report.ModelQuality["extraction"]; q.TaskTotal != 1 || q.TaskPassed != 0 {
		t.Fatalf("task success must use folded result: %+v", q)
	}
}

// TestReqV2GateVerdicts 逐项验证门槛失败与不可评估的 verdict 语义。
func TestReqV2GateVerdicts(t *testing.T) {
	gates := loadReqV2(t).Gates
	base := func() *ReqV2Report {
		r := &ReqV2Report{
			PerLayer:     map[string]ReqV2LayerSummary{},
			ModelQuality: map[string]ReqV2ModelQuality{"extraction": {}, "conversations": {}},
			Usage:        ReqV2Usage{ProviderErrors: map[string]int{}},
		}
		for _, layer := range ReqV2Layers {
			r.PerLayer[layer] = ReqV2LayerSummary{Failures: map[string]int{}}
		}
		return r
	}
	healthy := func(r *ReqV2Report) *ReqV2Report {
		// 一组全过指标的基线：20 例 signal 全对、precision/recall=1、无 veto，
		// 且新门槛（case 成功/最终状态/provider/延迟/单轮调用/预算）全部满足。
		ex := r.ModelQuality["extraction"]
		ex.Cases, ex.TaskTotal, ex.TaskPassed = 25, 25, 25
		ex.OpEmitted, ex.OpMatched, ex.OpExpected = 100, 100, 100
		ex.SignalTurns, ex.SignalMatches = 25, 25
		r.ModelQuality["extraction"] = ex
		cv := r.ModelQuality["conversations"]
		cv.Cases, cv.TaskTotal, cv.TaskPassed = 7, 7, 7
		cv.OpEmitted, cv.OpMatched, cv.OpExpected = 30, 30, 30
		r.ModelQuality["conversations"] = cv
		for _, layer := range ReqV2DeterministicLayers {
			s := r.PerLayer[layer]
			s.Cases, s.Passed = 5, 5
			r.PerLayer[layer] = s
		}
		r.MaxModelRequests = 200
		r.Usage.ModelCalls = 25
		r.Cases = append(r.Cases, ReqV2CaseResult{
			Layer: "extraction", ID: "healthy-state", Repeat: 1, Pass: true,
			Assertions: []ReqV2Assertion{{Name: "extraction:state:budget_cny", Pass: true}},
			Observation: ReqV2CaseObservation{Turns: []ReqV2TurnObservation{{
				ScreenModelCalled: true, ProviderRequests: 1, DurationMS: 800,
			}}},
		})
		return r
	}
	find := func(r *ReqV2Report, layer, metric string) *ReqV2GateVerdict {
		for i := range r.GateVerdicts {
			v := &r.GateVerdicts[i]
			if v.Layer == layer && v.Metric == metric {
				return v
			}
		}
		return nil
	}
	t.Run("all pass", func(t *testing.T) {
		r := healthy(base())
		evaluateQualityGates(r, gates)
		if r.GatePassed == nil || !*r.GatePassed {
			t.Fatalf("expected gate_passed=true, conclusion=%s, verdicts=%+v", r.Conclusion, r.GateVerdicts)
		}
	})
	t.Run("precision fail", func(t *testing.T) {
		r := healthy(base())
		ex := r.ModelQuality["extraction"]
		ex.OpEmitted, ex.OpMatched = 100, 90
		r.ModelQuality["extraction"] = ex
		evaluateQualityGates(r, gates)
		if v := find(r, "extraction", "operation_precision"); v == nil || v.Passed || !v.Evaluable {
			t.Fatalf("precision verdict wrong: %+v", v)
		}
		if r.GatePassed == nil || *r.GatePassed {
			t.Fatal("failed precision must make candidate unpublishable")
		}
	})
	t.Run("recall fail only", func(t *testing.T) {
		r := healthy(base())
		ex := r.ModelQuality["extraction"]
		ex.OpExpected, ex.OpMatched = 100, 70
		r.ModelQuality["extraction"] = ex
		evaluateQualityGates(r, gates)
		if v := find(r, "extraction", "operation_recall"); v == nil || v.Passed {
			t.Fatalf("recall verdict wrong: %+v", v)
		}
	})
	t.Run("insufficient signal turns unevaluable", func(t *testing.T) {
		r := healthy(base())
		ex := r.ModelQuality["extraction"]
		ex.SignalTurns, ex.SignalMatches = 3, 3
		r.ModelQuality["extraction"] = ex
		evaluateQualityGates(r, gates)
		v := find(r, "extraction", "min_signal_turns")
		if v == nil || v.Evaluable || v.Passed {
			t.Fatalf("signal sample gate must be unevaluable: %+v", v)
		}
		if r.GatePassed == nil || *r.GatePassed {
			t.Fatal("unevaluable gate must make candidate unpublishable")
		}
	})
	t.Run("insufficient min cases", func(t *testing.T) {
		r := healthy(base())
		ex := r.ModelQuality["extraction"]
		ex.Cases, ex.TaskTotal, ex.TaskPassed = 5, 5, 5
		r.ModelQuality["extraction"] = ex
		evaluateQualityGates(r, gates)
		if v := find(r, "extraction", "min_cases"); v == nil || v.Evaluable || v.Passed {
			t.Fatalf("min_cases must be unevaluable: %+v", v)
		}
	})
	t.Run("repeated question per case", func(t *testing.T) {
		r := healthy(base())
		r.Cases = append(r.Cases, ReqV2CaseResult{
			Layer: "conversations", ID: "cv-x", Repeat: 1,
			Assertions: []ReqV2Assertion{{Name: "conversations:repeated_question:budget_cny", Pass: false}},
		})
		evaluateQualityGates(r, gates)
		if v := find(r, "conversations", "repeated_question_max_per_case"); v == nil || v.Passed || v.Actual != "1" {
			t.Fatalf("repeated question verdict wrong: %+v", v)
		}
	})
	t.Run("forbidden op fail", func(t *testing.T) {
		r := healthy(base())
		ex := r.ModelQuality["extraction"]
		ex.ForbiddenOps = 1
		r.ModelQuality["extraction"] = ex
		evaluateQualityGates(r, gates)
		if v := find(r, "extraction", "forbidden_op_total"); v == nil || v.Passed {
			t.Fatalf("forbidden op verdict wrong: %+v", v)
		}
	})
	t.Run("key field wrong write fail", func(t *testing.T) {
		r := healthy(base())
		ex := r.ModelQuality["extraction"]
		ex.KeyFieldWrites = 2
		r.ModelQuality["extraction"] = ex
		evaluateQualityGates(r, gates)
		v := find(r, "extraction+conversations", "key_field_wrong_write_total")
		if v == nil || v.Passed || v.Actual != "2" {
			t.Fatalf("key field verdict wrong: %+v", v)
		}
	})
	t.Run("case success fail", func(t *testing.T) {
		r := healthy(base())
		ex := r.ModelQuality["extraction"]
		ex.TaskTotal, ex.TaskPassed = 25, 20
		r.ModelQuality["extraction"] = ex
		evaluateQualityGates(r, gates)
		if v := find(r, "extraction", "case_success"); v == nil || v.Passed {
			t.Fatalf("case success verdict wrong: %+v", v)
		}
		if r.GatePassed == nil || *r.GatePassed {
			t.Fatal("case success below 0.90 must fail the run")
		}
	})
	t.Run("final state fail", func(t *testing.T) {
		r := healthy(base())
		r.Cases = append(r.Cases, ReqV2CaseResult{
			Layer: "extraction", ID: "bad-state", Repeat: 1,
			Assertions: []ReqV2Assertion{{Name: "extraction:state:budget_cny", Pass: false}},
		})
		evaluateQualityGates(r, gates)
		if v := find(r, "extraction", "final_state_exact_match"); v == nil || v.Passed || v.Actual != "0.500 (1/2)" {
			t.Fatalf("final state verdict wrong: %+v", v)
		}
		if r.GatePassed == nil || *r.GatePassed {
			t.Fatal("final state below 0.90 must fail the run")
		}
	})
	t.Run("provider success fail", func(t *testing.T) {
		r := healthy(base())
		r.Cases = append(r.Cases, ReqV2CaseResult{
			Layer: "extraction", ID: "provider-fail", Repeat: 1,
			Observation: ReqV2CaseObservation{Turns: []ReqV2TurnObservation{{
				ScreenModelCalled: true, ProviderRequests: 1, ProviderError: "timeout",
			}}},
		})
		evaluateQualityGates(r, gates)
		if v := find(r, "model", "provider_success"); v == nil || v.Passed {
			t.Fatalf("provider success verdict wrong: %+v", v)
		}
		if r.GatePassed == nil || *r.GatePassed {
			t.Fatal("provider success below 0.98 must fail the run")
		}
	})
	t.Run("latency p95 fail", func(t *testing.T) {
		r := healthy(base())
		r.Cases = append(r.Cases, ReqV2CaseResult{
			Layer: "extraction", ID: "slow-turn", Repeat: 1,
			Observation: ReqV2CaseObservation{Turns: []ReqV2TurnObservation{{
				ScreenModelCalled: true, ProviderRequests: 1, DurationMS: 30000,
			}}},
		})
		evaluateQualityGates(r, gates)
		if v := find(r, "model", "latency_p95_ms"); v == nil || v.Passed {
			t.Fatalf("latency verdict wrong: %+v", v)
		}
		if r.GatePassed == nil || *r.GatePassed {
			t.Fatal("p95 above 10s must fail the run")
		}
	})
	t.Run("max calls per turn fail", func(t *testing.T) {
		r := healthy(base())
		r.Cases = append(r.Cases, ReqV2CaseResult{
			Layer: "extraction", ID: "chatty-turn", Repeat: 1,
			Observation: ReqV2CaseObservation{Turns: []ReqV2TurnObservation{{
				ScreenModelCalled: true, ProviderRequests: 3, DurationMS: 100,
			}}},
		})
		evaluateQualityGates(r, gates)
		if v := find(r, "model", "max_model_calls_per_turn"); v == nil || v.Passed || v.Actual != "3" {
			t.Fatalf("per-turn calls verdict wrong: %+v", v)
		}
		if r.GatePassed == nil || *r.GatePassed {
			t.Fatal("3 calls in one turn must fail the run")
		}
	})
	t.Run("budget exceeded", func(t *testing.T) {
		r := healthy(base())
		r.MaxModelRequests = 10
		r.Usage.ModelCalls = 25
		evaluateQualityGates(r, gates)
		if v := find(r, "model", "total_calls_within_budget"); v == nil || v.Passed {
			t.Fatalf("budget verdict wrong: %+v", v)
		}
		if r.GatePassed == nil || *r.GatePassed {
			t.Fatal("total calls above plan budget must fail the run")
		}
	})
	t.Run("budget missing unevaluable", func(t *testing.T) {
		r := healthy(base())
		r.MaxModelRequests = 0
		evaluateQualityGates(r, gates)
		if v := find(r, "model", "total_calls_within_budget"); v == nil || v.Evaluable || v.Passed {
			t.Fatalf("missing budget must be unevaluable: %+v", v)
		}
		if r.GatePassed == nil || *r.GatePassed {
			t.Fatal("unevaluable budget must fail the run")
		}
	})
	t.Run("conversation provider fail", func(t *testing.T) {
		r := healthy(base())
		r.Cases = append(r.Cases, ReqV2CaseResult{
			Layer: "conversations", ID: "cv-provider-fail", Repeat: 1,
			Observation: ReqV2CaseObservation{Turns: []ReqV2TurnObservation{{
				ScreenModelCalled: true, ProviderRequests: 1, ProviderError: "timeout",
			}}},
		})
		evaluateQualityGates(r, gates)
		if v := find(r, "model", "provider_success"); v == nil || v.Passed {
			t.Fatalf("conversation provider failure must fail the gate: %+v", v)
		}
		if r.GatePassed == nil || *r.GatePassed {
			t.Fatal("provider success must fail the run")
		}
	})
	t.Run("conversation per-turn over limit", func(t *testing.T) {
		r := healthy(base())
		r.Cases = append(r.Cases, ReqV2CaseResult{
			Layer: "conversations", ID: "cv-chatty", Repeat: 1,
			Observation: ReqV2CaseObservation{Turns: []ReqV2TurnObservation{{
				ScreenModelCalled: true, ProviderRequests: 3, DurationMS: 100,
			}}},
		})
		evaluateQualityGates(r, gates)
		if v := find(r, "model", "max_model_calls_per_turn"); v == nil || v.Passed || v.Actual != "3" {
			t.Fatalf("conversation per-turn over limit must fail: %+v", v)
		}
		if r.GatePassed == nil || *r.GatePassed {
			t.Fatal("per-turn over limit must fail the run")
		}
	})
	t.Run("conversation slow turn", func(t *testing.T) {
		r := healthy(base())
		r.Cases = append(r.Cases, ReqV2CaseResult{
			Layer: "conversations", ID: "cv-slow", Repeat: 1,
			Observation: ReqV2CaseObservation{Turns: []ReqV2TurnObservation{{
				ScreenModelCalled: true, ProviderRequests: 1, DurationMS: 25000,
			}}},
		})
		evaluateQualityGates(r, gates)
		if v := find(r, "model", "latency_p95_ms"); v == nil || v.Passed {
			t.Fatalf("conversation slow turn must fail latency gate: %+v", v)
		}
	})
	t.Run("gate p95 equals usage p95", func(t *testing.T) {
		r := healthy(base())
		r.Cases = append(r.Cases, ReqV2CaseResult{
			Layer: "conversations", ID: "cv-mid", Repeat: 1,
			Observation: ReqV2CaseObservation{Turns: []ReqV2TurnObservation{{
				ScreenModelCalled: true, ProviderRequests: 1, DurationMS: 5000,
			}}},
		})
		finalizeReport(r)
		evaluateQualityGates(r, gates)
		v := find(r, "model", "latency_p95_ms")
		if v == nil {
			t.Fatal("latency verdict missing")
		}
		want := fmt.Sprintf("%dms", r.Usage.LatencyP95MS)
		if v.Actual != want {
			t.Fatalf("gate p95 %s must equal usage.latency_p95 %s", v.Actual, want)
		}
	})
	t.Run("veto fail", func(t *testing.T) {
		r := healthy(base())
		s := r.PerLayer["policy"]
		s.Vetoes = 1
		r.PerLayer["policy"] = s
		evaluateQualityGates(r, gates)
		if v := find(r, "all", "veto_total"); v == nil || v.Passed {
			t.Fatalf("veto verdict wrong: %+v", v)
		}
		if r.GatePassed == nil || *r.GatePassed {
			t.Fatal("veto must make candidate unpublishable")
		}
	})
	t.Run("task success fail", func(t *testing.T) {
		r := healthy(base())
		cv := r.ModelQuality["conversations"]
		cv.TaskTotal, cv.TaskPassed = 7, 3
		r.ModelQuality["conversations"] = cv
		evaluateQualityGates(r, gates)
		if v := find(r, "conversations", "task_success"); v == nil || v.Passed {
			t.Fatalf("task success verdict wrong: %+v", v)
		}
	})
}

// TestReqV2KeyFieldWrongWrites：错值/额外写计入，完全漏记不计。
func TestReqV2KeyFieldWrongWrites(t *testing.T) {
	key := map[string]bool{"budget_cny": true, "owned_parts": true}
	wrongValue := schemas.RequirementOperation{Op: "set", Field: "budget_cny", Value: []byte("6000")}
	expected := []ReqV2OpGold{{Op: "set", Field: "budget_cny", Value: []byte("7000")}}
	if n := keyFieldWrongWrites([]schemas.RequirementOperation{wrongValue}, expected, key); n != 1 {
		t.Fatalf("wrong value on key field must count, got %d", n)
	}
	extra := schemas.RequirementOperation{Op: "set", Field: "owned_parts", Value: []byte("[]")}
	if n := keyFieldWrongWrites([]schemas.RequirementOperation{extra}, nil, key); n != 1 {
		t.Fatalf("extra key-field write must count, got %d", n)
	}
	if n := keyFieldWrongWrites(nil, expected, key); n != 0 {
		t.Fatalf("complete miss must not count as wrong write, got %d", n)
	}
	softField := schemas.RequirementOperation{Op: "set", Field: "noise_pref", Value: []byte(`"silent"`)}
	if n := keyFieldWrongWrites([]schemas.RequirementOperation{softField}, nil, key); n != 0 {
		t.Fatalf("non-key field writes are not key-field violations, got %d", n)
	}
}

// TestReqV2ReplayRestoresRepeats：replay 从冻结记录恢复 repeats，
// pass^3 折叠结果与逐 repeat 记录一致（1 过 2 挂 3 过 → 失败）。
func TestReqV2ReplayRestoresRepeats(t *testing.T) {
	data := loadReqV2(t)
	line := func(id string, repeat int, pass bool, obs string) []byte {
		raw, _ := json.Marshal(map[string]any{
			"layer": "readiness", "id": id, "repeat": repeat, "pass": pass, "split": "development", "session": "s",
			"observation": json.RawMessage(obs),
		})
		return raw
	}
	// 用真实金标 case rdy-fresh：obs 与金标一致时重判通过，不一致时失败。
	ok := `{"readiness":{"status":"incomplete","missing_fields":["use_case.type","budget_cny","existing_parts"],"blocking_conflicts":[],"confirmation_eligible":false}}`
	bad := `{"readiness":{"status":"ready","missing_fields":[],"blocking_conflicts":[],"confirmation_eligible":true}}`
	lines := [][]byte{
		line("rdy-fresh", 1, true, ok),
		line("rdy-fresh", 2, false, bad),
		line("rdy-fresh", 3, true, ok),
	}
	outcome, err := ReplayRequirementV2(data, lines, 0)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Report.Repeats != 3 {
		t.Fatalf("repeats must be restored from records, got %d", outcome.Report.Repeats)
	}
	if len(outcome.Changed) != 0 {
		t.Fatalf("re-grading must match stored verdicts, changed=%v", outcome.Changed)
	}
	if s := outcome.Report.PerLayer["readiness"]; s.Cases != 1 || s.Passed != 0 {
		t.Fatalf("pass^3 fold mismatch: %+v", s)
	}
	// 逐 repeat 记录保留各自结论（1、3 过，2 挂）；折叠结论在 PerLayer。
	repeatResults := map[int]bool{}
	for _, c := range outcome.Report.Cases {
		if c.ID == "rdy-fresh" {
			repeatResults[c.Repeat] = c.Pass
		}
	}
	if len(repeatResults) != 3 || !repeatResults[1] || repeatResults[2] || !repeatResults[3] {
		t.Fatalf("per-repeat verdicts must match records: %+v", repeatResults)
	}
}
