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

### 四.1 P1 校验契约(2026-07-28 冻结)

P1 闭环:`手写 BuildSelection JSON → PostgreSQL 解析零件真值 → ResolvedBuild → 12 条纯 Go 规则 → ValidationReport`。本节是 P1 契约的唯一出处,由 `internal/schemas` 实现;字段变更先改本节再改代码。

```jsonc
// BuildSelection —— P1 CLI 输入(P2 起由生成 Agent 产出,取代上文 BuildDraft 的 parts 口径)
{
  "schema_version": 1,
  "build_ref": "build_demo_001",
  "parts": {
    "cpu": "amd-ryzen5-7500f",
    "motherboard": "msi-b650m-mortar-wifi",
    "memory": "kingston-fury-beast-ddr5-6000-16gx2",
    "ssd": [{"sku": "samsung-990pro-1tb", "quantity": 1}],   // 数组,quantity 必须为正整数
    "gpu": "asus-dual-rtx4070s",                              // 必须显式给 SKU 或 null(核显点亮)
    "psu": "seasonic-focus-gx-750",
    "case": "fractal-north",
    "cooler": "thermalright-pa120-se"
  }
}
```

- 除 `gpu` 外其余七类为必选部件,缺失属于 **schema error**(解码即失败),不进入规则层;只有零件内部字段缺失才产生 `unknown`。
- 严格 JSON 解码:未知字段、非法枚举、非正数量均为输入错误。

**ResolvedBuild**:store 层按 SKU 从 PostgreSQL 展开的规格真值,是规则引擎的唯一输入。Canonical 字段字典:

| 品类 | 字段 | 类型 / 单位 |
|---|---|---|
| cpu | `socket` | string? |
| cpu | `supported_chipsets` | string[]?(集合) |
| cpu | `has_igpu` | bool? |
| cpu | `tdp_w` | int?(W) |
| gpu | `length_mm` | int?(mm) |
| gpu | `tdp_w` | int?(W) |
| gpu | `power_connectors` | connector[]?(集合,multiset) |
| motherboard | `socket` | string? |
| motherboard | `chipset` | string? |
| motherboard | `memory_generation` | string?(如 `ddr5`) |
| motherboard | `memory_speed_max_mts` | int?(MT/s) |
| motherboard | `form_factor` | `atx\|matx\|itx`? |
| motherboard | `m2_slots` | int? |
| memory | `generation` | string? |
| memory | `speed_mts` | int?(MT/s,额定) |
| ssd | `form_factor` | `m2\|sata_2_5`?(每条目另带 quantity) |
| psu | `wattage_w` | int?(W,额定) |
| psu | `power_connectors` | connector[]?(集合,multiset) |
| case | `gpu_length_max_mm` | int?(mm) |
| case | `cooler_height_max_mm` | int?(mm,风冷限高) |
| case | `supported_form_factors` | (`atx\|matx\|itx`)[]?(集合) |
| case | `radiator_sizes_mm` | int[]?(集合,如 240/280/360) |
| cooler | `type` | `air\|aio`? |
| cooler | `height_mm` | int?(mm,type=air 用) |
| cooler | `radiator_size_mm` | int?(mm,type=aio 用) |
| cooler | `cooling_capacity_w` | int?(W,解热能力) |

**null 语义与单位**:标量 `null` = 未知;集合 `null` = 未知,空集合 `[]` = 已知为空。单位统一为整数 mm / W / MT/s,不出现浮点与复合单位。

**供电接口枚举**:只保留 `pcie_8pin` 与 `pcie_16pin`;12VHPWR / 12V-2×6 一律归一为 `pcie_16pin`;不推断转接线或一分二,按 multiset 包含判定(规则 #10)。

```jsonc
// ValidationReport(P1 版,取代上文示意中的 status/detail 口径;quote/perf P2 起接入)
{
  "build_ref": "build_demo_001",
  "overall_status": "pass|review|fail",
  "checks": [
    {
      "rule_id": "SOCKET_MATCH",
      "outcome": "pass|fail|unknown",
      "severity": "none|warning|error",       // pass → none;fail → 规则级别;unknown → none
      "observed": {"cpu_socket": "AM5", "motherboard_socket": "AM5"},  // 稳定键值,golden 可比对
      "missing_fields": [],                    // 排序后的缺失字段(unknown 时非空)
      "detail": "..."                          // 自然语言,不进 golden 断言
    }
  ]
}
```

**报告聚合**:存在 error 级 fail → `fail`;否则存在 warning 级 fail 或任一 unknown → `review`;全部 pass 才是 `pass`。

**规则接口(Go,`internal/rules`)**:12 条规则按固定顺序全部执行、不短路;Go error 仅表示输入结构非法,规则判定结果一律进 CheckResult。

```go
type Rule interface {
    ID() schemas.RuleID
    Check(schemas.ResolvedBuild) schemas.CheckResult
}

func NewDefaultEngine() *Engine
func (e *Engine) Validate(schemas.ResolvedBuild) (schemas.ValidationReport, error)
```

### 四.2 P2 流水线契约(2026-07-30 冻结)

P2 把 §三 的三 Agent 串成端到端流水线,新增两份 schema:`RequirementSpec`(初筛 → 生成)与 `BuildDraft`(生成 → 校验)。本节是这两份契约的唯一出处,由 `internal/schemas` 实现;字段变更先改本节再改代码。落地拆解、提示词 SOP、tool 契约、Loop 参数见 [P2 流水线设计](tech/P2-流水线设计.md)(实现层,不改本节口径)。

**衔接关系**:`BuildDraft.selection` 复用 §四.1 的 `BuildSelection` parts 口径(SKU 引用);校验 Agent 从 `selection` 走 `store.ResolveBuild → 12 条规则`,得到 §四.1 的 `ValidationReport`。`rationale` 与 `budget_allocation` 是生成 Agent 的自报信息,**纯展示、不进规则层、不进 golden 断言**(工程实践指引 §四.1:真值不采信模型自报)。

```jsonc
// RequirementSpec —— 初筛 → 生成(MVP 单一主用途,use_case 为单对象而非数组,见 mvp.md §2.2)
{
  "schema_version": 1,
  "budget_cny": 8000,                                          // 必填,正整数(元)
  "budget_flex": 0.1,                                          // 预算弹性比例 [0,0.3],缺省 0.1
  "use_case": {
    "type": "gaming|productivity|general",                     // 单一主用途
    "titles": ["黑神话"],                                      // gaming 可选,给目标游戏/软件名
    "resolution": "1080p|2K|4K",                              // gaming 必填
    "fps_target": 144                                          // gaming 可选,正整数
  },
  "size_pref": "atx|matx|itx|any",                            // 缺省 any
  "noise_pref": "silent|normal|any",                          // 缺省 any
  "brand_pref": {"cpu": "any|intel|amd", "gpu": "any|nvidia|amd"},  // MVP 数据仅 AM5,cpu 实际恒 amd
  "existing_parts": ["ssd"],                                   // 已有件品类(不重复购买),八类枚举子集
  "priority": ["gpu", "cpu"],                                  // 预算倾斜优先级,八类枚举子集
  "notes": "..."                                               // 自由文本补充
}
```

- `budget_cny` 缺失或非正 → schema error(初筛 Agent 必须问齐预算才产出)。
- `use_case.type=gaming` 时 `resolution` 必填;其余字段可缺省。严格 JSON 解码,未知字段/非法枚举即输入错误。

```jsonc
// BuildDraft —— 生成 → 校验(生成 Agent 的结构化产出)
{
  "schema_version": 1,
  "requirement_ref": "req_xxx",                               // 关联的 RequirementSpec
  "build_ref": "build_xxx",
  "selection": { /* §四.1 BuildSelection 的 parts 口径:各品类 SKU / gpu 可 null */ },
  "rationale": {"cpu": "...", "gpu": "...", "...": "..."},      // 每件一句理由,纯展示
  "budget_allocation": {"gpu": 0.42, "cpu": 0.18, "...": 0.0}  // 生成 Agent 自报分配,纯展示
}
```

- `selection` 解码与约束完全等同 §四.1(七类必选、gpu 显式 SKU 或 null、ssd 为 `{sku,quantity}` 数组)。
- `rationale`/`budget_allocation` 为可选装饰字段:校验 Agent 忽略其内容,仅在出口翻译时透传给用户;缺失不影响校验。

## 五、兼容性规则表(校验 Agent 初版)

规则 ID 于 2026-07-28 随 P1 契约冻结,执行顺序 = 表内顺序:

| # | 规则 ID | 规则 | 级别 |
|---|---|---|---|
| 1 | `SOCKET_MATCH` | CPU 插槽 == 主板插槽(AM5/LGA1851) | 错误 |
| 2 | `CHIPSET_SUPPORT` | 主板芯片组 ∈ CPU 支持列表 | 错误 |
| 3 | `MEMORY_GENERATION` | 内存代数(DDR5)== 主板支持 | 错误 |
| 4 | `MEMORY_SPEED` | 内存额定频率 > 主板标称上限 | 警告 |
| 5 | `GPU_CLEARANCE` | 显卡长度 ≤ 机箱显卡限长 | 错误 |
| 6 | `COOLER_CLEARANCE` | 风冷:散热器高度 ≤ 机箱限高;AIO:冷排尺寸 ∈ 机箱支持位 | 错误 |
| 7 | `PSU_HEADROOM` | 电源功率 / 估算负载:≥1.30 通过;[1.15,1.30) 警告;<1.15 错误 | 警告/错误 |
| 8 | `FORM_FACTOR_SUPPORT` | 主板板型 ∈ 机箱支持板型 | 错误 |
| 9 | `M2_SLOT_CAPACITY` | M.2 SSD 数量合计 ≤ 主板 M.2 槽位 | 错误 |
| 10 | `GPU_POWER_CONNECTORS` | 显卡供电接口 multiset ⊆ 电源提供 | 错误 |
| 11 | `DISPLAY_OUTPUT` | CPU 无核显且无独显 → 点不亮 | 错误 |
| 12 | `COOLER_THERMAL_CAPACITY` | 散热器解热能力(W)≥ CPU TDP | 警告 |

**功耗公式(#7,冻结)**:估算负载 = CPU TDP + GPU TDP(`gpu:null` 时为 0)+ 100W 固定余项;比较用整数交叉相乘(如 `psu_w × 100 ≥ load_w × 130`),不引入浮点。上限比较类规则(#4/#5/#6/#12)等于边界即通过/不告警。

## 六、数据层设计

**P1 冻结表结构(2026-07-28,由 Goose 编号迁移管理)**——P1 只建三张表,P3/P4 表与 embedding 列到对应阶段再加:

```
parts           (sku PK, category CHECK(八类:cpu|gpu|motherboard|memory|ssd|psu|case|cooler),
                 brand, model, schema_version, specs JSONB, source_meta JSONB,
                 active, created_at, updated_at)
price_snapshots (id PK, snapshot_date UNIQUE, file_sha256 UNIQUE, imported_at)
prices          (snapshot_id FK, sku FK, price_cny NUMERIC(10,2), source,
                 UNIQUE(snapshot_id, sku))
```

- `parts.specs` 存 四.1 的 canonical 字段(按品类),`source_meta` 存每字段来源;缺失字段保留 null 入库,不编造。
- 价格按批次快照:一个 CSV 文件 = 一个 `price_snapshots` 批次(同批日期一致,SHA256 幂等),`prices` 只增不改;不同文件覆盖同日期则失败。

**长期表设计(P3/P4 落地,保留为方向)**
```
parts.embedding vector                                                          -- P3 加列:pgvector 语义选件
requirements (id PK, session_id, spec JSONB, created_at)                        -- P4
builds       (id PK, parent_id FK nullable, requirement_id FK, parts JSONB,
              validation JSONB, quote JSONB, created_at)                        -- P4 版本树:parent_id 串起 v1→v2
sessions     (id PK, user_id, profile JSONB, created_at)                        -- 阶段 3 用户画像
```
- **pgvector 用途**:`parts.embedding` 来自"型号+评测要点摘要"文本;支撑"安静/颜值/白色海景房"类模糊语义选件。
- **Redis**:会话热上下文(当前 RequirementSpec 与 build 草稿)、Agent 间短时状态、LLM 结果缓存。
- **数据管道(一次性脚本)**:pc-part-dataset + dbgpu 导入 → 裁剪到 AM5/LGA1851 主流 SKU(每类 20-50 个)→ passmark-scraper 补性能分 → 手工 CSV 维护京东参考价 → 生成 embedding。

## 七、客户端

- MVP:使用 ADK 自带 dev UI + CLI 调试,已随 P0–P6 封板。
- 阶段 1:Next.js/React 产品工作台(左侧聊天,右侧需求/配置/校验/版本检查器)+ 分享图/只读链接;实现顺序与边界见 product/stage1.md。

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
