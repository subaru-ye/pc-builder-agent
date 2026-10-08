package main

import (
	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

// 只冻结此入口真正使用的 Screening 增量协议；Builder 是零模型 oracle。
func savePromptEvidence(dir string) (evalsuite.PromptIdentity, error) {
	var components []evalsuite.PromptComponent
	for name, text := range pipeline.RequirementPromptComponents() {
		components = append(components, evalsuite.PromptComponent{Role: "screening", Name: name, Text: text})
	}
	snapshot, identity, err := evalsuite.NewPromptEvidence(components)
	if err != nil {
		return identity, err
	}
	return identity, snapshot.Write(dir)
}
