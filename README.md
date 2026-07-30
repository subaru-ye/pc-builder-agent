# 装机配置单 Agent

> 对话式 DIY 装机助手:说清预算和用途,得到**保证兼容**、**带日期化报价**、**可多轮修改**的装机配置单。
>
> 个人学习向项目,目标技术栈:多 Agent 流水线 + A2A 协议 + 记忆基座。当前状态:MVP 开发中(P1 数据底座与规则引擎)。

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
| 客户端 | MVP:ADK-Go 内置 dev UI;阶段 1:Next.js + React |

选型理由与取舍记录见 [docs/tech/技术选型.md](docs/tech/技术选型.md)(ADR 形式)。

## 文档导航

| 文档 | 职责 |
|---|---|
| [PRD](docs/product/PRD.md) | 产品定义、用户与场景、功能需求、路线图、成功指标(长期方向权威) |
| [MVP 实现指导](docs/product/mvp.md) | MVP 范围裁定、阶段拆分 P0–P6、验收用例、数据准备 runbook(短期执行权威) |
| [设计方案](docs/装机Agent设计方案.md) | 架构、A2A 消息 schema、兼容性规则表、数据表结构(技术设计权威) |
| [技术选型](docs/tech/技术选型.md) | 语言/框架/模型供应商决策记录(栈级选择权威) |
| [工程实践指引](docs/tech/工程实践指引.md) | 按模块/阶段筛选的 Agent 工程实践要点与阶段检查单 |
| [产品调研](docs/装机Agent产品调研.md) | 竞品格局与数据源论证(历史依据) |

## 快速启动

前置:Go 1.26+、Docker Desktop(WSL2 后端)、阿里百炼 API key。

```bash
docker compose up -d        # PG → localhost:15432,Redis → localhost:16379(非默认端口,避让本机原生服务)
```

复制 `.env.example` 为 `.env`,填入 `DASHSCOPE_API_KEY`;若创建 key 时控制台显示了工作空间专属「OpenAI 兼容地址」,一并填入 `DASHSCOPE_BASE_URL`。

```bash
go run ./cmd/host                # console 模式,快速验证模型连通
go run ./cmd/host web api webui  # Web UI(三个子命令缺一不可):http://localhost:8080
```

## MVP 进度

- [x] 产品调研 / 设计方案 / PRD / MVP 计划 / 技术选型
- [x] P0 环境与骨架
- [x] P1 数据底座与规则引擎
- [ ] P2 单进程三 Agent 流水线
- [ ] P3 pgvector 语义选件
- [ ] P4 版本快照与增量改单
- [ ] P5 A2A 单跳拆分
- [ ] P6 Redis 会话层

## 免责声明

- 兼容性校验不覆盖 BIOS 版本支持、内存 QVL 等长尾项,装机前请自行核对;
- 所有价格为带日期的快照参考价,非实时行情;
- 配置建议仅供参考,不构成购买承诺。

## License

[MIT](LICENSE)
