-- 007: 记忆文本关键词召回索引
-- memories.content_json 是 JSONB，文本在 content_json->>'text'。
-- 用表达式 GIN + pg_trgm 支持 ILIKE 子串匹配，与 knowledge_chunks.content 同策略。
CREATE INDEX IF NOT EXISTS idx_memories_text_trgm
    ON memories USING GIN ((content_json->>'text') gin_trgm_ops);
