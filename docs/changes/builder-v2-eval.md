# Change Spec：Builder v2 专项评估（builder-v2-eval）

日期：2026-09-25　分支：`codex/builder-v2-eval`（基于 b745554）　状态：第一阶段（仅建设与验证，不接真实模型）

## 1. 背景与目标

Requirement v2 把 Builder 的输入收敛为确认事务冻结的 `PlanningInput.effective_constraints`
（`internal/schemas/planning.go`：`Spec`=核定预览展开默认后的规范化 RequirementSpec v2，
`Defaults`=被展开默认的来源清单）。现有 planning-v2 评估（current-178 mechanisms 及 r1-r15
批次）以"对话全程"为单位混测 Screening 与 Builder；Requirement v2 数据集（reqv2-grader-v4）
则明确把 Builder 输出质量划出范围（见 `requirement_v2_run.go` limitations）。

目标：建立**"给 Builder 一份已核定的 v2 需求，看它如何选件"**的专项评估——

1. 复用 planningeval 既有执行链：product.Service 真实确认事务（冻结 EffectiveConstraints、
   review_hash 校验、base_draft 组装）→ `planning.Runner`（真实检索/evaluate/校验/持久化）→
   正式版本事务。**不建第二套 runner**。
2. 判卷以确定性规则为准：预算硬上限（must 预算 ×(1+弹性)，弹性缺省 0.1、显式 0 按陈述执行，
   与 `budgetCeiling`/核定预览同源）、已有件品类核账（new_purchase 口径）、兼容性
   validation=pass 才可 ready、unknown/无解如实保留、正式版本持久化。
3. 属性断言而非 SKU 锁定：预算算术、spec 值断言（`selected_specs`）、允许集合
   （`selected_option`）——允许多个同样合格的 SKU，不把旧预设配置当唯一答案。
4. 评估器记录工具合同指纹（builder 请求的 system instruction + tools 声明规范化哈希），
   可区分 planning_action 合同版本；本阶段不为设想中的新工具预写适配。

## 2. 范围

**本轮做**：

- 新 fixture `internal/planningeval/testdata/builder-v2-20260925/`（suite.json + provenance.json），
  商品目录、价格与 current-178 mechanisms 套件字节一致（126 active，快照 11）。
- 10 个迁移 case（BV2-101…110），来源为旧 Builder 用例，逐题登记旧题来源、语义变化与
  不能直接迁移的原因（见 §4 与 provenance.json）。旧用例只算回归来源，不冒充新盲测。
- planningeval 判卷增量（均为加法，不动 v1 冻结套件与 Requirement v2 冻结 gates/grader）：
  - `Expect.FrozenConstraints`：断言 Builder 实际收到的冻结载荷携带核定 v2 合同
    （spec 逐字段 dot-path 断言 + Defaults 来源断言）。
  - `Report.ToolContract`：builder system instruction + tools 声明的规范化哈希与模型名。
- 零模型机制 replay（一次性 pgvector 容器、独立临时库与产物目录）+ 聚焦 Go 回归
  （`go test ./internal/planningeval ./cmd/evalplanning` + 新增单测）。

**本轮不做**：不调用真实 Screening/Builder；不运行或预览 holdout；不改 Requirement v2 的
冻结 gates/grader；不做浏览器全套；不修改生产工具合同或 Builder prompt；不宣布完整产品 GO。

## 3. 执行与判卷设计

执行形态与 v1 套件同构（同一 `Suite`/`Case`/`Step`/`Run`），差异仅在 case 语义与判卷字段：

- 每个 case 以一条 scripted screening 种子轮（适配器输入，非金标）把已核定的 v2 需求写入
  RequirementState，再走**真实确认事务**：`RequirementReviewSpec` 生成核定预览与
  EffectiveDefaults → `PlanningBuilderInput` 冻结完整载荷（含 base_draft/previous_proposal）→
  `gateway.Remote` → `planning.Runner`。种子轮是唯一 scripted 环节；冻结、检索、evaluate、
  校验、门反馈、持久化全部为生产代码。
- Builder oracle 响应只提供"模型作出这些决定后系统是否正确执行"的机制回归；零模型。
- 判卷断言（复用 v1 既有检查 + 新增 FrozenConstraints）：

| 能力区 | 断言 |
| --- | --- |
| 预算硬上限与默认弹性 | `budget_ceiling`（确定性上限=budget×(1+flex)）；FrozenConstraints 断言冻结 spec 的 budget_cny/budget_flex 与 Defaults 来源（缺省 0.1 列 system_default；显式 0 不列默认） |
| 已有件保留 | `purchase_budget`+tight ceiling（新增采购口径）；FrozenConstraints 断言 owned_parts 冻结；无匹配已有件 → outcome 非 ready、versions=0 |
| 兼容性 | `validation=pass` 才可 ready；ITX+SFX 电源规则路径（FORM_FACTOR_SUPPORT 失败反馈 → 换 SFX → pass）；真实冲突 → validation=fail、versions=0、不得 ready |
| 缺规格/无可行解的诚实处理 | unknown 候选不被默认值替代（COOLER_THERMAL_CAPACITY unknown → 换完整字段候选）；无解 → proposal/clarify + issues 标注预算/牺牲，不得伪造 ready |
| 正式版本持久化 | `server_delivery`、`version_count`、`immutable_version:*`、`formal_parent_link`、retry 幂等（`idempotent_run`）、refresh 零执行 |

## 4. 用例迁移表（摘要；全文见 fixture provenance.json）

| 新 ID | 覆盖 | 旧题来源 | 语义变化 / 不能直接迁移的原因 |
| --- | --- | --- | --- |
| BV2-101 | 预算硬上限+默认弹性 | C123-006 step1（current-178 mechanisms） | 多轮收集折叠为一次核定 seed；上限断言改按冻结合同口径 4000×1.1=4400（旧题断言 4000 是更严格的产品语义，登记差异）；核显路径属性断言 has_igpu |
| BV2-102 | 严格预算（弹性 0） | L1-013（legacy-builder v1.5，15K 弹性 0.15）+ review_budget_consistency 显式 0 口径 | v1 spec 的弹性语义升格为 v2 冻结字段；15K 目录场景缩到 5K 以贴合 126 件目录 |
| BV2-103 | 已有件精确匹配·新增采购计价 | L4-206（legacy v1.5） | 已有 CPU 改为目录内可精确匹配的 4600G（唯一确定性匹配，允许断言 ID）；v2 品类核账由服务端确定性执行 |
| BV2-104 | 已有件无匹配·保留 vs 改购 | C123-003 step1 + L4-204 | 旧题多轮对话（先检索方向再澄清）折叠为单轮 Builder 决策；G.Skill 32GB 在快照 11 无精确匹配语义不变 |
| BV2-105 | ITX+SFX 电源规则 | C123-005（原 mechanisms c5） | 旧录制 oracle 超预算（9276+699+79>9900 上限），v2 fixture 重写为预算内 oracle；断言改属性口径（form_factor） |
| BV2-106 | 声称 ready 但真实冲突 | P2-011（proposal-review-20260915） | SKU 换为快照 11 内 AM4 CPU + AM5 板组合；"插槽"冲突语义不变 |
| BV2-107 | 缺规格 unknown 如实保留+换完整候选 | C123-007（原 mechanisms c7） | 旧题以 base_draft 前提构造（cooling_capacity unknown 保留）；v2 简化为新装单轮，保留"unknown 不被默认值替代"机制点 |
| BV2-108 | 无可行解·诚实非交付 | C123-006 step3（降 3000 超支）+ L4-215 | 旧题为续聊步；v2 单轮 2500 元整机预算（核显整机目录下限 3110.3，无条件可行解）；期望 proposal+预算 issue，不锁具体措辞 |
| BV2-109 | 正式版本持久化·幂等 | P2-005 + C123-001 step1 | P2-005 的 proposal 自动交付路径改为显式 confirm；新增 refresh/retry 幂等断言 |
| BV2-110 | 改单继承 base_draft·退库件替换 | C123-002（current-178 mechanisms） | previous_build fixture 原样复用；requirement 补 use_case.titles/existing_parts（v2 readiness 必填，旧题编写于 readiness v2 之前）；执行从旧 r6-era"聊天授权直通 plan"改为**显式 confirm**（现行产品语义：聊天不能替代确认 API 启动 Builder，V4）；上限断言 7700 |

未迁移（登记，不作新盲测）：B2-001/002/003（需求收集与比较收口属 Screening 层语义，
超出 Builder 评估范围）；C123-001 step3 升级收益证据（changed_cpu）依赖多轮对话轨迹；
P2-006/007/010（外部注册、询价不联网、备选不执行属会话语义）；L1-006 thermal 录制
（"不重写历史录制"政策）；L2-10x 锁定品类改单（旧 spec 锁定语义与 v2 冻结语义差异较大，
留待下阶段单列）。

## 4.1 第一期实施结果（2026-09-25，r1 套件已被 r2 取代，见 §4.2）

- 零模型 replay **10/10**（`artifacts/builder-v2-20260925-mech-r1`，169 项断言全过，frozen_constraints 30 项；
  actual_model_requests=0、external=0、tokens/cost=null）。工具合同指纹 `11c6a31c…` 已入报告。
- fixture 构造确认三条产品语义并据此修订：seed 用 v2 一轮合同（turn_signals/quote 逐字/空已有件证据词）；
  聊天不直接启动 Builder（BV2-110 改显式 confirm）；交付核验与预算门回环消耗 oracle 轮次（106/108 用重复 final 吸收）。
- 已知既有失败（基线同现，非本期引入）：`TestHistoricalBaseAndBatchProductFlow` 在容器 DSN 下因 readiness v2
  与旧 current-123 fixture 脱节失败（productivity 缺 titles），主树 b745554 对照证实。
- 运行记录：`docs/eval/运行记录.md` 2026-09-25 条目。

## 4.2 金标人工抽查与基线冻结（2026-09-26）

**抽查方法**：以目录快照逐字数据复核全部 10 题的 v2 金标（预算上限口径、已有件匹配唯一性、
无可行解下限、draft 总价算术、断言可判定性）；重点复核预算上限、已有件与"无解"三类。

| 复核项 | 结论 |
| --- | --- |
| 预算上限口径 | 冻结合同硬上限=budget×(1+弹性)；弹性缺省 0.1（BV2-101/103/105/107/109/110 断言 ×1.1 上限）、显式 0 按陈述执行（BV2-102 断言精确 5000）；与 `budgetCeiling`/核定预览同源（生产一致性单测覆盖） |
| 已有件匹配 | "AMD Ryzen 5 4600G"/"AMD Ryzen 5 5600" 在目录各唯一匹配（matchesOwnedPart 品牌全名）；G.Skill Ripjaws V 32GB DDR4-3200 零匹配（BV2-104 语义成立） |
| BV2-108 无解 | 核显整机目录下限 3060.3 元（4600G+各品类最低价）> 2750 上限（2500×1.1），独显路径 5677.5 元——无条件可行解成立 |
| BV2-110 前置 | 四件退库 SKU（sapphire-6600/ripjawsv-32-3200/p3plus/montech-air-903）确认不在 178 目录；替换后总价 7304 ≤ 7700 |
| 断言可判定性 | 修订两处会误伤同等合格候选的 SKU 级断言：BV2-105 去掉 psu.form_factor 等值（SFX-L loki 同样合格）、BV2-107 去掉 cooling_capacity_w=220 等值（validation 解热能力门即真属性） |
| **BV2-106 重写（r2）** | r1 的插槽冲突由脚本化模型错误诱导，live 下不可复现；r2 改为**已有件事实强制冲突**——用户已有 AM4 CPU（5600）与 DDR5 内存（FURY Beast 16-5200），目录核实 AM4 板全为 DDR4、AM5 板全为 DDR5，无板可同时兼容；冲突成为需求的确定性问题 |

**修订与重放**：套件升版 `builder-v2-mechanisms-20260925-r2`（sha256 `2ac8c3668cc1…`），零模型 replay 复验 **10/10**（`artifacts/builder-v2-20260925-mech-r2`）。

**冻结声明**（旧工具合同基线，配对优化的对照锚点）：

| 维度 | 值 |
| --- | --- |
| 离线 fixture | `builder-v2-mechanisms-20260925-r2`，sha256 `2ac8c3668cc1…`（replay 10/10） |
| live 诊断套件 | `builder-v2-live-diag-20260925-r1`，sha256 `9ba7205ab3bf…`（由 r2 派生，剥离 oracle） |
| 判卷 | planningeval Grade + FrozenConstraints 金标（本分支提交时点的 grade.go/builder_v2.go） |
| 工具合同 | 提示词+声明指纹 `11c6a31c…`；错误合同版本 `planning-tool-errors-v1`（`planning.ToolErrorContractVersion`） |
| 代码版本 | 分支 `codex/builder-v2-eval` 提交 `4039ff8`（manifest 另录 HEAD、二进制与逐源码哈希） |
| 模型 | deepseek-v4-flash-0731 @ ws-5z8rvj9oxtusr5m0（pin `baseline-builder-v2-diag-ws5z8rvj9oxtusr5m0-20260925.json`，max_retries=0、无模型链、reasoning=none） |

**单轮真实诊断（2026-09-26，10 题各一次，`artifacts/builder-v2-20260925-live-diag-r1`）**：
金标 9/10、调用 50/200、builder 协议 50 次、工具 95 次、external=0；token 1,550,515
（入 1,491,917/出 58,598）；单调用延迟 p50 12.6s / max 31.0s；批时长 11.5 分钟；费用按
r17/r18 账单反解混合价 1.33 元/1M 估算 ≈ **2.06 元**（方向性估算，非账单锚点读数）。
逐题：101/102/103/105/106/107/108/109/110 全过；**BV2-104 行为失败**（真实缺口，登记如下）。
本轮为诊断口径：不宣称通过率，单次尝试不计入任何门槛。

**登记的模型行为缺口（BV2-104，一次尝试）**：已有 G.Skill 32GB 目录无精确匹配时，模型未按
clarify 纪律先取证取舍，而是静默以 mem-corsair-lpx-32-3600（32GB DDR4，规格最接近的目录件）
占住 memory 槽交付 **ready**——reply 宣称"已有内存继续沿用"，但 draft 实际放入 1759 元新购 SKU
且被品类核账排除（采购合计 5096 元），账实不一致。服务端占位交付检测（placeholderDelivery）
依赖"替身/沿用用户"窄措辞特征，"继续沿用"变体未被覆盖（r11 登记过的 ponytail 局限现场复现）。
处置：本阶段只登记不改——修复属下一阶段产品改动（候选方向：已有件占位声明结构化、或 ready
前对未匹配已有件的确定性拦截），修复后以同一冻结基线做配对对比。

## 5. 产物与可复现性

- `run.py --suite internal/planningeval/testdata/builder-v2-20260925/suite.json --out artifacts/builder-v2-20260925-mech-r1`：
  一次性容器（`peval_` 前缀独立库）、零模型、零联网；输出 report/suite/provenance/manifest
  （Git HEAD、代码/迁移哈希、镜像 ID）。
- 报告记录：工具合同指纹（prompt+schema）、目录 sha256、代码版本、逐 case 调用轨迹
  （trace 含完整请求/响应）、质量（逐断言）、耗时；成本/token 离线为 null（不伪装零成本）。
- provenance.json 记录 suite_sha256、来源套件 sha、逐题迁移登记。

## 6. 下一阶段（Pass³，另行授权，本阶段不执行）

1. **样本与定位**：BV2-101…110 全量重跑 + 新增 2-3 题只补边界（如已有件占位声明的变体、
   严格弹性+改单组合）。**定位声明**：在 10+3 题规模上只能观测边界行为与回归方向，
   不能宣称有代表性的盲测质量结论；代表性盲测需要独立建样（≥30 case）另立项。
2. **调用预算（由单轮实测修订，替代原 150 次估计）**：单轮实测 50 次 provider 请求
   （10 case，1.55M token，≈2.06 元，11.5 分钟）。Pass³=3 轮 ≈150 次基础量，叠加单 case
   方差（本轮单题最高 ~10 次）与失败长尾，**建议共享上限 240 次**（约 7.4M token、
   ≈10 元、~35 分钟）；超过上限的尝试不发送给提供方，失败与未完成场景保留在分母。
   `MODEL_MAX_RETRIES=0`、单批不重跑政策不变。
3. **人工配置质量复核**：对每个 ready 交付逐件复核"目录内是否存在同等价位的更合规候选"
   （参照 delivery_quality.py 硬门口径扩展 v2 版：事实正确、预算内、兼容 0 错、无缺价、
   FrozenConstraints 到位）；人工复核结果与确定性判卷分列，不混入机制分。
4. **端到端接入**：BV2 fixture 的 seed ops 可直接作为 ReqV2 conversations 层的 scripted
   screening 输出复用，从而把"自然语言 → Screening 提取 → 确认冻结 → Builder 选件"接成
   一条链；接入点在套件层（conversation case 复用 seed），不改生产合同。
5. **工具合同前后对比（基线冻结后进行）**：模型、目录、题目、判卷四不变，仅合同变。
   配对报告必须显式记录：①`Report.ToolContract` 指纹（提示词+声明）；②**错误合同版本**
   `ToolErrorContractVersion`（指纹只覆盖提示词与声明——仅改参数校验或错误返回时指纹
   不变，该版本号是唯一的合同变更信号，合同改动时人工递增）；③**代码版本**——manifest
   的 Git HEAD、二进制与逐源码哈希。三者任一不同的批次不构成同前提配对。逐 case 断言
   沿用 regrade/compare 口径。

## 7. 风险

- seed 种子轮是 scripted 适配器输入：冻结合同的质量取决于 seed 与核定预览的真实投影一致；
  已用 `RequirementReviewSpec`（生产同源函数）生成核定预览，规避自拼 spec。
- message 触发 plan 的路径（BV2-110）依赖"执行授权"语义，属产品行为而非评估器构造；
  若产品语义再收紧需同步 seed 文本。
- 目录为 126 件隔离目录，不代表市场覆盖率；预算算术断言只在快照 11 价格下成立。

## 7. BV2-104 产品修复（2026-09-26，分支 fix/bv2-104-owned-standin，基于 35444c9）

**根因（三层叠加，全部确定性代码层）**：
1. `verifiedOwnership`（assessment.go）按**品类**核账：draft 在已有件品类选了任意候选即视为"核验"，整品类被 `WithOwnership` 剔出采购合计——用户型号未核实的新购替身（BV2-104 的 Corsair 32GB，1759 元）被免计价，采购合计 5096 元账实不一致。
2. `finalize` 的占位交付检测只在模型 outcome=="proposal" 时运行，且依赖"替身/沿用用户"窄措辞；BV2-104 真实措辞是"继续沿用"（issues 为空）→ 未强转 clarify；模型直接宣称 ready 时该检查完全绕过。
3. 未匹配已有件的交付 note 文案宣称"相应品类已按用户已有件核账，不计入采购合计"——把未核实的假设写成了事实陈述。

**修复（正确层，无回复关键词依赖）**：
1. 非 SSD 已有件只有在 draft 选中候选与用户型号**精确匹配**（matchesOwnedPart 品牌全名）时才豁免计价；同品类选中不再构成核验。SSD 保留数量核账（存储可互换）。
2. 占位检测替换为结构化 `ownershipTradeoffPending`：已有件目录零匹配且 draft 选中该品类 → 无条件 clarify（不论模型自述措辞、不论模型宣称 proposal 还是 ready），issue 逐件点名品类与用户型号并说明计价取舍。
3. 交付 note 收窄为 truthful 场景：SSD 数量豁免与未选中品类；被选中的零匹配非 SSD 品类由 clarify 点名，不再宣称"已核账"。

**测试与证据**：
- 冻结 live 输出零模型回放（`internal/planningeval/builder_v2_bv2104_replay_test.go`，脚本=testdata 提取的诊断轮 5 次真实响应）：修复前 **红**（outcome=ready、采购合计 5096、issues 空——逐字复现基线失败）；修复后 **绿**（clarify＋计价取舍 issue＋内存行不计 owned）。修复后采购合计 6855 超 6600 上限，触发预算压价回环属诚实会计，脚本以固执重申最终 proposal 建模，finalize 仍收口 clarify。
- 旧测试语义修订（均有理由）：①`accounting_recording_test` wrong_owned_model：ready+品类豁免+note → clarify（旧断言即账实不一致语义）；②`TestUnmatchedOwnedPartsBecomeNonBlockingNotes` → `TestUnmatchedOwnedSelectedForcesTradeoffClarify`（ready 宣称也拦截）；③`TestBudgetSolverTruthAuditAgainstFrozenCatalog` B2-002 段：4667≤6000 期望建立在品类豁免把 5700X3D 升级件当已有件上，修订为按新购计价 6815＞6000（升级不是豁免）。
- 新增覆盖：措辞独立（"继续沿用"+ready 宣称仍 clarify）；SSD 数量核账（1:1 豁免+note，1:2 计价）；未选中零匹配已有件不触发 clarify；精确匹配/授权改单路径不变（`TestPlaceholderDeliveryKeepsUpgradeDelivery`、BV2-110 退库件确认缺席于 owned_parts）。
- 回归：`go test ./...`（40 包）、`go vet ./...` 全绿；builder-v2 机制套件零模型 replay **10/10**（`artifacts/builder-v2-20260925-mech-r2-postfix-20260926`）。
- 单题 live（用户授权口径）：派生单题套件（provenance 记录来源与理由），plan.json 先于任何 provider 请求落盘（evalplanning 已改为所有 live 模式先写 plan），**6 次 Builder 调用 ≤15**，1/1 通过——模型自行 clarify 点名 G.Skill 内存与沿用/改购取舍，采购合计 5706（含被选中内存，不再豁免）。
- 纪律偏差披露：首次 live 尝试（4 次调用，1/1）产物目录被本方清理命令误删，已按 plan-first 修复后重跑；损失仅影响首跑存证，不影响结论。

## 8. 零模型定向收口 v3.1（2026-09-26，同一分支，基于 e311147）

**范围**：修复两类已有件边界，全程零模型（先写失败测试，再实现最小确定性修复）；不改冻结评估金标、不改旧基线、不加回复关键词、不跑 Pass³/新 live。

**文档核查（SSD 互换决策）**：产品文档（DESIGN/PRD/系统架构/openapi）无"SSD 只按数量视为可互换"的要求；数量规则的唯一出处是代码注释"防多盘误豁免"——其意图是防超豁免，不是授权"仅凭数量把不同型号 SSD 免计价"。收口保留数量一致性约束（多盘保护不回退），叠加型号对应，采购价误导风险（选中 B 被免计而实际购入 B）被消除，故不需要停下保留旧行为。

**边界修复**：
1. SSD 型号对应：已有 SSD A、draft 选中不同型号 SSD B（即使数量相同）不再按品类数量豁免——豁免要求"每个选中 SSD SKU 与某已有件型号匹配"且"选中数量合计=已有数量合计"；数量不一致（同型号 1 有 2 选）同样计价。
2. 目录匹配已有件被同品类替换：owned 型号 A 在目录有精确匹配、draft 选不同型号 B 且状态无改购授权（状态合同只能以移除已有件表达授权）→ 无条件 clarify，不得 ready。统一规则：豁免与取舍都以"实际选中件是否对应已有件"判定（零匹配、目录匹配两种形态合并为同一条对应性检查）。

**红-first 证据**：TestSSDDifferentModelMustBePricedEvenWithSameQuantity、TestCatalogMatchedOwnedReplacedByDifferentSKUForcesTradeoff 在修复前红（B 被豁免/直接 ready），修复后绿。

**语义变更（旧测试修订）**：e311147 的 `TestSSDOwnershipAccountingFollowsQuantity` 编码了已废除的"仅数量豁免"语义，重写为 `TestSSDOwnershipAccountingFollowsCorrespondence`（同型号数量一致豁免/数量不一致计价）；delivery note 条件统一为"品类未被选中才出 note"（被选中的不对应品类由 clarify 点名，note 不再对 SSD 例外）。

**回归**：BV2-104 冻结 live 回放绿；机制套件零模型 replay 10/10（`artifacts/builder-v2-20260925-mech-r2-postfix-v31-20260926`）；`go test ./...`（40 包）/`go vet ./...` 全绿。新增覆盖：不同型号同数量计价+clarify、目录匹配被替换 clarify、同型号数量不一致计价、移除旧件后授权改单计价交付。

**剩余风险**：①用户已有旧 SSD（目录必无精确匹配）且 draft 需填 SSD 槽时，现在会稳定产生计价取舍 clarify——这是 BV2-104 类风险的诚实代价，但 UX 上更啰嗦；消除它需要状态合同新增"用户已授权新购 SSD"的结构化表达（后续 change）。②多 SSD 部分对应（已有 A、选中 A+B）时按品类整体计价（保守多计），quote 行级豁免留待需要时再做。

## 9. 多 SSD 部分对应收口 v3.2（2026-09-26，同一分支，基于 df9fc18，零模型）

**勘误（先于修复）**：v3.1 报告"多 SSD 部分对应按品类整体计价（保守多计）"与代码实际行为不符——实测（红例）已有 A+B、选中 A+C 时，对应件 A 进入 OwnedParts，`WithOwnership` 按品类豁免使 **A 与新购 C 双双免计**，比报告表述更危险。红例：`TestSSDPartialCorrespondencePricesWholeCategory` 修复前红（ssd-a、ssd-c 均 Owned=true）。

**修复（最小确定性，仍零模型）**：SSD 豁免改为品类级整体对应判定 `ssdSelectionCorresponds`——每个选中 SKU 对应某已有件型号，且各对应已有件的选中数量合计等于其已有数量；整体对应才可整品类豁免，部分对应（A+C vs A+B）整品类计价并 clarify，不得把对应件单独放入 OwnedParts。`ownershipTradeoffPending` 的 SSD 分支同步改为品类级（不对应时该品类全部已有件列入取舍）。

**正例覆盖**：`TestSSDFullCorrespondenceExemptsWholeCategory`（已有 A+B 与选中 A+B 完全对应 → 整品类豁免、无 clarify）。

**回归**：BV2-104 冻结 live 回放绿；机制套件零模型 replay 10/10（`artifacts/builder-v2-20260925-mech-r2-postfix-v32-20260926`）；`go test ./...`（40 包）/`go vet ./...` 全绿。不改金标、不跑 live、不推送。

## 10. Pass³ 执行（2026-09-26，候选 1c38099，冻结套件，预算内）

- **口径**：3 轮 × 10 冻结 case（无新题、无 holdout），共享上限 240 次（实际 165），模型 pin 与诊断基线一致，plan.json 先于请求落盘。
- **结果**：折叠 **7/10**——BV2-101/102/103/104/107/108/109 全 3/3；BV2-105 **0/3 系统性**（尺寸评估未收敛＋工具纪律偏离）；BV2-106 2/3（r3 未完成冲突评估，被已有件取舍 clarify 兜底）；BV2-110 2/3（r1 幻觉 7000 上限超预算 proposal＋版本停滞）。
- **账实安全**：三轮 248 行报价逐行回验，7 行 owned 全部对应精确匹配已有件，**零违例**；BV2-104 替身内存三轮均计价（6825/6926/6896），BV2-103 豁免口径正确，BV2-110 升级件计价如实。
- **用量**：tokens 5.08M、≈40.2 分钟、费用估算 ≈6.76 元（1.33 元/1M 方向性估算）。
- **结论**：Builder 专项题未达全绿；账实安全维度零违例（本轮修复目标达成并经 3 轮验证）；BV2-105 等模型行为缺口登记为下一阶段工作。**不宣称完整产品 GO。**逐题明细与账实断言：`artifacts/builder-v2-20260925-pass3-report-20260926.md`。
