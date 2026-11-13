package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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

type JournalJobStatus struct {
	ID        int64     `json:"id"`
	Status    string    `json:"status"`
	ErrorMsg  string    `json:"error_msg"`
	CreatedAt time.Time `json:"created_at"`
}

func (db *DB) ListJournalJobs(ctx context.Context, agentID int64) ([]JournalJobStatus, error) {
	rows, err := db.PG.Query(ctx, `SELECT id, status, error_msg, created_at FROM journals
		WHERE agent_id = $1 ORDER BY id DESC LIMIT 100`, agentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[JournalJobStatus])
}

func (db *DB) GetJournalWork(ctx context.Context, agentID, id int64) (content string, saved bool, err error) {
	err = db.PG.QueryRow(ctx, `SELECT content, memories_saved FROM journals
		WHERE id = $1 AND agent_id = $2 AND status = 'digesting'`, id, agentID).Scan(&content, &saved)
	return
}

type MemoryDraft struct {
	ID         int64
	Type       string
	Text       string
	Importance int
	Meta       map[string]any
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
		 ORDER BY (corrected_at IS NOT NULL) DESC, importance DESC, created_at DESC LIMIT $2`,
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

// GetAgentProfile returns an empty profile while a newer correction awaits rebuilding.
func (db *DB) GetAgentProfile(ctx context.Context, agentID int64) (profile, state string, err error) {
	profile, state, _, err = db.ProfileForRebuild(ctx, agentID)
	return
}

func (db *DB) ProfileForRebuild(ctx context.Context, agentID int64) (profile, state string, rev int64, err error) {
	err = db.PG.QueryRow(ctx, `SELECT CASE WHEN profile_revision = memory_revision THEN profile_json::text ELSE '{}' END,
		CASE WHEN profile_revision = memory_revision THEN state_json::text ELSE '{}' END, memory_revision
		FROM agents WHERE id = $1`, agentID).Scan(&profile, &state, &rev)
	return
}

func (db *DB) SaveAgentProfile(ctx context.Context, agentID, rev int64, profile, state string) error {
	tag, err := db.PG.Exec(ctx, `UPDATE agents SET profile_json = $3::jsonb, state_json = $4::jsonb, profile_revision = $2
		WHERE id = $1 AND memory_revision = $2`, agentID, rev, profile, state)
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("记忆已更新，请重新生成画像")
	}
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
