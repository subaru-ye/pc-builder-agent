---
status: done
created: 2026-09-22
completed: 2026-09-22
---

# Change: Jev shadow intent evaluation

## Outcome

Add an evaluation-first Jev integration that classifies the current product control action as `collect`, `confirm`, `plan`, or `ambiguous`, records the full probability result beside the existing Screening decision, and produces reproducible selective-precision/coverage reports.

This change proves or disproves Jev's value before any production routing is added. Default product behavior, RequirementState mutation, Builder execution, compatibility rules, and delivery gates remain unchanged.

The first useful artifact is a frozen report answering:

- How accurately does Jev predict the current `next_action` contract?
- At each confidence or selected-probability threshold, what precision and coverage are obtained?
- How often is Jev unavailable, slow, or ambiguous?
- What is the upper bound on Screening calls that might be avoided by a later safe route policy?

## Boundaries

- In:
  - A small intent-domain contract and Jev HTTP implementation using the Go standard library.
  - Explicit, bounded Jev calls from the current `planningeval` harness.
  - Per-step Jev evidence, threshold metrics, focused intent fixtures, tests, and evaluation documentation.
  - Optional paired evaluation with the existing live Screening model when the caller explicitly selects live mode and supplies both call budgets.
- Out:
  - No call from `internal/product.Service`, no production `JEV_MODE`, and no production background worker.
  - No bypass of Screening, RequirementUpdate generation, RequirementState reduction, or Builder admission.
  - No preference scorer, preserve-component verifier, compatibility judgment, arithmetic, or free-text generation.
  - No database migration, generic decision-provider framework, new dependency, commit, or push.
- Approval boundary:
  - Jev network traffic is allowed only from an explicit evaluation mode with `JEV_API_KEY`, a pinned model, a positive request cap, a fresh output directory, and persisted run provenance.
  - Missing live credentials must not block implementation or local tests; it only leaves the real external report unverified.
  - Any proposal to add production routing, send broader user data, or change the current product response requires a separate confirmed change.

## Decisions

### Product contract

- The Jev taxonomy mirrors the current authority boundary: `collect`, `confirm`, `plan`, and `ambiguous`.
- `new_build`, `modify_build`, `update_requirement`, `explain`, and `compare` are not first-version labels because they overlap and do not map directly to the current state machine.
- Jev is evaluated as one bounded Choice question. It does not generate `RequirementUpdate.operations` or `reply`.
- Ground truth is reviewer-owned `Step.Expect.NextAction`; the existing Screening result is a comparison baseline, never the label.
- Route eligibility is not inferred from Jev confidence. The report may estimate an explicitly labelled upper bound, but must not report simulated coverage as an implemented production reduction.

### Input construction

Build the Jev input from the exact pre-Screening `product.ScreenInput` captured by `planningeval.gateway.Screen`:

- Current turn: `input.RequirementSource.Quote`, falling back to `input.Text` only when the source quote is empty.
- State: `schemas.RequirementStatePromptView(*input.RequirementState)`; do not serialize full history.
- Execution facts: `input.HasBuild`, `input.Conversation.CanPlan`, and `input.Conversation.BuildVersion`.
- Reference resolution: bounded `input.Conversation.LastAssistant` only.
- Excluded: `BaseDraft`, quote, parts, proposal payloads, traces, tool output, prices, and prior raw user messages.

The provider receives this already-bounded domain input and must not know about `product.ScreenInput`.

### Confidence semantics

- Persist Jev `confidence`, `probabilities[predicted]`, and the complete probability map separately.
- Evaluate `confidence` and selected probability as two independent score policies; never substitute one threshold for the other.
- `ambiguous` is a valid refusal class and is never counted as an autonomous action.
- A threshold is selected on a calibration split and reported once on a session-disjoint holdout split.
- The first 60-100 reviewed turns provide only a directional result; they do not substantiate a 99% production precision claim.

### Provider and versioning

- Use `POST /v1/systemone` through injected `net/http.Client`; add no SDK or dependency.
- Pin `jev-1.13.0`. Record both requested and response model IDs.
- Validate HTTP status, response type, selected option, finite probability values in `[0,1]`, distribution sum within a small documented tolerance, and usage fields.
- Classify `401`, `422`, `429`, `529`, timeout/cancellation, transport, and decode/contract failures.
- Do not automatically retry in the first evaluator. Record provider availability honestly; production retry policy is a later concern.

### Evaluation safety

- Existing `check`, `replay`, and `plan-live` behavior remains network-free with respect to Jev.
- Introduce an explicit `shadow-live` mode for recorded Screening/Builder fixtures plus live Jev calls.
- Allow existing `live` mode to opt into Jev only through a separate explicit flag and positive Jev call cap.
- Keep Jev call accounting separate from the existing Screening/Builder `max-calls` counter.
- Exhausted call budget or failed pre-request journal persistence is a harness failure; provider/API failure is recorded on the step and does not alter the underlying product case result.

## Implementation design

### Domain contract

Add `internal/decision/intent.go` with a narrow API:

```go
type Intent string

const (
	IntentCollect   Intent = "collect"
	IntentConfirm   Intent = "confirm"
	IntentPlan      Intent = "plan"
	IntentAmbiguous Intent = "ambiguous"
)

type IntentInput struct {
	CurrentTurn      string
	RequirementState json.RawMessage
	HasBuild         bool
	CanPlan          bool
	BuildVersion     int
	LastAssistant    string
}

type IntentResult struct {
	Intent              Intent
	Probabilities       map[Intent]float64
	Confidence          float64
	SelectedProbability float64
	RequestedModel      string
	ResponseModel       string
	InputTokens         int
	OutputTokens        int
	Duration            time.Duration
}

type IntentClassifier interface {
	Classify(context.Context, IntentInput) (IntentResult, error)
}
```

Keep metrics and policy out of this package. They belong to `planningeval` until a production consumer exists.

### Jev adapter

Add the smallest practical implementation under `internal/providers/jev/`:

- `client.go`: client configuration, private wire structs, request execution, response validation, and classified errors.
- `client_test.go`: `httptest.Server` coverage; create another file only if the test file becomes materially hard to read.

The Choice criteria must restate the current product semantics:

- `collect`: record/discuss/ask only; do not execute Builder planning.
- `confirm`: the first sufficiently complete intent is awaiting explicit execution authorization.
- `plan`: the current turn explicitly authorizes generation, modification, comparison, continuation, or replanning.
- `ambiguous`: the action cannot be selected safely, the turn contains conflicting actions, or the taxonomy does not fit.

Do not include examples copied from the evaluated case in the question definition. Hash the exact instruction and criteria JSON and persist that hash in the evaluation plan/report.

### Planning-eval integration

Extend the existing harness rather than creating a second evaluator:

- `internal/planningeval/gateway.go`: build the bounded `IntentInput` from the captured `ScreenInput`, call the optional classifier, and record its result independently of the existing Screening result.
- `internal/planningeval/types.go`: add optional Jev observation fields to `StepRecord` and aggregate metrics to `Report`; omit them when disabled.
- `internal/planningeval/run.go`: aggregate calls, availability, latency, per-class results, and threshold curves without changing existing pass/fail checks.
- `cmd/evalplanning/main.go`: add explicit mode/flags, call caps, plan provenance, and safe client construction.
- `docs/tech/评估设施.md`: document modes, credentials, budgets, outputs, limitations, and the fact that confidence is not correctness probability.

Recommended optional record shape:

```go
type IntentObservation struct {
	Prediction          string             `json:"prediction,omitempty"`
	Probabilities       map[string]float64 `json:"probabilities,omitempty"`
	Confidence          float64            `json:"confidence,omitempty"`
	SelectedProbability float64            `json:"selected_probability,omitempty"`
	RequestedModel      string             `json:"requested_model,omitempty"`
	ResponseModel       string             `json:"response_model,omitempty"`
	InputTokens         int                `json:"input_tokens,omitempty"`
	OutputTokens        int                `json:"output_tokens,omitempty"`
	DurationMS          int64              `json:"duration_ms,omitempty"`
	ExistingDecision    string             `json:"existing_decision,omitempty"`
	GroundTruth         string             `json:"ground_truth,omitempty"`
	Agreement           bool               `json:"agreement,omitempty"`
	Correct             bool               `json:"correct,omitempty"`
	ErrorClass          string             `json:"error_class,omitempty"`
	Error               string             `json:"error,omitempty"`
}
```

Do not duplicate raw input in this record. The existing `ScreenInput` evidence already contains the bounded evaluation input.

### CLI and configuration

Keep secrets in environment variables:

```text
JEV_API_KEY
JEV_BASE_URL=https://api.typesafe.ai
```

Use explicit evaluation flags:

```text
-mode shadow-live
-jev-model jev-1.13.0
-jev-max-calls <positive integer>
-jev-timeout <duration>
```

`shadow-live` requirements:

- Accept a non-live frozen suite because Screening and Builder remain recorded/oracle-backed.
- Require a positive Jev call cap and a new output directory.
- Persist model, endpoint host, timeout, question hash, suite hash, catalog hash, binary hash, and call cap before the first request.
- Never persist the API key or Authorization header.
- Journal request/response evidence consistently with current live evaluation discipline.

### Metrics

For score `s_i`, prediction `yhat_i`, ground truth `y_i`, and accepted set `A_t = {i | s_i >= t and yhat_i != ambiguous}`:

```text
Precision(t) = count(i in A_t where yhat_i == y_i) / count(A_t)
Coverage(t)  = count(A_t) / count(labelled valid examples)
Fallback(t)  = 1 - Coverage(t)
```

Report at minimum:

- Jev accuracy, API success/failure counts, and per-intent confusion matrix.
- Agreement with the existing final Screening `NextAction`, clearly distinguished from correctness.
- Threshold points for both confidence and selected probability.
- Latency p50/p95, token totals, and price estimate using an explicitly recorded rate.
- Calibration/holdout counts and limitations; an empty accepted set has undefined precision, not 100%.

An optional manually reviewed `route_eligible` expectation may estimate a future upper bound. It must not be used as a runtime feature or labelled `System-2 avoidance` until a deployable deterministic eligibility policy exists.

### Dataset

First inventory current planning suites for steps with non-empty `Expect.NextAction`. Reuse them only after checking that the label matches the current requirement-state protocol.

Add `internal/planningeval/testdata/jev-intent-v1/` only when existing reviewed coverage is insufficient. The combined frozen set should contain 60-100 labelled turns covering:

- Initial collection and first complete confirmation.
- Explicit execution with complete state and existing-build modification.
- Compound turns such as confirmation plus a new budget or component change.
- Negation, correction, reference to the last assistant, comparison/explanation, and ambiguity.
- Predominantly Chinese phrasing, with session-disjoint calibration and holdout partitions recorded in provenance.

Do not generate ground truth from either Jev or the existing Screening result. Generated candidate text is acceptable only after human review of every expected action.

## Execution outline

1. Add the intent-domain contract and strict Jev HTTP client with unit tests.
2. Add explicit eval configuration, separate Jev call accounting, and evidence journaling.
3. Record per-step observations and implement deterministic metric aggregation with tests.
4. Audit/reuse current labelled steps, add focused fixtures, freeze provenance, and document the run procedure.
5. Run local validation; when credentials are available, produce one frozen `shadow-live` report and state whether production Shadow deserves a separate change.

## Completion bar

Implementation:

- [x] Domain contract and Jev client are implemented without a new dependency.
- [x] Jev is callable only through explicit bounded eval configuration; existing default modes keep their current network behavior.
- [x] Step/report schemas and threshold metrics are implemented without changing product outcomes.

Evidence:

- [x] Provider, error, integration, metric, and regression tests pass.
- [x] A reviewed frozen intent dataset and provenance are present, or the reused dataset coverage is documented with equivalent evidence.
- [x] A real Jev report is produced when credentials are available; otherwise the missing external verification is stated explicitly and no performance numbers are claimed.

## Validation

Required local validation:

```text
go test ./internal/decision ./internal/providers/jev ./internal/planningeval
go test ./...
go run ./cmd/evalplanning -mode check -suite <intent-suite>
```

Required contract checks:

- Jev success, unknown option, missing/invalid probabilities, invalid confidence, malformed JSON, and response-model capture.
- Timeout, cancellation, transport error, `401`, `422`, `429`, and `529` classification.
- Nil/disabled/failing classifier leaves existing case result, state, versions, Builder calls, and pass/fail checks unchanged.
- Call caps and evidence-write failure stop further external spending.
- Logs and reports contain no API key or Authorization header.

Live validation when credentials are available:

```text
go run ./cmd/evalplanning \
  -mode shadow-live \
  -suite <intent-suite> \
  -out <new-output-directory> \
  -jev-model jev-1.13.0 \
  -jev-max-calls <reviewed-step-count> \
  -jev-timeout 2s
```

The exact timeout is an evaluation input, not a production recommendation. Record it in provenance and report observed timeout/failure rates.

## Follow-on gate

Open a separate production-Shadow change only if the frozen report shows all of the following directional signals:

- At least 95% point-estimate precision on an explicitly reported score threshold.
- At least 20% accepted coverage on the reviewed set.
- No obvious collapse on Chinese, multi-turn, compound-change, or ambiguous slices.
- Provider availability and p95 latency are compatible with running a non-blocking observer.
- There is a measurable set of `plan` turns that contain no required RequirementState mutation.

These are exploration gates, not production route criteria. Production routing still requires a larger session-disjoint holdout, a confidence lower bound for the target precision, a deterministic eligibility guard, fallback/kill-switch testing, and a separate user-approved change.

## Decision Log

<!-- Only append user-confirmed material direction changes. -->

## Result

2026-09-22 完成。实际改动：

- `internal/decision/intent.go`：有界意图契约（Intent/IntentInput/IntentResult/IntentClassifier），无指标无策略。
- `internal/providers/jev/client.go`：标准库实现的 SystemOne `POST /v1/systemone` 单 Choice 客户端，钉 `jev-1.13.0`；校验响应类型、选项、[0,1] 概率与分布和（容差 0.02）、confidence、usage；错误分类 auth/invalid_request/rate_limited/provider_overloaded/timeout/canceled/transport/decode/contract/unexpected_status，不重试。`client_test.go` httptest 覆盖全部合同与状态分类。
- `internal/planningeval`：`StepRecord.Intent` 观测、`Suite.IntentSplit`（Load 校验）、gateway 在 Screening 决策点前做有界 Jev 调用（独立记账、请求前证据落盘、provider 失败不改产品结果）、`intent.go` 确定性聚合（accuracy/混淆/一致率、confidence 与 selected-probability 双阈值曲线、校准选点保留报告、空接受集 precision=undefined、correct_share 显式上界、p50/p95、token 与显式费率现金估计）。
- `cmd/evalplanning`：`shadow-live` 模式与 `-jev*` 旗标；密钥缺失在建目录前失败；plan.json 先于首请求持久化 model/host/timeout/问题哈希/套件与目录与二进制哈希/调用上限；live 模式经 `-jev` 显式加入。
- 数据集 `internal/planningeval/testdata/jev-intent-v1/`（37 案例/63 标注轮，current-178 三套件 + 6 个手写 oracle 合成用例；组装脚本 `scripts/eval/planning/build_jev_intent.py`；标注为 AI 辅助逐条复核，15 条已有冻结标注全部一致，边界轮理由在 provenance）。
- 文档：`docs/tech/评估设施.md` 第 8 节；`docs/eval/运行记录.md` 2026-09-22 条目。

验证：`go build ./... && go vet ./... && go test ./...` 全绿；`-mode check` 与离线 `-mode replay` 通过（42 个 red 检查与源冻结套件本机基线逐项一致）；离线 replay 与真实 shadow-live 的产品检查失败集合逐项相同，证明 Jev 观察未改变产品结果。真实 shadow-live：63/63 调用成功、0 歧义、p50 279ms / p95 331ms；整体准确率 33.3%，一致率 33%；confirm 类 42 轮中 38 轮被判 collect（系统性坍塌）；holdout 最优 precision 0.833 @ coverage 0.194。

**结论：不满足 Follow-on gate，不开启生产 Shadow change。** Jev 可用性与延迟良好，但当前问题定义下价值不成立（confirm 判据疑对"本轮是否给出授权"表述过窄，可作为后续评测实验，不是生产改动）。

未验证/剩余风险：标注为 AI 辅助复核未经用户逐条签收，63 轮只支持方向性结论；套件继承源冻结件的本机 replay red 检查（与本次改动无关）；未提供确认费率故未折现；`live + -jev` 组合只有单元级覆盖，未做真实联合运行。
