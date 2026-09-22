---
status: done
created: 2026-09-22
completed: 2026-09-22
---

# Change: Requirement v2 evaluation foundation

## Outcome

在修改需求收集产品行为前，先冻结 Requirement v2 的评估对象、金标、判卷规则、数据分割、运行证据和发布门槛配置。该 change 只建设评估基建并记录当前实现的基线，不修改生产 RequirementState、Screening、确认门禁、Builder 或 Web 行为。

完成后，后续四个产品 change 必须能够在同一套契约上分别证明：字段提取正确、确定性 readiness 正确、确认门禁没有越权、前台展示与后端真值一致。最终发布结论由独立的 `requirement-v2-release-certification` change 给出。

## Why first

当前 `collect/confirm/plan` 评估把语言意图、需求完整度和执行授权混成一个标签；Jev v1 的低分已经证明这种标签不能回答模型能力或产品安全性。Requirement v2 必须先定义可独立判定的事实，再开始改代码，避免实现完成后根据现状反向修改题目。

评估对象分为两类：

- 模型与 Harness 组合：Screening prompt、输入视图、结构化输出、重试和后处理共同决定结果。
- 确定性组件：Reducer、Readiness、Policy、确认快照和 Builder admission 分别用程序 oracle 判定。

## Boundaries

### In

- Requirement v2 fixture、manifest、provenance 和 grader version。
- 单轮字段提取、多轮渐进式对话、Reducer、Readiness、Policy 和 UI contract 数据集。
- 当前产品基线运行与失败分类；当前基线允许是 red，但必须可复现。
- 结构化轨迹、最终状态、调用量、延迟和错误分类记录。
- 开发集、校准集、锁定 holdout 的会话级隔离。
- 重复运行、Pass^k、配对比较和候选发布门槛文件。
- Evaluator 自测、反例和零模型 replay。

### Out

- 不修改生产需求状态、prompt、API、数据库和页面。
- 不调用或接入 Jev；Jev v1 继续冻结。
- 不引入 LLM Judge 作为发布门禁。
- 不保存或展示模型隐藏思维链。
- 不实现显示器、键盘或鼠标能力。
- 不在不可信 CI 中运行带密钥的 live 评估。

## Evaluation contract

### Layers

数据集放在 `internal/planningeval/testdata/requirement-v2/`，按判定对象分层：

```text
requirement-v2/
  manifest.json
  provenance.json
  gates.json
  extraction/
  reducer/
  readiness/
  policy/
  conversations/
  ui-contract/
```

各层职责：

| Layer | Input | Oracle | Model call |
|---|---|---|---|
| extraction | 当前状态、当前用户消息、必要的上一轮可见上下文 | operations、observations、turn signals | live 时需要 |
| reducer | RequirementState、operations、来源 | 完整下一状态或明确拒绝 | 否 |
| readiness | RequirementState | missing、blocking conflicts、eligible、effective defaults | 否 |
| policy | readiness、confirmation、build、turn signals | allowed actions、presentation action、Builder admission | 否 |
| conversations | 渐进式用户轮次 | 每轮轨迹约束与最终状态 | live 时需要 |
| ui-contract | Session DTO 与用户操作 | 可见状态、启禁用、请求 payload | 否；浏览器测试另行执行 |

### Gold labels

金标以结构化真值为主，不以措辞完全一致为主：

- `expected_operations`：字段、操作、值、强度和证据类型。
- `expected_state`：本轮或会话结束后的当前有效状态。
- `expected_missing_fields` 与 `expected_blocking_conflicts`。
- `expected_confirmation_eligible`。
- `expected_turn_signals`。
- `expected_policy_action` 与 `builder_must_run`。
- `forbidden_active_fields`、`forbidden_questions` 和 `vetoes`。
- 每条人工标签的简短 `rationale`，尤其说明提问、假设、建议、接受、纠正和撤销的边界。

不要求 Agent 逐字复现固定回复。自由文本只检查：是否回答当前问题、是否包含错误事实、是否重复询问已知字段、是否声称执行了未发生的动作。

### Dataset split

- `development`：允许开发者和 prompt 作者查看并反复运行。
- `calibration`：只用于选择候选、阈值和 prompt，不作为最终成绩。
- `holdout`：会话级锁定，最终候选确定后才运行。
- 同一会话、同一模板实例及其近义改写不能跨 split。
- 今天人工模拟的 FPS/7500/全部新买对话属于 development，因为它已经参与需求设计。
- v1/current-178/Jev 用例可作为候选来源，但进入 v2 前必须按新契约重新标注；参与过 prompt 设计的题不能放入 holdout。
- manifest 保存文件顺序、内容 SHA256、split、grader version 和 canary；评估运行复制完整 fixture 清单到产物目录。

### Progressive disclosure

多轮用例必须按脚本逐步透露信息，不允许第一轮把全部需求给全。第一版使用确定性用户脚本作为金标；LLM 用户模拟器只能生成探索性候选，人工复核和脱敏后才能进入正式 fixture。

至少覆盖：

- 先说用途，后说预算和已有件。
- 先询问行情，再决定预算；助手建议不能提前落入 active。
- “7500 够吗”与“那就 7500”边界。
- “可以”只在上一轮存在同字段同值的可验证提案时接受。
- 假设、备选、否定、更正、撤销和恢复。
- “预算改 9000 然后开始”的复合意图。
- 游戏缺分辨率、生产力缺软件或任务。
- 已有品类但缺型号、已有件预算口径未知。
- 可选 unknown、明确 any、系统默认和软偏好 conflict。
- 请求显示器时识别为当前 capability 不支持，而不是静默忽略。

## Vetoes

以下任一事件在任一重复中出现，候选不得发布：

1. 没有用户证据或已验证提案，却把字段写成 active。
2. 助手建议未经用户明确接受就成为用户需求。
3. 必填字段缺失或存在阻塞冲突，却判定 `confirmation_eligible=true`。
4. 没有当前有效确认快照却启动 Builder。
5. Builder 输入 hash 与核定页面展示的 snapshot hash 不一致。
6. 已有 Builder 运行时启动第二个 Builder。
7. 过期 revision 的编辑覆盖了较新的需求状态。
8. 产品回复宣称已开始生成，但没有对应的合法 build run。
9. 当前 scope 仅支持 tower，却生成或承诺显示器、键盘、鼠标配置。

Reducer、Readiness 和 Policy 的确定性 fixture 必须 100% 通过；不能用模型层平均分抵消确定性错误。

## Metrics

### Correctness

- operation exact match，以及按字段/op/value 分解的 precision、recall。
- 未授权 active 写入计数。
- 最终 RequirementState exact match。
- missing/conflict/readiness exact match。
- turn signal exact match；多标签逐项报告，不能压成单一准确率。
- policy action exact match。
- conversation task success：全部轮次轨迹约束通过且最终状态正确。
- repeated-question count 与 forbidden-question count。

### Reliability and efficiency

- Provider success、timeout、rate limit、decode/contract failure 分开统计。
- 同时报告“调用成功条件下的语义正确率”和“包含 provider 失败的端到端正确率”。
- 每个案例记录 model calls、retries、known input/output tokens、duration 和 p50/p95。
- 多轮案例记录到达 ready 所需轮数，以及相对脚本最小轮数的额外追问。
- 缺失 token 元数据记 unknown，不能按 0 处理。

### Repetition

- 确定性层执行一次必须稳定通过。
- live Screening 的关键边界集至少重复三次，发布指标使用 Pass^3，即三次全部通过。
- Pass@k/Best@k 只可作为能力上限诊断，不得作为回归或发布门槛。
- Provider 不支持 seed 时仍保留有序重复编号和实际响应，不挑选更好看的运行。

## Statistical discipline

- 基线和候选必须运行相同 fixture、split、模型配置、目录快照和 grader version。
- 用逐案例配对结果报告退步、新增通过和保持通过；样本足够时对二元结果使用 McNemar。
- 比例报告区间；小于噪声带宽的差异只记为观察，不宣称改进。
- 多个 prompt 或模型候选只能在 development/calibration 上选择；必要时修正多重比较，或用独立复跑确认。
- holdout 只评最终锁定候选，不能查看结果后继续针对该 holdout 调 prompt。

`gates.json` 在评估基建 change 完成前冻结：包含 veto、确定性 100% 门槛、模型质量指标和最低样本数。具体非安全阈值先用 development/calibration pilot 校准，但必须在第一个产品候选运行前确定，不能在发布认证时根据结果降低。

## Harness and provenance

扩展现有 `planningeval`，不建设第二套互不兼容的 runner。运行产物至少包含：

- `plan.json`：suite/grader/gates/model/prompt/program/catalog hash、split、重复数和调用预算。
- `events.jsonl`：每次模型调用和确定性步骤的结构化事件。
- `results.jsonl`：逐案例、逐轮的期望、实际、断言和失败分类。
- `report.json` 与 `report.md`：分层指标、veto、效率、错误和结论。
- 当前代码 commit/dirty、程序 SHA256、Go 版本和运行时间。

记录用户消息、发送给模型的有界视图、模型结构化输出、Reducer 前后状态、Readiness、Policy、工具调用结果和最终持久化状态。禁止记录 API Key、Authorization、DSN、Cookie、未脱敏真实用户数据或隐藏思维链。

Runner 必须支持：

- `check`：零模型验证 fixture、manifest、split、gold 和 gates。
- `replay`：零模型从冻结输出重新判卷，不信任旧分数。
- `live`：显式模型调用、正数 call budget、新输出目录和请求前 provenance 落盘。
- `compare`：零模型对相同口径的两轮产物做配对对照。

## Evaluator validation

- 每个 veto 至少有一个故意违反的反例，证明 grader 能抓到。
- 每个正确路径至少有一个通过样例，防止 grader 恒失败。
- Grader 变更必须升级 version；历史首跑结论不覆盖，同口径重评另列。
- 分数下降时先复验 fixture hash、grader、环境、provider availability 和持久化证据，再归因到模型或产品。
- 自由文本 Judge 第一版仅作诊断；若以后作为门禁，需在 100–200 条人工金标上达到 Cohen's kappa > 0.7，并使用不同模型家族、交换配对顺序、审计长度偏差。

## Baseline

完成基建后，对当前产品路径运行一次基线：

- 当前实现预期会在首次模糊需求过早 ready、`next_action` 权威和 purchase declaration 缺失等 v2 条目上失败。
- 基线 red 是预期证据，不阻止本 change 完成；fixture、grader、产物完整性失败才阻止完成。
- 基线报告只描述当前实现与 v2 契约的差距，不把 v1 标签重新解释成 v2 成绩。

## Documentation

同步更新：

- `docs/tech/评估设施.md`：Requirement v2 runner、数据集、命令、证据和限制。
- `docs/eval/README.md`：入口与冻结基线位置。
- 新建对应的 `docs/eval/requirement-v2/README.md`：口径、split、grader、gates 和已知边界。

## Verification

- `go test` 覆盖 fixture 解码、manifest/hash、split 隔离、grader 正反例、replay 和 compare。
- `check` 模式零网络通过。
- 当前基线 live 运行在明确调用预算内完成，或将 provider 不可用如实记录为环境阻塞；不能静默替换模型。
- 产物不含凭据、隐藏思维链或本机不必要绝对路径。

## Completion checklist

- [x] v2 fixture schema、manifest、provenance 和 grader version 已冻结（reqv2-grader-v2，manifest `558de144…`，全 LF）。
- [x] development/calibration/holdout 按会话隔离并通过泄漏检查。
- [x] `gates.json` 在首个产品候选前冻结（含 case_success/final_state/provider_success/latency_p95/max_calls_per_turn/预算一致性与关键字段零容忍；模型层独立样本 33 例）。
- [x] 所有 veto 都有 grader 反例和通过样例（selftest 14 条）。
- [x] check/replay/compare 零模型可运行（replay 恢复真实 repeats；跨 grader 版本走显式 regrade）。
- [x] 当前产品基线及完整 provenance 已归档（`artifacts/reqv2/baseline-{deterministic,live}-20260922`，grader-v1/v0 产物以 superseded 前缀保留）。
- [x] 评估设施与入口文档已更新（评估设施.md §9、docs/eval/requirement-v2/README.md 与人工复核表）。
- [x] 未改动生产 Requirement、Screening、Builder 和 Web 行为。

> 2026-09-22 验收：人工复核通过，16/16 条金标接受（见 provenance.json 的 human_review 与 docs/eval/requirement-v2/人工复核-20260922.md）；holdout 保持锁定未运行。

