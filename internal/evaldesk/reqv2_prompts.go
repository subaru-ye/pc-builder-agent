package evaldesk

import (
	"github.com/subaru-ye/pc-builder-agent/internal/agents/pipeline"
	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/evalsuite"
)

type ReqV2PromptComponent struct {
	evalsuite.PromptComponent
	SHA256 string `json:"sha256"`
}

type ReqV2CurrentPrompts struct {
	Source     string                 `json:"source"`
	SHA256     string                 `json:"sha256"`
	Components []ReqV2PromptComponent `json:"components"`
}

// CurrentPromptsReqV2 读取本服务编译的固定常量，不扫描源码、不初始化模型，
// 不用当前文本回填历史运行。修改源码后需重启服务才能更新此视图。
func CurrentPromptsReqV2() (ReqV2CurrentPrompts, error) {
	builder, err := buildharness.PromptComponents()
	if err != nil {
		return ReqV2CurrentPrompts{}, err
	}
	var components []evalsuite.PromptComponent
	for role, values := range map[string]map[string]string{"screening": pipeline.RequirementPromptComponents(), "builder": builder} {
		for name, text := range values {
			components = append(components, evalsuite.PromptComponent{Role: role, Name: name, Text: text})
		}
	}
	snapshot, identity, err := evalsuite.NewPromptEvidence(components)
	if err != nil {
		return ReqV2CurrentPrompts{}, err
	}
	out := ReqV2CurrentPrompts{Source: "evaldesk_binary", SHA256: identity.SHA256}
	for _, component := range snapshot.Components {
		out.Components = append(out.Components, ReqV2PromptComponent{PromptComponent: component, SHA256: identity.Components[component.Role+"/"+component.Name]})
	}
	return out, nil
}
