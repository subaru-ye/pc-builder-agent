package evalsuite

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// AssertScreeningOutput 要求整段对话每轮都正确，不允许末轮成功掩盖早先错误。
func AssertScreeningOutput(c Case, output ScreeningOutput) Verdict {
	if len(c.Turns) == 0 {
		return AssertScreeningCase(c, output.Text)
	}
	if len(output.Turns) != len(c.Turns) || output.Text != "" {
		return Verdict{Failures: []AssertionFailure{{ID: "S0", Name: "对话证据完整性", Detail: fmt.Sprintf("需要 %d 轮，实际 %d 轮；顶层文本必须为空", len(c.Turns), len(output.Turns))}}}
	}
	var failures []AssertionFailure
	for i, turn := range c.Turns {
		if len(output.Turns[i].Turns) > 0 {
			return Verdict{Failures: []AssertionFailure{{ID: "S0", Name: "对话证据完整性", Detail: "不允许嵌套轮次"}}}
		}
		v := AssertScreeningCase(Case{Stage: StageScreening, Input: turn.Input, Expect: turn.Expect}, output.Turns[i].Text)
		for _, f := range v.Failures {
			f.Detail = fmt.Sprintf("第 %d 轮: %s", i+1, f.Detail)
			failures = append(failures, f)
		}
	}
	return Verdict{Passed: len(failures) == 0, Failures: failures}
}

func assertSpecFields(spec schemas.RequirementSpec, fields map[string]json.RawMessage) []AssertionFailure {
	if len(fields) == 0 {
		return nil
	}
	raw, err := schemas.EncodeRequirementSpec(spec)
	if err != nil {
		return []AssertionFailure{{ID: "S2", Name: "字段口径", Detail: err.Error()}}
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys) // 归因顺序和重放稳定，不依赖 map 遍历顺序。
	var failures []AssertionFailure
	for _, key := range keys {
		value := json.RawMessage(raw)
		for _, part := range strings.Split(key, ".") {
			var object map[string]json.RawMessage
			_ = json.Unmarshal(value, &object)
			value = object[part]
		}
		actual, actualErr := JSONHash(value)
		expected, expectedErr := JSONHash(fields[key])
		if actualErr != nil || expectedErr != nil || actual != expected {
			failures = append(failures, AssertionFailure{ID: "S2", Name: "字段口径", Detail: fmt.Sprintf("%s=%s，期望 %s", key, value, fields[key])})
		}
	}
	return failures
}
