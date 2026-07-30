# CLAUDE.md

装机配置单 Agent:对话式 DIY 装机助手,多 Agent 流水线(初筛/生成/校验)+ A2A + PG/pgvector/Redis。个人学习向项目。

## 常用命令

```bash
docker compose up -d             # PG → localhost:15432,Redis → localhost:16379
go run ./cmd/host                # console 模式(快速验证模型连通)
go run ./cmd/host web api webui  # ADK dev UI,http://localhost:8080(三个子命令缺一不可)
go build ./... && go vet ./...
```

- 端口非默认:本机原生 PostgreSQL 17 与 Redis 服务常驻占用 5432/6379,故 compose 用 15432/16379。
- `.env`(不入库)存 `DASHSCOPE_API_KEY`;工作空间专属端点用 `DASHSCOPE_BASE_URL` 覆盖,见 `.env.example`。
- `db/init/` 的 SQL 仅数据卷首次初始化执行,改动后需 `docker compose down -v` 重建。

## 权威文档(冲突仲裁)

| 文档 | 权威范围 |
|---|---|
| docs/product/PRD.md | 产品长期方向 |
| docs/product/mvp.md | MVP 范围与 P0–P6 阶段(短期执行唯一依据) |
| docs/装机Agent设计方案.md | 架构 / A2A schema / 规则表 / 表结构 |
| docs/tech/P2-流水线设计.md | P2 实现层:编排拓扑 / 提示词 SOP / tool 契约 / Loop 控制(schema 口径仍以设计方案 §四 为准) |
| docs/tech/P3-语义选件设计.md | P3 实现层:embedding 素材与文本 / 语义检索路径 / search_parts_semantic 契约 |
| docs/tech/技术选型.md | 栈级决策(ADR)+ 版本锁定表 |
| docs/tech/工程实践指引.md | 各阶段开工前扫对应小节;评审对照 §九检查单 |

## 工程纪律

- **版本纪律**:ADK-Go/a2a-go 迭代快,文档不写死 import 路径与 API 签名;首装后回填技术选型.md 末尾版本锁定表。模型型号只写在代码常量,不进文档(ADR-004)。
- **目录纪律**:布局唯一出处 mvp.md §4.4;`internal/*`、`scripts/` P1 起按需建,不为架构感提前拆(工程实践指引 §一.3)。
- **`internal/rules` 零 LLM**:P1 建包时同时配 golangci-lint depguard。
- **schema 单一出处**:`internal/schemas` 定义一份,字段变更回写设计方案 §四,不在代码里静默漂移。
- 当前进度:P2 完成(三 Agent 流水线,用例 A 端到端 pass,Pass@3=3/3);进行中 P3(pgvector 语义选件,设计见 docs/tech/P3-语义选件设计.md)。

## 已知环境坑(Windows)

- ADK openaimodel 走 OpenAI **Responses API**(非 Chat Completions),百炼 compatible-mode 已支持;该包标注 EXPERIMENTAL。
- Docker Desktop 若启动崩溃报 unix socket「cannot be accessed」:Windows 层删不掉损坏 socket,用 `wsl -d docker-desktop -e rm -f /mnt/host/c/<路径>` 删(2026-07-26 实修:dockerInference、engine.sock 等三处)。**根因是 Windows 快速启动(HiberbootEnabled=1)把 socket 冻成死文件,每次关机开机必复发,关掉快速启动才断根**(2026-07-27 确认)。
- `go`/`docker` 不在 PATH 时:Go 装在 `C:\Program Files\Go\bin`。
