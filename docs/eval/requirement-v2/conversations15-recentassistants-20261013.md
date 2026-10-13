# conversations15 追问修复复测(2026-10-13)

按 [计划](../真实链路验证与追问复测计划-20261013.md) 执行批次 B:验证"确定性注入最近助手回复消除重复追问"(commit `92b159d`)。**复测未通过:conversations 12/15(基线 15/15),extraction 25/26(基线 26/26)。修复的直接目标 `repeated_question` 全量零命中(无回归),但同一提交的提示词修改伴随三类行为漂移**,需修复迭代。用量 211 次(授权上限 230),未超支。

## 结论与身份

| 身份 | 值 |
|---|---|
| 产物 | `artifacts/reqv2/conversations15-live-recentassistants-20261013/` |
| 题库 | `requirement-v2-conversations15-r2-20261009`(与 fix2 基线同题库,金标零修改) |
| 判卷 | `reqv2-grader-v5`(与基线同) |
| 代码 | `4981496`(相对基线 `c83567`+dirty,对 screening 层的唯一改动是 `92b159d`:指令行修改 + RecentAssistants 注入,同提交不可拆分;planning 无进展/常量/runstats 不影响 screening 层) |
| 模型 | deepseek-v4-flash-0731,bailian,reasoning=none,max_retries=0(进程级),无链 |
| 用量 | **211 次**(上限 230);965,253 tokens 全量已知;latency p50=4851ms,p95=8564ms |
| 数据库 | 独立 `peval_cv15ra_1791855019`,批次后已删除;产品库未动 |

## 分层结果(对照 fix2 基线)

| 层 | 本批 | 基线 |
|---|---|---|
| reducer | 11/11 | 11/11 |
| readiness | 12/12 | 12/12 |
| policy | 8/8 | 8/8 |
| ui-contract | 3/3 | 3/3 |
| extraction | **25/26** | 26/26 |
| conversations | **12/15** | 15/15 |

`repeated_question` 断言全部 pass(含失败 case 内的该断言)——重复追问修复的直接目标达成且无反向回归;失败全部落在其他断言。

## 失败明细与归因(4 例)

1. **cv-owned-parts-withdrawal r3**:turn1 漏记 `owned_parts`("我有一张4070 Super显卡"未提交型号数组)→ missing `owned_parts.gpu.model` 与 confirmation_eligible=false 连锁失败;turn2 撤销与 turn3 改预算操作完全正确。首轮无历史回复,注入列表为空,输入差异只剩指令行文本。
2. **cv-ownership-unknown-keeps-gate r1/r3**:模型输出 notes 观察 reason 为"撤销之前保留的待确认观察"——主动"结案"了金标要求保留的 existing_parts 待确认观察,gate 语义失效。与"模型看到自己此前问过什么后倾向收口"的行为方向一致。
3. **cv-composite-9000-start r1**:模型照抄 system_default 的 budget_flex 被守卫拦截并留降级观察;金标不期望该观察(本轮无弹性表达应不输出 budget_flex)。
4. **ex-restore-temporary-budget r2/r3**:"还是按原来的7500来"被输出为 `set budget_cny=7500`,金标期望 `restore`;r1 通过,跨 repeat 不一致。

**归因边界**:批次间对 screening 的唯一改动是 `92b159d`(指令修改+注入,不可拆分);单批次 3 重复下无法进一步区分"提示词修改引起的行为漂移"与"模型波动"的占比(3/4 例跨 repeat 不一致表明波动存在)。不宣称单因素结论。

## 判定与后续

- 批次 B 判据(15/15 全 3/3)**未通过**;`92b159d` 不具备合入生产提示词的复测证据。
- 失败 4 例按失败案例工作区流程归因沉淀;修复方向(拟议,须人工复核+最小 diff+复测):
  1. 首轮/无历史时保持指令与基线逐字一致(把"避免重复询问"的约束随列表存在才附加,或迁入确定性参考块);
  2. 观察结案约束:待确认观察的撤销必须有用户原话证据,提示词或守卫侧收紧;
  3. `owned_parts` 明确型号的首轮抽取强化。
- 预算纪律:211/230,未超支;未运行 holdout,未 commit/push。
