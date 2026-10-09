package evaldesk

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBadCaseRun(t *testing.T, root, name string, repaired bool, change func(map[string]any, map[string]any, map[string]any, map[string]any)) reqV2Fixture {
	t.Helper()
	return writeRun(t, root, name, func(plan, report, manifest, gates map[string]any) {
		path := filepath.Join(root, "artifacts", "reqv2", name, "reducer", "cases.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`[{"id":"rd-1","title":"预算修改","split":"development","expected":{"revision_delta":1}},{"id":"rd-other","title":"其他题","split":"development"},{"id":"rd-skip","title":"跳过题","split":"development"}]`), 0o644); err != nil {
			t.Fatal(err)
		}
		manifest["files"] = []any{map[string]any{"path": "reducer/cases.json", "sha256": sha256File(t, path), "cases": 3, "sessions": 3}}
		rows := []any{}
		for repeat := 1; repeat <= 3; repeat++ {
			rows = append(rows, map[string]any{"layer": "reducer", "id": "rd-1", "split": "development", "session": "rd-dev-1", "repeat": repeat, "pass": repaired || repeat == 1,
				"assertions":  []any{map[string]any{"name": "budget", "pass": repaired || repeat == 1, "detail": "冻结断言"}},
				"observation": map[string]any{"turns": []any{map[string]any{"reply": "记录预算", "screen_model_called": false}}}})
		}
		rows = append(rows, map[string]any{"layer": "reducer", "id": "rd-other", "repeat": 1, "pass": !repaired, "assertions": []any{map[string]any{"name": "other", "pass": !repaired}}}, map[string]any{"layer": "extraction", "id": "rd-skip", "repeat": 1, "pass": false, "skipped": "零模型未评估"})
		report["cases"] = rows
		report["gate_verdicts"] = []any{map[string]any{"layer": "model", "metric": "latency_p95", "actual": "9000", "threshold": "8000", "passed": false, "evaluable": true}, map[string]any{"layer": "extraction", "metric": "min_cases", "actual": "0", "threshold": "20", "passed": false, "evaluable": false}}
		if change != nil {
			change(plan, report, manifest, gates)
		}
	})
}

func writeBadCaseIndex(t *testing.T, root string, reviews []BadCaseReview) {
	t.Helper()
	path := filepath.Join(root, "docs", "eval", "bad-case-reviews.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"schema_version": 1, "reviews": reviews})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixtureBadCaseReview() BadCaseReview {
	return BadCaseReview{ID: "budget-repair", Kind: "question", Side: "screening", Title: "预算修复", Summary: "冻结预算断言失败", Status: "verified", ReviewedAt: "2026-10-08T12:00:00+08:00", RootCause: "记录有误", Confirmed: true, Sources: []BadCaseSource{{Reference: "review", Excerpt: "冻结依据"}}, References: []BadCaseReference{{RunName: "before", Layer: "reducer", Case: "rd-1", Assertion: "budget"}, {RunName: "after", Layer: "reducer", Case: "rd-1"}}, Fixes: []BadCaseFix{{ID: "guard", Commit: strings.Repeat("a", 40), Reason: "修复预算", Changes: "代码守卫", Tests: []BadCaseTest{{Name: "TestGuard", Evidence: "新增测试", Result: "tests_added"}}, Validations: []BadCaseValidation{{ID: "pair", Baseline: "before", Candidate: "after", Layer: "reducer", Case: "rd-1", Scope: "开发集3次", Conclusion: "目标案例通过", Accepted: true}}}}}
}

func TestBadCaseFactsIdentityAndRunSeparation(t *testing.T) {
	root := t.TempDir()
	writeBadCaseRun(t, root, "before", false, nil)
	writeBadCaseRun(t, root, "same-version", false, nil)
	writeBadCaseRun(t, root, "different-version", false, func(_, _, manifest, _ map[string]any) { manifest["frozen_at"] = "new-version" })
	store := newReqV2Store(t, root)
	catalog := store.BadCases()
	budgetCases, merged := 0, false
	for _, item := range catalog.Cases {
		if item.Kind == "question" && item.Occurrences[0].Assertion == "budget" {
			budgetCases++
			merged = merged || len(item.Occurrences) == 4
		}
		if item.Kind == "run" {
			for _, row := range item.Occurrences {
				if row.Case != "" || row.Repeat != 0 {
					t.Fatal("运行问题不能伪造逐题记录")
				}
			}
		}
		if item.Occurrences[0].State == "skipped" && (len(item.States) != 1 || item.States[0] != "skipped") {
			t.Fatal("跳过不能计为行为失败")
		}
	}
	if budgetCases != 2 || !merged {
		t.Fatalf("同版重复合并、跨版隔离失败：%+v", catalog)
	}
	if len(catalog.Notes) == 0 {
		t.Fatal("缺审阅索引需显示缺口")
	}
	before := sha256File(t, filepath.Join(root, "artifacts", "reqv2", "before", "report.json"))
	for _, tc := range []struct {
		method, path string
		code         int
	}{{"GET", "bad-cases", 200}, {"GET", "bad-case?id=" + catalog.Cases[0].ID, 200}, {"GET", "bad-case?id=../../report.json", 404}, {"POST", "bad-cases", 405}} {
		r := httptest.NewRecorder()
		Handler(store).ServeHTTP(r, httptest.NewRequest(tc.method, "http://127.0.0.1/api/evaldesk/requirement-v2/"+tc.path, nil))
		if r.Code != tc.code {
			t.Fatalf("%s %s: %d", tc.method, tc.path, r.Code)
		}
		if strings.Contains(r.Body.String(), "secret") || strings.Contains(r.Body.String(), "base_host") {
			t.Fatal("凭据不得出现在投影中")
		}
	}
	if after := sha256File(t, filepath.Join(root, "artifacts", "reqv2", "before", "report.json")); after != before {
		t.Fatal("只读接口改写冻结产物")
	}
}

func TestBadCaseVetoReferenceUsesSavedVetoes(t *testing.T) {
	root := t.TempDir()
	writeBadCaseRun(t, root, "veto", false, func(_, report, _, _ map[string]any) {
		report["cases"].([]any)[1].(map[string]any)["vetoes"] = []string{"V9"}
	})
	store := newReqV2Store(t, root)
	_, _, runs := store.buildBadCases()
	rows := store.resolveBadCaseReference(BadCaseReference{RunName: "veto", Layer: "reducer", Case: "rd-1", Assertion: "veto:V9"}, "question", runs)
	if len(rows) != 3 || rows[1].State != "failed" || rows[0].State != "passed" || rows[2].State != "passed" {
		t.Fatalf("否决项应来自各次冻结记录：%+v", rows)
	}
}

func TestBadCaseRunUsageReferencesAndLocalValidation(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"before", "after"} {
		writeBadCaseRun(t, root, name, false, func(plan, report, _, _ map[string]any) {
			count := 0
			calls := 3
			if name == "before" {
				count = 1
				calls = 5
			}
			plan["max_model_requests"] = 4
			report["max_model_requests"] = 4
			report["usage"] = map[string]any{"model_calls": calls, "provider_errors": map[string]int{"timeout": count}}
		})
	}
	r := fixtureBadCaseReview()
	r.Kind = "run"
	r.Side = "all"
	r.References = []BadCaseReference{{RunName: "before", Metric: "provider_errors"}, {RunName: "after", Metric: "provider_errors"}}
	r.Fixes[0].Validations[0].Layer = ""
	r.Fixes[0].Validations[0].Case = ""
	r.Fixes[0].Validations[0].Metric = "provider_errors"
	writeBadCaseIndex(t, root, []BadCaseReview{r})
	store := newReqV2Store(t, root)
	detail, err := store.BadCase(r.ID)
	if err != nil || detail.Status != "verified" || !detail.Verifications[0].Checked || detail.Occurrences[0].Case != "" {
		t.Fatalf("运行级用量应准确核对而不伪造题目：%+v %v", detail, err)
	}
	if *detail.Verifications[0].CandidateRun.GatePassed {
		t.Fatal("局部用量验证不能提升冻结总门槛")
	}
	_, _, runs := store.buildBadCases()
	for _, metric := range []string{"provider_errors", "call_budget"} {
		before := store.resolveBadCaseReference(BadCaseReference{RunName: "before", Metric: metric}, "run", runs)
		after := store.resolveBadCaseReference(BadCaseReference{RunName: "after", Metric: metric}, "run", runs)
		if before[0].State != "failed" || after[0].State != "passed" {
			t.Fatalf("冻结用量引用错误：%s %+v %+v", metric, before, after)
		}
	}
}

func TestBadCaseVerificationAndEvidenceGaps(t *testing.T) {
	for _, scenario := range []string{"complete", "zero-model", "model-missing", "missing-assertions", "not-accepted", "host-different", "options-different", "dirty", "missing-run", "version-different", "skipped", "missing-frozen"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			writeBadCaseRun(t, root, "before", false, func(plan, report, _, _ map[string]any) {
				if scenario == "zero-model" {
					plan["zero_model"] = true
					plan["models"] = nil
					plan["mode"] = "deterministic"
					report["mode"] = "deterministic"
				}
			})
			fixture := writeBadCaseRun(t, root, "after", true, func(plan, report, manifest, _ map[string]any) {
				switch scenario {
				case "zero-model":
					plan["zero_model"] = true
					plan["models"] = nil
					plan["mode"] = "deterministic"
					report["mode"] = "deterministic"
				case "model-missing":
					plan["models"] = nil
				case "missing-assertions":
					report["cases"].([]any)[1].(map[string]any)["assertions"] = []any{}
				case "host-different":
					plan["models"].(map[string]any)["screening"].(map[string]any)["base_host"] = "different"
				case "options-different":
					plan["models"].(map[string]any)["screening"].(map[string]any)["new_option"] = "value"
				case "dirty":
					plan["code_dirty"] = true
				case "version-different":
					manifest["frozen_at"] = "different"
				case "skipped":
					report["cases"].([]any)[1].(map[string]any)["skipped"] = "未评估"
				}
			})
			review := fixtureBadCaseReview()
			if scenario == "not-accepted" {
				review.Fixes[0].Validations[0].Accepted = false
			}
			if scenario == "missing-run" {
				review.Fixes[0].Validations[0].Candidate = "no-such-run"
			}
			if scenario == "missing-frozen" {
				if err := os.Remove(filepath.Join(fixture.Dir, "reducer", "cases.json")); err != nil {
					t.Fatal(err)
				}
			}
			writeBadCaseIndex(t, root, []BadCaseReview{review})
			detail, err := newReqV2Store(t, root).BadCase(review.ID)
			if err != nil {
				t.Fatal(err)
			}
			checked := scenario == "complete" || scenario == "zero-model"
			if detail.Verifications[0].Checked != checked || (detail.Status == "verified") != checked {
				t.Fatalf("错误验证状态：%+v", detail)
			}
			if checked && (len(detail.Occurrences) != 6 || len(detail.Verifications[0].Regressed) != 1 || detail.Verifications[0].Regressed[0] != "reducer/rd-other") {
				t.Fatal("重复记录或确定性退步丢失")
			}
			if len(detail.Notes) == 0 {
				t.Fatal("本机不存在的代码提交必须显示缺口")
			}
		})
	}
}

func TestBadCaseIndexValidationAndExplicitRegression(t *testing.T) {
	root := t.TempDir()
	writeBadCaseRun(t, root, "before", false, nil)
	writeBadCaseRun(t, root, "after", true, nil)
	writeBadCaseRun(t, root, "diagnostic", false, nil)
	store := newReqV2Store(t, root)
	review := fixtureBadCaseReview()
	review.References = append(review.References, BadCaseReference{RunName: "diagnostic", Layer: "reducer", Case: "rd-1", Context: "换模型探索"})
	second := review
	second.ID = "second-case"
	writeBadCaseIndex(t, root, []BadCaseReview{review, second})
	detail, err := store.BadCase(review.ID)
	if err != nil || detail.Status != "verified" || len(detail.Occurrences) != 9 {
		t.Fatalf("探索或后续人工关系不得移除历史或更新当前状态：%+v %v", detail, err)
	}
	review.Status = "recurred"
	review.Regression = &BadCaseReference{RunName: "diagnostic", Layer: "reducer", Case: "rd-1", Repeat: 2, Assertion: "budget"}
	writeBadCaseIndex(t, root, []BadCaseReview{review})
	detail, err = store.BadCase(review.ID)
	if err != nil || detail.Status != "recurred" {
		t.Fatal("显式回归候选应保留再次失败")
	}
	for _, mutation := range []string{"duplicate-id", "path", "short-commit", "no-source", "bad-test"} {
		t.Run(mutation, func(t *testing.T) {
			r := fixtureBadCaseReview()
			list := []BadCaseReview{r}
			switch mutation {
			case "duplicate-id":
				list = append(list, r)
			case "path":
				list[0].References[0].RunName = "../../before"
			case "short-commit":
				list[0].Fixes[0].Commit = "abc"
			case "no-source":
				list[0].Sources = nil
			case "bad-test":
				list[0].Fixes[0].Tests[0].Result = "assumed_pass"
			}
			writeBadCaseIndex(t, root, list)
			if reviews, note := store.badCaseReviews(); len(reviews) != 0 || note == "" {
				t.Fatal("无效索引不得采用")
			}
			if facts := store.BadCases(); len(facts.Cases) == 0 || len(facts.Notes) == 0 {
				t.Fatal("索引无效仍应保留原始失败事实和错误")
			}
		})
	}
}

func TestBadCaseRealEvidenceChain(t *testing.T) {
	root := filepath.Join("..", "..")
	baselineDir := filepath.Join(root, "artifacts", "reqv2", "screening-fix-live-devcal-r3-20260924")
	candidateDir := filepath.Join(root, "artifacts", "reqv2", "screening-fix2-live-devcal-r3-20260924")
	if _, err := os.Stat(candidateDir); os.IsNotExist(err) {
		t.Skip("本机冻结验证产物未提供")
	}
	before := map[string]string{}
	for _, dir := range []string{baselineDir, candidateDir} {
		for _, name := range []string{"report.json", "plan.json", "manifest.json", "gates.json", "results.jsonl"} {
			path := filepath.Join(dir, name)
			before[path] = sha256File(t, path)
		}
	}
	store := newReqV2Store(t, root)
	reviews, note := store.badCaseReviews()
	if note != "" || len(reviews) != 70 {
		t.Fatalf("索引应包含首批5项、62项历史合同核对与3项保留集待办：%s %d", note, len(reviews))
	}
	detail, err := store.BadCase("owned-parts-removal")
	if err != nil || detail.Status != "verified" || len(detail.Occurrences) != 10 {
		t.Fatalf("真实闭环缺失：%+v %v", detail, err)
	}
	pair := detail.Verifications[0]
	if !pair.Checked || !pair.ModelSame || pair.BaselineRun.LayerScores["conversations"].Passed != 6 || pair.CandidateRun.LayerScores["conversations"].Passed != 7 || len(pair.Regressed) != 0 {
		t.Fatal("冻结前后数值不匹配")
	}
	wrongWrites := func(gates []ReqV2GateVerdictRow) string {
		for _, g := range gates {
			if g.Metric == "key_field_wrong_write_total" {
				return g.Actual
			}
		}
		return "missing"
	}
	if wrongWrites(pair.BaselineGates) != "2" || wrongWrites(pair.CandidateGates) != "0" {
		t.Fatal("关键字段错写应为2→0")
	}
	if len(pair.CandidateCase.Repeats) != 3 || pair.CandidateCase.Frozen == nil {
		t.Fatal("冻结期望与全部重复缺失")
	}
	if strings.Contains(fmtBadCase(detail), "base_host") {
		t.Fatal("完整配置只应内部核对")
	}
	for path, hash := range before {
		if sha256File(t, path) != hash {
			t.Fatalf("读取修改产物：%s", path)
		}
	}
}

func TestBadCaseHistoricalAcceptanceKeepsComparisonBoundaries(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong-source-hash", "no-regrade", "different-fixture", "dirty-candidate", "failed-candidate", "missing-repeat", "duplicate-repeat", "no-prior-failure", "not-accepted"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			writeBadCaseRun(t, root, "source", false, nil)
			sourceSHA := sha256File(t, filepath.Join(root, "artifacts", "reqv2", "source", "report.json"))
			writeBadCaseRun(t, root, "before", scenario == "no-prior-failure", func(plan, report, _, _ map[string]any) {
				plan["regrade"] = scenario != "no-regrade"
				plan["source_run"], plan["source_report_sha256"] = "artifacts/reqv2/source", sourceSHA
				delete(plan, "models")
				delete(plan, "code_commit")
				delete(plan, "code_dirty")
				if scenario == "wrong-source-hash" {
					plan["source_report_sha256"] = strings.Repeat("0", 64)
				}
			})
			writeBadCaseRun(t, root, "after", scenario != "failed-candidate", func(plan, report, manifest, _ map[string]any) {
				if scenario == "different-fixture" {
					manifest["frozen_at"] = "changed"
				}
				if scenario == "dirty-candidate" {
					plan["code_dirty"] = true
				}
				rows := report["cases"].([]any)
				if scenario == "missing-repeat" {
					report["cases"] = rows[1:]
				}
				if scenario == "duplicate-repeat" {
					rows[1].(map[string]any)["repeat"] = 1
				}
			})
			review := fixtureBadCaseReview()
			review.Fixes[0].Sources = review.Sources
			review.Fixes[0].Validations[0].ValidationKind = "historical"
			review.Fixes[0].Validations[0].Accepted = scenario != "not-accepted"
			writeBadCaseIndex(t, root, []BadCaseReview{review})
			detail, err := newReqV2Store(t, root).BadCase(review.ID)
			if err != nil {
				t.Fatal(err)
			}
			pair := detail.Verifications[0]
			if pair.Checked != (scenario == "valid") {
				t.Fatalf("历史验收边界错误：%+v", pair.Notes)
			}
			if scenario == "valid" && (detail.Category != "verified" || detail.Conclusion != "历史修复与验收已核对" || pair.ModelSame || pair.Comparison.Strict) {
				t.Fatal("历史修复核对不能伪造同模型严格配对实验")
			}
		})
	}
}

func TestBadCaseRequestCacheDoesNotHideChangedFrozenFiles(t *testing.T) {
	root := t.TempDir()
	writeBadCaseRun(t, root, "before", false, nil)
	writeBadCaseRun(t, root, "after", true, nil)
	writeBadCaseIndex(t, root, []BadCaseReview{fixtureBadCaseReview()})
	store := newReqV2Store(t, root)
	first, err := store.BadCase("budget-repair")
	if err != nil || first.Status != "verified" {
		t.Fatal("完整证据应通过")
	}
	path := filepath.Join(root, "artifacts", "reqv2", "after", "reducer", "cases.json")
	if err := os.WriteFile(path, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := store.BadCase("budget-repair")
	if err != nil || second.Status != "pending" || second.Verifications[0].CandidateCase.Frozen != nil {
		t.Fatal("新请求必须重新核对冻结文件，不能沿用上次逐题缓存")
	}
}

func TestBadCaseAllHistoricalContractsHaveFrozenAcceptance(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "artifacts", "reqv2", "spec6-cert-baseline-v1-regrade-v4-20260924")); os.IsNotExist(err) {
		t.Skip("本机历史产物未提供")
	}
	store := newReqV2Store(t, root)
	catalog, reviews, runs := store.buildBadCases()
	count := 0
	for _, item := range catalog.Cases {
		if !strings.HasPrefix(item.ID, "historical-") {
			continue
		}
		count++
		if item.Status != "verified" || item.ValidationKind != "historical" {
			t.Fatalf("历史关联缺验收：%s %s", item.ID, item.Status)
		}
		if len(reviews[item.ID].Fixes[0].Sources) == 0 {
			t.Fatal("历史修复不能无实现来源")
		}
	}
	if count != 62 {
		t.Fatalf("应覆盖62个旧合同失败题，实际%d", count)
	}
	for _, id := range []string{"historical-rdy-existing-unknown-blocks", "historical-pol-ready-confirm", "historical-ui-ready-unconfirmed", "historical-ex-all-new-purchase"} {
		review := reviews[id]
		pair := store.verifyBadCase(review.Fixes[0].Validations[0], runs)
		if !pair.Checked || len(pair.CandidateCase.Repeats) != 3 || pair.CandidateCase.Frozen == nil || pair.Comparison.Strict {
			t.Fatalf("代表性案例闭环不完整：%s", id)
		}
	}
	for _, item := range catalog.Cases {
		if item.Kind == "question" && strings.Contains(item.Occurrences[0].Case, "holdout") && item.Status == "verified" {
			t.Fatal("开发/校准修复不能关闭保留集失败")
		}
	}
}

func fmtBadCase(value any) string { raw, _ := json.Marshal(value); return string(raw) }

func TestBadCaseReferenceDoesNotInventRepair(t *testing.T) {
	for _, variant := range []string{"complete", "other-version", "missing-reference", "missing-repeat", "skipped", "failed-reference", "invalid-path"} {
		t.Run(variant, func(t *testing.T) {
			root := t.TempDir()
			writeBadCaseRun(t, root, "before", false, nil)
			writeBadCaseRun(t, root, "reference", true, func(_, report, manifest, _ map[string]any) {
				switch variant {
				case "other-version":
					manifest["frozen_at"] = "other"
				case "missing-repeat":
					report["cases"] = report["cases"].([]any)[1:]
				case "skipped":
					report["cases"].([]any)[0].(map[string]any)["skipped"] = "未评估"
				case "failed-reference":
					report["cases"].([]any)[0].(map[string]any)["pass"] = false
				}
			})
			name := "reference"
			if variant == "missing-reference" {
				name = "missing"
			}
			if variant == "invalid-path" {
				name = "../reference"
			}
			writeBadCaseIndex(t, root, nil)
			raw, _ := json.Marshal(map[string]any{"schema_version": 1, "reviews": []any{}, "reference": map[string]any{"run_name": name, "label": "所选历史验收", "source": "review.md"}})
			if err := os.WriteFile(filepath.Join(root, "docs", "eval", "bad-case-reviews.json"), raw, 0o644); err != nil {
				t.Fatal(err)
			}
			catalog := newReqV2Store(t, root).BadCases()
			found := false
			for _, item := range catalog.Cases {
				if item.Kind != "question" || item.Occurrences[0].Assertion != "budget" {
					continue
				}
				found = true
				if item.Status != "unanalysed" {
					t.Fatal("参考通过不能自动确认修复")
				}
				ref := item.Reference
				switch variant {
				case "complete":
					if ref == nil || ref.State != "passed" || ref.Passed != 3 || ref.Total != 3 || len(ref.MatchedRunIDs) != 1 {
						t.Fatalf("缺参考：%+v", ref)
					}
				case "other-version":
					if ref == nil || ref.State != "unmatched" || ref.Total != 0 {
						t.Fatalf("跨版误认通过：%+v", ref)
					}
				case "missing-reference", "invalid-path":
					if ref != nil || len(catalog.Notes) == 0 {
						t.Fatal("缺失或非法引用必须显示缺口")
					}
				default:
					if ref == nil || ref.State == "passed" {
						t.Fatalf("不完整/失败参考不能报通过：%+v", ref)
					}
				}
			}
			if !found {
				t.Fatal("历史失败不得丢失")
			}
		})
	}
}

func TestBadCaseRealModelExplorationReference(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "artifacts", "reqv2", "modelcmp-glm-devcal-r3-20260924")); os.IsNotExist(err) {
		t.Skip("本机历史模型对比未提供")
	}
	catalog := newReqV2Store(t, root).BadCases()
	if catalog.Reference == nil || catalog.Reference.Run == nil {
		t.Fatal("缺明确验收参考")
	}
	wanted := map[string]string{"ex-owned-gpu-vague": "modelcmp-glm-devcal-r3-20260924", "cv-budget-correction": "modelcmp-kimi-devcal-r3-20260924", "cv-monitor-refusal": "modelcmp-kimi-devcal-r3-20260924"}
	found := map[string]bool{}
	for _, item := range catalog.Cases {
		for _, occurrence := range item.Occurrences {
			if name, ok := wanted[occurrence.Case]; ok && occurrence.RunName == name && item.Status == "unanalysed" {
				ref := item.Reference
				if ref == nil || ref.State != "passed" || ref.Passed != 3 || ref.Total != 3 {
					t.Fatalf("截图案例参考未关联：%s %+v", occurrence.Case, ref)
				}
				found[occurrence.Case] = true
			}
		}
	}
	if len(found) != 3 {
		t.Fatalf("三个模型探索案例未保留：%v", found)
	}
}

func TestBadCaseFullInventoryHasOneDispositionPerRecord(t *testing.T) {
	root := t.TempDir()
	writeBadCaseRun(t, root, "before", false, nil)
	writeBadCaseRun(t, root, "other-version", false, func(_, _, manifest, _ map[string]any) { manifest["frozen_at"] = "new-version" })
	writeBadCaseRun(t, root, "after", true, nil)
	writeBadCaseIndex(t, root, []BadCaseReview{fixtureBadCaseReview()})
	catalog := newReqV2Store(t, root).BadCases()
	seen := map[string]int{}
	byID := map[string]BadCaseSummary{}
	for _, item := range catalog.Cases {
		byID[item.ID] = item
	}
	mergedVersions := false
	for _, group := range catalog.Groups {
		if group.Conclusion == "" || group.Category == "" {
			t.Fatal("目录必须只有一个明确分类结论")
		}
		for _, id := range group.Members {
			seen[id]++
			item := byID[id]
			if item.Category != group.Category {
				t.Fatal("待处理、已验证、历史和缺证据不能混合归组")
			}
			if item.Status == "verified" && group.Category != "verified" {
				t.Fatal("已验证不能被旧失败撤回")
			}
		}
		mergedVersions = mergedVersions || group.Versions > 1 && group.Kind == "question"
	}
	for _, item := range catalog.Cases {
		if seen[item.ID] != 1 {
			t.Fatalf("记录遗失或重复归类：%s %d", item.ID, seen[item.ID])
		}
	}
	if len(catalog.Groups) >= len(catalog.Cases) || !mergedVersions {
		t.Fatal("同题不同断言和版本需收进历史目录，根因分别保留")
	}
	for _, tc := range []struct {
		item     BadCaseSummary
		category string
	}{
		{BadCaseSummary{Status: "pending", Reference: &BadCaseReferenceResult{State: "passed"}}, "open"},
		{BadCaseSummary{Status: "verified", States: []string{"failed", "passed"}}, "verified"},
		{BadCaseSummary{Status: "unanalysed", Reference: &BadCaseReferenceResult{State: "passed"}}, "history"},
		{BadCaseSummary{Status: "unanalysed", Reference: &BadCaseReferenceResult{State: "failed"}}, "open"},
		{BadCaseSummary{Status: "unanalysed", States: []string{"skipped", "unevaluable"}}, "history"},
		{BadCaseSummary{Status: "unanalysed", States: []string{"failed"}}, "gaps"},
	} {
		if category, _ := badCaseDisposition(tc.item); category != tc.category {
			t.Fatalf("错误分类：%+v %s", tc.item, category)
		}
	}
}
