-- 会话归属用户（多人共享分身的隔离边界）
-- 幂等：可重复执行
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS user_id BIGINT REFERENCES users(id);

-- 回填存量会话：归属给第一个用户（admin）
UPDATE conversations
   SET user_id = (SELECT id FROM users ORDER BY id LIMIT 1)
 WHERE user_id IS NULL;

ALTER TABLE conversations ALTER COLUMN user_id SET NOT NULL;
CREATE INDEX IF NOT EXISTS idx_conversations_user ON conversations (user_id, last_active_at DESC);
