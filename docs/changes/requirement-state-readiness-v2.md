---
status: done
created: 2026-09-22
completed: 2026-09-23
---

# Change: RequirementState and deterministic readiness v2

## Dependency

必须先完成 `requirement-v2-evaluation-foundation`，冻结 Requirement v2 的 deterministic fixtures、grader 和 gates。

## Outcome

把“用户表达过什么”和“这些信息是否足以核定”收归确定性领域代码。RequirementState 只保存有证据的需求事实、约束、背景、备选和冲突；Readiness 根据稳定矩阵计算缺失项、阻塞冲突、有效系统默认和确认资格。模型不再通过 `next_action` 宣布需求已经完整。

本 change 完成后，即使没有 Screening 和 Web 改动，给定相同 RequirementState 也必须总能得到相同 readiness 结果。

## Boundaries

### In

- RequirementState schema v2 与 RequirementSpec schema v2 的最小增量。
- 字段状态、来源、明确不限、系统默认和冲突语义。
- `configuration_scope=["tower"]` 当前能力边界。
- `use_case.performance_goal`。
- 确定性 Readiness、Projection 和问题计划。
- 聊天与手动编辑共用的原子 Reducer。
- 移除持久化 `RequirementState.reply`、`RequirementState.next_action` 及其决策权。
- OpenAPI、PRD、系统架构、自主规划流程和相关领域文档同步。
- 一次性 schema 切换；不保留产品运行时 v1 会话兼容分支。

### Out

- 不修改 Screening prompt 或模型输出合同。
- 不实现 assistant proposal 接受协议；该证据类型和短期记录由 Screening v2 change 实现。
- 不实现确认快照、Builder admission 或 Web UI。
- 不实现显示器、键盘、鼠标。
- 不接入 Jev。
- 不实现多用途权重；当前仍是单一主要用途。

## Domain model

### RequirementState

`RequirementState` 是当前会话需求草稿的唯一事实源。字段状态保持：

| Status | Meaning | Readiness |
|---|---|---|
| unknown | 用户没有表达 | 必填阻塞；可选不阻塞 |
| active | 当前有效的用户事实或要求 | 参与投影和 readiness |
| conflict | 存在无法自动取舍的当前表达 | 必填或 must 阻塞；普通软偏好可省略 |
| removed | 用户明确撤销 | 必填按缺失处理；可选不阻塞，保留审计历史 |

`unknown`、明确 `any` 与未来的明确 `auto` 不得合并：

- unknown：没有用户授权。
- any：用户明确表示不限，是 active 用户值。
- auto：用户明确授权系统选择；本 v2 只保留语义，尚未进入枚举，Reducer 必须拒绝其作为当前 tower gaming resolution。

系统默认不写成 active 用户字段。Projection 可以产生 effective default，但必须保留 `origin=system_default` 供确认页展示。

### Schema version

- `RequirementState.schema_version` 和 `RequirementSpec.schema_version` 均升级为 2，两者的解码器都只接受 2。
- 删除持久化 `RequirementState.reply` 和 `RequirementState.next_action`。
- 本 change 不改 Screening prompt/输出，因此可在 pipeline 边界保留一个临时 legacy turn decoder 接收当前 `reply/next_action`；它必须在进入 Reducer 前剥离两者，`next_action` 不得影响 readiness、状态或 Builder。该适配器须有明确删除注释，并在 Screening v2 change 中删除。
- 领域 `RequirementUpdate` 不再含会话回复和流程动作；临时 decoder 是传输适配，不是第二套需求合同。
- 产品读取旧 RequirementState/Spec v1 必须以稳定错误拒绝，不得静默重建、补默认或自动迁移。开发数据通过显式、指定命名空间且可恢复的运维步骤重建，不得在应用启动时自动执行。
- 冻结 evaluator 的 reducer/readiness 输入保留了建基时的 v1 外形。除 2026-09-23 经定向复核授权修正的两条 readiness 金标及其 manifest 外，不修改其他 fixture、split 或 gates；`internal/planningeval` 可在调用 v2 领域 API 前做“只改 schema version/补新字段 unknown”的机械、零推断适配，并以单测证明不改变金标语义。该适配不得被产品路径调用。

### Configuration scope

RequirementSpec v2 显式包含：

```json
"configuration_scope": ["tower"]
```

当前只允许且必须规范化为精确的 `["tower"]`。它是产品能力和最终快照的有效字段，不是用户陈述，因此不进入 RequirementState 的 active 用户字段，也不要求用户回答。Readiness/Projection 注入该系统默认并向展示 DTO 标注来源；直接 Decode RequirementSpec v2 也必须要求该字段，禁止绕过 Projection。未来显示器或外设 change 扩展该枚举和对应规则；本 change 不提前接受未支持品类。

### Use case

保持一个主要用途：

```text
general
gaming
productivity
```

新增可选字段：

```text
use_case.performance_goal:
  balanced
  fps_first
  quality_first
```

- 仅 gaming 在未明确时使用系统默认 `balanced`，且不写入 RequirementState active；用户明确的 performance goal 可作为其他用途的取舍目标保留。
- “帧数越高越好”映射为 `fps_first`，不生成具体 FPS。
- `use_case.fps_target` 继续作为高级、明确数值的可选字段。
- FPS 是主机渲染目标，不等于显示器刷新率；当前 schema 不新增 refresh rate。

多用途表达仍需选出一个主要用途：明确“主要游戏、偶尔剪辑”可记 gaming；用户表示游戏与生产力同等重要且未给优先级时，`use_case.type` 保持 unknown/conflict 并进入追问。

### System defaults

Projection 保留现有产品默认并显式列出来源：

- `budget_flex=0.1`，表示最高可上浮 10%。
- `size_pref=any`。
- `noise_pref=any`。
- `brand_pref.cpu=any`、`brand_pref.gpu=any`。
- gaming `performance_goal=balanced`。
- `configuration_scope=[tower]`。

确认快照会冻结这些有效值；RequirementState 仍保持用户是否明确表达的真实来源。

`EffectiveDefaults` 每项固定为 `{field,value,origin:'system_default'}`，仅列出实际被展开的默认；已有 active 用户值的字段不再列入。输出顺序固定为 `budget_flex`、`size_pref`、`noise_pref`、`brand_pref.cpu`、`brand_pref.gpu`、`configuration_scope`，gaming 最后追加 `performance_goal`，与冻结 readiness 金标一致。

### 值域与规范化

- `budget_cny` 为正整数；`budget_flex` 为 0–0.3；`use_case.fps_target` 为正整数。
- `use_case.titles` 元素 trim 后必须非空，去重但保留用户顺序；productivity 至少一项。
- `existing_parts` 品类去重并按领域固定顺序输出；`owned_parts` 每个品类只能有一条当前型号，品类必须与 `existing_parts` 一致。
- Reducer 对 existing/owned 的联动在同一原子事务内完成；不允许产生 `existing_parts=[]` 但 owned 非空的状态。
- 字符串沿用当前长度上限，且结构化字段禁止空白值。非法 active 值是 schema error，不得修剪成 unknown 或静默丢弃。
- RequirementState 允许 resolution=`any` 作为有证据的用户值；gaming readiness 仍要求 1080p/2K/4K。RequirementSpec v2 可表达 `any`，但 gaming 的组合校验必须拒绝 `any`。

### 自由字段与 observation

- 保留现有 `free.*` 作为有来源、可独立修改的用户事实或约束；它们不能满足最低矩阵中的结构化字段。
- Projection 将 active `free.*`、`appearance`、`recipient` 放入 RequirementSpec v2 已有的 `requirement_details`，并在 `constraint_strengths`/`requirement_semantics` 保留强度和语义；不得临时生成 RequirementSpec 未定义的嵌套对象。
- unresolved observation 只是有来源的上下文，不得满足必填或进入用户偏好；后续对同一 field 的有效 set/remove/conflict 可将其标记 resolved。
- 当 observation 带经 Reducer 校验的稳定原因码 `unsupported_capability:<name>` 时，Readiness 将 name 单列到 `UnsupportedCapabilities` 并阻塞确认；当前 name 只允许 `monitor|keyboard|mouse`。只有用户后续放弃该要求、使对应 observation resolved 后才解除；自由文本 reason 不得通过关键词推断 capability。
- 两类 observation 的解除规则不同（2026-09-23 返工补全）：
  - 普通 observation：仍按"同 field 的有效 set/remove/conflict"自动 resolve。
  - unsupported capability observation：不参与同 field 自动 resolve——更新 notes 或其他无关字段不得解除显示器等阻塞。唯一解除方式是能力专属、绑定本轮用户证据的撤销操作 `remove unsupported.<name>`（name ∈ monitor|keyboard|mouse）；该键不是需求字段、不进入 fields，只由 Reducer 将同 name 的未解决观察标记 resolved，其余 capability 保持阻塞。`set/alternative/conflict unsupported.<name>` 与携带 value 的操作一律拒绝；没有对应未解决观察时拒绝。自然语言提取（识别"不要显示器了"并产出该操作）由 Screening 收集 change 负责，本 change 不做关键词启发式。

## Minimum confirmation matrix

### Always required user evidence

| Field | Rule |
|---|---|
| `budget_cny` | 正整数预算金额 |
| `use_case.type` | 单一主要用途 |
| `existing_parts` | 必须 active；`[]` 表示用户明确主机配件全部新买，unknown 不能默认为空 |

当前配置范围固定为 tower，因此预算覆盖本次购买/配置的主机八件；这是当前 capability 的明确展示，不需要再问显示器是否包含。用户要求显示器或外设时记录为 unresolved/unsupported observation，不能声称本次会覆盖。

### Conditional requirements

| Condition | Required |
|---|---|
| gaming | `use_case.resolution` 为 1080p/2K/4K |
| productivity | `use_case.titles` 至少一项；可为软件或任务，如 Premiere、Blender、4K 视频剪辑、本地 AI 推理 |
| `existing_parts` 非空 | 每个品类都存在对应、非空、准确的 `owned_parts.<category>.model` |
| `existing_parts` 或 `owned_parts` 非空 | `budget_basis` 为 `new_purchase` 或 `full_build` |

gaming titles、具体 FPS、品牌、静音、尺寸、外观、装机对象、优先配件和 notes 均不是最低条件。

### Blocking conflicts

- 任何必填或条件必填字段的 conflict 阻塞确认。
- 任何 `strength=must` 的 active constraint 发生 conflict 时阻塞确认。
- 普通可选 `prefer` conflict 不阻塞；Projection 不采用任何冲突值，并把原文作为 unresolved observation 交给确认页展示“本次未采用”。

## Readiness API

领域层提供单一纯函数；命名可按现有包风格调整，但语义不得拆散到产品或前端：

```go
type RequirementReadiness struct {
    Status                  string // incomplete | ready
    MissingFields           []string
    BlockingConflicts       []string
    UnsupportedCapabilities []string
    ConfirmationEligible    bool
    EffectiveDefaults       []RequirementDefault
}

func EvaluateRequirementReadiness(RequirementState) (RequirementReadiness, error)
```

要求：

- `MissingFields` 按产品追问优先级排序；`BlockingConflicts` 先用同一字段优先级，再按字段名排序；`UnsupportedCapabilities` 按名称排序。
- 同一状态重复计算字节级稳定。
- `ConfirmationEligible` 只等于“无缺失、无阻塞冲突、无未解决的 unsupported capability 且状态可投影”，不表示用户本轮请求确认。
- 非法 active 值返回 schema error，不把错误降级成 unknown。
- unsupported capability 单列为阻塞原因，不能伪装成普通 missing field。

### Projection

`ProjectRequirement`（或收敛后的 `RequirementStateSpec`）必须先调用相同 readiness 规则：

- incomplete 时返回 nil spec 和完整 readiness，不生成可供 Builder 使用的 RequirementSpec。
- ready 时只投影 active 用户字段并展开 system defaults。
- unknown、removed、alternative 和未解决 observation 不成为用户偏好。
- soft conflict 不投影冲突值；保留可读 observation。
- RequirementSpec v2 解码后再做一次组合不变量校验，避免调用方绕过 readiness 构造 gaming+any、owned/existing 不一致等非法 spec。
- Planning、确认和 API 读取必须共用这一入口，禁止再出现 `planningProjection()` 返回固定空 missing 的旁路。

## Reducer

保留并收敛现有 `ApplyRequirementUpdate`：

- 聊天和 UI 编辑使用同一 reducer。
- 一批 operations 全部校验成功后原子提交；任一项失败则状态不变。
- chat 来源的 set/remove/conflict/restore 必须绑定本轮用户证据；edit 来源由已认证的 UI 请求提供。
- `evidence=inferred` 不能写成 active。
- alternative 不改变当前 active 值。
- existing_parts 与 owned_parts 按上述不变量联动，但 `existing_parts=[]` 必须是明确 active 值。
- assistant proposal 永远不是 active；本 change 不新增 proposal 记录或 `accepted_proposal` evidence，避免在 Screening v2 之前形成无法验证的半套协议。
- 每个成功的非空批次只将 revision 增加 1，而不是每个 operation 增加 1；同批的 changes 使用同一 revision。operations 和 observations 都为空时是字节级 no-op，不增 revision。
- 任一 operation/observation 非法时，返回稳定错误码且输入状态在 fields、alternatives、observations、changes、history、revision 上全部不变。
- revision、changes、history 和 source 不因 Projection 展开默认值而变化。

## Deterministic question plan

领域层根据 readiness 返回下一个追问字段，不把自然语言回答权交给前端。输出是 `{reason_code, fields[]}` 或 nil；reason code 为稳定机器值，不包含中文文案：

1. 阻塞 conflict。
2. unsupported capability（告知当前 tower 边界并请用户放弃或等待未来能力）。
3. `use_case.type`。
4. `budget_cny`。
5. `existing_parts`（全部新买或复用）。
6. gaming resolution / productivity titles。
7. 缺失的 owned part 型号；多个品类可合并成一个请求。
8. `budget_basis`。

该计划只选择字段和稳定原因码；自然语言组合在 Screening/Product change 完成。每轮最多选择一个问题组；ready 时返回 nil。Readiness 的 missing 顺序与该计划使用同一优先级定义，禁止维护两份列表。

## Product integration boundary

本 change 只提供领域真值，但必须删除当前错误旁路：

- `internal/product.planningProjection` 不再忽略 missing。
- `requirementLifecycle` 不读取模型 `NextAction` 判断 collecting/ready。
- 分段中间态只能收集/计算 readiness；不新增任何从 chat 自动启动 Builder 的路径。现有显式确认入口可保持可构建，但本 change 不宣称已完成后续快照/admission 保证。
- 为保证分段实现期间主干可构建，产品层可以临时从 readiness 派生旧形状 DTO；该适配器不得保留旧判定逻辑，并必须在 Builder gate change 中删除，不能形成运行时双轨。
- Next.js 不复制最低矩阵或默认值。

## Documentation and contracts

同步修改：

- `internal/schemas` 及其 JSON/OpenAPI contract。
- `docs/api/openapi.yaml`。
- `docs/product/PRD.md`：F1 最低条件、生产力任务、当前 tower scope。
- `docs/tech/系统架构.md`：RequirementState/Spec 与确定性 Readiness。
- `docs/tech/自主规划流程.md`：Builder admission 前置条件。
- `docs/tech/产品API与会话状态机.md`：模型 next_action 不再有状态权威。

## Verification

- 运行 evaluation foundation 的 reducer/readiness 全部 fixture；要求 100%。
- 核对 2026-09-23 两条金标定向修正的人工复核记录、新旧 manifest 与旧产物保留；其余冻结数据不变。如需评估专用 v1 外形适配，单测必须证明它仅做 schema 机械升格、不生成用户事实，并证明产品 decoder 仍拒绝 v1。
- 聚焦单元测试覆盖每个 required/conditional/default/status/conflict/unsupported 分支，以及 RequirementSpec v2 组合不变量。
- 属性测试或表驱动测试证明相同输入稳定、Reducer 批次原子/no-op 不增 revision、Projection 不修改输入。
- 回归测试证明临时 legacy turn decoder 不将 `reply/next_action` 写入 RequirementState，且 `next_action=plan|confirm` 均不改变 readiness 或启动 Builder。
- `go test ./internal/schemas ./internal/product` 及受影响包通过。
- `go test ./...` 与 `go vet ./...` 通过。
- OpenAPI 生成类型与实现无漂移。
- 零模型 replay 证明当前错误的首句 ready 案例被 readiness 拦截。

## Acceptance scenarios

1. “玩 CS2 和无畏契约，希望高帧率”只记录用途、titles、performance goal；缺预算、existing parts、resolution，不能确认。
2. “1080p，预算 7500，全部新买”补齐后 ready；具体 FPS、品牌、静音未知不阻塞。
3. “预算 7500，做剪辑，全部新买”仍缺主要软件或任务。
4. “预算 7500，4K 视频剪辑，全部新买”满足 productivity 条件。
5. “已有显卡”仍缺准确型号和 budget basis。
6. optional soft conflict 可 ready，但冲突值不进入 spec。
7. budget conflict 或 must brand conflict 不可 ready。
8. 系统默认出现在 effective projection，RequirementState 对应字段仍为 unknown。
9. RequirementState/Spec v1 在产品解码边界被明确拒绝；不会回退为空 v2 状态。
10. 当前 Screening 即使返回 `next_action=plan`，也只有 operations/observations 能进入状态，不启动 Builder。
11. 用户要求显示器时，结构化 unsupported observation 单列阻塞；不解析自由文本 reason，也不将显示器写入 tower spec。

## Completion checklist

- [x] RequirementState/Spec v2 合同完成，产品 v1 兼容路径未保留；冻结 evaluator 的机械适配与产品隔离。
- [x] `reply`/`next_action` 不再存在于持久化需求真值；临时传输适配不保留其决策权。
- [x] 最低矩阵和 conflict 规则只有一个领域实现。
- [x] Planning/确认读取不再绕过 missing。
- [x] system defaults 可追溯且不冒充用户事实。
- [x] Reducer 的原子性、revision 语义、existing/owned 不变量和 unsupported reason code 有聚焦测试。
- [x] 本 change 负责的 reducer/readiness 层 dev+cal 全量 100% 通过且无 veto；policy/ui-contract 的失败与 veto 逐项记录并归属后续 change，不以本项宣称整体验收或可发布。
- [x] OpenAPI、PRD 和技术文档同步。
- [x] 未修改 Screening prompt 或 Web UI；现有确认入口仅接入确定性 readiness 校验，确认快照与完整 Builder admission 留给后续 change。

## 验收记录（2026-09-23）

- 冻结金标仅定向修正两条 readiness case，经人工复核并重冻 manifest 为 `abb9cd25b747…`；holdout 未运行。
- 零模型 dev+cal：reducer 11/11、readiness 12/12，两个领域层 veto 均为 0；模型层 26+7 例按计划跳过。
- policy 3/8 且 V9×1、ui-contract 0/3，已逐项记录并留给 Screening v2 与确认/Builder 门禁 change；整体候选仍不可发布。
- `go test ./...`、`go vet ./...`、Web 测试及数据库门控集成测试通过；Spec 2 范围验收完成。
