package evaldesk

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

var secretHeaders = regexp.MustCompile(`(?im)\b(?:authorization|proxy-authorization|cookie|set-cookie)\s*:\s*[^\r\n]+`)
var quotedSecrets = regexp.MustCompile(`(?i)(["']?(?:api[_-]?key|key|authorization|proxy-authorization|cookie|set-cookie|password|passwd|token|access[_-]?token|refresh[_-]?token|client[_-]?secret|secret|PG_DSN)["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*')`)
var secretAssignments = regexp.MustCompile(`(?i)(["']?(?:api[_-]?key|key|authorization|cookie|set-cookie|password|passwd|token|access[_-]?token|refresh[_-]?token|client[_-]?secret|secret|PG_DSN)["']?\s*[:=]\s*["']?)([^\s,"'<>;&]+)`)
var bearerSecrets = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
var keySecrets = regexp.MustCompile(`\b(?:sk-|sk_)[A-Za-z0-9_-]{8,}`)
var urlCredentials = regexp.MustCompile(`(?i)(?:https?|postgres(?:ql)?|redis)://[^\s/@]+:[^\s/@]+@[^\s]+`)

// Text is evidence, never HTML. Credential-shaped values are redacted in every
// projected string; unstructured execution errors and arbitrary metadata are omitted.
func clean(text string) string {
	text = secretHeaders.ReplaceAllString(text, "[凭据头已隐藏]")
	text = quotedSecrets.ReplaceAllString(text, "${1}[已隐藏]")
	text = bearerSecrets.ReplaceAllString(text, "Bearer [已隐藏]")
	text = secretAssignments.ReplaceAllString(text, "${1}[已隐藏]")
	text = keySecrets.ReplaceAllString(text, "[已隐藏]")
	return urlCredentials.ReplaceAllString(text, "[连接信息已隐藏]")
}

func pretty(v any) string {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "未记录"
	}
	return clean(string(raw))
}

func stageLabel(stage evalsuite.Stage) string {
	if stage == evalsuite.StageBuild || stage == evalsuite.StageScreening {
		return string(stage)
	}
	return "unknown"
}

func verdict(v evalsuite.Verdict) Verdict {
	out := Verdict{Passed: v.Passed, DataError: v.DataError, Failures: []Failure{}}
	for _, f := range v.Failures {
		detail := clean(f.Detail)
		if f.ID == "RUN" {
			detail = "执行异常或未执行；原始错误日志不对前端开放"
		}
		out.Failures = append(out.Failures, Failure{ID: clean(f.ID), Name: clean(f.Name), Detail: detail, Veto: f.Veto})
	}
	return out
}

func (s *Store) Cases(aID, bID, caseID string) (CaseResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := CaseResponse{CaseID: clean(caseID)}
	if caseID == "" || len(caseID) > 200 {
		return out, errNotFound
	}
	if aID != "" {
		a, err := s.lookup(aID)
		if err != nil {
			return out, err
		}
		out.Baseline = caseSide(a, caseID)
	}
	if bID != "" {
		b, err := s.lookup(bID)
		if err != nil {
			return out, err
		}
		out.Candidate = caseSide(b, caseID)
	}
	if out.Baseline == nil && out.Candidate == nil {
		return out, errNotFound
	}
	return out, nil
}

func caseSide(run *savedRun, id string) *CaseSide {
	frozen, hasFrozen := run.cases[id]
	records := filterRecords(run.records, map[string]bool{id: true})
	if !hasFrozen && len(records) == 0 {
		return nil
	}
	out := &CaseSide{RunID: run.summary.ID, CaseID: clean(id), InputFrozen: hasFrozen, Inputs: []Input{}, Trials: []Trial{}, Notes: []string{}}
	if hasFrozen {
		out.Title = clean(frozen.Title)
		out.Stage = stageLabel(frozen.Stage)
		switch {
		case len(frozen.Turns) > 0:
			for i, turn := range frozen.Turns {
				out.Inputs = append(out.Inputs, Input{Turn: i + 1, Input: clean(turn.Input), Expected: pretty(turn.Expect)})
			}
		case frozen.Stage == evalsuite.StageScreening:
			out.Inputs = append(out.Inputs, Input{Turn: 1, Input: clean(frozen.Input), Expected: pretty(frozen.Expect)})
		default:
			out.Inputs = append(out.Inputs, Input{Turn: 1, Input: pretty(map[string]any{"requirement": frozen.Requirement, "change": frozen.Change, "baseSelection": frozen.BaseSelection, "locked": frozen.Locked}), Expected: pretty(frozen.Expect)})
		}
	} else {
		out.Title = clean(records[0].Title)
		out.Stage = stageLabel(records[0].Stage)
		out.Notes = append(out.Notes, "冻结用户输入和期望未记录；不以当前题库或模型返回值补造。")
	}
	current := map[int]evalsuite.CaseRecord{}
	for _, record := range run.current {
		if record.CaseID == id {
			current[record.Seed] = record
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Seed < records[j].Seed })
	for _, record := range records {
		trial := Trial{Seed: record.Seed, Original: verdict(record.Verdict), Usage: measured([]evalsuite.CaseRecord{record}, 1), DurationMS: record.DurationMS, Turns: []Turn{}}
		if r, ok := current[record.Seed]; ok {
			v := verdict(r.Verdict)
			trial.Current = &v
		}
		if record.RunErr != "" {
			trial.Error = strptr("执行异常或未执行；原始错误日志不对前端开放")
		}
		if record.Screening != nil {
			if trial.Usage != nil {
				attempts := int(trial.Usage.ModelCalls)
				trial.Attempts = &attempts
			}
			turns := record.Screening.Turns
			if len(turns) == 0 {
				turns = []evalsuite.ScreeningOutput{*record.Screening}
			}
			for i, turn := range turns {
				t := Turn{Turn: i + 1, Output: clean(turn.Text), ModelOutputs: []string{}, Failures: []Failure{}}
				for _, text := range turn.ModelAttempts {
					t.ModelOutputs = append(t.ModelOutputs, clean(text))
				}
				if len(t.ModelOutputs) == 0 && turn.ModelText != "" {
					t.ModelOutputs = append(t.ModelOutputs, clean(turn.ModelText))
				}
				failures := trial.Original.Failures
				if trial.Current != nil {
					failures = trial.Current.Failures
				}
				for _, f := range failures {
					if len(turns) == 1 || strings.HasPrefix(f.Detail, fmt.Sprintf("第 %d 轮:", i+1)) {
						t.Failures = append(t.Failures, f)
					}
				}
				trial.Turns = append(trial.Turns, t)
			}
		}
		if record.Result != nil {
			if record.Result.Attempts >= 0 {
				attempts := record.Result.Attempts
				trial.Attempts = &attempts
			}
			var decision any
			if d := record.Result.Decision; d != nil {
				decision = map[string]any{"kind": d.Kind, "reason": d.Reason, "fields": d.Fields, "scope": d.Scope, "snapshotDate": d.SnapshotDate, "lowerBoundCNY": d.LowerBoundCNY, "message": d.Message}
			}
			text := pretty(map[string]any{"selection": record.Result.Draft.Selection, "rationale": record.Result.Draft.Rationale, "succeeded": record.Result.Succeeded, "message": record.Result.Message, "decision": decision})
			trial.Selection = &text
		}
		out.Trials = append(out.Trials, trial)
	}
	if out.Stage == "build" {
		out.Notes = append(out.Notes, "构建轨迹保存最终选件、判定和尝试次数；历史未保存每次生成的原文，不能恢复逐次回复。")
	}
	if len(records) == 0 {
		out.Notes = append(out.Notes, "该题尚无执行结果，冻结输入可查看。")
	}
	if !run.summary.Verified {
		out.Notes = append(out.Notes, "该运行未通过完整复验，仅显示保存的原成绩，统一口径复核未知。")
	}
	return out
}
