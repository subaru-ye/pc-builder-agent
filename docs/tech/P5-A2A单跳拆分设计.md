# P5 A2A 单跳拆分 — 实现设计

> 本文是 **P5 实现层设计**:把「生成 + 校验」流水线包装为独立进程的 A2A 远程服务,初筛 Agent 改为远程消费方,跨进程只传结构化 schema。
>
> 口径边界:A2A 消息 schema 冻结在[设计方案](../装机Agent设计方案.md) §四,本文不重定义;编排拓扑承接 [P4 设计](P4-版本快照与增量改单设计.md)。开工前对照[工程实践指引](工程实践指引.md) §八与 §九 P5 检查单。冲突时以设计方案 + mvp.md 为准。

## 1. 目标与范围

- **DoD(用例 G,mvp.md §3.6)**:两进程分别启动,dev UI 端到端体验与 P4 完全一致;日志可见跨进程 A2A 消息且 schema 校验通过。回归:用例 A/C/D/E 不退化。
- **范围内**:新增 `cmd/buildsvc`(A2A 远程服务进程);`cmd/host` 改为初筛 + 远程消费方;`pipeline.NewRemote`(生成+校验半程装配)与 ingest 入口节点;`internal/schemas` 作为跨进程唯一 schema 出处;A2A 收发日志。
- **范围外**:三 Agent 全拆(设计方案 §二.3 M3 条,只拆一跳)、鉴权与部署、Redis 会话共享(P6)、卡片式改单(阶段 3)。

## 2. 拆分边界

单进程 P4 拓扑 `Sequential(初筛 → 改单预处理 prep → Loop(生成 → 校验))`,在**初筛与 prep 之间**切一刀:

```
进程 A: cmd/host                         进程 B: cmd/buildsvc(A2A 远程服务)
┌────────────────────────┐  A2A JSONRPC  ┌───────────────────────────────────────┐
│ Sequential(            │ ────────────→ │ Sequential(                            │
│   screening(llmagent)  │ Requirement/  │   ingest(确定性入口)                    │
│   remoteAgent(A2A)     │ ChangeRequest │   prep(改单预处理)                      │
│ )                      │ ←──────────── │   Loop(builder → validator, max=3)     │
└────────────────────────┘ BuildDraft +  │ )                                      │
   低价档模型              交付文本        └───────────────────────────────────────┘
                                            旗舰档模型 + Store + QueryEmbedder
```

- **host 进程**:仅需初筛低价档模型 + buildsvc 的 agent card URL;不再依赖 Store / 生成模型 / 检索。
- **buildsvc 进程**:持有生成旗舰档模型、Store、QueryEmbedder;承载重试回路(Loop 留在服务进程内,指引 §八/§九)。
- 模型型号仍只写代码常量(ADR-004):初筛档常量留 host,生成档常量迁 buildsvc。

## 3. ADK/a2a-go 接线(v2.1.0 实证)

**服务端(buildsvc)**——参照 `google.golang.org/adk/v2/examples/a2a`:

- `adka2a.NewExecutor(adka2a.ExecutorConfig{RunnerConfig: runner.Config{AppName, Agent, SessionService}})` 把根 agent 包成 A2A 执行器;
- `a2asrv.NewHandler(executor)` + `a2asrv.NewJSONRPCHandler(handler)` 挂在 invoke 路径;
- `a2asrv.NewStaticAgentCardHandler(card)` 挂在 `a2asrv.WellKnownAgentCardPath`;card 的 Skills 用 `adka2a.BuildAgentSkills(agent)`;
- 用 `net/http` 手工 `http.Serve`(无 webui 需求),包一层请求日志中间件满足「A2A 消息日志可查」。

**客户端(host)**——`google.golang.org/adk/v2/agent/remoteagent/v2`:

- `remoteagent.NewA2A(remoteagent.A2AConfig{Name, AgentCardProvider: remoteagent.NewAgentCardProvider(buildsvcURL), BeforeRequestCallbacks: […]})`;
- 作为 `Sequential(screening, remoteAgent)` 的第二个子 agent。

**会话映射(关键)**:执行器 `toInvocationMeta` 取 `sessionID := reqCtx.ContextID`;客户端每轮从上一条远程事件元数据复用同一 `contextID`。故 **buildsvc 用进程内 `session.InMemoryService()` 即可让 `build_state` 按 contextID 跨轮持久**(P6 再迁 Redis)。

## 4. 跨进程状态流(与 P4 等价的核心)

P4 靠会话 state 键串联;拆进程后 state 不过 A2A,只有消息内容过。三处对齐:

1. **只传三样(指引 §八.2)**:ADK `remoteagent` 默认把「自上次远程响应以来的全部会话事件」(含用户自然语言 + 初筛推理)打包转发,违反纪律。用 `BeforeRequestCallbacks` 在发送前把 `req.Message.Parts` 替换为**唯一一条**文本 part —— 从原 parts 里抽出的 RequirementSpec/ChangeRequest JSON 对象(复用 `extractJSONObject` 同款策略,优先取含 `schema_version` 的),丢弃对话全文与推理。`ContextID` 不动,保证跨轮连续。
2. **入口回填**:buildsvc 收到的干净 JSON 成为「用户消息」。新增 `ingest` 确定性节点(Sequential 首个子 agent):读最近一条 `user` 事件文本,写入 state 键 `requirement_spec`。prep 及其后全部沿用 P4,不改。
3. **build_state 归属 buildsvc**:校验节点交付时写的改单状态块留在 buildsvc 会话(按 contextID),下一轮 prep 读它做载荷分类与锁定 —— 改单链 v1→v2→v3 因此在服务端闭合。

**初筛的改单判定**:P4 靠 `{build_state?}` 注入;拆分后 host 无 build_state,该占位符按 ADK 可选占位语义**渲染为空**,初筛改依赖**会话历史**(host 会话累积的历次远程交付文本,如「已落库 版本 v2」)+ 既有改单 SOP 判定改单 vs 全新。此为本阶段主要行为风险,DoD 用例 C/D/E 回放专门验证;不额外回传 build_state 到 host(保持「只传三样」与低耦合)。

## 5. schema 校验落点(用例 G)

- **host 发送前**:`BeforeRequestCallbacks` 抽出的 JSON 用 `schemas.DecodeRequirementSpec` / `DecodeChangeRequest` 试解,记一行日志(类型 + 通过/失败);解不出也照发(容忍初筛追问轮),由服务端兜底。
- **buildsvc 收到后**:ingest 记录 contextID + 载荷字节数;prep/validator 沿用 P4 的严格解码(schema 不合法即在 Loop 内如实打回)。
- 进出两端共用 `internal/schemas` 一份定义(§九「schema 单一出处」),无第二处漂移。

## 6. 进程与配置

- buildsvc 监听 `BUILDSVC_ADDR`(默认 `:8081`);host 读 `BUILDSVC_URL`(默认 `http://localhost:8081`),解析其 well-known agent card。二者写入 `.env.example`。
- 启动:先 `go run ./cmd/buildsvc`,再 `go run ./cmd/host web api webui`。
- 诚实标注(指引 §八.5):单跳拆分是 A2A 学习目的,非性能需要;多 Agent token 开销数倍,本项目如实定位。

## 7. 验收步骤(DoD)

1. 双进程启动;dev UI 回放 v1「8000 元 2K 黑神话」→ v2「降 500」→ v3「换 A 卡」。
2. 查 buildsvc 日志:三轮均可见入站 A2A 消息(contextID 一致)+ schema 校验通过行;host 日志可见出站载荷类型。
3. `cmd/builds list/diff/export` 核对版本树与 P4 同形(用例 C/D/E 不退化)。
4. 用例 A(单轮全 pass)回归;`go test ./...` 全绿(pipeline 单进程 `New` 与集成测试保留)。
