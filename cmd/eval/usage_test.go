package main

import (
	"context"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
	"iter"
	"testing"
)

type usageTestModel struct{ present bool }

func (usageTestModel) Name() string { return "usage-test" }
func (m usageTestModel) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if m.present {
			yield(&model.LLMResponse{Partial: true, UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 10, CandidatesTokenCount: 1, TotalTokenCount: 11}}, nil)
			yield(&model.LLMResponse{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 10, CandidatesTokenCount: 4, TotalTokenCount: 14}}, nil)
		} else {
			yield(&model.LLMResponse{}, nil)
		}
	}
}
func TestUsageCountsEachLogicalCallAndOnlyFinalUsage(t *testing.T) {
	u := &usageCounter{}
	for _, present := range []bool{true, false} {
		for _, err := range u.wrap(usageTestModel{present: present}).GenerateContent(context.Background(), &model.LLMRequest{}, false) {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	s := u.snapshot()
	if s.ModelCalls != 2 || s.UsageResponses != 1 || s.InputTokens != 10 || s.OutputTokens != 4 || s.TotalTokens != 14 {
		t.Fatalf("usage=%+v", s)
	}
}

func TestCallCapSharedByModelsAndEmbedding(t *testing.T) {
	u := &usageCounter{maxCalls: 2}
	for _, err := range u.wrap(usageTestModel{present: true}).GenerateContent(context.Background(), &model.LLMRequest{}, false) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := u.reserve(true); err != nil {
		t.Fatal(err)
	}
	denied := false
	for response, err := range u.wrap(usageTestModel{present: true}).GenerateContent(context.Background(), &model.LLMRequest{}, false) {
		if response != nil || err == nil {
			t.Fatal("cap allowed upstream output")
		}
		denied = true
	}
	if !denied || u.stopReason() == nil {
		t.Fatal("cap not enforced")
	}
	s := u.snapshot()
	if s.ModelCalls != 1 || s.EmbeddingCalls != 1 || s.UsageResponses != 1 {
		t.Fatalf("denied request counted as sent: %+v", s)
	}
}
