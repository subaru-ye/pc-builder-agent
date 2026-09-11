# CLAUDE.md

装机配置单 Agent:对话式 DIY 装机助手,多 Agent 流水线(初筛/生成/校验)+ A2A + PG/pgvector/Redis。个人学习向项目。

## 常用命令

```bash
docker compose up -d             # PG → localhost:15432,Redis → localhost:16379
go run ./cmd/authsetup            # 可选:生成本地 Auth 独立密钥,不覆盖已有 .env 值
docker compose -f docker-compose.yml -f docker-compose.auth.yml up -d postgres redis auth
go run ./cmd/migrate up          # 应用 PostgreSQL 编号迁移
go run ./cmd/buildsvc            # 终端 1:A2A 生成+校验服务
go run ./cmd/host web --write-timeout=10m api --sse-write-timeout=10m webui  # 终端 2:ADK dev UI,http://localhost:8080/ui/
go run ./cmd/api                 # 终端 3:产品 API,http://localhost:8082
go run ./cmd/modelcheck -role screening  # 显式上游检查;普通启动/测试不调用模型
uv run --project scripts/data pcdata source check  # 静态来源检查;默认不联网
uv run --project scripts/data pcdata price health  # 价格快照与动态年龄
cd web && pnpm dev               # 终端 4:产品 Web,http://localhost:3000
go build ./... && go vet ./...
```

- 端口非默认:本机原生 PostgreSQL 17 与 Redis 服务常驻占用 5432/6379,故 compose 用 15432/16379。
- `.env`(不入库)存所选供应商 Key、独立的 `SHARE_TOKEN_SECRET` 与可选 Auth 密钥;这些密钥不得复用。三个模型角色通过 `*_PROVIDER/MODEL/API_KEY/BASE_URL` 独立配置,见 `.env.example`。
- 数据库结构统一由 `db/migrations/` 的 goose 编号迁移管理；保留数据卷运行 `go run ./cmd/migrate up`，禁止为普通迁移执行 `down -v`。

## 文档入口与职责

完整导航见 `docs/README.md`。产品需求由 `docs/product/PRD.md` 维护，当前工作只在 `docs/product/路线图.md` 维护；不要恢复旧阶段清单。

| 文档 | 范围 |
|---|---|
| docs/tech/系统架构.md | 服务边界与模块导航 |
| docs/tech/开发约定.md | 目录、模型、状态与验证纪律 |
| docs/tech/技术选型.md | 栈级决策；实际版本以锁文件为准 |
| docs/tech/自主规划流程.md | 默认自主检索、方案、证据与调用边界 |
| docs/api/openapi.yaml | HTTP 路径与 DTO；流式细节见同目录 SSE 协议 |
| docs/data/数据获取与发布规则.md | 数据与条件发布的强制约束 |
| docs/ops/本地运行与部署.md | 启动、迁移和排障 |
| docs/eval/README.md | 测试、评估、历史基线与运行记录 |
| DESIGN.md / DESIGN_CONTEXT.md / UI_RULES.md | Web 视觉与实现规则；写 UI 前必须依次阅读 |

代码契约分别以 `internal/schemas`、OpenAPI 和 `db/migrations` 为准，相关更改同步更新文档与契约测试。

## 工程纪律

- **版本纪律**:ADK-Go/a2a-go 迭代快,文档不写死 import 路径与 API 签名;首装后回填技术选型.md 末尾版本锁定表。模型默认值集中在 `internal/modelprovider`,部署值只进未跟踪 `.env`;文档示例需与 `.env.example` 同步。
- **模型纪律**:不得在启动时探测模型;真实调用只通过显式 `modelcheck` 或 Live 门禁。日志/指标只写 provider、role、model、状态和安全错误类别。**额度降级链(2026-09 修订)**:chat 角色可配置 `SCREENING_MODEL_CHAIN`/`BUILDER_MODEL_CHAIN`(显式白名单,非跨供应商回退),运行中候选 403/404/配额耗尽自动切下一个并记日志,全部耗尽才失败;链外不得静默换模型。
- **Harness 纪律**:`BUILD_HARNESS_MODE` 默认 planning；最多 8 次模型往返、24 次工具执行、3 次外部搜索和 6 次网页读取。需求不做程序准入或候选硬筛选；真实校验结果作为反馈。v2/legacy 仅供显式历史诊断，不自动回退。见[自主规划流程](docs/tech/自主规划流程.md)。
- **认证纪律**:浏览器和 Next.js 不接触 Supabase Token、不读取 `auth.*`;产品 API 只用 HttpOnly opaque Cookie，Token 加密存 Redis。已认领 owner 不得匿名访问，公开分享接口不得读取身份 Cookie。
- **数据纪律**:网络响应和模型辅助结果默认只能进入候选区;只有精确身份、确定性证据和全部门禁通过的低风险变化可条件自动发布,其余进入 quarantine 并保持 last-known-good。定时主链模型调用必须为 0。数据操作以 `docs/data/数据获取与发布规则.md` 为准。
- **目录纪律**:见 `docs/tech/开发约定.md`；按实际职责建目录。
- **`internal/rules` 零 LLM**:使用 golangci-lint depguard 保护。
- **schema 单一出处**:`internal/schemas` 定义一份,字段变更同步对应模块设计,不在代码里静默漂移。
- 当前能力与未完成事项见 `docs/product/路线图.md`；真人门禁未通过不能宣称最终发布验收完成。

## 模型配置与评估口径

当前 `.env.example` 固定 Builder `qwen3.8-max-0902`、Screening `deepseek-v4-flash-0731`，二者 `*_MODEL_CHAIN` 为空；Embedding 保持 `qwen3.7-text-embedding`。沿用百炼供应商 Key，角色专用 Key 可覆盖。固定组合已完成 v1.1 基线与修复回归；当前 v1.3 已迁移已有件旧契约题，增加禁止重复追问已知信息的负向断言，真实结果见 docs/eval/运行记录.md。

额度链能力保留在 `internal/modelprovider/chain.go`，非空链会覆盖单模型配置。固定回归不得静默启用链；真实额度链评估独立标注，链首不是每次实际服务模型。历史额度不作为当前可用额度承诺。

## 已知环境坑(Windows)

- ADK openaimodel 走 OpenAI **Responses API**(非 Chat Completions),百炼 compatible-mode 已支持;该包标注 EXPERIMENTAL。- Docker Desktop 若启动崩溃报 unix socket「cannot be accessed」:Windows 层删不掉损坏 socket,用 `wsl -d docker-desktop -e rm -f /mnt/host/c/<路径>` 删(2026-07-26 实修:dockerInference、engine.sock 等三处)。**根因是 Windows 快速启动(HiberbootEnabled=1)把 socket 冻成死文件,每次关机开机必复发,关掉快速启动才断根**(2026-07-27 确认)。
- `go`/`docker` 不在 PATH 时:Go 装在 `C:\Program Files\Go\bin`。
