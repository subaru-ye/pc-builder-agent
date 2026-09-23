---
status: proposed
created: 2026-09-22
---

# Change: Screening requirement collection v2

## Dependency

依赖 `requirement-v2-evaluation-foundation` 和 `requirement-state-readiness-v2`。本 change 使用已经冻结的字段、Reducer、Readiness 和最低确认矩阵，不在 prompt 中复制另一套业务规则。

## Outcome

把 Screening 收敛为“本轮语义解析与回答”组件：从用户本轮消息中产生可验证的字段操作、未结构化 observation、多标签 turn signals 和问题回答；确定性 Reducer 更新需求，Readiness/Policy 决定后续允许动作。Screening 不再用单一 `collect/confirm/plan` 标签控制会话或 Builder。

完成后，首轮模糊需求会被正确记录并继续收集；用户提出咨询、修改、核对和开始请求可以在同一句话中并存；助手建议只有在用户明确接受且服务器可验证时才成为 active 需求。

## Boundaries

### In

- Screening v2 结构化输出合同和 prompt。
- 多标签 turn signals。
- chat operations、observations、answer 和 assistant proposals。
- 明确接受上一轮提案的可验证协议。
- 确定性回答组合，以及基于本 change 可用的 readiness/turn signals 生成短期 presentation action；三轴 Policy 在后续 Builder gate change 中扩展同一入口。
- Screening guard、bounded retry、错误分类和可观测证据。
- 现有 Screening evaluation/replay 适配到 v2 契约。

### Out

- 不改变最低确认矩阵。
- 不实现确认快照或 Builder admission。
- 不实现 Web 侧栏和核定面板。
- 不接入 Jev；Jev 只保留未来作为 turn signal 的辅助 prior。
- 不让 Screening 判断兼容、报价或选择配件。
- 不增加第二次 LLM 调用只为润色追问。
- 不实现 confirmation/build 三轴完整 Policy；Spec 4 扩展本 change 的 presentation action 入口，不另建并行决策器。

## Turn contract

Screening 返回每轮短生命周期结果，不把回答和动作写进 RequirementState：

```go
type RequirementTurnSignals struct {
    AsksQuestion   bool `json:"asks_question"`
    RequestsReview bool `json:"requests_review"`
    RequestsBuild  bool `json:"requests_build"`
    Ambiguous      bool `json:"ambiguous"`
}

type RequirementProposal struct {
    Field string          `json:"field"`
    Value json.RawMessage `json:"value"`
    Text  string          `json:"text"`
}

type RequirementTurnResult struct {
    Operations   []RequirementOperation        `json:"operations"`
    Observations []RequirementObservationInput `json:"observations"`
    Signals      RequirementTurnSignals        `json:"turn_signals"`
    Proposals    []RequirementProposal         `json:"proposals,omitempty"`
    Answer       string                        `json:"answer,omitempty"`
}
```

字段名可按项目风格调整，但合同语义必须保持：

- `operations` 只包含用户本轮明确表达或明确接受的当前变更。
- `observations` 保存不能可靠结构化、但后续理解不能丢失的用户原话。
- `turn_signals` 是多标签请求事实，不是授权；同一句可以同时 update 和 request build。
- `proposals` 是助手准备向用户建议的字段值，不是 active requirement。
- `answer` 只回答用户当前问题，不得宣布 readiness、确认成功或 Builder 已启动。
- 严格解码拒绝未知字段、非法 signal/operation/proposal 值及超长输出；空 `answer` 合法，不能为了凑回复而编造事实。

`has_requirement_update` 不需要模型输出，可由 operations/observations 是否为空确定。禁止恢复 `next_action` 单选标签。

## Operation semantics

沿用领域 Reducer 支持的操作：

- `set`：明确设置当前值。
- `remove`：明确撤销当前要求。
- `restore`：恢复被临时覆盖的前值。
- `alternative`：讨论备选，不改变当前值。
- `conflict`：当前表达无法可靠确定唯一有效值。

典型边界：

| User turn | Result |
|---|---|
| “预算 7500” | set `budget_cny=7500` |
| “7500 够吗？” | question；7500 可记为 alternative，不直接 active |
| “那就 7500 吧” | set `budget_cny=7500` |
| “预算不要超过 7500” | set budget 7500，并明确 budget flex 0 |
| “如果改成一万呢？” | alternative 10000，当前预算不变 |
| “全部新买” | set `existing_parts=[]` |
| “有张显卡” | set existing gpu；型号仍缺失 |
| “其实是 4070 Super” | 更正 owned gpu 型号 |
| “1080p 够了” | set gaming resolution 1080p |
| “帧率越高越好但看预算” | set performance goal fps_first/prefer；不生成 FPS 数字 |
| “预算改 9000 然后开始” | set budget + requests_build=true |

模型建议、市场行情、候选价格和系统默认不得作为用户 active 值。

### Unsupported capability

- 用户明确要求本次一并购买/配置显示器、键盘或鼠标时，Screening 以本轮原话产出稳定原因码 `unsupported_capability:monitor|keyboard|mouse` 的 observation；它不写入 tower spec，也不能只塞入 `notes` 后视为已处理。
- 用户随后明确放弃某项时，产出带本轮 quote 的 `remove unsupported.<name>`；不得靠无关字段更新或自由文本 reason 自动解除阻塞。
- 对“我有显示器”“1080p 显示器够用”等背景陈述与购买请求作区分；不确定时保留普通 observation/追问，不臆造 unsupported 或放弃操作。
- Reducer/Readiness 的允许值、撤销校验和优先级以 Spec 2 已落地的领域实现为准，Screening 不复制判断规则。

## Accepted proposal

必须支持以下小白对话而不牺牲证据：

```text
助手：按 7500 元预算继续可以吗？
用户：可以。
```

协议：

1. 助手提出可被接受的具体字段和值时，Screening 输出 `proposals`；只有最终实际发送给用户的助手消息明确呈现了对应字段、具体值和可接受问句，才允许保存 proposal。被组合器删改、过滤或未显示的建议必须丢弃。
2. 产品层在同一持久化边界保存助手消息与其 proposal，为 proposal 绑定真实 assistant message ID，并作为非 active、只对紧接着的下一条用户消息有效的记录；刷新或并发请求不得产生悬空、跨轮提案。
3. 紧接着的下一轮用户明确接受时，operation 使用 `evidence=accepted_proposal`；若该轮没有接受，proposal 自动失效。
4. Reducer 只在存在同字段、同规范化值、未过期 proposal，且当前用户 quote 明确表达接受时采用该值；字段、值、消息归属和紧邻轮次均由服务器验证，不能只相信模型给出的 `accepted_proposal` 标签。
5. 接受后 proposal 标记 resolved；拒绝、覆盖或未在下一轮接受时不得继续复用。
6. 模型不能自行提供或猜测 assistant message ID；绑定由产品层完成。多个未解决 proposal 或“可以”指向不明时不自动采用，要求用户指明字段和值。

不存在可验证 proposal 时，“可以”“就这样”只能产生 ambiguous/review signal，不能凭上下文编造字段。

## Input construction

Screening 输入只包含完成本轮语义任务所需的有界内容：

- 当前用户原文和服务器分配的 source metadata。
- `RequirementStatePromptView`，不发送完整 history。
- 当前 deterministic Readiness 摘要和 next question field；这是参考，不赋予模型覆盖权。
- 最近一条助手可见回复及未解决 proposals。
- 是否存在配置版本、是否有 build run 正在执行。
- 当前 capability 为 tower。

排除 Builder candidate、完整工具轨迹、价格表、隐藏推理和与本轮无关的历史消息。相同输入和配置必须确定性渲染 prompt，并记录 prompt SHA256。

## Response composition

产品层按固定顺序组合最终回复：

1. 应用 operations；失败时不部分保存。
2. 重新计算 Readiness。
3. 对 Screening 的 `answer` 做确定性工作流/能力范围守卫：不得透传“已开始生成”等虚假执行宣称，也不得承诺配置 tower 之外的品类；无法安全保留时用诚实的范围说明替换，不改写用户需求事实。
4. 如果用户当前提出问题，先展示 answer。
5. 若 incomplete，追加 deterministic question plan 选择的一个问题组。
6. 若刚变为 ready 且用户没有请求 review/build，只提示“现在可以核定需求”，不自动打开面板。
7. 若 ready 且 requests_review/build，产生 `open_requirement_review` presentation action；仍不启动 Builder。
8. 若 incomplete 且 requests_review/build，产生 `focus_missing_requirement` 并询问领域问题计划选中的首个阻塞项；它可能是 conflict 或 unsupported capability，而不一定是 missing field。

每轮最多主动追加一个问题组。多个 owned part 型号可以作为一个相关问题组；普通可选字段不主动追问。

### Question priority

直接消费 Spec 2 的 `NextRequirementQuestion`/`RequirementStateQuestions`（或等价单一领域入口）：先处理 blocking conflict、unsupported capability，再处理最低矩阵缺项。不得在 prompt、产品层或本 change 新建第二份字段顺序表；ready 时不补问。

如果用户说“不知道预算”，answer 可以提供有来源或明确口径的参考区间，但预算保持 unknown；建议值只有在用户后续明确接受时才进入 active。

## Turn signals and policy boundary

- `asks_question`：需要回答当前问题，不表示不能同时更新字段。
- `requests_review`：用户希望查看/核对需求。
- `requests_build`：用户表达开始生成意愿；仍需核定面板和确定性 admission。
- `ambiguous`：无法安全理解当前动作；字段证据充分的 operations 仍可保存，但产品不能执行不可逆动作。

Signals 只保存在当前 run/turn 的结果或事件中，不进入 RequirementState 长期真值。后续 Jev 可以提供同一合同的辅助 prior，但 Screening 必须能独立运行，且 Jev 无权修改 operations/readiness。

## Failure handling

- 保留现有有界结构修复重试，但设置清晰最大次数；不能因 JSON 失败无限调用。
- schema/decode/ungrounded operation 属于 contract failure；完整 state 不修改。
- provider timeout/rate limit/transport 与 semantic failure 分开记录。
- 如果模型 answer 可用但 operations 非法，不能只保存 answer 并声称需求已记录。
- 如果 proposal 保存失败，不得保留一个可被后续“可以”错误接受的半状态；助手消息仍可展示时须明确撤销该 proposal 的可接受资格。
- 错误文案说明需求是否保存和下一步；不暴露原始模型输出。
- 标题生成、摘要等辅助失败不得在 Screening 错误路径再次调用模型形成重试放大。

## Implementation targets

优先修改现有路径，不创建平行 Agent：

- `internal/agents/pipeline`：v2 prompt、严格解码、guard 和 bounded repair。
- `internal/product/agent.go`：返回新的 turn result。
- `internal/product/service.go`：Reducer、Readiness、response composition 和 presentation action。
- `internal/store`：把可接受 proposal 与实际发出的助手消息按 session/turn 绑定保存并保证紧邻轮次、并发消费和失效语义；复用现有消息/运行事务，不建立第二套需求真值。
- `internal/schemas`：只补充已在 readiness change 冻结的 accepted-proposal 支持。
- `internal/planningeval`：v2 extraction/conversation 记录和 replay。
- OpenAPI/技术文档同步 turn result 与事件，但不把内部 prompt 暴露给 Web。
- Spec 4 在这里的短期 presentation action/Policy 入口上补 confirmation/build 三轴、原子确认和 admission；本 change 不实现确认 API，也不创建 Builder run。

## Verification

- evaluation foundation 的 extraction 和 conversation development/calibration 全量运行。
- 关键边界 live 至少三次并报告 Pass^3；不使用 Best@3。按冻结 gates 报告 operation precision/recall、关键错写、turn signals、case/final-state 成功率、重复追问、provider/延迟/调用预算及 veto；模型层样本不足或某门槛不可评估时不得宣称通过。
- 先在 dev/calibration 修复；holdout 保持锁定，待发布认证 change 按预注册流程运行。真实模型调用必须有显式正数预算并保存 plan、provider 观测和重放证据。
- 对 V9 同时人工复核“承诺配置外设”与“明确告知暂不支持”两种回复：当前 grader 的纯关键词规则会把后者也算违规。不得靠回避品类名称或修改产品文案来迎合误报；若确认为判卷缺陷，按冻结资产变更流程修正 grader/金丝雀、升级版本、归档旧基线、零模型 regrade 并记录 provenance，再判断候选是否过门槛；不自行改 holdout 金标。
- guard/replay 覆盖 current prompt outputs 和故意非法输出。
- 产品/存储测试覆盖 proposal 可见性、同轮原子保存、不同会话、非紧邻轮、并发双重接受、多个候选的歧义及明确拒绝/覆盖；外设请求与撤销、仅有 unsupported/conflict 的追问和 V8/V9 安全回退。
- `go test ./internal/agents/pipeline ./internal/product ./internal/schemas ./internal/planningeval`。
- `go test ./... && go vet ./...`。
- 无网络模式下所有确定性测试和 replay 通过。

## Acceptance scenarios

1. 首句 FPS 游戏/高帧率只保存用途、titles、performance goal，并继续追问，不触发核定。
2. 用户询问“这个价位够吗”时先回答，不采用助手给出的数字。
3. 用户随后说“7500”才设置预算。
4. “全部新买”设置 explicit empty existing parts；若其他最低字段齐全，只提示可以核定。
5. “预算改 9000 然后开始”保存预算并请求打开核定，不继续使用旧确认。
6. “如果 10000 呢”不覆盖当前预算。
7. 无 proposal 时单独“可以”不生成新字段。
8. 有匹配 proposal 时“可以”可通过服务器验证并采用。
9. 用户问题和字段补充同轮出现时，既回答问题又保存字段，最多追加一个追问。
10. 明确要求配置显示器时记录 unsupported 并说明 tower 范围；后续“显示器先不要了”只解除 monitor 阻塞，更新 notes 不得解除。
11. 仅提及已有显示器或询问 1080p，不等于要求本次购买显示器。
12. 模型错误声称“已经开始生成”或“把显示器一起配上”时，最终回复不得透传该承诺，也不得启动 Builder。

## Completion checklist

- [ ] Screening v2 不输出或持久化 `next_action`。
- [ ] operations/observations/signals/proposals/answer 合同严格解码。
- [ ] assistant proposal 接受可由服务器验证。
- [ ] proposal 只在对应具体建议确实发送给用户后才可接受，且跨轮/并发/模糊“可以”不能误采用。
- [ ] unsupported 能力请求与明确撤销按 Spec 2 的稳定原因码和保留操作处理；背景提及不误阻塞。
- [ ] response composition 消费领域问题计划，短期 presentation action 由确定性代码控制；Spec 4 可在同一入口扩展三轴 Policy。
- [ ] 首句模糊需求不会因模型宣称 ready 而进入核定。
- [ ] extraction/conversation 评估按冻结 gates 报告并通过本 change 可评估门槛，所有本 change 负责的 veto 为零；V9 误报如发生，先完成有版本记录的判卷修正，不以绕过文案替代。
- [ ] OpenAPI 与技术文档同步。
- [ ] 未修改确认快照、Builder admission 和 Web UI。
