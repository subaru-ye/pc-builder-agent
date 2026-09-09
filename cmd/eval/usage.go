package main

import (
	"context"
	"iter"
	"sync"

	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

type usageCounter struct {
	mu    sync.Mutex
	total evalsuite.Usage
}

func (u *usageCounter) snapshot() evalsuite.Usage  { u.mu.Lock(); defer u.mu.Unlock(); return u.total }
func (u *usageCounter) wrap(m model.LLM) model.LLM { return measuredModel{LLM: m, usage: u} }

type measuredModel struct {
	model.LLM
	usage *usageCounter
}

func (m measuredModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		m.usage.mu.Lock()
		m.usage.total.ModelCalls++
		m.usage.mu.Unlock()
		var last *genai.GenerateContentResponseUsageMetadata
		defer func() {
			if last != nil {
				m.usage.mu.Lock()
				defer m.usage.mu.Unlock()
				m.usage.total.UsageResponses++
				m.usage.total.InputTokens += int64(last.PromptTokenCount)
				m.usage.total.OutputTokens += int64(last.CandidatesTokenCount)
				m.usage.total.TotalTokens += int64(last.TotalTokenCount)
			}
		}()
		for response, err := range m.LLM.GenerateContent(ctx, req, stream) {
			if response != nil && response.UsageMetadata != nil {
				copied := *response.UsageMetadata
				last = &copied
			}
			if !yield(response, err) {
				return
			}
		}
	}
}

type measuredEmbedder struct {
	base  buildharness.QueryEmbedder
	usage *usageCounter
}

func (m measuredEmbedder) EmbedOne(ctx context.Context, text string) ([]float32, error) {
	m.usage.mu.Lock()
	m.usage.total.EmbeddingCalls++
	m.usage.mu.Unlock()
	return m.base.EmbedOne(ctx, text)
}
