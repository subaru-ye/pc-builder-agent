# Screening 语义负向审计

这是独立增加的语义回归，不替换历史19题评分，不把新增断言后的成绩回填旧报告。来源为 `4033d15` 的32条真实回答，原始结果仍为16/19。

| 原题 | 新增断言 | 保存回答实际行为 |
| --- | --- | --- |
| L5-301 | 未说明费用口径时budget_basis保持unknown；未说明已有件时不记录已有件；预算与分辨率修改、刷新后仍成立 | 首轮自行写new_purchase，此后一直保留 |
| L5-303 | “看中一张报价”不等于已拥有，第一轮owned_parts和existing_parts均unknown；未说明费用口径时仍unknown | 把“售价3000元的显卡”当成准确型号并写入已有件，服务端联动品类 |

L5-303后续明确否认购买时，撤销或清空已有件均可能合理，因此不新增固定的第二轮owned_parts状态断言，原第二轮断言保留。初始虚构归属的错误不能因为随后被用户纠正就算通过。

## 验证与复现

生成器只添加字段断言，保留原消息、步骤、全部旧断言和保存回答。原始输入、已冻结回答、目录、现有期望发生漂移时拒绝生成；不允许新审计覆盖旧断言。输出分别为人工操作oracle、保存回答recorded、无答案live三套，live套件生成不会调用模型。

```bash
python scripts/eval/planning/freeze_live_replay.py --source artifacts/planning-screening-semantics-live-20260915-r1 --out artifacts/<新的回答冻结目录>
python scripts/eval/planning/build_screening_semantic_audit.py --recorded-suite artifacts/<新的回答冻结目录>/suite.json --out artifacts/<新的审计套件目录>
python -m unittest discover -s scripts/eval/planning -p 'test_*.py' -v
python scripts/eval/planning/run.py --suite artifacts/<新的审计套件目录>/recorded/suite.json --out artifacts/<新的保存回答回放目录>
python scripts/eval/planning/run.py --suite artifacts/<新的审计套件目录>/oracle/suite.json --out artifacts/<新的人工操作验证目录>
```

本次输出 `artifacts/planning-screening-semantic-audit-20260915/`：

- 保存回答通过真实产品入口、独立数据库及刷新回放为 **0/2**，准确报告上述错误；目录 `artifacts/planning-screening-semantic-audit-recorded-20260915-r1/`。退出码1是断言检出的业务失败，不是运行故障。
- 人工正确操作回放 **2/2**，目录 `artifacts/planning-screening-semantic-audit-oracle-20260915-r1/`；只证明断言与合法产品行为兼容，不代表模型会给出正确操作。
- 评估脚本18项测试通过，其中新增3项检验追加断言、保留输入/回答、live无oracle及漂移拒绝。两次产品回放各5次Screening协议调用，实际模型、Builder、网页、Embedding均0。

## 根因边界

准确型号检查只能验证一个字符串出现在原文中，不能证明那是商品型号或用户已经拥有；“售价3000元的显卡”逐字来自用户消息，因而通过现行字符串检查。这是语义理解缺陷，不能用新型号白名单或“包含数字就算型号”等格式规则修补。

另一个未解决问题来自 `62a6654` 的针对性真实批次L5-305：模型原始operations已遗漏budget_basis，并非服务端丢弃；同一回答的reply却声称记录了新增预算。回复正确提及一个事实，不代表服务端状态已记录它。不能根据回复关键词自动补需求，也不能把这种遗漏归因于数据库。输出顺序或提取协议的调整仍需独立验证，当前尚无证据证明它们能消除此类错误。

本审计不改产品代码、模型参数、历史会话或正式配置，也没有重新启动共享服务。下一真实语义评估应保留这套独立负向断言，并与原历史子集分别报告。
