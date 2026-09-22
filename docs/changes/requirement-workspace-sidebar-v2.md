---
status: proposed
created: 2026-09-22
---

# Change: Requirement workspace sidebar v2

## Dependency

依赖 `requirement-state-readiness-v2`、`screening-requirement-collection-v2` 和 `requirement-confirmation-builder-gate-v2` 的 OpenAPI 合同。视觉实现必须同步修订根目录 `DESIGN.md`、`DESIGN_CONTEXT.md`、`UI_RULES.md` 和 `docs/tech/Web客户端.md`，因为当前文档明确规定右栏只显示配置，与本 change 的已确认产品方向冲突。

## Outcome

把桌面右侧共享区域改为可切换的“需求状态 / 配置详情”。需求状态始终可达且作为默认 Tab，让用户直观看见当前已确定、未填写、冲突、撤销和系统默认的需求；配置详情在首个 Builder 版本生成后才可进入。字段可由对话或侧栏手动修改，两条路径共享同一后端 RequirementState 和 Reducer。

核定面板展示 Builder 将实际使用的完整快照；保存修改和确认启动是两个明确动作。前端不实现 readiness、默认值、确认比较或 Builder admission 规则。

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

必须展示 Builder 将使用的规范化快照，而不是只展示 active 用户字段：

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

确认请求携带打开/保存后最新的 `expected_revision`。409 时面板不关闭，重新载入并提示用户需求已经变化。

## Configuration tab

- 无版本：disabled，不显示大块空配置状态；需求 Tab 已提供下一步。
- running：如果已有旧版本，仍展示旧版本并标记当前正在基于确认快照生成；没有版本时 Tab 保持 disabled 或展示稳定进度入口，按最终现有 inspector 结构选择一种，不伪造配置。
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
- SSE `requirement.updated` 失效 Session；confirm/run/build 事件按 OpenAPI 精确失效 Session 和 builds。
- 前端不得根据消息文本、phase 或 version count 重建 readiness/confirmation。

## Design documentation changes

本 change 必须显式更新以下旧规则：

- `DESIGN.md` Responsive model 中“desktop build inspector contains only build...”改为共享 requirement/build inspector。
- `DESIGN_CONTEXT.md` Information density 与 Layout principles 改为需求状态默认常驻右栏。
- `UI_RULES.md` §3 删除“配置栏仅保留配置”，加入双 Tab、状态行和核定抽屉规则。
- `docs/tech/Web客户端.md` 页面结构、主流程、状态管理、测试要求同步。

保留其余设计约束：克制色彩、hairline、rows/dividers、一个 primary action、无卡片墙、无新 token。

## Verification

- Vitest/Testing Library 覆盖所有字段状态、CTA 状态、Tab disabled、409、running edit 和 stale build。
- Playwright 覆盖：新会话渐进收集、手动填写、核定、生成、修改后旧配置、running edit、failed retry。
- 375/768/1440 三档视口。
- 键盘可完成 Tab、字段编辑、保存、核定和返回；axe 无 serious/critical。
- 中文输入法、reduced motion、断网、degraded、build failure 均有明确行为。
- `pnpm typecheck && pnpm lint && pnpm test`，以及项目现有 Playwright 命令。
- API 生成类型无漂移；前端源码中不存在最低矩阵和 10% 预算计算的复制实现。

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

- [ ] 右栏共享双 Tab，需求默认、配置按版本启用。
- [ ] 所有字段状态和 system defaults 诚实展示。
- [ ] 保存、核定、生成动作边界清晰。
- [ ] Next.js 不包含业务判定或 hash 比较。
- [ ] 设计系统四份相关文档已同步，无相互矛盾旧规则。
- [ ] 桌面、移动端、键盘、IME 和无障碍验收通过。
- [ ] UI contract evaluation 无 veto。
- [ ] 未实现显示器、键鼠或 Jev。

