-- Phase 1 · 知识库增强：
--  1) knowledge_docs 增加原文列（单用户文本量小，原文直接存 PG，不需要 OSS）
--  2) 增加中文关键词检索索引（pg_trgm 支持 ILIKE 子串匹配，对中文无需分词插件）
--  3) 增加错误信息列，便于入库失败排查

CREATE EXTENSION IF NOT EXISTS pg_trgm;

ALTER TABLE knowledge_docs
    ADD COLUMN IF NOT EXISTS content   TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS error_msg TEXT NOT NULL DEFAULT '';

-- 分区表父表上建索引会自动级联到现有及未来的月度分区
CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_content_trgm
    ON knowledge_chunks USING GIN (content gin_trgm_ops);
