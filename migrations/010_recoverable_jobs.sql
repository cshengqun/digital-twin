-- The source rows are also the durable queue, so a submitted job cannot be lost between writes.
ALTER TABLE knowledge_docs
    ADD COLUMN IF NOT EXISTS job_attempts INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS job_run_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS chunks_saved BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE journals
    ADD COLUMN IF NOT EXISTS status TEXT,
    ADD COLUMN IF NOT EXISTS error_msg TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS job_attempts INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS job_run_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS memories_saved BOOLEAN NOT NULL DEFAULT false;

-- Older versions used digest_at both to claim and to mark work done. Preserve marked rows.
UPDATE journals SET status = CASE WHEN digest_at IS NULL THEN 'pending' ELSE 'ready' END
WHERE status IS NULL;
ALTER TABLE journals ALTER COLUMN status SET DEFAULT 'pending', ALTER COLUMN status SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_knowledge_jobs ON knowledge_docs (agent_id, job_run_at, id)
    WHERE status IN ('pending', 'indexing');
CREATE INDEX IF NOT EXISTS idx_journal_jobs ON journals (agent_id, job_run_at, id)
    WHERE status IN ('pending', 'digesting');

CREATE INDEX IF NOT EXISTS idx_journal_staged_memories ON memories (agent_id, source_ref)
    WHERE status = 'indexing';
