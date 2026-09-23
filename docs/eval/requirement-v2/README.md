# Requirement v2 评估

> 对应 change：`docs/changes/requirement-v2-evaluation-foundation.md`。数据集在 `internal/planningeval/testdata/requirement-v2/`（manifest 冻结），入口 `cmd/evalrequirement`，实现于 `internal/planningeval`（扩展而非平行 runner）。本文登记口径、split、grader、gates、已知边界与冻结基线。

## 口径

评估对象分两类，判卷互不抵消：

- **模型与 Harness 组合**（extraction / conversations，live 时需要真实 Screening）：字段操作、observation、turn signals 与多轮渐进对话的语义正确性。
- **确定性组件**（reducer / readiness / policy / ui-contract，永远零模型）：程序 oracle，必须 100% 通过，不能用模型层平均分抵消。

判卷版本 `reqv2-grader-v2`（v2 变更：provider_success/latency_p95/max_model_calls_per_turn/预算一致性统计 extraction+conversations 全部真实 provider 轮，verdict layer=model，样本范围与 report.usage 一致；v1 产物以 superseded-v1- 归档，跨版本判卷走显式 `-mode replay -regrade`，replay/compare 拒绝混用 grader 版本）：金标以结构化真值为主（expected_operations / expected_state / missing / blocking / eligible / defaults / turn signals / presentation action），不比较措辞。失败分三类：`v2_contract_gap`（当前实现没有该概念，如 turn signals、presentation action、系统默认、blocking 分离、schema 2）、`behavior_failure`（双方契约共有但行为错误）、`provider_failure`；`technical_fault` 是评估设施自身回归，必须为零。

## Split

按 case 的 `session` 字段划分（`manifest.json` 的 `split_sessions`）；同一 session 或同一 `variants_of` 模板不得跨 split，加载时强制。holdout 只评最终锁定候选，与 development/calibration 混跑会被 runner 拒绝。今天人工模拟的 FPS/7500/全部新买对话（`cv-fps-progressive`）按 spec 属 development。v1/current-178/Jev 用例未直接进入 v2 金标；金标按五份 v2 change spec 重新标注（AI 辅助起草 2026-09-22，人工复核未完成，见 provenance.json 的 known_boundaries）。

## Gates（`gates.json`，reqv2-gates-v1）

- 9 项 veto（V1 未授权 active … V9 scope 外品类承诺）：任一重复中出现即候选不可发布。grader 金丝雀（`selftest.json`）为每个 veto 提供故意违反的反例和正确路径通过样例，`check` 模式零模型验证。
- 确定性层 100% 通过；release 指标 pass^3（Best@k 只作上限诊断）；配对比较最小样本 30，不一致对 ≥6 时报精确 McNemar。
- 模型质量阈值已冻结（2026-09-22；precision/recall 按 operation 池化，不是按 case 数的允许失败例数）：
  - extraction：op precision ≥0.95、op recall ≥0.80（错误写入比漏记更不可接受：漏记可由追问补救，错写污染真值并触发 veto）、forbidden op 零容忍、turn signal 精确匹配 ≥0.90 且独立样本门槛 min_signal_turns=20、case_success（Pass^k 折叠）≥0.90、final_state_exact_match ≥0.90、provider_success ≥0.98、latency_p95 ≤10000ms（对齐 PRD 追问响应 ≤10s）、max_model_calls_per_turn ≤2（含 guard 纠偏重调）、总调用不得超过 plan.json 显式预算、min_cases=20。
  - conversations：任务成功 ≥0.80、min_cases=5、重复追问每例 0 次、veto 零容忍。
  - 关键字段（budget_cny、use_case.type、use_case.resolution、existing_parts、owned_parts、budget_basis）错值或额外写入零容忍；完全漏记只计入 recall。
  - `check` 拒绝 pending/缺字段/非法阈值（比例越界、precision<recall、非零容忍项、min_signal_turns<10、样本下限、延迟/单轮调用上限非正）。数据不足或指标缺失（含 plan 未声明预算）一律 UNEVALUABLE，不得默认通过。
  - 运行时逐项产出结构化 verdict（`gate_verdicts`：实际值/阈值/PASS-FAIL-UNEVALUABLE），任一失败或不可评估（样本不足）都判候选不可发布；replay 从冻结观测重算 model_quality 并执行相同门槛。

## 命令

```bash
# 零网络校验 fixture/manifest/split/gates + grader 金丝雀
go run ./cmd/evalrequirement -mode check
# 零模型基线（含 policy/ui 需要隔离 peval_ 数据库；无 DSN 时这两层记 skip）
PLANNING_EVAL_DSN=... go run ./cmd/evalrequirement -mode live -skip-model-layers \
  -out artifacts/reqv2/<新目录> -splits development,calibration
# live：显式正数预算、新目录、plan/provenance 先落盘；Screening 为 .env 固定模型（零重试、无链）
MODEL_MAX_RETRIES=0 PLANNING_EVAL_DSN=... go run ./cmd/evalrequirement -mode live \
  -out artifacts/reqv2/<新目录> -max-calls 60 -splits development,calibration
# 零模型重判冻结观测（不信任旧分数）
go run ./cmd/evalrequirement -mode replay -run artifacts/reqv2/<目录>
# 零模型配对对照两轮产物（同 manifest/grader 才可比）
go run ./cmd/evalrequirement -mode compare -baseline <A> -candidate <B>
```

产物目录包含完整 fixture 副本、`plan.json`（manifest/gates/grader/程序哈希、模型脱敏配置、预算、split、repeats）、`events.jsonl`（逐 turn 观测与模型调用）、`results.jsonl`（逐 case 冻结观测 + 断言）、`report.json` / `report.md`。不记录凭据与隐藏思维链。

## 金标定向修正（2026-09-23，grader 不变，manifest abb9cd25…）

- 授权更正两条建基时按 v1 行为误标的 readiness 金标：`rdy-budget-conflict-blocks` 补 `existing_parts`、`rdy-holdout-owned-basis` 补 `use_case.titles`;其余金标、split、gates 未动,holdout 未运行。记录见 [人工复核-20260923.md](人工复核-20260923.md) 与 `provenance.json` 的 `labeling.gold_corrections`。
- manifest:`558de144… → abb9cd25…`(readiness/cases.json `485436e6… → c9952e79…`,frozen_at 2026-09-23);grader 判定逻辑未变,版本保持 `reqv2-grader-v2`。
- 产物:旧基线原样保留;新零模型重判 `artifacts/reqv2/replay-goldfix-deterministic-20260923`(replay 旧基线冻结观测,67 例,verdict 变化 0);Spec 2 实现候选 `artifacts/reqv2/spec2-rework-deterministic-20260923`(dev+cal:reducer 11/11、readiness 12/12,这两层 veto 0;policy 3/8、veto V9×1,ui-contract 0/3,归属后续 change)。

## 历史冻结基线（2026-09-22，reqv2-grader-v2，当时的 v1 实现）

| Run | 产物 | 口径 |
|---|---|---|
| 零模型基线 | `artifacts/reqv2/baseline-deterministic-20260922` | development+calibration，repeats=1，零调用，grader v2 直跑 |
| live 基线 | `artifacts/reqv2/baseline-live-20260922` | grader v2 对 `superseded-v1-live-20260922` 冻结观测的零模型 regrade（plan.json 记录 source_run/source_grader_version/source_report_sha256；未调用 provider） |
| 已废弃 | `superseded-v0-*`、`superseded-v1-*` | 门槛冻结前与 grader-v1 产物，不作冻结基线 |

live 基线（以下全部取自冻结 report.json，`gate_passed=false，候选不可发布`）：

| Verdict | 实际 | 门槛 | 结果 |
|---|---|---|---|
| extraction.min_cases | 26 | ≥20 | PASS |
| extraction.operation_precision | 0.750 | ≥0.95 | FAIL |
| extraction.operation_recall | 0.800 | ≥0.80 | PASS |
| extraction.forbidden_op_total | 2 | ≤0 | FAIL |
| extraction.turn_signal_exact_match | 0.000 (0/25) | ≥0.90 | FAIL |
| extraction.case_success | 0.000 (0/26) | ≥0.90 | FAIL |
| extraction.final_state_exact_match | 0.733 (11/15) | ≥0.90 | FAIL |
| model.provider_success | 1.000 (43/43) | ≥0.98 | PASS |
| model.latency_p95_ms | 4298ms | ≤10000ms | PASS |
| model.max_model_calls_per_turn | 1 | ≤2 | PASS |
| model.total_calls_within_budget | 43 | ≤60 | PASS |
| conversations.min_cases | 7 | ≥5 | PASS |
| conversations.task_success | 0.000 (0/7) | ≥0.80 | FAIL |
| conversations.repeated_question_max_per_case | 2 | ≤0 | FAIL |
| all.veto_total | 10 | ≤0 | FAIL |
| extraction+conversations.key_field_wrong_write_total | 14 | ≤0 | FAIL |

- 用量：43 次 provider 请求（预算 60）、token 162571（全部已知 true）、p50/p95 2816/4298ms；gate `model.latency_p95_ms` 与 `usage.latency_p95_ms` 同为 4298ms、provider 分母为全部 43 个真实模型轮（26 extraction + 17 conversations）。
- 分层（pass^1）：reducer 3/11、readiness 1/12、policy 3/8、ui-contract 0/3、extraction 0/26、conversations 0/7；模型层独立样本 33 例。
- 零模型基线：模型层门槛全部 UNEVALUABLE（provider/latency/per-turn/预算四项亦然）、veto 3，不可发布。
- compare：模型层 33 对、0 新增通过、0 退步（不一致对<6 只记观察）；确定性层两轮逐层一致。

## 已知边界

- V5（Builder 输入 hash ≠ 核定快照）只有 grader 金丝雀覆盖：v2 驱动器用 scripted-error builder 观察 admission，不产生成功 build；产品侧 V5 证据待确认门禁 change 后补。
- V8/V9 的产品侧检测是中文关键词启发式，命中需人工复核原文。
- 追问检测（forbidden_questions）按字段标签关键字匹配，等价于 v1 S3 的保守启发式。
- policy 层的 scripted screening（next_action/ops/reply）是适配器输入，代表“模型声称”的极端情况，不是金标；guard 的 collect 纠偏重调由 `screen_oracle_fallback` 提供第二条 scripted 输出。
- ui-contract 只判后端 DTO 真值；浏览器呈现另行执行。
- 基线 repeats=1：Pass^3 是发布口径，基线只做差距记录。
- compare 只统计 extraction/conversations 的独立 case（repeat 折叠为 pass^k）；确定性层单独报告且不参与 minimum_paired_samples；模型层独立样本 <30 时显式声明“样本不足，不能宣称改善”。
- 金标人工复核表见 [人工复核-20260922](人工复核-20260922.md)（9 个 holdout 全量 + cv-fps-progressive + ex-monitor-request + accepted-proposal 边界；holdout 未运行）。
- 同一 case 的 repeat 按 pass^k AND 折叠后才进入任务成功率与配对比较；repeat 不是独立样本。
