# Supabase Auth 本地账号系统

## 初始化

账号功能默认关闭。首次启用时在仓库根目录运行：

```bash
go run ./cmd/authsetup
docker compose -f docker-compose.yml -f docker-compose.auth.yml up -d postgres redis auth
go run ./cmd/migrate up
```

`authsetup` 原子更新未跟踪的 `.env`，只补缺失值，不覆盖已有配置，也不输出密钥。关键配置为：

```dotenv
AUTH_ENABLED=true
SUPABASE_AUTH_URL=http://127.0.0.1:9999
SUPABASE_JWT_SECRET=<独立随机值>
AUTH_SESSION_SECRET=<独立随机值>
AUTH_SESSION_TTL=720h
```

Auth 只绑定 `127.0.0.1:9999`。`auth-db-init` 是一次性 bootstrap，只创建 `auth` schema 和 GoTrue 迁移兼容所需的无登录 `postgres` 角色；所有 `auth.*` 表由 GoTrue 自行管理，项目代码不得查询或迁移它们。

## 启动与检查

```bash
docker compose -f docker-compose.yml -f docker-compose.auth.yml ps postgres redis auth
curl http://127.0.0.1:9999/health
go run ./cmd/api
curl http://localhost:8082/readyz
```

账号开启时 `/readyz` 的 `dependencies.auth` 必须为 `ok`；`/healthz` 不依赖 Auth。产品页面仍从 `http://localhost:3000` 访问，浏览器不会连接 9999。

## 本地能力与限制

- `/login`：邮箱密码登录。
- `/register`：注册并自动认领当前浏览器的匿名会话。
- `/account`：修改显示名称、修改密码、退出。
- 当前 `GOTRUE_MAILER_AUTOCONFIRM=true`，不发送验证邮件，也没有忘记密码流程。
- 退出会旋转匿名身份；已认领数据不会重新暴露给访客。

不要把当前配置用于公网开放注册。公网部署前必须完成 SMTP/邮箱验证、CAPTCHA、入口限流、HTTPS、密钥轮换、备份恢复和滥用监控。

## 停用与排障

设置 `AUTH_ENABLED=false` 并重启 API 即可回到匿名模式；已有账号映射和 Auth 数据不会删除。停止 Auth：

```bash
docker compose -f docker-compose.yml -f docker-compose.auth.yml stop auth
```

- Auth 启动失败且提示 schema：确认 `auth-db-init` 已成功退出。
- 登录返回 `auth_unavailable`：检查 Redis 与 Auth；系统不会把登录用户降级成访客。
- 登录返回 `auth_session_expired`：重新登录；API 会清理失效认证 Cookie。
- 不执行 `docker compose down -v`，避免删除现有产品、Auth 和 Redis 数据。
