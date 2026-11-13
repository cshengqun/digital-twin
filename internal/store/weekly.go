package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// WeeklyReportRow 一行周报。
type WeeklyReportRow struct {
	ID          int64
	WeekStart   time.Time
	WeekEnd     time.Time
	ContentJSON json.RawMessage
	MemoryCount int
	CreatedAt   time.Time
}

// InsertWeeklyReport 写入一期周报（按 agent_id + week_start 唯一，冲突则覆盖）。
func (db *DB) InsertWeeklyReport(ctx context.Context, agentID int64, weekStart, weekEnd time.Time, content json.RawMessage, memCount int) (int64, error) {
	var id int64
	err := db.PG.QueryRow(ctx,
		`INSERT INTO weekly_reports (agent_id, week_start, week_end, content_json, memory_count)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (agent_id, week_start) DO UPDATE
		   SET content_json = EXCLUDED.content_json,
		       memory_count = EXCLUDED.memory_count,
		       created_at = now()
		 RETURNING id`,
		agentID, weekStart, weekEnd, string(content), memCount).Scan(&id)
	return id, err
}

// GetLatestWeeklyReport 取最近一期周报。
func (db *DB) GetLatestWeeklyReport(ctx context.Context, agentID int64) (*WeeklyReportRow, error) {
	var r WeeklyReportRow
	err := db.PG.QueryRow(ctx,
		`SELECT id, week_start, week_end, content_json, memory_count, created_at
		 FROM weekly_reports WHERE agent_id = $1
		 ORDER BY week_start DESC LIMIT 1`, agentID).Scan(
		&r.ID, &r.WeekStart, &r.WeekEnd, &r.ContentJSON, &r.MemoryCount, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ListMemoriesSince 取 since 之后的所有活跃记忆（含结构化字段），按时间升序。
// 用于周报：把本周所有记忆喂给 LLM 做分析。
func (db *DB) ListMemoriesSince(ctx context.Context, agentID int64, since time.Time) ([]MemText, error) {
	rows, err := db.PG.Query(ctx,
		`SELECT id, type, content_json->>'text' AS text
		 FROM memories
		 WHERE agent_id = $1 AND status = 'active' AND created_at >= $2
		 ORDER BY created_at ASC`, agentID, since)
	if err != nil {
		return nil, fmt.Errorf("list memories since: %w", err)
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
