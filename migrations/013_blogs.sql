-- Public blogs reuse the journal memory-extraction queue and the knowledge indexer.
ALTER TABLE journals ADD COLUMN IF NOT EXISTS title TEXT NOT NULL DEFAULT '';
ALTER TABLE journals ADD COLUMN IF NOT EXISTS knowledge_doc_id BIGINT REFERENCES knowledge_docs(id);
CREATE INDEX IF NOT EXISTS idx_public_blogs ON journals (agent_id, id DESC) WHERE source = 'blog';
