package buildharness

import "github.com/subaru-ye/pc-builder-agent/internal/evalmetrics"

// EvalTraceSink 把 Harness 指标写入现有 P10 脱敏 JSONL；未启用时自然 no-op。
type EvalTraceSink struct {
	Component string
}

func (s EvalTraceSink) Record(event HarnessEvent) {
	component := s.Component
	if component == "" {
		component = "buildsvc"
	}
	evalmetrics.Record(component, event.Name, event.Fields)
}
