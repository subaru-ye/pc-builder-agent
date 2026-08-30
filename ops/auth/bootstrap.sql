-- GoTrue 的官方迁移会向兼容角色 postgres 授予 auth 表只读权限。
-- 本项目的 PostgreSQL 由 pcbuilder 初始化，因此显式补一个无登录权限的兼容角色。
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'postgres') THEN
        CREATE ROLE postgres NOLOGIN;
    END IF;
END
$$;

CREATE SCHEMA IF NOT EXISTS auth AUTHORIZATION pcbuilder;
