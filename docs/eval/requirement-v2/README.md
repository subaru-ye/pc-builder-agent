# Requirement v2 评估

> 对应 change：`docs/changes/requirement-v2-evaluation-foundation.md`。数据集在 `internal/planningeval/testdata/requirement-v2/`（manifest 冻结），入口 `cmd/evalrequirement`，实现于 `internal/planningeval`（扩展而非平行 runner）。本文登记口径、split、grader、gates、已知边界与冻结基线。

## 口径

评估对象分两类，判卷互不抵消：

- **模型与 Harness 组合**（extraction / conversations，live 时需要真实 Screening）：字段操作、observation、turn signals 与多轮渐进对话的语义正确性。
- **确定性组件**（reducer / readiness / policy / ui-contract，永远零模型）：程序 oracle，必须 100% 通过，不能用模型层平均分抵消。

判卷版本 `reqv2-grader-v3`（v3 变更 2026-09-23：V9 从纯关键词改为按句检测"承诺动词+无豁免"，明确告知当前 tower 范围不含外设的诚实说明不再误判，见下方判卷修正记录；v2 变更：provider_success/latency_p95/max_model_calls_per_turn/预算一致性统计 extraction+conversations 全部真实 provider 轮，verdict layer=model，样本范围与 report.usage 一致；旧产物以 superseded- 前缀归档，跨版本判卷走显式 `-mode replay -regrade`，replay/compare 拒绝混用 grader 版本）：金标以结构化真值为主（expected_operations / expected_state / missing / blocking / eligible / defaults / turn signals / presentation action），不比较措辞。失败分三类：`v2_contract_gap`（当前实现没有该概念，如 turn signals、presentation action、系统默认、blocking 分离、schema 2）、`behavior_failure`（双方契约共有但行为错误）、`provider_failure`；`technical_fault` 是评估设施自身回归，必须为零。

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

## 判卷修正：grader-v3（2026-09-23，Screening v2 change 期间）

- 缺陷：V9 纯关键词规则（`显示器|键盘|鼠标|键鼠` 命中即违规）把产品正确行为——明确告知当前 tower 范围不含外设——也判为违规（`pol-monitor-promise-guard` 的新产品回复为诚实范围说明，人工复核确认非承诺配置）。
- 修正：V9 按句检测，句子同时含外设词与承诺/配置动词、且不含"明确告知暂不支持"豁免（仅支持主机|暂不在…范围|暂不支持|当前配置范围|等待后续支持|先放弃）时才违规。产品文案未为迎合判卷而修改。
- 金丝雀：`selftest.json` 新增 `st-v9-scope-notice-pass`（诚实说明必须通过）；`st-v9-peripheral-promise`（真实承诺必须命中）保持 fail。
- 金标：全部层金标（含 holdout）零修改；manifest 仅 `grader_version` 与 `selftest.json` 哈希变化（`abb9cd25… → e4f4493d…`）。
- 归档与 regrade：grader-v2 产物以 `superseded-v2-` 前缀归档；对最近冻结观测做零模型 regrade（67 cases，verdict changes: 0），provenance 记录于 `provenance.json grader_changes`。

## 金标定向修正（2026-09-23，grader 不变，manifest abb9cd25…）

- 授权更正两条建基时按 v1 行为误标的 readiness 金标：`rdy-budget-conflict-blocks` 补 `existing_parts`、`rdy-holdout-owned-basis` 补 `use_case.titles`;其余金标、split、gates 未动,holdout 未运行。记录见 [人工复核-20260923.md](人工复核-20260923.md) 与 `provenance.json` 的 `labeling.gold_corrections`。
- manifest:`558de144… → abb9cd25…`(readiness/cases.json `485436e6… → c9952e79…`,frozen_at 2026-09-23);grader 判定逻辑未变,版本保持 `reqv2-grader-v2`。
- 产物:旧基线原样保留;新零模型重判 `artifacts/reqv2/superseded-v2-replay-goldfix-deterministic-20260923`(replay 旧基线冻结观测,67 例,verdict 变化 0);Spec 2 实现候选 `artifacts/reqv2/superseded-v2-spec2-rework-deterministic-20260923`(dev+cal:reducer 11/11、readiness 12/12,这两层 veto 0;policy 3/8、veto V9×1,ui-contract 0/3,归属后续 change)。

## 历史冻结基线（2026-09-22，reqv2-grader-v2，当时的 v1 实现）

| Run | 产物 | 口径 |
|---|---|---|
| 零模型基线 | `artifacts/reqv2/superseded-v2-baseline-deterministic-20260922` | development+calibration，repeats=1，零调用，grader v2 直跑 |
| live 基线 | `artifacts/reqv2/superseded-v2-baseline-live-20260922` | grader v2 对 `superseded-v1-live-20260922` 冻结观测的零模型 regrade（plan.json 记录 source_run/source_grader_version/source_report_sha256；未调用 provider） |
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

- `fastlane-budget-v1/` 子目录属于 Jev v2 快走可行性实验（`docs/changes/jev-v2-fastlane-feasibility.md`）的 Set B 独立标注表，**不在本数据集 manifest 冻结范围内**；freeze-manifest 不会枚举它，修改它需按该 change 的标注复核表重新冻结运行。

- V5（Builder 输入 hash ≠ 核定快照）只有 grader 金丝雀覆盖：v2 驱动器用 scripted-error builder 观察 admission，不产生成功 build；产品侧 V5 证据待确认门禁 change 后补。
- V8/V9 的产品侧检测是中文关键词启发式，命中需人工复核原文。
- 追问检测（forbidden_questions）按字段标签关键字匹配，等价于 v1 S3 的保守启发式。
- policy 层的 scripted screening（next_action/ops/reply）是适配器输入，代表“模型声称”的极端情况，不是金标；guard 的 collect 纠偏重调由 `screen_oracle_fallback` 提供第二条 scripted 输出。
- ui-contract 只判后端 DTO 真值；浏览器呈现另行执行。
- 基线 repeats=1：Pass^3 是发布口径，基线只做差距记录。
- compare 只统计 extraction/conversations 的独立 case（repeat 折叠为 pass^k）；确定性层单独报告且不参与 minimum_paired_samples；模型层独立样本 <30 时显式声明“样本不足，不能宣称改善”。
- 金标人工复核表见 [人工复核-20260922](人工复核-20260922.md)（9 个 holdout 全量 + cv-fps-progressive + ex-monitor-request + accepted-proposal 边界；holdout 未运行）。
- 同一 case 的 repeat 按 pass^k AND 折叠后才进入任务成功率与配对比较；repeat 不是独立样本。

## Spec 3（Screening 收集 v2）最终验收 live 运行（2026-09-23，grader-v3，代码冻结后）

- 产物 `artifacts/reqv2/spec3-final-live-20260923`（development+calibration，repeats=3（Pass^3），max-calls=400，实际 129 次真实调用，grader reqv2-grader-v3，manifest f8fa51e6…）。授权边界四轮定向返工的迭代产物 `spec3-live/rework/rework2-live-20260923` 均保留，旧分数未沿用。
- 授权边界最终形态：接受需完整匹配肯定短答（可以/好的/同意/就这样…）或以采纳前缀开头且带具体提案值的采纳句（按/就按/就用/定为/敲定/采纳/确认/来个 + 值）；询问/拒绝/中性表达一律不写入 active 且保留 observation；accepted_proposal 失败不降级 stated；提案文本经 V8/V9 守卫并须以接受问句收尾；建议保存绑定 assistant 消息、仅紧邻下一条用户消息有效；多提案裸"可以"指向不明不自动采用。
- **最终结果：冻结 gate verdict 16/16 全部通过、全层 veto 0**——extraction precision 1.000 / recall 1.000 / turn signals 72⁄75 / case_success 25⁄26（0.962） / final_state 15⁄15 / forbidden op 0 / 关键字段错写 0；conversations precision 1.000 / recall 1.000 / task_success 6⁄7（0.857） / 重复追问 0 / 错写 0；provider 129⁄129、p95 5023ms、单轮调用 ≤1、总调用 129≤400。
- 模型层 Pass^3=31/33：ex-all-new-purchase（requests_build 0⁄3）、cv-composite-9000-start（2⁄3）为残留模型波动，不触及冻结门槛；gate_passed=false 仅由归属后续 change 的确定性层红项（ui-contract 0/3 → workspace sidebar、policy pol-edit-while-running → Builder gate）驱动，不作为本 change 失败。
- 零模型回归证据：冻结真实输出 40 轮经真实 Service 单轮重执行（requirement_rework_replay_test.go），无 accepted_proposal 非法写入；eval check/replay 通过。

## Spec 3（Screening 收集 v2）确定性运行（2026-09-23，grader-v3）

- 产物 `artifacts/reqv2/spec3-deterministic-20260923`（dev+cal，repeats=1，零模型）：
  - reducer 11/11、readiness 12/12（veto 0）。
  - policy 7/8、veto 0：presentation action（focus_missing_requirement / open_requirement_review）全部转绿；`pol-edit-while-running`（生成中编辑策略）仍 red，归确认/Builder gate change。
  - ui-contract 0/3 仍红（结构化 readiness 块、confirm payload、effective defaults 展示），归 workspace sidebar change；不以此宣称整体验收。

## Spec 6 发布认证正式运行（2026-09-24，暂定 NO-GO，待人工抽查）

- 候选 commit `e9e0c7d`（clean），grader v4、manifest `bb03c00b…`、gates `a30cdfaf…` 未动；五批 live 首跑存证（A dev+cal r1、B dev+cal r3 正式、C holdout r3 单批次、D model swap qwen3.8-max-0902 r3、E 单因素消融 cal r3），总 337 次真实调用全部成功、veto 0。汇总产物与报告：`artifacts/requirement-v2-certification-20260924/`（report.md / report.json / comparison.json / failures-and-flips.md / ui/）。
- 正式批次 B：16 项冻结门槛 15 PASS / 1 FAIL——conversations task_success pass^3 4/7（0.571<0.80）；extraction 26/26、veto 0。holdout C：pass^3 6/9，失败 pol-holdout-confirm-incomplete（0/3，**归因待审**：评估适配器把产品的 invalid_request"缺核定预览哈希"兜底映射为 stale_revision，且 fixture seed 引文与用户原话不匹配，见 erratum E2）、ex-holdout-budget-approx（0/3，失败点为 use_case.type 值错与 titles 漏发；budget=3000 与 existing_parts=[] 符合金标）、cv-holdout-productivity（0/3，titles 捕获问题）。
- model swap：qwen3.8-max-0902 无统计显著改善（McNemar p=0.69）且引入 extract 新失败（23/26）、关键字段错写 1、p95 5898ms → **不换模型**；不显著差异不能定位残留失败主因（模型 vs Harness），主因列待检假设。消融：模型可见 RequirementState 视图置空后 calibration 8/8 vs 8/8 无差异（单轮 extraction 非承载因素；多轮不在覆盖内；消融二进制仅评估工具三文件变更，code_dirty=true 如实记录）。
- 归因勘误（2026-09-24 第二轮，零模型核对，**分数不变**）：批次 B `cv-fps-progressive` 为 2/3 通过、仅 r3 失败——三个 repeat 第 3 轮均输出 `set budget_cny=7500`，r1/r2 `evidence=stated` 落库，r3 误标 `accepted_proposal` 而其前一轮无提案（原始输出 `proposals: []`），服务端 `verifyAcceptedProposals` 按授权合同拒收；**归因定案：模型误标证据类型，服务端拒收正确**（间歇性；spec3-final 曾 3/3 通过）。`pol-holdout-confirm-incomplete` **裁定为夹具/评估请求合同待修，不定责产品**：本次 holdout 结果保留，修正版须版本化并转入回归集，新盲测另建 case。逐条原证据见 `artifacts/requirement-v2-certification-20260924/erratum-20260924.md`；修订版 report-v2.md / report-v2.json / failures-and-flips-v2.md（原版保留不动）。
- E2E：进程内 5/5 + producthttp 全包 + 浏览器 harness→API→数据库 + 桌面 1440 Playwright 8/8（键盘/焦点/确认路径）通过；768/375 视口 16/16 为诊断。触控专项/IME/完整无障碍扫描/live 模型浏览器路径未测。
- **认证收口（2026-09-24）：结论 NO-GO——认证完成、当前候选不可发布，不表示产品获准发布**。人工抽查全部完成：归因定案（见上）之外，D 批次 `cv-monitor-refusal` r2 关键字段错写经逐字确认（用户原话仅请求显示器/键鼠，模型输出 `set existing_parts=[]`；金标 cases.json turn0 vs 该批 events.jsonl:573）——属**换模型诊断风险，不构成默认候选的独立 NO-GO 依据**（默认模型批次该 case 与关键字段错写均为 0）。修复须新开/恢复对应产品 change（模型证据标签稳定性、holdout 用途捕获、越权补写关键字段；评估设施 E2 夹具/请求合同三处）后以新候选重新认证；路线图不标 Requirement v2 已交付。最终报告：`artifacts/requirement-v2-certification-20260924/report-v2.md`（erratum 含裁定与收口记录）。
