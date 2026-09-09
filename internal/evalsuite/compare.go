package evalsuite

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
)

type PairedCase struct {
	ID             string          `json:"id"`
	APasses        int             `json:"a_passes"`
	BPasses        int             `json:"b_passes"`
	Repeats        int             `json:"repeats"`
	AOnly          int             `json:"a_only_trials"`
	BOnly          int             `json:"b_only_trials"`
	BothPass       int             `json:"both_pass_trials"`
	BothFail       int             `json:"both_fail_trials"`
	ADurationMS    int64           `json:"a_duration_ms"`
	BDurationMS    int64           `json:"b_duration_ms"`
	AUsage         Usage           `json:"a_usage"`
	BUsage         Usage           `json:"b_usage"`
	UsageMeasured  bool            `json:"usage_measured"`
	SemanticTrials []SemanticTrial `json:"semantic_trials,omitempty"`
}
type Comparison struct {
	Factor            string       `json:"factor"`
	Stage             Stage        `json:"evaluated_stage"`
	ExcludedCaseIDs   []string     `json:"excluded_other_stage_cases,omitempty"`
	A                 ReportMeta   `json:"a"`
	B                 ReportMeta   `json:"b"`
	Cases             []PairedCase `json:"cases"`
	MeanPassRateDelta float64      `json:"mean_pass_rate_delta_b_minus_a"`
	Bootstrap95       [2]float64   `json:"case_cluster_bootstrap_95"`
}

// Compare 检查同题、同快照、同二进制、仅一个预定变量发生变化，然后按用例配对。
// bootstrap 以用例为抽样单位，重复运行不冒充独立题目；区间只是本题库上的描述。
func Compare(a ReportMeta, ar []CaseRecord, b ReportMeta, br []CaseRecord, factor string) (Comparison, error) {
	out := Comparison{Factor: factor, A: a, B: b}
	if a.GraderVersion != b.GraderVersion {
		return out, fmt.Errorf("判卷版本不同，请先显式采用同一版本复核；不能混作单变量实验")
	}
	out.Stage = StageBuild
	if factor == "screening" {
		out.Stage = StageScreening
	}
	if a.SuiteSHA256 == "" || a.SuiteSHA256 != b.SuiteSHA256 || a.SnapshotDate != b.SnapshotDate || a.RequestedSeeds < 3 || a.RequestedSeeds != b.RequestedSeeds || a.Code == nil || b.Code == nil || a.Code.BinarySHA256 == "" || a.Code.BinarySHA256 != b.Code.BinarySHA256 || a.HarnessProfile == nil || b.HarnessProfile == nil || len(a.Models) == 0 || len(b.Models) == 0 {
		return out, fmt.Errorf("对照要求相同题库、快照、二进制和至少三次重复，且两组配置均已记录")
	}
	pa, pb := *a.HarnessProfile, *b.HarnessProfile
	if pa.AttemptLimit < 1 || pa.AttemptLimit > 3 || pb.AttemptLimit < 1 || pb.AttemptLimit > 3 {
		return out, fmt.Errorf("生成次数配置必须为 1–3")
	}
	ma, mb := a.Models, b.Models
	for _, models := range []map[string]map[string]any{ma, mb} {
		for _, cfg := range models {
			chain := reflect.ValueOf(cfg["model_chain"])
			if chain.IsValid() && chain.Kind() == reflect.Slice && chain.Len() > 0 {
				return out, fmt.Errorf("受控对照不能启用模型自动切换链")
			}
		}
	}
	changed := false
	switch factor {
	case "repair":
		changed = pa.AttemptLimit != pb.AttemptLimit
		pa.AttemptLimit = pb.AttemptLimit
	case "semantic":
		changed = pa.Semantic != pb.Semantic
		pa.Semantic = pb.Semantic
	case "builder", "screening":
		if ma[factor] == nil || mb[factor] == nil {
			return out, fmt.Errorf("缺少角色模型配置")
		}
		changed = !reflect.DeepEqual(ma[factor]["model"], mb[factor]["model"])
		// 复制后仅允许模型名变化，避免修改输入元数据。
		clone := make(map[string]map[string]any, len(ma))
		for role, values := range ma {
			clone[role] = make(map[string]any, len(values))
			for key, value := range values {
				clone[role][key] = value
			}
		}
		ma = clone
		ma[factor]["model"] = mb[factor]["model"]
	default:
		return out, fmt.Errorf("factor 仅支持 repair/semantic/builder/screening")
	}
	if !changed || pa != pb || !reflect.DeepEqual(ma, mb) {
		return out, fmt.Errorf("不是仅改变 %s 的受控对照", factor)
	}
	index := func(records []CaseRecord) (map[string]map[int]CaseRecord, error) {
		m := map[string]map[int]CaseRecord{}
		for _, r := range records {
			if m[r.CaseID] == nil {
				m[r.CaseID] = map[int]CaseRecord{}
			}
			if _, ok := m[r.CaseID][r.Seed]; ok || r.Seed < 1 || r.Seed > a.RequestedSeeds {
				return nil, fmt.Errorf("重复或无效的用例执行")
			}
			m[r.CaseID][r.Seed] = r
		}
		for _, rows := range m {
			if len(rows) != a.RequestedSeeds {
				return nil, fmt.Errorf("缺少重复执行")
			}
		}
		return m, nil
	}
	ai, err := index(ar)
	if err != nil {
		return out, err
	}
	bi, err := index(br)
	if err != nil {
		return out, err
	}
	if len(ai) == 0 || len(ai) != len(bi) {
		return out, fmt.Errorf("两组用例数量不一致")
	}
	ids := make([]string, 0, len(ai))
	for id := range ai {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var deltas []float64
	for _, id := range ids {
		if bi[id] == nil {
			return out, fmt.Errorf("对照 B 缺少用例 %s", id)
		}
		pair := PairedCase{ID: id, Repeats: a.RequestedSeeds, UsageMeasured: true}
		for seed := 1; seed <= a.RequestedSeeds; seed++ {
			ra, rb := ai[id][seed], bi[id][seed]
			if factor == "semantic" && ra.Stage == StageBuild {
				pair.SemanticTrials = append(pair.SemanticTrials, semanticTrial(ra, rb))
			}
			x, _ := json.Marshal(ra.Snapshot)
			y, _ := json.Marshal(rb.Snapshot)
			hx, _ := JSONHash(x)
			hy, _ := JSONHash(y)
			if ra.Stage != rb.Stage || hx != hy {
				return out, fmt.Errorf("%s 输入快照或阶段不一致", id)
			}
			if ra.Verdict.Passed {
				pair.APasses++
			}
			if rb.Verdict.Passed {
				pair.BPasses++
			}
			switch {
			case ra.Verdict.Passed && rb.Verdict.Passed:
				pair.BothPass++
			case ra.Verdict.Passed:
				pair.AOnly++
			case rb.Verdict.Passed:
				pair.BOnly++
			default:
				pair.BothFail++
			}
			pair.ADurationMS += ra.DurationMS
			pair.BDurationMS += rb.DurationMS
			if ra.Usage == nil || rb.Usage == nil {
				pair.UsageMeasured = false
			} else {
				addUsage(&pair.AUsage, *ra.Usage)
				addUsage(&pair.BUsage, *rb.Usage)
			}
		}
		// 复验全部记录，但只用该变量实际作用的阶段计算效果；避免初筛波动冒充修复收益。
		if ai[id][1].Stage != out.Stage {
			out.ExcludedCaseIDs = append(out.ExcludedCaseIDs, id)
			continue
		}
		delta := float64(pair.BPasses-pair.APasses) / float64(pair.Repeats)
		deltas = append(deltas, delta)
		out.MeanPassRateDelta += delta
		out.Cases = append(out.Cases, pair)
	}
	if len(deltas) == 0 {
		return out, fmt.Errorf("没有 %s 阶段可对照的用例", out.Stage)
	}
	out.MeanPassRateDelta /= float64(len(deltas))
	rng := rand.New(rand.NewSource(20260909))
	samples := make([]float64, 10000)
	for i := range samples {
		for range deltas {
			samples[i] += deltas[rng.Intn(len(deltas))]
		}
		samples[i] /= float64(len(deltas))
	}
	sort.Float64s(samples)
	out.Bootstrap95 = [2]float64{samples[249], samples[9749]}
	return out, nil
}

func addUsage(a *Usage, b Usage) {
	a.ModelCalls += b.ModelCalls
	a.EmbeddingCalls += b.EmbeddingCalls
	a.UsageResponses += b.UsageResponses
	a.InputTokens += b.InputTokens
	a.OutputTokens += b.OutputTokens
	a.TotalTokens += b.TotalTokens
}
