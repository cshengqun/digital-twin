-- 个人数字分身 Agent · Schema V5：日志喂养 + 人格建模（Profile / State）
-- journals：每日日志 / 喂养内容（对话纠偏、行为记录、上传内容）
CREATE TABLE IF NOT EXISTS journals (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    agent_id    BIGINT REFERENCES agents(id),
    content     TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT 'journal',   -- journal | file | correction
    digest_at   TIMESTAMPTZ,                        -- 消化完成时间（防重标记）
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- agents 增加动态 State 列（人格建模三层中的"动态状态"层）
ALTER TABLE agents ADD COLUMN IF NOT EXISTS state_json JSONB NOT NULL DEFAULT '{}';
