---
status: in-progress
created: 2026-09-24
---

# Change: Screening v2 证据落库质量修复

## Outcome

修复 Spec 6 认证（NO-GO）暴露的四类 Screening 证据落库缺陷，使模型输出在守卫与 reducer 的授权合同内可靠转化为需求事实。验收：修复代码冻结后，用当前 Screening 模型（deepseek-v4-flash-0731，零重试无链）做一次有上限（max-calls=400）的 development+calibration Pass³ 首跑，达到 **conversations task_success ≥6/7、key_field_wrong_write_total=0、veto=0**；分数不达标不修改冻结门槛，按结果决定是否迭代。

## Root causes（Spec 6 正式批次失败 + D4 诊断）

1. **B `cv-fps-progressive` r3**：模型把"那就7500吧"误标 `accepted_proposal`，而前一轮无提案；服务端 `verifyAcceptedProposals` 按合同拒收（行为正确）。根因是 prompt 对 stated/accepted_proposal 的判定顺序缺少明确指导。
2. **B `cv-composite-9000-start` r2**：模型把确定性参考中的系统默认 `budget_flex=0.1` 照抄成用户事实（quote 与弹性无关）。守卫无数值默认拷贝防线。
3. **B `cv-owned-model-flow` r2**：模型用单段点路径 `owned_parts.gpu` 携带对象值；守卫只归一 `owned_parts.<category>.model="M"` 形状，未知字段被整体丢弃（"尚未安全结构化"）。
4. **D `cv-monitor-refusal` r2（换模型诊断，已人工确认）**：泛购买语句"帮我配台电脑"被写成 `existing_parts=[]`——空已有件无任何"全部新买/无已有件"证据。守卫对空数组无证据红线。

## Fix scope

全部位于 `internal/agents/pipeline/screening_requirement_state.go`（评估对象是"模型+prompt+守卫"组合，服务端授权合同不变）：

- **prompt**：①evidence 判定顺序——本轮原话自己给出完整值一律 stated，`accepted_proposal` 仅用于采纳上一轮实际存在的建议，误标会被拒收且不会改判；②`effective_defaults` 是程序已应用的默认值，禁止照抄进 operations；③空 `existing_parts` 必须有本轮全部新买/无已有件原话，点路径误用说明程序会形状纠偏但不要依赖。
- **守卫**：①`normalizeOwnedModelPath` 扩展归一单段 `owned_parts.<category>`（对象/字符串值，类目与路径一致、quote 逐字），型号 grounding 仍由 reducer 校验；②空 `existing_parts=[]` 无"全新/新买/没有已有"等证据时降级 observation（关键词白名单，ponytail：口语变体漏报走追问补证不误写）；③数值型系统默认拷贝（当前唯一为 budget_flex 0.1）且原话无该值字面量时降级 observation（枚举默认 any 等是合法语义映射，不做字面量检查；ponytail："一成"类换算不在字面量内，漏报为可撤销 prefer 事实，无 veto）。

**二次补充（冻结 6c533be 首跑后新增，同根因族）**：修复首跑中模型在把型号并入 `owned_parts` 的同时多发无证据的 `remove existing_parts`（`cv-owned-model-flow` r2/r3，关键字段错写 ×2）——守卫新增撤销证据红线（意图动词+旧件/已有对象同时出现才执行移除，否则降级 observation），prompt 明确 existing_parts 与 owned_parts 并存、提交型号不代表撤销已有件。首跑（130 调用，task_success 6/7 达标、veto 0）存证于 `artifacts/reqv2/screening-fix-live-devcal-r3-20260924`。

**不放松授权合同**：`verifyAcceptedProposals` 原样；无提案/拒绝/询问边界仍按原合同拒收，不恢复任何 stated 数字降级路径。

## Out

- 不修改金标、grader、gates、冻结运行产物；已暴露 holdout 仅作历史诊断，pol-holdout 修正版须版本化转回归集、新盲测另建（评估设施侧后续项）。
- 不做模型选型与 prompt 大改；单因素为"守卫合同补齐+prompt 指导明确化"。

## Tests

- `screening_requirement_state_guard_test.go`：空已有件证据红线（反例=D4 实录原话；正例=全部新买/没有已有件；非空数组不受限）；数值默认拷贝拦截（B 实录反例；原话含值正例；非默认值不受限）；点路径归一（对象/字符串形状、类目不符拒绝）。
- `TestVerifyAcceptedProposalWithoutAnyProposalIsDropped`（product）：无提案时 accepted_proposal 拒收并保留观察；同句 stated 标签不受影响（证明拒收只针对误用标签）。

## Verification

- `go vet ./... && go test ./...`。
- 评估 `-mode check` 零网络通过（数据集/判卷身份未动）。
- 冻结提交后一次 dev+cal Pass³（首跑存证，不挑选重复），报告验收三项。
