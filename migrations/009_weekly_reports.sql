-- 009: 个人周报
-- 每周日晚自动跑，基于本周记忆生成结构化复盘，存 PG。
CREATE TABLE IF NOT EXISTS weekly_reports (
    id           BIGSERIAL PRIMARY KEY,
    agent_id     BIGINT NOT NULL REFERENCES agents(id),
    week_start   DATE NOT NULL,              -- 本周一日期
    week_end     DATE NOT NULL,              -- 本周日日期
    content_json JSONB NOT NULL,             -- LLM 生成的结构化周报
    memory_count INT NOT NULL DEFAULT 0,     -- 本周纳入分析的记忆条数
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(agent_id, week_start)
);
