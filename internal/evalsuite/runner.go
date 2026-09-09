package evalsuite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/subaru-ye/pc-builder-agent/internal/buildharness"
	"github.com/subaru-ye/pc-builder-agent/internal/schemas"
)

// ScreeningRunner 输入用户原文,返回最终可见回复(不含思考内容)。
// 保留文本才能验证追问内容,并在 replay 中重新提取 JSON。
type ScreeningRunner interface {
	Run(ctx context.Context, input string) (string, error)
}

// ScreeningOutput 使用指针留存:缺失表示旧产物,Text 为空表示实际空回复。
type ScreeningOutput struct {
	Text          string   `json:"text"`
	ModelText     string   `json:"model_text,omitempty"` // 程序拦截前原文，与最终可见输出区分。
	MissingFields []string `json:"missing_fields,omitempty"`
}

type detailedScreeningRunner interface {
	RunDetailed(context.Context, string) (ScreeningOutput, error)
}

// Deps 是执行评估集所需的运行时依赖。
type Deps struct {
	Harness   buildharness.Harness
	Snapshot  SnapshotView
	Screening ScreeningRunner // 用例含 screening 阶段时必填
	// Timeout 是单条用例的执行上限;0 表示不设单用例超时(仍受 ctx 约束)。
	Timeout time.Duration
	// Seeds 是每条用例的独立重复次数(≥1);>1 即 Pass^k 口径:全部 seed
	// pass 且零 veto 才算该用例通过。
	Seeds int
	// OnRecord 可选地显示已完成的用例进度,不参与判分。
	OnRecord func(CaseRecord)
}

// CaseRecord 是一次执行的完整轨迹,run 模式落盘、replay 模式读回重放断言。
// Requirement 以 EncodeRequirementSpec 的稳定线上 JSON 留存,便于人工审阅;
// seeds>1 时每个 (case, seed) 一条记录。
type CaseRecord struct {
	CaseID      string                    `json:"case_id"`
	Title       string                    `json:"title"`
	Stage       Stage                     `json:"stage"`
	Seed        int                       `json:"seed"`
	Requirement json.RawMessage           `json:"requirement,omitempty"`
	Expect      Expect                    `json:"expect"`
	Snapshot    SnapshotView              `json:"snapshot"`
	Result      *buildharness.BuildResult `json:"result,omitempty"`
	Screening   *ScreeningOutput          `json:"screening,omitempty"`
	Verdict     Verdict                   `json:"verdict"`
	Attribution []Attribution             `json:"attribution,omitempty"`
	DurationMS  int64                     `json:"duration_ms"`
	RunErr      string                    `json:"run_err,omitempty"`
}

// RunCases 逐条执行评估集。harness 返回 error 时的分类:
// 结构化业务结果按期望判卷；实际执行 error 一律计失败，不按错误文案剔除分母。
func RunCases(ctx context.Context, cases []Case, deps Deps) ([]CaseRecord, error) {
	seeds := deps.Seeds
	if seeds < 1 {
		seeds = 1
	}
	records := make([]CaseRecord, 0, len(cases)*seeds)
	for _, c := range cases {
		for seed := 1; seed <= seeds; seed++ {
			record, err := runOne(ctx, c, seed, deps)
			if err != nil {
				return nil, err
			}
			records = append(records, record)
			if deps.OnRecord != nil {
				deps.OnRecord(record)
			}
		}
	}
	return records, nil
}

func runOne(ctx context.Context, c Case, seed int, deps Deps) (CaseRecord, error) {
	record := CaseRecord{
		CaseID: c.ID, Title: c.Title, Stage: c.Stage, Seed: seed,
		Expect: c.Expect, Snapshot: deps.Snapshot,
	}

	runCtx := ctx
	cancel := func() {}
	if deps.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, deps.Timeout)
	}
	started := time.Now()
	defer cancel()

	var verdict Verdict
	var runErr error
	switch c.Stage {
	case StageScreening:
		if deps.Screening == nil {
			return CaseRecord{}, fmt.Errorf("evalsuite: 用例 %s 为 screening 阶段,但未提供 Screening runner", c.ID)
		}
		var output ScreeningOutput
		if detailed, ok := deps.Screening.(detailedScreeningRunner); ok {
			output, runErr = detailed.RunDetailed(runCtx, c.Input)
		} else {
			output.Text, runErr = deps.Screening.Run(runCtx, c.Input)
		}
		record.Screening = &output
		if runErr == nil {
			verdict = AssertScreeningCase(c, output.Text)
		}
	default: // StageBuild
		raw, err := schemas.EncodeRequirementSpec(c.Requirement)
		if err != nil {
			return CaseRecord{}, fmt.Errorf("evalsuite: 编码用例 %s 需求单失败: %w", c.ID, err)
		}
		record.Requirement = raw
		var result buildharness.BuildResult
		result, runErr = deps.Harness.Run(runCtx, buildharness.BuildInput{
			Requirement:   c.Requirement,
			Change:        c.Change,
			BaseSelection: c.BaseSelection,
			Locked:        c.Locked,
		})
		if runErr == nil {
			verdict = AssertCase(c, result, deps.Snapshot)
			record.Result = &result
		}
	}
	record.DurationMS = time.Since(started).Milliseconds()

	switch {
	case runErr != nil:
		record.RunErr = runErr.Error()
		verdict = Verdict{Passed: false, Failures: []AssertionFailure{{
			ID: "RUN", Name: "执行错误", Detail: runErr.Error(),
		}}}
	}
	record.Verdict = verdict
	record.Attribution = Attribute(verdict.Failures)
	return record, nil
}
