# 偏好记忆评估(确定性基线,2026-09-29 冻结)

> 对应设计:[偏好记忆设计草案](../../tech/偏好记忆设计草案.md);阶段四收口。
> 状态:**六类场景已落成可运行评估并冻结基线**(见[基线 2026-09-29](基线-20260929.md))。
> 阶段五(使用证据):零真实流量已确认,召回漏斗口径已冻结
> ([使用证据](使用证据-20260929.md)),当前唯一合法证据来源是
> [小规模真实用户走查脚本与记录表](走查脚本-召回漏斗-20260929.md)(未执行,不虚构数据)。
> 评估对象是当前产品合同:用户显式保存稳定偏好、按显式选择的 subject 手动召回、逐项确认;
> **无自动提取、初筛或 Builder 模型注入,本评估不宣称任何模型效果**。
> 入口 `cmd/evalpreference`(check/run),实现于 `internal/planningeval/preference_memory_eval.go`;
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

## 历史评审与修复记录

- 2026-09-29:冻结基线(7 用例、49 断言、门禁 4/4 通过);修复评估设施断言
  `errors.As` 指针/值类型错误(technical fault 类);修复前端冲突建议只显示首选
  原话的缺陷(多 owner 冲突时每个候选值各自展示来源原话,组件测试覆盖)。
- 2026-09-27:浏览器走查 10 步全过(见[走查记录](浏览器走查-20260927.md)),为本评估的场景来源。
