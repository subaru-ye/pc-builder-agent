# 15 项多轮对话开发场景（2026-10-09）

> 后续已完成真实首跑及撤销元数据金标的版本化修正，见[真实评估与修复交接](conversations15-live-20261009.md)。下文“未调用模型/待实测”记录题库构建阶段，不代表后续状态；当前 r2 对首跑冻结输出重判为 10/15，非新一次采样。

本次已构建 15 项 development 场景，共 44 轮用户输入，每项至少 2 轮。新增 8 项，并将原来只有 1 轮的 `cv-premature-ready`、`cv-monitor-refusal` 补成多轮流程。它们是人工编制形式的确定性用户脚本，由 AI 按当前代码合同起草，新增及扩展金标的人工逐项复核状态为 pending。

数据集：[`requirement-v2-conversations15-20261009`](../../../internal/planningeval/testdata/requirement-v2-conversations15-20261009/conversations/cases.json)。新版本复用原数据集的其他层、目录快照、selftest 和 gates，字节内容保持一致；保留原有 1 项历史 holdout，但不计入 15 项，也不称其为新盲测集。原数据集和历史评估产物未修改，新版本不能继承原 7 项的模型分数。

## 场景与验收目标

| # | ID | 对话示例（按用户轮次） | 独立验收目标 |
|---|---|---|---|
| 1 | cv-fps-progressive | 玩 CS2/无畏契约 → 问 7500 够吗 → 定 7500 → 1080p → 全新买 | 逐步收集、询问不写预算、不重复问已知字段 |
| 2 | cv-premature-ready | 配台打游戏的电脑 → 预算 7500、1080p、全新买 | 首轮不完整；补齐后才可核定 |
| 3 | cv-composite-9000-start | 7500 游戏主机需求齐备 → 改 9000 然后开始 | 同轮保存更正和执行意图，聊天仍不能代替核定 |
| 4 | cv-owned-model-flow | 有旧显卡 → 游戏/2K → 4070 Super → 6000、整机总价 | 型号和金额口径分轮补齐，不能无依据撤销已有件 |
| 5 | cv-monitor-refusal | 要显示器键鼠 → 补齐主机需求 → 外设都先不要 | 能力范围外请求阻止核定，明确撤销后解除阻塞 |
| 6 | cv-office-progressive | 老人上网 → 3000、全新买 | general 用途不强制追问游戏分辨率 |
| 7 | cv-budget-correction | 7500 游戏需求齐备 → 改 6000 | 修改预算，保留其他需求，仍不启动 Builder |
| 8 | cv-budget-question-keeps-state | 定 7500 → 问 9000 够吗 → 定 8500 | 询问保留已记录的预算，明确更正才替换 |
| 9 | cv-use-case-switch | 5000、老人上网、全新买 → 改打游戏 → 1080p | 用途变化重新触发条件必填，其他槽位保留 |
| 10 | cv-owned-model-correction | 已有 4070 Super、游戏/2K → 纠正为 4060 Ti → 6000、整机总价 | 替换同品类型号，不追加重复显卡 |
| 11 | cv-ownership-unknown-keeps-gate | 游戏/1080p/7500 → 旧件待回家确认 → 确认全部新买 | unknown 不默认为空数组，确认后才记录购买范围 |
| 12 | cv-budget-flex-revision | 7500 游戏需求齐备 → 严格不能超预算 → 改上浮 5% | 默认弹性不变成用户事实；明确弹性可逐轮更正 |
| 13 | cv-owned-parts-withdrawal | 已有 4070 Super 且需求齐备 → 不复用、全新买 → 改 8000 | 清理关联型号，后续预算变化不复活旧件 |
| 14 | cv-resolution-withdrawal | 1080p 游戏需求齐备 → 撤销分辨率 → 定 2K | 撤销后恢复不完整状态，再补充只更新对应字段 |
| 15 | cv-game-title-replacement | 玩 CS2 → 补 7500/1080p/全新买 → 只玩 DOTA2 | 替换游戏清单，不追加已经放弃的游戏 |

每轮金标包含明确操作、应保留的状态和缺失字段；新增轮还显式检查核定资格。最终均要求聊天不得启动 Builder。状态断言按字段值比较，不要求回复措辞逐字一致；引用证据须来自对应用户轮。模型层额外操作仍由现有严格判卷检查，不放宽现有门槛。

## 验证与指标边界

- `TestReqV2Conversations15VersionBoundaries`：15 项都是真实多轮结构；继承场景、历史 holdout 和其他冻结资产保持原内容；manifest/split/grader selftest 校验。
- `TestReqV2Conversations15GoldExecutable`：将金标操作及观察直接送入真实 reducer/readiness，逐轮核对状态、缺失项、核定资格和最终断言，15 项通过。此测试不解析自然语言、不使用模型，不作为模型成功率证据。
- `cmd/evalrequirement -mode check`：新版本题库、hash、split、门槛与判卷金丝雀检查通过；grader 为 `reqv2-grader-v4`。
- `go test ./internal/planningeval -count=1` 与 `go vet ./internal/planningeval` 通过；原数据集 `check` 通过。原 manifest SHA256 为 `bb03c00b7ee5724da89658459908a6936a5eb930b27aa3a734b069e5bac7ac59`，新 manifest 为 `9d89a58b8d73a7a9be5f8bd5cf84e91a11c42c0fbd1b2b9b037416ff34a44481`。
- 未调用真实模型。新版本 **Pass³ 待实测**，不登记为 100%。15 项都是开发回归场景，不能替代独立保留集或产品端到端发布验收。

复现离线校验：

```bash
go run ./cmd/evalrequirement -mode check \
  -dataset internal/planningeval/testdata/requirement-v2-conversations15-20261009
go test ./internal/planningeval -run TestReqV2Conversations15 -count=1
```

## 后续真实评估口径

人工复核后，在明确授权的真实评估任务中使用本版本和固定模型配置，禁用模型链及自动供应商切换；每项从独立会话开始完整运行 3 次，保留首次批次的失败及完整冻结报告。Pass³ = 三次均通过的开发场景数 / 15，不选择最好的一次。

15 项 × 3 次 = 45 段完整对话；44 轮用户输入 × 3 = 132 个对话评估轮，不能写成只调用模型 45 次。现有 runner 同时评 development/calibration 的 26 项 extraction，三次重复共增加 78 个模型轮，合计 210 个模型轮，guard 纠偏可能增加实际请求数。正式执行须另行记录调用预算、真实用量、模型身份、代码状态与报告位置。

构建阶段简历可写“设计 15 项多轮对话回归场景，覆盖需求补全、更正、撤销与生成准入”。模型实测完成后才能追加该版本的 Pass³ 数值。
