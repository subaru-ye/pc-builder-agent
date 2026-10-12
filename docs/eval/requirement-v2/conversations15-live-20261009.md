# 15 项多轮真实评估与修复交接（2026-10-09）

可复制的任务入口：[后续修复 Prompt](conversations15-repair-prompt-20261009.md)。

## 结论与身份

首轮真实调用 210 次，15 项多轮开发场景各独立重复 3 次，共 45 段对话、132 个多轮模型轮；另含 26 项单轮抽取的 78 个模型轮。所有模型请求成功。原始 Pass³ 为 8/15（53.3%）；纠正两项撤销金标的可选元数据要求后，对同一批冻结输出做零模型重判，Pass³ 为 **10/15（66.7%）**。两份报告均保留，后者不是新一次 live 运行。三项冻结门槛仍失败，不能宣称发布验收通过或简历 Pass³=100%。

| 身份 | 值 |
|---|---|
| 原始产物 | `artifacts/reqv2/conversations15-live-devcal-r3-20261009-110834/` |
| r2 重判产物 | `artifacts/reqv2/conversations15-gold-r2-replay-20261009/` |
| 当前回归题库 | `internal/planningeval/testdata/requirement-v2-conversations15-r2-20261009/` |
| 模型 | `deepseek-v4-flash-0731`，bailian，reasoning_effort=none，session_cache=true，无链，零自动重试 |
| 模型服务 host | `ws-5z8rvj9oxtusr5m0.cn-beijing.maas.aliyuncs.com`；其他实际配置见原始 `plan.json`，不记录凭据 |
| 调用预算与真实用量 | 上限 420；实际 210；tokens 891,322，全量已知 |
| 延迟与运行时间 | 模型轮 p50=3820ms、p95=6861ms；批次总时间 961,059ms |
| 代码身份 | `c83556760211995b8814ce637cd22518099fa8d7`；`code_dirty=true` 如实记录未提交题库/文档/测试 |
| 二进制 SHA256 | `a0739a8244c57f0242b7a751360c6ba822a9fce40a44fc5db10a763d4fcaf088` |
| 原 manifest SHA256 | `9d89a58b8d73a7a9be5f8bd5cf84e91a11c42c0fbd1b2b9b037416ff34a44481` |
| r2 manifest SHA256 | `5cdab0298de8e03cc5008df146e373a4579b9f35233ade16ae62b3f61a9f3a8e` |
| 原 report SHA256 | `1c004f117f1f3bcd1a51371ab8e711f9f9504cc242e0f3ebfcd78143f60bce85` |
| r2 report SHA256 | `f0cc76fc4ed747279b5846071f71ae24f1d76035f09e4b0e2dcb16f5eb666fd7` |

原始运行采用独立 `peval_cv15_1791515314322300400` 数据库，结束后已删除该临时库；产品数据库及数据卷未删除。现有 PostgreSQL 容器已启动，宿主端口 15432。Builder 使用 scripted-error oracle，只检测准入，不测 Builder 输出质量。未运行 holdout，也未提交或推送。

## 逐项结果

“通过次数”是完整对话通过的重复数；只有 3/3 才计入 Pass³。

| 场景 ID | 原始 | r2 重判 | 归因 |
|---|---|---|---|
| cv-fps-progressive | 3/3 | 3/3 | 通过 |
| cv-premature-ready | 2/3 | 2/3 | r2 用户只说“预算7500”却额外写 budget_flex=0，实际需求污染 |
| cv-composite-9000-start | 3/3 | 3/3 | 通过 |
| cv-owned-model-flow | 3/3 | 3/3 | 通过 |
| cv-monitor-refusal | 0/3 | 3/3 | 金标错误已修正；能力撤销与核定资格原本正确 |
| cv-office-progressive | 3/3 | 3/3 | 通过 |
| cv-budget-correction | 3/3 | 3/3 | 通过 |
| cv-budget-question-keeps-state | 2/3 | 2/3 | r3 只有额外 observations；预算仍 7500，最后正确改 8500；合同/金标严格度待裁定 |
| cv-use-case-switch | 3/3 | 3/3 | 通过 |
| cv-owned-model-correction | 3/3 | 3/3 | 通过 |
| cv-ownership-unknown-keeps-gate | 0/3 | 0/3 | r1/r3 否定句被写成 existing_parts=[]，过早可核定；r2 状态正确但重复记录两条观察 |
| cv-budget-flex-revision | 1/3 | 1/3 | r2/r3 原始模型将 5% 写为 5，schema 拒收并转 conflict；实际更正未完成 |
| cv-owned-parts-withdrawal | 1/3 | 1/3 | r1/r2 清除了旧型号但 owned_parts=removed，与金标 active [] 不同；合同/标准表达待裁定 |
| cv-resolution-withdrawal | 0/3 | 3/3 | 金标错误已修正；撤销、缺失项与重新补全原本正确 |
| cv-game-title-replacement | 3/3 | 3/3 | 通过 |

单轮抽取为 25/26，`ex-existing-add-ssd` r3 漏记已有 ssd，错误地因型号未明而保留为 notes；品类可以先记、型号继续追问，不能把“准确型号未知”当作“已有关系未知”。确定性层 reducer 11/11、readiness 12/12、policy 8/8、ui-contract 3/3，均按三次重复折叠，全层 veto=0。

原始报告的三项失败：conversations.task_success=8/15、repeated_question_max_per_case=1、key_field_wrong_write_total=7。r2 后分别为 10/15、1、4。后两项存在需要逐条审查的计数：预算弹性追问命中了“预算”关键词；旧型号明确撤销的合法额外操作被计入关键字段错写。**veto=0 不代表没有误写**，否定句导致的 active 空数组与过早核定已有冻结证据。

## 已确认的金标勘误

起草时将 `evidence=stated` 用于所有操作，导致 `remove unsupported.monitor/keyboard/mouse` 和 `remove use_case.resolution` 被错误要求携带该可选标记。领域撤销合同要求 `quote` 绑定本轮用户原话，`evidence` 并非撤销必需的分类元数据；程序真实输出有完整 quote，状态及核定资格符合原金标。

r2 **仅删除这四条 remove 金标的 evidence 条件**，仍要求相同操作、撤销对象与用户引用。用户脚本、状态、其他断言、其他层、split、grader、gates 均不变；两份数据集和原始运行保留。重判仅六个重复由 false 变 true，没有重采样。`TestReqV2Conversations15R2OnlyCorrectsRemovalMetadata` 锁定唯一差异，并验证无用户引用的 remove 仍无法匹配金标。新增/扩展金标人工复核状态仍为 pending。

## 后续优先修复

1. **优先：已有件证据的否定/未知边界。** 原话“旧配件能不能复用还得回家确认，先别当作全部新买”在 r1/r3 输出并落库空数组。`existingPartsClearedEvidence` 只检查“全部新买”等正向子串，忽略否定/待确认语境；prompt 与守卫共同检查，不能靠换说法或给此用例特判。保留明确“全部新买/没有旧件”的正例。
2. **预算弹性来源与百分比。** `cv-premature-ready` r2 凭“预算7500”写了零弹性，应保留 unknown/系统默认，不能把普通金额当严格上限。`cv-budget-flex-revision` r2/r3 模型原始 `value=5`（不是 0.05）；schema 拒收正确，问题在模型输出的单位及有证据的规范化路径，禁止任意对所有数值除 100。
3. **已有件品类与型号分开收集。** `ex-existing-add-ssd` r3 应追加 ssd 品类，再追问型号，保持已有 gpu。检查相关状态视图和模型指导，不能替用户猜型号。
4. **先裁定再处理两项语义差异。** `cv-budget-question-keeps-state` r3 的额外观察未改变预算；`cv-owned-parts-withdrawal` r1/r2 的明确 remove 已清除型号且后续没有复活。依据产品合同判断额外观察/状态表达是否应接受；若确属金标问题另建版本并记依据，不能直接改旧金标或用整体降门槛提分。
5. **计数盲区单独审查。** 预算弹性询问不应自动算作重复问 budget_cny；关键误写和 veto 判定也不能因有 quote 就忽略否定含义。修改判卷须版本化，保留正反例与旧报告；不能另造一套手工“通过率”。

相关入口：`internal/agents/pipeline/screening_requirement_state.go`、`screening_turn_v2.go`，`internal/schemas/requirement_state.go`、`requirement_owned_state.go`、`requirement_readiness.go`，`internal/planningeval/requirement_v2_grade.go`、`requirement_v2_run.go`。先读真实调用方和已有测试，修复共享根因，不修改无关 UI、Builder 或历史失败目录。

## 验证与恢复

本轮 `check`、新增金标及版本边界测试通过，真实批次无跳过；r2 重判为零模型。新版本测试覆盖所有撤销引用及仅四处元数据更正。正式复测前先用合成边界和冻结响应复现实际失败，再跑相关包检查。

后续真实评估使用固定模型、相同题库/门槛/评分器，独立新目录、全量 15 项 × 3 次；同时保留单轮抽取与确定性回归。所有结果和实际调用量如实记录。修改金标或评分器时先完成有依据的版本修订，再冻结评估身份；来源报告重判与新模型采样分开，不从不同版本结果拼接成功率。不得更新 golden、放宽断言、挑重复或持续重跑直到 100%。当前工作区未提交，保留既有 `debug.log`，不读、不提交它；用户未授权本轮 commit/push。
