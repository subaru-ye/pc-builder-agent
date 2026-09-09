package evalsuite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AuditedRun 保留首跑成绩；当前判卷结果单独存放，不重写历史产物。
type AuditedRun struct {
	Dir            string       `json:"dir"`
	Meta           ReportMeta   `json:"meta"`
	Original       []CaseRecord `json:"-"`
	Current        []CaseRecord `json:"-"`
	RegradedTrials int          `json:"regraded_trials"`
}

func AuditRun(dir string) (AuditedRun, error) {
	m, records, err := ReadVerifiedRun(dir)
	a := AuditedRun{Dir: dir, Meta: m, Original: records}
	if err != nil {
		return a, err
	}
	_, cases, err := ReadSuiteSnapshot(filepath.Join(dir, "cases.json"), m.SuiteSHA256)
	if err != nil {
		return a, err
	}
	byID := map[string]Case{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	for _, r := range records {
		current := r
		if r.RunErr == "" && r.Stage == StageBuild {
			current.Verdict, err = GradeBuild(byID[r.CaseID], r, CurrentGraderVersion)
			if err != nil {
				return a, err
			}
			current.Attribution = Attribute(current.Verdict.Failures)
		}
		if !sameJSON(current.Verdict, r.Verdict) {
			a.RegradedTrials++
		}
		a.Current = append(a.Current, current)
	}
	return a, nil
}

// CheckChangeConditions 允许代码不同，其他可记录条件必须相同。
// Models 是脱敏配置，不能证明供应商隐藏路由、账户额度或内部模型修订相同。
func CheckChangeConditions(a, b ReportMeta) error {
	if a.SuiteSHA256 == "" || a.SuiteSHA256 != b.SuiteSHA256 || a.SnapshotDate == "" || a.SnapshotDate != b.SnapshotDate || a.RequestedSeeds < 3 || a.RequestedSeeds != b.RequestedSeeds || a.HarnessProfile == nil || b.HarnessProfile == nil || !sameJSON(a.HarnessProfile, b.HarnessProfile) {
		return fmt.Errorf("变更回归要求同题、同快照、同机制、至少三次且相同重复数")
	}
	if a.Code == nil || b.Code == nil || a.Code.BinarySHA256 == "" || b.Code.BinarySHA256 == "" {
		return fmt.Errorf("缺少实际二进制标识")
	}
	if a.HarnessProfile.AttemptLimit < 1 || a.HarnessProfile.AttemptLimit > 3 {
		return fmt.Errorf("生成次数配置无效")
	}
	if !sameJSON(a.Models, b.Models) {
		return fmt.Errorf("模型配置变化，不能混作代码回归")
	}
	for _, role := range []string{"builder", "screening", "embedding"} {
		cfg := a.Models[role]
		if cfg == nil || cfg["model"] == nil || cfg["model"] == "" || cfg["provider"] == nil || cfg["base_host"] == nil {
			return fmt.Errorf("缺少 %s 模型配置", role)
		}
		chain, err := json.Marshal(cfg["model_chain"])
		if err != nil || string(chain) != "[]" {
			return fmt.Errorf("%s 必须显式关闭自动模型切换链", role)
		}
	}
	return nil
}

type ChangeTrial struct {
	AUsage        *Usage  `json:"a_usage"`
	BUsage        *Usage  `json:"b_usage"`
	AAttempts     int     `json:"a_build_attempts,omitempty"`
	BAttempts     int     `json:"b_build_attempts,omitempty"`
	Seed          int     `json:"seed"`
	OriginalA     Verdict `json:"original_a"`
	OriginalB     Verdict `json:"original_b"`
	CurrentA      Verdict `json:"current_a"`
	CurrentB      Verdict `json:"current_b"`
	OutputChanged bool    `json:"output_changed"`
	OutputA       any     `json:"output_a,omitempty"`
	OutputB       any     `json:"output_b,omitempty"`
}

type ChangeCase struct {
	ID              string        `json:"id"`
	Title           string        `json:"title"`
	Stage           Stage         `json:"stage"`
	Status          string        `json:"status"`
	OriginalAPasses int           `json:"original_a_passes"`
	OriginalBPasses int           `json:"original_b_passes"`
	APasses         int           `json:"a_passes"`
	BPasses         int           `json:"b_passes"`
	OutputChanges   int           `json:"output_changes"`
	ADurationMS     int64         `json:"a_duration_ms"`
	BDurationMS     int64         `json:"b_duration_ms"`
	AUsage          *Usage        `json:"a_usage"`
	BUsage          *Usage        `json:"b_usage"`
	UsageDelta      *Usage        `json:"usage_delta_b_minus_a"`
	Trials          []ChangeTrial `json:"trials"`
}

type ChangeReport struct {
	GraderVersion     string       `json:"grader_version"`
	Baseline          AuditedRun   `json:"baseline"`
	Candidate         AuditedRun   `json:"candidate"`
	GatePassed        bool         `json:"gate_passed"`
	Regressed         int          `json:"regressed_cases"`
	Improved          int          `json:"improved_cases"`
	RemainingFailures int          `json:"remaining_failed_cases"`
	AUsage            *Usage       `json:"a_usage"`
	BUsage            *Usage       `json:"b_usage"`
	UsageDelta        *Usage       `json:"usage_delta_b_minus_a"`
	Cases             []ChangeCase `json:"cases"`
}

func CompareChangeDirs(aDir, bDir string) (ChangeReport, error) {
	a, err := AuditRun(aDir)
	if err != nil {
		return ChangeReport{}, fmt.Errorf("基线复验: %w", err)
	}
	b, err := AuditRun(bDir)
	if err != nil {
		return ChangeReport{}, fmt.Errorf("候选复验: %w", err)
	}
	return compareChanges(a, b)
}

func measuredSum(records []CaseRecord) *Usage {
	u := &Usage{}
	for _, r := range records {
		if r.Usage == nil {
			return nil
		}
		v := *r.Usage
		if v.ModelCalls < 0 || v.EmbeddingCalls < 0 || v.UsageResponses < 0 || v.UsageResponses > v.ModelCalls || v.InputTokens < 0 || v.OutputTokens < 0 || v.TotalTokens < 0 {
			return nil
		}
		addUsage(u, v)
	}
	return u
}

func usageDelta(a, b *Usage) *Usage {
	if a == nil || b == nil {
		return nil
	}
	d := b.Since(*a)
	return &d
}

func visibleOutput(r CaseRecord) any {
	if r.Screening != nil {
		return r.Screening
	}
	if r.Result != nil {
		return map[string]any{"draft": r.Result.Draft, "decision": r.Result.Decision, "succeeded": r.Result.Succeeded, "result": r.Result.Result, "message": r.Result.Message}
	}
	return r.RunErr
}

func compareChanges(a, b AuditedRun) (ChangeReport, error) {
	out := ChangeReport{GraderVersion: CurrentGraderVersion, Baseline: a, Candidate: b, GatePassed: true}
	if err := CheckChangeConditions(a.Meta, b.Meta); err != nil {
		return out, err
	}
	index := func(rows []CaseRecord) (map[string]map[int]CaseRecord, error) {
		m := map[string]map[int]CaseRecord{}
		for _, r := range rows {
			if m[r.CaseID] == nil {
				m[r.CaseID] = map[int]CaseRecord{}
			}
			if _, exists := m[r.CaseID][r.Seed]; exists || r.Seed < 1 || r.Seed > a.Meta.RequestedSeeds {
				return nil, fmt.Errorf("重复或无效记录")
			}
			m[r.CaseID][r.Seed] = r
		}
		for _, rows := range m {
			if len(rows) != a.Meta.RequestedSeeds {
				return nil, fmt.Errorf("缺少重复记录")
			}
		}
		return m, nil
	}
	ai, err := index(a.Current)
	if err != nil {
		return out, err
	}
	bi, err := index(b.Current)
	if err != nil {
		return out, err
	}
	ao, err := index(a.Original)
	if err != nil {
		return out, err
	}
	bo, err := index(b.Original)
	if err != nil {
		return out, err
	}
	if len(ai) == 0 || len(ai) != len(bi) || len(ai) != len(ao) || len(ai) != len(bo) {
		return out, fmt.Errorf("两组题目数量不一致")
	}
	ids := make([]string, 0, len(ai))
	for id := range ai {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if bi[id] == nil || ao[id] == nil || bo[id] == nil {
			return out, fmt.Errorf("缺少 %s", id)
		}
		c := ChangeCase{ID: id, Title: ai[id][1].Title, Stage: ai[id][1].Stage}
		var ars, brs []CaseRecord
		for seed := 1; seed <= a.Meta.RequestedSeeds; seed++ {
			ra, rb := ai[id][seed], bi[id][seed]
			if ra.Stage != rb.Stage || !sameJSON(ra.Snapshot, rb.Snapshot) || !sameJSON(ra.Requirement, rb.Requirement) || ra.Snapshot.SnapshotDate != a.Meta.SnapshotDate {
				return out, fmt.Errorf("%s/%d 输入、完整目录或快照不一致", id, seed)
			}
			trial := ChangeTrial{Seed: seed, OriginalA: ao[id][seed].Verdict, OriginalB: bo[id][seed].Verdict, CurrentA: ra.Verdict, CurrentB: rb.Verdict}
			trial.AUsage, trial.BUsage = ra.Usage, rb.Usage
			if ra.Result != nil {
				trial.AAttempts = ra.Result.Attempts
			}
			if rb.Result != nil {
				trial.BAttempts = rb.Result.Attempts
			}
			if trial.OriginalA.Passed {
				c.OriginalAPasses++
			}
			if trial.OriginalB.Passed {
				c.OriginalBPasses++
			}
			if ra.Verdict.Passed {
				c.APasses++
			}
			if rb.Verdict.Passed {
				c.BPasses++
			}
			x, y := visibleOutput(ra), visibleOutput(rb)
			trial.OutputChanged = !sameJSON(x, y)
			if trial.OutputChanged {
				c.OutputChanges++
				trial.OutputA = x
				trial.OutputB = y
			}
			c.Trials = append(c.Trials, trial)
			c.ADurationMS += ra.DurationMS
			c.BDurationMS += rb.DurationMS
			ars = append(ars, ra)
			brs = append(brs, rb)
		}
		c.AUsage, c.BUsage = measuredSum(ars), measuredSum(brs)
		c.UsageDelta = usageDelta(c.AUsage, c.BUsage)
		switch {
		case c.BPasses < c.APasses:
			c.Status = "regressed"
			out.Regressed++
		case c.BPasses > c.APasses:
			c.Status = "improved"
			out.Improved++
		case c.BPasses < a.Meta.RequestedSeeds:
			c.Status = "persistent_failure"
		case c.OutputChanges > 0:
			c.Status = "output_changed"
		default:
			c.Status = "unchanged"
		}
		if c.BPasses != a.Meta.RequestedSeeds {
			out.RemainingFailures++
			out.GatePassed = false
		}
		out.Cases = append(out.Cases, c)
	}
	out.AUsage, out.BUsage = measuredSum(a.Current), measuredSum(b.Current)
	out.UsageDelta = usageDelta(out.AUsage, out.BUsage)
	return out, nil
}

// WriteChangeReport writes new files only. Original runs are never promoted or rewritten.
func WriteChangeReport(dir string, r ChangeReport) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "comparison.json"), append(raw, '\n'), 0o644); err != nil {
		return err
	}
	var s strings.Builder
	fmt.Fprintf(&s, "# 变更回归报告\n\n门禁通过：%v；退步 %d 题，改善 %d 题，当前仍有失败 %d 题。\n\n", r.GatePassed, r.Regressed, r.Improved, r.RemainingFailures)
	fmt.Fprintf(&s, "基线：%s\n\n候选：%s\n\n同一题库 %s、快照 %s、每题 %d 次，统一按 %s 复核。历史原成绩改变：基线 %d 次，候选 %d 次；复核不覆盖原成绩。\n\n", r.Baseline.Dir, r.Candidate.Dir, r.Baseline.Meta.SuiteVersion, r.Baseline.Meta.SnapshotDate, r.Baseline.Meta.RequestedSeeds, r.GraderVersion, r.Baseline.RegradedTrials, r.Candidate.RegradedTrials)
	s.WriteString("输出变化包含措辞、选件和追问变化，不等于质量退步。断言证据说明观察到哪里不符合要求，不自动证明代码变更是根因。重复编号不是供应商的随机 seed。\n\n")
	s.WriteString("固定的是已记录条件；完整目录快照不包含语义向量索引版本，供应商服务修订、网络及缓存状态也未完全固定。\n\n")
	if r.UsageDelta == nil {
		s.WriteString("用量记录不完整，调用和 token 差异未知。\n\n")
	} else {
		fmt.Fprintf(&s, "模型逻辑调用：%d → %d（%+d）；Embedding：%d → %d（%+d）。已知 token：%d → %d（%+d）；有 token 记录的模型响应：%d/%d → %d/%d。\n\n", r.AUsage.ModelCalls, r.BUsage.ModelCalls, r.UsageDelta.ModelCalls, r.AUsage.EmbeddingCalls, r.BUsage.EmbeddingCalls, r.UsageDelta.EmbeddingCalls, r.AUsage.TotalTokens, r.BUsage.TotalTokens, r.UsageDelta.TotalTokens, r.AUsage.UsageResponses, r.AUsage.ModelCalls, r.BUsage.UsageResponses, r.BUsage.ModelCalls)
	}
	s.WriteString("逻辑调用不含内部 HTTP 重试；缺失 usage 不当零 token，Embedding token 未计量。以上不能换算为费用，也不是生产延迟统计。\n\n| 题目 | 状态 | 原成绩 A→B | 同口径通过 A→B | 输出变化次数 | 调用增减 | 耗时增减 ms |\n|---|---|---|---|---|---|---|\n")
	labels := map[string]string{"regressed": "退步", "improved": "改善", "persistent_failure": "持续失败", "output_changed": "分数不变、输出变化", "unchanged": "未变化"}
	for _, c := range r.Cases {
		calls := "未知"
		if c.UsageDelta != nil {
			calls = fmt.Sprintf("%+d", c.UsageDelta.ModelCalls)
		}
		fmt.Fprintf(&s, "| %s | %s | %d→%d | %d→%d / %d | %d | %s | %+d |\n", c.ID, labels[c.Status], c.OriginalAPasses, c.OriginalBPasses, c.APasses, c.BPasses, r.Baseline.Meta.RequestedSeeds, c.OutputChanges, calls, c.BDurationMS-c.ADurationMS)
	}
	s.WriteString("\n## 失败证据\n\n")
	for _, c := range r.Cases {
		for _, tr := range c.Trials {
			for _, side := range []struct {
				name string
				v    Verdict
			}{{"基线", tr.CurrentA}, {"候选", tr.CurrentB}} {
				for _, f := range side.v.Failures {
					fmt.Fprintf(&s, "- %s / 第 %d 次 / %s / %s：%s\n", c.ID, tr.Seed, side.name, f.ID, strings.ReplaceAll(f.Detail, "\n", " "))
				}
			}
		}
	}
	s.WriteString("\n逐题的新旧断言、变更输出全文及用量保存在 [comparison.json](comparison.json)。基线与候选目录的 results.jsonl 保留完整首跑证据。\n")
	return os.WriteFile(filepath.Join(dir, "report.md"), []byte(s.String()), 0o644)
}
