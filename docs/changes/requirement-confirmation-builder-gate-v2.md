---
status: done
created: 2026-09-22
completed: 2026-09-23
---

# Change: Requirement confirmation and Builder gate v2

## Dependency

依赖 `requirement-state-readiness-v2` 和 `screening-requirement-collection-v2`。评估门槛来自 `requirement-v2-evaluation-foundation`。

## Outcome

把“信息完整”“用户已核定”“配置是否与当前需求一致”拆成独立状态，并建立唯一的原子确认入口。任何聊天文字、模型 signal、旧 phase 或 UI 猜测都不能绕过该入口启动 Builder。Builder 只消费用户实际看到并确认的不可变快照。

完成后，需求刚齐全只会获得确认资格；用户在核定面板执行明确确认后才启动 Builder。生成期间可以继续编辑草稿，但不能启动第二个 Builder，当前任务也不会读取变化中的草稿。

## Boundaries

### In

- Readiness、Confirmation、Build 三轴状态。
- 确认快照、规范化 hash、revision 和 build 关联。
- 与最终 Builder 载荷同源的服务端核定预览及预览 hash；核定面板只渲染，具体视觉留给 Web change。
- 原子 confirm-and-start API、幂等与并发控制。
- 在 Screening v2 已有的短期 presentation action 入口上扩展 confirmation/build 三轴 Policy；不另建平行决策器。
- 生成期间草稿编辑、旧配置 retained/outdated 行为。
- Builder 失败、重试和相同快照复用。
- Session DTO、SSE 事件、存储和相关文档更新。
- 关闭所有聊天直达远程生成的旧路径，尤其是 `RunChange`、确定性预算/显卡改单、`ChangeRequest` 与重试/恢复旁路。

### Out

- 不实现右侧栏视觉和表单；前端 change 消费本合同。
- 不改变字段提取和最低矩阵。
- 不实现并行 Builder、队列或自动取消。
- 不实现显示器和外设。
- 不接入 Jev。
- 不在本 change 重做自然语言改单提取；无法安全投影到当前 v2 草稿的旧 `ChangeRequest` 不得直接生成，须返回可理解的修改/核定引导并记录迁移限制。

## State axes

会话保留粗粒度执行 phase 供运行恢复，但 UI 和 admission 使用以下独立派生状态：

```text
requirement_readiness:
  incomplete
  ready

requirement_confirmation:
  unconfirmed
  confirmed
  modified

build_status:
  none
  running
  current
  outdated
  failed
```

定义：

- incomplete/ready 完全来自确定性 Readiness。
- confirmed 表示当前规范化有效需求与最近确认快照相同。
- modified 表示存在确认快照，但当前草稿规范化结果不同，或当前草稿已不足以投影。
- current 表示最新成功配置关联的 snapshot hash 与当前确认需求相同，且草稿未修改。
- outdated 表示配置仍可查看，但当前草稿/确认目标已经不同。
- failed 表示最近一次 Builder 失败；确认快照仍有效。
- `running` 优先表示活动 Builder；草稿在运行中修改时 confirmation 立即为 modified，run 结束后再按当时草稿与该 run 的 snapshot 计算 current/outdated/failed。`outdated` 仅在已有成功配置时成立；失败状态可与 modified confirmation 并存。最近一次 run 失败但已有更早成功配置时，失败信息与保留的历史配置分别展示，不把历史配置误称为本次成功。

典型组合必须可表达：

| Scenario | Readiness | Confirmation | Build |
|---|---|---|---|
| 新会话信息不足 | incomplete | unconfirmed | none |
| 最低条件齐全 | ready | unconfirmed | none |
| 已确认并生成中 | ready | confirmed | running |
| 生成完成 | ready | confirmed | current |
| 已有成功配置后修改可选偏好 | ready | modified | outdated |
| 已有成功配置后删除预算 | incomplete | modified | outdated |
| 修改后恢复为确认值 | ready | confirmed | current |
| Builder 失败 | ready | confirmed | failed |

## Confirmation snapshot

服务端在核定前先从最新 RequirementState 计算只读 `review_spec`（展开默认后的 RequirementSpec v2）、`review_hash` 和 revision；`review_hash` 基于规范化 `review_spec`，不信任客户端上传的 spec。核定面板展示该预览及所有会影响选型的有效约束、默认、配置范围与旧配置差异；未知/未采用的内容须按其真实状态说明，不能暗中成为 Builder 的要求。预算上限等展示计算由后端提供，不在 Next.js 复制规则。

用户确认时在同一事务冻结确认快照与首个 run 的执行载荷；二者分别不可变：

```text
confirmation snapshot:
  requirement_spec, requirement_state_or_evidence_ref, review_hash
  confirmed_revision, confirmed_at, configuration_scope, schema_version
build run:
  snapshot_id, builder_input_payload, builder_input_hash
```

- `review_hash` 绑定用户看到的规范化有效 RequirementSpec；`builder_input_hash` 绑定实际送往远程 Builder 的完整、规范化执行载荷（当前是 `PlanningInput` 包装，不得把它误当扁平 RequirementSpec）。两者用途不同，JSON key/order 差异不能改变任一 hash。确认时除 revision 还必须核对客户端从该预览获得的 `expected_review_hash`；默认规则或配置范围即使在 revision 未变时发生变化，也要拒绝旧预览。
- run ID、已有配置/提案上下文等影响最终 Builder 载荷的内容须在确认事务内确定并冻结；任何影响选型的语义不得藏在仅执行时才追加的上下文中。`RunBuild` 的发送路径只读取 run 绑定的冻结载荷，不得再用可变 session 草稿或 `planningContext` 重新组装/追加字段。V5 比较的是实际发出的规范化 Builder 载荷与其 `builder_input_hash`，而非把扁平 spec hash 与 `PlanningInput` hash 硬比。
- 快照不可修改；再次确认产生新快照或新的确认记录，不覆盖历史 build 所引用内容。现有 `confirmed_requirement` 若继续存 `PlanningInput`，API 不得将其伪装为 `RequirementSpec`；预览、执行载荷与 hash 须在存储/OpenAPI 中区分。
- 每个 build/run 永久记录其 snapshot ID、review hash 和该 run 的 builder input hash；不能只在 session 当前确认列中保存关联。失败重试复用同一确认快照，但生成新 run ID，因此另冻一份仅 run 标识不同的完整执行载荷与新 hash；先前 run 的载荷/hash 不修改。
- 系统默认更新不能改写旧快照。
- 草稿改回与当前确认快照完全相同的规范化 `review_spec` 时，可自动恢复 confirmed；不因 revision/history 不同强迫重复确认，但已有 run 的冻结执行载荷与 hash 不随之改变。

## Policy

Policy 输入：Readiness、Confirmation、Build、当前 turn signals 和事件来源。输出：allowed actions 与可选 presentation action。沿用 Spec 3 的 `requirementPresentationAction` / `presentation.action` 入口，不再新建平行事件或以模型 `requests_build` 直接调 Builder。

| Condition | User signal/event | Result |
|---|---|---|
| incomplete | requests review/build | `focus_missing_requirement`（按确定性 `next_question` 的字段及 reason code）；不启动 Builder |
| ready + unconfirmed/modified | 普通讨论 | 无自动确认动作 |
| ready + unconfirmed/modified | requests review/build | `open_requirement_review` |
| ready | confirm API submit | 校验后冻结并启动 |
| confirmed + failed | confirm API with `retry_of_run_id` | 草稿与失败 run 的 `review_hash` 一致时，复用确认快照，另建 run/载荷和新幂等键 |
| running | 任意确认/生成请求 | 拒绝第二个 Builder；草稿编辑仍可保存 |

聊天中的“就这样”“开始吧”“帮我配”永远不是 confirm API 的替代品。它只能产生 presentation action，让用户看见最终快照并执行明确操作。旧 `RunChange`、确定性预算/显卡改单及 `ChangeRequest` 也不能直达 Builder：可安全表达为 v2 草稿操作的先更新草稿并引导核定，不可表达的返回明确引导且不生成。任何旧 phase、重试或恢复入口均遵循同一 admission。

Presentation action 沿用已有 `presentation.action` SSE 事件，值为 `open_requirement_review` 或 `focus_missing_requirement`，不写进 RequirementState。刷新后无需自动重放打开动作；右侧需求状态始终可见，避免持久化 UI 命令。

## Confirm API

更新现有端点为显式 optimistic concurrency：

```http
POST /api/v1/sessions/{session_id}/requirement/confirm
Idempotency-Key: <key>
Content-Type: application/json

{
  "schema_version": 2,
  "expected_revision": 7,
  "expected_review_hash": "<hash from latest server review_spec>",
  "retry_of_run_id": null
}
```

`retry_of_run_id` 默认省略；仅失败重试时传目标 run ID，不另设模糊的“继续生成”入口。服务端校验目标属于本会话且已失败、关联快照仍与当前 `review_hash` 相同；复用其确认快照及选型上下文，仅为新 run 冻结新 run 标识和完整执行载荷/hash。

服务端在同一事务/原子存储边界执行；事件在提交后发布，发布失败不回滚已提交的确认/run，客户端可由持久化 run/session 恢复状态：

1. 校验 owner、session、幂等键；相同 key + 相同请求先返回原始 run/快照，不重新从当前草稿取载荷；相同 key + 不同请求返回稳定冲突。
2. 对新请求检查没有 active Builder，读取最新 RequirementState，revision 必须匹配。
3. 重新计算 Readiness；不信任客户端 `eligible`、missing 或 snapshot。
4. 规范化投影并展开 system defaults，计算服务端 `review_hash`，必须与 `expected_review_hash` 匹配。
5. 正常确认时冻结 `review_spec` 快照；失败重试时复用指定快照。确定新 run ID 及该快照关联的已有配置/提案等 Builder 上下文，冻结该 run 的完整 `PlanningInput` 载荷与 hash。
6. 创建唯一 build run，绑定不可变快照及两个 hash；提交后发布确认与 run.started 事件。

失败语义：

- revision 或 review hash 变化：409，需求未确认、Builder 未启动；返回最新预览供用户重新核定。即使 revision 不变，默认值/能力范围变化也属于预览失效。
- readiness 不完整：409/422 稳定 reason code，返回最新 missing/conflicts。
- active Builder：409，说明当前任务仍在运行。
- 同一幂等键和相同请求重试：返回原 run、原快照和原 hash；不能用变化后的 session 当前值代替。
- 事务失败：不能留下只有确认没有 run，或只有 run 没有快照的半状态。

## Editing while Builder runs

- `PATCH requirement-state` 在 build run 期间允许保存草稿编辑，不再因“会话正在执行任务”统一 409。不能复用当前 `StartMessageRun(ForceScreening)` 路径：它受 phase/单活动 run 约束，还会创建第二个 AgentRun；应使用独立原子编辑事务，但继续复用同一 Reducer、权限和幂等约束。
- 编辑继续要求 `expected_revision`，只修改 draft RequirementState、revision 和编辑历史；原 Builder run/phase 与其确认快照均保持不变。提交后响应/事件给出最新三轴状态。
- 正在运行的 Builder 只读取启动时 snapshot；不得重新加载 draft。
- 编辑后 UI 显示“本次生成仍基于上一版确认需求”。
- active Builder 存在时确认/生成 CTA 禁用，API 仍做服务端硬拒绝。
- 第一版不排队、不并行、不自动取消；当前任务结束后用户重新核定并生成新版。
- 聊天 composer 是否在生成中可发送保持现有产品限制；侧栏的确定性需求编辑必须可用。

## Build/version relationship

- 已有配置永不因草稿修改而删除或隐藏。
- 配置详情继续可读、可导出、可比较，并显示关联 snapshot/version。
- 当前草稿与 build snapshot 不同时标记“基于上一版需求”。
- 新 build 成功后成为 current；旧 build 保持历史只读。
- Builder 失败不取消 confirmed；仅在当前草稿规范化 `review_hash` 仍等于失败 run 的 hash、没有 active Builder 时，才可按相同确认快照和新幂等键重试。新 run 仅替换 run 标识，不重新读取可变草稿或最新 build 拼装选型上下文；草稿已修改则先重新核定。
- 如果用户在 run 期间修改草稿，run 成功后其产物立即是 outdated，相对它自己的 snapshot 仍是有效历史版本。

## Session API

Session DTO 增加稳定、后端计算的字段；具体嵌套可按 OpenAPI 风格调整：

```yaml
requirement_readiness:
  status: incomplete|ready
  missing_fields: []
  blocking_conflicts: []
  unsupported_capabilities: []
  next_question: {reason_code: string, fields: []}|null
  confirmation_eligible: boolean
  effective_defaults: []
review_spec: RequirementSpecV2|null
review_hash: string|null
effective_budget_ceiling_cny: integer|null
requirement_confirmation:
  status: unconfirmed|confirmed|modified
  confirmed_revision: integer|null
  confirmed_at: datetime|null
  confirmed_review_hash: string|null
build_relation:
  status: none|running|current|outdated|failed
  version: integer|null
  snapshot_id: string|null
  review_hash: string|null
  builder_input_hash: string|null
```

`review_spec` 与 `review_hash` 必须来自同一次服务端投影；incomplete 时不伪造可确认预览。旧的单值 `requirement_status` 和裸 `missing_fields` 在整体迁移中删除，不保留双轨。现有 `confirmed_requirement` 若为 `PlanningInput`，OpenAPI 类型须如实标明或改名，不得冒称 `RequirementSpecV2`。Next.js 只渲染这些真值，不重新比较 JSON、计算预算上限或推断状态。

## Run and storage invariants

- 同一 session 同时最多一个非终态 Builder run。
- build run 创建时 snapshot ID、review hash、builder input hash 非空且指向不可变内容。
- Builder input 直接读取 run 冻结的完整载荷；不得在发送时通过 `planningContext`、session 当前 draft 或最新 build 再拼装。记录实际发送载荷的规范化 hash 并与冻结 `builder_input_hash` 比较。
- 完成事件、build row、artifact 和 version 都保存相同 run/snapshot 标识；成功配置必须能追溯当时用户看到的 `review_spec` 与实际 Builder 输入。
- retry 明确区分“同幂等请求”与“失败后创建的新 run”；后者复用 review snapshot，但有自己的完整 Builder 载荷/hash。
- 错误恢复不能自动退回旧 confirmed spec 启动生成；`RunChange`/改单/旧恢复路径不得绕过唯一 confirm admission。

## Documentation

同步更新：

- `docs/api/openapi.yaml`。
- `docs/tech/产品API与会话状态机.md`。
- `docs/tech/服务与状态.md`。
- `docs/tech/自主规划流程.md`。
- `docs/tech/系统架构.md`。
- `docs/product/PRD.md` 和路线图中的确认/生成状态说明。

## Verification

- 冻结 policy development/calibration 用例 100% 通过、admission veto 为零；`ui-contract` 中属于后端 DTO/预览/默认展示数据的断言通过，视觉与交互断言留给 Web change。Spec 3 的模型层 16 项 verdict 不因本 change 的零模型检查自动重新认证。
- 服务层表驱动测试覆盖状态组合、409、幂等、失败重试和恢复原值。
- 持久化集成测试证明 snapshot/run 原子关系、完整发送载荷 hash 与快照一致，并通过可控成功 Builder 产物验证 V5；不能仅断言 run admission。
- 并发测试证明两个确认请求只产生一个 run。
- 运行中编辑测试证明 draft 改变、无第二个 AgentRun、phase/Builder payload/hash 不变，完成后正确落在 current/outdated/failed。
- 测试覆盖同 key 乱序重放仍返回原快照、相同 revision 但默认变化导致 hash 冲突、旧 `RunChange`/预算与显卡改单/`ChangeRequest` 均不能直达 Builder，以及失败后修改草稿不得以旧快照重试。
- 本 change 默认只跑零模型评估和必要的数据库集成；不跑 live/holdout，不以模型层旧分数声称本 change 的全局发布 gate 已过。
- `go test ./internal/product ./internal/store ./internal/producthttp` 及相关包。
- 使用真实临时数据库执行受影响工作流测试。
- `go test ./... && go vet ./...`。

## Acceptance scenarios

1. 最后一个必填字段补齐后 ready，但没有 build run。
2. 用户聊天说“开始吧”只产生 review presentation action。
3. 用户在核定面板确认后才创建 snapshot 和 Builder run。
4. 面板打开后 draft revision 改变，旧确认提交失败且不启动 Builder。
5. 面板打开后 revision 未变但默认/配置范围改变，旧 review hash 被拒绝且不启动 Builder。
6. Builder 运行中侧栏编辑成功，不创建第二个 AgentRun，第二次确认被拒绝。
7. 当前 Builder 完成后配置可见；若草稿已改，状态为 outdated。
8. Builder 失败后 confirmed 保持；草稿未变可用同一 snapshot 重试，已变则先重新核定。
9. 草稿改回原确认值后，confirmation/build relation 恢复 confirmed/current。
10. 聊天直接请求改单/生成只修改草稿或提示核定；无 `RunChange`、`ChangeRequest` 或恢复旁路直接发 Builder。

## Completion checklist

- [x] 三轴状态由后端唯一计算并进入 OpenAPI。
- [x] chat/model/phase 均不能绕过 confirm API。
- [x] confirm-and-start 原子、幂等且校验 revision/review hash/readiness；重放返回原快照。
- [x] Builder 永远使用 run 绑定的完整冻结载荷，发送 hash 与冻结 hash 相等。
- [x] 运行中可编辑草稿但不可启动第二个 Builder。
- [x] 旧配置在修改后保留并正确标记。
- [x] `RunChange`、确定性改单、`ChangeRequest` 和恢复路径不能直达 Builder。
- [x] policy evaluator 100%、后端 `ui-contract` 断言通过，所有 admission veto 为零。
- [x] 未实现 Web 视觉、Jev 或外设能力。

## Completion notes (2026-09-23)

- 确认事务冻结 `requirement_confirmations` 不可变快照与 run 的完整 Builder 载荷；载荷内 `effective_constraints` 冻结完整核定预览(展开系统默认 + 来源清单)，Builder 的模型输入与确定性门槛只读冻结值，`Runner.Run` 入口严格校验、损坏即拒绝(模型调用数为 0)，原始 `requirement_state` 仅保留用户事实与溯源。失败重试整体继承目标 run 的冻结载荷与有效约束，仅替换 run 标识；预算弹性等默认与核定预览共用 `schemas.DefaultBudgetFlex` 同一来源。
- 三轴派生、核定预览 hash(展开默认)与稳定 admission reason code 进入 Session DTO/OpenAPI；旧单值 `requirement_status`/`missing_fields`/`confirmed_requirement*` 删除；`RunChange`/确定性改单/`ChangeRequest` 旁路关闭。评估对齐 `reqv2-grader-v4`(ui-contract 金标按冻结流程修订并重冻结 manifest)。
- 验证口径：零模型评估(确定性层 100%、admission veto=0)+ 真实 PG 集成测试 + `go test ./... && go vet ./...`；真实模型层与 holdout 留待发布认证(requirement-v2-release-certification)。
- 前端类型与视觉消费新契约属 `requirement-workspace-sidebar-v2`(Spec 5)的预定工作；本次 Session DTO 删除旧字段造成的 web 类型漂移在该 change 内解决。
