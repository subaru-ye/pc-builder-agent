# 偏好记忆评估(确定性基线,2026-09-29 冻结)

> 对应设计:[偏好记忆设计草案](../../tech/偏好记忆设计草案.md);阶段四收口。
> 状态:**六类场景已落成可运行评估并冻结基线**(见[基线 2026-09-29](基线-20260929.md))。
> 阶段五(使用证据):零真实流量已确认,召回漏斗口径已冻结
> ([使用证据](使用证据-20260929.md)),当前唯一合法证据来源是
> [小规模真实用户走查脚本与记录表](走查脚本-召回漏斗-20260929.md)(未执行,不虚构数据)。
> 评估对象是当前产品合同:用户显式保存稳定偏好、按显式选择的 subject 手动召回、逐项确认;
> **无自动提取、初筛或 Builder 模型注入,本评估不宣称任何模型效果**。
> 入口 `cmd/evalpreference`(check/run;另有 handoff-check/handoff-run 见下文
> [Builder 交接确定性评估](#builder-交接确定性评估2026-09-29分支-codexmemory-builder-handoff-eval)),
> 实现于 `internal/planningeval/preference_memory_eval.go`;
> 全程零模型:需求状态由 scripted screening 输出经真实产品管道(StartMessage → pipeline →
> Reducer → store)产生,保存/召回/确认/删除全部走真实 `product.Service` 与一次性
> `peval_prefeval_*` 临时库(结束物理删除,不触碰主库)。

## 通用判定原则(冻结,对所有场景生效)

1. 用户确认的当前需求永远高于历史记忆;两者冲突时行为以当前需求为准。
2. 召回的偏好只能以"待确认建议"出现,确认前不得进入 RequirementState 的 must 约束或规则引擎;
   未确认的建议被静默忽略不算失败,自动变成硬约束算失败。
3. 建议呈现时必须可追溯到来源(哪次会话、什么原话、什么时间);多 owner 冲突时
   **每个候选值**都须携带各自来源(2026-09-29 修复了前端只显示首选原话的缺陷)。
4. 保存白名单仅有品牌、静音、尺寸、外观;预算、价格和规格不能经产品链路保存为偏好,
   相关历史事实场景只测拒绝写入。

## 场景与用例(冻结)

| 用例 | 分类 | 覆盖 | 关键断言 |
|---|---|---|---|
| PM-ISO-01 | iso | 本人与代配对象隔离 | self/小王 召回各只返回对应 subject;确认 self 不写入小王字段(size_pref 保持 unknown) |
| PM-ISO-02 | iso | 多 owner 冲突 | 同字段不同值列 conflict 双选项;每个候选的原话互异且与归属身份消息吻合;确认只写入选中值 |
| PM-TMP-01 | tmp | 临时要求不升级 | temporary 在会话内生效、保存被拒(preference_not_savable);存储层无新长期记录;新会话召回仍是被覆盖前的长期值 |
| PM-CHG-01 | chg | 用户更改主意 | 值变化 supersede(旧值墓碑、active 链接前值);新会话只召回新值;重申同值 unchanged 不新增行 |
| PM-DEL-01 | del | 删除后不再召回 | 物理删除 active+墓碑(行数 0);召回为空;重复删除稳定 404 |
| PM-STALE-01 | stale | 旧价格/规格拒绝保存 | 预算/用途/free.* 保存被拒;存储 0 行;召回为空;预算只由当前会话表达 |
| PM-CONF-01 | conf | 确认前不生效+当前需求优先 | 未确认不写入(gpu unknown);当前会话已明确字段不再建议且不被覆盖;确认后按 kind=edit 写入;预算拒存不影响新会话 |

## 门禁(preference-eval-gates-v1,全部确定性)

- `technical_fault = 0`:评估设施自身故障零容忍(harness error 与产品行为失败分开)。
- `failed_assertions = 0`:全部断言(含负向断言)必须通过,不与正向断言抵消。
- `failed_cases = 0`:任一用例出现失败断言即门禁失败,退出码非零。
- `categories_covered = 6`:六类分类必须各有用例(check 模式静态验证)。

## 运行方式

```bash
# 零网络自检场景注册表(六类覆盖、ID 唯一)
go run ./cmd/evalpreference -mode check
# 确定性基线:一次性临时库,结束删除;-out 目录必须不存在
PG_TEST_DSN=postgres://<user>:<pass>@127.0.0.1:15432/postgres?sslmode=disable \
  go run ./cmd/evalpreference -mode run -out artifacts/preference-memory/<新目录>
```

产物:`report.json`(逐用例逐断言)、`report.md`(结果表+失败明细+边界+脱敏复现命令)。
临时库在服务器上 CREATE/DROP,名字以 `peval_prefeval_` 开头;`-dsn` 只接受 localhost。

## 已知边界

- PM-STALE-01 **仅验证拒绝写入**:易失事实(旧价格/规格)经保存入口被白名单拒绝。
  代码里的 7 天新鲜度窗口(`preferenceVolatileWindow`)是召回侧对不存在写入路径的防御,
  不是"可保存易失事实"的产品能力;若未来另立易失事实写入合同,再冻结 `observed_at`
  与新鲜度窗口的正向场景,当前不把窗口写成已有产品能力。
- 多 owner 冲突以服务层双 owner 直连构造(同账号多 owner 的自然路径是匿名认领,
  该 auth 流程由浏览器走查与 auth 测试覆盖,评估不重复)。
- 聊天中的"忘掉"不自动解释为删除(本阶段删除只走显式删除操作),未单列场景。
- 评估不经过 HTTP 层(API 已由 `internal/producthttp` 测试覆盖);评估直接驱动
  `product.Service`,与浏览器走查(真实 UI+API+PG)互补。
- 自动提取、初筛召回注入、Builder 模型注入均未接入,也不在本评估范围。

## Builder 交接确定性评估(2026-09-29,分支 codex/memory-builder-handoff-eval)

> 状态:**四类交接场景可运行、门禁全绿**(真实结果见[运行记录](../运行记录.md))。
> 追踪用户确认偏好后,值与 must/prefer 强度从 `RequirementState` 经需求投影
> (`RequirementStateSpec` → 核定预览 `RequirementReviewSpec`)到确认事务冻结的
> Builder 载荷(`PlanningBuilderInput` → `PlanningInput.EffectiveConstraints`)的
> 确定性传递。这是阶段四"确认写入 RequirementState"向下游 Builder 交接的延伸,
> 仍属用户显式召回确认的合同范围——**不是**自动召回注入(那仍在待定表)。
> 入口 `cmd/evalpreference -mode handoff-check | handoff-run`,实现于
> `internal/planningeval/preference_builder_handoff_eval.go`;复用阶段四的
> prefEvalEnv/scripted screening/确认写入设施,临时库前缀 `peval_handoff_`。

**scripted builder 口径**:确认走真实 `StartConfirm`,确认事务在 store 内冻结
完整 `PlanningInput` 并由 `executeRemote` 派发;gateway.Remote(scripted builder,
零模型)记录实际收到的完整冻结载荷,断言只读该载荷与 run 冻结的
`builder_input_hash`(V5 同口径:两者规范化 hash 必须相等)。scripted builder
无 oracle 输出,planning 以失败终态结束是刻意行为,不评估选件质量(planning-v2
范围);builder 未收到载荷按 technical fault 暴露。

| 用例 | 分类 | 覆盖 | 关键断言 |
|---|---|---|---|
| PM-BH-01 | unconfirmed | 未确认不进入载荷 | 建议在但不确认:State 保持 unknown;冻结载荷该字段按系统默认展开(any)、无 strength 键;记忆行无副作用 |
| PM-BH-02 | confirmed | 确认后保真进入 | must(noise_pref)+prefer(brand_pref.gpu)确认后:State 与冻结载荷的值、`constraint_strengths` 逐项一致;载荷内 RequirementState 同步携带 |
| PM-BH-03 | current-first | 当前会话优先 | 本轮明确 nvidia/must 进入载荷,历史 amd 不出现在建议与载荷;强度取当前会话表达 |
| PM-BH-04 | owner-conflict | 冲突未选不进入 | 双 owner 冲突悬而未决时载荷无该字段(默认 any、无 strength);选择 nvidia 后所选值与强度进入 |

门禁(preference-handoff-eval-gates-v1):`technical_fault=0`、`failed_assertions=0`、
`failed_cases=0`、`categories_covered=4`。运行方式与阶段四一致,仅 mode 换为
`handoff-check` / `handoff-run`。

已知边界(除阶段四边界外):

- 评估到"冻结载荷被 scripted builder 收到"为止,不执行真实选件,不验证
  `requirement_constraints` 的 must/prefer 门槛行为(该层由 buildharness 单测覆盖,
  选件质量由 planning-v2 覆盖)。
- 失败重试(RetryOfRunID)继承冻结载荷的路径未单列场景(与首次确认共用同一
  `PlanningBuilderInput` 组装,由 store 测试覆盖)。
- 场景措辞须命中 `existingPartsClearedEvidence` 白名单("没有旧件"等):空
  existing_parts 是 scripted screening 守卫的关键字段,泛化措辞会被降级为观察、
  状态不 ready——这是产品守卫按设计工作,不是评估缺陷。

## 历史评审与修复记录

- 2026-09-29:冻结基线(7 用例、49 断言、门禁 4/4 通过);修复评估设施断言
  `errors.As` 指针/值类型错误(technical fault 类);修复前端冲突建议只显示首选
  原话的缺陷(多 owner 冲突时每个候选值各自展示来源原话,组件测试覆盖)。
- 2026-09-27:浏览器走查 10 步全过(见[走查记录](浏览器走查-20260927.md)),为本评估的场景来源。
