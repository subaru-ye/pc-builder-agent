# Change Spec：Builder v2 工具使用与评估合同（builder-v2-tool-contract）

日期：2026-09-27　分支：`fix/bv2-104-owned-standin`（基于 65253cf）　状态：判卷冻结；同日执行新候选 Pass³（另行小节登记）

## 1. 背景与目标

Pass³ v2（候选 b262d4d，2026-09-27）折叠 7/10，全部残余失败为单一断言类
`tool_required:search_local_batch`（BV2-101/105/108 各败 1 轮，逐轮 9/8/10）。builder-v2-eval
§12 已核对该断言沿袭 current-178 mechanisms（C123-001/005/006）属评估机制断言，且 §13 收紧了
预算 issue 剔除合同（混合取舍保留）。本 change 单独处理"工具使用如何评估"：定性、版本化、
重判，并为新合同下的新候选专项测试建立前提。**不触碰 Jev 工作树；旧题、旧判卷、旧 Pass³
产物原样保留。**

## 2. 核对记录（产品文档 / 工具声明 / 冻结套件）

| 核对项 | 结论 |
| --- | --- |
| 产品文档 | `docs/tech/自主规划流程.md` §4 只规定有界工具循环额度（每次 Builder 最多 8 次模型往返、24 次工具执行），并描述 `search_local_batch` 的可用语义（一次 1-8 子查询、各计额度、pending_queries）；全文**无任何"必须使用批量检索"的产品要求**。DESIGN.md/PRD.md 无批量检索条款。 |
| 工具声明（prompt 内说明，runner.go） | `search_local_batch: {queries:[…]}，一次提交最多8个独立本地查询…可一起比较CPU/主板/内存或多个平台，为evaluate和修正保留往返…`——措辞是能力与额度说明加"可一起比较"的建议性表述，无强制。工具声明本身（planning_action 参数 schema）不含工具选择强制。 |
| 冻结套件 | builder-v2-live 套件（sha `9ba7205a…`）中 BV2-101/105/108/110 四题 step1 `require_tools=["search_local_batch","evaluate"]`，沿袭 current-178 mechanisms（C123-001/005/006）；BV2-104 仅要求 `search_local`。pass3 v1/v2 的逐题失败显示该断言命中呈轮次波动，无 case 三轮皆败。 |

**核对结论：没有任何新的产品合同证据支持"批量检索是产品要求"。**

## 3. 决策（grading-v2）

1. **`search_local_batch` 定为观察指标（observe_tools），不再作为单题硬性通过条件。**
   理由：无产品合同依据；硬性判定使方案质量与账实安全的结论被工具选择波动淹没
   （pass3v2 三个折叠失败全是它）；且不存在"不用批量即检索不充分"的证据
   （BV2-104 仅用 `search_local` 亦交付合格方案）。
2. **仍如实记录**：每步 `tool_observations` 记录模型是否使用与调用次数（0 = 未使用）；
   报告分列"批量检索观察指标"维度，并在质量/安全失败的 case 上标注当轮批量使用情况
   作为上下文。**不为提高分数强迫模型调用该工具**——本 change 不改提示词、不改工具声明、
   不加任何诱导。
3. **质量与安全断言全部保留**（逐字不变）：预算硬上限（budget×(1+flex)，弹性缺省 0.1、
   显式 0 按陈述）、兼容性 validation=pass 才可 ready、已有件核账（选中件与用户型号对应性、
   混合预算取舍 issue 保留为待解决项——65253cf 合同）、unknown/无解如实保留、正式版本
   持久化（server_delivery/version_count/immutable_version/幂等）、FrozenConstraints 冻结
   载荷断言。
4. `evaluate` 仍是硬性断言（核验是产品语义）；`forbid_tools` 语义不变。

实现：`Expect.ObserveTools` + `StepRecord.ToolObservations`（internal/planningeval/types.go、
grade.go）——观察项不产生 check、不影响 pass/classification；聚焦测试
`TestObserveToolsRecordWithoutGating`（未使用记 0 次、不产生 check、不改分类；require 侧不受影响）。

## 4. 版本化与冻结资产

| 资产 | 值 |
| --- | --- |
| 新判卷套件 | `internal/planningeval/testdata/builder-v2-live-20260925-grading-v2/`，suite sha256 `28b475416421890bf40154551686a67e94b223fbf61332b71eebbef99302308b`，version `builder-v2-live-grading-v2-20260925` |
| 派生关系 | derived_from `builder-v2-live-20260925/suite.json`（sha `9ba7205a…`，live-diag-r1）；改动仅为四题 require→observe；题目语义/种子/目录/其余断言逐字保留 |
| 判卷版本代号 | **grading-v2**（ grading_note 记于套件 provenance；旧判卷记为 grading-v1，即 builder-v2-eval 冻结时点口径，原样保留） |
| 旧资产 | `builder-v2-live-20260925/`（grading-v1）、`builder-v2-20260925/`（机制套件）、pass3 v1/v2 全部产物：原样保留，不修改 |

## 5. 旧批次重判（【重判】——零模型重算，不是新 live 成绩）

用 regrade 帮助器（REGRADE_* 门控）对六份旧 live 报告按 grading-v2 重算断言，
产物与翻转清单见 `artifacts/builder-v2-20260925-regrade-grading-v2-20260927/`：

- **翻转仅由批量断言造成**（check 级 delta 只含 `tool_required:search_local_batch`，无其他断言变化）：
  pass3v2 r1 BV2-101、r2 BV2-105、r2 BV2-108（各 ❌→✅）；pass3v1 r3 BV2-105（❌→✅）。
- 逐轮：pass3v2 9/8/10 → 重判 10/10/10（折叠 7/10 → 10/10）；pass3v1 8/9/8 → 8/9/9
  （折叠 7/10 不变，BV2-105 由 0/3 变 1/3——其 r1/r2 失败另有尺寸收敛等真实原因）。
- **含义**：重判只证明"旧批次在 grading-v1 下的全部残余失败由批量断言造成"；它不是新候选
  的 live 成绩，不用于任何验收宣称。pass3v1 的 BV2-105 尺寸收敛缺口已在 b262d4d 服务端
  修复，与本判卷变化正交。
- 批量使用观察（重判报告 tool_observations）：BV2-105/108/110 使用呈轮次波动
  （105 在 pass3v1 r2/r3 与 pass3v2 r2 为 0；101 在 pass3v2 r1 为 0——恰为其旧判失败轮）；
  使用率与单题通过无稳定相关（105 批量=3 的轮次仍因尺寸收敛失败）。

## 6. 新旧批次可比性

| 维度 | 旧批次（pass3 v1/v2） | 新候选批次 | 可直接配对？ |
| --- | --- | --- | --- |
| 模型/目录/题目/种子 | deepseek-v4-flash-0731 @ ws-5z8rvj9oxtusr5m0；快照 11（sha `fbdaccfb…`）；10 冻结题 | 相同 | ✅ |
| 工具合同指纹（prompt+声明） | `11c6a31c…` | `11c6a31c…`（本 change 不改提示词/声明，live 报告回读核对） | ✅ |
| 错误/反馈合同 | pass3v1/v2 = `planning-tool-errors-v1` | `planning-tool-errors-v2`（budgetAlternatives 措辞如实区分金额与有效上限） | ❌ 反馈行为不同：预算相关的模型行为观察不可直接配对 |
| 代码版本 | b262d4d / 1c38099 | 65253cf 之后的冻结提交（manifest 记 HEAD+源码哈希） | ❌ 确定性层不同：预算 issue 剔除合同收紧（§13）、额度提示降级 |
| 判卷 | grading-v1（批量硬性） | grading-v2（批量观察） | ❌ 折叠分数口径不同；质量/安全断言子集可逐项比较 |
| 用量（调用数/token/时长） | 可比 | 可比 | ✅ 同口径计数 |

**可直接比较**：目录/题目/种子约束下的方案质量与账实安全逐项断言、调用结构与用量。
**不可直接配对**：预算反馈相关的模型行为（反馈合同 v1→v2）、工具纪律硬分（判卷 v1→v2）、
折叠总分。

## 7. 新候选 Pass³（本 change 冻结后执行）

- 口径：同一模型 pin（`baseline-builder-v2-diag-ws5z8rvj9oxtusr5m0-20260925.json`）与同一
  目录快照；grading-v2 套件；**3 轮 × 10 题，总 provider 调用上限 240（每轮 80 共享）**；
  `MODEL_MAX_RETRIES=0` 零重试；每轮请求前持久化 plan（plan-first 既有行为）。
- 纪律：不中途改题、不调参、不挑选重跑；**holdout 不运行**；预算耗尽或环境失败即保留证据
  停止，不补跑凑齐。
- 报告：逐题逐轮 + 折叠；**方案质量、账实安全、批量检索观察指标三个维度分列**；
  记录调用量、耗时、token、费用（按 r17/r18 账单反解混合价 1.33 元/1M 估算并注明推导，
  账单读数为最终核实途径）。
- 定位声明：只得出 Builder 专项题结论，不宣称完整产品 GO。

## 8. 风险

- 观察指标不设阈值，样本 3 轮只描述行为不估比例；批量检索的效率价值（往返节省）留待
  更大样本或专项实验。
- 判卷升版后，与 grading-v1 批次的折叠分数不可横向排名；报告与 manifest 已双向标注。

## 9. 新候选 Pass³ v3 执行结果（2026-09-27，冻结提交 4fc6077）

- **执行**：3 轮 × 10 题全部完成，零重试，无环境失败，未补跑；plan-first 三轮均落盘；预检 plan-live 零调用通过。
- **结果**：逐轮 10/10/8，折叠 **8/10**（grading-v2 口径）。调用 **156/240**（52/50/54），tokens 4,456,142，34.5 分钟，估算 ≈5.93 元（1.33 元/1M 推导）。
- **维度分列**：方案质量——r3 两题真实失败（BV2-104 模型以 proposal 自报而金标期望 clarify：留空内存槽绕过无条件取舍 clarify，服务端闸门只覆盖"占住品类"路径；BV2-105 ITX 尺寸只落主板，MATX 机箱+ATX 电源被 ready，暴露 b262d4d 尺寸核验只查主板的窄化）。账实安全——5 行 Owned 逐行回验零违例，采购合计算术一致。批量检索观察——108 三轮 0 次批量仍 3/3 通过，使用与通过无稳定相关；指标不作阈值判定。
- **残余失败全部与批量检索无关**；修复方向（留空品类强制 clarify、机箱/电源等级入尺寸核验）登记为下一阶段确定性 change 候选。
- **可比性回读**：三轮报告 tool_contract 指纹 `11c6a31c…`（与旧批次一致）、error_contract `planning-tool-errors-v2`、head `4fc6077`——与旧批次的预算反馈行为、工具纪律硬分不可直接配对；质量/安全断言与用量可比较。
- **定位**：只说明本专项题结果，不宣称完整产品 GO。报告：`artifacts/builder-v2-20260925-pass3v3-report-20260927.md`。

## 10. Pass³ v3 残余缺口定向修复与两题定向验证（2026-09-27，同分支，候选即本节提交）

**缺口 1（BV2-104 类：null 槽位绕过核账）**
- **根因**：模型 draft 把零匹配已有件品类留空（`"memory": null`）→ `DecodeBuildDraft` 失败（七类必选合同）→ `evaluate` 在解码错误时保留原始 JSON（runner.go:767）→ `finalize` 的 `DecodeBuildDraft` 同样失败 → `ownershipTradeoffPending` 整个被跳过；`wasReady`（proposal/ready 自报）路径只剩 `deliveryIssues` 的"候选尚未完成兼容性核验"降级，outcome 落盘 proposal，取舍未经确认。Pass³v3 r1/r2 通过纯属模型自报 clarify，服务端并未守住该路径。
- **修复（finalize 兜底，runner.go）**：draft 不可解码且 outcome 为 proposal/ready 时，改用 `unmatchedOwnedParts()`（目录零精确匹配的已有件全列）判取舍——非空则强转 clarify 并逐件点名 + 追加"方案配置未通过结构校验"issue。可解码 draft 完全走原路径，不受影响。
- **红例/正例**：`TestUnmatchedOwnedWithEmptySlotForcesTradeoffClarify`（修复前红：proposal+"候选尚未完成兼容性核验"，取舍被吞）；`TestValidDraftWithUnmatchedOwnedKeepsNormalPath`（可解码 draft 不叠加结构校验 issue，常规取舍 clarify 不变）。既有合法交付路径由 `TestSavedCompleteProposalAutomaticallyDelivers` 等覆盖。

**缺口 2（BV2-105 类：通吃机箱被判满足 must ITX）**
- **根因**：`sizePrefServerVerified` 只核"主板板型=ITX + FORM_FACTOR_SUPPORT 通过"，未核机箱本身。AP201（supported_form_factors=[matx,itx]、无电源仓字段）装 ITX 主板即通过 FORM_FACTOR_SUPPORT，模型自评 size_pref=met 也在场（证据引用存在），双门皆过 → ATX 电源随 ready 交付；金标 case 属性断言（supported_psu_form_factors ∈ sfx/sfx_l）拦住，判败。
- **修复（两处，均为确定性）**：①`sizePrefServerVerified` 增加"机箱为数据可验的 ITX 小机箱"前提（`itxCaseVerifiable`：supported_form_factors 仅 itx，或声明 sfx/sfx_l 电源仓约束）；②must 循环新增 `itxCaseUnverified` 否定分支——机箱不可验时模型自评不得通过，outcome=proposal 并点名"缺少可核验的小机箱数据，需更换数据可验的 ITX 机箱或与用户确认"。规则引擎（rules 层）保持用户无感知的物理判定不变；本修复在 planning 层的 must 字段合同。
- **红例/正例（Pass³v3 真实轨迹冻结回放）**：`TestBV2105Pass3v3FrozenReplayRejectsUniversalCaseAsITX`（r3 失败轨迹 7 响应，修复前 ready，修复后不得 ready 且 issue 点名 ITX）；`TestBV2105Pass3v3FrozenReplayKeepsTrueITXCaseReady`（r1 通过轨迹 6 响应，nr200p+SFX 电源+ITX 主板，修复后仍 ready——合法方案不被拦）。

**边界与剩余风险**：①通吃机箱装 SFX 电源的场景会被停在 proposal 需用户确认——数据不可验时的保守代价（消除它需目录补电源仓字段，属数据管道工作）；②`itxCaseVerifiable` 依赖目录属性声明，属性缺失的候选永不豁免自评（保守方向）；③规则引擎对大机箱电源仓仍不做推断（TestLargerCaseDoesNotInferPSUClearance 语义保留）。

**定向 live 验证（明确预算：每题 ≤15 次 provider 调用，plan-first，零重试，单题派生套件 provenance 已登记）**：
- BV2-104：**1/1 通过**，5 次调用，82,708 tokens——模型自行 clarify 点名 32GB DDR4-3200 沿用/改购取舍并选中替身计价（outcome=clarify）。
- BV2-105：**1/1 通过**，5 次调用，144,949 tokens——模型自选真 ITX 组合（nr200p + SFX 电源 + ITX 主板）交付 ready。
- 产物：`artifacts/builder-v2-20260925-bv2104-directedlive-b-20260927`、`artifacts/builder-v2-20260925-bv2105-directedlive-b-20260927`（plan-live 预检目录以 -preflight 后缀留存）。

**整套复评条件**：两缺口修复经冻结轨迹回放（红→绿）与相邻正例、全量 Go 测试、机制套件零模型 replay、两题定向 live 验证；`ToolErrorContractVersion` 不变（v2，budgetAlternatives/工具声明未动）、工具指纹不变——与 Pass³ v3 同前提，**具备整套 Pass³ 复评条件**；是否复跑由下一轮决定（上一轮 8/10 中两失败题即本题）。
