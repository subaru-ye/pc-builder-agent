---
status: proposed
created: 2026-09-24
---

# Change: Requirement v2 evaluation workbench

## Dependency and execution boundary

本 change 可先作为设计合同审阅；**实现须等 `requirement-workspace-sidebar-v2`（Spec 5）收口并提交后开始**，避免与在途 Web/设计规则改动混在一次提交。它是独立的评估设施 change，不是原六份 change 的重新编号，也不是 `requirement-v2-release-certification`（Spec 6）的发布门槛。

Spec 6 使用冻结的 CLI 产物和人工复核作最终 go/no-go；工作台只辅助定位和阅读证据。若希望用本工作台辅助 Spec 6 人工验收，应在认证候选冻结前实现并验收本 change；工作台故障或缺席不得改变 evaluator 的判卷结论。

## Outcome

把当前以历史 `eval`/`evalchange` 运行、旧 grader 重判和“运行对比”为中心的 `/eval`，重构为能优先审阅 Requirement v2 的只读证据工作台。使用者应能回答：

1. 正在看的究竟是哪次运行、什么模式、什么 split/repeats、哪个候选/模型/manifest/grader/gates；证据是否完整、是否来自零模型 replay/regrade。
2. 各层到底通过、失败、跳过了多少；冻结 gate 哪些 PASS、FAIL 或 UNEVALUABLE；veto 在哪道题、哪次重复、哪一轮出现。
3. 某个 case 的冻结输入与预期、实际 operations/observations/signals/readiness/状态、断言与失败分类怎样对应；Pass^k 的哪次重复失败。
4. 两次运行是否具备严格可比条件；若不可比，只能看各自证据，不能获得误导性改进分数。

历史评估仍可进入、检索和阅读，但允许合并重复导航、调整信息层级。v2 不套用旧 `evalsuite` 的 `Metrics`、grader 或“执行通过率”语义。

## Existing-state evidence

- `internal/evaldesk/store.go` 目前只扫描 `artifacts/eval` 与 `artifacts/evalchange`，识别 `meta.json`/`cases.json`/`results.jsonl`；Requirement v2 的 `artifacts/reqv2/*/plan.json`、`report.json`、manifest/gates 不会被发现。
- `internal/evaldesk/types.go` 与 `web/src/lib/evaldesk/types.ts` 围绕旧 trial/seed、current regrade、旧运行指标；无法直接表达六层、结构化 `gate_verdicts`、`model_quality`、veto、skip、Pass^k 和 replay/regrade 身份。
- 当前 `/eval` 导航分“评估运行/运行对比/代码提交/题库/时间线”，v2 人工复核需要先看候选结论，再从 gate/层进入题目和逐轮证据。原 UI 的历史溯源能力可复用，但导航主次要重排。
- Requirement v2 的冻结 `report.json` 已包含 `per_layer`、`model_quality`、`gate_verdicts`、`cases` 和 `usage`；`plan.json` 记录 mode、split、预算、模型与来源，`results.jsonl` 存逐 case/repeat 观测。首期不需要新 runner 或模型调用。

## Scope

### In

- `/eval` 的 v2 优先信息架构，以及历史评估的可达入口。
- Go 端对 `artifacts/reqv2` 的只读发现、产物完整性校验、版本化 v2 DTO 投影和安全 API。
- v2 运行列表、运行详情、冻结门槛、分层结果、题目/重复/逐轮证据、同身份运行对比与来源说明。
- 旧工作台导航与页面结构的必要重构，删除重复或不再适合的表现层代码；旧产物不删除、不迁移、不重新判卷。
- 合成离线产物测试、真实本机产物抽查、浏览器/响应式/无障碍验收及文档更新。

### Out

- 不修改 `cmd/evalrequirement`、`planningeval` 判卷、fixture、gold、manifest、grader、gates 或 Spec 6 的发布政策；不得因工作台显示而改分。
- 不运行 live、model swap、ablation 或 holdout；不提供启动、重跑、regrade、删除、编辑标签、改门槛等写操作。
- 不提前读取当前工作区的未运行 holdout 金标来填充 UI；题目只来自被选运行的冻结证据。
- 不预设 Spec 6 尚未冻结的认证总报告格式。后续可按真实产物合同增加认证视图；首期只支持 `artifacts/reqv2`。
- 不让 Next.js 实现评分、Pass^k、veto 检测、显著性检验或认证 go/no-go 计算。

## Information architecture

顶层按评估体系分为 **Requirement v2**（有有效 v2 运行时默认）与 **历史评估**（旧 `eval`/`evalchange`）；若尚无 v2 产物，显示清楚的空状态并允许进入历史评估，不伪造“0 分”或“未通过”。页内以运行作为稳定上下文，URL 可直达运行、门槛、题目和重复，不把选择仅藏在组件状态。

### 1. 运行目录

每行显示时间、运行类型（live/零模型 deterministic/replay/regrade）、split、repeats、manifest/grader 简写、候选代码身份、证据状态与结论。默认将 `superseded-*` 单独折叠或过滤，明确“历史归档/不可作当前基线”；不能以目录名或时间推断“最佳运行”。可按类型、split、证据状态、grader 搜索/过滤。运行中的产物、缺文件、损坏文件、旧 grader、dirty candidate 与真正 gate FAIL 使用不同文案。

### 2. 运行详情

首屏顺序：

1. **身份/有效性**：来源、模式、split/repeats、冻结 manifest/grader/gates、计划调用预算、代码 commit/dirty、模型身份（已脱敏）、report/plan/manifest 一致性；regrade 明示“零模型重判旧观测”，不得显示成新 live 结果。
2. **结论**：直接呈现冻结 `gate_passed` 与 `conclusion`，再解释“不可发布”是 gate FAIL、UNEVALUABLE、veto、确定性层失败或证据缺失；工作台不得自造第二个总分。
3. **冻结门槛**：完整 `gate_verdicts` 行表，按 model/extraction/conversations/全局分组，逐项显示实际值、阈值与 PASS/FAIL/UNEVALUABLE；`UNEVALUABLE` 用“未满足评估条件”而不是“模型失败”。
4. **六层**：extraction/conversations 与 reducer/readiness/policy/ui-contract 分组显示 case 级 Pass^k 通过数、失败/跳过/veto 与分类，模型层的 operation precision/recall、signal、任务成功另列，不能与确定性层平均。
5. **效率与限制**：调用数/预算、已知 token 及完整性、p50/p95、`limitations`。零模型运行不显示伪造的 latency 或 provider 成功率。

不要用 dashboard 卡片墙；用清晰的标题、摘要、分隔线、紧凑数据行和必要的表格。失败与不可评估优先可定位到 case，已通过项可折叠但不得消失。

### 3. 逐题证据

从层、gate、veto 或搜索进入 case。按 `layer/id` 唯一定位，显示 split、session、全部已记录 repeat；先展示 case 级 Pass^k，再逐 repeat 展示断言、veto、失败分类和实际观测。对 extraction/conversations，再按 turn 对齐被冻结的用户输入/期望与实际 operations、observations、turn_signals、proposal/回复、最终 RequirementState；对 deterministic 层显示输入状态、readiness/reducer/policy/ui 观测与逐字段断言。只有产物确实保存的字段才展示；缺失标“未记录”，不从当前 fixture、prompt、聊天历史或其他运行补填。长 JSON 放在次级展开区，主阅读区优先结构化差异和可读标签。

### 4. 对比与溯源

严格对比只对 evaluator 允许的同一冻结身份进行：至少 manifest、grader、gates 一致，且要明确相同的 case 集、split/repeats 与 live/zero-model/regrade 语义是否可直接比较。严格模型层配对复用 `planningeval.CompareRequirementV2` 的 case 级 Pass^k 与最小样本/McNemar 口径；确定性层独立显示，不混进模型统计。若条件不足或来源语义不同，仅提供并排证据和“不具备严格比较条件”的原因，不给“提升/退步百分比”或暗示因果。跨 grader 版本必须先由 CLI 显式 regrade，工作台不能静默处理。

代码/题库溯源复用旧工作台能力，但 v2 的来源以该运行 `plan.json`、冻结 manifest/gates 和产物本身为准；本机当前 HEAD 或目录名不代替运行时程序身份。运行详情可跳转到对应的冻结题目，而不是跳到“现在的题库”。

## Read-only data contract and safety

- 在 Go 端新增与旧 `evalsuite` DTO 分离的 v2 投影/API（例如 `/api/evaldesk/requirement-v2/*`）；旧 API 维持兼容。复用现有 loopback-only、GET-only、no-store、固定根目录与文件名白名单、大小限制、防路径穿越/符号链接/Windows junction 防护，不接受客户端传入文件路径。
- 发现范围仅 `artifacts/reqv2` 下有明确 v2 `plan.json`/`report.json` 的运行目录；识别 `superseded-*`，拒绝把孤立 JSON、原始 log 或当前仓库 fixture 冒充一次运行。固定文件包括运行计划、报告、结果、manifest、gates 和运行内冻结 fixture；不扫描 `.env`、凭据、任意源码或 model raw trace。
- 校验计划与报告的 grader/manifest/gates/split/repeats 身份一致，manifest/gates 文件实际哈希与冻结记录一致；对 results 的 `(layer,id,repeat)` 重复、缺失或不属于该运行冻结集合的记录给出 `incomplete/invalid`，不能把缺证据算通过。**产物完整性校验不等于重判正确性**，UI 分开标注。保留报告原值，不静默修正不一致分数。
- 后端只向浏览器投影必需的字段，长度有界并转义；不返回 API key、Authorization、完整模型配置、绝对路径、原始 events、隐藏推理或可用于枚举本机文件的错误。冻结用户文本/模型可见回复属于本机敏感证据，按现有只读本机限制展示，不经第三方服务。
- API 字段区分 `null/未记录`、`skipped`、`UNEVALUABLE`、`FAIL`；前端不把它们压成同一个 `false`。读取半成品时可以显示已知证据，但总状态保持 incomplete，禁止给发布建议。
- 性能以列表轻投影、按需加载报告/case 为先；大产物不整包发送给浏览器，缓存失效基于运行文件身份，继续提供明确“重新读取”。

## Design and interaction rules

沿用根目录 `DESIGN.md`、`DESIGN_CONTEXT.md`、`UI_RULES.md` 的 token、排版、语义色与只读原则；本 change 不是另建一套品牌视觉。主结论只占一处醒目位置，后续按证据层级展开；PASS/FAIL/UNEVALUABLE/skipped/invalid 均用文字+图标，不单靠颜色。桌面以目录/证据阅读为主，375px 将表格转定义列表，不以横向滚动藏结论；键盘可完成运行选择、过滤、切换 repeat 和返回列表，焦点与 URL 深链一致。不得自动打开大量 JSON、不得用“分数上涨”掩盖 veto。界面文字用简体中文说明“发生了什么、证据是否足够、下一步看哪里”。

## Verification and acceptance

### A. Data contract

- 合成 v2 产物覆盖 live、零模型 deterministic、replay/regrade、缺 plan/report、破损 JSON、manifest/gates 哈希不符、部分 results、重复 case/repeat、superseded、无目录、symlink/junction、路径穿越与敏感字段脱敏；旧 `eval`/`evalchange` 回归不变。
- 对仓库已有 `artifacts/reqv2/spec3-final-live-20260923` 抽查：完整 16 条 gate verdict、六层、129 次调用、Pass^3 逐题与产物一致；此运行 `gate_passed=false` 因当时确定性层红项，工作台不能误写为整体 16/16 发布通过。真实历史产物缺失时该抽查明确 skip，不生成替代样本。
- 对零模型 `spec4-*` 抽查：模型门槛 UNEVALUABLE、skip 与 no-model 身份正确，不能显示为模型失败；regrade 保留源运行指针且标明“零模型”。

### B. Browser and UX

- 首屏可在不打开 JSON 的情况下找到运行身份、总门槛、失败 gate/veto、六层和缺证据原因；逐题可定位失败 repeat/turn，并从深链返回原上下文。
- 两个同身份运行严格对比；不同 grader/manifest、live 对零模型、缺 case/重复记录等给出不可严格比较原因，不产出误导性改善率；归档运行不得默认当作现行基线。
- `go test ./...`、`go vet ./...`、Web typecheck/lint/component tests 和离线 Playwright evaldesk 套件通过；375/768/1440 视口、键盘、暗/亮主题及 axe serious/critical 检查。浏览器测试拦截非 GET/非 loopback/非 evaldesk API，请求模型次数为 0。

### C. Human acceptance checkpoints

1. 先验收**数据映射与证据诚实性**：拿一份真实 v2 live、一份 zero-model 和一份 superseded，逐项对照 report/plan，确认 PASS、FAIL、UNEVALUABLE、Pass^k 没被误读。
2. 再验收**工作流**：从失败 gate → 层 → case → repeat/turn → 冻结来源；尝试不可比运行；检查历史评估仍可访问。
3. 最后验收**视觉与无障碍**：桌面/手机、键盘、长中文和错误/空状态。此处不要求先跑 Spec 6 的 holdout。

## Completion checklist

- [ ] Spec 5 已独立收口并提交；本 change 的提交不混入其在途文件。
- [ ] v2 运行发现、安全读取、完整性状态和版本化只读 API 完成，旧 API 未回归。
- [ ] v2 优先信息架构与历史入口完成；没有重复评分体系或假总分。
- [ ] 冻结身份、门槛三态、六层、Pass^k、veto、逐题逐轮证据均可从产物追溯。
- [ ] 严格对比/不可比边界、regrade 和 superseded 身份正确表达。
- [ ] 零模型/损坏/缺证据/holdout 未运行场景不会被展示成有效发布结论。
- [ ] 离线单元、浏览器、响应式、安全和人工抽查通过；无真实模型调用。
- [ ] `docs/tech/评估设施.md`、`docs/tech/Web客户端.md`、必要的设计规则与评估 README 同步；标记 done 前附真实证据索引。
