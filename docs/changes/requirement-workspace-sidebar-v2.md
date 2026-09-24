---
status: done
created: 2026-09-22
completed: 2026-09-24
---

# Change: Requirement workspace sidebar v2

## Dependency

依赖 `requirement-state-readiness-v2`、`screening-requirement-collection-v2` 和 `requirement-confirmation-builder-gate-v2` 的 OpenAPI 合同。视觉实现必须同步修订根目录 `DESIGN.md`、`DESIGN_CONTEXT.md`、`UI_RULES.md` 和 `docs/tech/Web客户端.md`，因为当前文档明确规定右栏只显示配置，与本 change 的已确认产品方向冲突。

Spec 4 已提供三轴、`review_spec/review_hash` 与确认入口，但当前 Session 读取合同尚未提供失败重试的目标 run ID，也未提供“当前有效预览相对上次确认快照”的字段差异。本 change 允许仅为这两处增加后端只读派生字段、OpenAPI 和聚焦测试；不改变确认门禁、Reducer 或 Builder 执行。前端不得从消息、phase、build 版本或原始 JSON 自行推断它们。

## Outcome

把桌面右侧共享区域改为可切换的“需求状态 / 配置详情”。需求状态始终可达且作为默认 Tab，让用户直观看见当前已确定、未填写、冲突、撤销和系统默认的需求；配置详情在首个 Builder 版本生成后才可进入。字段可由对话或侧栏手动修改，两条路径共享同一后端 RequirementState 和 Reducer。

核定面板展示服务端 `review_spec` 中所有影响 Builder 选型的有效需求与默认值；run ID 等执行元数据不属于用户需求。保存修改和确认启动是两个明确动作。前端不实现 readiness、默认值、确认比较或 Builder admission 规则。

## Boundaries

### In

- 桌面右栏两个顶层 Tab 及所有状态。
- 需求状态分组、字段行、缺失/冲突/默认来源展示。
- 简单字段行内编辑、复杂字段完整抽屉。
- 核定面板、保存、确认并开始配置。
- 配置 Tab 的 disabled、running、current、outdated、failed 表达。
- 聊天顶部摘要去重。
- 响应式、键盘、中文输入法、焦点恢复和无障碍。
- TanStack Query、SSE invalidation 和 OpenAPI 生成类型接入。
- 为失败重试目标与重新核定差异补足最小后端只读 DTO；差异由当前 `review_spec` 与最近确认快照的规范化有效值计算，缺少可投影预览时返回空/不可用，不编造比较结果。
- 设计系统和 Web 客户端文档更新。

### Out

- 不在 Next.js 实现字段依赖、readiness、hash 或生命周期比较。
- 不修改 Screening prompt、Builder 或报价/校验规则。
- 不实现显示器和外设。
- 不增加新颜色、字体、圆角体系或 dashboard 卡片墙。
- 不展示隐藏 reasoning、内部 state key、tool 参数或 A2A 细节。

## Design decision

现有三栏宽度和可调分隔线保持不变。右栏从“只显示 build inspector”调整为一个共享 inspector：

```text
┌─────────────────────────────┐
│ [需求状态] [配置详情]        │
├─────────────────────────────┤
│ 当前 Tab 的内容              │
└─────────────────────────────┘
```

- 每次进入会话默认选择“需求状态”。
- 用户在当前会话主动切换后，切换状态只作为瞬时 UI 状态保存；不写服务器业务状态。
- Builder 完成后只启用配置 Tab并给出可感知的完成提示，不强制抢走用户当前焦点或自动切换。
- `version_count=0` 时配置 Tab disabled，旁边说明“生成配置后可查看”。disabled 必须真实不可操作并可由屏幕阅读器理解。
- 有配置后，无论需求是否修改，配置 Tab 始终可查看。
- 会话切换或重新进入时重置到需求 Tab；当前 `SessionWorkspace` 默认选中 build、`refreshBuilds` 成功后自动切 build 的旧行为须移除。只有用户显式切换才改变当前会话的瞬时 Tab。

## Requirement status tab

### Header

头部只展示一个主要状态和一个直接下一步：

```text
需求状态
还缺 2 项 / 可以核定 / 已确认 / 已修改
```

辅助显示配置范围“主机（当前支持）”和当前字段完成度。不能用大面积语义色背景；使用图标、文字和克制的状态 token。

### Sections

按 section、divider 和 row 组织，不为每个字段套卡片：

1. 配置范围
   - 主机（当前支持，system default）。
2. 核心需求
   - 预算。
   - 主要用途。
   - 主机配件全部新买或复用。
3. 用途需求
   - 主要游戏、软件或任务。
   - gaming 分辨率。
   - performance goal。
   - 具体 FPS。
4. 复用配件
   - 仅 existing parts 非空时展开；型号和预算口径。
5. 可选偏好
   - 预算弹性、品牌、静音、尺寸、外观、装机对象、备注；默认可折叠，但冲突/错误不能藏在折叠区。
6. 未解决原话
   - observations 和未采用的 soft conflicts；说明为何未进入本次配置。

### Field states

| Backend truth | Visible copy |
|---|---|
| required unknown | 待填写 |
| optional unknown | 未指定 |
| active | 当前值 |
| active any | 不限（用户已确认） |
| conflict | 需确认 |
| removed | 已撤销，当前未指定 |
| system default | 实际值＋“系统默认” |
| unsupported observation | 当前版本不支持，未纳入配置 |

状态不得只靠颜色。来源“来自对话 / 手动修改 / 系统默认”可以作为克制 metadata 展示，默认不显示长 quote；用户展开字段详情时可查看对应原话。

### Budget display

预算行同时展示：

```text
预算：7500 元
弹性：最多上浮 10%（系统默认）
最高预算：8250 元
```

金额使用 tabular nums。预算和上限来自后端 effective projection，前端不得自行套 10% 规则。
`budget_cny` 为 must 时称“最高预算”；为 prefer 时只能称“预算参考上沿”，不得把软偏好写成硬上限。文案可依后端字段 strength 呈现，不在前端计算金额或改变门禁。

## Editing

### Inline editor

预算、用途、分辨率、performance goal、品牌、静音、尺寸等简单字段点击行后在右栏内展开有 label 的编辑器：

- 显式“保存 / 取消”。
- 未保存值只存在 React Hook Form，不写 Query cache 真值。
- 保存调用 `PATCH requirement-state`，带 `expected_revision` 和幂等键。
- 成功后以服务器返回的完整 Session 替换缓存；不能用 optimistic merge 猜业务结果。
- 409 时保留用户输入，重新获取最新值，提示“需求已更新，请核对后重试”，不能覆盖较新 revision。

### Full requirement drawer

复杂的复用配件型号、多个字段批量编辑和“编辑全部”打开最大宽 672px 的需求抽屉。抽屉仍使用相同 operations API，不恢复旧的完整 RequirementSpec 替换路径作为主要入口。

保存和确认必须分开。字段错误紧邻字段，并有可聚焦错误摘要链接到对应控件。

## Primary action

需求 Tab 底部保持一个主要操作：

| State | Action |
|---|---|
| incomplete | disabled“核对当前需求”；附近列出最高优先级缺失项 |
| ready + unconfirmed | “核对当前需求” |
| confirmed + current | 状态“需求已确认”；无重复 primary CTA |
| modified | “重新核定并生成” |
| build running | 允许编辑；确认/生成 disabled，并说明当前生成基于哪版需求 |
| build failed + confirmed | “按相同需求重新生成” |

用户聊天请求 review/build 后收到 `open_requirement_review` presentation event 时打开核定面板；incomplete 时只聚焦需求 Tab 和第一项缺失字段。

## Confirmation surface

核定使用最大宽 672px 的 Drawer/Dialog；它是明确交互对象，可以使用单个 12px bordered panel，但内部仍以 section/row 为主。

### Contents

必须展示服务端预览中的全部有效选型约束，而不是只展示 active 用户字段：

- 核心：scope、预算、预算弹性、最高预算、购买/复用范围。
- 用途：type、titles、resolution、performance goal、具体 FPS。
- 偏好与约束：active 值、system defaults、optional unknown。
- 未指定项使用“未指定，本次不作为选型限制”。
- soft conflict 使用“存在歧义，本次未采用”。
- 当前只生成主机八件及后续修改需要重新核定的说明。
- modified 再核定时展示相对上一确认快照的字段级 diff。

不展示 schema version、revision、hash 或内部枚举；这些只存在于请求和可观测证据。

### Actions

- Secondary：“返回修改”。
- 编辑后：“保存修改”，成功并重新获取 readiness 后才能确认。
- 首次：“确认并开始配置”。
- modified：“确认修改并生成新版本”。
- failed retry：“按相同需求重新生成”。

确认请求携带打开/保存后最新的 `expected_revision` 与服务端预览给出的 `expected_review_hash`；失败重试另传服务端 `build_relation.retry_run_id` 指定的 `retry_of_run_id`，前端不从聊天消息寻找 run ID。后端只在最新 build run 已失败、确认仍有效且当前预览与其快照一致时给出 `retry_run_id`，否则为 null，前端无值时不显示重试 CTA。前端不自行计算 hash。409 时面板不关闭，保留未提交编辑，重新载入预览并提示用户需求或系统默认已变化，须重新核定。

同一次确认点击/网络重试复用同一 `Idempotency-Key` 与完全相同的请求体；409 后用户重新核定的提交使用新 key。不能每次网络重试生成新 key，也不能在旧请求未决时重复启动。

## Configuration tab

- 无版本：disabled，不显示大块空配置状态；需求 Tab 已提供下一步。
- running：如果已有旧版本，仍展示旧版本并标记当前正在基于确认快照生成；没有版本时配置 Tab 保持 disabled，生成进度留在聊天/需求 Tab，不伪造配置。
- current：沿用配置、校验、版本和 diff。
- outdated：顶部显示“此配置基于上一版需求”，提供返回需求 Tab 的明确操作。
- failed：保留旧配置；错误说明是否已保存、确认是否仍有效、如何重试。

现有八件顺序、十二条校验、快照日期、免责、版本和 diff 规则保持不变。

## Chat summary

聊天顶部不再重复完整需求字段，只保留：

- 当前阶段/三轴状态的可读摘要。
- 缺失项数量或“可以核定”。
- 打开右侧需求状态的操作。

桌面与右栏不得同时展示两份可编辑需求表。消息中的构建回复继续只摘要核心部件，完整详情留在配置 Tab。

## Responsive behavior

- ≥1024px：右栏常驻，两个 Tab 始终可达。
- 768–1023px：右侧共享 inspector 作为详情抽屉；从聊天顶部或完成事件打开。
- <768px：全宽详情抽屉，顶层为“需求 / 配置”，配置内部再进入校验/版本；提供明确“返回对话”。
- 375px 下可完成填写、保存、核定、查看配置和返回聊天。
- Drawer/Dialog 正确锁定并恢复焦点；presentation event 不抢夺正在输入或编辑字段的焦点，可改为非侵入提示由用户打开。

## Accessibility and copy

- Tab 使用正确 tablist/tab/tabpanel 语义，disabled 状态带原因。
- 所有字段 label 常驻，placeholder 不代替 label。
- 图标按钮有 accessible name 和 Tooltip。
- 触控目标至少 44×44px；桌面普通控件 36–40px。
- 中文 IME composition 期间 Enter 不提交。
- 动态状态更新只在完成点使用 polite aria-live，不逐 token 朗读。
- 使用“正在生成并校验配置”“数据不足”“基于上一版需求”等可验证文案。
- 禁止“AI 正在深度思考”“已经开始”但实际无 run 等文案。

## State management

- Session/Requirement/Build 仍由 TanStack Query 管理。
- Zustand 只保存当前 inspector Tab、mobile pane、局部 drawer 和未提交表单等瞬时 UI。
- 不在 localStorage 保存业务字段；现有栏宽偏好可继续保存。
- Screening 的 SSE `requirement.updated` 失效 Session；`presentation.action` 只驱动当前 run 的短期 UI 动作，不凭它推断业务状态或无故刷新 builds；`requirement.confirmed`/run/build 事件按合同精确失效 Session/builds。刷新后不重放 presentation action。侧栏 `PATCH requirement-state` 以返回的完整 Session 更新 Query cache，不能等待一个并不存在的编辑 run/SSE；同步修正 `docs/api/SSE事件协议.md` 中对侧栏编辑会创建 run/发送 `requirement.updated` 的过期说法。
- 前端不得根据消息文本、phase 或 version count 重建 readiness/confirmation。

只读 DTO 补充：`build_relation.retry_run_id: string|null` 由后端派生失败重试资格；`requirement_confirmation.review_diff` 是后端按规范化有效字段生成的 `{field, before, after}` 列表，只在有可比较的当前预览时提供。前端只把字段和值映射为用户可读标签，不自行比较 JSON；新字段须进入 OpenAPI 与生成类型并有服务端正反例测试。

## Design documentation changes

本 change 必须显式更新以下旧规则：

- `DESIGN.md` Responsive model 中“desktop build inspector contains only build...”改为共享 requirement/build inspector。
- `DESIGN_CONTEXT.md` Information density 与 Layout principles 改为需求状态默认常驻右栏。
- `UI_RULES.md` §3 删除“配置栏仅保留配置”，加入双 Tab、状态行和核定抽屉规则。
- `docs/tech/Web客户端.md` 页面结构、主流程、状态管理、测试要求同步。
- `docs/api/SSE事件协议.md` 同步当前无 run 的侧栏编辑行为与 `presentation.action`/`requirement.confirmed` 消费边界；不保留 v1 确认与旧 `requirement_status` 叙述。

保留其余设计约束：克制色彩、hairline、rows/dividers、一个 primary action、无卡片墙、无新 token。

## Verification

验收范围调整（2026-09-24，人工验收前）：本轮以桌面 Web 核心流程为交付范围。375/768 视口、触控、中文输入法与完整 axe 扫描保留为后续诊断与优化，不把未运行项目记为通过；Spec 6 仍需如实记录这些覆盖缺口。视觉信息层次和 AI 回复措辞的精修为已知优化项，除非造成错误承诺、误导确认或任务失败，否则不作为本 change 的阻断项。

- Vitest/Testing Library 覆盖所有字段状态、CTA 状态、Tab disabled、409、running edit 和 stale build。
- Playwright 以 1440px 桌面覆盖新会话渐进收集、手动填写、核定、生成、修改后旧配置与 running edit；失败重试及浏览器→API→数据库完整链路由 Spec 6 对最终候选继续验证。mock/离线 harness 不调用真实模型。
- 375/768 视口与触控为诊断覆盖，未完成时如实列限制，不作为本 change 人工验收通过项。
- 桌面键盘与焦点路径、axe、中文输入法、reduced motion、断网、degraded 和 build failure 的剩余场景进入 Spec 6 诊断/认证，不以未运行结果充作通过。
- 本 change 已运行的 typecheck、lint、vitest 与桌面聚焦 Playwright 结果见下方记录；全量 `pnpm test:e2e` / `pnpm test:e2e:requirements`、live E2E 和 holdout 留给 Spec 6 按最终候选重验。
- API 生成类型无漂移；前端源码中不存在最低矩阵和 10% 预算计算的复制实现。
- 零模型 `ui-contract` 回归无 veto；它验证后端读取合同，不能代替真实浏览器的视觉、焦点和交互验收。

### 返工验收记录（2026-09-24，针对提交 5a9a28b 的四处定向返工）

状态（2026-09-24 提交时）：**人工验收待完成**——自动化验证结果见下，真实浏览器交互、视觉与窄屏体验由人工验收；离线 requirements harness 503 接线问题未解决（单列见下）；本记录不将 Spec 5 标记为 done。

返工内容：① 完整需求抽屉只提交用户实际改动（diff 基线为打开时的有效值投影，系统默认预填不再被写成 active）；预算留空可单独保存其他字段；预算/帧率/预算弹性按后端 schemas 口径做前端数字校验；保存失败保留输入并展示错误。② Builder 运行期间行内编辑与完整抽屉保持可保存（busy 只锁定网络提交瞬间），再次确认启动仍由 PrimaryAction running 分支与后端 `ErrSessionBusy` admission 双重禁止。③ 需求状态栏对存在有效系统默认的 unknown/removed 字段显示"系统默认"（真实状态所有已知字段都以 unknown 键存在，默认判定必须先于 unknown 兜底；撤销墓碑不清除默认）；`configuration_scope` 不再作为字段行渲染。④ `presentation.action` 的 `open_requirement_review` 直接按服务端动作执行，不再用会话缓存 readiness 复核（同轮补齐最后条件并请求开始时缓存仍为旧值，会把动作降级丢失）。顺带：核定/编辑抽屉不再重复一级标题，默认右上角 X 关闭按钮移除，不再与"返回修改/关闭"重叠。

本次返工已运行的验证（返工最终代码上全部通过）：

- Go 聚焦测试：`go vet` + `go test ./internal/product/... ./internal/schemas/... ./internal/producthttp/...`（另 `./internal/store/...` 通过），含新增 `TestPresentationActionSurvivesSameTurnCompletion`（同轮补齐最后条件 + 请求开始必须发出 open_requirement_review；未请求开始不得发出）。
- Web 单测：`pnpm typecheck` 通过；`pnpm lint` 0 错误（存量 react-hooks 警告 5 条，非本次引入）；`pnpm exec vitest run` 77/77。新增 `requirement-full-editor.test.tsx` 8 例（只提交实际改动/预算留空可保存/数字校验口径/失败保留输入）与 `requirement-status.test.tsx` 系统默认展示 3 例，均先在修复前代码上确认失败再修复。
- 1440px 桌面核心流程：`pnpm exec playwright test e2e/requirement-workspace.spec.ts --project=desktop`（mock 路由离线回归，4 条聚焦用例：批量编辑最小 diff、系统默认展示、运行中可编辑不可再次确认、presentation.action 不因缓存滞后丢失），4/4 通过；四条用例均在修复前代码上确认失败。
- 真实 API 的进程内等价集成测试：`TestRequirementStatePersistentWorkflow`、`TestVideoRequirementConfirmationReplay`（PG_TEST_DSN 指向本地 compose PostgreSQL），确认→生成→版本保存链路通过。

### 返工验收记录（2026-09-24，第二轮定向返工）

第二轮修复两件事：

- 完整需求编辑器输入丢失：编辑基线在表单打开时一次性固定（状态快照 + 有效值预填），父组件重渲染、Session 轮询刷新或 defaults 新建 Map 不再触发 `form.reset`，未保存输入不丢失；保存 diff 始终按打开时的基线计算。保存遇 409 `requirement_revision_conflict` 保留输入并明示冲突（`problem.ts` 补冲突文案），不静默按新 revision 覆盖。为此把后端实际发出的全部 problem code 补齐进 OpenAPI `Problem.code` 枚举（此前 `requirement_revision_conflict` 等 13 个码后端已发出但枚举未登记），并重新生成前端类型。
- 本地 Web 端口统一为 3101：`web/package.json` dev 脚本固定 `--port 3101`（单一出处），四个 Playwright 配置的 baseURL 默认、mock webServer、`PUBLIC_WEB_BASE_URL` 与 `web/src/lib/api/server.ts` 回退值、`cmd/api` 与 `producthttp` 默认公共 Web 地址、离线 harness 的 `REQUIREMENT_BROWSER_WEB_URL` 默认值（3102→3101）、compose 本地 Auth 回调地址（GOTRUE_SITE_URL/ALLOW_LIST）、`.env.example` 与相关文档（ops 部署文档、产品API文档、README、CLAUDE.md）全部对齐。环境变量覆盖能力保留；后端 API 端口（8082）与 mock API（18082）不变。

第二轮验证（返工最终代码上全部通过）：

- Web：`pnpm typecheck`、`pnpm lint`（0 错误，存量警告不变）、`pnpm exec vitest run` 79/79（新增编辑器"父组件刷新不丢输入"与"409 冲突保留输入"两例，均在第一轮实现上确认失败）。
- Go：`go build ./...`、`go vet`、`go test ./internal/producthttp/... ./internal/sharing/...` 通过（producthttp 覆盖 problem 映射与来源检查）。
- 一条 1440px 桌面浏览器链路：`pnpm exec playwright test e2e/requirement-workspace.spec.ts --project=desktop` 4/4，同时验证 3101 端口接线（dev 服务器、baseURL、PUBLIC_WEB_BASE_URL、mock API 18082）。

第二轮仍未解决/未运行：离线 requirements harness 的 503 接线问题独立存在（见下）；移动端/平板、ui-contract、live、holdout、键盘/IME/axe 维持第一轮记录的延期状态。

### 返工验收记录（2026-09-24，第三轮聚焦修正）

- 完整编辑器的保存请求改为携带打开时的基线 revision：`onSave(operations, expectedRevision)` 由 `updateRequirement` 转发到 PATCH `expected_revision`；编辑期间 Session 被并发刷新（轮询/SSE）时保存按旧 revision 提交，由服务端 409 暴露冲突，不再拿最新 revision 静默覆盖。行内编辑不传该参数，继续使用当前缓存 revision。请求级验证：mock e2e 批量编辑用例在编辑期间经真实运行轮询把缓存刷到 revision 2，断言 PATCH `expected_revision` 仍为打开时的 1；单测断言 `onSave` 收到基线 revision 而非刷新后的值。
- `web/package.json` 的 `start` 脚本补 `--port 3101`，本地生产模式启动与 dev/Playwright/harness 地址一致。
- 验证：`pnpm typecheck`、`pnpm exec vitest run` 79/79、`playwright test e2e/requirement-workspace.spec.ts --project=desktop` 4/4（含请求级 revision 断言，修正前接线确认失败）。Go 侧本轮无改动。

### 历史阻塞：离线 requirements harness 确认→生成 503（已于 2026-09-24 修复）

main 上已提交的 `TestRequirementStateBrowserServer`（internal/producthttp/requirement_integration_test.go）以非 planning 接线启动：`requirementReplayGateway.Remote` 按裸 RequirementSpec 解码，而产品 API 的 Builder 载荷是 PlanningInput 形状，任何确认→生成在该 harness 下确定性 503（`upstream_unavailable`）。已用进程内等价复现定位（同一 `requirementIntegrationAPI(t)` 构造 + gaming 流程确认即复现）；planning 模式进程内集成测试（`TestRequirementStatePersistentWorkflow`、`TestVideoRequirementConfirmationReplay`）通过，说明是 harness 接线缺口而非产品路径缺陷。修复方向（如浏览器服务器改用 planning 网关或让裸网关接受 PlanningInput）与完整 `pnpm test:e2e:requirements` 重跑另行安排，不随本返工关闭。

后续独立测试设施修复 `c12b246` 将浏览器入口接至既有 planning 网关，新增 HTTP→API→数据库持久化回归；PG 聚焦测试通过，隔离端口桌面 requirements 浏览器核心链路通过（零真实模型）。上段及前述“未解决”是各轮返工**当时**的状态，不再是当前阻塞；三视口全套仍未运行，不记为通过。

第一轮返工未运行/延期的验证（不得记为通过）：

- 移动端（375）与平板（768）E2E 未在返工最终代码上运行；窄屏与触控交互的人工验收延期。
- mock 全量 `pnpm test:e2e`（三视口 37 通过、2 存量跳过）运行于返工中期版本（早于编辑器错误文案的一处小调整），未在最终代码上重跑全量；最终代码仅重跑了上述桌面核心流程。
- 离线 requirements harness（`pnpm test:e2e:requirements`）未完成：首次全量尝试因 harness 的 `REQUIREMENT_BROWSER_WEB_URL` 与 Web 端口不一致被跨域来源检查全部拒绝；修正来源后单条桌面核心流程复现出更深一层问题——main 上已提交的 `TestRequirementStateBrowserServer` 以非 planning 接线启动，任何确认→生成都确定性 503（`requirementReplayGateway.Remote` 收到 PlanningInput 形状载荷；已用进程内等价复现定位）。该缺口先于本次返工存在（返工未改 product/producthttp 源码），planning 模式进程内集成测试通过；harness 接线修复与完整套件重跑另行安排。
- `ui-contract` 零模型回归、live E2E、holdout 未运行，留给发布认证。
- 键盘/IME/axe 无障碍扫描未在返工最终代码上重跑（上次全量结果不覆盖本次改动）。

## Acceptance scenarios

1. 新会话默认显示需求状态，配置 Tab disabled。
2. 首句 FPS 游戏后，右栏显示已记录用途/游戏/高帧率和三个缺失项，不显示核定可用。
3. 对话和手动编辑相继更新同一状态，不互相覆盖。
4. 最低条件齐全后 CTA 可用，但面板不自动弹出。
5. 用户说“开始吧”后打开核定面板；点击确认才启动。
6. Builder 完成后配置 Tab 启用但不强制切换。
7. 修改需求后旧配置仍可看并标记“基于上一版需求”。
8. Builder 运行中可保存需求修改，不能启动第二个任务。

## Completion checklist

- [x] 右栏共享双 Tab，需求默认、配置按版本启用。
- [x] 所有字段状态和 system defaults 诚实展示。
- [x] 保存、核定、生成动作边界清晰。
- [x] 失败重试使用后端给出的目标 run ID；重新核定差异由后端只读字段提供，前端不猜。
- [x] Next.js 不包含业务判定或 hash 比较。
- [x] 设计系统四份相关文档已同步，无相互矛盾旧规则。
- [x] 桌面核心流程经用户人工验收（2026-09-24）；移动/平板、IME 与完整无障碍扫描按上述范围调整延期，不宣称通过。
- [x] UI contract 零模型评估 3/3、veto 0（Spec 6 预认证）；浏览器 harness 桌面核心链路随后由独立修复跑通。
- [x] 未实现显示器、键鼠或 Jev。

验收结论：用户认为桌面功能整体可接受；显示清晰度与 AI 交互生硬列为后续优化，不改写本次冻结评估门槛或已观察到的模型失败。正式发布判断仍由 Spec 6 独立给出。
