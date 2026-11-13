-- 个人数字分身 Agent · Schema V6：会话滚动摘要（超窗压缩）
-- summary_until_id：记录已并入摘要的最大消息 id，用于增量压缩（只压缩新增溢出的部分）
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS summary_until_id BIGINT;
