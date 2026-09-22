---
status: proposed
created: 2026-09-22
---

# Change: Requirement v2 release certification

## Dependency

仅在以下 change 全部完成并通过各自验收后执行：

1. `requirement-v2-evaluation-foundation`
2. `requirement-state-readiness-v2`
3. `screening-requirement-collection-v2`
4. `requirement-confirmation-builder-gate-v2`
5. `requirement-workspace-sidebar-v2`

## Outcome

对一个锁定的 Requirement v2 候选进行独立发布认证，产出可复现的 `go/no-go` 报告。本 change 不顺手修改业务代码、prompt、fixture、grader 或门槛；发现失败时回到对应产品 change 修复，形成新的候选后重新开始认证。

## Boundaries

### In

- 锁定候选、模型、prompt、程序、目录快照、fixture、grader 和 gates。
- deterministic、live Screening、多轮、Policy、持久化和 Web E2E 全套验证。
- 关键集 Pass^3。
- 当前实现基线与 v2 候选的配对比较。
- 至少一次 model swap 和一次 Harness 消融，用于定位收益来源。
- 正确性、可靠性、效率、成本和环境失败报告。
- 评估系统自审与最终人工抽查。

### Out

- 不修改产品代码、prompt、标签、grader 或 gate。
- 不在认证结果出来后挑选更好看的重复。
- 不接入 Jev 或评估显示器/外设。
- 不把 LLM Judge 诊断分作为发布门槛。
- 不把小样本差异描述为普遍模型排名。

## Candidate freeze

首次外部调用前写入 `plan.json`：

- Git commit、dirty 文件摘要和实际评估程序 SHA256。
- Requirement/Screening prompt SHA256。
- Screening requested/response model 和完整脱敏配置来源。
- suite manifest、grader、gates、catalog/snapshot hash。
- split、重复次数、调用上限、timeout、token budget。
- Go/Node/browser 版本和运行环境。

认证要求 clean candidate commit；本地评估产物可以 gitignored，但必须回链该 commit。若 dirty，仅允许评估工具生成的明确非源码产物，否则认证无效。

## Execution plan

### 1. Evaluator audit

- 运行 fixture/hash/split/gate `check`。
- 运行 grader 正反例，证明每个 veto 可被抓到。
- 对 foundation 基线做零模型 replay，确认 grader version 没有漂移。
- 检查 provider、数据库、Redis、目录快照和浏览器环境；环境失败不得记为语义退步。

### 2. Deterministic suites

- Reducer。
- Readiness。
- Policy/Builder admission。
- Snapshot hash、revision、idempotency、并发和持久化。
- UI contract 与前端组件测试。

要求全部通过且无 data-error。

### 3. Live Screening and conversations

- development 只用于确认运行可用，不参与最终选择。
- 对 calibration 上已经选定的唯一候选做固定重复，确认没有明显环境异常。
- 对锁定 holdout 运行一次候选批次；关键边界每题至少三次，按 Pass^3 判定。
- 保存每次首跑，不允许重跑后只选成功结果。

### 4. End-to-end product path

通过真实产品服务、存储和浏览器验证：

- 渐进式需求收集。
- 侧栏编辑与 chat reducer 等价。
- 核定快照与 Builder payload hash。
- 运行中编辑和第二 Builder 拒绝。
- build success/failure/retry。
- 修改后旧配置、版本和 diff。

既检查轨迹，也直接读取最终数据库/API 状态；不能只依据页面文案或 Agent 宣称。

### 5. Model swap

固定 v2 Harness、prompt、suite 和其他配置，只替换一个明确记录的 Screening 模型。目的不是自动选择模型，而是判断剩余失败主要来自模型能力还是 Harness：

- 强模型显著改善：记录模型能力边界和成本/延迟代价。
- 换模型几乎不变：优先检查输入、prompt、schema、guard 或标签。
- 结果只在相同实际模型可用、相同重复口径下比较；禁止静默 fallback。

### 6. Harness ablation

在评估入口而非生产 feature flag 中关闭一个预先声明的组件，例如结构化 RequirementState prompt view 或 accepted-proposal context。只改变一个因素，并在同一 calibration 子集做配对比较。

确定性 Readiness/Builder admission 属于安全边界，消融只能离线观察，不能产生可发布候选。

## Release gates

使用 foundation 已冻结的 `gates.json`，不得在本 change 调低。最低硬约束：

- 所有 veto：0。
- deterministic reducer/readiness/policy/persistence：100%。
- Builder 未授权启动：0。
- 非用户事实 active 写入：0。
- required missing/conflict 被判 ready：0。
- snapshot 展示/hash/Builder 输入不一致：0。
- 关键边界满足 Pass^3。
- candidate 不得相对冻结 baseline 新增安全失败。

非安全指标使用 gates 中预先冻结的阈值和最低样本数，包括 operation precision/recall、final-state exact match、conversation success、重复追问、延迟和调用预算。样本不足或置信区间重叠时只能给“不足以证明改善”，不能以点估计宣布胜出。

## Failure taxonomy

每个失败只选一个主因并保留次要标签：

```text
dataset_or_gold
grader
environment_or_provider
prompt_or_input
model_semantics
decode_or_contract
reducer
readiness
policy_or_admission
persistence_or_concurrency
api
ui_state
ui_accessibility
```

自动映射只是初步归类，不宣称因果；所有 veto 和分数显著变化必须人工查看完整结构化证据。

## Efficiency report

与当前基线和选定候选比较：

- 每轮及每会话 model calls。
- known input/output tokens。
- p50/p95 Screening latency。
- 达到 ready 的轮数与额外追问。
- repair/retry 次数。
- 端到端 confirm 到 build start 的延迟。
- 浏览器关键交互耗时。

成功率提高但调用、token 或延迟显著恶化时，报告必须明确成本收益，不用单项机制指标代替最终用户任务成功。

## Human review

发布结论前人工抽查：

- 全部 veto 候选和 grader 边界。
- 所有 baseline/candidate 胜负翻转。
- “7500 够吗”/“那就 7500”/“可以”等证据边界。
- 混合修改＋开始请求。
- 多轮问题回答是否先于追问。
- UI 是否展示 system default、unknown、conflict 和 stale build。
- 三档视口与键盘路径。

自由文本只按预先定义 rubric 诊断，不因为更长、更热情或更像预期措辞而加分。

## Artifacts

输出到新的、不可覆盖目录，例如：

```text
artifacts/requirement-v2-certification-<date>/
  plan.json
  events.jsonl
  results.jsonl
  report.json
  report.md
  comparison.json
  ui/
```

`report.md` 必须包含：

- 候选身份和证据完整性。
- 数据集/split/repeat/grader/gates。
- 分层指标和区间。
- veto 明细。
- provider availability 和错误分类。
- model swap/ablation 的适用边界。
- 效率与成本。
- 已知限制。
- 明确 `GO` 或 `NO-GO`，以及判断依据。

## Go/no-go policy

### GO

- 所有硬门槛通过。
- 非安全指标达到冻结 gates。
- 没有未解释的数据、grader 或环境异常。
- 人工抽查未发现系统性误判。
- 文档、OpenAPI 和实际 UI/行为一致。

### NO-GO

- 任一 veto 出现。
- deterministic 层非全绿。
- holdout 或证据不完整。
- 通过修改标签、排除失败或挑选重复才达到门槛。
- provider/环境不可用导致关键结果未知。
- 成本或延迟超过冻结上限且没有明确、被接受的收益依据。

NO-GO 后不得在本 change 内修改产品；应新开或恢复对应产品 change，修复后以新的候选身份重新认证。

## Documentation

- 更新 `docs/tech/评估设施.md` 的最终运行方式和口径。
- 更新 `docs/eval/requirement-v2/README.md` 的认证结果、边界和产物位置。
- 更新 `docs/eval/运行记录.md`，不覆盖旧 v1/Jev 结果。
- 只有 GO 后才在路线图把 Requirement v2 标为已交付。

## Verification

- `go test ./... && go vet ./...`。
- Web typecheck、lint、unit 和规定 Playwright 视口。
- evaluation check/replay/compare。
- 显式、预算有界的 live Pass^3。
- 真实临时数据库的确认/Builder 快照工作流。
- 报告与 events/results 数量、hash、模型用量相互一致。

## Completion checklist

- [ ] 候选、prompt、模型、suite、grader、gates 和程序已冻结并落盘。
- [ ] Evaluator 正反例和 baseline replay 通过。
- [ ] Deterministic、live、E2E 和 UI 套件全部执行。
- [ ] 关键边界按 Pass^3 报告，无挑选重复。
- [ ] Model swap 和单因素 ablation 已完成并限定结论。
- [ ] 所有失败均有证据和主因分类。
- [ ] 人工抽查完成。
- [ ] 报告给出明确 GO/NO-GO。
- [ ] 未在认证阶段修改业务代码、prompt、fixture、grader 或 gates。

