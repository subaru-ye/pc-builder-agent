# conversations15 追问修复迭 代复测(2026-10-13)

承接 [追问修复复测](conversations15-recentassistants-20261013.md) 的四例失败归因,做同层共享修复后两轮全量真实复测。**最终批次 conversations 15/15 全 3/3、确定性层全绿、vetoes=0、repeated_question 零命中**——追问修复 + 漂移修复共同收敛。extraction 25/26,剩余 1 例为单 repeat 波动(见下)。两轮批次各 211 次(授权上限各 230),未超支。

## 结论与身份

| 身份 | 值 |
|---|---|
| 最终产物 | `artifacts/reqv2/conversations15-live-recentassistants-fix2-20261013/` |
| 中间批次(如实保留) | `artifacts/reqv2/conversations15-live-recentassistants-fix-20261013/`(conversations 15/15、extraction 24/26,暴露修复 A 的缺口) |
| 题库/判卷 | 与基线同(r2 题库金标零修改、reqv2-grader-v5) |
| 模型 | deepseek-v4-flash-0731,bailian,reasoning=none,max_retries=0(进程级),无链 |
| 用量 | 211+211=**422 次**(各批上限 230);最终批 961,133 tokens 全量已知,p50=3770ms,p95=5537ms |
| 数据库 | peval_cv15rb_*(两批),批次后均已删除;产品库未动 |

## 修复内容(共享层,无逐题特判)

针对上批四例失败的最小 diff,全部在守卫与提示词同层:

1. **指令恢复基线 + 约束挪位**(针对首轮行为漂移):system 指令行恢复与基线逐字一致;"不得重复询问已问过的问题"的约束改为**数据块尾注,仅当最近助手回复列表存在时出现**。首轮(无历史)输入与基线完全一致,排除指令文本本身引起的抽取漂移。
2. **默认值照抄静默丢弃**(针对 cv-composite):守卫拦截数值系统默认照抄后不再留观察——该值已由确定性默认生效,降级观察无新信息(与 fix2 零弹性拦截同逻辑,金标口径"能进 operations 的不进 observations")。
3. **已结构化原文的复述观察丢弃**(针对 cv-ownership):模型观察的 quote 与本轮**实际生效**的 stated 操作 quote 互为包含时丢弃(常见形态:reason 声明"撤销之前的观察"的元操作)。被拦截/推断的操作未成为真值,不参与判定,其降级观察保留。
4. **空已有件拦截按语境区分**(中间批次暴露,针对 ex-composite):原文处于已有件讨论语境(旧/新买/复用等)时保留观察(cv-ownership 金标依赖);原文与已有件无关("预算9000，开始配吧")时静默丢弃。
5. **owned_parts 首轮例句**(针对 cv-withdrawal r3):既有规则处补显式例——首轮同句说出品类与准确型号时必须同轮提交,不等追问。

测试同步:默认拷贝与空已有件两处守卫断言按金标口径更新(拦截行为不变,观察去向从保留改为静默);新增结构化观察丢弃与渲染基线一致性两组守卫单测;`TestScreeningOpenLanguageAndPartialUncertainty` 的观察数断言 3→2(同 quote 复述观察按现行合同丢弃)。

## 批次结果

| 层 | 上批(修复前) | 中间批 | 最终批 | fix2 基线 |
|---|---|---|---|---|
| conversations | 12/15 | **15/15** | **15/15** | 15/15 |
| extraction | 25/26 | 24/26 | 25/26 | 26/26 |
| 确定性四层 | 全绿 | 全绿 | 全绿 | 全绿 |

- 中间批暴露修复盲点(失败 1→修复 4);最终批 conversations 维持 15/15,上批四例失败全部消失。
- 最终批唯一失败 `ex-budget-conflict-family` r3:多人不一致预算被写成 notes 观察而非 conflict 操作(金标期望零观察+conflict);r1/r2 通过,与前两批各出现一次的单 repeat 翻转同类,判为模型非确定性余量,不做针对性修复;conversations 层的 conflict 合同类金标未受影响。
- repeated_question 全量零命中;未运行 holdout;开发集结果,不代表独立保留集认证。

## 后续

- 修复代码与提示词随本报告交付,离线回归全绿;按仓库纪律提交由用户决定。
- extraction 的单 repeat 波动(累计三轮各 1 例、case 不重复)作为模型余量记录;若未来批次连续失败再立项。
