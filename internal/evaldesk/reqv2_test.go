package evaldesk

// Requirement v2 工作台数据合同的合成产物测试与真实产物抽查。
// 合成夹具只服务 evaldesk 展示层;不改 planningeval 判卷、金标或 gates。
import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type reqV2Fixture struct {
	Root string
	Dir  string // 运行目录绝对路径
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// writeRun 写出一个 v2 运行的最小一致产物;mutate 在写哈希前调整 JSON 内容。
func writeRun(t *testing.T, root, name string, mutate func(plan, report, manifest, gates map[string]any)) reqV2Fixture {
	t.Helper()
	dir := filepath.Join(root, "artifacts", "reqv2", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gates := map[string]any{"version": "gates-test-v1", "frozen_at": "2026-09-24", "vetoes": []any{},
		"deterministic_layers":   map[string]any{"names": []string{"reducer", "readiness", "policy", "ui-contract"}, "required_pass_rate": 1.0},
		"repetition":             map[string]any{"release_repeats": 3, "metric": "pass_all_k", "best_of_k_as_gate": false},
		"minimum_paired_samples": 2,
		"model_quality_thresholds": map[string]any{"status": "frozen", "frozen_at": "2026-09-24",
			"key_fields": []string{"budget_cny"}, "key_field_wrong_write_total_max": 0,
			"extraction":    map[string]any{"operation_precision_min": 0.9, "operation_recall_min": 0.9, "forbidden_op_total_max": 0, "turn_signal_exact_match_min": 0.8, "min_signal_turns": 5, "case_success_min": 0.8, "final_state_exact_match_min": 0.8, "provider_success_min": 0.98, "latency_p95_max_ms": 8000, "max_model_calls_per_turn": 4, "min_cases": 20},
			"conversations": map[string]any{"task_success_min": 0.8, "repeated_question_max_per_case": 0, "forbidden_veto_total_max": 0, "min_cases": 20}},
		"limitations": []string{}}
	manifest := map[string]any{"dataset": "requirement-v2", "grader_version": "reqv2-grader-test", "frozen_at": "2026-09-24",
		"files": []any{
			map[string]any{"path": "reducer/cases.json", "sha256": "aa", "cases": 1, "sessions": 1},
		},
		"split_sessions": map[string]any{"development": []string{"rd-dev-1"}}}
	report := map[string]any{"schema_version": 1, "mode": "live", "grader_version": "reqv2-grader-test",
		"splits": []string{"development"}, "repeats": 3, "gate_passed": false,
		"conclusion": "测试结论", "per_layer": map[string]any{}, "model_quality": map[string]any{},
		"gate_verdicts": []any{
			map[string]any{"layer": "reducer", "metric": "deterministic_pass", "actual": "1/1", "threshold": "100%", "passed": true, "evaluable": true},
			map[string]any{"layer": "extraction", "metric": "min_cases", "actual": "0", "threshold": "≥20", "passed": false, "evaluable": false, "note": "样本不足：门槛不可评估，按不可发布处理"},
		},
		"cases": []any{
			map[string]any{"layer": "reducer", "id": "rd-1", "split": "development", "session": "rd-dev-1", "repeat": 1, "pass": true,
				"assertions":  []any{map[string]any{"name": "reducer:no_error", "pass": true}},
				"observation": map[string]any{"turns": []any{map[string]any{"reply": "好的", "operations": []any{map[string]any{"op": "set", "field": "budget_cny"}}, "screen_model_called": false}}},
			},
		},
		"usage":              map[string]any{"model_calls": 2, "provider_errors": map[string]any{}, "tokens_known": 100, "tokens_all_known": true, "latency_p50_ms": 10, "latency_p95_ms": 20},
		"max_model_requests": 400, "limitations": []string{}, "duration_ms": 5}
	plan := map[string]any{"mode": "live", "created_at": "2026-09-24T00:00:00Z", "grader_version": "reqv2-grader-test",
		"splits": []string{"development"}, "repeats": 3, "code_commit": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "code_dirty": false,
		"max_model_requests": 400,
		"models":             map[string]any{"screening": map[string]any{"model": "test-model", "provider": "test", "base_host": "http://127.0.0.1:1", "api_key": "secret"}}}
	if mutate != nil {
		mutate(plan, report, manifest, gates)
	}
	write := func(name string, value any) {
		raw, _ := json.Marshal(value)
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("manifest.json", manifest)
	write("gates.json", gates)
	report["manifest_sha256"] = sha256File(t, filepath.Join(dir, "manifest.json"))
	report["gates_sha256"] = sha256File(t, filepath.Join(dir, "gates.json"))
	plan["manifest_sha256"] = report["manifest_sha256"]
	plan["gates_sha256"] = report["gates_sha256"]
	// results.jsonl 与 report.cases 保持一致。
	var results []map[string]any
	for _, c := range report["cases"].([]any) {
		row := c.(map[string]any)
		results = append(results, map[string]any{"layer": row["layer"], "id": row["id"], "split": row["split"], "session": row["session"], "repeat": row["repeat"], "pass": row["pass"]})
	}
	var jsonl strings.Builder
	for _, row := range results {
		raw, _ := json.Marshal(row)
		jsonl.Write(raw)
		jsonl.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "results.jsonl"), []byte(jsonl.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	write("plan.json", plan)
	write("report.json", report)
	return reqV2Fixture{Root: root, Dir: dir}
}

func newReqV2Store(t *testing.T, root string) *Store {
	t.Helper()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestReqV2DatasetIncludesUntestedCasesAndRejectsTampering(t *testing.T) {
	root := t.TempDir()
	fixture := writeRun(t, root, "dataset", func(plan, report, manifest, gates map[string]any) {
		path := filepath.Join(root, "artifacts", "reqv2", "dataset", "reducer", "cases.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		data := []byte(`[{"id":"rd-1","title":"开发题","split":"development","expected":{"revision_delta":1}},{"id":"rd-holdout","title":"未测试的保留题","split":"holdout","expected":{"revision_delta":0}}]`)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		manifest["files"] = []any{map[string]any{"path": "reducer/cases.json", "sha256": sha256File(t, path), "cases": 2, "sessions": 2}}
	})
	store := newReqV2Store(t, root)
	id := reqV2RunID("artifacts/reqv2/dataset")
	response, err := store.DatasetReqV2(id)
	if err != nil || len(response.Cases) != 2 || len(response.Notes) != 0 {
		t.Fatalf("完整题库应包含未测试题目：%+v %v", response, err)
	}
	if response.Cases[1].ID != "rd-holdout" || response.Cases[0].ContentSHA == response.Cases[1].ContentSHA {
		t.Fatalf("逐题身份或内容指纹无效：%+v", response.Cases)
	}
	caseDetail, err := store.CaseReqV2(id, "reducer", "rd-1")
	if err != nil || caseDetail.Frozen.ContentSHA != response.Cases[0].ContentSHA {
		t.Fatal("逐题证据与题库应复用相同投影")
	}
	request := httptest.NewRequest("GET", "http://127.0.0.1/api/evaldesk/requirement-v2/dataset?id="+id, nil)
	recorder := httptest.NewRecorder()
	Handler(store).ServeHTTP(recorder, request)
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), "rd-holdout") {
		t.Fatalf("完整题库接口失败：%s", recorder.Body.String())
	}
	if err := os.WriteFile(filepath.Join(fixture.Dir, "reducer", "cases.json"), []byte(`[{"id":"changed"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	response, err = store.DatasetReqV2(id)
	if err != nil || len(response.Cases) != 0 || len(response.Notes) != 1 {
		t.Fatalf("篡改题目不能展示：%+v %v", response, err)
	}
	if err := os.WriteFile(filepath.Join(fixture.Dir, "manifest.json"), []byte(`{"files":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	response, err = store.DatasetReqV2(id)
	if err != nil || len(response.Cases) != 0 || len(response.Notes) != 1 {
		t.Fatalf("篡改清单不能展示：%+v %v", response, err)
	}
	if _, note := store.reqV2FrozenCases(fixture.Dir, "../../other"); note == "" {
		t.Fatal("不应读取非白名单分类")
	}
}

func findRun(runs []ReqV2RunSummary, name string) *ReqV2RunSummary {
	for i := range runs {
		if runs[i].DirName == name {
			return &runs[i]
		}
	}
	return nil
}

func TestReqV2DiscoveryAndIntegrity(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "live-ok", nil)
	writeRun(t, root, "deterministic-zero-model", func(plan, report, manifest, gates map[string]any) {
		plan["mode"] = "deterministic"
		plan["zero_model"] = true
		report["mode"] = "deterministic"
	})
	writeRun(t, root, "replay-run", func(plan, report, _ map[string]any, __ map[string]any) {
		plan["mode"] = "replay"
		plan["zero_model"] = true
		plan["source_run"] = "artifacts/reqv2/other-run"
		report["mode"] = "replay"
	})
	writeRun(t, root, "superseded-x-regrade", func(plan, report, _ map[string]any, __ map[string]any) {
		plan["mode"] = "deterministic"
		plan["regrade"] = true
		plan["zero_model"] = true
		plan["source_run"] = "artifacts/reqv2/older"
		plan["source_grader_version"] = "reqv2-grader-v2"
		report["mode"] = "regrade"
	})
	// 缺 plan.json 的目录不冒充运行。
	if err := os.MkdirAll(filepath.Join(root, "artifacts", "reqv2", "orphan"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "artifacts", "reqv2", "orphan", "report.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 损坏的 report.json。
	brokenFixture := writeRun(t, root, "broken-report", nil)
	if err := os.WriteFile(filepath.Join(brokenFixture.Dir, "report.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	// manifest 实际内容与冻结哈希不一致。
	tamperedFixture := writeRun(t, root, "manifest-mismatch", nil)
	if err := os.WriteFile(filepath.Join(tamperedFixture.Dir, "manifest.json"), []byte(`{"dataset":"requirement-v2","tampered":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// results 缺失记录 → incomplete。
	partialFixture := writeRun(t, root, "partial-results", nil)
	if err := os.WriteFile(filepath.Join(partialFixture.Dir, "results.jsonl"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	// results 重复记录 → invalid。
	duplicateFixture := writeRun(t, root, "duplicate-results", nil)
	lines, _ := os.ReadFile(filepath.Join(duplicateFixture.Dir, "results.jsonl"))
	if err := os.WriteFile(filepath.Join(duplicateFixture.Dir, "results.jsonl"), append(lines, lines...), 0o644); err != nil {
		t.Fatal(err)
	}
	// 计划/报告判卷版本不一致 → invalid。
	writeRun(t, root, "identity-mismatch", func(plan, _, _, _ map[string]any) {
		plan["grader_version"] = "reqv2-grader-other"
	})

	store := newReqV2Store(t, root)
	response := store.RunsReqV2()
	if len(response.Runs) != 9 {
		t.Fatalf("应发现 9 次运行,实际 %d;warnings=%v", len(response.Runs), response.Warnings)
	}
	if len(response.Warnings) == 0 {
		t.Fatal("orphan 目录应产生跳过提示")
	}

	live := findRun(response.Runs, "live-ok")
	if live == nil {
		t.Fatal("live-ok 未被发现")
	}
	if live.Evidence.Status != "complete" || live.Mode != "live" {
		t.Fatalf("live-ok 证据状态=%s 模式=%s", live.Evidence.Status, live.Mode)
	}
	if live.Evidence.Notes == nil {
		t.Fatal("完整证据无说明时 notes 必须是空数组")
	}
	if len(live.Models) != 1 || live.Models[0].Model != "test-model" {
		t.Fatalf("模型身份投影错误: %+v", live.Models)
	}
	if strings.Contains(fmt.Sprint(live.Models), "secret") || strings.Contains(fmt.Sprint(live.Models), "base_host") {
		t.Fatal("模型投影泄漏 base_host/api_key 等敏感字段")
	}
	if live.Superseded {
		t.Fatal("普通运行不应标记 superseded")
	}

	zero := findRun(response.Runs, "deterministic-zero-model")
	if zero == nil || !zero.ZeroModel || zero.Mode != "deterministic" {
		t.Fatalf("零模型运行身份错误: %+v", zero)
	}

	replay := findRun(response.Runs, "replay-run")
	if replay == nil || replay.Mode != "replay" || replay.SourceRun == "" {
		t.Fatalf("replay 身份错误: %+v", replay)
	}

	regrade := findRun(response.Runs, "superseded-x-regrade")
	if regrade == nil || !regrade.Superseded || !regrade.Regrade || regrade.SourceRun == "" {
		t.Fatalf("regrade/superseded 身份错误: %+v", regrade)
	}
	if !strings.Contains(strings.Join(regrade.Evidence.Notes, "\n"), "零模型重判旧观测") {
		t.Fatal("regrade 必须标明零模型重判,不得显示成新 live 结果")
	}

	broken := findRun(response.Runs, "broken-report")
	if broken == nil || broken.Evidence.Status != "invalid" {
		t.Fatalf("损坏报告应标记 invalid: %+v", broken)
	}
	mismatch := findRun(response.Runs, "manifest-mismatch")
	if mismatch == nil || mismatch.Evidence.Status != "invalid" {
		t.Fatalf("manifest 哈希不一致应标记 invalid: %+v", mismatch)
	}
	partialRun := findRun(response.Runs, "partial-results")
	if partialRun == nil || partialRun.Evidence.Status != "incomplete" {
		t.Fatalf("缺 results 记录应标记 incomplete: %+v", partialRun)
	}
	duplicateRun := findRun(response.Runs, "duplicate-results")
	if duplicateRun == nil || duplicateRun.Evidence.Status != "invalid" {
		t.Fatalf("重复记录应标记 invalid: %+v", duplicateRun)
	}
	identityRun := findRun(response.Runs, "identity-mismatch")
	if identityRun == nil || identityRun.Evidence.Status != "invalid" {
		t.Fatalf("计划/报告身份不一致应标记 invalid: %+v", identityRun)
	}

	// lookup 防路径穿越。
	if _, err := store.reqV2Lookup("../../etc"); err == nil {
		t.Fatal("路径穿越 id 应被拒绝")
	}
	if _, err := store.reqV2Lookup("short"); err == nil {
		t.Fatal("长度不符 id 应被拒绝")
	}
}

func TestReqV2EmptyCollectionsAreJSONCollections(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "zero-model", func(plan, report, _, _ map[string]any) {
		delete(plan, "models")
		delete(report, "splits")
	})
	broken := writeRun(t, root, "broken-report", func(plan, _, _, _ map[string]any) {
		delete(plan, "models")
	})
	if err := os.WriteFile(filepath.Join(broken.Dir, "report.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := newReqV2Store(t, root)
	for _, run := range store.RunsReqV2().Runs {
		raw, err := json.Marshal(run)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"models":[]`) || !strings.Contains(string(raw), `"splits":[]`) {
			t.Fatalf("%s summary 集合不应为 null: %s", run.DirName, raw)
		}
		if run.DirName != "broken-report" {
			continue
		}
		detail, err := store.reqV2Lookup(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		raw, err = json.Marshal(detail.detail)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{`"gate_verdicts":[]`, `"limitations":[]`, `"cases":[]`, `"per_layer":{}`, `"model_quality":{}`} {
			if !strings.Contains(string(raw), field) {
				t.Fatalf("损坏报告的 %s 不应为 null: %s", field, raw)
			}
		}
	}
}

func TestReqV2IdentityAndLayerTotalsUseSetAndPassK(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "reordered-splits", func(plan, report, _, _ map[string]any) {
		plan["splits"] = []string{"development", "calibration"}
		report["splits"] = []string{"calibration", "development"}
		cases := report["cases"].([]any)
		for repeat, pass := range map[int]bool{2: false, 3: true} {
			row := map[string]any{}
			for key, value := range cases[0].(map[string]any) {
				row[key] = value
			}
			row["repeat"], row["pass"] = repeat, pass
			cases = append(cases, row)
		}
		cases = append(cases, map[string]any{"layer": "extraction", "id": "ex-1", "split": "development", "repeat": 1, "pass": false, "observation": map[string]any{"skipped": "zero model"}})
		report["cases"] = cases
		report["per_layer"] = map[string]any{"reducer": map[string]any{"cases": 1, "passed": 0}, "extraction": map[string]any{"cases": 0, "passed": 0, "skipped": 1}}
	})
	store := newReqV2Store(t, root)
	run := store.RunsReqV2().Runs[0]
	if run.Evidence.Status != "complete" {
		t.Fatalf("顺序不同的 splits 与一致的 Pass^k 汇总应保留完整证据: %+v", run.Evidence)
	}
	detail, err := store.reqV2Lookup(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range detail.detail.CaseIndex {
		if entry.ID == "ex-1" && !entry.Skipped {
			t.Fatal("observation.skipped 应标记为跳过")
		}
	}
}

func TestReqV2RunDetailAndCase(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "live-ok", func(_, report, _, _ map[string]any) {
		report["usage"] = map[string]any{"model_calls": 129, "provider_errors": map[string]any{}, "tokens_known": 512821, "tokens_all_known": true, "latency_p50_ms": 3555, "latency_p95_ms": 5023}
	})
	store := newReqV2Store(t, root)
	runs := store.RunsReqV2()
	run := runs.Runs[0]
	detail, err := store.reqV2Lookup(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.detail.GateVerdicts) != 2 {
		t.Fatalf("gate verdicts 投影数量错误: %d", len(detail.detail.GateVerdicts))
	}
	if len(run.FailedGates) != 1 || run.FailedGates[0].Metric != "min_cases" || run.FailedGates[0].Evaluable {
		t.Fatalf("摘要应保留未通过项的原始值与不可评估状态: %+v", run.FailedGates)
	}
	if detail.detail.Usage == nil {
		t.Fatal("usage 未投影")
	}
	if len(detail.detail.CaseIndex) != 1 || detail.detail.CaseIndex[0].ID != "rd-1" {
		t.Fatalf("case index 错误: %+v", detail.detail.CaseIndex)
	}
	if detail.detail.CaseIndex[0].PassK != true {
		t.Fatal("pass^k 应为 true")
	}

	caseDetail, err := store.CaseReqV2(run.ID, "reducer", "rd-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(caseDetail.Repeats) != 1 || !caseDetail.Repeats[0].Pass {
		t.Fatalf("逐题 repeat 投影错误: %+v", caseDetail.Repeats)
	}
	if !caseDetail.PassK {
		t.Fatal("所有 repeat 通过的单题应投影为 Pass^k 通过")
	}
	if len(caseDetail.Repeats[0].Turns) != 1 || caseDetail.Repeats[0].Turns[0].Reply != "好的" {
		t.Fatalf("turn 观测投影错误: %+v", caseDetail.Repeats[0].Turns)
	}
	// 不存在的题目返回 not found。
	if _, err = store.CaseReqV2(run.ID, "reducer", "missing"); err == nil {
		t.Fatal("缺失题目应返回 not found")
	}
}

func TestReqV2CompareStrictAndIncomparable(t *testing.T) {
	root := t.TempDir()
	// 同身份两次运行:仅候选把 reducer 题目改成失败。
	writeRun(t, root, "pair-base", nil)
	writeRun(t, root, "pair-cand", func(_, report, _, _ map[string]any) {
		cases := report["cases"].([]any)
		first := cases[0].(map[string]any)
		first["pass"] = false
	})
	store := newReqV2Store(t, root)
	a, b := reqV2RunID("artifacts/reqv2/pair-base"), reqV2RunID("artifacts/reqv2/pair-cand")
	response, err := store.CompareReqV2(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if !response.Strict {
		t.Fatalf("同身份对比应为严格: reasons=%v", response.Reasons)
	}
	if response.Outcome == nil {
		t.Fatal("严格对比应产出配对口径结果")
	}
	// live 对零模型:不可严格比较。
	writeRun(t, root, "zero-other", func(plan, report, _, _ map[string]any) {
		plan["mode"] = "deterministic"
		plan["zero_model"] = true
		report["mode"] = "deterministic"
	})
	zero := reqV2RunID("artifacts/reqv2/zero-other")
	response, err = store.CompareReqV2(a, zero)
	if err != nil {
		t.Fatal(err)
	}
	if response.Strict {
		t.Fatal("live 对零模型不应严格比较")
	}
	foundMode := false
	for _, reason := range response.Reasons {
		foundMode = foundMode || strings.Contains(reason, "运行类型不同")
	}
	if !foundMode {
		t.Fatalf("应给出运行类型不同的原因: %v", response.Reasons)
	}
}

func TestReqV2EmptyAndLegacyRegression(t *testing.T) {
	root := t.TempDir()
	store := newReqV2Store(t, root)
	response := store.RunsReqV2()
	if len(response.Runs) != 0 || len(response.Warnings) != 1 {
		t.Fatalf("空 reqv2 目录应返回空运行与提示: %+v", response)
	}
}

// 真实本机产物抽查:产物缺失时明确 skip,不生成替代样本。
func TestReqV2RealArtifactSpotChecks(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	candidates := []string{filepath.Join(repoRoot, "artifacts", "reqv2", "spec3-final-live-20260923")}
	found := false
	for _, dir := range candidates {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			found = true
		}
	}
	if !found {
		t.Skip("本机无 spec3-final-live 产物;真实产物抽查跳过")
	}
	store := newReqV2Store(t, repoRoot)
	runID := reqV2RunID("artifacts/reqv2/spec3-final-live-20260923")
	run, err := store.reqV2Lookup(runID)
	if err != nil {
		t.Fatal(err)
	}
	detail := run.detail
	if len(detail.GateVerdicts) != 16 {
		t.Fatalf("spec3-final-live 应有 16 条 gate verdict,实际 %d", len(detail.GateVerdicts))
	}
	if len(detail.PerLayer) != 6 {
		t.Fatalf("spec3-final-live 应有六层,实际 %d", len(detail.PerLayer))
	}
	var usage struct {
		ModelCalls int  `json:"model_calls"`
		TokensAll  bool `json:"tokens_all_known"`
	}
	if err := json.Unmarshal(detail.Usage, &usage); err != nil {
		t.Fatal(err)
	}
	if usage.ModelCalls != 129 {
		t.Fatalf("spec3-final-live 模型调用应为 129,实际 %d", usage.ModelCalls)
	}
	if detail.GatePassed == nil || *detail.GatePassed {
		t.Fatalf("spec3-final-live gate_passed 应为 false(确定性层红项),不得误写为发布通过")
	}
	extraction := detail.PerLayer["extraction"]
	if extraction.Cases != 26 || extraction.Passed != 25 {
		t.Fatalf("spec3-final-live extraction 层汇总与冻结报告不符: %+v", extraction)
	}

	// 零模型 deterministic 抽查:模型门槛 UNEVALUABLE、skip 与 no-model 身份正确。
	zeroDir := filepath.Join(repoRoot, "artifacts", "reqv2", "spec4-zero-model-final-175620")
	if st, err := os.Stat(zeroDir); err != nil || !st.IsDir() {
		t.Log("本机无 spec4-zero-model-final 产物;跳过零模型抽查")
		return
	}
	zeroRun, err := store.reqV2Lookup(reqV2RunID("artifacts/reqv2/spec4-zero-model-final-175620"))
	if err != nil {
		t.Fatal(err)
	}
	unevaluable := 0
	for _, verdict := range zeroRun.detail.GateVerdicts {
		if !verdict.Evaluable {
			unevaluable++
		}
	}
	if unevaluable == 0 {
		t.Fatal("零模型运行应存在 UNEVALUABLE 门槛")
	}
	if !zeroRun.summary.ZeroModel {
		t.Fatal("零模型身份未投影")
	}
	skipped := zeroRun.detail.PerLayer["extraction"]
	if skipped.Skipped == 0 {
		t.Fatalf("零模型 extraction 层应记录 skipped: %+v", skipped)
	}

	// regrade 抽查:保留源运行指针且标明零模型重判。
	regradeDir := filepath.Join(repoRoot, "artifacts", "reqv2", "superseded-v2-spec2-rework-deterministic-20260923-regrade-reqv2-grader-v3-20260923T034834Z")
	if st, err := os.Stat(regradeDir); err != nil || !st.IsDir() {
		t.Log("本机无 regrade 产物;跳过 regrade 抽查")
		return
	}
	regrade, err := store.reqV2Lookup(reqV2RunID("artifacts/reqv2/superseded-v2-spec2-rework-deterministic-20260923-regrade-reqv2-grader-v3-20260923T034834Z"))
	if err != nil {
		t.Fatal(err)
	}
	if !regrade.summary.Regrade || !regrade.summary.ZeroModel || regrade.summary.SourceRun == "" {
		t.Fatalf("regrade 身份不完整: %+v", regrade.summary)
	}
	if !strings.Contains(strings.Join(regrade.summary.Evidence.Notes, " "), "零模型重判旧观测") {
		t.Fatal("regrade 未标明零模型重判")
	}
}

func TestReqV2ScoreNoteManifestAndPromptProjection(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "scored-live", func(plan, report, manifest, _ map[string]any) {
		cases := []any{}
		perLayer := map[string]any{}
		for _, layer := range []string{"reducer", "readiness", "policy", "ui-contract", "extraction", "conversations"} {
			fails := 0
			if layer == "ui-contract" || layer == "extraction" {
				fails = 1
			}
			for i := 1; i <= fails+1; i++ {
				pass := i == 1 || fails == 0
				cases = append(cases, map[string]any{"layer": layer, "id": layer + "-1" + string(rune('0'+i)), "split": "development", "session": layer + "-s1", "repeat": 1, "pass": pass,
					"assertions": []any{map[string]any{"name": layer + ":no_error", "pass": pass}}})
			}
			perLayer[layer] = map[string]any{"cases": fails + 1, "passed": fails + 1 - fails, "skipped": 0, "vetoes": 0, "failure_classifications": map[string]any{}}
		}
		report["cases"] = cases
		report["per_layer"] = perLayer
		plan["note"] = "prompt SHA256 per request is recorded in events.jsonl model_request events"
		plan["models"] = map[string]any{"screening": map[string]any{"model": "test-model", "provider": "test", "base_host": "http://127.0.0.1:1", "api_key": "secret", "reasoning_effort": "low", "timeout": "1m0s", "session_cache": true}}
		manifest["files"] = []any{
			map[string]any{"path": "reducer/cases.json", "sha256": "aa11", "cases": 2, "sessions": 2},
			map[string]any{"path": "catalog.json", "sha256": "cc33", "cases": 0, "sessions": 0},
		}
		manifest["split_sessions"] = map[string]any{"development": []string{"reducer-s1", "extraction-s1"}, "holdout": []string{"reducer-h1"}}
	})
	store := newReqV2Store(t, root)
	response := store.RunsReqV2()
	run := findRun(response.Runs, "scored-live")
	if run == nil {
		t.Fatal("scored-live 未被发现")
	}
	// 初筛侧 reducer1+readiness1+extraction1+conversations1 = 4/5,选配侧 policy1+ui-contract1 = 2/3。
	if run.ScoreNote != "初筛 4/5 · 选配 2/3" {
		t.Fatalf("score_note 汇总错误: %q", run.ScoreNote)
	}
	if run.LayerScores["extraction"].Cases != 2 || run.LayerScores["policy"].Cases != 1 {
		t.Fatalf("目录须提供分层冻结结果，不能从 score_note 反解析: %+v", run.LayerScores)
	}
	if run.PlanNote == "" {
		t.Fatal("plan_note 未投影")
	}
	if len(run.Models) != 1 || run.Models[0].ReasoningEffort != "low" || run.Models[0].Timeout != "1m0s" || run.Models[0].SessionCache == nil || !*run.Models[0].SessionCache {
		t.Fatalf("模型脱敏身份投影错误: %+v", run.Models)
	}
	if strings.Contains(fmt.Sprint(run.Models), "secret") || strings.Contains(fmt.Sprint(run.Models), "base_host") {
		t.Fatal("模型身份投影泄漏敏感字段")
	}
	loaded, err := store.reqV2Lookup(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	detail := loaded.detail
	if detail.Manifest == nil || detail.Manifest.Dataset != "requirement-v2" || len(detail.Manifest.Files) != 2 {
		t.Fatalf("manifest 投影错误: %+v", detail.Manifest)
	}
	if detail.Manifest.Files[1].Layer != "catalog.json" || detail.Manifest.Files[0].SHA256 != "aa11" {
		t.Fatalf("manifest 文件投影错误: %+v", detail.Manifest.Files)
	}
	splits := map[string]ReqV2SplitRow{}
	for _, split := range detail.Manifest.Splits {
		splits[split.Split] = split
	}
	if splits["development"].Sessions != 2 || !splits["development"].Used || splits["holdout"].Used || splits["holdout"].Sessions != 1 {
		t.Fatalf("split 分布投影错误: %+v", detail.Manifest.Splits)
	}
}

func TestReqV2ZeroModelScoreNoteSkipsModelLayers(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "zero-scored", func(plan, report, _, _ map[string]any) {
		plan["mode"] = "deterministic"
		plan["zero_model"] = true
		report["mode"] = "deterministic"
		report["cases"] = []any{map[string]any{"layer": "reducer", "id": "rd-1", "split": "development", "session": "rd-s1", "repeat": 1, "pass": true,
			"assertions": []any{map[string]any{"name": "reducer:no_error", "pass": true}}}}
		report["per_layer"] = map[string]any{
			"reducer":    map[string]any{"cases": 1, "passed": 1, "skipped": 0, "vetoes": 0, "failure_classifications": map[string]any{}},
			"extraction": map[string]any{"cases": 0, "passed": 0, "skipped": 2, "vetoes": 0, "failure_classifications": map[string]any{}},
		}
	})
	store := newReqV2Store(t, root)
	run := findRun(store.RunsReqV2().Runs, "zero-scored")
	if run == nil {
		t.Fatal("zero-scored 未被发现")
	}
	if run.ScoreNote != "初筛 1/1 · 选配 0/0 · 模型层跳过" {
		t.Fatalf("零模型 score_note 错误: %q", run.ScoreNote)
	}
}
