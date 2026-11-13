-- 008: 记忆结构化字段索引
-- content_json 现在含 event_time/mood/amount/tags 等结构化字段。
-- tags 是字符串数组，加 GIN 索引支持按标签聚合查询（"面试"相关记忆有多少条）。
CREATE INDEX IF NOT EXISTS idx_memories_tags_gin
    ON memories USING GIN ((content_json->'tags'));
