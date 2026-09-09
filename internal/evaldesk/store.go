package evaldesk

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

const maxArtifactBytes = 256 << 20

var errNotFound = errors.New("运行不存在或产物已移除")

// Store never opens environment files, databases, model clients or raw log files.
type Store struct {
	root    string
	mu      sync.Mutex
	cache   map[string]*savedRun
	gitMu   sync.Mutex
	commits map[string]CommitSummary
}

type savedRun struct {
	dir         string
	stamp       string
	summary     RunSummary
	meta        evalsuite.ReportMeta
	cases       map[string]evalsuite.Case
	caseSources map[string]json.RawMessage
	hashes      map[string]string
	records     []evalsuite.CaseRecord
	current     []evalsuite.CaseRecord
}

func NewStore(root string) (*Store, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	return &Store{root: absolute, cache: map[string]*savedRun{}}, nil
}

func runID(relative string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(filepath.ToSlash(relative))))[:24]
}

func below(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

// Even artifact symlinks inside an otherwise safe directory are rejected.
func (s *Store) safeFile(dir, name string) (string, error) {
	if !below(s.root, dir) || filepath.Base(name) != name {
		return "", errNotFound
	}
	relative, err := filepath.Rel(s.root, filepath.Join(dir, name))
	if err != nil {
		return "", errNotFound
	}
	current := s.root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		st, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("不读取符号链接产物")
		}
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil || !below(s.root, resolved) {
		return "", errors.New("不读取越出项目目录的链接产物")
	}
	st, err := os.Stat(current)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Size() > maxArtifactBytes {
		return "", errors.New("产物类型或大小不受支持")
	}
	return current, nil
}

func (s *Store) read(dir, name string) ([]byte, error) {
	path, err := s.safeFile(dir, name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (s *Store) directories() (map[string]string, []string) {
	dirs := map[string]string{}
	warnings := []string{}
	for _, name := range []string{"eval", "evalchange"} {
		root := filepath.Join(s.root, "artifacts", name)
		artifactRoot, err := os.Lstat(filepath.Join(s.root, "artifacts"))
		if err != nil || artifactRoot.Mode()&os.ModeSymlink != 0 {
			warnings = append(warnings, "评估产物目录不存在或不可读取")
			break
		}
		st, err := os.Lstat(root)
		if err != nil {
			warnings = append(warnings, "artifacts/"+name+" 尚无产物")
			continue
		}
		if st.Mode()&os.ModeSymlink != 0 {
			warnings = append(warnings, "已跳过符号链接目录 artifacts/"+name)
			continue
		}
		resolved, resolveErr := filepath.EvalSymlinks(root)
		if resolveErr != nil || !below(s.root, resolved) {
			warnings = append(warnings, "已跳过越出项目目录的链接产物")
			continue
		}
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if d.IsDir() {
				resolved, resolveErr := filepath.EvalSymlinks(path)
				if resolveErr != nil || !below(s.root, resolved) {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Name() != "meta.json" && d.Name() != "cases.json" && d.Name() != "results.jsonl" {
				return nil
			}
			dir := filepath.Dir(path)
			rel, err := filepath.Rel(s.root, dir)
			if err == nil && len(dirs) < 1000 {
				dirs[runID(rel)] = dir
			}
			return nil
		})
		if err != nil {
			warnings = append(warnings, "部分评估产物目录不可读取")
		}
	}
	return dirs, warnings
}

func (s *Store) load(id, dir string) *savedRun {
	var stamp strings.Builder
	for _, name := range []string{"meta.json", "cases.json", "results.jsonl", "report.md", "source.json", "prompts.json"} {
		path, err := s.safeFile(dir, name)
		if err != nil {
			fmt.Fprintf(&stamp, "%s:missing;", name)
			continue
		}
		st, err := os.Stat(path)
		if err == nil {
			fmt.Fprintf(&stamp, "%s:%d:%d;", name, st.Size(), st.ModTime().UnixNano())
		}
	}
	if cached := s.cache[id]; cached != nil && cached.stamp == stamp.String() {
		return cached
	}
	r := &savedRun{dir: dir, stamp: stamp.String(), cases: map[string]evalsuite.Case{}, caseSources: map[string]json.RawMessage{}, hashes: map[string]string{}}
	rel, _ := filepath.Rel(filepath.Join(s.root, "artifacts"), dir)
	r.summary = RunSummary{ID: id, Label: clean(filepath.ToSlash(rel)), Status: "complete", Notes: []string{}}
	raw, err := s.read(dir, "meta.json")
	if err != nil {
		r.summary.Status = "incomplete"
		r.summary.Notes = append(r.summary.Notes, "运行元数据缺失，版本和计划次数未记录")
	} else if json.Unmarshal(raw, &r.meta) != nil {
		r.summary.Status = "invalid"
		r.summary.Notes = append(r.summary.Notes, "运行元数据格式损坏，无法复核")
	}
	r.summary.Versions = s.versions(dir, r.meta, raw)
	if !r.meta.GeneratedAt.IsZero() {
		r.summary.CreatedAt = strptr(r.meta.GeneratedAt.Format(time.RFC3339Nano))
	}
	path, err := s.safeFile(dir, "cases.json")
	if err == nil {
		snapshot, cases, readErr := evalsuite.ReadSuiteSnapshot(path, r.meta.SuiteSHA256)
		if readErr != nil {
			r.summary.Status = "invalid"
			r.summary.Notes = append(r.summary.Notes, "冻结题库无法通过内容与版本校验")
		} else {
			for i, c := range cases {
				r.cases[c.ID] = c
				r.caseSources[c.ID] = snapshot.Cases[i]
				r.hashes[c.ID] = snapshot.Manifest.Cases[i].SHA256
			}
		}
	} else {
		if r.summary.Status == "complete" {
			r.summary.Status = "legacy"
		}
		r.summary.Notes = append(r.summary.Notes, "冻结题目未记录，不能用当前题库替代历史输入和期望")
	}
	if path, err = s.safeFile(dir, "results.jsonl"); err == nil {
		var incomplete bool
		r.records, incomplete = readPartialRecords(path)
		if incomplete {
			r.summary.Status = "incomplete"
			r.summary.Notes = append(r.summary.Notes, "结果文件含不完整或损坏记录，仅展示可读取部分")
		}
	} else {
		r.summary.Status = "incomplete"
		r.summary.Notes = append(r.summary.Notes, "执行结果缺失，尚不能形成完整结论")
	}
	if len(r.cases) > 0 && r.meta.RequestedSeeds > 0 && len(r.records) != len(r.cases)*r.meta.RequestedSeeds {
		r.summary.Status = "incomplete"
		r.summary.Notes = append(r.summary.Notes, "记录数未满足冻结题目 × 计划重复次数；缺记录不计作通过")
	}
	if len(r.cases) > 0 && invalidIdentity(r) {
		r.summary.Status = "invalid"
		r.summary.Notes = append(r.summary.Notes, "存在重复、无效或不属于冻结题库的执行记录")
	}
	if r.meta.Prompts != nil {
		if _, err := s.safeFile(dir, "prompts.json"); err != nil {
			r.summary.Status = "invalid"
			r.summary.Notes = append(r.summary.Notes, "声明的提示词快照缺失或不是受支持的本机普通文件")
		}
	}
	var lifecycle struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(raw, &lifecycle)
	if r.summary.Status != "invalid" && (lifecycle.Status == "running" || lifecycle.Status == "incomplete") {
		r.summary.Status = "incomplete"
		r.summary.Notes = append(r.summary.Notes, "运行未完成")
	}
	for _, record := range r.records {
		if r.summary.Status != "invalid" && (strings.Contains(record.RunErr, "上限") || strings.Contains(record.RunErr, "未执行")) {
			r.summary.Status = "incomplete"
			r.summary.Notes = append(r.summary.Notes, "存在调用上限耗尽或未执行记录")
			break
		}
	}
	r.summary.Original = metrics(r, r.records, nil)
	if r.summary.Status == "complete" {
		audit, auditErr := evalsuite.AuditRun(dir)
		if auditErr == nil {
			r.current = audit.Current
			r.summary.Verified = true
			current := metrics(r, r.current, nil)
			r.summary.Current = &current
			r.summary.RegradedTrials = &audit.RegradedTrials
		} else {
			r.summary.Status = "invalid"
			r.summary.Notes = append(r.summary.Notes, "原始成绩或执行证据未通过离线复验；仅展示保存的原成绩，不给出统一复核结论")
		}
	}
	if r.meta.RequestedSeeds <= 0 {
		r.summary.Notes = append(r.summary.Notes, "计划重复次数未记录，全部重复通过比例未知")
	}
	if r.summary.Versions.PromptVersion == nil {
		r.summary.Notes = append(r.summary.Notes, "提示词独立版本及原文快照未记录")
	}
	if r.summary.Versions.DataFingerprint == nil {
		r.summary.Notes = append(r.summary.Notes, "商品数据指纹未记录；严格对照另行逐条核对冻结目录")
	}
	s.cache[id] = r
	return r
}

func invalidIdentity(r *savedRun) bool {
	seen := map[string]bool{}
	for _, record := range r.records {
		key := fmt.Sprintf("%s/%d", record.CaseID, record.Seed)
		c, ok := r.cases[record.CaseID]
		if !ok || c.Stage != record.Stage || record.Seed < 1 || (r.meta.RequestedSeeds > 0 && record.Seed > r.meta.RequestedSeeds) || seen[key] {
			return true
		}
		seen[key] = true
	}
	return false
}

func readPartialRecords(path string) ([]evalsuite.CaseRecord, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, true
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 65536), 32<<20)
	records := []evalsuite.CaseRecord{}
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var record evalsuite.CaseRecord
		if json.Unmarshal(scanner.Bytes(), &record) != nil {
			return records, true
		}
		records = append(records, record)
	}
	return records, scanner.Err() != nil
}

func (s *Store) Runs() RunsResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	dirs, warnings := s.directories()
	out := RunsResponse{Runs: []RunSummary{}, Warnings: warnings, CurrentGrader: evalsuite.CurrentGraderVersion}
	for id, dir := range dirs {
		out.Runs = append(out.Runs, s.load(id, dir).summary)
	}
	sort.Slice(out.Runs, func(i, j int) bool {
		return deref(out.Runs[i].CreatedAt)+out.Runs[i].Label > deref(out.Runs[j].CreatedAt)+out.Runs[j].Label
	})
	return out
}

func (s *Store) lookup(id string) (*savedRun, error) {
	if len(id) != 24 || strings.ContainsAny(id, "/\\.") {
		return nil, errNotFound
	}
	dirs, _ := s.directories()
	dir, ok := dirs[id]
	if !ok {
		return nil, errNotFound
	}
	return s.load(id, dir), nil
}

func strptr(s string) *string {
	if s == "" {
		return nil
	}
	v := clean(s)
	return &v
}
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (s *Store) versions(dir string, m evalsuite.ReportMeta, raw []byte) Versions {
	v := Versions{Suite: strptr(m.SuiteVersion), SuiteHash: strptr(m.SuiteSHA256), SnapshotDate: strptr(m.SnapshotDate), Grader: strptr(m.GraderVersion), Models: []ModelVersion{}}
	if m.Code != nil {
		v.Commit = strptr(m.Code.CheckoutCommit)
		v.Binary = strptr(m.Code.BinarySHA256)
		v.Dirty = m.Code.CheckoutDirty
	}
	for _, role := range []string{"builder", "screening", "embedding"} {
		cfg := m.Models[role]
		model, _ := cfg["model"].(string)
		provider, _ := cfg["provider"].(string)
		if model == "" {
			switch role {
			case "builder":
				model = m.BuilderModel
			case "screening":
				model = m.ScreeningModel
			case "embedding":
				model = m.EmbeddingModel
			}
		}
		if model != "" {
			v.Models = append(v.Models, ModelVersion{Role: role, Model: clean(model), Provider: strptr(provider)})
		}
	}
	var evidence struct {
		Prompts *struct {
			SHA256       string `json:"sha256"`
			SnapshotFile string `json:"snapshot_file"`
		} `json:"prompts"`
		Data *struct {
			SHA256 string `json:"sha256"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &evidence)
	if evidence.Prompts != nil {
		v.PromptVersion = strptr(evidence.Prompts.SHA256)
		if evidence.Prompts.SnapshotFile == "prompts.json" {
			_, err := s.safeFile(dir, "prompts.json")
			v.PromptSnapshot = err == nil
		}
	}
	if evidence.Data != nil {
		v.DataFingerprint = strptr(evidence.Data.SHA256)
		if v.DataFingerprint != nil {
			v.DataFingerprintSource = strptr("运行时记录的完整目录与价格")
		}
	}
	// Source files contain paths and build input hashes; only expose their explicit digest.
	if raw, err := s.read(dir, "source.json"); err == nil {
		var source struct {
			SHA256 string `json:"sha256"`
		}
		if json.Unmarshal(raw, &source) == nil {
			v.SourceFingerprint = strptr(source.SHA256)
		}
	}
	return v
}
