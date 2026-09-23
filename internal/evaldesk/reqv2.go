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

	"github.com/subaru-ye/pc-builder-agent/internal/planningeval"
)

const reqV2Dir = "reqv2"

// 长文本投影上限:主阅读区优先结构化差异,超长原文放入前端次级展开区。
const (
	reqV2MaxText      = 4000
	reqV2MaxValueJSON = 600
)

type ReqV2ModelIdentity struct {
	Role     string `json:"role"`
	Model    string `json:"model"`
	Provider string `json:"provider,omitempty"`
}

type ReqV2Evidence struct {
	Status string   `json:"status"` // complete | incomplete | invalid
	Notes  []string `json:"notes"`
}

type ReqV2RunSummary struct {
	ID          string               `json:"id"`
	DirName     string               `json:"dir_name"`
	Label       string               `json:"label"`
	CreatedAt   *string              `json:"created_at"`
	Mode        string               `json:"mode"` // live | deterministic | replay | regrade | unknown
	ZeroModel   bool                 `json:"zero_model"`
	Regrade     bool                 `json:"regrade"`
	SourceRun   string               `json:"source_run,omitempty"`
	Superseded  bool                 `json:"superseded"`
	Grader      string               `json:"grader_version"`
	Splits      []string             `json:"splits"`
	Repeats     int                  `json:"repeats"`
	CodeCommit  string               `json:"code_commit,omitempty"`
	CodeDirty   *bool                `json:"code_dirty"`
	Models      []ReqV2ModelIdentity `json:"models"`
	GatePassed  *bool                `json:"gate_passed"`
	Conclusion  string               `json:"conclusion"`
	Evidence    ReqV2Evidence        `json:"evidence"`
	ManifestSHA string               `json:"manifest_sha256,omitempty"`
	GatesSHA    string               `json:"gates_sha256,omitempty"`
	MaxCalls    int                  `json:"max_model_requests,omitempty"`
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
	Index        int               `json:"index"`
	Reply        string            `json:"reply,omitempty"`
	Operations   []json.RawMessage `json:"operations,omitempty"`
	TurnSignals  []string          `json:"turn_signals,omitempty"`
	DurationMS   int64             `json:"duration_ms,omitempty"`
	ModelCalled  bool              `json:"screen_model_called"`
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
	Layer     string         `json:"layer"`
	ID        string         `json:"id"`
	Title     string         `json:"title,omitempty"`
	Split     string         `json:"split,omitempty"`
	Session   string         `json:"session,omitempty"`
	Rationale string         `json:"rationale,omitempty"`
	Fields    map[string]any `json:"fields"`
	SHA256    string         `json:"sha256"`
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
}

type reqV2PlanModelSpec struct {
	Model    string `json:"model"`
	Provider string `json:"provider"`
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
	Layer      string   `json:"layer"`
	ID         string   `json:"id"`
	Split      string   `json:"split"`
	Session    string   `json:"session"`
	Repeat     int      `json:"repeat"`
	Pass       bool     `json:"pass"`
	Vetoes     []string `json:"vetoes,omitempty"`
	Skipped    string   `json:"skipped,omitempty"`
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
	return ReqV2Evidence{Status: status, Notes: ig.notes}
}

// ---- 发现与缓存 ----

func (s *Store) reqV2Root() string { return filepath.Join(s.root, "artifacts", reqV2Dir) }

func (s *Store) reqV2Directories() (map[string]string, []string) {
	dirs := map[string]string{}
	warnings := []string{}
	root := s.reqV2Root()
	st, err := os.Lstat(root)
	if err != nil {
		return dirs, []string{"artifacts/reqv2 尚无 Requirement v2 产物"}
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
	for _, name := range []string{"plan.json", "report.json", "results.jsonl", "manifest.json", "gates.json"} {
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
	r := &savedReqV2Run{dir: dir, stamp: stamp}
	r.summary = s.buildReqV2Summary(id, dir, plan, report, ig.finalize(), ig.checks)
	r.detail = buildReqV2Detail(r.summary, plan, report, ig.checks)
	s.cacheReqV2[id] = r
	return r
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
	identity("splits", strings.Join(plan.Splits, ","), strings.Join(report.Splits, ","))
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
	folded := map[string]*ReqV2LayerRow{}
	for _, row := range report.Cases {
		layer := folded[row.Layer]
		if layer == nil {
			layer = &ReqV2LayerRow{}
			folded[row.Layer] = layer
		}
		layer.Cases++
		if row.Pass {
			layer.Passed++
		}
	}
	for name, summary := range report.PerLayer {
		computed, ok := folded[name]
		if !ok {
			continue
		}
		if summary.Cases != computed.Cases || summary.Passed != computed.Passed {
			ig.record("per_layer/"+name, "incomplete", fmt.Sprintf("层 %s 报告汇总与逐题记录不一致(报告 %d/%d,逐题 %d/%d);保留报告原值", name, summary.Cases, summary.Passed, computed.Cases, computed.Passed))
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
		Grader: firstNonEmpty(report.GraderVersion, plan.GraderVersion, "未记录"), Splits: report.Splits, Repeats: report.Repeats,
		CodeCommit: plan.CodeCommit, CodeDirty: plan.CodeDirty,
		GatePassed: report.GatePassed, Conclusion: truncateText(report.Conclusion, reqV2MaxText),
		Evidence: evidence, ManifestSHA: shortSHA(report.ManifestSHA256), GatesSHA: shortSHA(report.GatesSHA256),
		MaxCalls: plan.MaxModelRequests,
	}
	for role, spec := range plan.Models {
		if spec.Model == "" {
			continue
		}
		summary.Models = append(summary.Models, ReqV2ModelIdentity{Role: role, Model: spec.Model, Provider: spec.Provider})
	}
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

func buildReqV2Detail(summary ReqV2RunSummary, plan *reqV2PlanIdentity, report *reqV2ReportHead, checks []ReqV2IntegrityCheck) *ReqV2RunDetail {
	return &ReqV2RunDetail{
		ReqV2RunSummary: summary,
		DurationMS:      report.DurationMS,
		GateVerdicts:    report.GateVerdicts,
		PerLayer:        report.PerLayer,
		ModelQuality:    report.ModelQuality,
		Usage:           report.Usage,
		Limitations:     report.Limitations,
		CaseIndex:       buildReqV2CaseIndex(report.Cases),
		RegradePlan:     buildReqV2RegradePlan(plan),
		Thresholds:      report.Gates,
		Integrity:       checks,
	}
}

func buildReqV2CaseIndex(cases []ReqV2CaseResultRow) []ReqV2CaseIndexEntry {
	order := []string{}
	index := map[string]*ReqV2CaseIndexEntry{}
	for _, row := range cases {
		key := row.Layer + "/" + row.ID
		entry, ok := index[key]
		if !ok {
			entry = &ReqV2CaseIndexEntry{Layer: row.Layer, ID: row.ID, Split: row.Split, Session: row.Session, PassK: true, Repeats: []int{}, Failures: []string{}}
			index[key] = entry
			order = append(order, key)
		}
		entry.Repeats = append(entry.Repeats, row.Repeat)
		entry.PassK = entry.PassK && row.Pass
		if row.Skipped != "" {
			entry.Skipped = true
		}
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
