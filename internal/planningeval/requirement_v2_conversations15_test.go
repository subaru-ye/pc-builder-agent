package planningeval

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

const conversations15Dataset = "testdata/requirement-v2-conversations15-20261009"

func TestReqV2Conversations15R2OnlyCorrectsRemovalMetadata(t *testing.T) {
	original, err := LoadRequirementV2(conversations15Dataset)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := LoadRequirementV2("testdata/requirement-v2-conversations15-r2-20261009")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original.GatesRaw, revision.GatesRaw) {
		t.Fatal("gate thresholds changed")
	}
	for i, file := range original.Manifest.Files {
		if file.Path != "conversations/cases.json" && file != revision.Manifest.Files[i] {
			t.Fatalf("unrelated layer changed: %s", file.Path)
		}
	}
	if !reflect.DeepEqual(original.Manifest.SplitSessions, revision.Manifest.SplitSessions) {
		t.Fatal("split changed")
	}
	// 构造唯一允许的差异，不接受其他金标、撤销对象或引文变化。
	expected := append([]ReqV2ConversationCase(nil), original.Conversations...)
	corrected := 0
	for ci := range expected {
		expected[ci].Turns = append([]ReqV2ConversationTurn(nil), expected[ci].Turns...)
		for ti := range expected[ci].Turns {
			expected[ci].Turns[ti].Expected.Operations = append([]ReqV2OpGold{}, expected[ci].Turns[ti].Expected.Operations...)
			for oi := range expected[ci].Turns[ti].Expected.Operations {
				gold := &expected[ci].Turns[ti].Expected.Operations[oi]
				if gold.Op != "remove" {
					continue
				}
				if expected[ci].ID != "cv-monitor-refusal" && expected[ci].ID != "cv-resolution-withdrawal" {
					t.Fatal("unexpected removal gold")
				}
				gold.Evidence = ""
				corrected++
				if opMatchesGold(schemas.RequirementOperation{Op: "remove", Field: gold.Field}, *gold) {
					t.Fatal("removal without user quote must still fail")
				}
				if !opMatchesGold(schemas.RequirementOperation{Op: "remove", Field: gold.Field, Quote: gold.QuoteContains}, *gold) {
					t.Fatal("contract-valid removal should not require optional evidence metadata")
				}
			}
		}
	}
	if corrected != 4 || !reflect.DeepEqual(expected, revision.Conversations) {
		t.Fatal("r2 differs beyond the four removal evidence annotations")
	}
}

func TestReqV2Conversations15VersionBoundaries(t *testing.T) {
	data, err := LoadRequirementV2(conversations15Dataset)
	if err != nil {
		t.Fatal(err)
	}
	parent := loadReqV2(t)
	if err := GradeReqV2Selftest(data.Selftest); err != nil {
		t.Fatal(err)
	}
	for _, file := range append(parent.Manifest.Files, ReqV2ManifestFile{Path: "gates.json"}) {
		if file.Path == "conversations/cases.json" {
			continue
		}
		original, err := os.ReadFile(filepath.Join(parent.Root, filepath.FromSlash(file.Path)))
		if err != nil {
			t.Fatal(err)
		}
		revision, err := os.ReadFile(filepath.Join(data.Root, filepath.FromSlash(file.Path)))
		if err != nil || !bytes.Equal(original, revision) {
			t.Fatalf("non-conversation asset changed: %s (%v)", file.Path, err)
		}
	}
	development := 0
	seen := map[string]bool{}
	for _, c := range data.Conversations {
		seen[c.ID] = true
		if c.Split == "development" {
			development++
			if len(c.Turns) < 2 || c.Final.BuilderMustRun {
				t.Fatalf("%s must have multiple user turns and keep the confirmation gate", c.ID)
			}
		}
		for _, old := range parent.Conversations {
			if old.ID != c.ID {
				continue
			}
			if c.ID == "cv-premature-ready" || c.ID == "cv-monitor-refusal" {
				if !reflect.DeepEqual(old.Turns, c.Turns[:len(old.Turns)]) {
					t.Fatalf("%s original turn changed", c.ID)
				}
			} else if !reflect.DeepEqual(old, c) {
				t.Fatalf("%s inherited case changed", c.ID)
			}
		}
	}
	if development != 15 || len(data.Conversations) != 16 {
		t.Fatalf("expected 15 development + 1 historical holdout, got %d/%d", development, len(data.Conversations))
	}
	for _, c := range parent.Conversations {
		if !seen[c.ID] {
			t.Fatalf("inherited case missing: %s", c.ID)
		}
	}
}

// 金标操作直接送真实 reducer/readiness，检查脚本的状态断言是否自洽。
// 不解析用户语义、不调用模型，也不作为 Pass^3 证据。
func TestReqV2Conversations15GoldExecutable(t *testing.T) {
	data, err := LoadRequirementV2(conversations15Dataset)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range data.Conversations {
		if c.Split != "development" {
			continue
		}
		t.Run(c.ID, func(t *testing.T) {
			state := schemas.NewRequirementState()
			var final ReqV2TurnObservation
			for index, turn := range c.Turns {
				gold := turn.Expected
				update := schemas.RequirementUpdate{Operations: []schemas.RequirementOperation{}}
				for _, want := range gold.Operations {
					if want.QuoteContains == "" || !strings.Contains(turn.Text, want.QuoteContains) {
						t.Fatalf("turn %d: operation %s lacks a matching user quote", index+1, want.Field)
					}
					evidence := want.Evidence
					if evidence == "" {
						evidence = "stated"
					}
					update.Operations = append(update.Operations, schemas.RequirementOperation{
						Op: want.Op, Field: want.Field, Value: want.Value, Quote: want.QuoteContains,
						Evidence: evidence, Strength: want.Strength,
					})
				}
				for _, want := range gold.Observations {
					if want.TextContains == "" || !strings.Contains(turn.Text, want.TextContains) {
						t.Fatalf("turn %d: observation lacks a matching user quote", index+1)
					}
					reason := want.ReasonContains
					if reason == "" {
						reason = "尚待用户确认"
					}
					update.Observations = append(update.Observations, schemas.RequirementObservationInput{
						Field: want.Field, Quote: want.TextContains, Reason: reason,
					})
				}
				state, err = schemas.ApplyRequirementUpdate(state, update, schemas.RequirementSource{
					Kind: "chat", MessageID: fmt.Sprintf("turn-%d", index+1), Quote: turn.Text,
				})
				if err != nil {
					t.Fatalf("turn %d: reducer: %v", index+1, err)
				}
				readiness, err := schemas.EvaluateRequirementReadiness(state)
				if err != nil {
					t.Fatalf("turn %d: readiness: %v", index+1, err)
				}
				final = ReqV2TurnObservation{State: ProjectRequirementState(state), Readiness: &ReqV2ReadinessResult{
					MissingFields: readiness.MissingFields, ConfirmationEligible: readiness.ConfirmationEligible,
				}}
				g := &reqV2Grader{}
				g.gradeStateFields("gold", c.ID, final.State, ReqV2StateProjection{}, gold.StateFields, false)
				for _, assertion := range g.assertions {
					if !assertion.Pass {
						t.Fatalf("turn %d: %s: %s", index+1, assertion.Name, assertion.Detail)
					}
				}
				if gold.MissingFields == nil {
					t.Fatalf("turn %d: missing required-field gold", index+1)
				}
				if !equalStringList(readiness.MissingFields, gold.MissingFields) || (gold.ConfirmationEligible != nil && readiness.ConfirmationEligible != *gold.ConfirmationEligible) {
					t.Fatalf("turn %d: readiness gold mismatch: got missing=%v eligible=%v; want %v/%v",
						index+1, readiness.MissingFields, readiness.ConfirmationEligible, gold.MissingFields, gold.ConfirmationEligible)
				}
			}
			for _, assertion := range gradeConversationFinal(c, final) {
				if !assertion.Pass {
					t.Fatalf("%s: %s", assertion.Name, assertion.Detail)
				}
			}
		})
	}
}
