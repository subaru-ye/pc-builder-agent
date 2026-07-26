# 装机配置单 Agent — MVP 实现指导

> 2026-07-26 · 短期实现的唯一依据 · 配套长期方向见 [PRD.md](PRD.md)

## 0. 文档定位

- 本文是**短期实现指导**:范围裁定、阶段拆分、验收标准、环境与数据准备,均以本文为准。
- [装机Agent设计方案](../装机Agent设计方案.md)是架构、A2A schema、兼容性规则表、表结构的**唯一出处**——本文只引用章节号,一律不复制,防止两处漂移。
- [PRD.md](PRD.md) 承担长期产品方向(用户/功能/路线图/指标),本文不谈愿景与竞品。
- [技术选型](../tech/技术选型.md)是技术栈决策(ADR)的权威:语言/框架/模型供应商以它为准(Go + ADK-Go + a2a-go + 百炼)。
- 各阶段开工前与评审时,对照[工程实践指引](../tech/工程实践指引.md)对应小节与阶段检查单(§九)。
- **本文的范围裁定与阶段划分取代设计方案 §一"MVP 功能边界"与 §八"里程碑 M1–M6"**,冲突时以本文为准。旧文档不修改,保留为历史设计依据。

## 1. MVP 目标与验收

### 1.1 一句话目标

在 ADK dev UI(ADK-Go 内置 launcher 的 Web UI)里跑通完整闭环:**对话收集需求 → 生成配置单 → 规则校验报告 → 多轮增量改单 → 版本快照与 diff → 导出 Markdown**;其中"生成 + 校验"流水线作为一个 A2A 远程服务独立运行,会话热上下文落 Redis。零自研前端。

### 1.2 验收用例清单(MVP 冻结条件 = 全绿)

| 用例 | 内容 | 演示方式 | 落地阶段 |
|---|---|---|---|
| A | 「8000 元 2K 玩黑神话」→ 完整配置单 + 全 pass 校验报告 + 快照日期 | dev UI 对话 | P2 |
| B | 注入故意错配的配置 → 报告逐项报错;字段缺失的规则输出 unknown 而非崩溃 | go test + CLI 脚本 | P1 |
| C | 「降 500 优先砍哪」→ v2 增量修改,未涉及件被锁定 | dev UI 对话 | P4 |
| D | 「换成 A 卡」→ v3;版本树 v1→v2→v3 可回放,diff 正确 | dev UI 对话 + CLI | P4 |
| E | 导出 Markdown,含快照日期与免责边界 | CLI / dev UI | P4 |
| F | 「要安静的显卡」「白色海景房」→ 语义候选可解释命中 | dev UI 对话 | P3 |
| G | 初筛与"生成+校验"分处两进程,A2A 消息日志可查、schema 校验通过 | 双进程启动 + 日志 | P5 |
| H | kill 掉 host 进程重启后,同一会话从 Redis 恢复继续改单 | 操作演示 | P6 |

其中用例 B 为可自动化回归(go test);其余为 dev UI / CLI 人工演示用例,判定标准见对应阶段的 DoD(§3)。

### 1.3 学习目标覆盖矩阵

| 学习组件 | 落地阶段 | 对应验收用例 |
|---|---|---|
| ADK 多 Agent 编排(Sequential + Loop) | P2 | A |
| 规则引擎兜底(零 LLM) | P1 | B |
| pgvector 语义检索 | P3 | F |
| 版本快照 + diff + 日期化报价 | P4 | C/D/E |
| A2A 协议 | P5 | G |
| Redis 会话热上下文与缓存 | P6 | H |

## 2. 范围裁定

### 2.1 范围内

- ADK 三 Agent:初筛(LLM)/ 生成(LLM + 检索)/ 校验核算(纯规则引擎),Sequential + Loop 编排,Loop 重试设最大轮数
- A2A **单跳**:初筛 Agent(host)↔「生成 + 校验」一个远程服务;Loop 重试留在远程服务进程内
- PostgreSQL:parts / prices / requirements / builds 版本树(表结构见设计方案 §六)
- pgvector:限定一个用途——模糊偏好 → 候选过滤
- Redis:限定两个用途——跨进程会话热上下文(P5 拆分后才有必要性)+ embedding/LLM 结果缓存
- 版本快照、v1→vN diff、日期化报价、Markdown 导出
- 增量改单:ChangeRequest 只支持三类意图(换某件 / 调预算 ±N / 改约束),其余降级为整单重生成并告知用户
- 兼容性规则 12 条全做(条目见设计方案 §五),引擎必须支持**字段缺失 → 该条规则输出 unknown 而非崩溃**
- 数据:**AM5 单平台**、每类 20–30 个 SKU、手工维护价格 CSV
- 客户端:ADK dev UI + CLI 脚本

### 2.2 范围外(每项注明去向)

| 项 | 去向 | 理由 |
|---|---|---|
| Next.js/React Web 客户端(设计方案 §七) | PRD Epic E1(阶段 1) | 非学习目标,单人项目最大工时黑洞 |
| 分享图 / 公开链接 | PRD FR-602/603(阶段 1) | 依赖 E1 |
| 真人用户验证(原 M5 验收) | PRD 阶段 1 退出标准 | MVP 验收改为 §1.2 验收用例(自动化回归 + 人工演示) |
| LGA1851 / Intel 平台数据 | PRD Epic E3;数据扩充备忘 | 纯数据无代码,砍掉省一半芯片组支持表维护 |
| 价格半自动更新(原 M6) | PRD Epic E2(阶段 2) | 手工 CSV + 快照日期已足够支撑架构叙事 |
| sessions.profile 跨会话用户画像 | PRD FR-701(阶段 3) | MVP 的"记忆"只到会话级 |
| 多用途混合(use_cases 多元素加权) | PRD §3.3 暂缓项 | MVP 只处理单一主用途 |
| PassMark 抓取脚本 | 降级为可选兜底:60–100 条 CPU/GPU 分数手工录入 | 性价比字段设为 optional,报告优雅降级 |

### 2.3 与设计方案 M1–M6 的差异

| 原里程碑 | 本文处理 | 理由 |
|---|---|---|
| M1 数据 + 规则 | 保留为 P1,基本不变 | 规则引擎是产品脊柱且零 LLM 成本,先做正确 |
| M2 单进程流水线 | 保留为 P2,但生成 Agent 先用**纯 SQL 结构化过滤**选件,不等 embedding | 消除 M2 对 pgvector 的隐式依赖,端到端更早跑通 |
| M3 三 Agent 全拆 A2A | **降级为单跳 A2A**,移到 P5 | 一跳即覆盖 A2A 全协议面(agent card / task / 消息 schema);对已稳定的流水线做传输层改造,风险最低;"生成↔校验"重试回路跨 A2A 做会非常痛苦 |
| M4 记忆基座三合一 | **拆散**:pgvector → P3(它是检索能力不是记忆),版本树 → P4(业务核心),Redis → P6(拆进程后才有诚实的存在理由) | 按真实依赖排序,避免"为用而用" |
| M5 客户端 + 真人验证 | 移出 MVP(见 §2.2) | 非学习目标 |
| M6 可选项 | 移出 MVP | 原本即可选 |

### 2.4 范围变更规则

新想法一律进 PRD backlog(§3.3 暂缓项或 §5.10 Epic),本文范围**只准收缩不准扩张**。MVP 冻结条件 = §1.2 验收用例全绿,冻结后才允许启动 PRD 阶段 1。

## 3. 实现阶段拆分

原则:每个阶段结束都有能跑、能演示的东西;LLM 介入越晚越好(先把不花钱的确定性部分做对)。

### 3.0 阶段总览

| 阶段 | 目标 | 验证 | 估时 |
|---|---|---|---|
| P0 环境与骨架 | 开发环境一次配齐 | 两容器健康 + hello agent 可对话 | 0.5–1 天 |
| P1 数据底座与规则引擎 | 零 LLM 的确定性地基 | golden set pytest 全绿(用例 B) | 2–3 天 |
| P2 单进程三 Agent 流水线 | 首个端到端时刻 | dev UI 出全 pass 配置单(用例 A) | 2–3 天 |
| P3 pgvector 语义选件 | 模糊偏好可用 | 语义命中可解释(用例 F) | 1–2 天 |
| P4 版本快照与增量改单 | 架构主线核心成型 | v1→v3 diff 回放 + 导出(用例 C/D/E) | 2–3 天 |
| P5 A2A 单跳拆分 | 协议学习目标落地 | 双进程体验与 P4 一致(用例 G) | 1–2 天 |
| P6 Redis 会话层 | 记忆基座补全 | 重启恢复会话(用例 H) | 1 天 |

合计约 **10–15 个全职工作日**(业余节奏约 5–8 周)。P6 结束触发 MVP 冻结检查。

**激进收敛备选**(时间紧张时启用):MVP = P0–P4,A2A 与 Redis 移出为后续两个独立追加阶段;额外收缩:规则精简——暂缓依赖稀缺字段的 #10 显卡供电接口、#12 散热器解热能力两条,以及 #6 中的水冷排尺寸子项(保留风冷限高校验),其余全做(编号见设计方案 §五);改单只支持"换某一件";不做性能分。两案之间切换零浪费——推荐方案即备选方案 + 两个追加阶段。

### 3.1 P0 环境与骨架

- 交付:git 仓库、go module、docker-compose(PG16 + pgvector、Redis7)、一个 hello-world ADK-Go Agent(嵌入内置 launcher)
- DoD:`docker compose up -d` 后两容器(PG16+pgvector 镜像、Redis7)健康;launcher 的 Web UI 里能与 hello agent 对话
- 不做:任何业务逻辑

### 3.2 P1 数据底座与规则引擎

- 交付:数据导入脚本(Python:pc-part-dataset + dbgpu → AM5 裁剪入库)、价格 CSV 及导入、规则引擎纯 Go 包(`internal/rules`)+ 单测
- DoD:20 组人工构造配置(含故意错配)`go test` 全绿;CLI 脚本对手写 build JSON 输出校验报告;**任一字段缺失时对应规则输出 unknown,进程不崩溃**(用例 B)
- 引用:规则条目见设计方案 §五;表结构见 §六;数据操作步骤见本文 §5
- 不做:LLM、Agent、embedding

### 3.3 P2 单进程三 Agent 流水线

- 交付:初筛/生成/校验三 Agent,Sequential + Loop 编排;生成 Agent 用**纯 SQL 结构化过滤**选件;校验 Agent 将 P1 引擎包装为 tool;Loop 最大重试轮数与熔断
- DoD:dev UI 输入「8000 元 2K 玩黑神话」,一轮对话产出全 pass 配置单 + 人话解释报告(用例 A)
- 引用:Agent 职责与消息流见设计方案 §三;RequirementSpec/BuildDraft/ValidationReport schema 见 §四(`internal/schemas` Go struct + 校验实现)
- 不做:pgvector、版本落库、A2A

### 3.4 P3 pgvector 语义选件

- 交付:embedding 生成脚本、parts.embedding 填充、生成 Agent 增加语义候选检索路径(与 SQL 过滤并联)
- DoD:「要安静的显卡」「白色海景房」两条查询各取 top-5 候选,人工逐条核对能否指出命中的参数字段(噪音/散热、颜色/侧透),每条查询 ≥3/5 候选可指出命中字段即通过;P2 端到端不回归(用例 F)
- 不做:embedding 模型调优、召回评测体系(记入 PRD 阶段 1 打磨)

### 3.5 P4 版本快照与增量改单

- 交付:requirements/builds 落库(parent_id 版本树)、报价绑定快照日期、ChangeRequest 三类意图解析、增量重生成(锁定未涉及件)、文本 diff、Markdown 导出(含免责声明)
- DoD:对话脚本 v1 →「降 500」→ v2 →「换 A 卡」→ v3;版本树可回放、v1→v3 diff 正确;导出 md 含快照日期与免责边界(用例 C/D/E)
- 前置:ChangeRequest schema 已补入设计方案 §四(2026-07-26),实现时以该定义为准
- 不做:卡片式改单交互(PRD OQ-3)

### 3.6 P5 A2A 单跳拆分

- 交付:「生成 + 校验」流水线包装为 A2A 远程服务(独立进程,a2a-go);初筛 Agent 改为远程消费方;RequirementSpec/ChangeRequest 进、BuildDraft + ValidationReport 出,schema 校验(共用 `internal/schemas`)
- DoD:两进程分别启动,dev UI 端到端体验与 P4 完全一致;日志可见跨进程 A2A 消息且 schema 校验通过(用例 G)
- 不做:三 Agent 全拆(见 §2.3 M3 条)、鉴权/部署

### 3.7 P6 Redis 会话层与缓存

- 交付:会话热上下文(当前需求单 + 配置草稿)写 Redis 带 TTL,供两进程共享;embedding/LLM 结果缓存
- DoD:kill 掉 host 进程重启后,同一会话继续改单成功;缓存命中有日志佐证(用例 H)
- 不做:跨会话长期画像(PRD FR-701)

## 4. 技术选型与环境

### 4.1 依赖清单

- **服务端(Go 1.26+)**:google.golang.org/adk(ADK-Go)、a2a-go、pgx + pgvector-go、go-redis;测试用标准库 testing(golden set 由 `go test` 驱动)。
- **数据管道(Python 3.12+,一次性脚本)**:uv、dbgpu、pandas、openai SDK(调百炼兼容端点生成 embedding)。

**版本以实现当日官方文档为准,首装跑通后回填 [技术选型](../tech/技术选型.md) 的版本锁定表**;本文不写死任何 import 路径或 API 签名(ADK-Go/a2a-go 迭代快)。选型依据见技术选型 ADR-001~005。

### 4.2 本地基础设施

- docker-compose:`pgvector/pgvector:pg16` + `redis:7`;数据卷持久化
- Windows 11 本机:Docker Desktop(WSL2 后端);注意 CRLF(`.gitattributes` 统一 LF)、路径分隔符不进代码
- `.env` 约定:百炼 API key、PG/Redis 连接串;`.env` 入 `.gitignore`,提供 `.env.example`

### 4.3 模型接入策略

阿里百炼 OpenAI 兼容端点一站式(技术选型 ADR-004):初筛用低价档 qwen、生成用旗舰档 qwen、embedding 用百炼 text-embedding 系(全库 20–30 SKU × 8 类,量极小);具体型号实现当日查百炼文档,不写死。开发期设 token 预算上限并开启 LLM 结果缓存(P6 前先用进程内 map 顶替,P6 迁 Redis)。

### 4.4 目录结构与工程约定

```
cmd/
  host/        # 初筛 Agent + 内置 launcher(dev UI)入口
  buildsvc/    # 「生成+校验」A2A 远程服务入口(P5 起)
internal/
  agents/      # 初筛 / 生成 / 校验 三 Agent 与编排
  rules/       # 规则引擎:纯 Go,禁止 import agents/LLM 相关包(零 LLM 的工程保证)
  schemas/     # RequirementSpec / BuildDraft / ValidationReport / ChangeRequest 的 struct 与校验
  store/       # pgx / redis 访问
  export/      # Markdown 导出
scripts/       # Python 数据管道(导入/embedding/性能分)+ CLI 演示脚本
```

关键纪律:`internal/rules` 的零 LLM 隔离用 golangci-lint depguard 兜底;schema 只在 `internal/schemas` 定义一份,Agent 与 A2A 层共用。

## 5. 数据准备 runbook

1. **拉取 pc-part-dataset**:下载 JSON/CSV;裁剪标准——AM5 平台(CPU 取 AM5 全部在售主流型号,主板取 B650/X670/B850/X870 系),每类保留 20–30 个主流 SKU
2. **字段核对**:对照设计方案 §五的 12 条规则**逐条派生**所需字段(插槽、芯片组、内存代数/频率、显卡长度、机箱限长/限高/板型支持、电源功率/供电接口、散热器高度/解热 TDP、主板 M.2 槽位数、CPU 核显有无等,以规则派生结果为全集,勿以本括号为准),核对每个入库 SKU 的字段完整度;缺失字段**保留 null 入库**(规则层输出 unknown),不编造
3. **dbgpu 取数**:pip 安装,补显卡详情(长度/供电接口/TDP)
4. **价格 CSV**:列格式 `sku,price_cny,source,captured_at`;手工维护,每次更新整批追加(prices 只增不改,见设计方案 §六);MVP 期更新频率不做承诺,只保证日期真实
5. **性能分**:手工录入 60–100 条 CPU/GPU PassMark 分兜底;passmark-scraper 为可选优化
6. **embedding 生成**:对 `型号 + 关键参数摘要 + 风格标签(静音/白色/侧透)` 文本生成向量,填充 parts.embedding(P3 执行)
7. 表结构与字段口径一律见设计方案 §六,本文不复制 DDL

## 6. 风险与应对

| 风险 | 应对 |
|---|---|
| 开发期 LLM 成本失控 | 分层模型 + 结果缓存 + token 预算上限(§4.3);P1 全程零 LLM |
| 开源数据字段缺失导致规则失真 | "缺失 → unknown"写进 P1 DoD;入库时字段核对(§5.2);不编造数据 |
| ADK-Go / a2a-go API 迭代快 | 实现当日先查官方文档;首装后锁版本回填技术选型文档;不在文档写死 API |
| ADK-Go 社区资料少于 Python 版 | 对照 Python 版文档/示例翻译成 Go API;优先看 pkg.go.dev 与 adk-go 仓库示例 |
| Loop 重试风暴(生成↔校验死循环) | 最大轮数 + 熔断,超限后带着失败报告出栈,向用户如实说明 |
| Windows 本地环境坑 | WSL2 后端、LF 统一、路径不硬编码(§4.2) |
| 范围蔓延 | §2.4 纪律:新想法进 PRD backlog,本文只准收缩 |

## 7. MVP 之后

1. **冻结动作**:§1.2 用例全绿后打 tag(`mvp-freeze`),README 补充演示说明/GIF
2. **移交 PRD**:§2.2 去向清单中的各项按 PRD 路线图(§9)排期,从阶段 1(Web 客户端 + 真人验证)开始
3. **设计方案维护纪律**(历史待办——ChangeRequest schema、8 大件勘误、ADK-Go/百炼措辞、职责批注——已于 2026-07-26 完成修订):实现中发现的 schema 字段调整,回写设计方案 §四而非在代码里静默漂移。
