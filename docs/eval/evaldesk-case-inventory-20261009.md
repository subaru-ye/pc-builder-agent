# 失败案例全量历史核对（2026-10-09）

本清单替代当日先前仅整理目录、显示“已验证修复3”的版本。只核对既有提交、文档和冻结产物，没有新调用模型、修改金标或重写历史分数。

人工索引70项：首批5项、历史合同修复62项、保留集已登记待办3项。按题目组织后，未归档范围共124个入口：待处理4、已验证修复62、历史与模型探索48、证据待补齐10。历史修复62项与首批3个场景重叠，不能相加为65个场景。

## 验证口径

- 同模型严格对照继续核对完整模型配置、干净代码、冻结身份、运行模式与全部适用重复。
- 历史合同验收使用 `spec6-cert-baseline-v1-regrade-v4-20260924` 的显式旧观测重判与 `screening-fix2-live-devcal-r3-20260924` 的干净最终候选；逐题核对同一完整 manifest/gates/grader、冻结题目及全部3次适用重复，并核对重判来源报告完整 SHA-256。
- 历史核对同时关联已存在的实现提交，不从“候选通过”自动生成根因；各提交均已核对为最终候选提交的祖先。运行模式/重复次数/模型身份不一致继续显示，不报告配对改善或单因素因果。
- 所有关联的旧断言/不同版本并列保留，不断言同题的不同表现具有同一根因；后续 Kimi/GLM 等换模型探索独立保留。
- 已暴露保留集的3项失败没有开发/校准验收替代，认证 NO-GO 不变；也不宣称今日代码已复测。

## 已验证场景与实现来源

| 层 | 题号 | 场景 | 实现提交 | 验收 |
|---|---|---|---|---|
| conversations | cv-budget-correction | 确认前更正预算 | `a5b2e345` | 历史合同验收：3/3 |
| conversations | cv-composite-9000-start | 复合意图后仍需核定 | `a5b2e345` | 历史合同验收：3/3 |
| conversations | cv-fps-progressive | 渐进式 FPS 装机对话 | `a5b2e345` | 历史合同验收：3/3 |
| conversations | cv-monitor-refusal | 显示器键鼠请求不承诺 | `a5b2e345` | 历史合同验收：3/3 |
| conversations | cv-office-progressive | 家用办公渐进对话 | `a5b2e345` | 历史合同验收：3/3 |
| conversations | cv-owned-model-flow | 已有件型号补全流程 | `a5b2e345` | 历史合同验收：3/3 |
| conversations | cv-premature-ready | 首句模糊需求不得过早 ready | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-all-new-purchase | “全部新买”是明确空已有件 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-appearance-white | 外观偏好 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-bare-accept-needs-protocol | 裸“可以”需要可验证提案协议 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-budget-accept-explicit | “就按7500来”是明确表达 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-budget-ceiling | “不要超过”设置预算与零弹性 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-budget-conflict-family | 两个出资方预算无法取舍记 conflict | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-budget-correction-same-turn | 同轮自我更正取后值 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-budget-flex-explicit | 明确上浮比例 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-budget-question-7500 | “7500够吗”不激活预算 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-budget-raise | 上调预算 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-composite-budget-start | 复合意图：预算+开始 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-existing-add-ssd | 追加已有固态 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-first-fps-vague | 首句模糊 FPS 需求只记可验证事实 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-fps-target-numeric | 明确帧数记 fps_target | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-monitor-request | 显示器请求识别为不支持能力 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-multi-field-edit | 同轮多字段更新 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-noise-normal | 噪音正常就行 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-office-general | 家用办公用途 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-owned-correction | 更正已有件型号 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-owned-gpu-vague | “有张显卡”记录品类不猜型号 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-priority-gpu | 预算紧张优先显卡 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-quiet-only | 只表达静音偏好 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-recipient-gaming | 装机对象与用途同轮 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-restore-temporary-budget | 恢复临时覆盖的预算 | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-two-game-titles | 两个游戏名记 titles | `a5b2e345` | 历史合同验收：3/3 |
| extraction | ex-web-video-general | 上网看视频是 general | `a5b2e345` | 历史合同验收：3/3 |
| policy | pol-edit-after-confirm-rebuild | 确认后修改需重新核定 | `1fc71f38` | 历史合同验收：3/3 |
| policy | pol-edit-while-running | 运行中草稿编辑仍可保存 | `1fc71f38` | 历史合同验收：3/3 |
| policy | pol-incomplete-chat-start | 信息不足时聊天要求开始不得启动 Builder | `1fc71f38` | 历史合同验收：3/3 |
| policy | pol-monitor-promise-guard | scope 外品类不得被承诺 | `a5b2e345` | 历史合同验收：3/3 |
| policy | pol-ready-chat-start | 聊天“开始吧”只打开核定不启动 | `1fc71f38` | 历史合同验收：3/3 |
| policy | pol-ready-confirm | 核定面板确认可启动 | `1fc71f38` | 历史合同验收：3/3 |
| policy | pol-second-builder-running | 运行中第二个确认被拒 | `1fc71f38` | 历史合同验收：3/3 |
| readiness | rdy-budget-conflict-blocks | 预算冲突单列阻塞 | `6ee7274e` | 历史合同验收：3/3 |
| readiness | rdy-existing-unknown-blocks | 已有件 unknown 不能默认为空 | `6ee7274e` | 历史合同验收：3/3 |
| readiness | rdy-fresh | 空会话缺三必填 | `6ee7274e` | 历史合同验收：3/3 |
| readiness | rdy-gaming-any-resolution-not-satisfied | gaming 的 any 不满足最低矩阵 | `6ee7274e` | 历史合同验收：3/3 |
| readiness | rdy-gaming-missing-three | 只知用途缺预算/已有件/分辨率 | `6ee7274e` | 历史合同验收：3/3 |
| readiness | rdy-gaming-ready-defaults | gaming 齐备展开系统默认 | `6ee7274e` | 历史合同验收：3/3 |
| readiness | rdy-must-brand-conflict-blocks | must 约束冲突阻塞 | `6ee7274e` | 历史合同验收：3/3 |
| readiness | rdy-owned-missing-model | 已有显卡缺型号与口径 | `6ee7274e` | 历史合同验收：3/3 |
| readiness | rdy-productivity-no-titles | 生产力缺主要软件或任务 | `6ee7274e` | 历史合同验收：3/3 |
| readiness | rdy-productivity-ready | 生产力齐备 | `6ee7274e` | 历史合同验收：3/3 |
| readiness | rdy-soft-conflict-not-blocking | 普通软偏好冲突不阻塞 | `6ee7274e` | 历史合同验收：3/3 |
| reducer | rd-alternative-keeps-active | 备选不改变当前值 | `6ee7274e` | 历史合同验收：3/3 |
| reducer | rd-existing-empty-explicit | 全部新买是明确 active 空数组 | `6ee7274e` | 历史合同验收：3/3 |
| reducer | rd-owned-correction | 更正已有件型号覆盖旧值 | `6ee7274e` | 历史合同验收：3/3 |
| reducer | rd-remove-budget | 明确撤销预算留墓碑 | `6ee7274e` | 历史合同验收：3/3 |
| reducer | rd-set-budget-7500 | 聊天来源设置预算并绑定本轮原文 | `6ee7274e` | 历史合同验收：3/3 |
| reducer | rd-temporary-restore | 临时覆盖后恢复原值 | `6ee7274e` | 历史合同验收：3/3 |
| reducer | rd-v2-any-resolution | 明确不限是 active 用户值 | `6ee7274e` | 历史合同验收：3/3 |
| reducer | rd-v2-performance-goal | v2 字段 use_case.performance_goal | `6ee7274e` | 历史合同验收：3/3 |
| ui-contract | ui-collecting-fresh | 新会话可见收集态与缺失项 | `6537156d` | 历史合同验收：3/3 |
| ui-contract | ui-incomplete-shown-ready | 不完整状态不得显示可确认 | `6537156d` | 历史合同验收：3/3 |
| ui-contract | ui-ready-unconfirmed | 齐备未确认的可视状态与确认 payload | `6537156d` | 历史合同验收：3/3 |

## 仍待处理

1. `empty-existing-parts`：守卫已调整，但基线Qwen、候选Flash，不具备同模型修复验证。
2. `ex-holdout-budget-approx`：认证登记的用途捕获失败；尚无后续独立保留集验收。
3. `cv-holdout-productivity`：认证登记的多轮用途捕获失败；尚无后续独立保留集验收。
4. `pol-holdout-confirm-incomplete`：人工已裁定为评估夹具/请求合同问题，不定责产品；版本化转回归集和新盲测尚待完成。

另有10个未归档运行级指标缺后续可比验收，不是10个独立未修复题目。

## 全部目录

包括归档共129个目录、664条索引记录；人工关联收起已审阅的自动记录，具体断言、重复和原报告仍可在详情核对。每条索引记录归类一次。

| 分类 | 层 | 题号/指标 | 场景 | 唯一结论 | 成员 | 运行 | 版本 |
|---|---|---|---|---|---:|---:|---:|
| 历史与模型探索 | extraction | ex-owned-gpu-vague | “有张显卡”记录品类不猜型号 | 验收参考已通过 | 9 | 20 | 8 |
| 已验证修复 | conversations | cv-owned-model-flow | 提交已有显卡型号时无依据撤销已有件 | 修复验证已确认 | 3 | 23 | 8 |
| 历史与模型探索 | model | latency_p95_ms | latency_p95_ms | 验收参考已通过 | 1 | 1 | 1 |
| 历史与模型探索 | conversations | task_success | task_success | 验收参考已通过 | 22 | 22 | 7 |
| 历史与模型探索 | conversations | cv-monitor-refusal | 显示器键鼠请求不承诺 | 验收参考已通过 | 11 | 21 | 8 |
| 历史与模型探索 | conversations | cv-budget-correction | 确认前更正预算 | 验收参考已通过 | 8 | 19 | 7 |
| 历史与模型探索 | conversations | repeated_question_max_per_case | repeated_question_max_per_case | 验收参考已通过 | 1 | 1 | 1 |
| 历史与模型探索 | extraction | ex-budget-conflict-family | 两个出资方预算无法取舍记 conflict | 验收参考已通过 | 8 | 20 | 7 |
| 历史与模型探索 | extraction+conversations | key_field_wrong_write_total | key_field_wrong_write_total | 验收参考已通过 | 20 | 20 | 7 |
| 历史与模型探索 | extraction | min_cases | min_cases | 该批次未评估此项 | 20 | 20 | 7 |
| 历史与模型探索 | conversations | min_cases | min_cases | 该批次未评估此项 | 20 | 20 | 7 |
| 历史与模型探索 | conversations | cv-fps-progressive | 渐进式 FPS 装机对话 | 验收参考已通过 | 14 | 20 | 8 |
| 历史与模型探索 | extraction | ex-monitor-request | 显示器请求识别为不支持能力 | 验收参考已通过 | 9 | 20 | 8 |
| 历史与模型探索 | extraction | case_success | case_success | 验收参考已通过 | 19 | 19 | 7 |
| 历史与模型探索 | extraction | ex-fps-target-numeric | 明确帧数记 fps_target | 验收参考已通过 | 8 | 19 | 7 |
| 待处理 | conversations | cv-monitor-refusal | 泛购买请求被解释为没有已有件 | 修复验证待补齐 | 1 | 2 | 1 |
| 待处理 | extraction | ex-holdout-budget-approx | 生产力用途抽取未满足冻结预期 | 根因已定位，待修复验证 | 1 | 1 | 1 |
| 待处理 | policy | pol-holdout-confirm-incomplete | 保留集确认夹具与请求合同不一致 | 根因已定位，待修复验证 | 1 | 1 | 1 |
| 待处理 | conversations | cv-holdout-productivity | 生产力渐进对话未满足冻结预期 | 根因已定位，待修复验证 | 1 | 1 | 1 |
| 证据待补齐 | extraction | operation_recall | operation_recall | 缺少同版本验收证据 | 1 | 1 | 1 |
| 证据待补齐 | conversations | task_success | task_success | 缺少同版本验收证据 | 14 | 14 | 7 |
| 证据待补齐 | extraction | case_success | case_success | 缺少同版本验收证据 | 11 | 11 | 7 |
| 证据待补齐 | extraction+conversations | key_field_wrong_write_total | key_field_wrong_write_total | 缺少同版本验收证据 | 13 | 13 | 7 |
| 历史与模型探索 | extraction | min_signal_turns | min_signal_turns | 该批次未评估此项 | 19 | 19 | 7 |
| 证据待补齐 | extraction | operation_precision | operation_precision | 缺少同版本验收证据 | 7 | 7 | 6 |
| 已验证修复 | conversations | cv-composite-9000-start | 系统弹性默认值被写成用户事实 | 修复验证已确认 | 2 | 12 | 7 |
| 已验证修复 | conversations | cv-fps-progressive | 预算更正误标为接受建议 | 修复验证已确认 | 2 | 14 | 8 |
| 已验证修复 | extraction | ex-all-new-purchase | “全部新买”是明确空已有件 | 历史修复与验收已核对 | 1 | 24 | 7 |
| 历史与模型探索 | extraction | ex-budget-flex-explicit | 明确上浮比例 | 该批次未评估此项 | 7 | 18 | 7 |
| 历史与模型探索 | extraction | ex-budget-raise | 上调预算 | 该批次未评估此项 | 7 | 18 | 7 |
| 历史与模型探索 | model | total_calls_within_budget | total_calls_within_budget | 该批次未评估此项 | 17 | 17 | 6 |
| 历史与模型探索 | conversations | cv-office-progressive | 家用办公渐进对话 | 该批次未评估此项 | 7 | 18 | 7 |
| 历史与模型探索 | model | provider_success | provider_success | 该批次未评估此项 | 17 | 17 | 6 |
| 历史与模型探索 | extraction | ex-appearance-white | 外观偏好 | 该批次未评估此项 | 7 | 18 | 7 |
| 历史与模型探索 | extraction | ex-first-fps-vague | 首句模糊 FPS 需求只记可验证事实 | 该批次未评估此项 | 8 | 19 | 8 |
| 历史与模型探索 | extraction | ex-quiet-only | 只表达静音偏好 | 该批次未评估此项 | 8 | 19 | 8 |
| 历史与模型探索 | model | latency_p95 | latency_p95 | 该批次未评估此项 | 17 | 17 | 6 |
| 历史与模型探索 | extraction | ex-recipient-gaming | 装机对象与用途同轮 | 该批次未评估此项 | 7 | 18 | 7 |
| 历史与模型探索 | extraction | ex-bare-accept-needs-protocol | 裸“可以”需要可验证提案协议 | 该批次未评估此项 | 8 | 19 | 8 |
| 历史与模型探索 | extraction | operation_recall | operation_recall | 该批次未评估此项 | 18 | 18 | 7 |
| 历史与模型探索 | conversations | cv-composite-9000-start | 复合意图后仍需核定 | 该批次未评估此项 | 8 | 19 | 8 |
| 历史与模型探索 | conversations | cv-owned-model-flow | 已有件型号补全流程 | 该批次未评估此项 | 8 | 19 | 8 |
| 历史与模型探索 | extraction | ex-office-general | 家用办公用途 | 该批次未评估此项 | 7 | 18 | 7 |
| 历史与模型探索 | extraction | ex-owned-correction | 更正已有件型号 | 该批次未评估此项 | 8 | 19 | 8 |
| 历史与模型探索 | extraction | ex-budget-question-7500 | “7500够吗”不激活预算 | 该批次未评估此项 | 8 | 19 | 8 |
| 历史与模型探索 | extraction | ex-restore-temporary-budget | 恢复临时覆盖的预算 | 该批次未评估此项 | 7 | 18 | 7 |
| 历史与模型探索 | extraction | final_state_exact_match | final_state_exact_match | 该批次未评估此项 | 18 | 18 | 7 |
| 历史与模型探索 | extraction | ex-noise-normal | 噪音正常就行 | 该批次未评估此项 | 7 | 18 | 7 |
| 历史与模型探索 | extraction | ex-priority-gpu | 预算紧张优先显卡 | 该批次未评估此项 | 7 | 18 | 7 |
| 历史与模型探索 | extraction | ex-budget-ceiling | “不要超过”设置预算与零弹性 | 该批次未评估此项 | 8 | 19 | 8 |
| 历史与模型探索 | extraction | ex-web-video-general | 上网看视频是 general | 该批次未评估此项 | 7 | 18 | 7 |
| 历史与模型探索 | extraction | operation_precision | operation_precision | 该批次未评估此项 | 18 | 18 | 7 |
| 历史与模型探索 | model | max_model_calls_per_turn | max_model_calls_per_turn | 该批次未评估此项 | 17 | 17 | 6 |
| 历史与模型探索 | conversations | cv-premature-ready | 首句模糊需求不得过早 ready | 该批次未评估此项 | 8 | 19 | 8 |
| 历史与模型探索 | extraction | ex-existing-add-ssd | 追加已有固态 | 该批次未评估此项 | 7 | 18 | 7 |
| 历史与模型探索 | extraction | ex-two-game-titles | 两个游戏名记 titles | 该批次未评估此项 | 7 | 18 | 7 |
| 历史与模型探索 | extraction | ex-budget-correction-same-turn | 同轮自我更正取后值 | 该批次未评估此项 | 7 | 18 | 7 |
| 历史与模型探索 | extraction | ex-composite-budget-start | 复合意图：预算+开始 | 该批次未评估此项 | 8 | 19 | 8 |
| 历史与模型探索 | extraction | ex-budget-accept-explicit | “就按7500来”是明确表达 | 该批次未评估此项 | 8 | 19 | 8 |
| 历史与模型探索 | extraction | ex-all-new-purchase | “全部新买”是明确空已有件 | 该批次未评估此项 | 8 | 19 | 8 |
| 历史与模型探索 | extraction | ex-multi-field-edit | 同轮多字段更新 | 该批次未评估此项 | 8 | 19 | 8 |
| 已验证修复 | extraction | ex-budget-ceiling | “不要超过”设置预算与零弹性 | 历史修复与验收已核对 | 1 | 6 | 5 |
| 已验证修复 | extraction | ex-office-general | 家用办公用途 | 历史修复与验收已核对 | 1 | 5 | 4 |
| 已验证修复 | readiness | rdy-productivity-no-titles | 生产力缺主要软件或任务 | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | conversations | cv-premature-ready | 首句模糊需求不得过早 ready | 历史修复与验收已核对 | 1 | 6 | 5 |
| 已验证修复 | extraction | ex-fps-target-numeric | 明确帧数记 fps_target | 历史修复与验收已核对 | 1 | 6 | 5 |
| 证据待补齐 | extraction | forbidden_op_total | forbidden_op_total | 缺少同版本验收证据 | 7 | 7 | 6 |
| 已验证修复 | extraction | ex-owned-gpu-vague | “有张显卡”记录品类不猜型号 | 历史修复与验收已核对 | 1 | 9 | 7 |
| 已验证修复 | conversations | cv-budget-correction | 确认前更正预算 | 历史修复与验收已核对 | 1 | 7 | 6 |
| 已验证修复 | extraction | ex-quiet-only | 只表达静音偏好 | 历史修复与验收已核对 | 1 | 6 | 5 |
| 已验证修复 | readiness | rdy-gaming-missing-three | 只知用途缺预算/已有件/分辨率 | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | reducer | rd-v2-performance-goal | v2 字段 use_case.performance_goal | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | extraction | ex-first-fps-vague | 首句模糊 FPS 需求只记可验证事实 | 历史修复与验收已核对 | 1 | 8 | 7 |
| 已验证修复 | readiness | rdy-existing-unknown-blocks | 已有件 unknown 不能默认为空 | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | readiness | rdy-fresh | 空会话缺三必填 | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | readiness | rdy-productivity-ready | 生产力齐备 | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | extraction | ex-budget-question-7500 | “7500够吗”不激活预算 | 历史修复与验收已核对 | 1 | 6 | 5 |
| 已验证修复 | extraction | ex-priority-gpu | 预算紧张优先显卡 | 历史修复与验收已核对 | 1 | 9 | 5 |
| 已验证修复 | policy | pol-edit-after-confirm-rebuild | 确认后修改需重新核定 | 历史修复与验收已核对 | 1 | 14 | 7 |
| 证据待补齐 | conversations | repeated_question_max_per_case | repeated_question_max_per_case | 缺少同版本验收证据 | 4 | 4 | 4 |
| 已验证修复 | extraction | ex-owned-correction | 更正已有件型号 | 历史修复与验收已核对 | 1 | 6 | 5 |
| 已验证修复 | extraction | ex-bare-accept-needs-protocol | 裸“可以”需要可验证提案协议 | 历史修复与验收已核对 | 1 | 6 | 5 |
| 已验证修复 | reducer | rd-remove-budget | 明确撤销预算留墓碑 | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | ui-contract | ui-incomplete-shown-ready | 不完整状态不得显示可确认 | 历史修复与验收已核对 | 1 | 37 | 9 |
| 已验证修复 | extraction | ex-noise-normal | 噪音正常就行 | 历史修复与验收已核对 | 1 | 7 | 6 |
| 已验证修复 | extraction | ex-monitor-request | 显示器请求识别为不支持能力 | 历史修复与验收已核对 | 1 | 10 | 8 |
| 已验证修复 | extraction | ex-budget-accept-explicit | “就按7500来”是明确表达 | 历史修复与验收已核对 | 1 | 7 | 6 |
| 证据待补齐 | extraction | final_state_exact_match | final_state_exact_match | 缺少同版本验收证据 | 10 | 10 | 6 |
| 已验证修复 | policy | pol-monitor-promise-guard | scope 外品类不得被承诺 | 历史修复与验收已核对 | 1 | 14 | 7 |
| 已验证修复 | policy | pol-second-builder-running | 运行中第二个确认被拒 | 历史修复与验收已核对 | 1 | 3 | 1 |
| 已验证修复 | policy | pol-incomplete-chat-start | 信息不足时聊天要求开始不得启动 Builder | 历史修复与验收已核对 | 1 | 14 | 7 |
| 已验证修复 | extraction | ex-two-game-titles | 两个游戏名记 titles | 历史修复与验收已核对 | 1 | 7 | 6 |
| 已验证修复 | reducer | rd-set-budget-7500 | 聊天来源设置预算并绑定本轮原文 | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | extraction | ex-budget-correction-same-turn | 同轮自我更正取后值 | 历史修复与验收已核对 | 1 | 6 | 5 |
| 已验证修复 | reducer | rd-existing-empty-explicit | 全部新买是明确 active 空数组 | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | readiness | rdy-must-brand-conflict-blocks | must 约束冲突阻塞 | 历史修复与验收已核对 | 1 | 11 | 6 |
| 证据待补齐 | all | veto_total | veto_total | 缺少同版本验收证据 | 12 | 12 | 6 |
| 已验证修复 | policy | pol-ready-confirm | 核定面板确认可启动 | 历史修复与验收已核对 | 1 | 3 | 1 |
| 证据待补齐 | extraction | turn_signal_exact_match | turn_signal_exact_match | 缺少同版本验收证据 | 4 | 4 | 4 |
| 已验证修复 | extraction | ex-restore-temporary-budget | 恢复临时覆盖的预算 | 历史修复与验收已核对 | 1 | 7 | 6 |
| 已验证修复 | extraction | ex-appearance-white | 外观偏好 | 历史修复与验收已核对 | 1 | 13 | 7 |
| 已验证修复 | extraction | ex-recipient-gaming | 装机对象与用途同轮 | 历史修复与验收已核对 | 1 | 5 | 4 |
| 已验证修复 | policy | pol-edit-while-running | 运行中草稿编辑仍可保存 | 历史修复与验收已核对 | 1 | 37 | 9 |
| 已验证修复 | extraction | ex-composite-budget-start | 复合意图：预算+开始 | 历史修复与验收已核对 | 1 | 7 | 6 |
| 已验证修复 | reducer | rd-alternative-keeps-active | 备选不改变当前值 | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | extraction | ex-budget-raise | 上调预算 | 历史修复与验收已核对 | 1 | 6 | 5 |
| 已验证修复 | extraction | ex-existing-add-ssd | 追加已有固态 | 历史修复与验收已核对 | 1 | 5 | 4 |
| 已验证修复 | ui-contract | ui-ready-unconfirmed | 齐备未确认的可视状态与确认 payload | 历史修复与验收已核对 | 1 | 38 | 10 |
| 已验证修复 | extraction | ex-budget-conflict-family | 两个出资方预算无法取舍记 conflict | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | readiness | rdy-gaming-ready-defaults | gaming 齐备展开系统默认 | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | reducer | rd-temporary-restore | 临时覆盖后恢复原值 | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | readiness | rdy-gaming-any-resolution-not-satisfied | gaming 的 any 不满足最低矩阵 | 历史修复与验收已核对 | 1 | 9 | 5 |
| 已验证修复 | readiness | rdy-owned-missing-model | 已有显卡缺型号与口径 | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | readiness | rdy-budget-conflict-blocks | 预算冲突单列阻塞 | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | policy | pol-ready-chat-start | 聊天“开始吧”只打开核定不启动 | 历史修复与验收已核对 | 1 | 14 | 7 |
| 已验证修复 | readiness | rdy-soft-conflict-not-blocking | 普通软偏好冲突不阻塞 | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | conversations | cv-office-progressive | 家用办公渐进对话 | 历史修复与验收已核对 | 1 | 11 | 7 |
| 已验证修复 | ui-contract | ui-collecting-fresh | 新会话可见收集态与缺失项 | 历史修复与验收已核对 | 1 | 37 | 9 |
| 已验证修复 | extraction | ex-web-video-general | 上网看视频是 general | 历史修复与验收已核对 | 1 | 5 | 4 |
| 已验证修复 | reducer | rd-v2-any-resolution | 明确不限是 active 用户值 | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | reducer | rd-owned-correction | 更正已有件型号覆盖旧值 | 历史修复与验收已核对 | 1 | 11 | 6 |
| 已验证修复 | extraction | ex-multi-field-edit | 同轮多字段更新 | 历史修复与验收已核对 | 1 | 9 | 6 |
| 已验证修复 | extraction | ex-budget-flex-explicit | 明确上浮比例 | 历史修复与验收已核对 | 1 | 5 | 4 |
| 已验证修复 | conversations | cv-monitor-refusal | 显示器键鼠请求不承诺 | 历史修复与验收已核对 | 1 | 10 | 8 |
| 证据待补齐 |  | provider_errors | 上游调用失败 | 缺少同版本验收证据 | 1 | 1 | 1 |
| 历史与模型探索 | extraction | latency_p95 | latency_p95 | 该批次未评估此项 | 1 | 1 | 1 |
| 历史与模型探索 | extraction | max_model_calls_per_turn | max_model_calls_per_turn | 该批次未评估此项 | 1 | 1 | 1 |
| 历史与模型探索 | extraction | total_calls_within_budget | total_calls_within_budget | 该批次未评估此项 | 1 | 1 | 1 |
| 历史与模型探索 | extraction | provider_success | provider_success | 该批次未评估此项 | 1 | 1 | 1 |
