# Change Spec：Builder 工具参数合同（builder-tool-param-contract）

日期：2026-09-27　分支：`fix/builder-tool-param-contract`（基于 main 77314a9，该提交已整合 fix/bv2-104-owned-standin 全部内容）　状态：审计完成，最小校验修复

## 1. 零模型审计（Pass³ v1/v2/v3/v4 十二份冻结 events.jsonl，逐 call ID 对齐）

总 planning_action 调用 2257 次；`error` 类响应 **91 次（4.0%）**、`unavailable` 类 25 次
（服务容量/可用性，非参数问题）。错误高度集中：**91 次中 84 次落在 8 个 case 轮**
（BV2-104×42、BV2-102×17、BV2-107×9、BV2-108×7、BV2-103×7）；120 个 case 轮中 112 轮
零参数错误。**全部 case 最终通过，没有任何 case 因参数错误直接失败**；代价是额外的
模型往返（最差一例：BV2-104 在 pass3v2-r3 对同一 null 内存 draft 连续 6 次 evaluate
被拒）。

### 错误簇分类

| 簇 | 次数 | 根因 | 定性 |
| --- | --- | --- | --- |
| `build selection: 部件 X 的 SKU 不得为空`（memory 20/cpu 5/cooler 4） | 29 | 模型对七类必选槽位送 null（BV2-104 留空内存、BV2-103 cpu、BV2-108 cooler），且重复发送同一非法 draft | 模型失误 ×2：接口错误已点名部件，但**不指明正确形态与重试方式**，恢复靠模型自行领悟（见 §2） |
| `payload须为JSON对象字符串` | 17 | 模型生成的 payload JSON 字符串**本身语法非法**（内层 draft 文本值含未转义引号等，解析错位如 char 656）；错误信息无位置提示 | 模型失误；接口错误过于笼统 |
| `build draft: EOF` | 6 | evaluate 的 payload 直接给 draft 对象、缺 `{"draft":…}` 包裹 | 模型失误；接口错误不说明包裹形态 |
| `queries须含1至8个本地查询` | 5 | BV2-102（pass3v4-r2）：空 `{"queries":[]}` 与超 8 项批量；错误未说明合法范围语义已被说出但无字段级指引 | 模型失误，边界清楚 |
| `未知工具操作` | 4 | action 拼错（如 `noop`）；错误未列出合法 action | 模型失误；接口可列合法集 |
| `build draft: build_ref 缺失或为空` | 1 | 漏字段 | 模型失误，错误已点名 |
| register_candidate 域校验（候选不存在/须正文来源/须 ext-编号） | 25 | 领域规则正确拒绝且已给指引 | 接口正常工作 |
| evaluate 引用目录外 SKU | 4 | 引用已有件型号（无 SKU），错误已含 SKU 名 | 接口正常工作 |

**接口缺陷（与服务端静默行为相关，审计新发现）**：search_local 对显式 `limit≤0 或 >24`
**静默改写为 16**、显式 `offset<0` **静默改写为 0**；payload **未知字段静默忽略**；
**category 不校验**（拼错品类静默返回空结果，模型无法区分"目录无货"与"参数拼错"）。
冻结事件中未捕获到被静默改写的实例（未观测到显式越界 limit/offset），但它们是
"显式非法值被静默改写"的既有接口缺陷，按合同修正。

### BV2-102 r2 专项核对

用户指认的"BV2-102 r2 search_local_batch 参数错误"核定为 **pass3v4 r2（2026-09-27）**：
5 次 `queries须含1至8个本地查询`（4×空数组 + 1×超 8 项），均当场以更正后的批量重试
成功，case 通过——恢复链完好，属偶发模型失误，无接口缺陷。另：pass3 v1 的
BV2-102 曾出现 `payload须为JSON对象字符串` ×9（payload JSON 语法非法），同属模型侧
语法失误；两处均**不构成把 payload 字符串改为嵌套 schema 的依据**（见 §3）。

### 接口改造评估

planning_action(action, payload 字符串) 在现有提供方（deepseek-v4-flash-0731 @ bailin，
genai function-call）下改为嵌套参数 schema 的收益不确定且代价高：审计显示 payload
字符串形态下模型已会产出非法 JSON（17 次），嵌套深层对象同样依赖模型 JSON 生成能力；
改造将变更工具指纹与提示词、需要全新冻结与配对重测，而参数错误仅 4.0% 且全部恢复。
**结论：不做接口重构，走"至少"分支——清晰校验。**

## 2. 最小修复（校验层，不改执行语义）

`internal/planning/runner.go` 的 `call`：

1. **payload 语法错误带定位**：JSON 语法错误时报告字节偏移（`payload JSON 语法错误（偏移 N）`）。
2. **逐 action 必填/未知字段校验**：
   - search_local：允许 `{query,category,order_by,offset,limit}`，未知字段报错并给出允许集合；
     `category`（显式给定时）必须在八品类内，错误列出合法值；
     `order_by` 缺席→文档化默认 relevance（不变），显式非法值报错（原有，保留）；
     `limit` 缺席→文档化默认 16，显式值必须在 1..24，越界报错（**取代静默改写 16**）；
     `offset` 缺席→默认 0，显式负数报错（**取代静默改写 0**；offset 超出目录长度的
     分页截断语义保留——空页+next_offset 是分页语义不是非法值）。
   - search_local_batch：`queries` 1..8（原有）；子查询经同一路径校验，结果按 index 返回（原有）。
   - evaluate：payload 必须含非空 `draft`；缺失时错误说明包裹形态 `{"draft":{...}}`。
   - read_evidence：`id` 必填；search_web：`query` 必填；read_page：`url` 必填。
   - 未知 action：错误列出全部合法 action。
3. **不改**：额度（24 次工具/批量计额）、分页与截断语义、真实核验、search_web/semantic
   的 unavailable 语义；不新增依赖。

**错误反馈全部满足**：指出字段、允许范围/合法值、可重试方式（整体修正后重试）。

## 3. 合同版本与可比性

- `ToolErrorContractVersion` v2→**v3**（参数校验规则与错误文案结构变化；提示词与工具
  声明不变，工具指纹 `11c6a31c…` 不变）。
- **可比性**：本分支之前的一切批次（含 Pass³ v1–v4）为 v2/v1 合同；v3 批次与旧批次
  不构成同前提配对。旧 Pass³ 分数（v4=9/10 折叠等）不可直接同前提比较。
- 不改现有评估金标（冻结套件/FrozenConstraints/判卷）。

## 4. 验证计划

红例先行（显式越界 limit/offset、未知字段、非法 category、payload 语法错定位、
evaluate 缺包裹、未知 action 列合法集）→ 修复后绿；聚焦测试、`go test ./...`、
builder-v2 机制套件零模型 replay。代码冻结后跑**一次**定向 live（派生 2 题套件
BV2-102+BV2-104——两大量产错误簇代表，调用上限 30，plan-first，零重试，不跑
holdout），报告参数错误率、恢复次数、调用量、延迟与任务结果的前后对照。

## 5. 验证结果

- **红例→绿**：8 项参数合同聚焦测试（显式越界 limit/offset、未知字段、非法 category、payload 语法错定位、evaluate 缺包裹、未知 action、默认值/合法值不误伤）——修复前 7 红（1 正例控制绿），修复后全绿。实现中发现并修正一处允许集遗漏（register_candidate 的 `external` 为 Candidate 合法线上字段）。
- **回归**：`go test ./...` 0 失败；`go vet ./...` 干净；builder-v2 机制套件零模型 replay **10/10**（`artifacts/builder-v2-20260925-mech-r2-postfix-v42-paramcontract-20260927`）。
- **合同**：`ToolErrorContractVersion` v2→**v3**（已在 runner.go 注释登记）；提示词/工具声明未动，指纹 `11c6a31c…` 不变。**旧批次（含 Pass³ v1–v4 的 9/10 等分数）与 v3 批次不构成同前提配对，不可直接比较。**

### 定向 live（一次，调用上限 30，plan-first，零重试；套件=grading-v2 派生 BV2-102+BV2-104 两题，sha `0e250941…`）

| 指标 | 修复前（冻结 Pass³ 事件，同两题的历史轮） | 修复后（定向 live） |
| --- | --- | --- |
| 参数错误 | BV2-102：v1-r1 9 次 payload 非法 JSON＋1 次未知 action、v4-r2 5 次 queries 越界；BV2-104：v2-r3 6 次 null 槽位等、v4-r2 9 次 | **0 次**（11 次工具调用） |
| 任务结果 | 各轮均最终通过/收口（靠重试恢复） | 2/2：BV2-102 ready（4 次调用）、BV2-104 clarify 取舍收口（7 次调用） |
| 调用量/延迟 | — | 11/30 次 builder 调用；tokens 293,771；2.9 分钟；p50 12.0s / max 42.0s |

样本仅 2 题 11 次调用，"0/11 参数错误"只是**小样本观察**，不得表述或理解为"错误率从 4% 降至 0%"——4% 是十二轮 2257 次调用的统计，两者样本与前提不同；价值在于错误信息现按"字段＋允许范围＋重试方式"返回，且显式越界值不再被静默改写。产物：`artifacts/builder-v2-20260925-param-directedlive-b-20260927`（预检 `-preflight-` 留存）。

## 6. 结论

- 审计显示参数问题（4.0%，集中于 8 case 轮，全部恢复）**不足以支持接口重构**（payload 字符串→嵌套 schema），按"至少"分支落地清晰校验；未做大重构。
- 是否值得整套复评：**暂不必要**。参数合同只影响错误恢复效率与静默改写边界，未观察到错误 ready/账实影响；待下一次有理由的整套 Pass³（如目录或模型变更）时以 v3 合同为准即可。剩余风险：错误信息更详尽可能轻微增加 token 占用（本轮 +1.5%/调用以内，可忽略）；register_candidate 允许集若模型使用别名仍会被拒（错误会点名合法集，可恢复）。

## 7. 收口返工（2026-09-27 同日，静态审阅三处静默通道，零模型定向）

审阅 v3 实现发现三处与"显式非法值不静默改写"不一致，逐处红例→入口拒绝：

1. **payload 显式 `null`**：解码为 nil 对象静默通过，search_local 按空参数执行全品类检索。红例 `TestParamNullPayloadRejected`（修复前返回候选集）；修复后报"payload 为 null：必须为 JSON 对象…"。
2. **批量子查询未知字段**：类型化解码＋重新序列化会吞掉子查询拼错字段（`catgory`），绕过未知字段检查。红例 `TestParamBatchSubqueryUnknownFieldRejected`（修复前照常执行）；修复后在批量入口对每个子查询按 `queries[i]` 点名拒绝（允许集＝search_local 字段集）。
3. **窗口参数静默改写**：read_evidence/read_page 的显式 limit/offset 仍由 `evidenceWindow` 静默改写为默认（limit≤0/>16000→16000、offset<0→0）。红例 `TestParamWindowParamsRejected`；修复后 `checkWindowParams` 在入口拒绝（limit 1..16000、offset≥0，缺席走文档化默认）。

**合同版本 v3→v4**（参数校验规则再次变化；此前的定向 live 产物记录的是 v3 合同，出处可分）。**报告口径更正**：上节定向 live 的"0/11 参数错误"为小样本观察，不得表述为"错误率从 4% 降至 0%"。**回归**：11 项参数测试全绿；`go test ./...`＋`go vet ./...` 全绿；机制套件零模型 replay 10/10（`artifacts/builder-v2-20260925-mech-r2-postfix-v43-windowbatch-20260927`）。本轮零模型定向返工，不重做审计、不跑 Pass³；v4 合同下的 live 验证随下一次有理由的 live 批次进行。
