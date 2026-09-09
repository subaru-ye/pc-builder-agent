package buildharness

import (
	"encoding/json"
	"fmt"
)

// PromptComponents 返回本程序实际编译的静态提示词及各尝试轮次的输出契约。
// 动态需求、候选和修复上下文不混入独立提示词版本；调用方得到独立副本。
func PromptComponents() (map[string]string, error) {
	components := map[string]string{"system": builderV2Instruction}
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		raw, err := json.Marshal(outputContract(attempt))
		if err != nil {
			return nil, err
		}
		components[fmt.Sprintf("output_contract_attempt_%d", attempt)] = string(raw)
	}
	return components, nil
}
