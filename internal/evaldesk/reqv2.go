package evaldesk

// Requirement v2 运行的只读发现、完整性校验与安全投影。
// 与旧 evalsuite DTO 分离;不重判、不改分,完整性状态不等于正确性。
// 只接受固定文件名(plan/report/results/manifest/gates/各层 cases.json),
// 拒绝符号链接、越出产物根目录与超大小文件。
import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
	"github.com/subaru-ye/pc-builder-agent/internal/planningeval"
)

const reqV2Dir = "reqv2"

// 长文本投影上限:主阅读区优先结构化差异,超长原文放入前端次级展开区。
const (
	reqV2MaxText      = 4000
	reqV2MaxValueJSON = 600
)

type ReqV2ModelIdentity struct {
	Role            string `json:"role"`
	Model           string `json:"model"`
	Provider        string `json:"provider,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	Timeout         string `json:"timeout,omitempty"`
	SessionCache    *bool  `json:"session_cache,omitempty"`
}

type ReqV2Evidence struct {
	Status string   `json:"status"` // complete | incomplete | invalid
	Notes  []string `json:"notes"`
}

type ReqV2RunSummary struct {
	ID          string                   `json:"id"`
	DirName     string                   `json:"dir_name"`
	Label       string                   `json:"label"`
	CreatedAt   *string                  `json:"created_at"`
	Mode        string                   `json:"mode"` // live | deterministic | replay | regrade | unknown
	ZeroModel   bool                     `json:"zero_model"`
	Regrade     bool                     `json:"regrade"`
	SourceRun   string                   `json:"source_run,omitempty"`
	Superseded  bool                     `json:"superseded"`
	Grader      string                   `json:"grader_version"`
	Splits      []string                 `json:"splits"`
	Repeats     int                      `json:"repeats"`
	CodeCommit  string                   `json:"code_commit,omitempty"`
	CodeDirty   *bool                    `json:"code_dirty"`
	Models      []ReqV2ModelIdentity     `json:"models"`
	GatePassed  *bool                    `json:"gate_passed"`
	Conclusion  string                   `json:"conclusion"`
	Evidence    ReqV2Evidence            `json:"evidence"`
	ManifestSHA string                   `json:"manifest_sha256,omitempty"`
	GatesSHA    string                   `json:"gates_sha256,omitempty"`
	MaxCalls    int                      `json:"max_model_requests,omitempty"`
	ScoreNote   string                   `json:"score_note,omitempty"`
	PlanNote    string                   `json:"plan_note,omitempty"`
	PromptSHA   string                   `json:"prompt_sha256,omitempty"`
	LayerScores map[string]ReqV2LayerRow `json:"layer_scores,omitempty"`
	FailedGates []ReqV2GateVerdictRow    `json:"failed_gates,omitempty"`
}

type ReqV2GateVerdictRow struct {
	Layer     string `json:"layer"`
	Metric    string `json:"metric"`
	Actual    string `json:"actual"`
	Threshold string `json:"threshold"`
	Passed    bool   `json:"passed"`
	Evaluable bool   `json:"evaluable"`
	Note      string `json:"note,omitempty"`
}

type ReqV2LayerRow struct {
	Cases    int            `json:"cases"`
	Passed   int            `json:"passed"`
	Skipped  int            `json:"skipped"`
	Vetoes   int            `json:"vetoes"`
	Failures map[string]int `json:"failure_classifications"`
}

type ReqV2CaseIndexEntry struct {
	Layer    string   `json:"layer"`
	ID       string   `json:"id"`
	Split    string   `json:"split"`
	Session  string   `json:"session"`
	Repeats  []int    `json:"repeats"`
	PassK    bool     `json:"pass_k"`
	Skipped  bool     `json:"skipped"`
	Vetoes   int      `json:"vetoes"`
	Failures []string `json:"failures"`
}

type ReqV2RunDetail struct {
	ReqV2RunSummary
	DurationMS   int64                      `json:"duration_ms"`
	GateVerdicts []ReqV2GateVerdictRow      `json:"gate_verdicts"`
	PerLayer     map[string]ReqV2LayerRow   `json:"per_layer"`
	ModelQuality map[string]json.RawMessage `json:"model_quality"`
	Usage        json.RawMessage            `json:"usage"`
	Limitations  []string                   `json:"limitations"`
	CaseIndex    []ReqV2CaseIndexEntry      `json:"cases"`
	RegradePlan  json.RawMessage            `json:"regrade_plan,omitempty"`
	Integrity    []ReqV2IntegrityCheck      `json:"integrity_checks"`
	Thresholds   json.RawMessage            `json:"gate_thresholds,omitempty"`
	Manifest     *ReqV2Manifest             `json:"manifest,omitempty"`
}

// ReqV2Manifest 投影冻结评估集清单:每层题数/会话数/哈希与 split 分布。
type ReqV2Manifest struct {
	Dataset  string             `json:"dataset"`
	FrozenAt string             `json:"frozen_at"`
	Grader   string             `json:"grader_version"`
	Files    []ReqV2ManifestRow `json:"files"`
	Splits   []ReqV2SplitRow    `json:"splits"`
}

type ReqV2ManifestRow struct {
	Layer    string `json:"layer"`
	Cases    int    `json:"cases"`
	Sessions int    `json:"sessions"`
	SHA256   string `json:"sha256"`
}

type ReqV2SplitRow struct {
	Split    string `json:"split"`
	Sessions int    `json:"sessions"`
	Used     bool   `json:"used"`
}

type ReqV2IntegrityCheck struct {
	Check  string `json:"check"`
	State  string `json:"state"` // ok | mismatch | missing | invalid | incomplete
	Detail string `json:"detail,omitempty"`
}

type ReqV2AssertionRow struct {
	Name           string `json:"name"`
	Pass           bool   `json:"pass"`
	Detail         string `json:"detail,omitempty"`
	Classification string `json:"classification,omitempty"`
}

type ReqV2TurnRow struct {
	Index       int               `json:"index"`
	Reply       string            `json:"reply,omitempty"`
	Operations  []json.RawMessage `json:"operations,omitempty"`
	TurnSignals []string          `json:"turn_signals,omitempty"`
	DurationMS  int64             `json:"duration_ms,omitempty"`
	ModelCalled bool              `json:"screen_model_called"`
}

type ReqV2RepeatEvidence struct {
	Repeat      int                 `json:"repeat"`
	Pass        bool                `json:"pass"`
	Skipped     string              `json:"skipped,omitempty"`
	Error       string              `json:"error,omitempty"`
	Vetoes      []string            `json:"vetoes,omitempty"`
	Assertions  []ReqV2AssertionRow `json:"assertions"`
	Turns       []ReqV2TurnRow      `json:"turns,omitempty"`
	Observation json.RawMessage     `json:"observation_rest,omitempty"`
}

type ReqV2FrozenCase struct {
	Layer      string         `json:"layer"`
	ID         string         `json:"id"`
	Title      string         `json:"title,omitempty"`
	Split      string         `json:"split,omitempty"`
	Session    string         `json:"session,omitempty"`
	Rationale  string         `json:"rationale,omitempty"`
	Fields     map[string]any `json:"fields"`
	SHA256     string         `json:"sha256"`
	ContentSHA string         `json:"content_sha256"`
}

type ReqV2Dataset struct {
	Cases []ReqV2FrozenCase `json:"cases"`
	Notes []string          `json:"notes"`
}

type ReqV2CaseDetail struct {
	Run        string                `json:"run"`
	Layer      string                `json:"layer"`
	Case       string                `json:"case"`
	Split      string                `json:"split"`
	Session    string                `json:"session"`
	PassK      bool                  `json:"pass_k"`
	Repeats    []ReqV2RepeatEvidence `json:"repeats"`
	Frozen     *ReqV2FrozenCase      `json:"frozen,omitempty"`
	FrozenNote string                `json:"frozen_note,omitempty"`
	Integrity  ReqV2Evidence         `json:"integrity"`
}

type ReqV2RunsResponse struct {
	Runs     []ReqV2RunSummary `json:"runs"`
	Warnings []string          `json:"warnings"`
}

// ---- plan / report 的最小解码结构 ----

type reqV2PlanIdentity struct {
	Mode             string                        `json:"mode"`
	CreatedAt        string                        `json:"created_at"`
	GraderVersion    string                        `json:"grader_version"`
	ManifestSHA256   string                        `json:"manifest_sha256"`
	GatesSHA256      string                        `json:"gates_sha256"`
	Splits           []string                      `json:"splits"`
	Repeats          int                           `json:"repeats"`
	CodeCommit       string                        `json:"code_commit"`
	CodeDirty        *bool                         `json:"code_dirty"`
	MaxModelRequests int                           `json:"max_model_requests"`
	ZeroModel        bool                          `json:"zero_model"`
	Regrade          bool                          `json:"regrade"`
	SourceRun        string                        `json:"source_run"`
	SourceGrader     string                        `json:"source_grader_version"`
	SourceManifest   string                        `json:"source_manifest_sha256"`
	SourceReport     string                        `json:"source_report_sha256"`
	Models           map[string]reqV2PlanModelSpec `json:"models"`
	Note             string                        `json:"note"`
	Prompts          *evalsuite.PromptIdentity     `json:"prompts,omitempty"`
}

type reqV2PlanModelSpec struct {
	Model           string `json:"model"`
	Provider        string `json:"provider"`
	ReasoningEffort string `json:"reasoning_effort"`
	Timeout         string `json:"timeout"`
	SessionCache    bool   `json:"session_cache"`
}

type reqV2ReportHead struct {
	SchemaVersion    int                        `json:"schema_version"`
	Mode             string                     `json:"mode"`
	GraderVersion    string                     `json:"grader_version"`
	ManifestSHA256   string                     `json:"manifest_sha256"`
	GatesSHA256      string                     `json:"gates_sha256"`
	Splits           []string                   `json:"splits"`
	Repeats          int                        `json:"repeats"`
	GatePassed       *bool                      `json:"gate_passed"`
	Conclusion       string                     `json:"conclusion"`
	PerLayer         map[string]ReqV2LayerRow   `json:"per_layer"`
	ModelQuality     map[string]json.RawMessage `json:"model_quality"`
	GateVerdicts     []ReqV2GateVerdictRow      `json:"gate_verdicts"`
	Cases            []ReqV2CaseResultRow       `json:"cases"`
	Usage            json.RawMessage            `json:"usage"`
	MaxModelRequests int                        `json:"max_model_requests"`
	Limitations      []string                   `json:"limitations"`
	DurationMS       int64                      `json:"duration_ms"`
	Gates            json.RawMessage            `json:"gates"`
}

type ReqV2CaseResultRow struct {
	Layer           string   `json:"layer"`
	ID              string   `json:"id"`
	Split           string   `json:"split"`
	Session         string   `json:"session"`
	Repeat          int      `json:"repeat"`
	Pass            bool     `json:"pass"`
	Vetoes          []string `json:"vetoes,omitempty"`
	Skipped         string   `json:"skipped,omitempty"`
	ObservationMeta struct {
		Skipped string `json:"skipped"`
	} `json:"observation"`
	Assertions []struct {
		Name           string `json:"name"`
		Pass           bool   `json:"pass"`
		Detail         string `json:"detail"`
		Classification string `json:"classification"`
	} `json:"assertions"`
}

type savedReqV2Run struct {
	dir     string
	stamp   string
	summary ReqV2RunSummary
	detail  *ReqV2RunDetail
}

// reqV2Integrity 聚合完整性检查;invalid > mismatch > incomplete > ok。
type reqV2Integrity struct {
	checks []ReqV2IntegrityCheck
	status string
	notes  []string
}

func (ig *reqV2Integrity) record(check, state, detail string) {
	ig.checks = append(ig.checks, ReqV2IntegrityCheck{Check: check, State: state, Detail: detail})
	switch state {
	case "invalid":
		ig.status = "invalid"
		ig.notes = append(ig.notes, detail)
	case "mismatch":
		if ig.status != "invalid" {
			ig.status = "invalid"
		}
		ig.notes = append(ig.notes, detail)
	case "missing", "incomplete":
		if ig.status == "" || ig.status == "complete" {
			ig.status = "incomplete"
		}
		ig.notes = append(ig.notes, detail)
	}
}

func (ig *reqV2Integrity) finalize() ReqV2Evidence {
	status := ig.status
	if status == "" || status == "ok" {
		status = "complete"
	}
	return ReqV2Evidence{Status: status, Notes: append([]string{}, ig.notes...)}
}

// ---- 发现与缓存 ----

func (s *Store) reqV2Root() string { return filepath.Join(s.root, "artifacts", reqV2Dir) }

func (s *Store) reqV2Directories() (map[string]string, []string) {
	dirs := map[string]string{}
	warnings := []string{}
	root := s.reqV2Root()
	st, err := os.Lstat(root)
	if err != nil {
		return dirs, []string{"尚无评估产物，请先生成评估产物后重新读取"}
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return dirs, []string{"已跳过符号链接目录 artifacts/reqv2"}
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || !below(s.root, resolved) {
		return dirs, []string{"已跳过越出项目目录的 artifacts/reqv2"}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return dirs, []string{"artifacts/reqv2 不可读取"}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil || !below(s.root, resolved) {
			warnings = append(warnings, "已跳过链接或不可定位的运行目录 "+entry.Name())
			continue
		}
		// 运行身份要求 plan.json 与 report.json 同时存在;孤立 JSON/日志不冒充运行。
		recognized := true
		for _, name := range []string{"plan.json", "report.json"} {
			st, err := os.Lstat(filepath.Join(dir, name))
			if err != nil || st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
				recognized = false
			}
		}
		if !recognized {
			warnings = append(warnings, "已跳过缺少 plan.json/report.json 的目录 "+entry.Name())
			continue
		}
		rel, err := filepath.Rel(s.root, dir)
		if err != nil || len(dirs) >= 1000 {
			continue
		}
		dirs[reqV2RunID(rel)] = dir
	}
	return dirs, warnings
}

func reqV2RunID(relative string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte("reqv2/"+filepath.ToSlash(relative))))[:24]
}

func (s *Store) reqV2Lookup(id string) (*savedReqV2Run, error) {
	if len(id) != 24 || strings.ContainsAny(id, "/\\.") {
		return nil, errNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dirs, _ := s.reqV2Directories()
	dir, ok := dirs[id]
	if !ok {
		return nil, errNotFound
	}
	return s.loadReqV2(id, dir), nil
}

func (s *Store) reqV2Stamp(dir string) string {
	var stamp strings.Builder
	for _, name := range []string{"plan.json", "report.json", "results.jsonl", "manifest.json", "gates.json", "prompts.json"} {
		path, err := s.safeFile(dir, name)
		if err != nil {
			fmt.Fprintf(&stamp, "%s:missing;", name)
			continue
		}
		if st, err := os.Stat(path); err == nil {
			fmt.Fprintf(&stamp, "%s:%d:%d;", name, st.Size(), st.ModTime().UnixNano())
		}
	}
	return stamp.String()
}

// loadReqV2 要求调用方已持有 s.mu(RunsReqV2 / reqV2Lookup 均如此)。
func (s *Store) loadReqV2(id, dir string) *savedReqV2Run {
	stamp := s.reqV2Stamp(dir)
	if cached := s.cacheReqV2[id]; cached != nil && cached.stamp == stamp {
		return cached
	}
	ig := &reqV2Integrity{}
	plan, report := s.readReqV2PlanReport(dir, ig)
	s.checkReqV2Identity(dir, plan, report, ig)
	if plan.Prompts != nil {
		raw, err := s.read(dir, "prompts.json")
		if err == nil {
			_, err = verifyPromptSnapshot(raw, plan.Prompts)
		}
		if err != nil {
			ig.record("prompts", "invalid", "声明的提示词原文缺失或未通过指纹校验")
		} else {
			ig.record("prompts", "ok", "运行原文与记录指纹一致")
		}
	}
	r := &savedReqV2Run{dir: dir, stamp: stamp}
	r.summary = s.buildReqV2Summary(id, dir, plan, report, ig.finalize(), ig.checks)
	if plan.Prompts != nil {
		r.summary.PromptSHA = plan.Prompts.SHA256
	}
	r.detail = buildReqV2Detail(r.summary, plan, report, ig.checks, s.loadReqV2Manifest(dir, report, ig))
	s.cacheReqV2[id] = r
	return r
}

func strptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (s *Store) RunsReqV2() ReqV2RunsResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	dirs, warnings := s.reqV2Directories()
	loaded := make([]*savedReqV2Run, 0, len(dirs))
	for id, dir := range dirs {
		loaded = append(loaded, s.loadReqV2(id, dir))
	}
	sort.SliceStable(loaded, func(a, b int) bool {
		return deref(loaded[a].summary.CreatedAt)+loaded[a].summary.DirName > deref(loaded[b].summary.CreatedAt)+loaded[b].summary.DirName
	})
	out := ReqV2RunsResponse{Runs: []ReqV2RunSummary{}, Warnings: warnings}
	for _, run := range loaded {
		out.Runs = append(out.Runs, run.summary)
	}
	return out
}

// readReqV2PlanReport 读入并解析两份身份文件;缺失或损坏写入 invalid 证据。
func (s *Store) readReqV2PlanReport(dir string, ig *reqV2Integrity) (*reqV2PlanIdentity, *reqV2ReportHead) {
	var plan *reqV2PlanIdentity
	var report *reqV2ReportHead
	decode := func(name string, target any) bool {
		raw, err := s.read(dir, name)
		if err != nil {
			ig.record(name, "invalid", name+" 缺失或不可读取;此目录不构成完整运行证据")
			return false
		}
		if err := json.Unmarshal(raw, target); err != nil {
			ig.record(name, "invalid", name+" 无法解析(产物损坏)")
			return false
		}
		return true
	}
	planOK := decode("plan.json", &plan)
	reportOK := decode("report.json", &report)
	if !planOK {
		plan = &reqV2PlanIdentity{}
	}
	if !reportOK {
		report = &reqV2ReportHead{}
	}
	return plan, report
}

// checkReqV2Identity 校验计划/报告身份一致与冻结文件哈希。
func (s *Store) checkReqV2Identity(dir string, plan *reqV2PlanIdentity, report *reqV2ReportHead, ig *reqV2Integrity) {
	if plan.CreatedAt == "" && plan.GraderVersion == "" && plan.Mode == "" {
		return // plan 缺失或损坏已在读取时记录
	}
	identity := func(field, pv, rv string) {
		if pv == rv {
			ig.record(field, "ok", "")
			return
		}
		ig.record(field, "mismatch", fmt.Sprintf("%s 在计划与报告之间不一致(plan=%s report=%s)", field, truncateText(pv, 60), truncateText(rv, 60)))
	}
	identity("grader_version", plan.GraderVersion, report.GraderVersion)
	identity("manifest_sha256", plan.ManifestSHA256, report.ManifestSHA256)
	identity("gates_sha256", plan.GatesSHA256, report.GatesSHA256)
	planSplits := append([]string{}, plan.Splits...)
	reportSplits := append([]string{}, report.Splits...)
	sort.Strings(planSplits)
	sort.Strings(reportSplits)
	identity("splits", strings.Join(planSplits, ","), strings.Join(reportSplits, ","))
	if plan.Repeats == report.Repeats {
		ig.record("repeats", "ok", "")
	} else {
		ig.record("repeats", "mismatch", fmt.Sprintf("计划重复次数与报告不一致(plan=%d report=%d)", plan.Repeats, report.Repeats))
	}
	verifyFile := func(name, want string) {
		path, err := s.safeFile(dir, name)
		if err != nil {
			ig.record(name, "missing", name+" 冻结文件缺失:门槛与题库身份无法核验")
			return
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			ig.record(name, "invalid", name+" 读取失败")
			return
		}
		got := fmt.Sprintf("%x", sha256.Sum256(raw))
		if got == want {
			ig.record(name, "ok", "")
			return
		}
		ig.record(name, "mismatch", name+" 实际内容与冻结哈希不一致;运行身份存疑")
	}
	if report.ManifestSHA256 != "" {
		verifyFile("manifest.json", report.ManifestSHA256)
	} else {
		ig.record("manifest.json", "invalid", "报告未记录 manifest 哈希")
	}
	if report.GatesSHA256 != "" {
		verifyFile("gates.json", report.GatesSHA256)
	} else {
		ig.record("gates.json", "invalid", "报告未记录 gates 哈希")
	}
	s.verifyReqV2Results(dir, report, ig)
	verifyReqV2LayerTotals(report, ig)
}

// verifyReqV2Results 校验 results.jsonl 的 (layer,id,repeat) 集合:
// 重复→invalid;缺失/多余→incomplete(缺证据不计作通过)。
func (s *Store) verifyReqV2Results(dir string, report *reqV2ReportHead, ig *reqV2Integrity) {
	path, err := s.safeFile(dir, "results.jsonl")
	if err != nil {
		ig.record("results.jsonl", "missing", "逐题观测文件缺失;证据不完整,缺记录不计作通过")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		ig.record("results.jsonl", "invalid", "results.jsonl 不可读取")
		return
	}
	defer func() { _ = file.Close() }()
	type key struct {
		layer, id string
		repeat    int
	}
	seen := map[key]int{}
	inResults := map[key]bool{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 65536), 32<<20)
	broken := false
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var row struct {
			Layer  string `json:"layer"`
			ID     string `json:"id"`
			Repeat int    `json:"repeat"`
		}
		if json.Unmarshal([]byte(text), &row) != nil || row.Layer == "" || row.ID == "" {
			broken = true
			continue
		}
		k := key{row.Layer, row.ID, row.Repeat}
		seen[k]++
		inResults[k] = true
	}
	if scanner.Err() != nil {
		ig.record("results.jsonl", "invalid", "results.jsonl 读取中断")
	}
	if broken {
		ig.record("results.jsonl", "invalid", "results.jsonl 存在无法解析的损坏记录")
	}
	duplicates := 0
	for _, count := range seen {
		if count > 1 {
			duplicates++
		}
	}
	if duplicates > 0 {
		ig.record("results.jsonl", "invalid", fmt.Sprintf("results.jsonl 存在 %d 组重复 (layer,id,repeat) 记录", duplicates))
	}
	reportKeys := map[key]bool{}
	for _, row := range report.Cases {
		reportKeys[key{row.Layer, row.ID, row.Repeat}] = true
	}
	missing, extra := 0, 0
	for k := range reportKeys {
		if !inResults[k] {
			missing++
		}
	}
	for k := range inResults {
		if !reportKeys[k] {
			extra++
		}
	}
	if missing > 0 || extra > 0 {
		ig.record("results_vs_report", "incomplete", fmt.Sprintf("results.jsonl 与报告逐题集合不一致:缺 %d 条、多 %d 条;缺失证据不计作通过", missing, extra))
	}
}

// verifyReqV2LayerTotals 交叉核对报告汇总与逐题记录;只提示不一致,保留报告原值。
func verifyReqV2LayerTotals(report *reqV2ReportHead, ig *reqV2Integrity) {
	folded := map[string]map[string]bool{}
	for _, row := range report.Cases {
		if row.Skipped != "" || row.ObservationMeta.Skipped != "" {
			continue
		}
		if folded[row.Layer] == nil {
			folded[row.Layer] = map[string]bool{}
		}
		passed, seen := folded[row.Layer][row.ID]
		folded[row.Layer][row.ID] = (!seen || passed) && row.Pass
	}
	for name, summary := range report.PerLayer {
		cases := folded[name]
		passed := 0
		for _, pass := range cases {
			if pass {
				passed++
			}
		}
		if summary.Cases != len(cases) || summary.Passed != passed {
			ig.record("per_layer/"+name, "incomplete", fmt.Sprintf("层 %s 报告汇总与逐题 Pass^k 不一致(报告 %d/%d,逐题 %d/%d);保留报告原值", name, summary.Cases, summary.Passed, len(cases), passed))
		}
	}
}

// ---- 投影 ----

func (s *Store) buildReqV2Summary(id, dir string, plan *reqV2PlanIdentity, report *reqV2ReportHead, evidence ReqV2Evidence, checks []ReqV2IntegrityCheck) ReqV2RunSummary {
	dirName := filepath.Base(dir)
	rel, _ := filepath.Rel(s.root, dir)
	mode, zeroModel := reqV2Mode(plan, report)
	summary := ReqV2RunSummary{
		ID: id, DirName: dirName, Label: filepath.ToSlash(rel), CreatedAt: strptr(plan.CreatedAt),
		Mode: mode, ZeroModel: zeroModel, Regrade: plan.Regrade,
		SourceRun: strings.TrimSpace(plan.SourceRun), Superseded: strings.HasPrefix(dirName, "superseded-"),
		Grader: firstNonEmpty(report.GraderVersion, plan.GraderVersion, "未记录"), Splits: append([]string{}, report.Splits...), Repeats: report.Repeats,
		CodeCommit: plan.CodeCommit, CodeDirty: plan.CodeDirty, Models: []ReqV2ModelIdentity{},
		GatePassed: report.GatePassed, Conclusion: truncateText(report.Conclusion, reqV2MaxText),
		Evidence: evidence, ManifestSHA: shortSHA(report.ManifestSHA256), GatesSHA: shortSHA(report.GatesSHA256),
		MaxCalls: plan.MaxModelRequests,
	}
	for role, spec := range plan.Models {
		if spec.Model == "" {
			continue
		}
		identity := ReqV2ModelIdentity{Role: role, Model: spec.Model, Provider: spec.Provider, ReasoningEffort: spec.ReasoningEffort, Timeout: spec.Timeout}
		if spec.SessionCache {
			identity.SessionCache = &spec.SessionCache
		}
		summary.Models = append(summary.Models, identity)
	}
	summary.ScoreNote = reqV2ScoreNote(report, summary.ZeroModel)
	summary.LayerScores = report.PerLayer
	for _, gate := range report.GateVerdicts {
		if !gate.Passed {
			summary.FailedGates = append(summary.FailedGates, gate)
		}
	}
	summary.PlanNote = truncateText(plan.Note, reqV2MaxText)
	sort.Slice(summary.Models, func(a, b int) bool { return summary.Models[a].Role < summary.Models[b].Role })
	if summary.Superseded {
		summary.Evidence.Notes = append(summary.Evidence.Notes, "superseded 归档运行:历史记录,不作为当前基线")
	}
	if plan.Regrade {
		summary.Evidence.Notes = append(summary.Evidence.Notes, "零模型重判旧观测:复用源运行冻结证据,不是新 live 结果")
	}
	if summary.ZeroModel {
		summary.Evidence.Notes = append(summary.Evidence.Notes, "零模型运行:不显示 provider 成功率或延迟等模型运行指标")
	}
	return summary
}

func buildReqV2Detail(summary ReqV2RunSummary, plan *reqV2PlanIdentity, report *reqV2ReportHead, checks []ReqV2IntegrityCheck, manifest *ReqV2Manifest) *ReqV2RunDetail {
	detail := &ReqV2RunDetail{
		ReqV2RunSummary: summary,
		DurationMS:      report.DurationMS,
		GateVerdicts:    append([]ReqV2GateVerdictRow{}, report.GateVerdicts...),
		PerLayer:        report.PerLayer,
		ModelQuality:    report.ModelQuality,
		Usage:           report.Usage,
		Limitations:     append([]string{}, report.Limitations...),
		CaseIndex:       buildReqV2CaseIndex(report.Cases),
		RegradePlan:     buildReqV2RegradePlan(plan),
		Thresholds:      report.Gates,
		Integrity:       checks,
		Manifest:        manifest,
	}
	if detail.PerLayer == nil {
		detail.PerLayer = map[string]ReqV2LayerRow{}
	}
	if detail.ModelQuality == nil {
		detail.ModelQuality = map[string]json.RawMessage{}
	}
	return detail
}

// reqV2ScoreNote 汇总两侧层跑分为目录摘要;题数为 0 的层不参与合计,
// 零模型运行的模型层单独标注跳过。只做展示汇总,不改变判卷口径。
func reqV2ScoreNote(report *reqV2ReportHead, zeroModel bool) string {
	sum := func(layers ...string) (passed, cases int) {
		for _, layer := range layers {
			row, ok := report.PerLayer[layer]
			if !ok || row.Cases == 0 {
				continue
			}
			passed += row.Passed
			cases += row.Cases
		}
		return passed, cases
	}
	screeningP, screeningT := sum("extraction", "conversations", "reducer", "readiness")
	builderP, builderT := sum("policy", "ui-contract")
	if screeningT == 0 && builderT == 0 {
		return ""
	}
	note := fmt.Sprintf("初筛 %d/%d · 选配 %d/%d", screeningP, screeningT, builderP, builderT)
	if zeroModel {
		note += " · 模型层跳过"
	}
	return note
}

// loadReqV2Manifest 投影已通过哈希核验的 manifest.json;解析失败不阻断运行展示。
func (s *Store) loadReqV2Manifest(dir string, report *reqV2ReportHead, ig *reqV2Integrity) *ReqV2Manifest {
	raw, err := s.read(dir, "manifest.json")
	if err != nil {
		return nil
	}
	var file struct {
		Dataset       string `json:"dataset"`
		FrozenAt      string `json:"frozen_at"`
		GraderVersion string `json:"grader_version"`
		Files         []struct {
			Path     string `json:"path"`
			Cases    int    `json:"cases"`
			Sessions int    `json:"sessions"`
			SHA256   string `json:"sha256"`
		} `json:"files"`
		SplitSessions map[string][]string `json:"split_sessions"`
	}
	if json.Unmarshal(raw, &file) != nil {
		ig.record("manifest.json", "invalid", "manifest.json 无法解析为评估集清单")
		return nil
	}
	manifest := &ReqV2Manifest{Dataset: file.Dataset, FrozenAt: file.FrozenAt, Grader: file.GraderVersion, Files: []ReqV2ManifestRow{}, Splits: []ReqV2SplitRow{}}
	for _, row := range file.Files {
		manifest.Files = append(manifest.Files, ReqV2ManifestRow{Layer: strings.TrimSuffix(row.Path, "/cases.json"), Cases: row.Cases, Sessions: row.Sessions, SHA256: shortSHA(row.SHA256)})
	}
	used := map[string]bool{}
	for _, split := range report.Splits {
		used[split] = true
	}
	splitNames := make([]string, 0, len(file.SplitSessions))
	for split := range file.SplitSessions {
		splitNames = append(splitNames, split)
	}
	sort.Strings(splitNames)
	for _, split := range splitNames {
		manifest.Splits = append(manifest.Splits, ReqV2SplitRow{Split: split, Sessions: len(file.SplitSessions[split]), Used: used[split]})
	}
	return manifest
}

func buildReqV2CaseIndex(cases []ReqV2CaseResultRow) []ReqV2CaseIndexEntry {
	order := []string{}
	index := map[string]*ReqV2CaseIndexEntry{}
	for _, row := range cases {
		key := row.Layer + "/" + row.ID
		entry, ok := index[key]
		if !ok {
			entry = &ReqV2CaseIndexEntry{Layer: row.Layer, ID: row.ID, Split: row.Split, Session: row.Session, PassK: true, Skipped: true, Repeats: []int{}, Failures: []string{}}
			index[key] = entry
			order = append(order, key)
		}
		entry.Repeats = append(entry.Repeats, row.Repeat)
		entry.PassK = entry.PassK && row.Pass
		entry.Skipped = entry.Skipped && (row.Skipped != "" || row.ObservationMeta.Skipped != "")
		entry.Vetoes += len(row.Vetoes)
		for _, assertion := range row.Assertions {
			if !assertion.Pass && assertion.Classification != "" {
				entry.Failures = appendUnique(entry.Failures, assertion.Classification)
			}
		}
	}
	sort.Strings(order)
	out := make([]ReqV2CaseIndexEntry, 0, len(order))
	for _, key := range order {
		entry := index[key]
		sort.Ints(entry.Repeats)
		sort.Strings(entry.Failures)
		out = append(out, *entry)
	}
	return out
}

// reqV2Mode 以报告为准(live/deterministic),regrade 由计划标记;
// 零模型 = deterministic/replay/regrade 语义,不显示 provider 运行指标。
func reqV2Mode(plan *reqV2PlanIdentity, report *reqV2ReportHead) (mode string, zeroModel bool) {
	switch {
	case plan.Regrade:
		return "regrade", true
	case report.Mode == "replay" || plan.Mode == "replay":
		return "replay", true
	case report.Mode == "deterministic" || plan.Mode == "deterministic":
		return "deterministic", true
	case report.Mode == "live":
		return "live", plan.ZeroModel
	default:
		return firstNonEmpty(plan.Mode, report.Mode, "unknown"), plan.ZeroModel
	}
}

// buildReqV2RegradePlan 投影 regrade 身份:来源运行指针与源版本,不含路径之外的本机信息。
func buildReqV2RegradePlan(plan *reqV2PlanIdentity) json.RawMessage {
	if !plan.Regrade {
		return nil
	}
	raw, err := json.Marshal(map[string]any{
		"source_run":             plan.SourceRun,
		"source_grader_version":  plan.SourceGrader,
		"source_manifest_sha256": shortSHA(plan.SourceManifest),
		"source_report_sha256":   shortSHA(plan.SourceReport),
		"zero_model":             plan.ZeroModel,
	})
	if err != nil {
		return nil
	}
	return raw
}

func truncateText(text string, limit int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…(已截断)"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func shortSHA(sha string) string {
	if len(sha) >= 12 {
		return sha[:12]
	}
	return sha
}

func appendUnique(list []string, value string) []string {
	for _, item := range list {
		if item == value {
			return list
		}
	}
	return append(list, value)
}

var _ = planningeval.ReqV2GraderVersion
var _ = sync.Mutex{}
