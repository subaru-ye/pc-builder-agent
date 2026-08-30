# ADR-008：Supabase Auth 账号系统

> 状态：已采用（2026-08-30）。适用于本地学习环境；尚不满足公网开放注册条件。

## 决策

账号服务采用独立 `supabase/gotrue:v2.196.0`，只复用 PostgreSQL 的 `auth` schema，不安装完整 Supabase 平台。浏览器与 Next.js 不直接访问 GoTrue，也不持有 Access Token 或 Refresh Token；Go 产品 API 作为 BFF，通过随机 `pcb_auth_session` HttpOnly Cookie 标识登录会话，Token 以 AES-256-GCM 加密后存入 Redis。

首版仅支持邮箱密码注册、登录、显示名称、修改密码和退出。邮箱自动确认，只用于本地学习；不实现找回密码、邮箱变更、OAuth、MFA、Passkey、账号删除或角色权限。

## 身份与数据归属

- 匿名模式继续可完整使用，身份为 `pcb_anonymous_id`。
- `product_users` 只保存 Auth subject、邮箱投影和显示名称，不保存密码。
- `product_user_owners` 把一个账号关联到多个历史匿名 owner，并指定一个主 owner。
- 登录或注册自动认领当前 owner，但不改写 `web_sessions.owner_id`。因此 Agent contextID、版本树、分享 token 和幂等语义保持不变。
- 已认领 owner 不再允许匿名 Cookie 访问。退出时同时删除认证 Cookie并旋转匿名 Cookie。
- 登录身份读取会话、run、build、diff、导出和分享管理时，可访问其全部关联 owner；其他账号仍统一得到 404。

## Token 与错误策略

- `AUTH_SESSION_SECRET` 必须为独立的 32 字节 base64url 密钥；不得复用模型或分享密钥。
- Token 保险箱 TTL 为 30 天。Access Token 临近过期时，以 Redis 锁串行刷新，避免 Refresh Token 并发复用。
- Auth Redis 故障时登录请求返回 `auth_unavailable`，不静默降级为访客；无认证 Cookie 的匿名模式不受影响。
- Auth 写请求使用 UUID 幂等键。请求 HMAC 与成功结果在 Redis 中加密保存 15 分钟；同 key 不同正文返回 409。
- 上游响应正文、邮箱、密码和 Token 不进入应用日志或 Problem detail。

## 取舍

本方案比在 Go 中自建密码体系更安全且边界清晰，也与另一个 Keycloak 项目形成技术区分。代价是本地多一个 GoTrue 进程，并且登录会话强依赖 Redis。公网开放前必须增加真实 SMTP 与邮箱验证、CAPTCHA、反向代理限流、HTTPS、密钥轮换和账号恢复流程。
