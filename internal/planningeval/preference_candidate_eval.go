package planningeval

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

//go:embed testdata/preference-candidates/*.json preference_candidate.go
var preferenceCandidateFiles embed.FS

type candidateEvalTurn struct {
	preferenceCandidateMessage
	Update         schemas.RequirementUpdate `json:"update"`
	ExpectRejected bool                      `json:"expect_rejected,omitempty"`
}

type candidateGold struct {
	Field     string          `json:"field"`
	Value     json.RawMessage `json:"value"`
	Strength  string          `json:"strength"`
	Evidence  string          `json:"evidence"`
	MessageID string          `json:"message_id"`
	Quote     string          `json:"quote"`
}

type candidateEvalCase struct {
	ID        string              `json:"id"`
	Family    string              `json:"family"`
	Category  string              `json:"category"`
	Turns     []candidateEvalTurn `json:"turns"`
	Expected  []candidateGold     `json:"expected"`
	Rationale string              `json:"rationale"`
}

type candidateEvalCorpus struct {
	SchemaVersion int                 `json:"schema_version"`
	GoldStatus    string              `json:"gold_status"`
	Provenance    string              `json:"provenance"`
	Split         string              `json:"split"`
	Cases         []candidateEvalCase `json:"cases"`
}

type CandidateMetrics struct {
	Cases          int      `json:"cases"`
	ExactCases     int      `json:"exact_cases"`
	TruePositives  int      `json:"true_positives"`
	FalsePositives int      `json:"false_positives"`
	FalseNegatives int      `json:"false_negatives"`
	Precision      *float64 `json:"precision"`
	Recall         *float64 `json:"recall"`
}

type CandidateEvalResult struct {
	ID        string                        `json:"id"`
	Split     string                        `json:"split"`
	Category  string                        `json:"category"`
	Policy    string                        `json:"policy"`
	Expected  []candidateGold               `json:"expected"`
	Decisions []PreferenceCandidateDecision `json:"decisions"`
	Errors    []string                      `json:"errors"`
	Rationale string                        `json:"rationale"`
}

type CandidateEvalReport struct {
	SchemaVersion   int                         `json:"schema_version"`
	GeneratedAt     time.Time                   `json:"generated_at"`
	GoldStatus      string                      `json:"gold_status"`
	PolicySHA256    string                      `json:"policy_sha256"`
	CorpusSHA256    string                      `json:"corpus_sha256"`
	ReadyForProduct bool                        `json:"ready_for_product"`
	Limitations     []string                    `json:"limitations"`
	Metrics         map[string]CandidateMetrics `json:"metrics"`
	Cases           []CandidateEvalResult       `json:"cases"`
}

// CheckPreferenceCandidateEval 同时检查两组暂定金标。它核对格式与场景合法性，
// 不判定自然语言标注正确，也不代表人工签收。
func CheckPreferenceCandidateEval() error {
	_, _, err := loadPreferenceCandidateCorpora()
	return err
}

func loadPreferenceCandidateCorpora() ([]candidateEvalCorpus, string, error) {
	var corpora []candidateEvalCorpus
	ids, families := map[string]bool{}, map[string]string{}
	hash := sha256.New()
	for _, split := range []string{"dev", "validation"} {
		raw, err := preferenceCandidateFiles.ReadFile("testdata/preference-candidates/" + split + ".json")
		if err != nil {
			return nil, "", err
		}
		hash.Write(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")))
		var corpus candidateEvalCorpus
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&corpus); err != nil {
			return nil, "", fmt.Errorf("%s: %w", split, err)
		}
		if decoder.Decode(new(any)) != io.EOF {
			return nil, "", fmt.Errorf("%s: 尾部必须为空", split)
		}
		if corpus.SchemaVersion != 1 || corpus.GoldStatus != "provisional" || corpus.Split != split || corpus.Provenance != "assistant_authored_synthetic" || len(corpus.Cases) == 0 {
			return nil, "", fmt.Errorf("%s: 语料元数据无效", split)
		}
		for _, c := range corpus.Cases {
			if c.ID == "" || c.Family == "" || c.Category == "" || c.Rationale == "" || c.Expected == nil || len(c.Turns) == 0 || ids[c.ID] {
				return nil, "", fmt.Errorf("%s: 用例字段缺失或 ID 重复: %s", split, c.ID)
			}
			ids[c.ID] = true
			if other := families[c.Family]; other != "" && other != split {
				return nil, "", fmt.Errorf("%s: family %s 跨集合泄漏", c.ID, c.Family)
			}
			families[c.Family] = split
			state, messages, err := candidateCaseState(c)
			if err != nil {
				return nil, "", err
			}
			seenFields := map[string]bool{}
			for _, want := range c.Expected {
				f := state.Fields[want.Field]
				if seenFields[want.Field] || !schemas.IsStablePreferenceField(want.Field) || f.Status != "active" || f.Source == nil ||
					f.Source.MessageID != want.MessageID || f.Source.Quote != want.Quote || !schemas.EqualPreferenceValue(f.Value, want.Value) || f.Strength != want.Strength || f.Evidence != want.Evidence ||
					f.Scope != "session" || (f.Evidence != "stated" && f.Evidence != "accepted_proposal") {
					return nil, "", fmt.Errorf("%s: 暂定正例必须对应有效最终状态: %s", c.ID, want.Field)
				}
				for _, m := range messages {
					if m.ID == want.MessageID && m.Role != "user" {
						return nil, "", fmt.Errorf("%s: 正例来源不是用户", c.ID)
					}
				}
				seenFields[want.Field] = true
			}
		}
		corpora = append(corpora, corpus)
	}
	return corpora, fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func candidateCaseState(c candidateEvalCase) (schemas.RequirementState, []preferenceCandidateMessage, error) {
	state := schemas.NewRequirementState()
	messages := make([]preferenceCandidateMessage, 0, len(c.Turns))
	seen := map[string]bool{}
	for _, t := range c.Turns {
		if t.ID == "" || seen[t.ID] || t.Content == "" || (t.Role != "user" && t.Role != "assistant") || t.Update.Operations == nil {
			return state, messages, fmt.Errorf("%s: 非法或重复消息: %s", c.ID, t.ID)
		}
		seen[t.ID] = true
		messages = append(messages, t.preferenceCandidateMessage)
		if t.Role == "assistant" {
			if len(t.Update.Operations) != 0 || len(t.Update.Observations) != 0 || t.ExpectRejected {
				return state, messages, fmt.Errorf("%s: 助手消息只作上下文", c.ID)
			}
			continue
		}
		next, err := schemas.ApplyRequirementUpdate(state, t.Update, schemas.RequirementSource{Kind: "chat", MessageID: t.ID, Quote: t.Content})
		if (err != nil) != t.ExpectRejected {
			return state, messages, fmt.Errorf("%s/%s: reducer 拒绝与夹具约定不符: %v", c.ID, t.ID, err)
		}
		if err == nil {
			state = next
		}
	}
	return state, messages, nil
}

func gradePreferenceCandidates(result *CandidateEvalResult, metrics *CandidateMetrics) {
	metrics.Cases++
	matched := map[string]bool{}
	result.Errors = []string{}
	for _, d := range result.Decisions {
		got := d.Candidate
		if got == nil {
			continue
		}
		match := false
		for _, want := range result.Expected {
			if !matched[want.Field] && got.Field == want.Field && schemas.EqualPreferenceValue(got.Value, want.Value) && got.Strength == want.Strength && got.Evidence == want.Evidence &&
				got.Source.Kind == "chat" && got.Source.SessionID == result.ID && got.Source.MessageID == want.MessageID && got.Source.Quote == want.Quote && got.SubjectStatus == "requires_user_selection" {
				matched[want.Field], match = true, true
				break
			}
		}
		if match {
			metrics.TruePositives++
		} else {
			metrics.FalsePositives++
			result.Errors = append(result.Errors, "FP:"+got.Field+" (无金标或值/强度/来源/归属状态不符)")
		}
	}
	for _, want := range result.Expected {
		if !matched[want.Field] {
			metrics.FalseNegatives++
			result.Errors = append(result.Errors, "FN:"+want.Field)
		}
	}
	if len(result.Errors) == 0 {
		metrics.ExactCases++
	}
	metrics.Precision = candidateRatio(metrics.TruePositives, metrics.TruePositives+metrics.FalsePositives)
	metrics.Recall = candidateRatio(metrics.TruePositives, metrics.TruePositives+metrics.FalseNegatives)
}

func candidateRatio(numerator, denominator int) *float64 {
	if denominator == 0 {
		return nil
	}
	value := float64(numerator) / float64(denominator)
	return &value
}

// RunPreferenceCandidateEval 不创建 Service、连接数据库或调用模型。split 可选
// dev/validation/all；暂定金标上的失败保留为实验结果，不触发“修到全绿”。
func RunPreferenceCandidateEval(split string) (*CandidateEvalReport, error) {
	if !slices.Contains([]string{"dev", "validation", "all"}, split) {
		return nil, fmt.Errorf("split 仅允许 dev/validation/all")
	}
	corpora, corpusHash, err := loadPreferenceCandidateCorpora()
	if err != nil {
		return nil, err
	}
	policy, err := preferenceCandidateFiles.ReadFile("preference_candidate.go")
	if err != nil {
		return nil, err
	}
	report := &CandidateEvalReport{SchemaVersion: 1, GeneratedAt: time.Now().UTC(), GoldStatus: "provisional",
		PolicySHA256: fmt.Sprintf("%x", sha256.Sum256(bytes.ReplaceAll(policy, []byte("\r\n"), []byte("\n")))), CorpusSHA256: corpusHash,
		Metrics: map[string]CandidateMetrics{}, Cases: []CandidateEvalResult{},
		Limitations: []string{
			"全合成语料、同一助手编写规则与暂定金标；validation 仅为分组留出，不是盲测或独立人工金标。",
			"lifetime-v1.1 修复了首轮验证暴露的中文锚点边界缺陷；本版 validation 已见，仅用于复测，不能作为新的独立验证。",
			"字段值来自 scripted operations 经真实 Reducer；不测自由文本字段提取、生产授权或模型质量。",
			"候选始终要求用户选择归属并确认；无自动保存、无 recipient 映射、无模型注入。",
			"accepted_proposal 缺产品授权上下文时保守弃权；长期意图词表有作用域、转述与措辞覆盖上限。",
			"precision/recall 为候选字段级精确匹配；值、强度或消息来源错误同时计 FP 与 FN；零分母为 null。",
			"退出码 0 仅代表实验成功执行；暂定金标与当前实验均不构成生产准入。",
		},
	}
	for _, corpus := range corpora {
		if split != "all" && split != corpus.Split {
			continue
		}
		for _, c := range corpus.Cases {
			state, messages, err := candidateCaseState(c)
			if err != nil {
				return nil, err
			}
			for _, policy := range []string{"eligibility-v0", preferenceCandidatePolicy} {
				result := CandidateEvalResult{ID: c.ID, Split: corpus.Split, Category: c.Category, Policy: policy,
					Expected: c.Expected, Rationale: c.Rationale, Decisions: preferenceCandidates(c.ID, state, messages, policy == preferenceCandidatePolicy)}
				key := corpus.Split + "/" + policy
				metrics := report.Metrics[key]
				gradePreferenceCandidates(&result, &metrics)
				report.Metrics[key] = metrics
				report.Cases = append(report.Cases, result)
			}
		}
	}
	return report, nil
}

func WritePreferenceCandidateReport(dir string, report *CandidateEvalReport) error {
	// Mkdir 拒绝覆盖已有报告；父目录可以预先存在。
	if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), append(raw, '\n'), 0644); err != nil {
		return err
	}
	var md strings.Builder
	fmt.Fprintf(&md, "# 偏好候选离线实验（暂定金标）\n\n- policy SHA256: `%s`\n- corpus SHA256: `%s`\n- ready_for_product: false\n\n", report.PolicySHA256, report.CorpusSHA256)
	for _, limitation := range report.Limitations {
		fmt.Fprintf(&md, "- %s\n", limitation)
	}
	md.WriteString("\n| 分组/策略 | 用例全对 | TP | FP | FN | Precision | Recall |\n|---|---|---|---|---|---|---|\n")
	keys := make([]string, 0, len(report.Metrics))
	for key := range report.Metrics {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	percent := func(v *float64) string {
		if v == nil {
			return "n/a"
		}
		return fmt.Sprintf("%.1f%%", *v*100)
	}
	for _, key := range keys {
		m := report.Metrics[key]
		fmt.Fprintf(&md, "| %s | %d/%d | %d | %d | %d | %s | %s |\n", key, m.ExactCases, m.Cases, m.TruePositives, m.FalsePositives, m.FalseNegatives, percent(m.Precision), percent(m.Recall))
	}
	md.WriteString("\n## 失败样例（先列误提取）\n")
	for _, prefix := range []string{"FP:", "FN:"} {
		for _, c := range report.Cases {
			for _, failure := range c.Errors {
				if strings.HasPrefix(failure, prefix) {
					fmt.Fprintf(&md, "\n- %s / %s / %s: %s。暂定依据：%s\n", c.Split, c.Policy, c.ID, failure, c.Rationale)
				}
			}
		}
	}
	return os.WriteFile(filepath.Join(dir, "report.md"), []byte(md.String()), 0644)
}
