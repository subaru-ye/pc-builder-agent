-- +goose Up
-- P3 语义选件(P3-语义选件设计.md §4):parts 加 embedding 两列。
-- 维度 1024 = 百炼端点 embedding 模型实测输出;换模型即换维度,须新迁移重算全量。
-- embedding_text 落库保可解释:检索命中时能指出候选因哪些词命中(DoD 用例 F 依赖)。
-- 不建向量索引:160 SKU 顺序扫描足够,ivfflat/hnsw 到 SKU 上千再议。
ALTER TABLE parts ADD COLUMN embedding vector(1024);
ALTER TABLE parts ADD COLUMN embedding_text TEXT;

-- +goose Down
ALTER TABLE parts DROP COLUMN embedding_text;
ALTER TABLE parts DROP COLUMN embedding;
