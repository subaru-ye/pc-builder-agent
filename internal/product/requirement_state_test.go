package product

import (
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

func runPtr(id string) *string { return &id }

// recentAssistantReplies 只取当前 run 用户消息之前的助手回复,按时间正序
// 截最近 limit 条,单条截断 maxRunes;这是消除重复追问的注入数据源。
func TestRecentAssistantReplies(t *testing.T) {
	runID := "run-3"
	long := strings.Repeat("长", recentAssistantsMaxRunes+10)
	messages := []store.WebMessage{
		{ID: "m1", Role: "assistant", Content: "第1轮回复：请问预算多少？"},
		{ID: "m2", Role: "user", RunID: runPtr("run-1"), Content: "预算8000"},
		{ID: "m3", Role: "assistant", Content: "好的。主要用来做什么？"},
		{ID: "m4", Role: "user", RunID: runPtr("run-2"), Content: "玩游戏"},
		{ID: "m5", Role: "assistant", Content: long},
		{ID: "m6", Role: "assistant", Content: "玩什么游戏，对噪音有要求吗？"},
		{ID: "m7", Role: "user", RunID: runPtr(runID), Content: "DOTA2"},
		{ID: "m8", Role: "assistant", Content: "当前轮之后的回复不得进入"},
	}
	replies := recentAssistantReplies(messages, runID, 3, recentAssistantsMaxRunes)
	if len(replies) != 3 {
		t.Fatalf("应截最近 3 条: %d", len(replies))
	}
	if replies[0] != "好的。主要用来做什么？" || replies[2] != "玩什么游戏，对噪音有要求吗？" {
		t.Fatalf("时间正序且不含当前轮之后或第1轮回复: %q", replies)
	}
	if got := len([]rune(replies[1])); got != recentAssistantsMaxRunes {
		t.Fatalf("超长回复应截断到 %d runes, got %d", recentAssistantsMaxRunes, got)
	}
	// 空白回复不计入;stopRunID 不存在时统计全部历史。
	blank := []store.WebMessage{
		{ID: "b1", Role: "assistant", Content: "  "},
		{ID: "b2", Role: "assistant", Content: "唯一有效回复"},
	}
	if replies := recentAssistantReplies(blank, "run-x", 5, recentAssistantsMaxRunes); len(replies) != 1 || replies[0] != "唯一有效回复" {
		t.Fatalf("空白助手回复应跳过: %q", replies)
	}
	// 无助手消息返回 nil,渲染层退化为最近一条。
	if replies := recentAssistantReplies(nil, "run-x", 5, recentAssistantsMaxRunes); replies != nil {
		t.Fatalf("无助手消息应返回 nil: %q", replies)
	}
}
