package planningeval

import (
	"errors"
	"sort"

	"github.com/subaru-ye/pc-builder-agent/internal/decision"
)

// errClass extracts a stable provider error class without coupling the
// harness to a concrete provider package.
func errClass(err error) string {
	var c interface{ ErrorClass() string }
	if errors.As(err, &c) {
		return c.ErrorClass()
	}
	return "unknown"
}

func truncateErr(err error) string {
	s := []rune(err.Error())
	if len(s) > 300 {
		s = s[:300]
	}
	return string(s)
}

type intentExample struct {
	caseID     string
	confidence float64
	selected   float64
	predicted  string
	correct    bool
}

// buildIntentReport aggregates per-step observations. It never changes pass/fail
// outcomes: a failing classifier is recorded, not scored as product failure.
func buildIntentReport(suite Suite, report *Report, models Models) *IntentReport {
	obs := models.Intent
	if obs == nil {
		return nil
	}
	split := map[string]string{}
	if suite.IntentSplit != nil {
		for _, id := range suite.IntentSplit.Calibration {
			split[id] = "calibration"
		}
		for _, id := range suite.IntentSplit.Holdout {
			split[id] = "holdout"
		}
	}
	r := &IntentReport{Limitations: []string{
		"Confidence 不是正确率概率；阈值曲线只是选择性精度/覆盖率的事实记录。",
		"与现有 Screening 决策的一致率不是正确率，两者分开报告。",
		"correct_share 是假设存在确定性路由资格策略时的上界估计，不是已实现的生产调用削减。",
	}}
	calibration, holdout, all := []intentExample{}, []intentExample{}, []intentExample{}
	responseModels := map[string]int{}
	var durations []int64
	unassigned := false
	for _, c := range report.Cases {
		for i := range c.Steps {
			o := c.Steps[i].Intent
			if o == nil {
				continue
			}
			r.Calls++
			if o.ErrorClass != "" {
				r.Failures++
				if r.Errors == nil {
					r.Errors = map[string]int{}
				}
				r.Errors[o.ErrorClass]++
				continue
			}
			r.Successes++
			r.InputTokens += int64(o.InputTokens)
			r.OutputTokens += int64(o.OutputTokens)
			durations = append(durations, o.DurationMS)
			responseModels[o.ResponseModel]++
			if r.RequestedModel == "" {
				r.RequestedModel = o.RequestedModel
			}
			if o.Prediction == string(decision.IntentAmbiguous) {
				r.Ambiguous++
			}
			if o.GroundTruth != "" {
				example := intentExample{
					caseID:     c.ID,
					confidence: o.Confidence,
					selected:   o.SelectedProbability,
					predicted:  o.Prediction,
					correct:    o.Correct,
				}
				all = append(all, example)
				switch split[c.ID] {
				case "calibration":
					calibration = append(calibration, example)
				case "holdout":
					holdout = append(holdout, example)
				default:
					unassigned = true
				}
				if r.Confusion == nil {
					r.Confusion = map[string]map[string]int{}
				}
				if r.Confusion[o.GroundTruth] == nil {
					r.Confusion[o.GroundTruth] = map[string]int{}
				}
				r.Confusion[o.GroundTruth][o.Prediction]++
			} else {
				r.PredictedUnlabelled++
			}
			if o.ExistingDecision != "" {
				r.AgreementTotal++
				if o.Agreement {
					r.AgreementCount++
				}
			}
		}
	}
	r.Labelled = len(all)
	r.CalibrationLabelled, r.HoldoutLabelled = len(calibration), len(holdout)
	if unassigned {
		r.Limitations = append(r.Limitations, "部分有标注步骤的用例未列入 intent_split，只计入全集曲线，不参与校准/保留分割。")
	}
	if r.Labelled > 0 {
		correct := 0
		for _, e := range all {
			if e.correct {
				correct++
			}
		}
		accuracy := float64(correct) / float64(r.Labelled)
		r.Accuracy = &accuracy
	}
	if len(durations) > 0 {
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		r.LatencyP50MS = durations[len(durations)/2]
		r.LatencyP95MS = durations[int(0.95*float64(len(durations)-1))]
	}
	if len(responseModels) > 0 {
		r.ResponseModels = responseModels
	}
	if models.IntentInputPricePerMTok > 0 || models.IntentOutputPricePerMTok > 0 {
		cost := float64(r.InputTokens)/1e6*models.IntentInputPricePerMTok + float64(r.OutputTokens)/1e6*models.IntentOutputPricePerMTok
		r.CashCost = &cost
	}
	// Threshold grids come from the calibration split so the holdout curve is
	// evaluated exactly once at preselected points.
	scores := func(examples []intentExample, policy string) []float64 {
		grid := map[float64]bool{0: true}
		for _, e := range examples {
			if policy == "confidence" {
				grid[e.confidence] = true
			} else {
				grid[e.selected] = true
			}
		}
		values := make([]float64, 0, len(grid))
		for v := range grid {
			values = append(values, v)
		}
		sort.Sort(sort.Reverse(sort.Float64Slice(values)))
		return values
	}
	curve := func(policy, splitName string, examples []intentExample, grid []float64) IntentThresholdCurve {
		c := IntentThresholdCurve{Policy: policy, Split: splitName}
		for _, t := range grid {
			c.Points = append(c.Points, thresholdPoint(t, policy, examples))
		}
		return c
	}
	for _, policy := range []string{"confidence", "selected_probability"} {
		if suite.IntentSplit != nil {
			grid := scores(calibration, policy)
			r.Thresholds = append(r.Thresholds,
				curve(policy, "calibration", calibration, grid),
				curve(policy, "holdout", holdout, grid),
			)
		} else {
			r.Thresholds = append(r.Thresholds, curve(policy, "all", all, scores(all, policy)))
			r.Limitations = append(r.Limitations, "套件未提供 intent_split：阈值在同一集合内选择并报告，不具备分割外泛化声明。")
		}
	}
	return r
}

// thresholdPoint implements Precision/Coverage/Fallback for one policy value.
// `ambiguous` predictions are refusals and never join the accepted set.
func thresholdPoint(t float64, policy string, examples []intentExample) IntentThresholdPoint {
	point := IntentThresholdPoint{Threshold: t}
	for _, e := range examples {
		score := e.confidence
		if policy == "selected_probability" {
			score = e.selected
		}
		if score < t || e.predicted == string(decision.IntentAmbiguous) {
			continue
		}
		point.Accepted++
		if e.correct {
			point.Correct++
		}
	}
	point.Coverage = float64(point.Accepted) / float64(len(examples))
	point.Fallback = 1 - point.Coverage
	if point.Accepted > 0 {
		precision := float64(point.Correct) / float64(point.Accepted)
		point.Precision = &precision
		point.CorrectShare = precision * point.Coverage
	}
	return point
}
