// Package buildharness 实现 Agent Harness 2.0 的确定性候选准备、单次选配和定向修复。
// 本包不负责 A2A 传输或版本落库；调用方继续复用既有 pipeline 的会话和保存契约。
package buildharness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/adk/v2/model"

	"github.com/subaru-ye/pc-builder-agent/internal/agents/validate"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
)

// Mode 是 buildsvc 的内部构建执行模式；v2 通过真实烟测后成为默认路径。
type Mode string

const (
	ModeLegacy Mode = "legacy"
	ModeV2     Mode = "v2"

	MaxAttempts       = 3
	MaxCandidateRunes = 24_000
)

// ParseMode 严格解析 BUILD_HARNESS_MODE；空值使用已验收的 v2，legacy 可显式回切。
func ParseMode(value string) (Mode, error) {
	mode := Mode(strings.ToLower(strings.TrimSpace(value)))
	if mode == "" {
		return ModeV2, nil
	}
	if mode != ModeLegacy && mode != ModeV2 {
		return "", fmt.Errorf("BUILD_HARNESS_MODE=%q 无效:须为 legacy 或 v2", value)
	}
	return mode, nil
}

// Candidate 是发给 builder 的最小候选视图。内部排序标记不参与 JSON。
type Candidate struct {
	SKU       string         `json:"sku"`
	Brand     string         `json:"brand"`
	Model     string         `json:"model"`
	PriceCNY  *string        `json:"price_cny"`
	Specs     map[string]any `json:"specs"`
	MatchText string         `json:"match_text,omitempty"`

	origin    string
	protected bool
}

// CandidateGroup 按八品类固定顺序保存候选，避免依赖 map 迭代顺序。
type CandidateGroup struct {
	Category   schemas.Category `json:"category"`
	Candidates []Candidate      `json:"candidates"`
}

// CandidateBundle 是单次 run 的一致候选包。
type CandidateBundle struct {
	SchemaVersion int              `json:"schema_version"`
	SnapshotDate  string           `json:"snapshot_date"`
	Groups        []CandidateGroup `json:"groups"`
	Trimmed       int              `json:"trimmed"`
}

// BuildInput 是 Harness 的内部输入；公共 Agent schema 不随之改变。
type BuildInput struct {
	Requirement   schemas.RequirementSpec
	Change        *schemas.ChangeRequest
	BaseSelection *schemas.BuildSelection
	Locked        []schemas.Category
	TraceID       string
}

// BuildResult 是 Harness 的确定性执行结果；Succeeded=false 表示未保存配置。
type BuildResult struct {
	Succeeded bool
	Attempts  int
	Draft     schemas.BuildDraft
	Result    validate.Result
	Message   string
}

// CatalogSource 为候选准备器限定最小数据依赖。
type CatalogSource interface {
	ActiveCatalogSnapshot(context.Context) (store.CatalogSnapshot, error)
	SemanticCandidates(context.Context, store.SemanticQuery) (store.SemanticResult, error)
}

// QueryEmbedder 只在确有软偏好时使用。
type QueryEmbedder interface {
	EmbedOne(context.Context, string) ([]float32, error)
}

// Evaluator 是现有确定性 validator 的最小接口。
type Evaluator interface {
	Evaluate(context.Context, schemas.BuildSelection) (validate.Result, error)
}

// CandidatePlanner 为一次 run 准备不可变候选包。
type CandidatePlanner interface {
	Prepare(context.Context, BuildInput) (CandidateBundle, error)
}

// Harness 是 v2 执行入口。
type Harness interface {
	Run(context.Context, BuildInput) (BuildResult, error)
}

// RepairConstraints 是修复规划所需的确定性边界。
type RepairConstraints struct {
	Requirement schemas.RequirementSpec
	Change      *schemas.ChangeRequest
	Base        *schemas.BuildSelection
	Locked      []schemas.Category
	Bundle      CandidateBundle
}

// RepairPlan 固定未受影响的 SKU，只开放 Mutable 中的候选。
type RepairPlan struct {
	Mutable            []schemas.Category          `json:"mutable"`
	Reason             string                      `json:"reason"`
	PreferredSelection map[schemas.Category]string `json:"preferred_selection,omitempty"`
}

// RepairPlanner 将规则报告和预算状态映射为最小修复面。
type RepairPlanner interface {
	Plan(validate.Result, schemas.BuildDraft, RepairConstraints) (RepairPlan, error)
}

// HarnessEvent 仅允许放置脱敏计数、枚举和耗时。
type HarnessEvent struct {
	Name   string
	Fields map[string]any
}

// TraceSink 接收可选验收指标；默认实现为 no-op。
type TraceSink interface {
	Record(HarnessEvent)
}

type noopTrace struct{}

func (noopTrace) Record(HarnessEvent) {}

// Config 装配 v2；模型不注册工具，SDK 重试策略仍由 modelprovider 控制。
type Config struct {
	Model    model.LLM
	Planner  CandidatePlanner
	Repairer RepairPlanner
	Eval     Evaluator
	Trace    TraceSink
}

func rawJSON(value any) json.RawMessage {
	b, _ := json.Marshal(value)
	return b
}
