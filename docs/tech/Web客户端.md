# Web客户端

> 本文维护 Next.js 工作台的组件、状态与交互边界。产品需求见[PRD](../product/PRD.md)，HTTP 契约见[OpenAPI](../api/openapi.yaml)。

## 1. 产品与视觉命题

### 1.1 Visual thesis

一个克制、精确、偏深色的装机工作台:以低对比炭黑表面和细边线承载高密度信息,只用一枚紫蓝强调色表达主要动作,让配置、价格和校验结论成为视觉主角。

### 1.2 Content plan

这是工作应用而非营销站,首屏直接进入任务:

1. 会话与运行状态:用户知道自己在哪个会话、系统是否可用。
2. 对话/需求:输入需求、回答追问、确认结构化需求。
3. 配置检查器:查看当前版本零件、报价、校验和理由。
4. 版本与交付:回看、diff、导出、分享。

不做 Hero、统计卡片墙、品牌口号或与任务无关的展示区。

### 1.3 Interaction thesis

只保留三类有意义的动效:

- Agent 进度按真实事件淡入,阶段更新使用同一位置的短过渡。
- 切换版本时检查器内容做 160–220ms 的轻微淡入/位移,保持空间关系。
- diff 中变化行用一次性背景高亮和边线强调,不循环闪烁。

prefers-reduced-motion 下全部变为无位移即时切换。

## 2. 工程栈与目录

已结束且带运行 ID 的助手回复下提供“不满意”入口，按运行保存原因和说明，允许修改。提交失败保留输入；读取历史反馈后可载入编辑。入口使用现有按钮、表单与响应式布局，详情见[反馈与评估回流](反馈与评估回流.md)。浏览器覆盖桌面、平板和手机的提交、重试、修改、刷新后读取流程；这不替代真实模型的完整产品验收。

前端位于 web/,不建立通用 monorepo:

```text
web/
├── src/
│   ├── app/
│   │   ├── page.tsx
│   │   ├── s/[session_id]/page.tsx
│   │   └── share/[token]/page.tsx
│   ├── components/
│   │   ├── ui/              # 仅实际安装的 shadcn/Radix 原子组件
│   │   ├── chat/
│   │   ├── requirement/
│   │   ├── build/
│   │   └── versions/
│   ├── features/            # 按会话/run/build/share 划分的状态与组合逻辑
│   ├── lib/api/             # 生成类型、fetch client、SSE parser
│   └── stores/              # 仅瞬时 UI 状态
├── package.json
└── pnpm-lock.yaml
```

固定选择:

- Next.js App Router + TypeScript strict。
- Tailwind CSS + CSS variables。
- shadcn/ui 使用 Radix 基础;按需安装 Button、Tabs、Dialog、Drawer、Tooltip、DropdownMenu、ScrollArea、Textarea、Select、Form、Toast/Sonner。
- TanStack Query 管服务端状态。
- Zustand 只管 mobilePane、inspectorTab、diffSelection 等瞬时状态;不存 session、message、build 或 run 真值。
- React Hook Form + schema validator 用于 RequirementSpec 编辑;validator 必须从 OpenAPI/共享 schema 派生,不能手写另一份规则。
- Playwright 做 E2E,Vitest + Testing Library 做组件/纯函数测试。

scaffold 当天使用稳定版本,随后以 package.json 和 pnpm-lock.yaml 为唯一真值。开发指导不得长期保留 latest 命令作为日常安装方式。

## 3. Server/Client 边界

| 页面/模块 | 渲染边界 | 原因 |
|---|---|---|
| 根 layout、静态元数据 | Server Component | 少 JS、统一主题和字体 |
| /share/[token] | Server Component | 首屏、OG 和只读 SEO |
| 主工作台 shell | Server 可输出框架 | 快速首屏 |
| 聊天、composer、SSE、需求表单 | Client Component | 浏览器状态与交互 |
| 配置/版本查询 | TanStack Query Client | SSE 后精确失效与刷新 |
| 分享图 | Next ImageResponse | 纯展示,只消费 public DTO |

Next.js 不定义业务 Route Handler 或 Server Action。开发环境由 next.config rewrite 把 /api/:path* 转发到 http://localhost:8082/api/:path*;healthz/readyz 可单独代理。生产由同源反向代理承担相同职责。

## 4. 页面与导航

### 4.1 /

- 显示品牌名「装机配置单 Agent」、一句功能说明和直接可用的 composer。
- 不预创建空会话;用户首次发送时先 POST session,再导航到 /s/{id} 并提交消息。
- 提供 3 个真实示例 prompt,点击只填入输入框不自动发送。
- 若已有匿名会话,左侧显示最近会话;无登录诱导。

### 4.2 /s/[session_id]

桌面结构:

```text
┌────────────────────────────────────────────────────────────┐
│ 品牌 / 会话标题                   服务状态  导出  分享     │
├──────────────────────┬─────────────────────────────────────┤
│                      │ 需求 / 配置 / 校验 / 版本            │
│ 对话历史             ├─────────────────────────────────────┤
│ Agent 进度            │ 当前版本摘要                         │
│ 需求确认卡            │ 零件紧凑列表                         │
│                      │ 价格、理由、状态                      │
│                      │                                     │
├──────────────────────┤ 固定可见的快照与免责                  │
│ 多行输入框 / 发送     │                                     │
└──────────────────────┴─────────────────────────────────────┘
```

- 视口 ≥1024px 使用 42%/58% 双栏,检查器最小宽 560px。
- 顶栏高 56px;输入区保持可见,聊天区独立滚动。
- 不用厚重外框包住每个区域;主要层级由表面色和 1px hairline 分隔。

### 4.3 /share/[token]

- 无会话导航、聊天、改单或 owner 操作。
- 只展示配置版本、生成时间、报价快照、校验、理由和免责。
- token 无效/撤销统一展示「分享不存在或已失效」,不提供内部错误。
- 页面可打印,打印样式移除导航和交互按钮。

## 5. 主流程交互

### 5.1 会话创建与恢复

- 首次发送时按钮立即进入 submitting,防止双击。
- Session GET 返回 active_run 时自动连接 SSE。
- 页面刷新后先画已有消息/版本,再恢复 run,不清空检查器。
- 404 显示通用不存在页;不得区分他人会话与真实不存在。
- degraded=true 时顶栏展示常驻琥珀提示,说明刷新后可能无法恢复,但不阻塞当前操作。

### 5.2 聊天

- user 与 assistant 使用对齐、留白和轻表面区分,不做大气泡和彩色头像。
- assistant.delta 只更新当前临时消息;assistant.completed 替换并刷新 session。
- markdown 仅支持段落、列表、强调和安全链接;禁用原始 HTML。
- composer 支持 Shift+Enter 换行、Enter 发送;中文输入法 composition 期间 Enter 不发送。
- running 状态禁用再次发送,显示当前 run 的真实阶段。
- 不提供假取消按钮;允许用户离开页面并稍后恢复。

### 5.3 需求确认卡

字段分组:

- 核心:预算、预算弹性、主用途。
- 游戏:游戏名称、分辨率、帧率;非 gaming 隐藏但不伪造值。
- 偏好:尺寸、噪音、CPU/GPU 品牌。
- 约束:已有配件品类、优先品类、备注。

规则:

- 表单初始值来自完整 RequirementSpec。
- 保存调用 PATCH 完整替换;成功后再更新 Query cache。
- 确认按钮与保存分开;有未保存修改时确认先保存,保存成功后再 confirm。
- 字段错误显示 API 返回的稳定字段路径,不展示 Go error 原文。
- 品牌 any 明确显示「不限」;不得默认帮用户选择 NVIDIA/AMD。

### 5.4 Agent 进度

进度区固定映射:

| stage | 文案 | 可说明内容 |
|---|---|---|
| screening | 正在整理需求 | 可能继续追问或准备确认卡 |
| remote_processing | 正在生成并校验配置 | 生成和规则校验合并描述 |
| finalizing | 正在保存结果 | 版本即将可查看 |

最后一次事件超过 30 秒时显示「连接不稳定,正在重连」;这不是 run 失败。超过 60 秒增加「复杂偏好可能需要更久」辅助文案;10 分钟失败后提供按新幂等键重试。

## 6. 配置检查器

### 6.1 版本摘要

顶部一行固定展示:

- vN 与父版本。
- pass/review/fail 文字 + 图标。
- 总价、预算、差额。
- 快照日期。

回看旧版本时显示「历史版本,当前为 vN」,所有改单快捷入口禁用,避免用户误以为会基于旧版修改。

### 6.2 零件列表

固定顺序:CPU、GPU、主板、内存、SSD、电源、机箱、散热。

每行包含:

- 品类图标和中文名。
- 品牌型号;次级文本显示 SKU。
- 数量、单价/小计。
- 一句 rationale。
- 当前版本且 phase=ready 时的「更换此件」。

「更换此件」行为:

1. 将「把显卡换成……,其他配件尽量不动」写入 composer。
2. 选中省略处并聚焦。
3. 用户编辑后手动发送。

无商品图片时不用占位图片框,只用一致的品类图标。缺价显示「缺价,未计入合计」,不可显示 ¥0。

### 6.3 校验区

- 摘要先展示总体状态和通过/警告/unknown/错误数量。
- 12 条规则顺序与 schemas.AllRuleIDs 一致。
- 每条展示规则中文名、outcome、severity、detail;高级展开才显示 observed/missing_fields。
- pass 不用大面积绿色,只使用小图标和文本。
- unknown 与 warning 视觉不同:unknown 表示数据不足,warning 表示已知风险。
- 免责区固定可见,不能折叠或通过设置关闭。

### 6.4 版本与 diff

- 时间线按 v1→vN 排列,明确 parent。
- 默认查看当前版本;选择旧版不会改变当前真值。
- diff 需要显式选择 from/to,默认当前版本与父版本。
- 八品类全部展示;未变化行保持低强调,变化行展示前后型号和价格差。
- 快照日期不同必须在总差额附近提示。
- 切换版本时保留聊天滚动和输入草稿。

## 7. 状态管理与缓存

TanStack Query key 固定分层:

- sessions
- session/{id}
- builds/{session_id}
- build/{session_id}/{version}
- diff/{session_id}/{from}/{to}
- publicShare/{token}

失效规则:

- assistant.completed → session。
- requirement.ready → session。
- build.saved → session、builds、对应 build、相关 diff。
- 分享创建/撤销 → 当前 build 的 share 状态。

SSE 增量不直接伪造完整 BuildView。Zustand 不跨浏览器刷新持久化;路由参数/session/version 是可分享导航状态,优先放 URL。

## 8. 视觉 token 摘要

精确规则以根目录 DESIGN.md、DESIGN_CONTEXT.md、UI_RULES.md 为准。实现必须使用 CSS variables,不得在组件散落 hex。

- canvas:#0b0c0f;surface:#111318;surface-raised:#171a21。
- hairline:#272b35;文字主色:#f2f3f5;次级:#a6abb6。
- primary:#737de8;只用于主操作、选中和焦点。
- success:#45a66b;review:#d39a45;error:#d85c66;unknown:#8f96a3。
- 按钮/输入 8px 圆角,交互面板 12px;不用 pill 作为常规按钮。
- 4px 基础间距;常用 8/12/16/24/32。

## 9. 响应式

| 宽度 | 布局 |
|---|---|
| ≥1280px | 42/58 双栏,完整会话侧栏和检查器 |
| 1024–1279px | 双栏,会话列表收为抽屉 |
| 768–1023px | 顶部「对话/配置」切换,单面板 |
| <768px | 手机单栏,底部 composer,配置内二级 tabs |

手机端要求:

- 核心功能不删除:需求确认、配置、12 条规则、版本、diff、导出、分享均可达。
- 点击 build.saved 后不强行把用户从聊天切到配置,只显示可访问提示。
- 表格转为定义列表,不横向压缩到不可读。
- 点击目标 ≥44×44px。

## 10. 无障碍与文案

- 所有状态同时具备文字、图标和颜色。
- focus-visible 清晰,不移除 outline 后无替代。
- Tabs、Dialog、Drawer 使用 Radix 语义与焦点管理。
- SSE 更新使用礼貌 aria-live;delta 不逐 token 朗读,completed 时统一通知。
- 错误文案说明发生了什么、数据是否保存、下一步怎么做。
- 不使用「AI 正在思考」等不可验证表述;只显示协议允许的真实阶段。
- 价格快照和免责使用产品语言,不使用小到难读的法律灰字。

## 11. 测试与 DoD

- API client 与 OpenAPI 生成类型无漂移。
- RequirementSpec 表单条件字段、保存/确认串行和错误路径单测。
- SSE parser 覆盖拆包、多行 data、心跳、重放、重复 id 和终止事件。
- 配置、校验、diff 的 pass/review/fail/unknown/缺价快照测试。
- Playwright 覆盖首次会话、追问、确认、生成、改单、回看、diff、导出。
- 375/768/1440 视口截图回归。
- axe 无严重/高等级问题;仅键盘可完成主路径。
- 人为断网、Redis degraded、buildsvc 503、10 分钟 timeout 均有可恢复 UI。
