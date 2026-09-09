package main

import (
	"context"
	"fmt"
	"iter"
	"sync"

	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

type usageCounter struct {
	mu       sync.Mutex
	total    evalsuite.Usage
	maxCalls int64
	denied   bool // 至少一项计划内请求或题目因调用上限未执行。
}

func (u *usageCounter) reserve(embedding bool) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.maxCalls > 0 && u.total.ModelCalls+u.total.EmbeddingCalls >= u.maxCalls {
		u.denied = true
		return fmt.Errorf("评估调用上限 %d 已耗尽；未继续请求模型", u.maxCalls)
	}
	if embedding {
		u.total.EmbeddingCalls++
	} else {
		u.total.ModelCalls++
	}
	return nil
}

func (u *usageCounter) stopReason() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.maxCalls > 0 && u.total.ModelCalls+u.total.EmbeddingCalls >= u.maxCalls {
		u.denied = true
		return fmt.Errorf("调用上限已耗尽，本题未执行")
	}
	return nil
}

func (u *usageCounter) limitDenied() bool { u.mu.Lock(); defer u.mu.Unlock(); return u.denied }

func (u *usageCounter) snapshot() evalsuite.Usage  { u.mu.Lock(); defer u.mu.Unlock(); return u.total }
func (u *usageCounter) wrap(m model.LLM) model.LLM { return measuredModel{LLM: m, usage: u} }

type measuredModel struct {
	model.LLM
	usage *usageCounter
}

func (m measuredModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if err := m.usage.reserve(false); err != nil {
			yield(nil, err)
			return
		}
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
	if err := m.usage.reserve(true); err != nil {
		return nil, err
	}
	return m.base.EmbedOne(ctx, text)
}
