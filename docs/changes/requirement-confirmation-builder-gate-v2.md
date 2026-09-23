---
status: proposed
created: 2026-09-22
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
- 原子 confirm-and-start API、幂等与并发控制。
- 在 Screening v2 已有的短期 presentation action 入口上扩展 confirmation/build 三轴 Policy；不另建平行决策器。
- 生成期间草稿编辑、旧配置 retained/outdated 行为。
- Builder 失败、重试和相同快照复用。
- Session DTO、SSE 事件、存储和相关文档更新。

### Out

- 不实现右侧栏视觉和表单；前端 change 消费本合同。
- 不改变字段提取和最低矩阵。
- 不实现并行 Builder、队列或自动取消。
- 不实现显示器和外设。
- 不接入 Jev。

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

典型组合必须可表达：

| Scenario | Readiness | Confirmation | Build |
|---|---|---|---|
| 新会话信息不足 | incomplete | unconfirmed | none |
| 最低条件齐全 | ready | unconfirmed | none |
| 已确认并生成中 | ready | confirmed | running |
| 生成完成 | ready | confirmed | current |
| 修改可选偏好 | ready | modified | outdated |
| 删除预算 | incomplete | modified | outdated |
| 修改后恢复为确认值 | ready | confirmed | current |
| Builder 失败 | ready | confirmed | failed |

## Confirmation snapshot

用户确认时冻结：

```text
confirmed_requirement_spec
confirmed_requirement_state_or_evidence_ref
confirmed_requirement_hash
confirmed_revision
confirmed_at
configuration_scope
schema_version
```

- hash 基于规范化、展开 system defaults 后的 RequirementSpec；JSON key/order 差异不能改变 hash。
- 快照不可修改；再次确认产生新快照或新的确认记录，不覆盖历史 build 所引用内容。
- 每个 build/run 永久记录其 snapshot hash/ID。
- 系统默认更新不能改写旧快照。
- 草稿改回与当前确认快照完全相同的规范化 spec 时，可自动恢复 confirmed；不因 revision/history 不同强迫重复确认。

## Policy

Policy 输入：Readiness、Confirmation、Build、当前 turn signals 和事件来源。输出：allowed actions 与可选 presentation action。

| Condition | User signal/event | Result |
|---|---|---|
| incomplete | requests review/build | `focus_missing_requirement`；不启动 Builder |
| ready + unconfirmed/modified | 普通讨论 | 无自动确认动作 |
| ready + unconfirmed/modified | requests review/build | `open_requirement_review` |
| ready | confirm API submit | 校验后冻结并启动 |
| confirmed | retry after failed | 使用同一 snapshot 重试 |
| running | 任意确认/生成请求 | 拒绝第二个 Builder；草稿编辑仍可保存 |

聊天中的“就这样”“开始吧”“帮我配”永远不是 confirm API 的替代品。它只能产生 presentation action，让用户看见最终快照并执行明确操作。

Presentation action 属于当前 run/turn 的短期结果或 SSE 事件，不写进 RequirementState。建议事件：

```text
requirement.review_requested
requirement.missing_focused
```

刷新后无需自动重放打开动作；右侧需求状态始终可见，避免持久化 UI 命令。

## Confirm API

更新现有端点为显式 optimistic concurrency：

```http
POST /api/v1/sessions/{session_id}/requirement/confirm
Idempotency-Key: <key>
Content-Type: application/json

{
  "schema_version": 2,
  "expected_revision": 7
}
```

服务端在同一事务/原子存储边界执行：

1. 校验 owner、session、幂等键和没有 active Builder。
2. 读取最新 RequirementState，revision 必须匹配。
3. 重新计算 Readiness；不信任客户端 `eligible`、missing 或 snapshot。
4. 规范化投影并展开 system defaults。
5. 冻结 snapshot 和 hash。
6. 创建唯一 build run，绑定 snapshot ID/hash。
7. 发布确认与 run.started 事件。

失败语义：

- revision 变化：409，需求未确认、Builder 未启动。
- readiness 不完整：409/422 稳定 reason code，返回最新 missing/conflicts。
- active Builder：409，说明当前任务仍在运行。
- 同一幂等键重试：返回原 run，不创建第二个。
- 事务失败：不能留下只有确认没有 run，或只有 run 没有快照的半状态。

## Editing while Builder runs

- `PATCH requirement-state` 在 build run 期间允许保存草稿编辑，不再因“会话正在执行任务”统一 409。
- 编辑继续要求 `expected_revision`，只修改 draft RequirementState。
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
- Builder 失败不取消 confirmed；可按相同 snapshot 和新幂等键重试。
- 如果用户在 run 期间修改草稿，run 成功后其产物立即是 outdated，相对它自己的 snapshot 仍是有效历史版本。

## Session API

Session DTO 增加稳定、后端计算的字段；具体嵌套可按 OpenAPI 风格调整：

```yaml
requirement_readiness:
  status: incomplete|ready
  missing_fields: []
  blocking_conflicts: []
  confirmation_eligible: boolean
  effective_defaults: []
requirement_confirmation:
  status: unconfirmed|confirmed|modified
  confirmed_revision: integer|null
  confirmed_at: datetime|null
  confirmed_hash: string|null
build_relation:
  status: none|running|current|outdated|failed
  version: integer|null
  requirement_hash: string|null
```

旧的单值 `requirement_status` 和裸 `missing_fields` 在整体迁移中删除，不保留双轨。Next.js 只渲染这些真值，不重新比较 JSON 或推断状态。

## Run and storage invariants

- 同一 session 同时最多一个非终态 Builder run。
- build run 创建时 snapshot ID/hash 非空且指向不可变内容。
- Builder input 从 run 绑定 snapshot 构造，不能从 session 当前 draft 构造。
- 完成事件、build row、artifact 和 version 都保存相同 run/snapshot 标识。
- retry 明确区分“同幂等请求”与“失败后创建的新 run”。
- 错误恢复不能自动退回旧 confirmed spec 启动生成。

## Documentation

同步更新：

- `docs/api/openapi.yaml`。
- `docs/tech/产品API与会话状态机.md`。
- `docs/tech/服务与状态.md`。
- `docs/tech/自主规划流程.md`。
- `docs/tech/系统架构.md`。
- `docs/product/PRD.md` 和路线图中的确认/生成状态说明。

## Verification

- evaluation foundation 的 policy 和 conversation fixture 全部通过。
- 服务层表驱动测试覆盖状态组合、409、幂等、失败重试和恢复原值。
- 持久化集成测试证明 snapshot/run 原子关系及 Builder 输入来源。
- 并发测试证明两个确认请求只产生一个 run。
- 运行中编辑测试证明 draft 改变而 Builder payload/hash 不变。
- `go test ./internal/product ./internal/store ./internal/producthttp` 及相关包。
- 使用真实临时数据库执行受影响工作流测试。
- `go test ./... && go vet ./...`。

## Acceptance scenarios

1. 最后一个必填字段补齐后 ready，但没有 build run。
2. 用户聊天说“开始吧”只产生 review presentation action。
3. 用户在核定面板确认后才创建 snapshot 和 Builder run。
4. 面板打开后 draft revision 改变，旧确认提交失败且不启动 Builder。
5. Builder 运行中侧栏编辑成功，第二次确认被拒绝。
6. 当前 Builder 完成后配置可见；若草稿已改，状态为 outdated。
7. Builder 失败后 confirmed 保持，可用同一 snapshot 重试。
8. 草稿改回原确认值后，confirmation/build relation 恢复 confirmed/current。

## Completion checklist

- [ ] 三轴状态由后端唯一计算并进入 OpenAPI。
- [ ] chat/model/phase 均不能绕过 confirm API。
- [ ] confirm-and-start 原子、幂等且校验 revision/readiness。
- [ ] Builder 永远使用绑定快照。
- [ ] 运行中可编辑草稿但不可启动第二个 Builder。
- [ ] 旧配置在修改后保留并正确标记。
- [ ] policy evaluator 100% 且所有 admission veto 为零。
- [ ] 未实现 Web 视觉、Jev 或外设能力。
