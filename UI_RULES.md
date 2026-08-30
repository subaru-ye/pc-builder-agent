# UI Rules — 装机配置单 Agent

## 1. Required reading

开始任何 Web UI 实现或评审前,按顺序读取:

1. DESIGN.md。
2. DESIGN_CONTEXT.md。
3. 本文件。
4. docs/tech/P8-Web客户端设计.md。

已有 token/组件优先;不得为单一页面引入新配色、圆角或字体体系。

## 2. Token discipline

- 所有颜色、间距、圆角、字体和动效时长使用 CSS variables/Tailwind theme token。
- 业务组件禁止散落十六进制颜色。
- primary 只能用于主要动作、选中、链接和 focus ring。
- success/review/error/unknown 只能对应真实状态。
- 普通容器不得使用 semantic 背景填满整个区域。
- 系统、深色、浅色只能切换既有 token；组件不得写主题分支或新增局部十六进制颜色。
- 菜单项的 hover、键盘高亮和展开态统一使用 interactive hover/selected token；当前选项还必须有勾选或文字状态，不能只靠颜色。

## 3. Spacing and layout

- 4px 基准;常用间距仅 4/8/12/16/24/32/48。
- 桌面主工作区 42/58,检查器最小宽 560px。
- 顶栏 56px;常规控件 36–40px,触屏 44px。
- 区域之间使用 1px hairline;同一语义组内优先 whitespace。
- 不把聊天、配置、校验、版本各自再包多层卡片。
- 无内容时给出一个明确下一步,不用多张空状态卡。

## 4. Typography

- 正文 14px/1.55 desktop,15px/1.6 mobile。
- 最小可读正文 12px,仅用于短 metadata;免责声明不得降到难读尺寸。
- 价格使用 tabular-nums。
- SKU/版本可使用 mono;型号、理由、错误不用 mono。
- 中文标题不使用全大写模拟英文 eyebrow。
- 每屏最多三级明显字号层级。

## 5. Components

### Buttons

- Primary:一屏一个主要动作;primary fill,8px radius。
- Secondary:surface-2/hairline。
- Tertiary:文本或透明背景。
- Destructive:仅撤销分享等真实破坏动作,用 error 语义。
- 禁止常规 pill CTA、渐变按钮和发光按钮。
- disabled 必须同时改变可用性和视觉,并提供原因附近文案。

### Inputs

- 输入/选择 8px radius,focus-visible 使用 2px primary ring。
- label 永远存在;placeholder 不代替 label。
- 错误紧邻字段,包含稳定字段名的用户友好翻译。
- 中文输入法 composition 期间 Enter 不触发提交。

### Cards and panels

- 只有 Requirement confirmation、Dialog、Share summary 等本身是交互对象时才用 card。
- 普通信息组用 section + divider + row。
- 禁止 dashboard 卡片墙、玻璃拟态和多层 shadow。

### Status

- pass:Check 图标 +「通过」。
- review:Alert 图标 +「需复核」。
- fail:X 图标 +「未通过」。
- unknown:Question/Minus 图标 +「数据不足」。
- 任一状态不得只显示彩色圆点。

## 6. Domain-specific rules

- 配件固定顺序:CPU、GPU、主板、内存、SSD、电源、机箱、散热。
- 校验固定 12 条顺序,与后端 schemas.AllRuleIDs 一致。
- 缺价显示「缺价,未计入合计」;禁止 ¥0。
- 快照日期与总价同一视觉区域。
- 免责在配置、导出、分享中始终存在。
- 历史版本明确标注只读,改单动作禁用。
- 「更换此件」只预填 composer,不得自动发送。
- 不展示 Agent 思维、工具参数、A2A task 或内部 state key。

## 7. Responsive behavior

- ≥1024px 使用双栏;小于 1024px 改为单活动面板。
- <768px composer 固定底部,配置/校验/版本用二级 tabs。
- 复杂表格在手机转定义列表,不靠横向滚动隐藏主要内容。
- 所有核心能力在 375px 可达。
- Drawer/Dialog 打开后正确锁定和恢复焦点。

## 8. Motion

- 只允许 Agent 进度、版本切换、diff 高亮和基本控件反馈。
- enter/update 160–220ms;hover/press 100–160ms。
- 不使用自动循环、背景漂移、粒子、光标 spotlight 或装饰性滚动视差。
- prefers-reduced-motion 时去掉位移和非必要过渡。

## 9. Accessibility

- 正文/背景达到 WCAG AA;focus ring 清晰。
- 图标按钮必须有 accessible name 和 Tooltip。
- 动态 delta 不逐 token aria-live;completed 时统一礼貌通知。
- 错误摘要可聚焦并链接到字段。
- 键盘可完成创建会话、发送、保存/确认需求、切换版本、diff、导出和分享。
- 触控目标 ≥44×44px。

## 10. Copy

- 标题直接描述区域或动作:「当前配置」「校验结果」「版本对比」。
- 进度只用 screening、remote_processing、finalizing 的真实映射。
- 错误必须回答:发生什么、是否已保存、下一步怎么做。
- 不使用「绝对兼容」「实时最低价」「AI 深度思考」等不可证实表述。
- 不重复同一信息;总价、状态、快照在主摘要展示一次,细节区只补充。

## 11. Review checklist

- 是否先展示工作面而不是营销 Hero?
- 是否一眼可见当前 phase、版本、状态、总价和快照?
- 是否有多余卡片、边框、阴影或颜色?
- 所有状态是否有文字和图标?
- unknown 是否被诚实表达?
- 手机端是否保留 8 大件、12 条规则和版本能力?
- 是否无业务逻辑进入 Next.js?
- 是否通过键盘、中文输入法、reduced motion 和三档视口测试?
