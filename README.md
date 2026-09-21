# 装机配置单 Agent

> 对话式 DIY 装机助手：说清预算和用途，得到**逐项兼容核验**、**带日期化报价**、**可多轮修改**的候选方案；缺项保留待解决，满足交付条件后保存正式版本。
>
> 个人学习向项目,目标技术栈:多 Agent 流水线 + A2A 协议 + 记忆基座。当前状态:MVP P0–P6 已封板,P7–P9 已完成;P10 Harness v2 机器复验已完成,仅待 3 人真人盲评;P11 已完成,P12B 暂停;Agent Harness 2.0 与本地 Supabase Auth 已实现。

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

默认 planning 模式由模型在有限额度内检索、补充资料和调整方案；规则引擎决定兼容性和报价结果。未解决时保存候选与具体问题，允许继续对话。

## 技术栈

| 层 | 选型 |
|---|---|
| 服务端 | Go + [ADK-Go](https://github.com/google/adk-go)(多 Agent 编排)+ [a2a-go](https://github.com/a2aproject)(A2A 协议) |
| 数据层 | PostgreSQL 16 + pgvector、Redis 7(pgx / go-redis) |
| 模型 | 统一 Responses 适配层:阿里百炼、MiMo、通用 OpenAI-compatible；三个角色独立配置 |
| 数据管道 | Python(pc-part-dataset / dbgpu 导入、embedding 生成) |
| 客户端 | Next.js 16 + React 19；ADK dev UI 仅用于调试 |

选型理由与取舍记录见 [docs/tech/技术选型.md](docs/tech/技术选型.md)(ADR 形式)。

## 文档导航

完整入口见 [docs/README.md](docs/README.md)。

| 文档 | 内容 |
|---|---|
| [产品需求](docs/product/PRD.md) | 产品定义、功能边界和质量目标 |
| [当前路线图](docs/product/路线图.md) | 未完成事项、阻塞与验证条件 |
| [系统架构](docs/tech/系统架构.md) | 服务边界、契约与数据职责 |
| [本地运行与部署](docs/ops/本地运行与部署.md) | 启动、迁移、健康检查和排障 |
| [评估](docs/eval/README.md) | 测试方法、评估集、基线与运行记录 |
| [数据规则](docs/data/数据获取与发布规则.md) | 证据、价格和安全发布约束 |

## 快速启动

前置:Go 1.26+、Docker Desktop(WSL2 后端)，以及所选供应商的 API key。

```bash
docker compose up -d        # PG → localhost:15432,Redis → localhost:16379(非默认端口,避让本机原生服务)
go run ./cmd/migrate up     # 应用 PostgreSQL 编号迁移
```

复制 `.env.example` 为 `.env`。screening、builder、embedding 可独立配置 `PROVIDER/MODEL/API_KEY/BASE_URL`;未设置 provider 时兼容旧配置并默认百炼。当前示例固定 Builder `qwen3.8-max-0902`、Screening `deepseek-v4-flash-0731` 并清空切换链；显式配置 `*_MODEL_CHAIN` 才启用链内额度切换，不跨供应商回退。MiMo 当前只用于 chat，builder 须关闭思考；embedding 继续使用百炼。`BUILD_HARNESS_MODE` 默认 `planning`，由模型在最多 8 次往返内检索、补充资料并调用客观校验；`v2` 和 `legacy` 仅用于显式历史诊断，失败不自动回退。显式连通性检查使用 `go run ./cmd/modelcheck -role screening|builder|embedding`。分享功能还要求独立的 `SHARE_TOKEN_SECRET`,生成方法见[本地运行手册](docs/ops/本地运行与部署.md)。账号功能默认关闭；需要本地账号时运行 `go run ./cmd/authsetup`，再用 `docker-compose.auth.yml` 启动独立 GoTrue。

```bash
go run ./cmd/authsetup
docker compose -f docker-compose.yml -f docker-compose.auth.yml up -d postgres redis auth
go run ./cmd/migrate up
```

```bash
go run ./cmd/buildsvc  # 终端 1:启动「生成 + 校验」A2A 服务(http://localhost:8081)

# 终端 2:启动初筛 host + dev UI;语义选件可能超过 ADK 默认写超时,故统一放宽到 10 分钟
go run ./cmd/host web --write-timeout=10m api --sse-write-timeout=10m webui
# 浏览器访问 http://localhost:8080/ui/

# 终端 3:产品 API
go run ./cmd/api
# 存活/完整就绪检查:http://localhost:8082/healthz 和 /readyz

# 终端 4:Web 配置工作台
cd web
pnpm install --frozen-lockfile
pnpm dev
# 浏览器访问 http://localhost:3000
```

P11 数据发布只走显式人工流程，不使用本机定时任务。以下命令只使用确定性代码，不调用大模型：

```powershell
uv run --project scripts/data pcdata source check
uv run --project scripts/data pcdata bootstrap
uv run --project scripts/data pcdata health
```

采集需要逐个来源显式执行 `pcdata collect` → `normalize` → `review` → `publish`，步骤与门禁见[发布管道](docs/tech/数据获取与发布管道.md)。当前唯一外部来源是固定映射的 AMD 官方 CPU 具体型号页，串行条件请求，严格核对型号并只生成 socket、支持芯片组、TDP、核显和官方名称的确定性 evidence；政策、身份或页面结构变化会隔离来源。

人工价格 observation、安全选价和动态过期提示可用；自动价格来源和每日任务保持禁用。操作见[价格任务](docs/ops/价格任务.md)，依据见[数据来源决策](docs/data/数据来源决策.md)，未完成工作见[路线图](docs/product/路线图.md)。

## 当前能力

Web 工作台支持需求确认、配置生成、规则校验、增量改单、版本对比、分享和导出；默认 planning，具备本地可选账号与数据安全发布管道。本地缺规格可用已读取正文补充到会话快照，保留本地报价及历史版本。人工价格维护可用，自动价格来源保持禁用。

本次交付范围与限制见[业务收尾有限验收](docs/eval/planning-v2/业务收尾有限验收-20260915.md)。历史质量结果保存在[评估目录](docs/eval/README.md)，不作为当前版本自动通过验收的声明；不要求当前版本追平历史分数。

## 免责声明

- 兼容性校验不覆盖 BIOS 版本支持、内存 QVL 等长尾项,装机前请自行核对;
- 所有价格为带日期的快照参考价,非实时行情;
- 配置建议仅供参考,不构成购买承诺。

## License

[MIT](LICENSE)
