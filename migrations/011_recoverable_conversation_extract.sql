ALTER TABLE conversations
    ADD COLUMN IF NOT EXISTS status TEXT,
    ADD COLUMN IF NOT EXISTS error_msg TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS job_attempts INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS job_run_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS memories_saved BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS extract_from_id BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS extract_until_id BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_conversation_extract_jobs ON conversations (agent_id, job_run_at, id)
    WHERE status IN ('pending', 'extracting');
