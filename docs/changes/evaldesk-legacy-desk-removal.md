---
status: done
created: 2026-10-06
---

# Change: 移除 /eval 历史评估桌,Requirement v2 单桌按两侧组织

## Decision

推翻 `requirement-v2-evaldesk-workbench.md` 中"历史评估仍可进入、旧 API 维持兼容"的约束:`/eval` 只保留 Requirement v2 桌,历史评估(legacy)桌连同它的前端组件与 Go 旧只读接口一并删除。理由:两套信息模型(六层/门槛三态/Pass^k vs 基线候选对比/执行通过率)共居一个入口造成持续的结构性复杂度——双份 URL 参数空间、深链兼容层、无 enabled 门控的 legacy 查询在 v2 桌也一直请求。

同时把 v2 运行详情的分层展示从"确定性层/模型层"改为按角色语义二分:**初筛 Screening 侧**(extraction、conversations、reducer、readiness——真实 Screening 模型与需求状态机)与**选配 Builder 侧**(policy、ui-contract——Builder 准入门与工作台合同;Builder 输出质量归 planning-v2,页内注明)。

## Removed

- Web:`components/evaldesk/{navigation,catalog,provenance,change-summary}.tsx` 及两个样式模块、`lib/evaldesk/{client,types,catalog}.ts`、`e2e-evaldesk/{history,provenance}.spec.ts`;`workbench.tsx` 削成薄壳(标题栏 + navigate + ReqV2Workbench),侧栏与两桌切换删除,旧 `desk`/`baseline`/`candidate` 深链参数被忽略落到 v2 目录。
- Go:`internal/evaldesk/{compare,details,provenance,changes,types}.go` 与对应测试、`/api/evaldesk/{runs,compare,cases,timeline,provenance}` 路由、`store.go` 的 legacy 扫描(`artifacts/eval`、`artifacts/evalchange`)。`store.go` 只保留 reqv2 复用的基础设施(EvalSymlinks 防护、safeFile、read);Windows junction 测试改写为针对 `artifacts/reqv2` 扫描。
- 不删除任何盘上产物;`cmd/eval`、`cmd/evalchange` 工具保持可用,只是产物不再有 UI。

## Fixed along the way

- 真实 `gate_verdicts` 只有 extraction×7 / model×4 / conversations×3 / all×1 / extraction+conversations×1,旧前端的"确定性层门槛"分组从不渲染,且 `all` 与 `extraction+conversations` 两行被静默丢弃。新分组为**初筛侧门槛**(extraction/conversations/model)+ **跨层门槛**(all、extraction+conversations)+ 兜底"其他"组(未知 layer 原样列出,不再丢弃),并注明确定性层 100% 折算进总结论。
- 同身份对比此前未渲染 `model_layers`;现按两侧展示 `model_layers` 与 `deterministic_layers`(层名用中文标签)。
- Playwright 夹具的 `gate_verdicts` 修成真实契约形状(去掉臆造的 reducer 门槛行,补 all / extraction+conversations),并新增"被丢弃门槛行可见"回归断言。

## Verification

- `go test ./... && go vet ./...`(internal/evaldesk 含改写后的 junction 测试)
- `cd web && pnpm typecheck && pnpm lint && pnpm exec vitest run`
- `pnpm exec playwright test --config=playwright.evaldesk.config.ts`(合成夹具 + 本机真实产物抽查)
- 手工冒烟:运行详情 16 条门槛全显示(modelcmp-glm-devcal-r3-20260924 的 latency FAIL 行)、两侧分组、对比页 model_layers。
