# 后续修复 Prompt

```text
请在 C:\code\pc-builder-agent 继续修复 Screening 多轮需求收集，遵守仓库 AGENTS.md。先读 docs/eval/requirement-v2/conversations15-live-20261009.md 的完整证据与归因，不从零重建题库。

当前有15项开发场景，每项3次独立对话。原始真实运行 artifacts/reqv2/conversations15-live-devcal-r3-20261009-110834/ 实际调用210次，Pass³=8/15；撤销金标误加evidence条件已另建r2，仅零模型重判同批冻结输出，产物 artifacts/reqv2/conversations15-gold-r2-replay-20261009/，Pass³=10/15（66.7%）。使用 internal/planningeval/testdata/requirement-v2-conversations15-r2-20261009/ 为当前回归题库；两份历史报告都不能覆盖。尚有人工金标复核待办，开发集不代表盲测或发布认证。

优先处理已确认的问题：① cv-ownership-unknown-keeps-gate r1/r3把“先别当作全部新买”写为existing_parts=[]并过早允许核定，检查existingPartsClearedEvidence的否定/待确认边界，同时保留明确全新购买正例；② cv-premature-ready r2凭普通预算7500擅自写budget_flex=0，普通金额不能充当零弹性证据；③ cv-budget-flex-revision r2/r3将5%输出为数值5而非0.05，schema拒收正确，修复输出与有证据的单位规范化，不要任意对数值除100；④ 单轮ex-existing-add-ssd r3因型号未知漏记已有ssd，品类和型号须独立收集。

先裁定两项合同/金标差异：cv-budget-question-keeps-state r3只多出观察，预算保持正确；cv-owned-parts-withdrawal r1/r2旧型号已正确清除，但状态是removed而金标只接受active []。依据领域合同决定标准表达；确属金标问题才另建版本并记依据，不能为提分删断言。同时审查“预算弹性”被关键词误算重复问budget_cny，以及关键误写/veto计数盲区；veto=0不等于没有需求污染。修改评分器须版本化并保留正反例。

先读相关调用方、已有测试和冻结输出，在共享根因层做最小修复，不加逐题特判、无关抽象或新依赖，不修改无关Web/Builder代码。用离线边界测试和冻结响应复现失败，修复后运行相关测试与vet，再冻结版本进行真实复测。

允许真实模型测试，总调用预算420次，固定原始plan的deepseek-v4-flash-0731及供应商选项，零自动重试、禁用模型链，不静默换模型。使用独立localhost peval_评估库、新产物目录，全量15项各3次独立会话，同时跑原单轮及确定性回归，保留每次失败和实际调用量。若需要修订金标/评分器，先完成有依据的版本化裁定，再冻结相同版本做前后比较。不要挑最好一次、拼接不同批次、反复重跑到100%、改旧报告或降低门槛。目标15/15三次均通过；未达到就如实报告剩余失败，不宣称完成或发布通过。

保留工作区已有改动；debug.log不读、不纳入提交。未经我明确要求不要commit/push。最终交付根因、改动位置、离线验证、真实Pass³、调用用量、产物路径和剩余限制。现在直接开展修复，无需再次询问已授权的常规步骤。
```

