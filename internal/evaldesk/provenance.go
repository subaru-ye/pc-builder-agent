package evaldesk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

const maxCommitFiles = 100

type limitedGitOutput struct{ bytes.Buffer }

func (b *limitedGitOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 256<<10 {
		return 0, errors.New("Git output limit")
	}
	return b.Buffer.Write(p)
}

// fixed local Git commands never accept a ref/path from the HTTP request. Lazy
// fetch and replace objects are disabled; no source patches or raw stderr escape.
func (s *Store) readGit(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"--no-pager", "--no-replace-objects", "-C", s.root}, args...)...)
	command.Env = append(os.Environ(), "GIT_NO_LAZY_FETCH=1", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	var output limitedGitOutput
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (s *Store) commitSummary(commit *string) CommitSummary {
	out := CommitSummary{Hash: commit, Status: "unrecorded"}
	if commit == nil {
		return out
	}
	out.Status = "unavailable"
	if !commitID.MatchString(*commit) {
		out.Hash = nil
		return out
	}
	key := strings.ToLower(*commit)
	s.gitMu.Lock()
	defer s.gitMu.Unlock()
	if cached, ok := s.commits[key]; ok {
		return cached
	}
	raw, err := s.readGit("show", "--no-patch", "--no-ext-diff", "--format=%s%x00%cI", key, "--")
	if err != nil || len(raw) > 8192 {
		return out
	}
	parts := strings.SplitN(strings.TrimSpace(string(raw)), "\x00", 2)
	if len(parts) != 2 || len(parts[0]) > 1000 {
		return out
	}
	when, err := time.Parse(time.RFC3339, parts[1])
	if err != nil {
		return out
	}
	out.Subject = strptr(provenanceText(parts[0]))
	out.CommittedAt = strptr(when.Format(time.RFC3339))
	out.Status = "available"
	if s.commits == nil {
		s.commits = map[string]CommitSummary{}
	}
	s.commits[key] = out
	return out
}

// The public file list covers repository source and documentation only. Hidden
// credentials, artifacts, logs, absolute paths and control characters are omitted.
func publicCommitPath(name string) bool {
	if name == "" || len(name) > 400 || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\:") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	lower := strings.ToLower(name)
	for _, part := range strings.Split(lower, "/") {
		if strings.HasPrefix(part, ".env") || part == ".git" || strings.Contains(part, "credential") || strings.Contains(part, "secret") || strings.Contains(part, "password") || strings.HasSuffix(part, ".pem") || strings.HasSuffix(part, ".key") {
			return false
		}
	}
	if !strings.Contains(name, "/") {
		allowed := map[string]bool{"go.mod": true, "go.sum": true, "package.json": true, "pnpm-lock.yaml": true, "pnpm-workspace.yaml": true, "README.md": true, "DESIGN.md": true, "AGENTS.md": true, "LICENSE": true, ".gitignore": true, ".gitattributes": true, ".editorconfig": true, "Dockerfile": true, "docker-compose.yml": true, "docker-compose.yaml": true, "Makefile": true}
		return allowed[name]
	}
	root := strings.SplitN(name, "/", 2)[0]
	if !map[string]bool{"cmd": true, "internal": true, "web": true, "docs": true, "scripts": true, "migrations": true, ".github": true}[root] {
		return false
	}
	for _, part := range strings.Split(lower, "/") {
		if part == "artifacts" || part == "logs" || part == "node_modules" || part == "test-results" || part == ".next" {
			return false
		}
	}
	return map[string]bool{".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true, ".json": true, ".md": true, ".mdx": true, ".css": true, ".scss": true, ".sql": true, ".sh": true, ".ps1": true, ".toml": true, ".yaml": true, ".yml": true, ".svg": true, ".html": true, ".txt": true}[strings.ToLower(path.Ext(name))]
}

func parseCommitFiles(raw []byte) ([]CommitFile, bool, error) {
	files := []CommitFile{}
	fields := bytes.Split(raw, []byte{0})
	if len(fields) > 0 && len(fields[len(fields)-1]) == 0 {
		fields = fields[:len(fields)-1]
	}
	if len(fields)%2 != 0 {
		return nil, false, errors.New("invalid Git file list")
	}
	omitted := false
	for i := 0; i < len(fields); i += 2 {
		name := string(fields[i+1])
		if !publicCommitPath(name) {
			omitted = true
			continue
		}
		if len(files) >= maxCommitFiles {
			omitted = true
			continue
		}
		status, label := "other", "其他变化"
		switch string(fields[i]) {
		case "A":
			status, label = "added", "新增"
		case "M":
			status, label = "modified", "修改"
		case "D":
			status, label = "deleted", "删除"
		case "T":
			status, label = "type_changed", "类型变化"
		default:
			return nil, false, errors.New("unsupported Git file status")
		}
		files = append(files, CommitFile{Path: provenanceText(name), Status: status, Label: label})
	}
	return files, omitted, nil
}

func (s *Store) commitDetails(run *savedRun) CommitDetails {
	out := CommitDetails{CommitSummary: s.commitSummary(run.summary.Versions.Commit), Files: []CommitFile{}, Notes: []string{
		"这是评估启动时记录的仓库提交；提交内容不等于实际执行程序的完整源码，提交时间也不等于评估时间。",
	}}
	if run.summary.Versions.Dirty == nil {
		out.Notes = append(out.Notes, "启动时是否存在未提交改动：未记录。")
	} else if *run.summary.Versions.Dirty {
		out.Notes = append(out.Notes, "启动时存在未提交改动；下面的提交文件清单不包含这些改动。")
	} else {
		out.Notes = append(out.Notes, "记录显示启动时没有未提交改动；这仍不构成程序与源码之间的精确映射。")
	}
	if out.Status != "available" {
		message := "提交标题、时间和文件清单在本机不可读取；冻结题库仍可查看。"
		if out.Status == "unrecorded" {
			message = "仓库提交未记录；冻结题库仍可查看。"
		}
		out.Notes = append(out.Notes, message)
		return out
	}
	raw, err := s.readGit("show", "--format=", "--name-status", "--no-renames", "--no-ext-diff", "--no-textconv", "--diff-merges=first-parent", "-z", deref(out.Hash), "--")
	if err != nil {
		out.Notes = append(out.Notes, "本机提交文件清单不可读取或超出读取限制。")
		return out
	}
	files, truncated, err := parseCommitFiles(raw)
	if err != nil {
		out.Notes = append(out.Notes, "本机提交文件清单格式无法识别。")
		return out
	}
	out.Files, out.FilesAvailable, out.FilesTruncated = files, true, truncated
	out.Notes = append(out.Notes, "文件清单按该提交相对第一父提交的变化显示；首次提交视为新增，重命名显示为删除与新增，不读取源码补丁。")
	if truncated {
		out.Notes = append(out.Notes, fmt.Sprintf("仅展示公开源码与文档路径，最多 %d 个文件；超出范围或数量限制的路径已隐藏。", maxCommitFiles))
	}
	return out
}

func frozenCaseCount(run *savedRun) *int {
	if len(run.cases) == 0 {
		return nil
	}
	count := len(run.cases)
	return &count
}

func (s *Store) Timeline() TimelineResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	dirs, warnings := s.directories()
	out := TimelineResponse{Items: []TimelineEntry{}, Warnings: warnings, Notes: []string{"时间线按产物记录的评估时间排列；时间未记录的运行排在末尾，不从目录名称补造日期。", "同一代码提交可以对应多次评估；提交时间与运行时间分别展示，不以时间先后证明结果变化的原因。"}}
	// Available objects are immutable and cached in Store; request-local caching
	// also deduplicates missing objects, which may become available on a later read.
	commits := map[string]CommitSummary{}
	for id, dir := range dirs {
		run := s.load(id, dir)
		key := strings.ToLower(deref(run.summary.Versions.Commit))
		commit, ok := commits[key]
		if !ok {
			commit = s.commitSummary(run.summary.Versions.Commit)
			commits[key] = commit
		}
		out.Items = append(out.Items, TimelineEntry{Run: run.summary, FrozenCaseCount: frozenCaseCount(run), Commit: commit})
	}
	sort.Slice(out.Items, func(i, j int) bool {
		a, b := out.Items[i].Run, out.Items[j].Run
		if a.CreatedAt == nil || b.CreatedAt == nil {
			if a.CreatedAt == nil && b.CreatedAt == nil {
				return a.ID < b.ID
			}
			return a.CreatedAt != nil
		}
		at, _ := time.Parse(time.RFC3339, *a.CreatedAt)
		bt, _ := time.Parse(time.RFC3339, *b.CreatedAt)
		if at.Equal(bt) {
			return a.ID < b.ID
		}
		return at.After(bt)
	})
	return out
}

var windowsPrivatePath = regexp.MustCompile(`(?i)\b[a-z]:[\\/][^\r\n\s"<>]+`)
var unixPrivatePath = regexp.MustCompile(`(?:/(?:Users|home|etc|private|tmp|var)/)[^\r\n\s"<>]+`)
var frozenSecretField = regexp.MustCompile(`(?i)^(?:api[_-]?key|key|authorization|proxy-authorization|cookie|set-cookie|password|passwd|token|access[_-]?token|refresh[_-]?token|client[_-]?secret|secret|pg_dsn)$`)

func provenanceText(text string) string {
	return unixPrivatePath.ReplaceAllString(windowsPrivatePath.ReplaceAllString(clean(text), "[本机路径已隐藏]"), "[本机路径已隐藏]")
}

func frozenJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "未记录"
	}
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		return "未记录"
	}
	var sanitize func(any) any
	sanitize = func(value any) any {
		switch value := value.(type) {
		case string:
			return provenanceText(value)
		case map[string]any:
			out := map[string]any{}
			for key, item := range value {
				if frozenSecretField.MatchString(key) {
					out[provenanceText(key)] = "[已隐藏]"
				} else {
					out[provenanceText(key)] = sanitize(item)
				}
			}
			return out
		case []any:
			for i, item := range value {
				value[i] = sanitize(item)
			}
			return value
		default:
			return value
		}
	}
	raw, err = json.MarshalIndent(sanitize(decoded), "", "  ")
	if err != nil {
		return "未记录"
	}
	// The known frozen schema contains no credentials fields. Free-form string
	// values were sanitized before encoding to keep the displayed JSON valid.
	return string(raw)
}

func expectationField(field string) string {
	labels := map[string]string{"budget_cny": "预算（元）", "resolution": "分辨率", "owned_parts": "已有配件", "budget_basis": "预算计算范围", "use_case": "用途", "use_case.type": "用途类型", "noise_pref": "噪声偏好", "size_pref": "机箱尺寸偏好", "budget_flex": "预算浮动比例", "existing_parts": "沿用配件类别"}
	if label := labels[field]; label != "" {
		return label
	}
	return provenanceText(field)
}

func expectationSummary(expect evalsuite.Expect) []string {
	lines := []string{}
	if expect.Kind != "" {
		labels := map[string]string{"spec": "应输出结构化需求单", "clarify": "应追问缺失信息，不输出需求单"}
		if label := labels[expect.Kind]; label != "" {
			lines = append(lines, label)
		}
	}
	if expect.Outcome != "" {
		labels := map[string]string{"pass": "应交付通过校验的配置单", "budget_adaptive": "应交付合格配置，或给出可复核的预算不足证据", "clarify": "应补问完成配置所需的信息", "catalog_infeasible": "应说明当前商品目录无法满足约束，并提供证据", "data_unavailable": "应说明缺少可核验的数据，本轮不交付", "search_exhausted": "应说明本轮搜索未找到合格方案，不能认定整个目录无解"}
		if label := labels[expect.Outcome]; label != "" {
			lines = append(lines, label)
		}
	}
	if expect.Reason != "" {
		labels := map[string]string{"missing_owned_information": "已有配件信息不完整", "owned_model_unresolved": "已有配件型号无法唯一核验", "owned_lock_conflict": "已有配件与锁定配置冲突", "catalog_category_missing": "当前目录缺少必需品类", "budget_lower_bound": "必需配件最低价超过预算上限", "platform_budget_lower_bound": "平台最低价超过预算上限", "selected_price_missing": "所选配件缺少可核验报价", "no_verified_solution": "本轮未找到通过校验的方案"}
		reason := labels[expect.Reason]
		if reason == "" {
			reason = provenanceText(expect.Reason)
		}
		lines = append(lines, "期望原因："+reason)
	}
	if expect.BudgetCNY != nil {
		lines = append(lines, fmt.Sprintf("预算应为 %d 元", *expect.BudgetCNY))
	}
	if expect.Resolution != "" {
		lines = append(lines, "分辨率应为 "+provenanceText(expect.Resolution))
	}
	for _, brand := range []struct{ label, value string }{{"CPU", expect.CPUBrand}, {"显卡", expect.GPUBrand}} {
		if brand.value != "" {
			if brand.value == "any" {
				lines = append(lines, brand.label+"不应指定品牌偏好")
			} else {
				lines = append(lines, brand.label+"品牌应为 "+provenanceText(brand.value))
			}
		}
	}
	for _, group := range []struct {
		prefix string
		fields []string
	}{{"应追问：", expect.ClarifyFields}, {"不应再次追问：", expect.ForbiddenClarifyFields}} {
		if len(group.fields) > 0 {
			labels := []string{}
			for _, field := range group.fields {
				labels = append(labels, expectationField(field))
			}
			lines = append(lines, group.prefix+strings.Join(labels, "、"))
		}
	}
	fields := []string{}
	for field := range expect.SpecFields {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		lines = append(lines, expectationField(field)+"应与冻结记录精确相同："+frozenJSON(expect.SpecFields[field]))
	}
	return lines
}

func frozenInputs(run *savedRun, c evalsuite.Case) []FrozenCaseInput {
	inputs := []FrozenCaseInput{}
	var wire struct {
		Requirement   json.RawMessage `json:"requirement"`
		Change        json.RawMessage `json:"change"`
		BaseSelection json.RawMessage `json:"base_selection"`
		Locked        json.RawMessage `json:"locked"`
		Expect        json.RawMessage `json:"expect"`
		Turns         []struct {
			Input  string          `json:"input"`
			Expect json.RawMessage `json:"expect"`
		} `json:"turns"`
	}
	if json.Unmarshal(run.caseSources[c.ID], &wire) != nil {
		return inputs
	}
	if len(c.Turns) > 0 {
		for i, turn := range c.Turns {
			inputs = append(inputs, FrozenCaseInput{Turn: i + 1, Input: provenanceText(turn.Input), InputKind: "text", Expected: frozenJSON(wire.Turns[i].Expect), ExpectationSummary: expectationSummary(turn.Expect)})
		}
		return inputs
	}
	input, kind := provenanceText(c.Input), "text"
	if c.Stage == evalsuite.StageBuild {
		kind = "structured"
		fields := map[string]json.RawMessage{}
		for _, field := range []struct {
			key   string
			value json.RawMessage
		}{{"requirement", wire.Requirement}, {"change", wire.Change}, {"base_selection", wire.BaseSelection}, {"locked", wire.Locked}} {
			if len(field.value) > 0 {
				fields[field.key] = field.value
			}
		}
		input = frozenJSON(fields)
	}
	inputs = append(inputs, FrozenCaseInput{Turn: 1, Input: input, InputKind: kind, Expected: frozenJSON(wire.Expect), ExpectationSummary: expectationSummary(c.Expect)})
	return inputs
}

func (s *Store) Provenance(id string) (ProvenanceResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.lookup(id)
	if err != nil {
		return ProvenanceResponse{}, err
	}
	out := ProvenanceResponse{Run: run.summary, FrozenCaseCount: frozenCaseCount(run), Cases: []FrozenCase{}, Commit: s.commitDetails(run), Notes: []string{"题目来自这次运行已保存且通过内容校验的冻结题库，包含未执行题目；不从当前题库补充历史内容。", "原输入与期望保留已记录字段，界面文本隐藏凭据和本机路径；这里不提供模型回复或原始日志。"}}
	if len(run.cases) == 0 {
		out.Notes = append(out.Notes, "冻结题库未记录、不可读取或未通过校验，无法展示完整题目；运行成绩和已知代码证据仍可查看。")
		return out, nil
	}
	ids := []string{}
	for id := range run.cases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c := run.cases[id]
		item := FrozenCase{ID: provenanceText(c.ID), Title: provenanceText(c.Title), Stage: stageLabel(c.Stage), ContentHash: strptr(run.hashes[id]), Inputs: frozenInputs(run, c), Notes: []string{}}
		for _, record := range run.records {
			if record.CaseID == id {
				item.RecordedTrials++
			}
		}
		if run.meta.RequestedSeeds > 0 {
			repeats := run.meta.RequestedSeeds
			item.PlannedTrials = &repeats
		}
		if item.RecordedTrials == 0 {
			item.Notes = append(item.Notes, "尚无执行记录，仍可查看冻结输入和期望。")
		}
		if len(item.Inputs) == 0 {
			item.Notes = append(item.Notes, "冻结原文不可读取，未用解码默认值代替原记录。")
		}
		out.Cases = append(out.Cases, item)
	}
	return out, nil
}
