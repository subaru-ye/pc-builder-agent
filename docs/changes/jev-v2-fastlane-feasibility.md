---
status: done
created: 2026-09-25
completed: 2026-09-25
---

# Change: Jev v2 fastlane feasibility（单字段预算快走影子验证）

## Outcome

回答一个可判定的问题：**Jev 能否识别"可安全跳过 Screening"的简单需求更新，从而降低整轮端到端延迟与成本。** 本 change 只研究单字段预算更新（budget_cny），并且只做影子验证：产品行为、RequirementState 写入路径、Builder、Screening 全部不变；快路由只存在于评估器里。

与 v1（`jev-shadow-intent-evaluation`）的区别：不复用 collect/confirm/plan/ambiguous 分类，也不比较两个模型的单次响应速度。Jev 在本 change 里只回答一个新问题：本轮是否在**确定**该预算（determine），还是询问（ask）、引用报价（quote_reference）、拒绝/撤回（reject）、不确定（uncertain）。数值、状态写入、需求确认、Builder 启动一律由确定性代码完成，Jev 无权生成。

分两阶段执行，阶段间有显式闸门：

1. **零模型机会审计**（零调用）：逐轮统计 requirement-v2 development+calibration 语料中候选预算轮数、理论可绕过比例和错误风险。该语料全部用于过开发，不能称为真实流量或盲测；结论只对语料负责。
2. **有界影子实验**（仅当阶段 1 候选足够）：默认关闭、显式旗标开启，比较三路——原 Screening（冻结 live 观测）／纯确定性规则／规则＋Jev。阈值只用校准集选择。

## Boundaries

- In:
  - `internal/decision` 新增 fastlane 判定域契约与问题定义（可哈希、无指标）。
  - `internal/providers/jev` 最小泛化：问题定义参数化的通用 Choice 调用；v1 `Classify` 行为与测试不变。
  - `internal/agents/pipeline` 导出现有预算候选值提取（`budgetAmount`/`moneyAmount`/`chineseBudgetNumber` 的包装），不改任何既有行为。
  - `internal/planningeval` 实验引擎：语料构建（金标回放先验状态）、确定性规则、可选 Jev 门控、指标聚合、产物落盘。
  - `cmd/evalfastlane`：`audit`（零模型）与 `shadow-live`（有界真实 Jev）两个模式。
  - 独立标注表（Set B，AI 起草、待人工确认）与标注复核文档。
- Out:
  - 不启用生产快路由；`internal/product.Service` 零改动；无 `FASTLANE` 环境变量。
  - 不触碰 Builder；不运行已暴露 holdout（加载器强制过滤 split=holdout 并断言产物无 holdout id）。
  - Jev 不生成数值、不写状态、不确认需求；未通过判定的轮次一律回退 Screening。
  - 不宣称 GO/发布；不推送。
- Approval boundary:
  - Jev 网络调用只发生在 `cmd/evalfastlane -mode shadow-live`：要求 `JEV_API_KEY`、正数 `-jev-max-calls`、新输出目录；plan/provenance 在首请求前落盘（模型、host、超时、问题哈希、语料与 Set B 哈希、二进制哈希、调用上限）。
  - Jev 输入只有本轮原话与候选字段/值；不发送密钥、需求全量状态、聊天历史。
  - 若影子结果证明 Jev 相对纯规则有增量价值，后续生产 change（带开关、真实产品链路 A/B、故障回退）另行起草，由用户验收后决定是否执行。

## Decisions

### 快走候选合同（单一出处，规则与 Jev 共用）

一个轮次可被快走，当且仅当：

- 确定性提取（复用 `pipeline` 既有正则与中文数字解析）从本轮原话得到**恰好一个**预算候选值，且在 500–200000 元哨兵区间内（区间外的"候选"视为型号/帧率等误提取，直接回退）；
- 该轮的领域效果只有 `set budget_cny=<值>`：不含其他字段的确定、不含执行请求（开始配/生成）、不含预算口径/弹性（上限、封顶、上浮、总价、口径）、不是提案接受协议轮（裸"可以"）；
- 用户语义是在**设定/修改**该预算，而不是提问、引用报价、拒绝或撤回；
- 快走产生的操作经真实 Reducer（`schemas.ApplyRequirementUpdate`）与 Readiness（`schemas.RequirementStateSpec`）落地，关键字段（budget_cny）终值与金标一致。

"预算改成7500"是候选；"7500够吗"（询问）、"看到7500的报价"（引用）、"7500不行"（拒绝）、复合消息（预算＋机箱）、需要自由文本回答的消息一律回退 Screening。

### 三路对比口径

- **原 Screening**：冻结 `artifacts/reqv2/spec3-final-live-20260923` 的逐轮真实观测（events/results），不重新调用。
- **纯规则（R）**：确定性规则链 C1–C9（见实现设计），全部通过才快走。
- **规则＋Jev（R+J）**：确定性范围守卫（C1 候选唯一〔哨兵区间过滤后〕、C2 数值哨兵、C6 执行请求、C7 预算语义扩展与历史状态措辞、C8 复合字段）＋ Jev 判定（C3 询问、C4 拒绝、C5 引用、C9 采纳句式的语义替代）；verdict=determine 且 P(determine)≥阈值才快走，其余（含 Jev 故障/超时/不确定）一律回退。

C3/C4/C5/C9 是关键词表最脆的语义判断，正是 Jev 增量假设所在；C7 同时拦截"封顶/口径"与依赖历史状态的 restore 措辞（r1 影子轮暴露后补齐，词表是范围事实而非情感判断）：Jev 的价值既可以是否决纯规则的错误快走（precision 增量），也可以是救回纯规则保守回退的可快走轮（coverage 增量），两者分开报告。

### 正确性口径

- fast-route 正确 ＝ 快走且落地终值与金标关键字段一致；另设 `op_semantics_match` 单独记录 set/restore 等操作语义分歧（如"还是按原来的7500来"），不与值正确性混算。
- 误路由三类分开：false-fast（不可快走轮被快走——会跳过 Screening 造成错写风险）、wrong-value（快走但值错——关键字段错写）、false-fallback（可快走轮被回退——只是损失节省，无正确性风险）。
- Screening 基线不参与"正确快路由率"的分子分母，只作为节省上限与行为参照。

### 阈值与集合

- 阈值只在校准集上选：语料 calibration split ＋ Set B 的 cal 分区；report 集（语料 development ＋ Set B report 分区）只用选定阈值报告一次。
- 语料（development+calibration，43 轮）用于审计与评估；Set B（AI 起草 24 轮）用于正反例行为验证。两者都不代表真实流量；所有结论标注语料来源与"开发数据"属性。

### 影子纪律

沿用 v1 已验证的纪律：plan/provenance 先于首请求落盘；钉 `jev-1.13.0`；每轮调用独立记账；预算耗尽或证据写失败即失败（不得继续花钱）；provider 失败记为该轮 fallback，不影响其他轮；不自动重试；报告 p50/p95、token、显式费率折现（无费率则如实标注未折现）。

## Implementation design

### 域契约（`internal/decision/fastlane.go`）

```go
type FastlaneVerdict string // determine | ask | quote_reference | reject | uncertain
type FastlaneInput struct {
    CurrentTurn    string
    CandidateField string
    CandidateValue json.RawMessage
}
type FastlaneResult struct { // verdict + probabilities + confidence + tokens + duration
}
type FastlaneJudge interface{ Judge(context.Context, FastlaneInput) (FastlaneResult, error) }
```

问题定义（instructions + criteria）固定在代码里，`FastlaneQuestionJSON()/Hash()` 供 provenance 使用；不含任何被评轮次原话。

### Jev 客户端最小泛化（`internal/providers/jev`）

新增 `Question{ID, Instructions string, Options map[string]string}` 与 `Ask(ctx, state json.RawMessage, q Question) (ChoiceResult, error)`；校验逻辑参数化到 Question.Options（分布和容差、[0,1]、usage、响应模型照旧）。v1 `Classify` 改为 `Ask` 的薄包装，wire 行为与现有测试逐字节不变。

### 候选值提取导出（`internal/agents/pipeline`）

`ExtractBudgetCandidates(text string) []int`：包装既有 `budgetAmount`/`moneyAmount`/`chineseBudgetNumber`，返回去重升序金额。不改动既有 guard 行为。

### 实验引擎（`internal/planningeval/fastlane.go`）

- 语料构建：`LoadRequirementV2` 过滤 holdout；extraction 用 `seed`+reducer 重建先验状态，conversations 以前序轮金标回放重建；每轮记录 quote、先验 budget_cny 状态、金标 ops。
- 规则链 C1–C10（词表在代码里单处定义、测试覆盖正反例）。
- R+J：通过范围守卫的轮次调用 `FastlaneJudge`（实际为 jev.Client 适配器），determine≥t 快走。
- 指标与产物：audit.json（审计计数）、turns.jsonl（逐轮三路判定）、jev.jsonl（逐次调用观测）、report.md/report.json。
- Screening 基线从冻结 artifacts 目录按 (layer,id,repeat,turn) 对齐读取，缺失记 `screening_observation_missing`，不冒充。

### CLI（`cmd/evalfastlane`）

```bash
# 阶段 1：零模型机会审计（无网络）
go run ./cmd/evalfastlane -mode audit -out artifacts/fastlane/<新目录>

# 阶段 2：有界影子实验（显式正数上限、超时；JEV_API_KEY 来自环境）
JEV_API_KEY=... go run ./cmd/evalfastlane -mode shadow-live -out artifacts/fastlane/<新目录> \
  -jev-model jev-1.13.0 -jev-max-calls 64 -jev-timeout 2s \
  -screening-baseline artifacts/reqv2/spec3-final-live-20260923 \
  -jev-input-price 0 -jev-output-price 0
```

`-mode audit` 输出覆盖率与收支平衡估算；若候选不足（快走机会为 0 或无法支持方向性结论），在该模式停止，不进入阶段 2。

## Execution outline

1. 域契约、Jev 泛化、提取导出 + 单测。
2. 实验引擎 + CLI + Set B 数据集 + 规则单测。
3. 阶段 1 audit 冻结产物 → 覆盖率判断（本 change 的显式闸门）。
4. 阶段 2 shadow-live 冻结产物（仅当闸门通过）。
5. 标注复核表、运行记录、一页决策报告。

## Completion bar

- [x] 阶段 1 审计产物冻结，覆盖率与收支平衡估算可复核。
- [x] 阶段 2 影子产物冻结（候选不足时改为记录闸门未通过并停止）。
- [x] 三路对比指标齐全：覆盖率、正确快路由率、误路由、关键字段错写、可省 Screening 调用、Jev 故障回退、实际延迟与费用。
- [x] 端到端收益仅以"预测"口径出现。
- [x] 标注复核表标明待人工确认项；holdout 零接触；无生产行为变化。

## Validation

```bash
go build ./... && go vet ./... && go test ./internal/decision ./internal/providers/jev ./internal/agents/pipeline ./internal/planningeval
go test ./...
go run ./cmd/evalfastlane -mode audit -out artifacts/fastlane/<新目录>   # 零网络
```

合同检查：Jev Ask 的成功/未知选项/概率缺失/超时/错误分类；规则链正反例（含"7500够吗""看到7500的报价""预算不要超过7500""6000吧哦不对7000"）；holdout 过滤断言；plan 先于首请求；调用上限与超时；产物无密钥。

## Follow-on gate（生产 change 的前置，非本 change 结论）

只有当冻结影子结果同时满足：R+J 在快走子集零关键字段错写、相对纯规则有可指认的增量（否决或救回，任何一方向，且不是靠调题或挑重复）、Jev 故障率与 p95 延迟兼容非阻塞观察——才起草带开关、真实产品链路 A/B 与故障回退的生产 change，交用户验收。语料是开发数据，该结论不外推为生产覆盖率。

## Decision Log

<!-- Only append user-confirmed material direction changes. -->

## Result

2026-09-25 完成。实际改动：

- `internal/decision/fastlane.go`：快走判定域（五值 verdict、最小输入合同、问题定义与 SHA256 钉 `89f56597…`，测试钉死）。
- `internal/providers/jev/client.go`：最小泛化出 `Question`/`Ask`/`ChoiceResult`，校验参数化到问题选项；v1 `Classify` 成为薄包装，全部既有合同测试不变；新增 fastlane 问题的 httptest 用例。
- `internal/agents/pipeline/screening_guard.go`：导出 `ExtractBudgetCandidates`/`FindBudgetAmountTokens`（复用既有预算正则与中文数字解析，另加评估侧宽口径"金额提及"模式）；产品 guard 的 `groundedBudget` 仍用窄模式，行为零变化。
- `internal/planningeval/fastlane.go` / `fastlane_run.go`：语料构建（holdout 过滤＋断言、金标回放先验状态）、C1–C9 规则链、真实 Reducer/Readiness 落地、Jev 适配器（输入仅本轮原话+候选字段/值）、冻结 Screening 基线对齐、三路统计与阈值选择（校准集优先零误快点）、audit/shadow-live/shadow-replay 三模式与产物落盘。
- `cmd/evalfastlane`：plan/provenance 先于首请求落盘（模型、host、超时、问题哈希、数据集与 Set B 与基线哈希、二进制哈希、调用上限）；audit 强制零网络。
- 数据集 `testdata/requirement-v2/fastlane-budget-v1/set-b.json`（25 轮 AI 起草独立标注，cal 12 / report 13）。
- 文档：`docs/eval/requirement-v2/快走标注复核表-20260925.md`；`docs/eval/运行记录.md` 2026-09-25 条目；决策报告 `artifacts/fastlane/decision-20260925.md`。

验证：`go build ./... && go vet ./... && go test ./...` 全绿（41 包）。冻结产物：`audit-20260925`（语料 43 轮、5 候选、11.6% 上限、纯规则 4/4 正确 0 误快）、`shadow-r1-20260925`（首轮影子，暴露"不要超过"与 restore 守卫缺口，保留为证据）、`shadow-final-20260925`（最终 live：22 次真实 Jev 调用全成功、p50 276ms / p95 721ms、阈值 0.40；Set B 上 R+J 否决纯规则 2 次误快且零损失；语料上与纯规则持平）。

**结论：Jev 相对纯规则有可指认增量（否决方向 2 次、0 救回、0 新误快），满足起草生产 change 的信号条件；覆盖率天花板 11.6% 与"千五"解析错值缺口是硬约束。** 生产 change 轮廓见决策报告，待用户验收后另行起草；本轮未做任何生产行为变更。未验证/剩余风险：标注未经用户逐条签收；Set B 为 AI 起草；费用未折现；收益全部为预测口径。
