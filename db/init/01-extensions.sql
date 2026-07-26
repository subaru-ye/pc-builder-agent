-- 由 postgres 镜像 entrypoint 在 POSTGRES_DB(pcbuilder)中执行,
-- 仅数据卷首次初始化时运行;幂等,重跑无副作用。
CREATE EXTENSION IF NOT EXISTS vector;
