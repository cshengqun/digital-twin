-- 004: 会话记忆抽取标记（conversation 结束后整段抽取，防重复）
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS mem_extracted_at TIMESTAMPTZ;
