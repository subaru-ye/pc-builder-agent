---
status: proposed
created: 2026-09-22
---

# Change: RequirementState and deterministic readiness v2

## Dependency

必须先完成 `requirement-v2-evaluation-foundation`，冻结 Requirement v2 的 deterministic fixtures、grader 和 gates。

## Outcome

把“用户表达过什么”和“这些信息是否足以核定”收归确定性领域代码。RequirementState 只保存有证据的需求事实、约束、背景、备选和冲突；Readiness 根据稳定矩阵计算缺失项、阻塞冲突、有效系统默认和确认资格。模型不再通过 `next_action` 宣布需求已经完整。

本 change 完成后，即使没有 Screening 和 Web 改动，给定相同 RequirementState 也必须总能得到相同 readiness 结果。

## Boundaries

### In

- RequirementState schema v2 与 RequirementSpec 的最小增量。
- 字段状态、来源、明确不限、系统默认和冲突语义。
- `configuration_scope=["tower"]` 当前能力边界。
- `use_case.performance_goal`。
- 确定性 Readiness、Projection 和问题计划。
- 聊天与手动编辑共用的原子 Reducer。
- 移除 `RequirementState.reply`、`RequirementState.next_action` 及其决策权。
- OpenAPI、PRD、系统架构、自主规划流程和相关领域文档同步。
- 一次性 schema 升级；不保留 v1 会话兼容分支。

### Out

- 不修改 Screening prompt 或模型输出合同。
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
- auto：用户明确授权系统选择；当前 tower gaming resolution 不支持 auto。

系统默认不写成 active 用户字段。Projection 可以产生 effective default，但必须保留 `origin=system_default` 供确认页展示。

### Schema version

- `RequirementState.schema_version` 升级为 2。
- 删除持久化 `reply` 和 `next_action`。
- `RequirementUpdate` 的会话回复与本轮动作移到后续 Screening contract，不进入长期需求真值。
- 不读取或迁移旧 RequirementState v1；开发数据由实现 change 的执行者用明确、可恢复的环境步骤重建，不能在应用启动时静默猜测旧状态。

### Configuration scope

RequirementSpec v2 显式包含：

```json
"configuration_scope": ["tower"]
```

当前只允许 `tower`。它是产品能力和最终快照的有效字段，不是用户陈述，因此不进入 RequirementState 的 active 用户字段，也不要求用户回答。Readiness/Projection 注入该系统默认并向展示 DTO 标注来源。未来显示器或外设 change 扩展该枚举和对应规则；本 change 不提前接受未支持品类。

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

- 系统默认 `balanced`，但未明确时不写入 RequirementState active。
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
    Status               string // incomplete | ready
    MissingFields        []string
    BlockingConflicts    []string
    ConfirmationEligible bool
    EffectiveDefaults    []RequirementDefault
}

func EvaluateRequirementReadiness(RequirementState) (RequirementReadiness, error)
```

要求：

- 输出顺序稳定，按产品追问优先级排序。
- 同一状态重复计算字节级稳定。
- `ConfirmationEligible` 只等于“无缺失、无阻塞冲突且状态可投影”，不表示用户本轮请求确认。
- 非法 active 值返回 schema error，不把错误降级成 unknown。
- unsupported capability 单列为阻塞原因，不能伪装成普通 missing field。

### Projection

`ProjectRequirement`（或收敛后的 `RequirementStateSpec`）必须先调用相同 readiness 规则：

- incomplete 时返回 nil spec、完整 missing/conflict，不生成可供 Builder 使用的 RequirementSpec。
- ready 时只投影 active 用户字段并展开 system defaults。
- unknown、removed、alternative 和未解决 observation 不成为用户偏好。
- soft conflict 不投影冲突值；保留可读 observation。
- Planning、确认和 API 读取必须共用这一入口，禁止再出现 `planningProjection()` 返回固定空 missing 的旁路。

## Reducer

保留并收敛现有 `ApplyRequirementUpdate`：

- 聊天和 UI 编辑使用同一 reducer。
- 一批 operations 全部校验成功后原子提交；任一项失败则状态不变。
- chat 来源的 set/remove/conflict/restore 必须绑定本轮用户证据；edit 来源由已认证的 UI 请求提供。
- `evidence=inferred` 不能写成 active。
- alternative 不改变当前 active 值。
- existing_parts 与 owned_parts 保持现有联动，但 `existing_parts=[]` 必须是明确 active 值。
- assistant proposal 永远不是 active；为后续明确接受保留可验证的 proposal/alternative 记录及来源，但接受协议由 Screening change 完成。
- revision、changes、history 和 source 不因 Projection 展开默认值而变化。

## Deterministic question plan

领域层根据 readiness 返回下一个追问字段，不把自然语言回答权交给前端：

1. 阻塞 conflict。
2. `use_case.type`。
3. `budget_cny`。
4. `existing_parts`（全部新买或复用）。
5. gaming resolution / productivity titles。
6. 缺失的 owned part 型号；多个品类可合并成一个请求。
7. `budget_basis`。

该计划只选择字段和稳定原因码；自然语言组合在 Screening/Product change 完成。每轮最多选择一个问题组。

## Product integration boundary

本 change 只提供领域真值，但必须删除当前错误旁路：

- `internal/product.planningProjection` 不再忽略 missing。
- `requirementLifecycle` 不读取模型 `NextAction` 判断 collecting/ready。
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
- 聚焦单元测试覆盖每个 required/conditional/default/status/conflict 分支。
- 属性测试或表驱动测试证明相同输入稳定、Reducer 原子、Projection 不修改输入。
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

## Completion checklist

- [ ] RequirementState/Spec v2 合同完成，v1 兼容路径未保留。
- [ ] `reply`/`next_action` 不再存在于持久化需求真值。
- [ ] 最低矩阵和 conflict 规则只有一个领域实现。
- [ ] Planning/确认读取不再绕过 missing。
- [ ] system defaults 可追溯且不冒充用户事实。
- [ ] deterministic evaluator 100% 通过且无 veto。
- [ ] OpenAPI、PRD 和技术文档同步。
- [ ] 未修改 Screening prompt、Builder admission 和 Web UI。
