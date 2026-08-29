# Agent Harness 2.0：确定性候选与定向修复

> 状态：已实现并通过 L1/L2 真实 v2 烟测，默认启用 `v2`；`legacy` 仅保留为显式诊断路径。最后更新：2026-08-29。

## 1. 背景与目标

优化前的 builder 依赖模型逐品类调用 `search_parts` 和 `search_parts_semantic`。2026-08-12 的 L1/L2 单次烟测虽然都能保存 pass 配置，但合计产生 23 次 builder Responses 调用，输入 165,419 Token，耗时中位数约 126.7 秒。提示模型并行调用工具只能降低部分波动，不能从结构上限制往返次数。

Harness 2.0 把数据准备和修复范围交给确定性代码：

```text
RequirementSpec / ChangeRequest
              ↓
固定价格快照 + active_core 全量读取
              ↓
硬约束、平台通道、价格分位、可选语义补充
              ↓
无工具 builder 一次输出 BuildDraft
              ↓
12 条规则 + 预算 + 锁定品类确定性校验
              ↓ fail/unknown/预算越界
固定无关 SKU，只开放相关品类，最多再修复两次
```

本工程线不增加 planner/reviewer Agent，不修改 A2A、产品 API、数据库或四份核心 schema。模型仍只负责在受控候选内选择和写理由，最终判定与版本保存继续复用既有代码。

## 2. 双轨与故障语义

`cmd/buildsvc` 读取 `BUILD_HARNESS_MODE`：

| 值 | 行为 |
|---|---|
| 空或 `v2` | `ingest → change prep → Harness v2` |
| `legacy` | 既有 ADK `Loop(builder → validator)`，保留工具调用路径 |
| 其他值 | 启动失败 |

两条路径的 A2A 根 Agent 仍名为 `pc_build_service`，继续使用相同 contextID、Redis session、`build_state`、版本树和保存事务。v2 失败不会自动执行 legacy；自动回退会重复消费模型额度并掩盖候选或提示缺陷，必须由操作者修改配置后显式重试。

## 3. Candidate Bundle

每次 run 先读取最新 `price_snapshots` ID，再只关联该批次读取所有 `active=true AND catalog_state=active_core` 零件。语义补充即使发生在新快照发布之后，也只能引用本次固定目录中已有的候选和价格。

确定性裁剪规则：

- 锁定品类只提供基版本 SKU；GPU 锁定为 `null` 时不提供候选。
- CPU 按 Intel/AMD 平台各保留最多 3 个价格分位候选。
- GPU 从 SKU、品牌和型号确定性识别 NVIDIA/AMD/Intel 芯片阵营，不把 MSI、Sapphire 等板卡品牌误当阵营；A/N 卡换件提示覆盖旧需求中的 GPU 品牌偏好。
- 主板必须属于候选 CPU 的 socket 和 supported chipsets，每个 socket 最多 3 个，总数最多 8 个。
- 内存代数来自候选主板；尺寸偏好同时过滤主板板型和机箱支持板型。
- 其余品类保留最低、25%、中位、75% 和最高价格代表项。
- 只有静音或 notes 中明确出现颜色、风格、颜值词时才做一次跨品类 embedding 检索；结果必须先通过硬约束，每品类最多补充 2 个且总数不超过 8 个。

发给模型的候选仅包含 SKU、品牌、型号、精确价格、规则所需 specs 和可选 `match_text`。JSON 最多 24,000 个 Unicode 字符，超限时先移除语义项，再移除非平台最低项；锁定项和每个平台最低候选不得删除。

## 4. 模型决策与修复

v2 请求不声明任何 function tool，模型必须直接输出既有 `BuildDraft`。程序随后依次执行：

1. 严格 schema 解码。
2. Candidate Bundle 成员校验。
3. 基版本锁定和预算改单最多两品类校验。
4. 现有 12 条兼容性规则与报价。
5. 预算弹性窗口校验。

规则失败或可消除的 unknown 按固定映射只开放相关品类，例如 PSU 余量优先换 PSU、显卡限长优先换机箱、内存代数优先换内存。预算超上限时开放最多两个当前价格最高且存在更便宜替代项的品类；低于下限时开放升级空间最大的两个品类。若规则与预算同时失败，两种修复面在同一轮合并。只剩预算问题时，代码枚举最多 64 个离散候选组合，以既有 validator 筛掉不兼容或仍越界的组合，再把唯一最佳组合交给模型重写。修复请求同时带上一版完整 selection，并把其他品类写入 `fixed_selection`。

一次 run 最多 3 次 builder 调用。连续两次 selection 相同、所有关联品类被锁定、没有替代项或第三次仍失败都会明确终止且不保存版本。模型文本不能宣称成功，数据库新增版本仍是产品交付真值。

## 5. 指标与验收

`P10_METRICS_DIR` 启用时增加以下脱敏事件：

- `harness.started`、`candidate.bundle`。
- `decision.started`、`decision.completed`。
- `validation.completed`、`repair.planned`、`repair.budget_candidates`。
- `harness.completed`、`harness.failed`。

字段只包含 session 指纹、mode、attempt、候选计数、锁定/可变品类、规则 ID、Token、缓存和耗时；不记录需求正文、Prompt、模型响应、Cookie 或凭据。

2026-08-29 默认切换验收：

- 在明确设置 `BUILD_HARNESS_MODE=v2` 后串行运行 L1/L2 各一次，Playwright retry 为 0。
- L1 为 pass；L2 无 error 且满足静音/白色软偏好。
- 每项 builder 调用不超过 3 次，合计不超过 6 次。
- 每项不超过 90 秒，合计输入 Token 相比 2026-08-12 对应烟测下降至少 30%。
- 任何失败都保持默认 legacy；不得自动换模型或挑选性保留成功样本。

| 指标 | v2 结果 | 2026-08-12 legacy 基线 |
|---|---:|---:|
| L1 / L2 结果 | pass / pass，均保存 v1 | pass / pass |
| builder 调用 | 1 / 3，合计 4 | 合计 23 |
| 输入 Token | 合计 13,610 | 合计 165,419 |
| 输出 Token | 合计 881 | 合计 12,857 |
| cache 命中 Token | 合计 13,578 | 合计 132,810 |
| 场景总耗时 | 9,380 / 18,782 ms | 136,639 / 116,778 ms |
| 首个进度 | 2 / 2 ms | 6 / 9 ms |

输入 Token 相比基线下降约 91.8%，builder 调用下降约 82.6%，两项均低于 90 秒且所有断言通过。L2 的白色/静音属于软偏好：缺少可靠颜色或噪声字段时，交付理由必须明确提示购买前核对，不能伪装成已验证事实。

默认值已在独立提交中切换为 `v2`。完整 L1–L6 Pass³ 和三人真人盲评仍由[阶段 1 待封口 Backlog](../product/stage1-backlog.md)管理。
