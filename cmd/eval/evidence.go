package main

import (
	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

// runPromptEvidence 只记录题库会使用的角色，从正在运行的程序读取常量而非扫描当前源码。
func runPromptEvidence(cases []evalsuite.Case) (evalsuite.PromptSnapshot, evalsuite.PromptIdentity, error) {
	roles := map[evalsuite.Stage]bool{}
	for _, c := range cases {
		roles[c.Stage] = true
	}
	var components []evalsuite.PromptComponent
	add := func(role string, values map[string]string) {
		for name, text := range values {
			components = append(components, evalsuite.PromptComponent{Role: role, Name: name, Text: text})
		}
	}
	if roles[evalsuite.StageBuild] {
		values, err := buildharness.PromptComponents()
		if err != nil {
			return evalsuite.PromptSnapshot{}, evalsuite.PromptIdentity{}, err
		}
		add("builder", values)
	}
	if roles[evalsuite.StageScreening] {
		add("screening", pipeline.NewBuildPromptComponents())
	}
	return evalsuite.NewPromptEvidence(components)
}
