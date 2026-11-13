-- 个人数字分身 Agent · 数据库 Schema V1（Phase 0 全量表结构）
-- 约定：
--   * 文本统一 UTF-8；时间统一 TIMESTAMPTZ
--   * messages / knowledge_chunks 按 created_at 月度 RANGE 分区，
--     分区表由应用启动时自动 ensure（本月 + 下月），避免插入报错。
--   * 单用户架构；agent_id 仅用于个人多角色区分，不是租户隔离。

-- ============ 账号与角色 ============
CREATE TABLE IF NOT EXISTS users (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,          -- argon2id
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agents (
    id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner_id          BIGINT NOT NULL REFERENCES users(id),
    name              TEXT NOT NULL,
    profile_json      JSONB NOT NULL DEFAULT '{}',   -- 人设 Profile（3.4 节）
    model_config_json JSONB NOT NULL DEFAULT '{}',
    status            TEXT NOT NULL DEFAULT 'active',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============ 会话与消息 ============
CREATE TABLE IF NOT EXISTS conversations (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    agent_id       BIGINT REFERENCES agents(id),
    title          TEXT NOT NULL DEFAULT '新对话',
    summary        TEXT NOT NULL DEFAULT '',          -- 滚动摘要（工作记忆持久层）
    last_active_at TIMESTAMPTZ NOT NULL DEFAULT now(),-- 会话边界判定依据
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_conversations_last_active ON conversations (last_active_at);

CREATE TABLE IF NOT EXISTS messages (
    id              BIGINT GENERATED ALWAYS AS IDENTITY,
    conversation_id BIGINT NOT NULL REFERENCES conversations(id),
    role            TEXT NOT NULL,                    -- user | assistant | system | tool
    content         TEXT NOT NULL,
    tokens          INT  NOT NULL DEFAULT 0,
    tool_calls_json JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)                      -- 分区键必须包含在主键内
) PARTITION BY RANGE (created_at);

CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages (conversation_id, created_at DESC);

-- ============ 记忆体系 ============
CREATE TABLE IF NOT EXISTS memories (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    agent_id        BIGINT REFERENCES agents(id),
    type            TEXT NOT NULL,                    -- fact | episodic | procedural
    content_json    JSONB NOT NULL,                   -- 结构化记忆内容
    embedding_id    TEXT,                             -- Qdrant point id（Phase 1 启用）
    importance      INT  NOT NULL DEFAULT 5,          -- 1-10
    decay_at        TIMESTAMPTZ,                      -- 遗忘调度时间
    source_ref      TEXT,                             -- 来源指针：conversation:{id} / log:{id} / doc:{id}
    status          TEXT NOT NULL DEFAULT 'active',   -- active | pending_confirm | archived | deleted
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_memories_type ON memories (type, importance DESC);
CREATE INDEX IF NOT EXISTS idx_memories_agent ON memories (agent_id, status);

CREATE TABLE IF NOT EXISTS memory_relations (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    from_memory_id BIGINT NOT NULL REFERENCES memories(id),
    to_memory_id   BIGINT NOT NULL REFERENCES memories(id),
    relation       TEXT NOT NULL,                     -- cause_of | follows_from | evidences | supersedes | relates_to
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (from_memory_id, to_memory_id, relation)
);
CREATE INDEX IF NOT EXISTS idx_memory_relations_from ON memory_relations (from_memory_id);
CREATE INDEX IF NOT EXISTS idx_memory_relations_to   ON memory_relations (to_memory_id);

-- ============ 知识库（TXT / HTML） ============
CREATE TABLE IF NOT EXISTS knowledge_docs (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    agent_id    BIGINT REFERENCES agents(id),
    source_type TEXT NOT NULL,                        -- txt | html_url | html_file
    source_url  TEXT NOT NULL DEFAULT '',             -- HTML 的 URL；TXT 可为空
    title       TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'pending',      -- pending | indexing | ready | failed | deleted
    chunk_count INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS knowledge_chunks (
    id              BIGINT GENERATED ALWAYS AS IDENTITY,
    doc_id          BIGINT NOT NULL REFERENCES knowledge_docs(id),
    content         TEXT NOT NULL,
    embedding_id    TEXT,
    metadata_json   JSONB NOT NULL DEFAULT '{}',      -- 标题树 / 章节 / 段落
    parent_chunk_id BIGINT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_doc ON knowledge_chunks (doc_id);

-- ============ 任务与用量 ============
CREATE TABLE IF NOT EXISTS scheduled_tasks (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    agent_id     BIGINT REFERENCES agents(id),
    cron_expr    TEXT NOT NULL,
    action_json  JSONB NOT NULL,
    status       TEXT NOT NULL DEFAULT 'active',
    last_run_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS usage_records (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    agent_id      BIGINT REFERENCES agents(id),
    model         TEXT NOT NULL,
    input_tokens  INT NOT NULL DEFAULT 0,
    output_tokens INT NOT NULL DEFAULT 0,
    cost          NUMERIC(12,6) NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_usage_records_created ON usage_records (created_at);

-- ============ 预置默认角色（首个分身） ============
INSERT INTO agents (id, owner_id, name, profile_json)
OVERRIDING SYSTEM VALUE
SELECT 1, id, '默认分身', '{"role":"personal-assistant","style":"concise","principles":[]}'::jsonb
FROM users WHERE NOT EXISTS (SELECT 1 FROM agents WHERE id = 1);
