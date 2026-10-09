package evaldesk

// 案例关系来自人工索引；成绩、断言和观测始终来自所选冻结运行。
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

type BadCaseSource struct {
	Reference string `json:"reference"`
	Excerpt   string `json:"excerpt"`
}

type BadCaseReference struct {
	RunName   string `json:"run_name"`
	Layer     string `json:"layer,omitempty"`
	Case      string `json:"case,omitempty"`
	Assertion string `json:"assertion,omitempty"`
	Metric    string `json:"metric,omitempty"`
	Repeat    int    `json:"repeat,omitempty"`
	Turn      int    `json:"turn,omitempty"`
	Context   string `json:"context"`
}

type BadCaseTest struct {
	Name     string `json:"name"`
	Evidence string `json:"evidence"`
	Result   string `json:"result"` // recorded_pass | tests_added | unknown
}

type BadCaseValidation struct {
	ValidationKind string   `json:"validation_kind,omitempty"` // historical: 已重判旧观测与干净候选的验收核对，非配对实验
	ID             string   `json:"id"`
	Baseline       string   `json:"baseline"` // 准确目录名，不接受路径
	Candidate      string   `json:"candidate"`
	Layer          string   `json:"layer,omitempty"`
	Case           string   `json:"case,omitempty"`
	Metric         string   `json:"metric,omitempty"`
	Conclusion     string   `json:"conclusion"`
	Scope          string   `json:"scope"`
	Limitations    []string `json:"limitations"`
	Accepted       bool     `json:"accepted"` // 人工确认，仅在目标证据满足时投影为已核对
}

type BadCaseFix struct {
	ID           string              `json:"id"`
	Commit       string              `json:"commit"`
	Reason       string              `json:"reason"`
	Changes      string              `json:"changes"`
	PromptRole   string              `json:"prompt_role,omitempty"`
	PromptCommit string              `json:"prompt_commit,omitempty"`
	PromptBase   string              `json:"prompt_base,omitempty"`
	Combination  bool                `json:"combination"`
	Tests        []BadCaseTest       `json:"tests"`
	Sources      []BadCaseSource     `json:"sources"`
	Validations  []BadCaseValidation `json:"validations"`
}

type BadCaseReview struct {
	ID         string             `json:"id"`
	Kind       string             `json:"kind"` // question | run
	Side       string             `json:"side"`
	Title      string             `json:"title"`
	Summary    string             `json:"summary"`
	Status     string             `json:"status"`
	ReviewedAt string             `json:"reviewed_at"`
	RootCause  string             `json:"root_cause"`
	Confirmed  bool               `json:"confirmed"`
	Unknown    []string           `json:"unknown"`
	Sources    []BadCaseSource    `json:"sources"`
	References []BadCaseReference `json:"references"`
	Fixes      []BadCaseFix       `json:"fixes"`
	Regression *BadCaseReference  `json:"regression,omitempty"` // 仅指定验收候选可改变状态
}

type BadCaseOccurrence struct {
	ID             string           `json:"id"`
	Run            *ReqV2RunSummary `json:"run,omitempty"`
	RunName        string           `json:"run_name"`
	Layer          string           `json:"layer,omitempty"`
	Case           string           `json:"case,omitempty"`
	Assertion      string           `json:"assertion,omitempty"`
	AssertionIndex int              `json:"assertion_index,omitempty"`
	Metric         string           `json:"metric,omitempty"`
	Repeat         int              `json:"repeat,omitempty"`
	Turn           int              `json:"turn,omitempty"`
	State          string           `json:"state"` // failed | passed | skipped | unevaluable | incomplete | missing
	Detail         string           `json:"detail"`
	Context        string           `json:"context,omitempty"`
	ManifestSHA    string           `json:"manifest_sha256,omitempty"`
}

type BadCaseSummary struct {
	ValidationKind string                  `json:"validation_kind,omitempty"`
	ID             string                  `json:"id"`
	Kind           string                  `json:"kind"`
	Side           string                  `json:"side"`
	Title          string                  `json:"title"`
	Summary        string                  `json:"summary"`
	Status         string                  `json:"status"`
	States         []string                `json:"states"`
	LastSeen       string                  `json:"last_seen"`
	RunIDs         []string                `json:"run_ids"`
	Occurrences    []BadCaseOccurrence     `json:"occurrences"`
	Commits        []string                `json:"commits"`
	Reference      *BadCaseReferenceResult `json:"reference,omitempty"`
	Category       string                  `json:"category"`
	Conclusion     string                  `json:"conclusion"`
}

// 分组是目录组织，不把同题不同版本或断言推断成同一根因。
type BadCaseGroup struct {
	ID         string   `json:"id"`
	Category   string   `json:"category"`
	Kind       string   `json:"kind"`
	Side       string   `json:"side"`
	Layer      string   `json:"layer,omitempty"`
	Case       string   `json:"case,omitempty"`
	Metric     string   `json:"metric,omitempty"`
	Title      string   `json:"title"`
	Conclusion string   `json:"conclusion"`
	Members    []string `json:"members"`
	RunIDs     []string `json:"run_ids"`
	Versions   int      `json:"versions"`
	LastSeen   string   `json:"last_seen"`
}

// 参考结果只回答同一冻结身份下该题的记录，不改变人工处理状态。
type BadCaseReferenceResult struct {
	RunID         string   `json:"run_id"`
	Layer         string   `json:"layer"`
	Case          string   `json:"case"`
	State         string   `json:"state"`
	Passed        int      `json:"passed"`
	Total         int      `json:"total"`
	MatchedRunIDs []string `json:"matched_run_ids"`
	Note          string   `json:"note"`
}

type BadCaseReferenceRun struct {
	RunName string           `json:"run_name"`
	Label   string           `json:"label"`
	Source  string           `json:"source"`
	Run     *ReqV2RunSummary `json:"run,omitempty"`
}

type badCaseIndex struct {
	SchemaVersion int                  `json:"schema_version"`
	Reviews       []BadCaseReview      `json:"reviews"`
	Reference     *BadCaseReferenceRun `json:"reference,omitempty"`
}

type BadCaseCatalog struct {
	Cases     []BadCaseSummary     `json:"cases"`
	Notes     []string             `json:"notes"`
	Reference *BadCaseReferenceRun `json:"reference,omitempty"`
	Groups    []BadCaseGroup       `json:"groups"`
}

type BadCaseVerification struct {
	BadCaseValidation
	BaselineRun    *ReqV2RunSummary      `json:"baseline_run,omitempty"`
	CandidateRun   *ReqV2RunSummary      `json:"candidate_run,omitempty"`
	BaselineCase   *ReqV2CaseDetail      `json:"baseline_case,omitempty"`
	CandidateCase  *ReqV2CaseDetail      `json:"candidate_case,omitempty"`
	Comparison     *ReqV2CompareResponse `json:"comparison,omitempty"`
	Checked        bool                  `json:"checked"`
	ModelSame      bool                  `json:"model_same"`
	Notes          []string              `json:"notes"`
	BaselineGates  []ReqV2GateVerdictRow `json:"baseline_gates"`
	CandidateGates []ReqV2GateVerdictRow `json:"candidate_gates"`
	Regressed      []string              `json:"regressed"`
}

type BadCaseDetail struct {
	BadCaseSummary
	Review        *BadCaseReview        `json:"review,omitempty"`
	Verifications []BadCaseVerification `json:"verifications"`
	Notes         []string              `json:"notes"`
}

var badCaseIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,95}$`)

func badCaseID(parts ...string) string {
	return fmt.Sprintf("fact-%x", sha256.Sum256([]byte(strings.Join(parts, "\x00"))))
}

func fullCommit(value string) bool {
	_, err := hex.DecodeString(value)
	return len(value) == 40 && err == nil
}

func validBadCaseReference(ref BadCaseReference, kind string) bool {
	if ref.RunName == "" || ref.RunName == "." || ref.RunName == ".." || filepath.Base(ref.RunName) != ref.RunName || strings.ContainsAny(ref.RunName, `/\\`) || ref.Repeat < 0 || ref.Turn < 0 {
		return false
	}
	if kind == "run" {
		return ref.Metric != "" && ref.Case == "" && ref.Repeat == 0 && ref.Turn == 0
	}
	return badCaseSide(ref.Layer) != "unknown" && ref.Case != "" && ref.Metric == ""
}

func badCaseSide(layer string) string {
	switch layer {
	case "extraction", "conversations", "reducer", "readiness":
		return "screening"
	case "policy", "ui-contract":
		return "builder"
	default:
		return "unknown"
	}
}

func (s *Store) badCaseReviews() ([]BadCaseReview, string) {
	index, note := s.badCaseIndex()
	return index.Reviews, note
}

func (s *Store) badCaseIndex() (badCaseIndex, string) {
	raw, err := s.read(filepath.Join(s.root, "docs", "eval"), "bad-case-reviews.json")
	if os.IsNotExist(err) {
		return badCaseIndex{}, "案例审阅索引未记录；仍可浏览冻结失败事实。"
	}
	var index badCaseIndex
	if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &index) != nil || index.SchemaVersion != 1 {
		return badCaseIndex{}, "案例审阅索引不可读取或格式无效；未采用分析和修复结论。"
	}
	if ref := index.Reference; ref != nil {
		// 文件只声明准确目录；运行摘要始终从冻结产物读取。
		ref.Run = nil
		if !validBadCaseReference(BadCaseReference{RunName: ref.RunName, Metric: "reference"}, "run") || ref.Label == "" || ref.Source == "" {
			return badCaseIndex{}, "验收参考索引无效；未采用分析和修复结论。"
		}
	}
	seen := map[string]bool{}
	for _, review := range index.Reviews {
		_, dateErr := time.Parse(time.RFC3339, review.ReviewedAt)
		valid := badCaseIDPattern.MatchString(review.ID) && !seen[review.ID] && (review.Kind == "question" || review.Kind == "run") && (review.Side == "screening" || review.Side == "builder" || review.Side == "all") && review.Title != "" && review.Summary != "" && dateErr == nil && len(review.References) > 0
		seen[review.ID] = true
		switch review.Status {
		case "unanalysed", "located", "pending", "verified", "recurred":
		default:
			valid = false
		}
		if review.Confirmed && (review.RootCause == "" || len(review.Sources) == 0) {
			valid = false
		}
		for _, ref := range review.References {
			valid = valid && validBadCaseReference(ref, review.Kind)
		}
		if review.Regression != nil {
			valid = valid && validBadCaseReference(*review.Regression, review.Kind)
		}
		fixIDs := map[string]bool{}
		for _, fix := range review.Fixes {
			valid = valid && badCaseIDPattern.MatchString(fix.ID) && !fixIDs[fix.ID] && fullCommit(fix.Commit) && fix.Reason != "" && fix.Changes != ""
			fixIDs[fix.ID] = true
			if fix.PromptCommit != "" || fix.PromptBase != "" || fix.PromptRole != "" {
				valid = valid && fullCommit(fix.PromptCommit) && (fix.PromptBase == "" || fullCommit(fix.PromptBase)) && (fix.PromptRole == "screening" || fix.PromptRole == "builder")
			}
			for _, test := range fix.Tests {
				valid = valid && test.Name != "" && test.Evidence != "" && (test.Result == "recorded_pass" || test.Result == "tests_added" || test.Result == "unknown")
			}
			validationIDs := map[string]bool{}
			for _, verification := range fix.Validations {
				valid = valid && (verification.ValidationKind == "" || verification.ValidationKind == "historical" && review.Kind == "question" && len(fix.Sources) > 0)
				valid = valid && badCaseIDPattern.MatchString(verification.ID) && !validationIDs[verification.ID] && validBadCaseReference(BadCaseReference{RunName: verification.Baseline, Metric: "validation"}, "run") && validBadCaseReference(BadCaseReference{RunName: verification.Candidate, Metric: "validation"}, "run") && verification.Scope != "" && verification.Conclusion != ""
				// 题目验证必须明确目标，不能用整批通过代替该案例。
				if review.Kind == "question" && (badCaseSide(verification.Layer) == "unknown" || verification.Case == "") {
					valid = false
				}
				if review.Kind == "run" && (verification.Metric == "" || verification.Case != "") {
					valid = false
				}
				validationIDs[verification.ID] = true
			}
		}
		if review.Status != "unanalysed" && (!review.Confirmed || len(review.Sources) == 0) {
			valid = false
		}
		if (review.Status == "pending" || review.Status == "verified" || review.Status == "recurred") && len(review.Fixes) == 0 {
			valid = false
		}
		if review.Status == "recurred" && review.Regression == nil {
			valid = false
		}
		if !valid {
			return badCaseIndex{}, "案例审阅索引的身份、状态、提交或引用无效；未采用分析和修复结论。"
		}
	}
	return index, ""
}

func occurrence(run *savedReqV2Run, row ReqV2CaseResultRow, assertion, metric, state, detail string, assertionIndex int) BadCaseOccurrence {
	return BadCaseOccurrence{
		ID:  badCaseID(run.summary.ID, row.Layer, row.ID, fmt.Sprint(row.Repeat), assertion, metric, fmt.Sprint(assertionIndex)),
		Run: &run.summary, RunName: run.summary.DirName, Layer: row.Layer, Case: row.ID,
		Repeat: row.Repeat, Assertion: assertion, AssertionIndex: assertionIndex, Metric: metric,
		State: state, Detail: truncateText(detail, reqV2MaxText),
	}
}

func (s *Store) badCaseFacts() (map[string]*BadCaseSummary, map[string]*savedReqV2Run, []string) {
	facts := map[string]*BadCaseSummary{}
	byName := map[string]*savedReqV2Run{}
	runs := s.RunsReqV2()
	s.mu.Lock()
	snapshots := make(map[string]*savedReqV2Run, len(runs.Runs))
	for _, summary := range runs.Runs {
		if cached := s.cacheReqV2[summary.ID]; cached != nil {
			local := *cached
			local.caseDetails = map[string]*ReqV2CaseDetail{}
			snapshots[summary.ID] = &local
		}
	}
	s.mu.Unlock()
	notes := append([]string{}, runs.Warnings...)
	// ponytail: 本机几十批产物按需扫描，不建第二份持久化索引；规模增长后按产物指纹缓存投影。
	for _, summary := range runs.Runs {
		run := snapshots[summary.ID]
		if run == nil {
			notes = append(notes, "一份已列出的运行已不可读取；未当作零失败。")
			continue
		}
		byName[summary.DirName] = run
		raw, err := s.read(run.dir, "report.json")
		var report reqV2ReportHead
		if err != nil || json.Unmarshal(raw, &report) != nil {
			notes = append(notes, summary.DirName+"：报告不可读取，失败数量未知。")
			item := occurrence(run, ReqV2CaseResultRow{}, "", "integrity", "incomplete", "报告不可读取，失败数量未知。", 0)
			id := badCaseID(summary.ID + "/integrity")
			facts[id] = &BadCaseSummary{ID: id, Kind: "run", Side: "all", Title: "运行证据需核对", Summary: item.Detail, Status: "unanalysed", Occurrences: []BadCaseOccurrence{item}, Commits: []string{}}
			continue
		}
		titles := map[string]string{}
		loaded := map[string]bool{}
		add := func(kind, side, title, identity string, item BadCaseOccurrence) {
			item.ManifestSHA = report.ManifestSHA256
			id := badCaseID(identity)
			entry := facts[id]
			if entry == nil {
				entry = &BadCaseSummary{ID: id, Kind: kind, Side: side, Title: title, Summary: item.Detail, Status: "unanalysed", Occurrences: []BadCaseOccurrence{}, Commits: []string{}}
				facts[id] = entry
			}
			entry.Occurrences = append(entry.Occurrences, item)
		}
		for _, row := range report.Cases {
			needsProjection := !row.Pass || len(row.Vetoes) > 0 || row.Skipped != "" || row.ObservationMeta.Skipped != ""
			for _, assertion := range row.Assertions {
				needsProjection = needsProjection || !assertion.Pass
			}
			if !needsProjection {
				continue
			}
			if !loaded[row.Layer] {
				frozen, _ := s.reqV2FrozenCases(run.dir, row.Layer)
				for _, c := range frozen {
					titles[c.Layer+"/"+c.ID] = c.Title
				}
				loaded[row.Layer] = true
			}
			title := firstNonEmpty(titles[row.Layer+"/"+row.ID], row.ID+"（场景名称未记录）")
			manifest := report.ManifestSHA256
			if len(manifest) != 64 || run.summary.Evidence.Status == "invalid" {
				manifest = summary.ID // 身份未知/无效时不跨运行合并。
			}
			addQuestion := func(assertion, state, detail string, index int) {
				item := occurrence(run, row, assertion, "", state, detail, index)
				add("question", badCaseSide(row.Layer), title, strings.Join([]string{manifest, row.Layer, row.ID, assertion, state}, "\x00"), item)
				if state == "failed" {
					facts[badCaseID(strings.Join([]string{manifest, row.Layer, row.ID, assertion, state}, "\x00"))].Summary = "冻结断言未通过：" + assertion
				}
			}
			if skipped := firstNonEmpty(row.Skipped, row.ObservationMeta.Skipped); skipped != "" {
				addQuestion("skipped", "skipped", skipped, 0)
				continue
			}
			failed := false
			for index, assertion := range row.Assertions {
				if !assertion.Pass {
					failed = true
					addQuestion(assertion.Name, "failed", firstNonEmpty(assertion.Detail, assertion.Name), index+1)
				}
			}
			for _, veto := range row.Vetoes {
				failed = true
				addQuestion("veto:"+veto, "failed", "否决项："+veto, 0)
			}
			if !row.Pass && !failed {
				addQuestion("unrecorded-failure", "incomplete", "题目未通过，但失败断言未记录。", 0)
			}
		}
		for _, gate := range report.GateVerdicts {
			if gate.Passed && gate.Evaluable {
				continue
			}
			state := "failed"
			if !gate.Evaluable {
				state = "unevaluable"
			}
			item := occurrence(run, ReqV2CaseResultRow{Layer: gate.Layer}, "", gate.Metric, state, gate.Actual+" / 要求 "+gate.Threshold+" "+gate.Note, 0)
			add("run", "all", gate.Metric, summary.ID+"/gate/"+gate.Layer+"/"+gate.Metric, item)
		}
		var usage struct {
			ModelCalls     int            `json:"model_calls"`
			ProviderErrors map[string]int `json:"provider_errors"`
		}
		if json.Unmarshal(report.Usage, &usage) == nil {
			errors := 0
			for _, count := range usage.ProviderErrors {
				if count > 0 {
					errors += count
				}
			}
			if errors > 0 {
				item := occurrence(run, ReqV2CaseResultRow{}, "", "provider_errors", "failed", fmt.Sprintf("用量报告记录 %d 次 provider 失败；不推断为某道题行为错误。", errors), 0)
				add("run", "all", "上游调用失败", summary.ID+"/provider_errors", item)
			}
			if report.MaxModelRequests > 0 && usage.ModelCalls > report.MaxModelRequests {
				item := occurrence(run, ReqV2CaseResultRow{}, "", "call_budget", "failed", fmt.Sprintf("记录调用 %d 次 / 预算 %d 次；冻结门槛保持原样。", usage.ModelCalls, report.MaxModelRequests), 0)
				add("run", "all", "记录调用超过预算", summary.ID+"/call_budget", item)
			}
		}
		if summary.Evidence.Status != "complete" {
			item := occurrence(run, ReqV2CaseResultRow{}, "", "integrity", "incomplete", strings.Join(summary.Evidence.Notes, "；"), 0)
			add("run", "all", "运行证据需核对", summary.ID+"/integrity", item)
		}
	}
	return facts, byName, notes
}

func (s *Store) resolveBadCaseReference(ref BadCaseReference, kind string, runs map[string]*savedReqV2Run) []BadCaseOccurrence {
	run := runs[ref.RunName]
	missing := BadCaseOccurrence{ID: badCaseID(ref.RunName, ref.Layer, ref.Case, ref.Assertion, ref.Metric, fmt.Sprint(ref.Repeat), ref.Context), RunName: ref.RunName, Layer: ref.Layer, Case: ref.Case, Assertion: ref.Assertion, Metric: ref.Metric, Repeat: ref.Repeat, Turn: ref.Turn, Context: ref.Context, State: "missing", Detail: "准确引用未找到；不从其他运行补填。"}
	if run == nil {
		return []BadCaseOccurrence{missing}
	}
	missing.Run = &run.summary
	if kind == "run" {
		for _, gate := range run.detail.GateVerdicts {
			if gate.Metric == ref.Metric && (ref.Layer == "" || ref.Layer == gate.Layer) {
				missing.State = "failed"
				if !gate.Evaluable {
					missing.State = "unevaluable"
				} else if gate.Passed {
					missing.State = "passed"
				}
				missing.Detail = gate.Actual + " / 要求 " + gate.Threshold
				return []BadCaseOccurrence{missing}
			}
		}
		var usage struct {
			ModelCalls     *int           `json:"model_calls"`
			ProviderErrors map[string]int `json:"provider_errors"`
		}
		if json.Unmarshal(run.detail.Usage, &usage) == nil {
			count, known := 0, false
			switch ref.Metric {
			case "provider_errors":
				known = usage.ProviderErrors != nil
				for _, n := range usage.ProviderErrors {
					if n > 0 {
						count += n
					}
				}
				missing.Detail = fmt.Sprintf("冻结用量记录 %d 次 provider 失败。", count)
			case "call_budget":
				known = usage.ModelCalls != nil && run.summary.MaxCalls > 0
				if known {
					count = *usage.ModelCalls - run.summary.MaxCalls
					missing.Detail = fmt.Sprintf("记录调用 %d 次 / 预算 %d 次；冻结门槛保持原样。", *usage.ModelCalls, run.summary.MaxCalls)
				}
			case "integrity":
				known = true
				missing.Detail = "运行证据：" + run.summary.Evidence.Status + "；" + strings.Join(run.summary.Evidence.Notes, "；")
				if run.summary.Evidence.Status != "complete" {
					missing.State = "incomplete"
					return []BadCaseOccurrence{missing}
				}
			}
			if known {
				missing.State = "passed"
				if count > 0 {
					missing.State = "failed"
				}
				return []BadCaseOccurrence{missing}
			}
		}
		return []BadCaseOccurrence{missing}
	}
	data, err := s.caseReqV2(run, ref.Layer, ref.Case)
	if err != nil {
		return []BadCaseOccurrence{missing}
	}
	var resolved []BadCaseOccurrence
	for _, repeat := range data.Repeats {
		if ref.Repeat != 0 && repeat.Repeat != ref.Repeat {
			continue
		}
		item := missing
		item.ID = badCaseID(missing.ID, fmt.Sprint(repeat.Repeat))
		item.Repeat = repeat.Repeat
		item.State, item.Detail = "failed", "题目未通过"
		if repeat.Pass && len(repeat.Vetoes) == 0 {
			item.State, item.Detail = "passed", "题目通过"
		}
		if strings.HasPrefix(ref.Assertion, "veto:") {
			item.State, item.Detail = "passed", "未触发所引用的否决项"
			for _, veto := range repeat.Vetoes {
				if "veto:"+veto == ref.Assertion {
					item.State, item.Detail = "failed", "否决项："+veto
				}
			}
		} else if ref.Assertion != "" {
			found := false
			for _, a := range repeat.Assertions {
				if a.Name == ref.Assertion {
					found = true
					item.State, item.Detail = "passed", firstNonEmpty(a.Detail, a.Name)
					if !a.Pass {
						item.State = "failed"
						break
					}
				}
			}
			if !found {
				item.State, item.Detail = "missing", "所引用的断言未记录。"
			}
		}
		if repeat.Skipped != "" {
			item.State, item.Detail = "skipped", repeat.Skipped
		}
		if ref.Turn > len(repeat.Turns) {
			item.State, item.Detail = "missing", "所引用的轮次未记录。"
		}
		resolved = append(resolved, item)
	}
	if len(resolved) == 0 {
		return []BadCaseOccurrence{missing}
	}
	return resolved
}

func finishBadCase(entry *BadCaseSummary) {
	states, runs := map[string]bool{}, map[string]bool{}
	entry.States, entry.RunIDs = []string{}, []string{}
	for _, item := range entry.Occurrences {
		states[item.State] = true
		if item.Run != nil {
			runs[item.Run.ID] = true
			if item.State != "passed" && deref(item.Run.CreatedAt) > entry.LastSeen {
				entry.LastSeen = deref(item.Run.CreatedAt)
			}
		}
	}
	for state := range states {
		entry.States = append(entry.States, state)
	}
	for id := range runs {
		entry.RunIDs = append(entry.RunIDs, id)
	}
	sort.Strings(entry.States)
	sort.Strings(entry.RunIDs)
	sort.SliceStable(entry.Occurrences, func(i, j int) bool {
		a, b := entry.Occurrences[i], entry.Occurrences[j]
		at, bt := "", ""
		if a.Run != nil {
			at = deref(a.Run.CreatedAt)
		}
		if b.Run != nil {
			bt = deref(b.Run.CreatedAt)
		}
		if at != bt {
			return at > bt
		}
		if a.RunName != b.RunName {
			return a.RunName < b.RunName
		}
		if a.Repeat != b.Repeat {
			return a.Repeat < b.Repeat
		}
		return a.ID < b.ID
	})
}

func (s *Store) buildBadCases() (BadCaseCatalog, map[string]BadCaseReview, map[string]*savedReqV2Run) {
	facts, runs, notes := s.badCaseFacts()
	index, note := s.badCaseIndex()
	if note != "" {
		notes = append(notes, note)
	}
	byID := map[string]BadCaseReview{}
	for _, review := range index.Reviews {
		byID[review.ID] = review
		entry := &BadCaseSummary{ID: review.ID, Kind: review.Kind, Side: review.Side, Title: review.Title, Summary: review.Summary, Status: review.Status, Occurrences: []BadCaseOccurrence{}, Commits: []string{}}
		for _, fix := range review.Fixes {
			entry.Commits = append(entry.Commits, fix.Commit)
		}
		for _, ref := range review.References {
			entry.Occurrences = append(entry.Occurrences, s.resolveBadCaseReference(ref, review.Kind, runs)...)
		}
		if review.Regression != nil {
			entry.Occurrences = append(entry.Occurrences, s.resolveBadCaseReference(*review.Regression, review.Kind, runs)...)
		}
		// 只收起被准确审阅的断言；同题其他失败保留待分析入口。
		for id, automatic := range facts {
			if _, reviewed := byID[id]; reviewed {
				continue
			}
			remaining := automatic.Occurrences[:0]
			for _, item := range automatic.Occurrences {
				matched := false
				for _, ref := range review.References {
					matched = matched || (ref.RunName == item.RunName && ref.Layer == item.Layer && ref.Case == item.Case && ref.Assertion == item.Assertion && ref.Metric == item.Metric && (ref.Repeat == 0 || ref.Repeat == item.Repeat))
				}
				if !matched {
					remaining = append(remaining, item)
				}
			}
			if len(remaining) == 0 {
				delete(facts, id)
			} else {
				automatic.Occurrences = remaining
			}
		}
		if review.Status == "verified" {
			verified := false
			for _, fix := range review.Fixes {
				for _, pair := range fix.Validations {
					checked := s.verifyBadCase(pair, runs).Checked
					verified = verified || checked
					if checked && pair.ValidationKind == "historical" {
						entry.ValidationKind = "historical"
					}
				}
			}
			if !verified {
				entry.Status = "pending"
			}
		}
		if review.Status == "recurred" {
			entry.Status = "pending"
			if review.Regression != nil {
				for _, item := range s.resolveBadCaseReference(*review.Regression, review.Kind, runs) {
					if item.State == "failed" && item.Run != nil && item.Run.Evidence.Status == "complete" {
						entry.Status = "recurred"
					}
				}
			}
		}
		facts[entry.ID] = entry
	}
	response := BadCaseCatalog{Cases: []BadCaseSummary{}, Notes: notes, Reference: index.Reference}
	annotate := s.badCaseReferenceAnnotator(index.Reference, runs, &response.Notes)
	for _, entry := range facts {
		finishBadCase(entry)
		annotate(entry)
		entry.Category, entry.Conclusion = badCaseDisposition(*entry)
		response.Cases = append(response.Cases, *entry)
	}
	sort.Slice(response.Cases, func(i, j int) bool {
		if response.Cases[i].LastSeen != response.Cases[j].LastSeen {
			return response.Cases[i].LastSeen > response.Cases[j].LastSeen
		}
		return response.Cases[i].ID < response.Cases[j].ID
	})
	response.Groups = groupBadCases(response.Cases)
	return response, byID, runs
}

func badCaseDisposition(item BadCaseSummary) (string, string) {
	switch item.Status {
	case "verified":
		if item.ValidationKind == "historical" {
			return "verified", "历史修复与验收已核对"
		}
		return "verified", "修复验证已确认"
	case "pending":
		return "open", "修复验证待补齐"
	case "located":
		return "open", "根因已定位，待修复验证"
	case "recurred":
		return "open", "验收候选再次失败"
	}
	if item.Reference != nil {
		if item.Reference.State == "passed" {
			return "history", "验收参考已通过"
		}
		if item.Reference.State == "failed" {
			return "open", "验收参考仍有失败"
		}
	}
	notEvaluated := len(item.States) > 0
	for _, state := range item.States {
		notEvaluated = notEvaluated && (state == "skipped" || state == "unevaluable")
	}
	if notEvaluated {
		return "history", "该批次未评估此项"
	}
	for _, state := range item.States {
		if state == "incomplete" || state == "missing" {
			return "gaps", "冻结证据待核对"
		}
	}
	return "gaps", "缺少同版本验收证据"
}

func groupBadCases(cases []BadCaseSummary) []BadCaseGroup {
	groups := map[string]*BadCaseGroup{}
	versions := map[string]map[string]bool{}
	for _, item := range cases {
		if len(item.Occurrences) == 0 {
			continue
		}
		first := item.Occurrences[0]
		identity := []string{item.Category, item.Kind, first.Layer, first.Case, first.Metric}
		// 缺题号的证据不能因空身份归为同一道题。
		if item.Kind == "question" && first.Case == "" {
			identity = append(identity, item.ID)
		}
		id := "group-" + strings.TrimPrefix(badCaseID(identity...), "fact-")
		group := groups[id]
		if group == nil {
			group = &BadCaseGroup{ID: id, Category: item.Category, Kind: item.Kind, Side: item.Side, Layer: first.Layer, Case: first.Case, Metric: first.Metric, Title: item.Title, Conclusion: item.Conclusion, Members: []string{}, RunIDs: []string{}}
			groups[id], versions[id] = group, map[string]bool{}
		}
		group.Members = append(group.Members, item.ID)
		if item.Conclusion != group.Conclusion {
			if item.Category == "history" && (item.Conclusion == "验收参考已通过" || group.Conclusion == "验收参考已通过") {
				group.Conclusion = "验收参考已通过"
			} else {
				group.Conclusion = map[string]string{"open": "修复或验证待处理", "verified": "修复验证已确认", "history": "历史与探索记录", "gaps": "验收证据待补齐"}[item.Category]
			}
		}
		if item.LastSeen > group.LastSeen {
			group.LastSeen = item.LastSeen
		}
		for _, occurrence := range item.Occurrences {
			if occurrence.Run == nil {
				continue
			}
			group.RunIDs = appendUnique(group.RunIDs, occurrence.Run.ID)
			version := occurrence.ManifestSHA
			if version == "" {
				version = occurrence.Run.ManifestSHA
			}
			if version == "" || occurrence.Run.Evidence.Status == "invalid" {
				version = occurrence.Run.ID
			}
			versions[id][version] = true
		}
	}
	result := []BadCaseGroup{}
	for id, group := range groups {
		group.Versions = len(versions[id])
		sort.Strings(group.RunIDs)
		result = append(result, *group)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].LastSeen != result[j].LastSeen {
			return result[i].LastSeen > result[j].LastSeen
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func (s *Store) badCaseReferenceAnnotator(ref *BadCaseReferenceRun, runs map[string]*savedReqV2Run, notes *[]string) func(*BadCaseSummary) {
	noop := func(*BadCaseSummary) {}
	if ref == nil {
		return noop
	}
	target := runs[ref.RunName]
	if target == nil {
		*notes = append(*notes, "声明的验收参考运行未找到；参考结果未知。")
		return noop
	}
	ref.Run = &target.summary
	// 每个来源运行只复用一次既有冻结身份比较，每道参考题只读取一次。
	matched := map[string]bool{}
	for name, run := range runs {
		comparison, err := s.compareReqV2Runs(run, target)
		matched[name] = err == nil && comparison.Strict
	}
	cases := map[string]*ReqV2CaseDetail{}
	return func(entry *BadCaseSummary) {
		if len(entry.Occurrences) == 0 {
			return
		}
		first := entry.Occurrences[0]
		result := &BadCaseReferenceResult{RunID: target.summary.ID, Layer: first.Layer, Case: first.Case, State: "unmatched", MatchedRunIDs: []string{}, Note: "冻结身份不同或证据不完整，不能用参考通过记录覆盖此失败。"}
		entry.Reference = result
		for _, item := range entry.Occurrences {
			if item.Layer == first.Layer && item.Case == first.Case && matched[item.RunName] && item.Run != nil {
				result.MatchedRunIDs = appendUnique(result.MatchedRunIDs, item.Run.ID)
			}
		}
		if len(result.MatchedRunIDs) == 0 {
			return
		}
		if entry.Kind == "run" {
			rows := s.resolveBadCaseReference(BadCaseReference{RunName: ref.RunName, Layer: first.Layer, Metric: first.Metric}, "run", runs)
			if len(rows) == 1 && (rows[0].State == "passed" || rows[0].State == "failed") {
				result.State, result.Total = rows[0].State, 1
				if result.State == "passed" {
					result.Passed = 1
				}
				result.Note = "同冻结身份的验收参考门槛记录；不同模型或代码不推断修复因果。"
			} else {
				result.State, result.Note = "incomplete", "验收参考未记录此运行指标或无法评估。"
			}
			return
		}
		key := first.Layer + "/" + first.Case
		detail, ok := cases[key]
		if !ok {
			detail, _ = s.caseReqV2(target, first.Layer, first.Case)
			cases[key] = detail
		}
		result.State, result.Note = "incomplete", "验收参考缺少完整题目、断言或重复证据。"
		if detail == nil || detail.Frozen == nil || detail.Integrity.Status != "complete" || len(detail.Repeats) != target.summary.Repeats || target.summary.Repeats < 1 {
			return
		}
		seen := map[int]bool{}
		for _, repeat := range detail.Repeats {
			if repeat.Repeat < 1 || repeat.Repeat > target.summary.Repeats || seen[repeat.Repeat] || repeat.Skipped != "" || len(repeat.Assertions) == 0 {
				return
			}
			seen[repeat.Repeat] = true
			pass := repeat.Pass && repeat.Error == "" && len(repeat.Vetoes) == 0
			for _, assertion := range repeat.Assertions {
				pass = pass && assertion.Pass
			}
			if pass {
				result.Passed++
			}
		}
		result.Total = len(detail.Repeats)
		result.State = "failed"
		if result.Passed == result.Total {
			result.State = "passed"
		}
		result.Note = "同一冻结题目与评分身份的验收参考记录；模型和代码可能不同，不推断修复因果，不代表今日代码已复测。"
	}
}

func (s *Store) BadCases() BadCaseCatalog {
	response, _, _ := s.buildBadCases()
	return response
}

func (s *Store) verifyBadCase(pair BadCaseValidation, runs map[string]*savedReqV2Run) BadCaseVerification {
	result := BadCaseVerification{BadCaseValidation: pair, Notes: []string{}, BaselineGates: []ReqV2GateVerdictRow{}, CandidateGates: []ReqV2GateVerdictRow{}, Regressed: []string{}}
	a, b := runs[pair.Baseline], runs[pair.Candidate]
	if a == nil || b == nil {
		result.Notes = append(result.Notes, "验证引用的准确运行未找到；结论尚未核对。")
		return result
	}
	result.BaselineRun, result.CandidateRun = &a.summary, &b.summary
	result.BaselineGates, result.CandidateGates = a.detail.GateVerdicts, b.detail.GateVerdicts
	comparison, err := s.compareReqV2Runs(a, b)
	if err != nil {
		result.Notes = append(result.Notes, "运行对比读取失败。")
	} else {
		result.Comparison = comparison
	}
	readModels := func(run *savedReqV2Run) map[string]any {
		raw, err := s.read(run.dir, "plan.json")
		var plan struct {
			Models map[string]any `json:"models"`
		}
		if err != nil || json.Unmarshal(raw, &plan) != nil {
			return nil
		}
		return plan.Models
	}
	ma, mb := readModels(a), readModels(b)
	// 完整配置仅内部核对，不返回 host、凭据或供应商选项；不能用展示摘要代替身份。
	result.ModelSame = a.summary.ZeroModel && b.summary.ZeroModel || len(ma) > 0 && len(mb) > 0 && reflect.DeepEqual(ma, mb)
	if !result.ModelSame {
		result.Notes = append(result.Notes, "模型配置不同或未记录；只展示观察，不归因于修复。")
	}
	if a.summary.CodeDirty == nil || b.summary.CodeDirty == nil || *a.summary.CodeDirty || *b.summary.CodeDirty {
		result.Notes = append(result.Notes, "运行工作区含未提交修改或身份未知，不能仅凭提交还原。")
	}
	if comparison != nil && comparison.Strict {
		baseline := map[string]ReqV2CaseIndexEntry{}
		for _, row := range a.detail.CaseIndex {
			baseline[row.Layer+"/"+row.ID] = row
		}
		for _, row := range b.detail.CaseIndex {
			key := row.Layer + "/" + row.ID
			if prior, ok := baseline[key]; ok && prior.PassK && !prior.Skipped && !row.PassK && !row.Skipped {
				result.Regressed = append(result.Regressed, key)
			}
		}
		sort.Strings(result.Regressed)
	}
	hasHoldout := false
	for _, split := range b.summary.Splits {
		hasHoldout = hasHoldout || split == "holdout"
	}
	if hasHoldout {
		result.Notes = append(result.Notes, "本页已展示保留集证据；针对已暴露题目的修复不能作为独立泛化证明。")
	} else {
		result.Notes = append(result.Notes, "未完成独立保留集验证。")
	}
	if pair.Case != "" {
		result.BaselineCase, _ = s.caseReqV2(a, pair.Layer, pair.Case)
		result.CandidateCase, _ = s.caseReqV2(b, pair.Layer, pair.Case)
		candidate := result.CandidateCase
		expectedRepeats := b.summary.Repeats
		applicable := candidate != nil && len(candidate.Repeats) == expectedRepeats && candidate.Frozen != nil && result.BaselineCase != nil && result.BaselineCase.Frozen != nil
		if candidate != nil {
			seen := map[int]bool{}
			for _, row := range candidate.Repeats {
				applicable = applicable && row.Repeat >= 1 && row.Repeat <= expectedRepeats && !seen[row.Repeat] && row.Pass && row.Skipped == "" && row.Error == "" && len(row.Vetoes) == 0 && len(row.Assertions) > 0
				seen[row.Repeat] = true
				for _, assertion := range row.Assertions {
					applicable = applicable && assertion.Pass
				}
			}
		}
		result.Checked = pair.Accepted && applicable && comparison != nil && comparison.Strict && result.ModelSame && a.summary.CodeDirty != nil && b.summary.CodeDirty != nil && !*a.summary.CodeDirty && !*b.summary.CodeDirty
		if pair.ValidationKind == "historical" {
			// 历史核对只确认已登记的修复在冻结候选中满足原题合同。
			// 不绕过判卷/金标身份，不将重判的旧观测称为同模型配对实验。
			failed := false
			if result.BaselineCase != nil {
				for _, row := range result.BaselineCase.Repeats {
					failed = failed || !row.Pass && row.Skipped == ""
				}
			}
			var source struct {
				Run       string `json:"source_run"`
				ReportSHA string `json:"source_report_sha256"`
			}
			plan, _ := s.read(a.dir, "plan.json")
			_ = json.Unmarshal(plan, &source)
			sourceValid := false
			if prior := runs[filepath.Base(source.Run)]; prior != nil && source.Run == "artifacts/reqv2/"+prior.summary.DirName {
				raw, err := s.read(prior.dir, "report.json")
				sourceValid = err == nil && source.ReportSHA == fmt.Sprintf("%x", sha256.Sum256(raw))
			}
			result.Checked = pair.Accepted && applicable && failed && sourceValid && a.summary.Mode == "regrade" && a.summary.Evidence.Status == "complete" && b.summary.Evidence.Status == "complete" && a.manifestSHA != "" && a.manifestSHA == b.manifestSHA && a.gatesSHA == b.gatesSHA && a.summary.Grader == b.summary.Grader && reflect.DeepEqual(result.BaselineCase.Frozen, candidate.Frozen) && b.summary.Mode == "live" && b.summary.CodeDirty != nil && !*b.summary.CodeDirty && fullCommit(b.summary.CodeCommit)
			if !sourceValid {
				result.Notes = append(result.Notes, "重判来源报告缺失或完整 SHA-256 不符；不确认历史修复。")
			}
			result.Notes = append(result.Notes, "历史修复核对：旧观测已在候选的同一冻结题目、判卷与门槛下重判；候选覆盖全部适用重复。此结论不表示同模型配对改善、单因素因果或今日代码复测。")
		}
		if !applicable {
			result.Notes = append(result.Notes, "目标案例缺失、未覆盖全部适用重复或仍有失败；未确认该案例验证通过。")
		}
	} else {
		baseline := s.resolveBadCaseReference(BadCaseReference{RunName: pair.Baseline, Layer: pair.Layer, Metric: pair.Metric}, "run", runs)
		candidate := s.resolveBadCaseReference(BadCaseReference{RunName: pair.Candidate, Layer: pair.Layer, Metric: pair.Metric}, "run", runs)
		result.Checked = pair.Accepted && len(baseline) == 1 && baseline[0].State != "missing" && len(candidate) == 1 && candidate[0].State == "passed" && comparison != nil && comparison.Strict && result.ModelSame && a.summary.CodeDirty != nil && b.summary.CodeDirty != nil && !*a.summary.CodeDirty && !*b.summary.CodeDirty
		result.Notes = append(result.Notes, "仅核对所选运行级 metric，整批结论仍以冻结总门槛为准。")
	}
	return result
}

func (s *Store) BadCase(id string) (*BadCaseDetail, error) {
	if !badCaseIDPattern.MatchString(id) {
		return nil, errNotFound
	}
	catalog, reviews, runs := s.buildBadCases()
	for _, entry := range catalog.Cases {
		if entry.ID != id {
			continue
		}
		detail := &BadCaseDetail{BadCaseSummary: entry, Verifications: []BadCaseVerification{}, Notes: catalog.Notes}
		if review, exists := reviews[id]; exists {
			detail.Review = &review
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, fix := range review.Fixes {
				if _, err := s.promptGit(ctx, "cat-file", "-e", fix.Commit+"^{commit}"); err != nil {
					detail.Notes = append(detail.Notes, "修复提交 "+fix.Commit+" 本机不可核对；不从当前源码补填。")
				}
				for _, pair := range fix.Validations {
					detail.Verifications = append(detail.Verifications, s.verifyBadCase(pair, runs))
				}
			}
		}
		return detail, nil
	}
	return nil, errNotFound
}
