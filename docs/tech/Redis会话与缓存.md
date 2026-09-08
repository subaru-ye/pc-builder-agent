# Redis会话与缓存

> 本文维护 ADK 会话热状态、TTL、双进程共享与 embedding 缓存。业务版本由 PostgreSQL 持久化，跨会话用户画像不在当前实现范围。

## 1. 目标与范围

- **范围内**:`internal/redisstore` 包 —— Redis 客户端拨号 + ADK `session.Service` 的 Redis 实现 + query 向量化缓存装饰器;`cmd/host` 与 `cmd/buildsvc` 注入 Redis 会话服务;buildsvc 的 `QueryEmbedder` 外包一层缓存;`.env` 配置 `REDIS_ADDR` / 会话 TTL。
- **范围外(诚实标注)**:**LLM 结果缓存**——`model.LLM.GenerateContent` 是流式 `iter.Seq2`,且本应用每轮请求都带增长的对话历史,多轮请求几乎不重复(命中率约 0),materialize+replay 复杂度换不来开发期成本收益;成本控制沿用已有的**模型分档 + Loop 熔断 + token 预算**(开发约定 也把缓存定位为开发期成本工具)。跨会话长期画像(PRD FR-701)、Redis 集群/持久化策略,均不做。

## 2. 会话持久化边界

配置 Redis 时，host 与 buildsvc 注入共享 Redis SessionService；未配置时才降级为 InMemory。host 保存对话与远程 contextID，buildsvc 按 contextID 保存 build_state；key 按 appName 隔离。

**用例 H 恢复链路**:
1. host 被 kill → 进程内状态本会全丢;但会话已在 Redis(key 含 sessionID)。
2. host 重启,dev UI 按同一 sessionID `Get` → 对话历史 + 上一条远程交付事件(其元数据携带远程 `contextID`)一并恢复。
3. 下一轮改单:remoteagent 复用恢复出的 `contextID` 发往 buildsvc;buildsvc 用同一 `contextID` `Get` 到仍在 Redis 的 `build_state` → 改单链在服务端续上。

> 关键假设:远程 `contextID` 随 host 会话事件持久(A2A 服务 实证「从上一条远程事件元数据复用 contextID」)。本 Redis 实现整条 event 以 JSON 落库,元数据不丢,该假设成立;DoD 步骤 3 专门验证。

## 3. ADK `session.Service` 接线(v2.1.0 实证)

- **实现接口**(`session/service.go`,注意名为 `session.Service` 非 `SessionService`,5 方法):
  `Create(ctx,*CreateRequest)`、`Get(ctx,*GetRequest)`、`List(ctx,*ListRequest)`、`Delete(ctx,*DeleteRequest)`、`AppendEvent(ctx, Session, *Event)`。
- **具体类型**:`Session`/`State`/`Events` 均为接口,各有独立实现(照 `session/database` 蓝本的 `localSession`/`state`/`events`),并加 `var _ session.Session = (*redisSession)(nil)` 等断言。
- **注入点**:
  - host —— `launcher.Config{SessionService: redisSvc, AgentLoader: …}`(字段实证存在;`full.NewLauncher` 的 console/web 子 launcher 会把它透传给各自 `runner.New`)。
  - buildsvc —— `adka2a.ExecutorConfig{RunnerConfig: runner.Config{SessionService: redisSvc, …}}`。
- **行为契约(不满足会挂)**:
  - `Get` miss **必须返回 error**(runner/executor 靠 error 判断"不存在再 Create";返回空 session+nil 会误判)。
  - `AppendEvent`:`event.Partial==true` 直接 `return nil`;对入参 session 做类型断言(runner 回传的正是本服务 `Create`/`Get` 返回的具体类型);持久化前 trim 掉 `temp:` 前缀的 StateDelta。
  - state 三层作用域:`app:`/`user:` 前缀的 delta 抽到独立键,其余为 session 级(本项目状态键无前缀,实际只用 session 级;但仍实现三层以做正确的 drop-in,并用官方 `sessiontestsuite` 校验)。
- **contextID→sessionID 映射**(executor `toInvocationMeta`):`sessionID = reqCtx.ContextID`,`userID = "A2A_USER_" + contextID`。buildsvc 侧 Redis key 里的 sessionID 即 A2A contextID。

## 4. Redis 数据模型

`internal/sessionutils` 是 internal 包不可 import,`extractStateDeltas` / `mergeStates` / `trimTempDeltaState` 三个小函数照 `session/database` 复制一份。

- **序列化**:`session.Event`(内嵌 `model.LLMResponse`、`EventActions.StateDelta`、`*genai.Content`)经实证可用 `encoding/json` 无损往返(database 包逐字段 marshal 佐证),故本实现直接整体 `json.Marshal(*Event)`,比按列拆分更简单。
- **key 设计**(前缀 `pcb:sess`):
  - `pcb:sess:{app}:{user}:{sid}` → JSON `{sessionState, events:[]*Event, createTime, updateTime}`(一个会话一个键,便于整体 TTL)。
  - `pcb:sess:app:{app}` → app 级 state map(JSON)。
  - `pcb:sess:user:{app}:{user}` → user 级 state map(JSON)。
- **List**:用 `SCAN MATCH pcb:sess:{app}:*`(可再按 user 过滤);当前规模下 SCAN 足够,且过期键自然不出现,无需维护额外索引集。
- **TTL**:会话键与 app/user state 键统一设 `REDIS_SESSION_TTL`(默认 24h);在 `Create` 与每次 `AppendEvent` 时刷新(滑动过期),保证活跃会话不因 TTL 掉线。
- **降级**:`REDIS_ADDR` 未设置时回退 `session.InMemoryService()` 并告警一行 —— 保证无 Redis 的 CI / `go test` 仍可跑(与现有 PG_TEST_DSN 门控同风格)。

## 5. embedding 缓存

- 位置:`internal/redisstore` 的缓存装饰器,结构化实现 `EmbedOne(ctx,text) ([]float32,error)`(即 `tools.QueryEmbedder` 面),包住真实 `embedding.Client`;buildsvc 装配 `QueryEmbedder` 前套一层。
- key:`pcb:emb:{identity_hash}:{sha256(text)}`（identity 为 provider、endpoint host、model、dimensions；使用 SHA256 前 8 字节的十六进制）;value:向量的紧凑编码(JSON `[]float32`)。命中直接返回,未命中回落底层 client 并写回、设 TTL(`REDIS_CACHE_TTL`,默认 7 天)。
- 日志:命中 `[cache] embedding 命中 model=… len(text)=…`、未命中 `[cache] embedding 未命中 …`(满足检查单「缓存命中有日志」)。
- 正确性:同一模型身份配置 + 同一 query 文本 → 同一向量,天然可缓存;查询短文本(软偏好原话/浓缩)在多轮/跨会话高频复现,命中率真实。
- 降级:无 Redis 时装饰器不启用,直接用底层 client(host/buildsvc 均可无 Redis 运行)。

## 6. 进程与配置

- `.env.example` 增:`REDIS_ADDR=localhost:16379`(compose 端口,见 CLAUDE.md)、`REDIS_SESSION_TTL=720h`（示例配置；代码回退值为 24h）、`REDIS_CACHE_TTL=168h`。
- go-redis(`github.com/redis/go-redis/v9`)已作为依赖,`redisstore.Dial(ctx,addr)` 做 `NewClient` + `Ping`。
- 启动顺序不变:`docker compose up -d` → `go run ./cmd/buildsvc` → `go run ./cmd/host web api webui`;两进程共享同一 Redis。

## 7. 验收步骤(DoD)

1. 双进程起,dev UI(web)开会话:v1「8000 元 2K 黑神话」→ v2「降 500」;确认 buildsvc 日志 build_state 落 Redis。
2. **kill host 进程**(buildsvc 保持),重启 `go run ./cmd/host web api webui`;dev UI 打开**同一会话**继续「换 A 卡」→ 产出 v3。
3. `cmd/builds list -session <contextID>` 核对 v1←v2←v3 版本树完整(证明恢复后改单在同一 contextID 续上,用例 C/D/E 不退化)。
4. 观察缓存日志:同一软偏好 query 二次出现时 `[cache] embedding 命中`。
5. `go test ./...` 全绿(含 `sessiontestsuite` 对 Redis 实现的一致性测试,按 `REDIS_TEST_ADDR` 门控;未设置则 skip)。用例 A/G 回归。
