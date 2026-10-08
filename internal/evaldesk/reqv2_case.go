package evaldesk

// Requirement v2 逐题证据与同身份对比的只读投影。
// 冻结题目只来自被选运行自身的 cases.json(哈希对上 manifest 才可用),
// 不从当前仓库 fixture 或其他运行补填。
import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/planningeval"
)

// Case 在报告逐题集合中定位唯一 (layer,id) 的全部 repeat。
func (s *Store) CaseReqV2(id, layer, caseID string) (*ReqV2CaseDetail, error) {
	run, err := s.reqV2Lookup(id)
	if err != nil {
		return nil, err
	}
	detail := &ReqV2CaseDetail{
		Run: run.summary.ID, Layer: layer, Case: caseID, PassK: true,
		Integrity: run.summary.Evidence,
	}
	// 从 report.json 重新解码,取到该 case 的完整观测(含 turns)。
	var report struct {
		Cases []struct {
			ReqV2CaseResultRow
			Observation json.RawMessage `json:"observation"`
		} `json:"cases"`
	}
	raw, err := s.read(run.dir, "report.json")
	if err != nil || json.Unmarshal(raw, &report) != nil {
		detail.FrozenNote = "报告不可读取,无法投影逐题观测"
		return detail, nil
	}
	found := false
	for _, row := range report.Cases {
		if row.Layer != layer || row.ID != caseID {
			continue
		}
		found = true
		repeat := ReqV2RepeatEvidence{
			Repeat: row.Repeat, Pass: row.Pass, Vetoes: row.Vetoes, Skipped: row.Skipped,
		}
		if detail.Split == "" {
			detail.Split = row.Split
			detail.Session = row.Session
		}
		for _, assertion := range row.Assertions {
			repeat.Assertions = append(repeat.Assertions, ReqV2AssertionRow{
				Name: assertion.Name, Pass: assertion.Pass,
				Detail:         truncateText(assertion.Detail, reqV2MaxText),
				Classification: assertion.Classification,
			})
		}
		var observation struct {
			Turns []struct {
				Reply        string            `json:"reply"`
				Operations   []json.RawMessage `json:"operations"`
				TurnSignals  []string          `json:"turn_signals"`
				DurationMS   int64             `json:"duration_ms"`
				ScreenCalled bool              `json:"screen_model_called"`
			} `json:"turns"`
			Readiness  json.RawMessage `json:"readiness"`
			UI         json.RawMessage `json:"ui"`
			FinalState json.RawMessage `json:"final_state"`
			Skipped    string          `json:"skipped"`
			Error      string          `json:"error"`
		}
		if row.Observation != nil && json.Unmarshal(row.Observation, &observation) == nil {
			repeat.Skipped = firstNonEmpty(repeat.Skipped, observation.Skipped)
			repeat.Error = truncateText(observation.Error, reqV2MaxText)
			for index, turn := range observation.Turns {
				projected := ReqV2TurnRow{
					Index: index + 1, Reply: truncateText(turn.Reply, reqV2MaxText),
					Operations:  truncateJSONEach(turn.Operations, reqV2MaxValueJSON),
					TurnSignals: turn.TurnSignals, DurationMS: turn.DurationMS,
					ModelCalled: turn.ScreenCalled,
				}
				repeat.Turns = append(repeat.Turns, projected)
			}
			rest := map[string]json.RawMessage{}
			for key, value := range map[string]json.RawMessage{
				"readiness":   observation.Readiness,
				"ui":          observation.UI,
				"final_state": observation.FinalState,
			} {
				if len(value) > 0 {
					rest[key] = value
				}
			}
			if len(rest) > 0 {
				encoded, err := json.Marshal(rest)
				if err == nil {
					repeat.Observation = encoded
				}
			}
		}
		detail.Repeats = append(detail.Repeats, repeat)
		detail.PassK = detail.PassK && row.Pass
	}
	if !found {
		return nil, errNotFound
	}
	sort.Slice(detail.Repeats, func(a, b int) bool { return detail.Repeats[a].Repeat < detail.Repeats[b].Repeat })
	detail.Frozen, detail.FrozenNote = s.reqV2FrozenCase(run.dir, layer, caseID)
	return detail, nil
}

// reqV2FrozenCase 读取运行内冻结的 <layer>/cases.json 并按 id 定位;
// 文件 SHA256 必须与报告冻结 manifest 一致,否则不投影题目内容。
func (s *Store) reqV2FrozenCase(dir, layer, caseID string) (*ReqV2FrozenCase, string) {
	cases, note := s.reqV2FrozenCases(dir, layer)
	if note != "" {
		return nil, note
	}
	for _, candidate := range cases {
		if candidate.ID == caseID {
			return &candidate, ""
		}
	}
	return nil, "冻结题目中不存在该 id;不从当前题库补填"
}

// DatasetReqV2 包含全部冻结题目，包括未被本次测试选中的用途分组。
func (s *Store) DatasetReqV2(id string) (*ReqV2Dataset, error) {
	run, err := s.reqV2Lookup(id)
	if err != nil {
		return nil, err
	}
	response := &ReqV2Dataset{Cases: []ReqV2FrozenCase{}, Notes: []string{}}
	manifest, err := s.readReqV2Manifest(run.dir)
	if err != nil {
		response.Notes = append(response.Notes, "评估集清单未通过校验，无法读取题目")
		return response, nil
	}
	for _, file := range manifest.Files {
		if !strings.HasSuffix(file.Path, "/cases.json") {
			continue
		}
		layer := strings.TrimSuffix(file.Path, "/cases.json")
		cases, note := s.reqV2FrozenCases(run.dir, layer)
		if note != "" {
			response.Notes = append(response.Notes, layer+"："+note)
		} else {
			response.Cases = append(response.Cases, cases...)
		}
	}
	return response, nil
}

func (s *Store) reqV2FrozenCases(dir, layer string) ([]ReqV2FrozenCase, string) {
	switch layer {
	case "extraction", "conversations", "reducer", "readiness", "policy", "ui-contract":
	default:
		return nil, "不支持的题目分类"
	}
	manifest, err := s.readReqV2Manifest(dir)
	if err != nil {
		return nil, "冻结题目清单不可读取;不从当前题库或其他运行补填"
	}
	want := ""
	for _, file := range manifest.Files {
		if file.Path == layer+"/cases.json" {
			want = file.SHA256
		}
	}
	if want == "" {
		return nil, "冻结清单未登记 " + layer + "/cases.json;不从当前题库补填"
	}
	raw, err := s.read(filepath.Join(dir, layer), "cases.json")
	if err != nil {
		return nil, "运行内冻结题目文件缺失或不可读取"
	}
	got := fmt.Sprintf("%x", sha256.Sum256(raw))
	if got != want {
		return nil, "冻结题目文件与 manifest 哈希不一致;不展示内容"
	}
	var cases []map[string]any
	if json.Unmarshal(raw, &cases) != nil {
		return nil, "冻结题目文件无法解析"
	}
	projected := make([]ReqV2FrozenCase, 0, len(cases))
	seen := map[string]bool{}
	for _, candidate := range cases {
		id, _ := candidate["id"].(string)
		if id == "" || seen[id] {
			return nil, "题目编号缺失或重复，无法定位题目"
		}
		seen[id] = true
		fields := map[string]any{}
		for key, value := range candidate {
			if key == "id" || key == "title" || key == "split" || key == "session" || key == "rationale" {
				continue
			}
			fields[key] = truncateValue(value, 32<<10)
		}
		text := func(key string) string {
			value, _ := candidate[key].(string)
			return value
		}
		content, _ := json.Marshal(candidate)
		projected = append(projected, ReqV2FrozenCase{
			Layer: layer, ID: id, Title: text("title"), Split: text("split"),
			Session: text("session"), Rationale: text("rationale"),
			Fields: fields, SHA256: got, ContentSHA: fmt.Sprintf("%x", sha256.Sum256(content)),
		})
	}
	return projected, ""
}

func (s *Store) readReqV2Manifest(dir string) (*struct {
	Files []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}, error) {
	raw, err := s.read(dir, "manifest.json")
	if err != nil {
		return nil, err
	}
	var report struct {
		ManifestSHA string `json:"manifest_sha256"`
	}
	reportRaw, err := s.read(dir, "report.json")
	if err != nil || json.Unmarshal(reportRaw, &report) != nil || report.ManifestSHA == "" || fmt.Sprintf("%x", sha256.Sum256(raw)) != report.ManifestSHA {
		return nil, fmt.Errorf("清单与报告哈希不一致")
	}
	var manifest struct {
		Files []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// 截断任意 JSON 值的字符串表示;主阅读区不放大段原文。
func truncateValue(value any, limit int) any {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) <= limit {
		return value
	}
	text := string(encoded)
	if len(text) > limit {
		text = text[:limit] + "…(已截断)"
	}
	return json.RawMessage(`"` + strings.ReplaceAll(strings.ReplaceAll(text, `\`, `\\`), `"`, `\"`) + `"`)
}

func truncateJSONEach(values []json.RawMessage, limit int) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		var decoded any
		if json.Unmarshal(value, &decoded) != nil {
			continue
		}
		out = append(out, json.RawMessage(truncateJSONText(value, limit)))
	}
	return out
}

func truncateJSONText(raw json.RawMessage, limit int) string {
	text := strings.TrimSpace(string(raw))
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}

// ReqV2CompareResponse 区分严格对照与仅并列查看;不产出改善百分比。
type ReqV2CompareResponse struct {
	Strict    bool                `json:"strict"`
	Reasons   []string            `json:"reasons,omitempty"`
	Baseline  ReqV2RunSummary     `json:"baseline"`
	Candidate ReqV2RunSummary     `json:"candidate"`
	Outcome   json.RawMessage     `json:"outcome,omitempty"`
	CaseSet   ReqV2CompareCaseSet `json:"case_set"`
}

type ReqV2CompareCaseSet struct {
	BaselineCases   int      `json:"baseline_cases"`
	CandidateCases  int      `json:"candidate_cases"`
	Common          int      `json:"common"`
	OnlyInBaseline  []string `json:"only_in_baseline,omitempty"`
	OnlyInCandidate []string `json:"only_in_candidate,omitempty"`
}

// CompareReqV2 严格对比只对 evaluator 允许的同一冻结身份进行:
// grader/manifest/gates 一致、split/repeats/运行语义一致、两侧证据完整。
func (s *Store) CompareReqV2(a, b string) (*ReqV2CompareResponse, error) {
	runA, err := s.reqV2Lookup(a)
	if err != nil {
		return nil, err
	}
	runB, err := s.reqV2Lookup(b)
	if err != nil {
		return nil, err
	}
	response := &ReqV2CompareResponse{Baseline: runA.summary, Candidate: runB.summary}
	if runA.summary.Evidence.Status == "invalid" {
		response.Reasons = append(response.Reasons, "基线运行证据无效(损坏或身份不一致),不能进入严格对比")
	}
	if runB.summary.Evidence.Status == "invalid" {
		response.Reasons = append(response.Reasons, "候选运行证据无效(损坏或身份不一致),不能进入严格对比")
	}
	if runA.summary.Evidence.Status == "incomplete" || runB.summary.Evidence.Status == "incomplete" {
		response.Reasons = append(response.Reasons, "存在缺证据的运行;对比只能并排查看,缺失记录不计作通过")
	}
	compare := func(field, av, bv string) {
		if av != bv {
			response.Reasons = append(response.Reasons, fmt.Sprintf("%s 不同(%s vs %s);不同冻结身份不具备严格可比条件", field, av, bv))
		}
	}
	compare("判卷版本", runA.summary.Grader, runB.summary.Grader)
	compare("manifest 冻结哈希", runA.summary.ManifestSHA, runB.summary.ManifestSHA)
	compare("gates 冻结哈希", runA.summary.GatesSHA, runB.summary.GatesSHA)
	compare("splits", strings.Join(runA.summary.Splits, ","), strings.Join(runB.summary.Splits, ","))
	if runA.summary.Repeats != runB.summary.Repeats {
		response.Reasons = append(response.Reasons, fmt.Sprintf("repeats 不同(%d vs %d);Pass^k 折叠口径不同,不具备严格可比条件", runA.summary.Repeats, runB.summary.Repeats))
	}
	if runA.summary.Mode != runB.summary.Mode {
		response.Reasons = append(response.Reasons, fmt.Sprintf("运行类型不同(%s vs %s);live、零模型、replay/regrade 语义不同,只能并排查看各自证据", runA.summary.Mode, runB.summary.Mode))
	}
	if runA.summary.ZeroModel != runB.summary.ZeroModel {
		response.Reasons = append(response.Reasons, "live 与零模型运行对比不能得出模型质量改善或退步结论")
	}
	// case 集合必须一致(以报告逐题 layer/id 集合为准,与 repeat 无关)。
	setA := reqV2CaseSet(runA)
	setB := reqV2CaseSet(runB)
	response.CaseSet = ReqV2CompareCaseSet{BaselineCases: len(setA), CandidateCases: len(setB)}
	onlyA, onlyB := []string{}, []string{}
	for key := range setA {
		if !setB[key] {
			onlyA = append(onlyA, key)
		} else {
			response.CaseSet.Common++
		}
	}
	for key := range setB {
		if !setA[key] {
			onlyB = append(onlyB, key)
		}
	}
	sort.Strings(onlyA)
	sort.Strings(onlyB)
	response.CaseSet.OnlyInBaseline = onlyA
	response.CaseSet.OnlyInCandidate = onlyB
	if len(onlyA) > 0 || len(onlyB) > 0 {
		response.Reasons = append(response.Reasons, fmt.Sprintf("两次运行的 case 集不同:基线独有 %d、候选独有 %d;缺题目不具备配对条件", len(onlyA), len(onlyB)))
	}
	response.Strict = len(response.Reasons) == 0
	if !response.Strict {
		return response, nil
	}
	// 严格对比:复用 planningeval.CompareRequirementV2 的 case 级 Pass^k 与
	// 最小样本/McNemar 口径;确定性层单独显示,不进入模型统计。
	linesA, errA := s.readReqV2ResultLines(runA.dir)
	linesB, errB := s.readReqV2ResultLines(runB.dir)
	if errA != nil || errB != nil {
		response.Strict = false
		response.Reasons = append(response.Reasons, "results.jsonl 不可读取,无法配对")
		return response, nil
	}
	gates, err := reqV2GatesOf(s, runA)
	if err != nil {
		response.Strict = false
		response.Reasons = append(response.Reasons, "冻结门槛无法读取,不能运行配对口径")
		return response, nil
	}
	outcome, err := planningeval.CompareRequirementV2(gates, linesA, linesB)
	if err != nil {
		response.Strict = false
		response.Reasons = append(response.Reasons, "配对统计无法完成:"+err.Error())
		return response, nil
	}
	encoded, err := json.Marshal(outcome)
	if err != nil {
		response.Strict = false
		return response, nil
	}
	response.Outcome = encoded
	return response, nil
}

func reqV2CaseSet(run *savedReqV2Run) map[string]bool {
	set := map[string]bool{}
	for _, entry := range run.detail.CaseIndex {
		set[entry.Layer+"/"+entry.ID] = true
	}
	return set
}

func (s *Store) readReqV2ResultLines(dir string) ([][]byte, error) {
	path, err := s.safeFile(dir, "results.jsonl")
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	lines := [][]byte{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 65536), 32<<20)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		lines = append(lines, []byte(scanner.Text()))
	}
	return lines, scanner.Err()
}

func reqV2GatesOf(s *Store, run *savedReqV2Run) (planningeval.ReqV2Gates, error) {
	var gates planningeval.ReqV2Gates
	raw, err := s.read(run.dir, "gates.json")
	if err != nil {
		return gates, err
	}
	return gates, json.Unmarshal(raw, &gates)
}
