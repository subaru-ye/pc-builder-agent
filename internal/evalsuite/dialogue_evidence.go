package evalsuite

import (
	"fmt"
	"reflect"

	"github.com/subaru-ye/pc-builder-agent/internal/product"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// CheckDialogueEvidence 证明每轮上下文来自保存的用户题目和真实前轮输出；不得塞入答案或别题历史。
// 重放与配对报告共用这项检查，不能只复验最后的有效载荷而忽略模型实际收到的上下文。
func CheckDialogueEvidence(c Case, output ScreeningOutput) error {
	if len(c.Turns) == 0 {
		return nil
	}
	if len(c.Turns) != len(output.Turns) {
		return fmt.Errorf("dialogue round count mismatch")
	}
	var history []store.WebMessage
	for i, turn := range c.Turns {
		actual := output.Turns[i]
		input := product.BuildScreenInput(history, turn.Input)
		if input.Context != actual.Context || !reflect.DeepEqual(input.UserSources, actual.UserSources) {
			return fmt.Errorf("turn %d context/source mismatch", i+1)
		}
		guardText := actual.Text
		if actual.GuardText != "" {
			guardText = actual.GuardText
		}
		parsed, err := product.ParseScreeningResult(guardText, turn.Input)
		if err != nil {
			return err
		}
		text, assistant := guardText, parsed.Text
		if parsed.Kind == product.ScreenRequirement {
			text = string(parsed.Payload)
			assistant = product.ScreeningReadyMessage
		}
		if text != actual.Text {
			return fmt.Errorf("turn %d normalized payload mismatch", i+1)
		}
		history = append(history, store.WebMessage{Role: "user", Content: turn.Input}, store.WebMessage{Role: "assistant", Content: assistant})
	}
	return nil
}
