# 2026-09-14 离线验收记录

最终运行 `artifacts/planning-eval-20260914-r6`：12/12 场景、42 个步骤、293 条断言全部通过。执行真实产品服务、数据库迁移、需求状态更新、规划工具、报价和兼容性核验、正式版本保存与读取；模型采用冻结协议响应，外网采用保存资料。

| 项目 | 结果 |
| --- | --- |
| 新交付步骤 | 8 |
| 有候选和具体问题的待解决步骤 | 3 |
| 未启动规划的收集/讨论步骤 | 20 |
| 面板编辑 | 1 |
| 刷新或重试 | 10 |
| Screening / Builder 协议调用 | 23 / 38 |
| 实际模型提供方请求 | 0 |
| 工具执行 | 26 |
| 搜索 / 读取工具执行 | 1 / 1（均返回离线资料） |
| 实际外网请求 | 0 |
| Token / 计费金额 | null / null（无真实推理计量） |
| Runner 时间 | 5800 ms，不含构建、容器启动，不代表真实服务延迟 |

验证命令：

```bash
go test ./internal/planningeval ./cmd/evalplanning
go test ./internal/planning ./internal/agents/pipeline ./internal/product ./internal/producthttp
go vet ./internal/planningeval ./cmd/evalplanning
go run ./cmd/evalplanning -mode check
python scripts/eval/planning/audit_legacy.py
python scripts/eval/planning/run.py --out artifacts/planning-eval-20260914-r6
git diff --check
```

上述命令通过。已有包测试使用缓存，依赖显式 `PG_TEST_DSN` 或浏览器服务开关的既有集成测试不在此次 Go 包命令中启用；新 runner 则实际启动独立数据库完成 42 步产品链路，不依赖这些跳过的测试。

新增评分器反向测试验证：丢失偏好、声称检索但无工具记录、仅在系统提示出现配件名、未关联正式版本的 ready、空问题 proposal、未经审查的笼统待解决、重复追问和无报价不能判成功。另外验证套件字节漂移/重复 ID/尾随 JSON/未知动作被拒绝，未知网页不联网回退，oracle 用尽明确失败，共享库 DSN 在连接前被拒绝，刷新不计新交付。

50 个旧题均核对通过原套件登记的规范 JSON 哈希，旧题与历史成绩未改写。新文件检查为 UTF-8、无 BOM、LF。最终专用评估容器已清理，未重启共享服务；本轮没有修改采集脚本、正在更新的数据文件或共享数据库。

## 失败迭代与来源

保留了 `r1` 到 `r6` 的本地运行产物。r1 的冻结种子包含未导入商品的证据，触发外键错误；改为只导入选中商品的证据。r2 的就绪检测误将 PostgreSQL 初始化阶段的 Unix socket 视为 TCP 已可用；改为检测 localhost TCP。

r3 通过 9/12。失败来自回放 oracle：升级优先级被标为 must 却没有评估、外部规格引用了搜索摘要而非正文、错误 ready 收到服务端纠正后没有后续响应。补齐 oracle 的合法工具轨迹和评估，并保留服务端约束及预期行为。r4、r5、r6 均通过 12/12；后两次用于验证更严格的模型输入断言、刷新统计和工具执行断言。没有调用真实模型来反复调试。

最终套件统一为 LF 后原始字节哈希发生变化；r4 与 r5/r6 的语义输入相同，保留各自套件副本。最终追溯标识：

- Git HEAD：`24c289ad4df68b8ddd7ad3fe8ff3d93dcb186b2a`，加当时工作区改动；准确代码哈希见运行 `manifest.json`。
- 套件 SHA256：`a05770dc7c3032b127c361813e7a6f1f658bd1acc90e34b6979a07a83dc3fd34`。
- 目录结构快照 SHA256：`8509504e4e8665fcea83962c5500b26d1ae1b36f8fb26ed1ffb453c5f65bb138`。
- 最终 `report.json` SHA256：`25b1e54bb18d30e565dc85ccd8a3dd86695985f51161155cb28990f9ef4940f5`。

## 尚未验证

不据此宣称真实模型通过率、商品覆盖率、方案性价比、联网抓取成功率、真实 Token 成本或生产 P95 延迟。没有浏览器实测或 A2A 网络传输测试。素材修改和 CPU 升级场景证明状态与执行协议可行，模型自主理解成功率仍需后续固定模型实跑。

数据任务完成后，按[评估说明](README.md)冻结新数据并另设版本，先比较同机制下旧/新数据，再做小规模真实模型与联网测试；不要将本轮离线分数与旧执行器历史分数直接相减。
