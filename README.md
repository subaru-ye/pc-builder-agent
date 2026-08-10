# 装机配置单 Agent

> 对话式 DIY 装机助手:说清预算和用途,得到**保证兼容**、**带日期化报价**、**可多轮修改**的装机配置单。
>
> 个人学习向项目,目标技术栈:多 Agent 流水线 + A2A 协议 + 记忆基座。当前状态:MVP P0–P6 已封板,P7–P9 已完成;P10 的确定性门禁已实现,Live Pass³ 与真人盲评尚未完成。

## 为什么做

纯 LLM 配单不可靠——预算分配失衡、知识过时、幻觉搭配;传统攒机工具有兼容检查但没有对话能力。本项目的立场是:

**LLM 只负责理解意图和解释结果,兼容性与核算的最终判定权属于规则引擎。**

三个核心承诺:

1. **兼容硬校验**:每份配置单经过规则引擎逐项校验(插槽/内存代数/限长限高/电源余量等),数据不足时诚实输出 unknown 而非冒充通过;
2. **日期化报价**:总价绑定报价快照日期,不承诺实时,承诺透明;
3. **增量改单**:"降 500 优先砍哪""换成 A 卡"只动必要的件,每次修改产生新版本,历史可回放、可 diff。

## 架构

```
用户对话
   ↓
┌──────────────┐  A2A: RequirementSpec / ChangeRequest  ┌────────────────────────────────┐
│  初筛 Agent   │ ─────────────────────────────────────→ │  生成 Agent  →  校验核算 Agent    │
│  (LLM)       │ ←───────────────────────────────────── │ (LLM+pgvector)  (纯规则引擎,零LLM) │
└──────────────┘   BuildDraft + ValidationReport         └────────────────────────────────┘
                                                              ↓
                                          PostgreSQL(零件库 / 配置单版本树 / 报价快照)
                                          + pgvector(语义选件)+ Redis(会话热上下文)
```

校验不通过时在服务内自动换件重试(有上限),LLM 只把机器可读的报告翻译成人话。

## 技术栈

| 层 | 选型 |
|---|---|
| 服务端 | Go + [ADK-Go](https://github.com/google/adk-go)(多 Agent 编排)+ [a2a-go](https://github.com/a2aproject)(A2A 协议) |
| 数据层 | PostgreSQL 16 + pgvector、Redis 7(pgx / go-redis) |
| 模型 | 阿里百炼 Qwen(OpenAI 兼容端点,chat 分档 + embedding) |
| 数据管道 | Python(pc-part-dataset / dbgpu 导入、embedding 生成) |
| 客户端 | MVP:ADK-Go 内置 dev UI;阶段 1:Next.js 16 + React 19 |

选型理由与取舍记录见 [docs/tech/技术选型.md](docs/tech/技术选型.md)(ADR 形式)。

## 文档导航

| 文档 | 职责 |
|---|---|
| [PRD](docs/product/PRD.md) | 产品定义、用户与场景、功能需求、路线图、成功指标(长期方向权威) |
| [MVP 实现指导](docs/product/mvp.md) | 已封板 MVP 范围、P0–P6 阶段拆分与验收历史 |
| [阶段 1 实现指导](docs/product/stage1.md) | P7–P10 Web 产品化范围、顺序、退出标准与阶段 2/3 交接(当前短期执行权威) |
| [设计方案](docs/装机Agent设计方案.md) | 架构、A2A 消息 schema、兼容性规则表、数据表结构(技术设计权威) |
| [P2 流水线设计](docs/tech/P2-流水线设计.md) | P2 三 Agent 流水线实现层:编排拓扑、提示词 SOP、tool 契约、Loop 控制 |
| [P7 产品 API 设计](docs/tech/P7-产品API与会话状态机设计.md) | 产品会话状态机、匿名身份、后台 run、SSE 与配置读模型 |
| [P8 Web 客户端设计](docs/tech/P8-Web客户端设计.md) | Next.js 工作台、需求确认、配置/校验/版本交互与响应式 |
| [P9 分享与导出设计](docs/tech/P9-分享与导出设计.md) | 共享 presenter、只读链接、Markdown 与分享图 |
| [P10 评测与阶段验收](docs/tech/P10-评测与阶段验收.md) | 50+ golden、Web/Live E2E、真人 rubric 与退出门禁 |
| [产品 API 契约](docs/api/openapi.yaml) | 阶段 1 HTTP DTO、路径与错误响应唯一线格式 |
| [设计上下文](DESIGN_CONTEXT.md) | Linear 派生的产品视觉目标;具体 token/规则见 DESIGN.md、UI_RULES.md |
| [技术选型](docs/tech/技术选型.md) | 语言/框架/模型供应商决策记录(栈级选择权威) |
| [工程实践指引](docs/tech/工程实践指引.md) | 按模块/阶段筛选的 Agent 工程实践要点与阶段检查单 |
| [产品调研](docs/装机Agent产品调研.md) | 竞品格局与数据源论证(历史依据) |

## 快速启动

前置:Go 1.26+、Docker Desktop(WSL2 后端)、阿里百炼 API key。

```bash
docker compose up -d        # PG → localhost:15432,Redis → localhost:16379(非默认端口,避让本机原生服务)
go run ./cmd/migrate up     # 应用 PostgreSQL 编号迁移
```

复制 `.env.example` 为 `.env`,填入 `DASHSCOPE_API_KEY`;若创建 key 时控制台显示了工作空间专属「OpenAI 兼容地址」,一并填入 `DASHSCOPE_BASE_URL`。`SCREENING_MODEL`、`BUILDER_MODEL` 与 `EMBEDDING_MODEL` 可按控制台实际免费额度独立切换，百炼不会在额度耗尽后自动改用其他 Model Code。P9 还要求独立的 `SHARE_TOKEN_SECRET`,生成方法见[本地运行手册](docs/ops/阶段1本地运行与部署准备.md)。

```bash
go run ./cmd/buildsvc  # 终端 1:启动「生成 + 校验」A2A 服务(http://localhost:8081)

# 终端 2:启动初筛 host + dev UI;语义选件可能超过 ADK 默认写超时,故统一放宽到 10 分钟
go run ./cmd/host web --write-timeout=10m api --sse-write-timeout=10m webui
# 浏览器访问 http://localhost:8080/ui/

# 终端 3:P7 产品 API
go run ./cmd/api
# 存活/完整就绪检查:http://localhost:8082/healthz 和 /readyz

# 终端 4:P8 Web 配置工作台
cd web
pnpm install --frozen-lockfile
pnpm dev
# 浏览器访问 http://localhost:3000
```

## MVP 进度

- [x] 产品调研 / 设计方案 / PRD / MVP 计划 / 技术选型
- [x] P0 环境与骨架
- [x] P1 数据底座与规则引擎
- [x] P2 单进程三 Agent 流水线
- [x] P3 pgvector 语义选件
- [x] P4 版本快照与增量改单
- [x] P5 A2A 单跳拆分
- [x] P6 Redis 会话层与 embedding 缓存
- [x] P7 产品 API 与会话状态机
- [x] P8 Web 配置工作台
- [x] P9 分享与导出
- [ ] P10 评测与发布准备(50 组 golden、自动门禁与验收工具已完成;Live/真人待完成)

2026-08-09 封板验收结果:PostgreSQL/pgvector/Redis 真实集成测试无跳过;Python 数据流水线 104 项测试通过;用例 A–H 全部验证,包括全 pass 配单、语义召回、v1→v3 回放/diff/Markdown 导出、A2A schema/contextID 以及 kill host 后从 Redis 恢复同一会话继续改单。

阶段 1 的 P7–P9 已实现:P7 提供产品 API、匿名会话、需求确认、后台 run 与 Redis SSE;P8 提供 Next.js 工作台、配置/校验/版本/diff 与响应式交互;P9 提供不可变版本分享、所有者撤销、最小披露的 SSR 只读页、公开 Markdown 与 1200×630 PNG。P10 已完成 50 组 golden、Go/Web/Python 自动门禁、Live 记录器和真人验收工具;2026-08-10 检查点的确定性门禁全绿,但真实模型矩阵因连续上游失败未形成 18 个 Pass³ 样本,真人盲评仍为 0/3,因此尚未创建 `stage1-freeze`。

## 免责声明

- 兼容性校验不覆盖 BIOS 版本支持、内存 QVL 等长尾项,装机前请自行核对;
- 所有价格为带日期的快照参考价,非实时行情;
- 配置建议仅供参考,不构成购买承诺。

## License

[MIT](LICENSE)
