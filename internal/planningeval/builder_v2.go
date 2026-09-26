package planningeval

// Builder v2 评估的判卷辅助：冻结 EffectiveConstraints 金标断言与工具合同
// 指纹。执行链复用 Run()（真实确认事务 → planning.Runner → 持久化），本文件
// 不引入第二条执行路径。
import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/planning"
)

// dotPathValue 在解码后的 JSON 对象里按点路径取值；数组下标支持数字段。
func dotPathValue(root map[string]any, path string) (json.RawMessage, bool) {
	var cur any = root
	for _, seg := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			var idx int
			if _, err := fmt.Sscanf(seg, "%d", &idx); err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	raw, err := json.Marshal(cur)
	if err != nil {
		return nil, false
	}
	return raw, true
}

// gradeFrozenConstraints 断言 Builder 输入携带确认事务冻结的有效选型约束。
// 只读 PlanningInput.EffectiveConstraints——那份载荷就是运行时合同本身。
func gradeFrozenConstraints(r *StepRecord, gold *FrozenConstraintsGold, check func(name string, pass bool, detail any)) {
	if r.PlanningInput == nil || r.PlanningInput.EffectiveConstraints == nil {
		check("frozen_constraints_present", false, "builder input carried no effective_constraints")
		return
	}
	c := r.PlanningInput.EffectiveConstraints
	var spec map[string]any
	if err := json.Unmarshal(c.Spec, &spec); err != nil {
		check("frozen_constraints_spec_decodable", false, err)
		return
	}
	for path, want := range gold.Spec {
		got, ok := dotPathValue(spec, path)
		check("frozen_constraints_spec:"+path, ok && jsonEqual(got, want), string(got))
	}
	for _, want := range gold.Defaults {
		found := false
		for _, d := range c.Defaults {
			if d.Field != want.Field {
				continue
			}
			found = true
			pass := (want.Origin == "" || d.Origin == want.Origin) &&
				(len(want.Value) == 0 || jsonEqual(mustJSON(d.Value), mustJSON(want.Value)))
			check("frozen_constraints_default:"+want.Field, pass, fmt.Sprintf("%v origin=%s", string(d.Value), d.Origin))
		}
		if !found {
			check("frozen_constraints_default:"+want.Field, false, "default not expanded")
		}
	}
	for _, field := range gold.AbsentDefaults {
		found := false
		for _, d := range c.Defaults {
			found = found || d.Field == field
		}
		check("frozen_constraints_default_absent:"+field, !found, "user-stated value must not reappear as a default")
	}
}

func mustJSON(raw json.RawMessage) json.RawMessage {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

// AttachToolContract 从运行中第一条 builder 请求提取工具合同指纹：
// system instruction + tools 声明整体规范化（Go map 键序稳定）后哈希。
// 合同指纹相同的批次才可直接配对比较。
func AttachToolContract(report *Report) {
	for _, c := range report.Cases {
		for _, s := range c.Steps {
			for _, t := range s.Trace {
				if t.Role != "builder" || len(t.Request) == 0 {
					continue
				}
				var req struct {
					Config json.RawMessage `json:"config"`
					Model  string          `json:"model"`
				}
				if json.Unmarshal(t.Request, &req) != nil || len(req.Config) == 0 {
					continue
				}
				var cfg map[string]any
				if json.Unmarshal(req.Config, &cfg) != nil {
					continue
				}
				// 只保留合同本体：提示词与工具声明。其余配置（温度等）不属于
				// 工具合同，不影响指纹。
				contract := map[string]any{}
				for _, key := range []string{"systemInstruction", "system_instruction", "tools"} {
					if v, ok := cfg[key]; ok {
						contract[key] = v
					}
				}
				raw, err := json.Marshal(contract)
				if err != nil {
					continue
				}
				report.ToolContract = &ToolContractIdentity{
					SHA256:        Hash(raw),
					Model:         req.Model,
					ErrorContract: planning.ToolErrorContractVersion,
				}
				return
			}
		}
	}
}
