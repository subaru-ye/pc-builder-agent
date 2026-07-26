# 装机配置单 Agent — 产品方案与技术架构设计

> 2026-07-26 · 个人学习向项目 · 前置文档:[装机Agent产品调研.md](装机Agent产品调研.md)
>
> **职责批注(2026-07-26)**:本文管架构、A2A schema、兼容性规则、数据层设计。产品定义以 [PRD](product/PRD.md) 为准(本文 §一/§二保留为历史设计依据);MVP 范围与阶段以 [mvp.md](product/mvp.md) 为准(取代本文 §一"MVP 功能边界"与 §八"里程碑");技术栈以[技术选型](tech/技术选型.md)为准。

## 一、产品定义

**一句话**:对话式装机助手——用户说清预算和用途,产出保证兼容、带日期化报价、可多轮修改的配置单。

**目标用户(设定)**:装机小白与"半懂哥"。前者需要被引导问出真实需求,后者需要快速验证自己的想法并抠性价比。

**MVP 功能边界**:
- 做:对话收集需求 → 生成配置单 → 兼容性/功耗/性价比核算报告 → 多轮增量修改("换 A 卡""降 500 优先砍哪")→ 配置单版本历史与 diff → 导出 Markdown。
- 不做:实时爬价(用日期化快照价)、下单跳转、外设(显示器键鼠)、水冷定制、BIOS 版本与内存 QVL 长尾校验(免责声明)、笔记本/整机推荐。

## 二、用户流程

```
用户开口(模糊需求)
  → 初筛 Agent 多轮追问:预算 / 用途(游戏@分辨率帧率 | 生产力 | AI推理)/ 尺寸偏好 / 静音 / 品牌倾向 / 已有配件
  → 输出结构化需求单,用户确认
  → 生成 Agent 出配置草案(8 大件卡片 + 预算分配 + 选件理由)
  → 校验核算 Agent 出报告(兼容性逐项 ✓/✗ / 功耗余量 / 总价 vs 预算 / 性能分与性价比)
  → 用户迭代:自然语言改单 → 增量重生成 → 新版本快照(v1→v2→…,可回看 diff)
  → 导出配置单(含快照日期与免责边界)
```

## 三、Agent 架构

采用 Google ADK(Go 版,ADK-Go)编排,Agent 间用 A2A 协议传结构化消息(学习目标:体验跨 Agent 通信,而非单进程函数调用);语言与框架选型依据见[技术选型](tech/技术选型.md) ADR-001。

```
┌─────────────┐  A2A: RequirementSpec  ┌─────────────┐  BuildDraft   ┌───────────────┐
│ 初筛 Agent   │ ─────────────────────→ │ 生成 Agent   │ ────────────→ │ 校验核算 Agent  │
│ Requirement │ ←───────────────────── │ Builder     │ ←──────────── │ Validator      │
└─────────────┘   澄清回询              └─────────────┘  ValidationReport + Quote
      ↑                                     ↓ pgvector 语义选件
   用户对话                              PostgreSQL 零件库/价格快照
```

1. **初筛 Agent(LLM 主导)**:多轮对话 → `RequirementSpec`;负责追问缺失字段与冲突消解(预算 5000 却要 4K 光追 → 主动降预期)。
2. **生成 Agent(LLM + 检索)**:按用途模板分配预算(游戏机显卡 35-45%、生产力 CPU 优先);pgvector 语义检索候选件("安静的显卡" → 噪音/散热参数过滤);产出 `BuildDraft`,每个选件附一句理由。
3. **校验核算 Agent(纯规则引擎,零 LLM)**:跑兼容性规则表 + 功耗余量 + 报价合计 + PassMark 性价比;输出机器可读的 `ValidationReport`,不通过则回传生成 Agent 自动换件重试(LoopAgent,设最大轮数)。LLM 只在最后把报告翻译成人话。
4. **修改流**:用户改单请求 → 初筛 Agent 解析成 `ChangeRequest`(目标件/约束变更)→ 生成 Agent 增量替换(锁定未涉及的件)→ 重新校验 → 新版本落库。

**模型接入**:走阿里百炼 OpenAI 兼容端点(见[技术选型](tech/技术选型.md) ADR-004);开发期低价档模型跑初筛、旗舰档跑生成。具体型号与 API 以实现当日官方文档为准,不在本文写死。

## 四、A2A 消息 Schema(核心四份)

```jsonc
// RequirementSpec —— 初筛 → 生成
{
  "budget_cny": 8000, "budget_flex": 0.1,
  "use_cases": [{"type": "gaming", "titles": ["黑神话"], "resolution": "2K", "fps_target": 144}],
  "size_pref": "ATX|MATX|ITX|any", "noise_pref": "silent|normal|any",
  "brand_pref": {"cpu": "any|intel|amd", "gpu": "any|nvidia|amd"},
  "existing_parts": ["ssd"], "priority": ["gpu", "cpu"], "notes": "..."
}

// BuildDraft —— 生成 → 校验
{
  "requirement_ref": "req_xxx",
  "parts": {"cpu": {"sku": "amd-7500f", "price_ref": "p_202607", "reason": "..."},
            "gpu": {...}, "mobo": {...}, "ram": {...}, "ssd": {...},
            "psu": {...}, "case": {...}, "cooler": {...}},
  "budget_allocation": {"gpu": 0.42, "cpu": 0.18, "...": 0}
}

// ValidationReport + Quote —— 校验 → 出口
{
  "build_ref": "build_v2",
  "checks": [{"rule": "SOCKET_MATCH", "status": "pass"},
             {"rule": "PSU_HEADROOM", "status": "warn", "detail": "余量仅 18%,建议 ≥30%"}],
  "power": {"est_load_w": 520, "psu_w": 650},
  "perf": {"passmark_total": 41200, "score_per_yuan": 5.15, "bottleneck": "none"},
  "quote": {"snapshot_date": "2026-07-20", "total_cny": 7890, "items": [...]}
}

// ChangeRequest —— 初筛 → 生成(改单请求;超出三类意图则降级为整单重生成并告知用户)
{
  "base_build_ref": "build_v2",                                  // 基于哪个版本改
  "intent": "swap_part | adjust_budget | change_constraint",     // 三类意图,单次请求只承载一类
  "swap": {"category": "gpu", "target_hint": "AMD 显卡"},         // intent=swap_part 时必填
  "budget_delta_cny": -500,                                      // intent=adjust_budget 时必填(正加负减)
  "constraint_patch": {"noise_pref": "silent"},                  // intent=change_constraint 时必填:RequirementSpec 字段局部覆盖
  "locked_categories": ["cpu", "mobo"],                          // 显式加锁;未涉及件默认全部锁定
  "notes": "..."
}
```

## 五、兼容性规则表(校验 Agent 初版)

| # | 规则 | 级别 |
|---|---|---|
| 1 | CPU 插槽 == 主板插槽(AM5/LGA1851) | 错误 |
| 2 | 主板芯片组 ∈ CPU 支持列表 | 错误 |
| 3 | 内存代数(DDR5)== 主板支持 | 错误 |
| 4 | 内存频率 > 主板标称上限 | 警告 |
| 5 | 显卡长度 ≤ 机箱显卡限长 | 错误 |
| 6 | 散热器高度 ≤ 机箱限高;水冷排尺寸 ∈ 机箱支持位 | 错误 |
| 7 | 电源功率 ≥ 整机估算负载 × 1.3(余量 30%) | 警告(<1.15 错误) |
| 8 | 主板板型 ∈ 机箱支持板型 | 错误 |
| 9 | SSD 数量 ≤ 主板 M.2 槽位 | 错误 |
| 10 | 显卡供电接口(12V-2×6/8pin×N)⊆ 电源提供 | 错误 |
| 11 | CPU 无核显且无独显 → 点不亮 | 错误 |
| 12 | 散热器解热能力(TDP)≥ CPU 功耗档 | 警告 |

## 六、数据层设计

**PostgreSQL(主库,含 pgvector 扩展)**
```
parts        (sku PK, category, brand, model, specs JSONB, embedding vector)   -- 零件库,specs 按品类放参数
prices       (id PK, sku FK, price_cny, source, captured_at)                   -- 报价快照,只增不改
requirements (id PK, session_id, spec JSONB, created_at)
builds       (id PK, parent_id FK nullable, requirement_id FK, parts JSONB,
              validation JSONB, quote JSONB, created_at)                       -- 版本树:parent_id 串起 v1→v2
sessions     (id PK, user_id, profile JSONB, created_at)                       -- 用户画像(偏好沉淀)
```
- **pgvector 用途**:`parts.embedding` 来自"型号+评测要点摘要"文本;支撑"安静/颜值/白色海景房"类模糊语义选件。
- **Redis**:会话热上下文(当前 RequirementSpec 与 build 草稿)、Agent 间短时状态、LLM 结果缓存。
- **数据管道(一次性脚本)**:pc-part-dataset + dbgpu 导入 → 裁剪到 AM5/LGA1851 主流 SKU(每类 20-50 个)→ passmark-scraper 补性能分 → 手工 CSV 维护京东参考价 → 生成 embedding。

## 七、客户端

- MVP:Web 客户端(先用 ADK 自带 dev UI 调试;正式版 Next.js/React:左侧聊天流、右侧配置单卡片 + 校验报告 + 版本 diff)。
- 阶段 2:配置单导出为分享图、公开链接。

## 八、里程碑

| 阶段 | 交付 | 验证标准 |
|---|---|---|
| M1 数据与规则 | 数据导入脚本 + 规则引擎(纯 Python 库 + 单测) | 对 20 组人工构造的配置(含故意错配)校验全对 |
| M2 单进程流水线 | ADK 三 Agent 跑通(先函数级衔接) | 「8000 元 2K 游戏机」一轮出合格配置单 |
| M3 A2A 化 | 初筛/生成/校验拆为独立 A2A 服务 | 消息 schema 校验通过,跨进程调用成功 |
| M4 记忆基座 | Postgres 版本树 + pgvector 选件 + Redis 会话 | "降 500""换 A 卡"增量改单,v1→v3 diff 可回放 |
| M5 客户端 | Web UI + 导出 | 找 3 个真人各配一台,方案可用率主观 ≥80% |
| M6 可选 | 价格半自动更新、发装机社区收反馈 | — |

## 九、学习目标对照(技术栈验收)

| 学习目标 | 本项目落点 |
|---|---|
| 多 Agent 间 A2A 结构化通信 | RequirementSpec / BuildDraft / ValidationReport 三份结构化消息 |
| 检索、方案编排与价格核算 | 零件库检索、配置组合与预算分配、报价合计与性价比核算 |
| 方案修改与迭代 | ChangeRequest 增量改单 + 版本树 |
| 记忆基座(PG/Redis/pgvector) | 版本快照 / 会话热上下文 / 语义选件 |
| 报价快照 | prices 只增表 + build 绑定 snapshot_date |
