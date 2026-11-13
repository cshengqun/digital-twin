package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ---------- 日志（每日喂养） ----------

// InsertJournal 写入一篇日志，返回 id。
func (db *DB) InsertJournal(ctx context.Context, agentID int64, content, source string) (int64, error) {
	var id int64
	err := db.PG.QueryRow(ctx,
		`INSERT INTO journals (agent_id, content, source) VALUES ($1, $2, $3) RETURNING id`,
		agentID, content, source).Scan(&id)
	return id, err
}

// ClaimJournalDigest 抢占日志消化标记（幂等防重）；成功返回 true 表示本次负责消化。
func (db *DB) ClaimJournalDigest(ctx context.Context, id int64) (bool, error) {
	var claimed bool
	err := db.PG.QueryRow(ctx,
		`UPDATE journals SET digest_at = now() WHERE id = $1 AND digest_at IS NULL RETURNING true`,
		id).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // 已被消化（或不存在）
	}
	if err != nil {
		return false, err
	}
	return claimed, nil
}

// UnclaimJournalDigest 消化失败时撤销标记，允许后续重试。
func (db *DB) UnclaimJournalDigest(ctx context.Context, id int64) error {
	_, err := db.PG.Exec(ctx, `UPDATE journals SET digest_at = NULL WHERE id = $1`, id)
	return err
}

// GetJournalContent 读取一篇日志正文。
func (db *DB) GetJournalContent(ctx context.Context, id int64) (string, error) {
	var c string
	err := db.PG.QueryRow(ctx, `SELECT content FROM journals WHERE id = $1`, id).Scan(&c)
	return c, err
}

// ---------- 人格建模（Profile / State） ----------

// MemText 画像重建用的记忆条目。
type MemText struct {
	ID   int64
	Type string
	Text string
}

// ListMemoriesForProfile 取最重要的记忆文本（fact + procedural），作为 Profile 提炼输入。
func (db *DB) ListMemoriesForProfile(ctx context.Context, agentID int64, limit int) ([]MemText, error) {
	rows, err := db.PG.Query(ctx,
		`SELECT id, type, content_json->>'text' FROM memories
		 WHERE agent_id = $1 AND status = 'active' AND type IN ('fact','procedural')
		 ORDER BY importance DESC, created_at DESC LIMIT $2`,
		agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemText
	for rows.Next() {
		var m MemText
		if err := rows.Scan(&m.ID, &m.Type, &m.Text); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListRecentMemories 取最近记忆（全类型、按时间倒序），作为 State 评估输入。
func (db *DB) ListRecentMemories(ctx context.Context, agentID int64, limit int) ([]MemText, error) {
	rows, err := db.PG.Query(ctx,
		`SELECT id, type, content_json->>'text' FROM memories
		 WHERE agent_id = $1 AND status = 'active'
		 ORDER BY created_at DESC LIMIT $2`,
		agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemText
	for rows.Next() {
		var m MemText
		if err := rows.Scan(&m.ID, &m.Type, &m.Text); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetAgentProfile 返回人设 Profile 与动态 State（JSON 文本）。
func (db *DB) GetAgentProfile(ctx context.Context, agentID int64) (profileJSON, stateJSON string, err error) {
	err = db.PG.QueryRow(ctx,
		`SELECT COALESCE(profile_json::text, '{}'), COALESCE(state_json::text, '{}') FROM agents WHERE id = $1`,
		agentID).Scan(&profileJSON, &stateJSON)
	return
}

// UpdateAgentProfile 覆写人设 Profile。
func (db *DB) UpdateAgentProfile(ctx context.Context, agentID int64, profileJSON string) error {
	_, err := db.PG.Exec(ctx,
		`UPDATE agents SET profile_json = $1::jsonb WHERE id = $2`,
		profileJSON, agentID)
	return err
}

// UpdateAgentState 覆写动态 State。
func (db *DB) UpdateAgentState(ctx context.Context, agentID int64, stateJSON string) error {
	_, err := db.PG.Exec(ctx,
		`UPDATE agents SET state_json = $1::jsonb WHERE id = $2`,
		stateJSON, agentID)
	return err
}

// ScanJSON 把 JSON 字符串转成 map 的便捷封装（供 profile 注入渲染）。
func ScanJSON(s string) map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal([]byte(s), &m)
	return m
}

// S 取 map 里字符串字段（无则空串）；数组字段用"；"连接。
func S(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	if v, ok := m[key].([]any); ok {
		parts := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "；")
	}
	return ""
}
