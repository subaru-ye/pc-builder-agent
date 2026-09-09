package evaldesk

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

func filterRecords(records []evalsuite.CaseRecord, ids map[string]bool) []evalsuite.CaseRecord {
	if ids == nil {
		return records
	}
	out := []evalsuite.CaseRecord{}
	for _, r := range records {
		if ids[r.CaseID] {
			out = append(out, r)
		}
	}
	return out
}

func measured(records []evalsuite.CaseRecord, expected int) *Usage {
	if len(records) == 0 {
		return nil
	}
	u := &Usage{TotalRecords: len(records)}
	if expected > u.TotalRecords {
		u.TotalRecords = expected
	}
	for _, r := range records {
		v := r.Usage
		if v == nil || v.ModelCalls < 0 || v.EmbeddingCalls < 0 || v.UsageResponses < 0 || v.UsageResponses > v.ModelCalls || v.InputTokens < 0 || v.OutputTokens < 0 || v.TotalTokens < 0 {
			continue
		}
		u.MeasuredRecords++
		u.ModelCalls += v.ModelCalls
		u.EmbeddingCalls += v.EmbeddingCalls
		u.UsageResponses += v.UsageResponses
		u.InputTokens += v.InputTokens
		u.OutputTokens += v.OutputTokens
		u.TotalTokens += v.TotalTokens
	}
	if u.MeasuredRecords == 0 {
		return nil
	}
	u.Complete = u.MeasuredRecords == u.TotalRecords
	return u
}

func metrics(run *savedRun, records []evalsuite.CaseRecord, ids map[string]bool) Metrics {
	rows := filterRecords(records, ids)
	out := Metrics{Recorded: len(rows)}
	all := map[string]bool{}
	if ids != nil {
		for id := range ids {
			all[id] = true
		}
	} else if len(run.cases) > 0 {
		for id := range run.cases {
			all[id] = true
		}
	} else {
		for _, r := range rows {
			all[r.CaseID] = true
		}
	}
	out.CaseCount = len(all)
	expected := 0
	if run.meta.RequestedSeeds > 0 {
		repeats := run.meta.RequestedSeeds
		out.Repeats = &repeats
		if len(run.cases) > 0 {
			expected = out.CaseCount * repeats
			out.Expected = &expected
		}
	}
	byID := map[string]map[int]bool{}
	duplicates := map[string]bool{}
	for _, r := range rows {
		if r.Verdict.Passed && !r.Verdict.DataError {
			out.Passed++
		} else if r.Verdict.DataError {
			out.DataErrors++
		} else {
			out.Failed++
		}
		out.DurationMS += r.DurationMS
		if byID[r.CaseID] == nil {
			byID[r.CaseID] = map[int]bool{}
		}
		if _, exists := byID[r.CaseID][r.Seed]; exists {
			duplicates[r.CaseID] = true
		}
		passed := r.Verdict.Passed && !r.Verdict.DataError
		for _, f := range r.Verdict.Failures {
			if f.Veto {
				passed = false
			}
		}
		byID[r.CaseID][r.Seed] = passed
	}
	denominator := out.Recorded
	if out.Expected != nil {
		denominator = *out.Expected
	}
	if denominator > 0 {
		v := float64(out.Passed) / float64(denominator)
		out.ExecutionRate = &v
	}
	if out.Repeats != nil && out.Expected != nil {
		for id := range all {
			passed := !duplicates[id] && len(byID[id]) == *out.Repeats
			for seed := 1; seed <= *out.Repeats; seed++ {
				if !byID[id][seed] {
					passed = false
				}
			}
			if passed {
				out.AllPassedCases++
			}
		}
		if out.CaseCount > 0 {
			v := float64(out.AllPassedCases) / float64(out.CaseCount)
			out.AllPassedRate = &v
		}
	}
	if run.summary.Status == "invalid" {
		out.ExecutionRate = nil
		out.AllPassedRate = nil
	}
	out.Usage = measured(rows, expected)
	return out
}

func caseScore(run *savedRun, records []evalsuite.CaseRecord, id string) *CaseScore {
	rows := filterRecords(records, map[string]bool{id: true})
	if len(rows) == 0 {
		return nil
	}
	out := &CaseScore{Total: len(rows)}
	if run.meta.RequestedSeeds > 0 {
		x := run.meta.RequestedSeeds
		out.Expected = &x
	}
	for _, r := range rows {
		if r.Verdict.Passed && !r.Verdict.DataError {
			out.Passed++
		}
	}
	return out
}

func hashJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	hash, err := evalsuite.JSONHash(raw)
	if err != nil {
		return ""
	}
	return hash
}

func output(record evalsuite.CaseRecord) any {
	if record.Screening != nil {
		return record.Screening
	}
	if record.Result != nil {
		return map[string]any{"draft": record.Result.Draft, "decision": record.Result.Decision, "succeeded": record.Result.Succeeded, "result": record.Result.Result, "message": record.Result.Message}
	}
	return record.RunErr
}

func changedOutput(a, b []evalsuite.CaseRecord) int {
	bySeed := map[int]evalsuite.CaseRecord{}
	for _, r := range a {
		bySeed[r.Seed] = r
	}
	changed := 0
	for _, r := range b {
		if old, ok := bySeed[r.Seed]; ok && hashJSON(output(old)) != hashJSON(output(r)) {
			changed++
		}
	}
	return changed
}

func condition(key, label string, a, b *string) Condition {
	c := Condition{Key: key, Label: label, Baseline: a, Candidate: b, State: "unknown"}
	if a != nil && b != nil {
		c.State = "same"
		if *a != *b {
			c.State = "changed"
		}
	}
	return c
}

func modelText(run *savedRun) *string {
	parts := []string{}
	for _, m := range run.summary.Versions.Models {
		parts = append(parts, m.Role+": "+m.Model)
	}
	return strptr(strings.Join(parts, " · "))
}

func profileText(run *savedRun) *string {
	if run.meta.HarnessProfile == nil {
		return nil
	}
	semantic := "关闭"
	if run.meta.HarnessProfile.Semantic {
		semantic = "开启"
	}
	return strptr(fmt.Sprintf("最多生成 %d 次；语义检索%s", run.meta.HarnessProfile.AttemptLimit, semantic))
}

func repeatsText(run *savedRun) *string {
	if run.meta.RequestedSeeds < 1 {
		return nil
	}
	return strptr(fmt.Sprintf("每题 %d 次", run.meta.RequestedSeeds))
}

func configHash(run *savedRun) *string {
	if len(run.meta.Models) == 0 {
		return nil
	}
	return strptr(hashJSON(run.meta.Models))
}

func frozenDataHash(run *savedRun) *string {
	if len(run.records) == 0 {
		return nil
	}
	hashes := map[string]bool{}
	for _, record := range run.records {
		if record.Snapshot.Catalog == nil {
			return nil
		}
		hashes[hashJSON(record.Snapshot)] = true
	}
	list := []string{}
	for hash := range hashes {
		list = append(list, hash)
	}
	sort.Strings(list)
	return strptr(strings.Join(list, " / "))
}

func (s *Store) Compare(aID, bID string) (CompareResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.lookup(aID)
	if err != nil {
		return CompareResponse{}, err
	}
	b, err := s.lookup(bID)
	if err != nil {
		return CompareResponse{}, err
	}
	out := CompareResponse{Baseline: a.summary, Candidate: b.summary, Mode: "observational", CurrentGrader: evalsuite.CurrentGraderVersion, Cases: []CaseComparison{}, Counts: map[string]int{}, Notices: []string{
		"所有变化描述均为已保存运行之间的观察；小样本、上游模型服务、缓存及向量索引状态不足以证明因果。",
		"指标差异仅汇总内容和期望均未改变的共同题；新增、删除、修改题单列。统一复核在内存进行，不覆盖原成绩。",
		"模型和 Embedding 为逻辑调用，不含适配器内部 HTTP 重试；token 为上游已返回的已知用量，不含 Embedding token。",
	}}
	for _, key := range []string{"common", "regressed", "improved", "persistent_failure", "output_changed", "unchanged", "added", "removed", "modified", "unavailable"} {
		out.Counts[key] = 0
	}
	av, bv := a.summary.Versions, b.summary.Versions
	out.Conditions = []Condition{
		condition("suite", "题库版本", av.Suite, bv.Suite), condition("suiteHash", "冻结题库内容", av.SuiteHash, bv.SuiteHash),
		condition("snapshotDate", "商品快照日期", av.SnapshotDate, bv.SnapshotDate), condition("data", "商品数据指纹", av.DataFingerprint, bv.DataFingerprint),
		condition("frozenData", "冻结目录核对（由产物计算）", frozenDataHash(a), frozenDataHash(b)),
		condition("prompt", "提示词独立版本", av.PromptVersion, bv.PromptVersion), condition("binary", "实际程序版本", av.Binary, bv.Binary),
		condition("commit", "代码提交", av.Commit, bv.Commit), condition("source", "构建输入摘要", av.SourceFingerprint, bv.SourceFingerprint),
		condition("models", "模型", modelText(a), modelText(b)), condition("modelParameters", "模型完整配置摘要", configHash(a), configHash(b)),
		condition("harness", "执行机制", profileText(a), profileText(b)), condition("repeats", "重复次数", repeatsText(a), repeatsText(b)),
		condition("grader", "原判卷版本", av.Grader, bv.Grader),
	}
	if a.summary.Verified && b.summary.Verified {
		if _, strictErr := evalsuite.CompareChangeDirs(a.dir, b.dir); strictErr == nil {
			out.Mode = "strict"
		} else {
			out.StrictReason = strptr("同题、完整目录、模型、执行机制、重复次数或运行证据未满足现有严格对照要求")
		}
	} else {
		out.StrictReason = strptr("至少一侧未通过完整离线复验，无法形成严格代码回归结论")
	}
	ids := map[string]bool{}
	for id := range a.cases {
		ids[id] = true
	}
	for id := range b.cases {
		ids[id] = true
	}
	for _, r := range a.records {
		ids[r.CaseID] = true
	}
	for _, r := range b.records {
		ids[r.CaseID] = true
	}
	ordered := []string{}
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	common := map[string]bool{}
	for _, id := range ordered {
		ac, aok := a.cases[id]
		bc, bok := b.cases[id]
		c := CaseComparison{ID: clean(id), Status: "unavailable", Content: "unknown"}
		if bok {
			c.Title = clean(bc.Title)
			c.Stage = stageLabel(bc.Stage)
		} else if aok {
			c.Title = clean(ac.Title)
			c.Stage = stageLabel(ac.Stage)
		} else {
			for _, r := range append(append([]evalsuite.CaseRecord{}, a.records...), b.records...) {
				if r.CaseID == id {
					c.Title = clean(r.Title)
					c.Stage = stageLabel(r.Stage)
					break
				}
			}
		}
		switch {
		case aok && bok && a.hashes[id] == b.hashes[id]:
			c.Content = "common"
			common[id] = true
			out.Counts["common"]++
		case aok && bok:
			c.Content = "modified"
			c.Status = "modified"
		case !aok && bok && len(a.cases) > 0:
			c.Content = "added"
			c.Status = "added"
		case aok && !bok && len(b.cases) > 0:
			c.Content = "removed"
			c.Status = "removed"
		}
		ar := filterRecords(a.records, map[string]bool{id: true})
		br := filterRecords(b.records, map[string]bool{id: true})
		c.OriginalA = caseScore(a, a.records, id)
		c.OriginalB = caseScore(b, b.records, id)
		c.CurrentA = caseScore(a, a.current, id)
		c.CurrentB = caseScore(b, b.current, id)
		c.UsageA = measured(ar, a.meta.RequestedSeeds)
		c.UsageB = measured(br, b.meta.RequestedSeeds)
		c.OutputChanges = changedOutput(ar, br)
		if c.Content == "common" && c.CurrentA != nil && c.CurrentB != nil && a.meta.RequestedSeeds == b.meta.RequestedSeeds && a.meta.RequestedSeeds > 0 {
			switch {
			case c.CurrentB.Passed < c.CurrentA.Passed:
				c.Status = "regressed"
			case c.CurrentB.Passed > c.CurrentA.Passed:
				c.Status = "improved"
			case c.CurrentB.Passed < b.meta.RequestedSeeds:
				c.Status = "persistent_failure"
			case c.OutputChanges > 0:
				c.Status = "output_changed"
			default:
				c.Status = "unchanged"
			}
		}
		if c.Content == "common" && a.summary.Verified && b.summary.Verified && a.meta.RequestedSeeds == b.meta.RequestedSeeds && c.UsageA != nil && c.UsageB != nil && c.UsageA.Complete && c.UsageB.Complete {
			d := c.UsageB.ModelCalls - c.UsageA.ModelCalls
			c.CallsDelta = &d
			if tokensKnown(c.UsageA) && tokensKnown(c.UsageB) {
				tokens := c.UsageB.TotalTokens - c.UsageA.TotalTokens
				c.TokensDelta = &tokens
			}
		}
		if c.Content == "common" && a.summary.Verified && b.summary.Verified {
			var duration int64
			for _, r := range br {
				duration += r.DurationMS
			}
			for _, r := range ar {
				duration -= r.DurationMS
			}
			c.DurationDeltaMS = &duration
		}
		out.Counts[c.Status]++
		out.Cases = append(out.Cases, c)
	}
	out.Metrics.Scope = fmt.Sprintf("%d 道共同题（冻结内容和期望均相同）", len(common))
	if len(common) > 0 {
		am, bm := metrics(a, a.current, common), metrics(b, b.current, common)
		if a.summary.Verified {
			out.Metrics.Baseline = &am
		}
		if b.summary.Verified {
			out.Metrics.Candidate = &bm
		}
		if a.summary.Verified && b.summary.Verified && a.meta.RequestedSeeds == b.meta.RequestedSeeds {
			out.Metrics.Delta = delta(am, bm)
		} else {
			out.Notices = append(out.Notices, "共同题的重复次数或复核证据不一致，指标增减未知，不能比较原始通过次数来判定退步。")
		}
	} else {
		out.Mode = "unavailable"
		out.Notices = append(out.Notices, "没有可核对的共同题，无法比较成绩和用量。")
	}
	changed := map[string]bool{}
	for _, c := range out.Conditions {
		if c.State == "changed" {
			group := c.Key
			switch c.Key {
			case "suiteHash":
				group = "suite"
			case "binary", "commit", "source":
				group = "code"
			case "snapshotDate", "data", "frozenData":
				group = "data"
			case "models", "modelParameters":
				group = "models"
			}
			changed[group] = true
		}
	}
	if len(changed) > 1 {
		out.Notices = append([]string{"多项条件同时变化，本次只能观察相关变化，不能确定是哪项改动造成结果变化。"}, out.Notices...)
	}
	if a.summary.Versions.Grader == nil || b.summary.Versions.Grader == nil {
		out.Notices = append(out.Notices, "空判卷版本显示未记录；现有评估工具按历史分支复验原成绩，再单列当前口径复核结果。")
	}
	out.ChangeSummaries = s.changeSummaries(a, b, out)
	return out, nil
}

func delta(a, b Metrics) MetricsDelta {
	out := MetricsDelta{}
	if a.ExecutionRate != nil && b.ExecutionRate != nil {
		d := *b.ExecutionRate - *a.ExecutionRate
		out.ExecutionRate = &d
	}
	if a.AllPassedRate != nil && b.AllPassedRate != nil {
		d := *b.AllPassedRate - *a.AllPassedRate
		out.AllPassedRate = &d
	}
	duration := b.DurationMS - a.DurationMS
	out.DurationMS = &duration
	if a.Usage != nil && b.Usage != nil && a.Usage.Complete && b.Usage.Complete {
		d := b.Usage.ModelCalls - a.Usage.ModelCalls
		out.ModelCalls = &d
		embeddings := b.Usage.EmbeddingCalls - a.Usage.EmbeddingCalls
		out.EmbeddingCalls = &embeddings
		if tokensKnown(a.Usage) && tokensKnown(b.Usage) {
			tokens := b.Usage.TotalTokens - a.Usage.TotalTokens
			out.TotalTokens = &tokens
		}
	}
	return out
}

func tokensKnown(u *Usage) bool { return u != nil && (u.ModelCalls == 0 || u.UsageResponses > 0) }
