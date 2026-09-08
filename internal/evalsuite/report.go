package evalsuite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ReportMeta 描述一次评估运行的环境口径;结论只对"用例分布 × 快照批次 × 模型"
// 负责(P13 §3.5)。
type ReportMeta struct {
	RecordSchemaVersion int                       `json:"record_schema_version,omitempty"` // 1: screening 原文必存
	ReplaySkipped       []string                  `json:"replay_skipped,omitempty"`
	SuiteVersion        string                    `json:"suite_version,omitempty"`
	SuiteSHA256         string                    `json:"suite_sha256,omitempty"`
	RequestedSeeds      int                       `json:"requested_seeds,omitempty"`
	Code                *CodeIdentity             `json:"code,omitempty"`
	ReplayCode          *CodeIdentity             `json:"replay_code,omitempty"`
	Models              map[string]map[string]any `json:"configured_models,omitempty"`
	ScreeningModel      string                    `json:"screening_model,omitempty"`
	SnapshotDate        string                    `json:"snapshot_date"`
	BuilderModel        string                    `json:"builder_model"`
	EmbeddingModel      string                    `json:"embedding_model"`
	GeneratedAt         time.Time                 `json:"generated_at"`
	Mode                string                    `json:"mode"` // run | replay
}

type CodeIdentity struct {
	CheckoutCommit string `json:"checkout_commit,omitempty"`
	CheckoutDirty  *bool  `json:"checkout_dirty,omitempty"`
	BinarySHA256   string `json:"binary_sha256,omitempty"`
	GoVersion      string `json:"go_version,omitempty"`
}

// Summary 是一次评估的汇总。记录级指标按 (case, seed) 统计;用例级 Pass^k =
// 该用例全部 seed pass 且零 veto、无 data-error(P13 §3.3/§七)。
type Summary struct {
	Meta         ReportMeta   `json:"meta"`
	Seeds        int          `json:"seeds"`
	CaseCount    int          `json:"case_count"`
	Total        int          `json:"total"` // 记录数 = 用例数 × seeds
	Passed       int          `json:"passed"`
	Failed       int          `json:"failed"`
	DataErrors   int          `json:"data_errors"`
	PassRate     float64      `json:"pass_rate"`   // 记录级
	PassKRate    float64      `json:"pass_k_rate"` // 用例级 Pass^k
	PassKPassed  int          `json:"pass_k_passed"`
	VetoTriggers int          `json:"veto_triggers"`
	Records      []CaseRecord `json:"records"`
}

// Summarize 汇总逐用例记录(记录按首见顺序分组)。
func Summarize(meta ReportMeta, records []CaseRecord) Summary {
	s := Summary{Meta: meta, Records: records, Total: len(records)}
	// 用例顺序:按首条记录出现的顺序(即用例加载顺序)。
	order := []string{}
	byCase := map[string][]CaseRecord{}
	for _, record := range records {
		if _, ok := byCase[record.CaseID]; !ok {
			order = append(order, record.CaseID)
		}
		byCase[record.CaseID] = append(byCase[record.CaseID], record)
	}
	seenSeeds := map[int]bool{}
	for _, record := range records {
		seenSeeds[record.Seed] = true
		switch {
		case record.Verdict.DataError:
			s.DataErrors++
		case record.Verdict.Passed:
			s.Passed++
		default:
			s.Failed++
		}
		for _, failure := range record.Verdict.Failures {
			if failure.Veto {
				s.VetoTriggers++
			}
		}
	}
	s.CaseCount = len(order)
	s.Seeds = len(seenSeeds)

	denominator := 0
	for _, caseID := range order {
		records := byCase[caseID]
		allPassed, hasDataError, hasVeto := true, false, false
		for _, record := range records {
			switch {
			case record.Verdict.DataError:
				hasDataError = true
			case !record.Verdict.Passed:
				allPassed = false
			}
			for _, failure := range record.Verdict.Failures {
				if failure.Veto {
					hasVeto = true
				}
			}
		}
		switch {
		case hasDataError:
			// 该用例口径无法验证,不进 Pass^k 分母
		default:
			denominator++
			if allPassed && !hasVeto {
				s.PassKPassed++
			}
		}
	}
	if effective := s.Total - s.DataErrors; effective > 0 {
		s.PassRate = float64(s.Passed) / float64(effective)
	}
	if denominator > 0 {
		s.PassKRate = float64(s.PassKPassed) / float64(denominator)
	}
	return s
}

// AllGreen 报告是否全绿:无失败且无 data-error(data-error 意味着口径无法验证,
// 门禁必须显式失败,不能静默放行)。seeds>1 时记录级全绿即等价 Pass^k 全绿。
func (s Summary) AllGreen() bool { return s.Failed == 0 && s.DataErrors == 0 }

// WriteJSONL 把逐用例记录写为 results.jsonl(每 (case, seed) 一行)。
func (s Summary) WriteJSONL(dir string) error {
	file, err := os.Create(filepath.Join(dir, "results.jsonl"))
	if err != nil {
		return fmt.Errorf("evalsuite: 创建 results.jsonl 失败: %w", err)
	}
	defer func() { _ = file.Close() }() // 写入失败也释放句柄；成功路径显式检查关闭错误。
	enc := json.NewEncoder(file)
	for _, record := range s.Records {
		if err := enc.Encode(record); err != nil {
			return fmt.Errorf("evalsuite: 写入用例 %s 失败: %w", record.CaseID, err)
		}
	}
	return file.Close()
}

// WriteReport 生成 Markdown 汇总报告。
func (s Summary) WriteReport(dir string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# 评估报告(%s)\n\n", s.Meta.Mode)
	if s.Meta.SuiteVersion != "" {
		fmt.Fprintf(&b, "- 评估集:%s;清单 SHA256:%s;完整题目见 cases.json\n", s.Meta.SuiteVersion, s.Meta.SuiteSHA256)
	} else {
		b.WriteString("- 历史产物:未记录评估集版本与完整题目副本\n")
	}
	if s.Meta.Code != nil {
		fmt.Fprintf(&b, "- 首跑代码:checkout=%s;binary SHA256=%s（工作树状态及模型参数见 meta.json）\n", s.Meta.Code.CheckoutCommit, s.Meta.Code.BinarySHA256)
	}
	if s.Meta.ReplayCode != nil {
		fmt.Fprintf(&b, "- 本次重判程序 SHA256:%s\n", s.Meta.ReplayCode.BinarySHA256)
	}
	if s.Meta.ScreeningModel != "" {
		fmt.Fprintf(&b, "- screening:%s（记录配置，不等于链内逐请求实际模型）\n", s.Meta.ScreeningModel)
	}
	fmt.Fprintf(&b, "- 快照批次:%s\n", s.Meta.SnapshotDate)
	fmt.Fprintf(&b, "- builder:%s;embedding:%s\n", s.Meta.BuilderModel, s.Meta.EmbeddingModel)
	fmt.Fprintf(&b, "- 生成时间:%s;seeds=%d\n", s.Meta.GeneratedAt.Format("2006-01-02 15:04:05"), s.Seeds)
	fmt.Fprintf(&b, "- 用例 %d 条 × %d seed:记录级通过 %d / 失败 %d / data-error %d,**Pass@1 = %.1f%%**;用例级 **Pass^%d = %.1f%%**(%d/%d);veto 触发 %d 次\n\n",
		s.CaseCount, s.Seeds, s.Passed, s.Failed, s.DataErrors, s.PassRate*100,
		s.Seeds, s.PassKRate*100, s.PassKPassed, s.CaseCount-s.countDataErrorCases(), s.VetoTriggers)
	if s.Meta.Mode == "replay" {
		b.WriteString("> replay 模式:结论由首跑轨迹重放断言得出,零模型调用。\n\n")
		if len(s.Meta.ReplaySkipped) > 0 {
			fmt.Fprintf(&b, "> 部分重放:以下历史 screening 记录缺少回复原文,仅保留旧结论,不计作已复验:%s。\n\n", strings.Join(s.Meta.ReplaySkipped, ", "))
		}
	}

	b.WriteString("| 用例 | 阶段 | 结论 | per-seed | 耗时(ms) | 归因与明细 |\n|---|---|---|---|---:|---|\n")
	for _, caseID := range s.caseOrder() {
		records := s.recordsOf(caseID)
		first := records[0]
		outcome, perSeed, detail := describeCase(records)
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %s |\n",
			caseID, first.Stage, outcome, perSeed, first.DurationMS, detail)
	}
	fmt.Fprintf(&b, "\n> 本轮 %d 条样本用于方向性回归；不支持精细模型选型结论。\n", s.CaseCount)

	if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("evalsuite: 写入 report.md 失败: %w", err)
	}
	return nil
}

func (s Summary) caseOrder() []string {
	seen := map[string]bool{}
	var order []string
	for _, record := range s.Records {
		if !seen[record.CaseID] {
			seen[record.CaseID] = true
			order = append(order, record.CaseID)
		}
	}
	return order
}

func (s Summary) recordsOf(caseID string) []CaseRecord {
	var out []CaseRecord
	for _, record := range s.Records {
		if record.CaseID == caseID {
			out = append(out, record)
		}
	}
	return out
}

func (s Summary) countDataErrorCases() int {
	seen := map[string]bool{}
	for _, record := range s.Records {
		if record.Verdict.DataError {
			seen[record.CaseID] = true
		}
	}
	return len(seen)
}

func describeCase(records []CaseRecord) (outcome, perSeed, detail string) {
	allPassed, hasDataError := true, false
	var parts []string
	var attributionParts []string
	for _, record := range records {
		mark := "✅"
		switch {
		case record.Verdict.DataError:
			mark = "⚠️"
			hasDataError = true
			allPassed = false
		case !record.Verdict.Passed:
			mark = "❌"
			allPassed = false
		}
		parts = append(parts, fmt.Sprintf("seed%d %s", record.Seed, mark))
		if record.RunErr != "" {
			detail := record.RunErr
			if len([]rune(detail)) > 160 {
				detail = string([]rune(detail)[:160]) + "…"
			}
			parts = append(parts, "err:"+detail)
		}
		for _, attribution := range record.Attribution {
			tag := attribution.Code
			if attribution.Primary {
				tag += "(主因)"
			}
			attributionParts = append(attributionParts, tag)
		}
		for _, failure := range record.Verdict.Failures {
			text := fmt.Sprintf("**%s %s**:%s", failure.ID, failure.Name, failure.Detail)
			if failure.Veto {
				text = "🚫" + text
			}
			if len([]rune(text)) > 200 {
				text = string([]rune(text)[:200]) + "…"
			}
			parts = append(parts, strings.ReplaceAll(text, "\n", " "))
		}
	}
	sort.Strings(attributionParts) // 稳定输出
	switch {
	case hasDataError:
		outcome = "⚠️ data-error"
	case allPassed:
		outcome = "✅ pass"
	default:
		outcome = "❌ fail"
	}
	perSeed = strings.Join(parts, "<br>")
	if len(attributionParts) > 0 {
		detail = "归因:" + strings.Join(attributionParts, ",") + "<br>" + detail
	}
	return outcome, perSeed, detail
}

// ReadRecords 从 results.jsonl 读回逐用例记录(replay 入口)。
func ReadRecords(path string) ([]CaseRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("evalsuite: 读取 %s 失败: %w", path, err)
	}
	var records []CaseRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record CaseRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, fmt.Errorf("evalsuite: 解析 results.jsonl 行失败: %w", err)
		}
		records = append(records, record)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("evalsuite: %s 中没有任何用例记录", path)
	}
	return records, nil
}
