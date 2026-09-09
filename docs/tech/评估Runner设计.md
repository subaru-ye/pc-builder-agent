# 评估 Runner 设计

> 更新：2026-09-09。依据当前 `cmd/eval` 与 `internal/evalsuite` 整理；覆盖已实现能力并明确验证边界。方法见[Agent 的评估](../eval/methodology/01-Agent的评估方法论精读.md)，用例契约见[评估集建立记录](../eval/评估集建立记录.md)。

## 1. 目标与范围

在固定用例、快照和模型配置下，检测提示词、检索、规则与修复策略的回归。评估对象是模型与 Harness 的组合，确定性断言负责打分。

- 当前 v1.5 的 12 条 L1 整机、10 条 L2 改单及 9 条 L4 构建边界题直接调用 `buildharness`，输入结构化需求，不经过产品初筛与持久化交付层。
- 5 条 L3 初筛及 4 条 L4 初筛输入自然语言，v1.4 共 40 题。v1.5 原样保留这 40 题，另外增加 10 段 L5 需求确认前的对话；初筛调用 ADK InMemory 会话，使用产品同口径 `pipeline.ExtractPayload`。
- `-seeds N` 为每个用例重复执行 N 次；编号不传入模型采样参数，不能称为不同随机种子的确定性实验。
- 产品已有负反馈保存和本机待复核候选导出，见[反馈与评估回流](反馈与评估回流.md)；正式入题仍需人工复核。消融开关、调用用量记录和配对比较工具已实现，真实对照正在按[实验顺序](评估对照实验.md)推进；异构 Judge 报告质量尚待验证。完整产品链路由 Web Live 测试补充。

## 2. 执行入口

```bash
go run ./cmd/eval -mode run -snapshot-date 2026-07-28 -seeds 3
go run ./cmd/eval -mode replay -dir artifacts/eval/<时间戳>
```

快照日期必须显式给出，且存在于数据库；示例日期用于已有基线，不能视为最新数据。产物为 `cases.json`、`meta.json`、`results.jsonl`、`report.md`，写入带时间戳和唯一后缀的 `artifacts/eval/` 子目录，不入库。更换固定模型后需要新建基线，不能直接继承额度链成绩。当前默认版本见[评估集 v1.5](../eval/versions/v1.5.md)。

## 3. 设计

### 3.1 快照与依赖

`CatalogSnapshotByDate` 为候选、校验和报价提供指定价格批次；查询仍依赖当前目录状态，日期参数不等于完整版本化历史目录。候选与价格通过 pinned adapters 传入评估；Embedding 直接调用配置模型，评估不使用 Redis embedding 缓存。

### 3.2 用例契约

fixtures 位于 `internal/evalsuite/testdata/cases/`，`testdata/suites/v1.5.json` 固定当前题目顺序与内容哈希；旧清单保持不变。build 包含 RequirementSpec、期望及可选 ChangeRequest、BaseSelection、Locked；screening 包含用户文字和 spec/clarify 期望。clarify 的 `clarify_fields` 指定必须追问的字段，支持 `budget_cny`、`resolution`、`owned_parts`、`budget_basis`、`use_case`；`forbidden_clarify_fields` 指定禁止追问字段，两组不能冲突。旧 fixture 未填写时仅检查基本询问形式。字段解码复用 `internal/schemas`。运行前校验清单，使用同一次读取的内容执行并保存完整副本，避免读取与执行使用不同题目。

### 3.3 断言与否决

多轮 screening 使用 `turns`（2–8 轮），每轮固定用户输入和期望。runner 回填真实前轮可见回复，复用产品 `BuildScreenInput` 的有界历史及 `ParseScreeningResult` 规范化；确认提示不当成已保存配置或用户事实。每个重复编号重跑整个独立对话，所有轮次通过才计一次正确。`spec_fields` 支持新题显式检查用途、静音、尺寸、采购口径、弹性和已有件；S0 检查轮数。具体新题见[v1.5](../eval/versions/v1.5.md)。

build 成功交付断言 A1–A8 覆盖结构、快照、预算、品牌、SKU 成员、报价合计和锁定；新增 A9 复验已有件与采购报价，A4/A6/A8/A9 为 veto。期望 pass 而未交付时记录 A2；期望非交付时用 N1 校验类型、原因和证据，不把任意失败当正确拒绝。veto=0 不代表所有执行成功。

v1.4 新题 L4-215 的 `budget_adaptive` 接受合格交付或预算不足证明：交付执行全部适用硬断言；非交付仅允许 `catalog_infeasible` 的独立品类/平台预算下限原因，并重建金额、范围、快照与程序说明。搜索耗尽、缺数据及接口错误均不能借此通过。旧 L1-004 的 pass 期望不改写。

screening 的 S1 检查 JSON 形态，clarify 必须是非空文本且不含 JSON/代码块；S2 检查预算、分辨率和品牌；S3 检查是否追问了每个指定字段（预算、分辨率、已有件型号、预算口径），也拦截缺已有件必要信息却输出需求单的行为。S3 是中文关键词与询问/请求词在同一短句内匹配的确定性近似规则，支持常见问法和请求补充信息的句式，过滤常见否定/拒绝表达。它不等于开放域语义理解，仍可能误判生僻表达，遇到争议需查看原文并人工复核。

S4 仅对显式声明 `forbidden_clarify_fields` 的新题检查重复追问，区分已知信息复述与请求再次确认，也区分预算金额与预算口径。请求列表和简短尾问有范围限制；触发短句进入失败明细，供人工复核。该近似规则不能替代开放域语义评审。

### 3.4 replay 边界

新产物通过 `cases.json` 恢复 Locked/BaseSelection 和期望，重判 build（包括 A8/A9/N1）；新记录保存完整目录供非交付证据重建，并校验清单哈希和重复记录完整性。screening 在 `results.jsonl` 的 `screening.text` 保存最终可见回复（不含思考内容），重放从原文重新执行 `ExtractPayload` 和 S1/S2/S3/S4，不信任旧分数或从当前题库读取期望。空字符串是实际空回复，缺失对象是缺少证据。

`meta.json` 的 `record_schema_version=1` 标识回复必须留存的新记录；无执行错误却缺少 screening 回复时重放报错（退出 2）。旧产物没有原文时保留旧结论，在报告标为“部分重放”，不宣称已复验；旧产物没有完整题目副本时仍无法补验 A8。执行错误保留原记录，不把残留文本当成功输出。可重判记录比较完整 Verdict（含各断言及 veto）与归因，任何差异退出 1；一致退出 0，即使复现的是失败。首跑程序标识保留，重判程序哈希单独展示。

初筛程序核验启用后，新增可选 `screening.model_text`（拦截前的模型回复）和 `screening.missing_fields`（核验发现的字段）。打分及重放仍基于最终可见的 `screening.text`；原文用于判断改善来自模型还是程序兜底，不把程序拦截后的通过率称为模型原始正确率。旧记录未保存这两个字段时不补造原文，也不追溯重跑新核验。

多轮保存 turns、context、user_sources 及规范化前 guard_text。`CheckDialogueEvidence` 在 replay 和对照工具中共用：从冻结题目和前轮真实输出重建上下文与有效载荷，拒绝缺轮、串题和额外答案。初筛格式重试最多一次，所有尝试保存于 model_attempts，最后一次仍在 model_text；重放不调用模型、不重新执行重试。

已有件澄清文案通过 Decision 的 `explanation_version` 演进：0 保留原精确模板，1 按已证明缺失的字段生成针对性追问。N1 仍逐字段、逐字重建相应版本，拒绝未知版本或任意说明；修复追问文案不会覆盖旧成绩。

修改初筛核验规则后，可额外设置 `SCREENING_GUARD_RUN_DIR` 为完整运行产物的绝对路径，执行 `go test ./internal/agents/pipeline -run TestScreeningGuardSavedRun -v`。这项零模型审计用保存的用户题目和模型原文重新执行当前核验，逐条比较最终回复及缺失字段，检查数量和重复项；须配合正常 `eval -mode replay` 的哈希及判卷验证，不能拿重处理后的输出覆盖首跑成绩。

### 3.5 指标与门禁

报告将通过项拆分为成功交付、合理非交付、单轮初筛正确、多轮对话正确及历史未分类。Pass@1 表示任务处理正确率，不能称为装机交付率；通过的合理非交付仍保留 `Succeeded=false`、类型和证据。修改验收契约带来的跨版本分差须单列解释。

真实运行 error 一律记失败，不按“没有候选”等文案排除分母；结构化 data_unavailable 作为业务结果判卷。历史成功交付缺价的 data-error 判卷兼容路径仍保留。记录级通过率以非 data-error 记录为分母；用例级 Pass^k 要求全部重复通过且零 veto，含 data-error 的用例从该比率分母排除，但 `AllGreen()` 仍要求无失败、无 data-error。版本化产物在 replay 时拒绝缺失、重复、越界或未知用例记录；历史产物仍需人工核对完整性。当前 50 场景用于方向性回归，不能支持精细模型排名。

### 3.6 模型与归因

builder/screening 使用 `modelprovider` 独立配置。元数据保存三角色的脱敏模型、端点主机、推理配置、缓存、超时和链，以及启动工作区 Git commit/dirty、Go 版本和实际程序 SHA256。固定回归清空切换链；额度链评估另标口径，配置不是每次实际调用记录，链首名称不等于每次服务模型。更完整的逐请求记录仍待补充。

归因是失败断言到领域码的规则映射，首个映射项标为主因；它不是自动完成的因果分析。根因仍需检查候选、输入契约、修复轨迹和数据可达性。

### 3.7 对照与用量

`-attempt-limit`（1–3，默认 3）与 `-semantic`（默认 true）写入 `harness_profile`。逐记录 Usage 统计逻辑模型/Embedding 调用、返回 token 元数据的调用数及已知 token 总量；nil 表示未测量，缺少上游用量不能当作零。流式累计用量只取每次调用最后一条，适配器内部 HTTP 重试及 Embedding token 未测量，不换算费用。

`candidates` 保存本轮实际候选包的独立副本，提前非交付没有候选时不沿用上一题。`cmd/evalcompare` 复验两组题目/完整断言/多轮上下文后，要求同二进制、同清单、同目录快照和唯一预定变量。效果仅在该变量对应阶段计算，逐题保留重复结果及用量；按题目聚类的 bootstrap 区间不等于小样本已经证明等效。语义实验另列候选增减、选用 SKU 与匹配文本，不能把检索内容存在当成偏好质量已经验证。执行顺序见[评估对照实验](评估对照实验.md)。

## 4. 副作用与兼容

评估真实调用模型并读取数据库，但不经过产品版本保存层，不写 builds/versions。replay 不调用模型，目前会覆盖同目录 `report.md`，原 `results.jsonl` 保留；比较历史报告前应注意此行为。

## 5. 验证

离线断言与 runner 测试使用 fake 依赖；数据库集成测试受 `PG_TEST_DSN` 门控。真实评估必须显式运行。修改用例期望前先确认产品契约，不为通过率降低约束；每次运行追加口径与产物路径到[运行记录](../eval/运行记录.md)。
