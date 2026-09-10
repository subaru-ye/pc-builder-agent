package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"google.golang.org/adk/v2/model"
)

// 原文及旧协议期望来自已保存真实运行；增量 update 是人工 oracle。
// 此测试证明 reducer/grounding/提问/刷新链路，不冒充新提示词真实模型准确率。
func TestRequirementStateReplaysSavedDialogueInputs(t *testing.T) {
	var fixture struct {
		SourceArchive string `json:"source_archive"`
		SourceHash    string `json:"source_archive_sha256"`
		Cases         []struct {
			ID    string `json:"id"`
			Turns []struct {
				Input  string                    `json:"input"`
				Update schemas.RequirementUpdate `json:"update"`
				Expect struct {
					Kind       string                     `json:"kind"`
					Budget     int                        `json:"budget_cny"`
					Resolution string                     `json:"resolution"`
					CPU        string                     `json:"cpu_brand"`
					GPU        string                     `json:"gpu_brand"`
					Clarify    []string                   `json:"clarify_fields"`
					Forbidden  []string                   `json:"forbidden_clarify_fields"`
					Fields     map[string]json.RawMessage `json:"spec_fields"`
				} `json:"expect"`
			} `json:"turns"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile("testdata/requirement_state_replay.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if archived, err := os.ReadFile(filepath.Join("..", "..", "..", fixture.SourceArchive)); err == nil {
		hash := sha256.Sum256(archived)
		if hex.EncodeToString(hash[:]) != fixture.SourceHash {
			t.Fatal("saved source archive changed; review replay provenance")
		}
	}
	for _, c := range fixture.Cases {
		t.Run(c.ID, func(t *testing.T) {
			state := schemas.NewRequirementState()
			for i, turn := range c.Turns {
				out, _ := json.Marshal(turn.Update)
				m := &stateProtocolModel{output: string(out)}
				source := schemas.RequirementSource{Kind: "chat", MessageID: c.ID, Quote: turn.Input}
				ctx := WithRequirementState(context.Background(), state, source)
				var delivered string
				for response, err := range (screeningGuard{LLM: m}).GenerateContent(ctx, &model.LLMRequest{}, false) {
					if err != nil {
						t.Fatalf("turn %d: %v", i+1, err)
					}
					delivered = screeningText(response.Content)
				}
				if m.calls != 1 {
					t.Fatalf("turn %d: extra model invocation", i+1)
				}
				actualUpdate, err := schemas.DecodeRequirementUpdate([]byte(delivered))
				if err != nil {
					t.Fatal(err)
				}
				next, err := schemas.ApplyRequirementUpdate(state, actualUpdate, source)
				if err != nil {
					t.Fatal(err)
				}
				stored, _ := json.Marshal(next)
				if err := json.Unmarshal(stored, &state); err != nil {
					t.Fatal(err)
				} // 每轮从持久化状态恢复。
				specRaw, missing, err := schemas.RequirementStateSpec(state)
				if err != nil {
					t.Fatal(err)
				}
				if turn.Expect.Kind == "clarify" {
					if specRaw != nil {
						t.Fatalf("turn %d: incomplete requirement became spec", i+1)
					}
					for _, field := range turn.Expect.Clarify {
						if !replayHasField(missing, field) {
							t.Fatalf("turn %d: missing %s absent: %v", i+1, field, missing)
						}
					}
					for _, field := range turn.Expect.Forbidden {
						if replayHasField(missing, field) {
							t.Fatalf("turn %d: repeated known question %s", i+1, field)
						}
					}
					continue
				}
				if len(missing) > 0 {
					t.Fatalf("turn %d: repeated questions %v", i+1, missing)
				}
				spec, _ := schemas.DecodeRequirementSpec(specRaw)
				if turn.Expect.Budget > 0 && spec.BudgetCNY != turn.Expect.Budget {
					t.Fatalf("turn %d: old budget restored: %d", i+1, spec.BudgetCNY)
				}
				if turn.Expect.Resolution != "" && string(spec.UseCase.Resolution) != turn.Expect.Resolution {
					t.Fatal("resolution lost")
				}
				if turn.Expect.CPU != "" && string(spec.BrandPref.CPU) != turn.Expect.CPU {
					t.Fatal("CPU brand update lost")
				}
				if turn.Expect.GPU != "" && string(spec.BrandPref.GPU) != turn.Expect.GPU {
					t.Fatal("GPU brand update lost")
				}
				for key, want := range turn.Expect.Fields {
					got := json.RawMessage(specRaw)
					for _, part := range strings.Split(key, ".") {
						var obj map[string]json.RawMessage
						_ = json.Unmarshal(got, &obj)
						got = obj[part]
					}
					var expected, actual any
					_ = json.Unmarshal(want, &expected)
					_ = json.Unmarshal(got, &actual)
					if !reflect.DeepEqual(expected, actual) {
						t.Fatalf("turn %d: %s=%s expected %s", i+1, key, bytes.TrimSpace(got), want)
					}
				}
			}
		})
	}
}

func replayHasField(missing []string, key string) bool {
	for _, field := range missing {
		if field == key || (key == "use_case" && field == "use_case.type") || (key == "resolution" && field == "use_case.resolution") || (key == "owned_parts" && strings.HasPrefix(field, "owned_parts.")) {
			return true
		}
	}
	return false
}
