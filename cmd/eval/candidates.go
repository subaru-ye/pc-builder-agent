package main

import (
	"context"
	"encoding/json"

	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
)

// 顺序评估独立保存每次运行实际使用的候选，供检索消融复核；不影响生产候选逻辑。
type recordingPlanner struct {
	buildharness.CandidatePlanner
	last *buildharness.CandidateBundle
}

func (p *recordingPlanner) Prepare(ctx context.Context, input buildharness.BuildInput) (buildharness.CandidateBundle, error) {
	p.last = nil
	bundle, err := p.CandidatePlanner.Prepare(ctx, input)
	if err != nil {
		return bundle, err
	}
	// 保存序列化副本，避免后续修复过程或下一题修改共享切片。
	raw, err := json.Marshal(bundle)
	if err != nil {
		return bundle, err
	}
	var saved buildharness.CandidateBundle
	if err := json.Unmarshal(raw, &saved); err != nil {
		return bundle, err
	}
	p.last = &saved
	return bundle, nil
}

type recordingHarness struct {
	buildharness.Harness
	planner *recordingPlanner
}

func (h recordingHarness) Run(ctx context.Context, input buildharness.BuildInput) (buildharness.BuildResult, error) {
	h.planner.last = nil // 提前非交付也不能沿用上一题候选。
	return h.Harness.Run(ctx, input)
}

func (h recordingHarness) CandidateBundle() *buildharness.CandidateBundle { return h.planner.last }
