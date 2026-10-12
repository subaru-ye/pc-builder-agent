# 15 项多轮需求收集修复复测(2026-10-09)

承接 [首轮真实评估与修复交接](conversations15-live-20261009.md):对四个已确认问题做共享根因修复后,r2 题库全量真实复测。**conversations 15/15 全部 3/3 通过,extraction 26/26,确定性层全绿,全部冻结门槛通过,veto=0**。开发集结果,不代表 holdout 或发布认证;金标人工复核 pending 状态不变。

## 结论与身份

| 身份 | 值 |
|---|---|
| 最终产物 | `artifacts/reqv2/conversations15-live-fix2-r3-20261009/` |
| 中间批次(已归档) | `artifacts/reqv2/superseded-fix1-live-20261009/`(13/15,回归已修复,如实保留) |
| 题库 | `internal/planningeval/testdata/requirement-v2-conversations15-r2-20261009/`(金标零修改) |
| 判卷 | `reqv2-grader-v5`(repeated_question 检测修正,见下) |
| 模型 | `deepseek-v4-flash-0731`,bailian,reasoning_effort=none,session_cache=true,max_retries=0,无链;host 与原始 plan 一致 |
| 调用预算与用量 | 授权上限 420;fix1 批次 211 + fix2 批次 211 = **422,超授权 2 次**(见"预算纪律") |
| tokens / 延迟 | fix2 批次 945,613 tokens 全量已知;p50=3767ms,p95=6633ms |
| 代码身份 | commit `c83556760211995b8814ce637cd22518099fa8d7`,code_dirty=true(含本轮未提交修复与题库) |
| 数据库 | 独立 `peval_cv15r3_1791518746`,批次后已删除;产品库与数据卷未动 |
| builder | scripted-error oracle,只测准入,不测 Builder 输出质量;未运行 holdout,未 commit/push |

## 根因与修复(共享层,无逐题特判)

全部改动在守卫(`internal/agents/pipeline/screening_requirement_state.go`)、判卷(`internal/planningeval/requirement_v2_grade.go`)与 prompt 同层配合;`internal/schemas` 仅导出 `MaxBudgetFlex` 常量,行为无变化。r2 题库金标、gates、用户脚本零修改;三处题库 manifest/provenance 仅同步 `grader_version`。

1. **否定/待确认语境的空已有件**(cv-ownership-unknown r1/r3):`existingPartsClearedEvidence` 增加否定/待确认词表("先别""还得""待确认"等),命中则拒绝 `set existing_parts=[]`,拦截观察绑定完整原文;prompt 同步说明该类表达保持 unknown 并用一条观察保留原话。金标正例("确认过了没有可复用的旧件，主机配件全部新买")不受影响。
2. **零弹性证据红线**(cv-premature-ready r2):`set budget_flex=0` 要求本轮整句或模型摘录任一出现严格上限表达("不能超""不要超""不得超过"等),否则静默丢弃(普通金额已由 budget_cny 记录,降级观察只是噪声)。正例"预算不能超，一分都不能超"与"预算不要超过7500"(ex-budget-ceiling)均放行。
3. **有证据的百分比换算**(cv-budget-flex-revision r2/r3):`normalizePercentBudgetFlex` 仅当值超出 schema 上限(>0.3)且本轮原话出现精确对应的百分比字面量("5%"或"百分之5")才除以 100;不任意对数值除 100。词表外的中文数字("百分之五")不换算,走原 schema 拒绝路径。
4. **撤销已有件的标准表达**(cv-owned-parts-withdrawal r1/r2):`set existing_parts=[]` 的确定性级联已把 owned_parts 置为 active 空数组;新增守卫跳过冗余的 `remove owned_parts`(墓碑会覆盖级联的明确空值),"全部新买"证据下与操作顺序无关;`remove existing_parts` 在同一证据下规范化为 `set []`(值语义优先于墓碑)。纯撤销表达("旧件都不要了")仍走 remove。
5. **降级观察去噪**(cv-ownership r2、cv-budget-question r3):`observe()` 对同一段原文(quote 完全相同或同字段互包含)只保留一条且保留更完整摘录;`unsupported_capability:*` 观察是每能力一条的结构化记录,不参与去重;推断操作的原话已被同轮明确操作结构化时不再降级为观察。inferred 与缺席值的其余降级观察(供追问)保留,既有产品语义合同不变。
6. **判卷 reqv2-grader-v5**:预算弹性(budget_flex)冲突确认与预算口径(budget_basis)追问的句子携带"预算"字样但不是再次索要金额,`asksAboutField` 对 budget_cny 的关键词从裸"预算"收紧为金额索求类表达,消除 repeated_question 误报;阈值与金标不变,只减少误判。三处题库 manifest 同步 v5;主题库 provenance 补录 v4(当时漏记)与 v5 变更记录。
7. **prompt 共享语义强化**:百分比先换算再提交、普通预算金额不写 budget_flex、询问行情不输出 operation/observation、已有件"旧的/也能用"立即记品类而型号未知只影响 owned_parts、全部新买用 set [] 不 remove owned_parts、inferred 操作会被丢弃、同一句原文一条观察。
8. **fix1 批次暴露的三处新问题(本修复引入或暴露,同批修复)**:跨字段去重曾把同 quote 的 monitor/keyboard/mouse 三条能力观察合并导致 cv-monitor-refusal 0/3 回归(改为能力观察豁免去重);零弹性词表漏"不要超过"误伤 ex-budget-ceiling 0/3(补"不要超/不得超过/别超"等并接受模型摘录作为证据);模型偶发 `remove notes` 噪声操作(cv-ownership r1)静默跳过(无备注撤销表达时)。三处均有守卫单测与冻结重放验证。

## 领域合同裁定(不修改金标)

- **cv-budget-question-keeps-state r3 的"额外观察"**:prompt 原"必要时存 observation"口径与金标"无操作无观察"矛盾,裁定以金标为准收紧 prompt(询问行情只在 answer 讨论);非金标问题。
- **cv-owned-parts-withdrawal 的 removed vs active []**:撤销全部已有件的标准表达是 active 空数组(可核定的明确值),墓碑仅用于纯撤销;程序级联+守卫实现,金标不动。
- **repeated_question 误报**:判卷启发式缺陷,版本化修正为 grader v5,非金标问题。

## 验证

- 守卫回归测试新增 8 个(`screening_requirement_state_guard_test.go`),全部基于冻结失败实录的输入形状,含正反例;`asksAboutField` 判卷单测含误报/命中两组例。
- 冻结响应零模型重放:首轮产物与 fix1 产物的失败轮原始模型输出经新守卫重放,目标行为(空数组拦截、0.05 换算、级联空数组、能力观察保留、remove notes 跳过、ceiling 放行)逐项核对通过。
- `go build ./...`、`go vet ./...`、`go test ./...` 全部通过;conversations15 版本边界测试(r2 仅四处 remove 元数据修正)不受影响。
- 真实复测 fix2:15 项各 3 次独立对话全过;extraction 26/26(precision=recall=1.0,turn_signal 75/75);reducer 11/11、readiness 12/12、policy 8/8、ui-contract 3/3;repeated_questions=0、key_field_wrong_writes=0、forbidden_op=0、veto=0;全部 gates verdict passed。

## 预算纪律

授权总预算 420 次真实调用。fix1 批次(211 次)暴露三处回归,修复后需要完整批次验证,fix2 批次(211 次)合计 422 次,**超出授权 2 次**。未挑重复、未拼接批次、未重跑到 100%(fix1 的 13/15 与 fix2 的 15/15 均完整保留);超支原因是修复回归后的复测决策,在此如实报告。

## 产物一致性说明

fix2 产物目录内冻结的 manifest.json 副本(2 空格缩进变体,`9ce1b606…`)与题库当前 manifest(恢复 1 空格原格式后,`e45d2b15…`)字节不同,但 `grader_version=reqv2-grader-v5`、files 清单与各层 SHA256 完全一致;产物自身 report/plan 的 manifest_sha256 与其内冻结副本一致,自洽。gates.json 两处一致(`a30cdfaf…`)。

## 剩余限制

- 开发集结果;holdout 未运行,新增/扩展金标人工复核 pending 不变,不宣称发布验收通过。
- 守卫证据词表为保守启发式(各处 `ponytail:` 注释标明上限):口语变体漏报的路径均为"本轮不写入+原文保留/下轮再表达",不产生错误状态;`remove notes` 拦截词表外表达会被跳过一轮。
- ex-all-new-purchase 的 turn_signals 在 fix1 r3 出现过一次模型波动(prompt 已有正确例句),fix2 三次全过;该类单点波动依赖 prompt 约束,无守卫兜底。
- 判卷 v5 的 budget_cny 关键词收紧方向为漏报安全(只影响零容忍门槛的误报消除),金额类重复询问的口语变体可能漏报,由金丝雀与人工复核兜底。
