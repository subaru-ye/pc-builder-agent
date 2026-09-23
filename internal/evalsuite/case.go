// Package evalsuite 实现 P13 评估集:用例加载、确定性断言矩阵、执行器与报告。
// 断言输入只来自执行结果、钉死快照与用例本身,全部为零 LLM 纯函数;
// 口径与设计见 docs/tech/评估设施.md 与 docs/eval/评估集建立记录.md。
package evalsuite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// Stage 区分被测阶段:build(生成+校验)与 screening(初筛行为)。
type Stage string

const (
	StageBuild     Stage = "build"
	StageScreening Stage = "screening"
)

// Expect 是用例的期望。build 用例用 outcome;screening 用例用 kind 与字段口径。
type Expect struct {
	// build 期望:pass、budget_adaptive(合格交付或可重建预算证明)、或明确非交付结果。
	Outcome string `json:"outcome,omitempty"`
	Reason  string `json:"reason,omitempty"`
	// screening 期望:kind = spec(应输出需求单 JSON)| clarify(应只追问,不输出 JSON)。
	Kind       string `json:"kind,omitempty"`
	BudgetCNY  *int   `json:"budget_cny,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	CPUBrand   string `json:"cpu_brand,omitempty"` // 期望值:amd/intel/any(any = 不得填)
	GPUBrand   string `json:"gpu_brand,omitempty"`
	// ClarifyFields 要求实际追问的缺失信息;空值仅兼容旧题目。
	ClarifyFields []string `json:"clarify_fields,omitempty"` // budget_cny / resolution
	// ForbiddenClarifyFields 明确禁止追问的已知/不适用字段；仅用于新 clarify 题。
	ForbiddenClarifyFields []string `json:"forbidden_clarify_fields,omitempty"`
	// SpecFields 仅新题显式指定：对需求单字段作精确复验，不从模型回复生成期望。
	SpecFields map[string]json.RawMessage `json:"spec_fields,omitempty"`
}

type ScreeningTurn struct {
	Input  string `json:"input"`
	Expect Expect `json:"expect"`
}

// Case 是一条评估用例。build 用例锁可独立复核的约束、不写死期望配置单;
// screening 用例锁输出形态与字段口径。
type Case struct {
	ID    string
	Title string
	Stage Stage
	// build 专有
	Requirement   schemas.RequirementSpec
	Change        *schemas.ChangeRequest
	BaseSelection *schemas.BuildSelection
	Locked        []schemas.Category
	// screening 专有
	Input  string
	Expect Expect
	Turns  []ScreeningTurn
}

// caseWire 是 fixture 的线上形态;requirement/change/base_selection 二次走严格
// 解码器,保证与产品 API 同一套 schema 契约。
type caseWire struct {
	ID            string             `json:"id"`
	Title         string             `json:"title"`
	Stage         Stage              `json:"stage"`
	Requirement   json.RawMessage    `json:"requirement"`
	Change        json.RawMessage    `json:"change"`
	BaseSelection json.RawMessage    `json:"base_selection"`
	Locked        []schemas.Category `json:"locked"`
	Input         string             `json:"input"`
	Expect        Expect             `json:"expect"`
	Turns         []ScreeningTurn    `json:"turns,omitempty"`
}

// LoadCases 按文件名顺序加载目录下全部 *.json 用例;任一 fixture 不合法即报错
// (评估集自身必须全绿才能作为回归基线)。
func LoadCases(dir string) ([]Case, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("evalsuite: 读取用例目录失败: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("evalsuite: 用例目录 %s 没有 *.json 用例", dir)
	}

	cases := make([]Case, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("evalsuite: 读取 %s 失败: %w", path, err)
		}
		c, err := decodeCase(data)
		if err != nil {
			return nil, fmt.Errorf("evalsuite: fixture %s 不合法: %w", name, err)
		}
		cases = append(cases, c)
	}
	return cases, nil
}

func decodeCase(data []byte) (Case, error) {
	var w caseWire
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return Case{}, fmt.Errorf("解码失败: %w", err)
	}
	if strings.TrimSpace(w.ID) == "" || strings.TrimSpace(w.Title) == "" {
		return Case{}, fmt.Errorf("id 与 title 必填")
	}
	stage := w.Stage
	if stage == "" {
		stage = StageBuild
	}
	c := Case{ID: w.ID, Title: w.Title, Stage: stage, Input: w.Input, Expect: w.Expect, Locked: w.Locked, Turns: w.Turns}
	if len(w.Turns) > 0 {
		if stage != StageScreening || w.Input != "" || !reflect.DeepEqual(w.Expect, Expect{}) || len(w.Turns) < 2 || len(w.Turns) > 8 {
			return Case{}, fmt.Errorf("turns 仅用于 2–8 轮 screening，不能混用 input/expect")
		}
		for i, turn := range w.Turns {
			raw, err := json.Marshal(caseWire{ID: w.ID, Title: w.Title, Stage: StageScreening, Input: turn.Input, Expect: turn.Expect})
			if err != nil {
				return Case{}, err
			}
			if _, err := decodeCase(raw); err != nil {
				return Case{}, fmt.Errorf("turn %d: %w", i+1, err)
			}
		}
		return c, nil
	}
	for field, value := range w.Expect.SpecFields {
		if stage != StageScreening || w.Expect.Kind != "spec" || !validSpecField(field) || !json.Valid(value) {
			return Case{}, fmt.Errorf("spec_fields 含不支持的字段或期望: %s", field)
		}
	}

	switch stage {
	case StageBuild:
		if len(w.Expect.ForbiddenClarifyFields) > 0 {
			return Case{}, fmt.Errorf("build 用例不使用 forbidden_clarify_fields")
		}
		switch w.Expect.Outcome {
		case "pass", "budget_adaptive":
			if w.Expect.Reason != "" {
				return Case{}, fmt.Errorf("%s 不使用 reason", w.Expect.Outcome)
			}
		case "clarify", "catalog_infeasible", "data_unavailable", "search_exhausted":
			if w.Expect.Reason == "" {
				return Case{}, fmt.Errorf("非交付期望必须有 reason")
			}
		default:
			return Case{}, fmt.Errorf("不支持的 outcome=%q", w.Expect.Outcome)
		}
		if len(w.Requirement) == 0 {
			return Case{}, fmt.Errorf("build 用例必须带 requirement")
		}
		spec, err := schemas.DecodeLegacyRequirementSpec(w.Requirement)
		if err != nil {
			return Case{}, fmt.Errorf("requirement: %w", err)
		}
		c.Requirement = spec
		if len(w.Change) > 0 {
			change, err := schemas.DecodeChangeRequest(w.Change)
			if err != nil {
				return Case{}, fmt.Errorf("change: %w", err)
			}
			c.Change = &change
		}
		if len(w.BaseSelection) > 0 {
			base, err := schemas.DecodeBuildSelection(w.BaseSelection)
			if err != nil {
				return Case{}, fmt.Errorf("base_selection: %w", err)
			}
			c.BaseSelection = &base
		}
	case StageScreening:
		if strings.TrimSpace(w.Input) == "" {
			return Case{}, fmt.Errorf("screening 用例必须带 input")
		}
		if w.Expect.Kind != "spec" && w.Expect.Kind != "clarify" {
			return Case{}, fmt.Errorf("expect.kind=%q:仅支持 spec 或 clarify", w.Expect.Kind)
		}
		if w.Expect.Outcome != "" {
			return Case{}, fmt.Errorf("screening 用例不使用 expect.outcome")
		}
		seen := map[string]bool{}
		for _, field := range w.Expect.ClarifyFields {
			if w.Expect.Kind != "clarify" || !validQuestionField(field) || seen[field] {
				return Case{}, fmt.Errorf("clarify_fields 必须是 clarify 期望下不重复的受支持字段")
			}
			seen[field] = true
		}
		for _, field := range w.Expect.ForbiddenClarifyFields {
			if w.Expect.Kind != "clarify" || !validQuestionField(field) || seen[field] {
				return Case{}, fmt.Errorf("forbidden_clarify_fields 必须是不重复且不与 clarify_fields 冲突的受支持字段")
			}
			seen[field] = true
		}
	default:
		return Case{}, fmt.Errorf("stage=%q 无效:须为 build 或 screening", stage)
	}
	return c, nil
}

func validQuestionField(field string) bool {
	return field == "budget_cny" || field == "resolution" || field == "owned_parts" || field == "budget_basis" || field == "use_case"
}

func validSpecField(field string) bool {
	switch field {
	case "use_case.type", "noise_pref", "size_pref", "budget_basis", "budget_flex", "owned_parts", "existing_parts":
		return true
	default:
		return false
	}
}
