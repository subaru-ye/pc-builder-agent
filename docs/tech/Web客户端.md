# Web客户端

> 本文维护 Next.js 工作台的组件、状态与交互边界。产品需求见[PRD](../product/PRD.md)，HTTP 契约见[OpenAPI](../api/openapi.yaml)，执行与确认规则见[自主规划流程](自主规划流程.md)。视觉精确规则以根目录 DESIGN.md、DESIGN_CONTEXT.md、UI_RULES.md 为准。

## 1. 定位与栈

一个克制、精确、偏深色的装机工作台，不做 Hero、统计卡片墙或营销区；只保留 Agent 进度、版本切换和 diff 高亮三类有意义动效，prefers-reduced-motion 下全部变为无位移即时切换。

- Next.js App Router + TypeScript strict + Tailwind CSS(必须用 CSS variables,不散落 hex)+ shadcn/ui(Radix 基础，按需安装)。
- TanStack Query 管服务端状态；Zustand 只管 mobilePane、inspectorTab、diffSelection 等瞬时 UI，不存 session、message、build 或 run 真值，不跨浏览器刷新持久化。
- React Hook Form + schema validator 用于 RequirementSpec 编辑，validator 必须从 OpenAPI/共享 schema 派生。
- Playwright 做 E2E,Vitest + Testing Library 做组件/纯函数测试。以 package.json 和 pnpm-lock.yaml 为唯一真值。

## 2. Server/Client 边界

| 页面/模块 | 渲染边界 | 原因 |
|---|---|---|
| 根 layout、静态元数据 | Server Component | 少 JS、统一主题；Geist 本地字体包，不请求 Google Fonts |
| /share/[token] | Server Component | 首屏、OG 和只读 SEO |
| 主工作台 shell | Server 可输出框架 | 快速首屏 |
| 聊天、composer、SSE、需求表单 | Client Component | 浏览器状态与交互 |
| 配置/版本查询 | TanStack Query Client | SSE 后精确失效与刷新 |
| 分享图 | Next ImageResponse | 纯展示，只消费 public DTO |

Next.js 不定义业务 Route Handler 或 Server Action；开发环境由 next.config rewrite 把 /api/:path* 转发到 Go API,生产由同源反向代理承担。

本机评估 `/eval` 入口：显式设置 `EVALDESK_API_BASE_URL` 后经 rewrite 连接独立 `cmd/evaldesk`,前后端均绑定 loopback,详见[评估设施](评估设施.md)。页面为 Requirement v2 单桌(历史评估 legacy 桌已移除),只读审阅 `artifacts/reqv2` 冻结证据(门槛三态、初筛 Screening 侧 / 选配 Builder 侧分层、逐题逐轮、同身份对比),Next.js 不做任何评分或比较计算。

评估工作台内部按六个独立工作区导航：Screening（默认）、Builder、评估集与题目、提示词迭代、测试记录、运行对比。`area` 保存分区，`panel` 保存运行详情页签；运行详情不再堆叠所有内容。桌面常驻导航，窄屏转双列入口；测试记录每页 12 条，默认隐藏归档。边界与证据缺口见[工作区改版](../changes/evaldesk-workspaces.md)。

Screening 按测试目的与实验批次组织运行：模型对比、修复验证、认证检查、程序回归、评分核对、消融与诊断；执行方式独立筛选。`test_purpose` / `test_mode` / `test_result` 保存目的、执行方式与所选结果。历史分组由 `web/src/lib/evaldesk/run-experiments.ts` 按已核对的准确目录名关联，未知记录保留为「未标注目的」，不按模型名或时间自动归因。Flash 修复验收首跑同时作为模型对比基线，分类总数按运行 ID 去重。顶部显示明确的所选测试结果，模型实验默认选择该批基线；不把最新候选当作当前方案表现。摘要新增 `failed_gates`，只投影冻结报告未通过/不可评估项，前端不重判；对比仍由既有接口核对冻结身份。

切换测试目的时重置执行方式及所选运行，避免旧的执行方式筛空新分类。显式冲突深链显示当前分类记录数与冲突条件，提供清除执行方式入口；无匹配时不渲染全空分层概览。

Builder 复用同一分类与实验视图，仅纳入 policy / ui-contract 有题量的运行，展示「选配准入 / 界面行为」分数。模型对比类标为「模型实验检查」，型号标为「初筛模型」，保持原实验目的与 Builder 检查范围的区别。全局冻结门槛标为「整次运行」，失败项折叠核对，不重判为 Builder 门槛。两侧均不截断相关记录；切换工作区重置测试目的、执行方式与所选结果，运行详情返回保留筛选。

两对象页的概览只保留结果、模型/批次、时间与范围；已标注运行不平铺目录名，相同范围集中显示，说明与原文默认折叠。Screening 的程序层用「需求更新 / 信息完整性」，Builder 用「选配准入 / 界面行为」并保留未覆盖配件选择质量的边界。正常证据备注和目录身份可在运行证据中核对，证据异常与失败原因仍在主要结果区显示。

评估集默认浏览完整题目（包括未参与测试的分组），提供分类、搜索、每页 12 条及输入/期望展开；迭代记录与关联测试独立成页签。`dataset_view` 等参数保存视图，`snapshot` 保留版本深链。完整题库由 Go 的 `requirement-v2/dataset` 只读接口按文件哈希校验后返回，前端只展示内容变化，不计算评分。

提示词默认展示 `requirement-v2/prompts` 提供的当前编译原文，按角色及组成部分切换，历史测试独立展示；`prompt_role` / `prompt_view` / `prompt_component` 保存阅读位置。当前编译原文不作为历史运行快照，源码修改后需重启 evaldesk 服务更新。

`requirement-v2/prompt-versions` 提供 Git 历史常量原文与经过指纹校验的运行原文，按角色选择版本并比较段落内字词增删；`prompt_version` / `prompt_base` 保存候选与对照版本，`prompt_read` / `prompt_section` 保存原文阅读方式与段落。Git 提交只是原文的代码来源，不冒充实际模型请求；新评估运行保存原文后可按内容版本查看关联测试。

`prompt_view=iterations` 显示内容迭代原因、具体调整、验证记录和依据。每版可选 `review` 来自 Go 对固定 `docs/eval/prompt-iterations.json` 的安全读取，仅以完整提交/角色绑定；验证类型区分真实模型记录、离线回放、仅有测试代码与未记录。当前组成部分仅在原文指纹一致时引用历史说明。记录提及的旧测试与保存原文的测试使用不同说明，不以提交身份代替运行原文或提示词单因素效果。

## 3. 页面与导航

### 3.1 /

- 品牌名 + 一句功能说明 + 直接可用的 composer;3 个真实示例 prompt 点击只填入不自动发送。
- 页面加载不预创建空会话；点击「新建对话」显式 POST session。
- 会话列表桌面常驻、窄屏从顶栏打开；每条会话「…」提供重命名、归档/取消归档和删除。删除前明确提示消息、需求、配置版本与分享一并移除，运行中会话拒绝删除。默认列表只含未归档会话。
- 会话管理以 Go API 为真值:`PATCH /sessions/{id}`(title 1–80 字符、archived)、`GET ?archived=true`、`DELETE`(204)。改名与归档不改需求快照或配置版本，不调用模型。

### 3.2 /s/[session_id]

桌面三栏：左 240px 会话列表(可调 200–360px),中对话，右 `clamp(360px, 34vw, 480px)` 共享 inspector(可调 280–640px,聊天至少保留 360px),顶层为「需求状态 / 配置详情」双 Tab。`ResizableWorkspace` 分隔线支持键盘 16px 微调、Home/End 到边界、双击恢复默认；宽度存 localStorage(仅为界面偏好)。

- 会话摘要常驻聊天顶部,只含阶段/三轴摘要、缺失数量或「可以核定」和打开需求状态的入口,不再重复需求字段表。需求状态是右栏默认 Tab:分组 rows 展示已确定、待填写、冲突、撤销和系统默认;配置详情在 `version_count=0` 时真实 disabled 并说明「生成配置后可查看」,生成后只启用不抢焦点、不自动切换;会话切换或重新进入重置到需求 Tab。
- 简单字段在需求状态 Tab 行内编辑(显式保存/取消,`PATCH requirement-state` 带 `expected_revision` 与幂等键,成功以返回的完整 Session 替换缓存;409 保留输入、重取真值提示重试);复杂与批量编辑打开最大宽 672px 需求抽屉,仍走同一 operations API。核定使用独立 672px Drawer/Dialog:展示服务端 review_spec 的全部有效约束(含系统默认与未指定项),modified 时展示服务端 `review_diff`;确认请求携带打开/保存后最新的 `expected_revision` 与 `expected_review_hash`,失败重试另传 `build_relation.retry_run_id` 指定的 `retry_of_run_id`,前端不自行计算 hash、不从消息寻找 run ID;同一次确认点击/网络重试复用同一幂等键,409 后重新核定用新键且面板不关闭。
- `Message.display_content` 为服务端生成的可选展示摘要，聊天展示与复制优先使用非空摘要；摘要复用 presenter 的版本、配件名称、报价口径和规则结论，不调用额外模型。旧消息仅依据同会话运行证据生成只读摘要，无法准确关联时保留原文。
- 切换会话按 session_id 重建聊天组件，运行句柄和未发送输入不误带入另一会话。
- 需求字段独立展示信息用途(事实、说明、配置条件)和满足强度；未分类旧字段保持未知，不在前端按关键词猜值。撤销后的原文仍留在历史，不继续作为生成输入。
- 已结束且带运行 ID 的助手回复下提供“不满意”入口，详见[评估设施](评估设施.md)。

### 3.3 /share/[token]

无会话导航、聊天、改单或 owner 操作；只展示配置版本、生成时间、报价快照、校验、理由和免责；token 无效/撤销统一展示「分享不存在或已失效」；页面可打印，打印样式移除导航和交互按钮。

## 4. 主流程交互

- 首次发送按钮立即 submitting 防双击；Session GET 返回 active_run 时自动连接 SSE;刷新先画已有消息/版本再恢复 run,不清空检查器。404 统一通用不存在页，不区分他人会话与真实不存在。degraded=true 时顶栏常驻提示，不阻塞当前操作。
- 聊天：user 与 assistant 用对齐、留白和轻表面区分，不做大气泡和彩色头像；markdown 仅支持段落、列表、强调和安全链接，禁用原始 HTML;composer Shift+Enter 换行、Enter 发送，中文输入法 composition 期间 Enter 不发送；running 状态禁用再次发送，不提供假取消按钮。
- 生成未交付时，聊天持久化服务端按结构化业务原因生成的安全说明(待核验条件、需求已保存、原配置保留、下一步)；失败操作区保留「查看或补充需求」和重试，原始远程输出只作证据不直接作为错误文案。
- 需求状态与核定面板全部读取服务端三轴与 readiness:前端不实现字段依赖、readiness、默认值、hash 或确认比较;预算上限展示使用服务端 `effective_budget_ceiling_cny`,不复制 10% 规则。品牌 any 明确显示「不限（用户已确认）」,系统默认标注「系统默认」。字段错误显示 API 稳定字段路径。
- 动态状态面板按服务端 kind 区分用途事实、补充说明与配置条件；只有配置条件显示必须/尽量满足。保留原话区展示未明确内容、原因和来源，撤销操作明确说明会同时撤销相关字段。
- Agent 进度固定三阶段文案(screening 正在整理需求 / remote_processing 正在生成并校验配置 / finalizing 正在保存结果)；最后事件超 30 秒显示重连提示，超 60 秒加辅助文案，10 分钟失败后提供按新幂等键重试。

## 5. 配置检查器

- 版本摘要：vN 与父版本、pass/review/fail、总价、预算、差额、快照日期。回看旧版本显示「历史版本」，改单快捷入口禁用。
- 零件列表固定顺序：CPU、GPU、主板、内存、SSD、电源、机箱、散热。每行含品类图标、品牌型号、SKU、数量、单价/小计、一句 rationale;当前版本 phase=ready 时提供「更换此件」(预填「把显卡换成……」到 composer 并聚焦)。无商品图片不用占位框；缺价显示「缺价，未计入合计」，不可显示 ¥0。已有件不显示普通换件按钮。
- 校验区：12 条规则顺序与 schemas.AllRuleIDs 一致；每条展示中文名、outcome、severity、detail,高级展开才显示 observed/missing_fields;unknown(数据不足)与 warning(已知风险)视觉不同；免责区固定可见。
- 版本与 diff:时间线按 v1→vN 排列并明确 parent;diff 显式选择 from/to,八品类全部展示，变化行展示前后型号和价格差；快照日期不同必须在总差额附近提示；切换版本保留聊天滚动和输入草稿。工作台同时显示整机参考价与采购合计，预算差按选定口径计算。

## 6. 状态管理与缓存

TanStack Query key 分层：sessions / session/{id} / builds/{session_id} / build/{session_id}/{version} / diff/{session_id}/{from}/{to} / publicShare/{token};Zustand 只保存 workspaceTab、inspectorSection、diff 选择等瞬时 UI,会话切换重置。失效规则：assistant.completed / requirement.ready / requirement.updated / requirement.confirmed → session;presentation.action 只驱动当前 run 的短期 UI 动作,不失效业务缓存;build.saved → session、builds、对应 build、相关 diff;分享创建/撤销 → 当前 build 的 share 状态。侧栏 `PATCH requirement-state` 以响应的完整 Session 替换缓存,不等待不存在的编辑 run/SSE。SSE 增量不直接伪造完整 BuildView;路由参数/session/version 是可分享导航状态，优先放 URL。

## 7. 响应式与无障碍

- ≥1024px 三栏；768–1023px 单列聊天 + 顶栏会话抽屉；<768px 手机单列、底部 composer、详情全宽抽屉。核心功能不删除：需求确认、配置、规则、版本、diff、导出、分享均可达；表格转定义列表；点击目标 ≥44×44px。
- 所有状态同时具备文字、图标和颜色；focus-visible 清晰；Radix 语义与焦点管理；SSE 更新用礼貌 aria-live,delta 不逐 token 朗读；错误文案说明发生了什么、数据是否保存、下一步怎么做；不使用「AI 正在思考」等不可验证表述；价格快照和免责使用产品语言。

## 8. 测试要求

API client 与 OpenAPI 生成类型无漂移；需求状态分组、字段状态、CTA 状态、Tab disabled、409 冲突、running 编辑与 outdated build 的组件测试；SSE parser 覆盖拆包、多行 data、心跳、重放、重复 id 和终止事件；配置、校验、diff 的 pass/review/fail/unknown/缺价快照测试；Playwright(离线 requirements harness,不调用真实模型)覆盖渐进收集、手动填写、核定、生成、修改后旧配置与失败重试;375/768/1440 视口;键盘可完成 Tab、编辑、保存、核定与返回,中文输入法 composition 期间 Enter 不提交;axe 无严重/高等级问题；断网、Redis degraded、buildsvc 503、10 分钟 timeout 均有可恢复 UI。
