# CLAUDE.md

装机配置单 Agent:对话式 DIY 装机助手,多 Agent 流水线(初筛/生成/校验)+ A2A + PG/pgvector/Redis。个人学习向项目。

## 常用命令

```bash
docker compose up -d             # PG → localhost:15432,Redis → localhost:16379
go run ./cmd/migrate up          # 应用 PostgreSQL 编号迁移
go run ./cmd/buildsvc            # 终端 1:A2A 生成+校验服务
go run ./cmd/host web --write-timeout=10m api --sse-write-timeout=10m webui  # 终端 2:ADK dev UI,http://localhost:8080/ui/
go run ./cmd/api                 # 终端 3:产品 API,http://localhost:8082
go run ./cmd/modelcheck -role screening  # 显式上游检查;普通启动/测试不调用模型
uv run --project scripts/data pcdata source check  # P11 静态来源检查;默认不联网
uv run --project scripts/data pcdata price health  # P12A 价格快照与动态年龄
cd web && pnpm dev               # 终端 4:产品 Web,http://localhost:3000
go build ./... && go vet ./...
```

- 端口非默认:本机原生 PostgreSQL 17 与 Redis 服务常驻占用 5432/6379,故 compose 用 15432/16379。
- `.env`(不入库)存所选供应商 Key 和独立的 `SHARE_TOKEN_SECRET`;三个模型角色通过 `*_PROVIDER/MODEL/API_KEY/BASE_URL` 独立配置,见 `.env.example`。
- 数据库结构统一由 `db/migrations/` 的 goose 编号迁移管理；保留数据卷运行 `go run ./cmd/migrate up`，禁止为普通迁移执行 `down -v`。

## 权威文档(冲突仲裁)

| 文档 | 权威范围 |
|---|---|
| docs/product/PRD.md | 产品长期方向 |
| docs/product/mvp.md | 已封板 MVP 范围与 P0–P6 验收历史 |
| docs/product/stage1.md | P7–P10 Web 产品化范围、顺序与退出标准(当前短期执行唯一依据) |
| docs/product/stage1-backlog.md | 阶段 1 未封口项、外部阻塞、恢复条件与最终封板步骤 |
| docs/product/stage2.md | P11–P14 本机数据自动化范围、顺序、数据分层与阶段退出标准 |
| docs/data/数据获取与发布规则.md | 数据来源、字段证据、价格选择、模型禁区和条件发布强制规则 |
| docs/tech/P11-数据获取与发布管道设计.md | 本机调度、HTTP 安全采集、风险分类、不可变 release 与数据库演进 |
| docs/ops/P11本机数据任务.md | P11 初始化、计划任务安装、日志、补跑和卸载操作 |
| docs/tech/P12-价格观察与安全选价设计.md | P12A observation/选价/新鲜度与 P12B 自动来源门禁 |
| docs/ops/P12价格任务.md | P12 CSV、review/publish、inbox、health 与排障操作 |
| docs/装机Agent设计方案.md | 架构 / A2A schema / 规则表 / 表结构 |
| docs/tech/P2-流水线设计.md | P2 实现层:编排拓扑 / 提示词 SOP / tool 契约 / Loop 控制(schema 口径仍以设计方案 §四 为准) |
| docs/tech/P3-语义选件设计.md | P3 实现层:embedding 素材与文本 / 语义检索路径 / search_parts_semantic 契约 |
| docs/tech/P4-版本快照与增量改单设计.md | P4 实现层:builds/requirements 版本表 / ChangeRequest 意图解析 / 锁定校验 / cmd/builds 回放·diff·导出 |
| docs/tech/P5-A2A单跳拆分设计.md | P5 实现层:buildsvc A2A 远程服务(生成+校验)/ host 初筛远程消费方 / 出站只传三样 + 入站 schema 校验 / contextID 会话映射 |
| docs/tech/P7-产品API与会话状态机设计.md | P7 实现层:产品 API / 匿名所有权 / 需求确认状态机 / run 与 SSE / 产品读模型 |
| docs/tech/P8-Web客户端设计.md | P8 实现层:Next.js 工作台 / 需求确认 / 配置·校验·版本 / 响应式与无障碍 |
| docs/tech/P9-分享与导出设计.md | P9 实现层:共享 presenter / Markdown / 分享 token / 只读页与分享图 |
| docs/tech/P10-评测与阶段验收.md | P10 实现层:50+ golden / Web 与 Live E2E / 真人 rubric / 最终门禁 |
| docs/tech/ADR-007-模型供应商适配与成本控制.md | 百炼/MiMo/通用 Responses 装配 / 无自动 fallback / 上下文与 Token 优化 |
| docs/api/openapi.yaml | 产品 HTTP 路径与 DTO 线格式;SSE 细节见同目录协议文档 |
| DESIGN.md / DESIGN_CONTEXT.md / UI_RULES.md | Web 视觉来源、产品设计语境与实现规则;写 UI 前必须依次阅读 |
| docs/tech/技术选型.md | 栈级决策(ADR)+ 版本锁定表 |
| docs/tech/工程实践指引.md | 各阶段开工前扫对应小节;评审对照 §九检查单 |

## 工程纪律

- **版本纪律**:ADK-Go/a2a-go 迭代快,文档不写死 import 路径与 API 签名;首装后回填技术选型.md 末尾版本锁定表。模型默认值集中在 `internal/modelprovider`,部署值只进未跟踪 `.env`;文档示例需与 `.env.example` 同步。
- **模型纪律**:不得在启动时探测模型,不得自动 fallback;真实调用只通过显式 `modelcheck` 或 Live 门禁。日志/指标只写 provider、role、model、状态和安全错误类别。
- **数据纪律**:网络响应和模型辅助结果默认只能进入候选区;只有精确身份、确定性证据和全部门禁通过的低风险变化可条件自动发布,其余进入 quarantine 并保持 last-known-good。定时主链模型调用必须为 0。阶段 2 以 `docs/data/数据获取与发布规则.md` 为准。
- **目录纪律**:布局唯一出处 mvp.md §4.4;`internal/*`、`scripts/` P1 起按需建,不为架构感提前拆(工程实践指引 §一.3)。
- **`internal/rules` 零 LLM**:P1 建包时同时配 golangci-lint depguard。
- **schema 单一出处**:`internal/schemas` 定义一份,字段变更回写设计方案 §四,不在代码里静默漂移。
- 当前进度:MVP P0–P6 已封板;P7 产品 API、P8 Web 工作台与 P9 分享只读页已实现。P10 的 50 组 golden、全部自动门禁和 L1–L6×3 Live Pass³ 已通过,但 3 人真人盲评尚未执行,不得创建 `stage1-freeze`。P11 已完成。P12A 已实现价格观察、人工 CSV、安全选价、不可变快照和动态过期提示；P12B 仍等待获准自动来源及连续两个 weekly 周期，P12 不得标记完成。产品入口为 `web/` 的 Next.js 工作台,ADK dev UI 继续只作调试入口。

## 已知环境坑(Windows)

- ADK openaimodel 走 OpenAI **Responses API**(非 Chat Completions),百炼 compatible-mode 已支持;该包标注 EXPERIMENTAL。
- Docker Desktop 若启动崩溃报 unix socket「cannot be accessed」:Windows 层删不掉损坏 socket,用 `wsl -d docker-desktop -e rm -f /mnt/host/c/<路径>` 删(2026-07-26 实修:dockerInference、engine.sock 等三处)。**根因是 Windows 快速启动(HiberbootEnabled=1)把 socket 冻成死文件,每次关机开机必复发,关掉快速启动才断根**(2026-07-27 确认)。
- `go`/`docker` 不在 PATH 时:Go 装在 `C:\Program Files\Go\bin`。
