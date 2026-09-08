# 产品 API SSE 事件协议

> 状态:已实现，事件处理见 `internal/producthttp/sse.go` 与 `internal/runevents`；本文维护协议约束。HTTP 路径和 DTO 以 openapi.yaml 为准;本文补充 OpenAPI 不便完整表达的流式语义。

## 1. 连接模型

创建消息或确认需求后,API 返回 202 Run DTO。前端随后连接:

```text
GET /api/v1/runs/{run_id}/events
Accept: text/event-stream
Last-Event-ID: 1723000000000-0   # 仅重连时
```

run 必须属于当前匿名用户。SSE 响应使用:

```text
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
X-Accel-Buffering: no
```

浏览器使用 fetch + ReadableStream,不用原生 EventSource,以便统一携带 cookie、AbortSignal 和错误处理。用户离开页面只关闭订阅,不取消后台 Agent。

## 2. 统一事件外壳

每个业务事件的 data 都是一个完整 JSON 对象:

```json
{
  "schema_version": 1,
  "run_id": "7aa7c6d2-2c04-4ee4-969a-faf38919e20f",
  "timestamp": "2026-08-09T12:00:00Z",
  "payload": {}
}
```

- schema_version 当前固定为 1。
- run_id 必须与 URL 相同。
- timestamp 使用服务端 UTC RFC 3339。
- payload 随 event 类型变化。
- SSE id 使用 Redis Stream ID,不得由前端解释其时间含义。

线格式示例:

```text
id: 1723000000000-0
event: run.started
data: {"schema_version":1,"run_id":"...","timestamp":"...","payload":{"kind":"build"}}

```

## 3. 固定事件类型

### 3.1 run.started

一个 run 只有一条,必须是首个业务事件。

```json
{"kind":"screening|build|change"}
```

### 3.2 run.progress

```json
{
  "stage": "screening|remote_processing|finalizing",
  "label": "正在整理需求",
  "sequence": 1
}
```

- stage 只允许三种稳定值。
- label 是展示文案,前端不能依赖它判断状态。
- sequence 在单 run 内递增,用于忽略乱序 UI 更新。
- remote_processing 合并生成与规则校验;除非未来有可验证的跨 A2A 事件,不得拆成虚构阶段。

### 3.3 requirement.ready

初筛得到合法 RequirementSpec 时发送。payload 是完整 RequirementSpec v1。收到后前端:

1. 将会话缓存 phase 更新为 requirement_ready。
2. 展示需求确认卡。
3. 结束当前输入 loading。
4. 等待 run.completed 后再刷新会话真值。

### 3.4 assistant.delta

```json
{"message_id":"临时或最终 UUID","text":"增量文本"}
```

- text 只含新增片段,前端按到达顺序追加。
- 仅转发面向用户的 assistant 内容。
- 工具调用、模型思维、A2A task 和 schema JSON 不得作为 delta 展示。
- 上游不提供可靠 partial 时可以完全不发送本事件。

### 3.5 assistant.completed

```json
{
  "message": {
    "schema_version": 1,
    "id": "...",
    "role": "assistant",
    "content": "需求已经整理好,请确认。",
    "run_id": "...",
    "created_at": "..."
  }
}
```

completed 是数据库已保存的产品消息。前端用它替换同 message_id 的临时 delta,然后失效 session query。

### 3.6 build.saved

```json
{
  "version": 3,
  "build_url": "/api/v1/sessions/.../builds/3"
}
```

本事件只在版本已成功提交 PostgreSQL 且读模型可查询后发送。前端收到后失效 builds、build detail、diff 和 session query,不能直接把 payload 当完整 BuildView。

### 3.7 run.failed

payload 是 application/problem+json 的 JSON 对象投影,至少含 type、title、status、code、request_id。它是终止事件之一,之后只能再出现 run.completed。

不得把技术堆栈、SQL、模型请求体、API key 或上游原始响应写入 detail。

### 3.8 run.completed

```json
{"status":"succeeded|failed|interrupted"}
```

每个 run 恰好一条且为最后一个业务事件。客户端收到后关闭流并重新读取 Run/Session。没有 build.saved 不代表失败:初筛追问和 requirement.ready 都可以成功完成而不产生版本。

## 4. 心跳、重放与断线

### 4.1 心跳

服务端在 15 秒没有业务事件时发送注释帧:

```text
: heartbeat 2026-08-09T12:00:15Z

```

注释不写 Redis Stream、不带 id、不改变业务状态。前端连续 30 秒收不到任何字节时显示「连接不稳定,正在重连」,但不把 run 标为失败。

### 4.2 重放

- 首次连接不带 Last-Event-ID,从第一条事件开始。
- 重连带最后成功处理的 id,服务端从下一条开始。
- 前端按 id 去重;相同 id 的内容必须完全一致。
- Redis Stream 已过期但 Run 仍存在时返回 410 events_expired;前端转为轮询 GET Run 和 GET Session。
- run.completed 后仍可在 TTL 内完整重放,便于刷新恢复。

### 4.3 前端退避

重连间隔固定为 1s、2s、5s、10s,之后每 15s 一次;页面重新获得网络或可见性时立即尝试。401/404 不重试;410 转轮询;5xx 继续退避。

## 5. 事件顺序约束

合法的典型序列:

```text
run.started
run.progress(screening)
assistant.delta *
assistant.completed?
requirement.ready?
run.completed(succeeded)
```

```text
run.started
run.progress(remote_processing)
assistant.delta *
run.progress(finalizing)
assistant.completed
build.saved
run.completed(succeeded)
```

```text
run.started
run.progress(...)
run.failed
run.completed(failed)
```

禁止:

- run.started 之前出现业务事件。
- build.saved 先于数据库提交。
- run.failed 后继续发送 delta 或 build.saved。
- run.completed 后继续发送任何业务事件。
- 同一 run 发送多个 requirement.ready 或 build.saved。

## 6. 降级模式

Redis 不可用时:

- 事件只在当前 API 进程内广播。
- Last-Event-ID 不保证重放。
- Session DTO 和 Readiness 必须标记 degraded。
- 前端展示会话恢复受限提示。
- P7/P10 正式验收不得在此模式完成。

## 7. 契约测试

- 每种事件 payload 都做 schema 校验。
- 测试合法序列、失败序列和无 delta 序列。
- 测试 Last-Event-ID 恰好从下一条恢复。
- 测试重复连接不会重复持久化消息或 build。
- 测试心跳不进入 Redis Stream。
- 测试 30 秒静默提示与 410 转轮询。
- 测试所有输出均不含 thought、tool args、A2A task 或内部 state key。
