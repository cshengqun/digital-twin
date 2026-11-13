package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// InsertMemory 写入一条长期记忆。
// meta 为可选结构化字段（event_time/mood/amount/tags），与 text 一起存进 content_json。
func (db *DB) InsertMemory(ctx context.Context, agentID int64, mtype, text string, importance int, sourceRef string, meta map[string]any) (int64, error) {
	return insertMemory(ctx, db.PG.QueryRow, agentID, mtype, text, importance, sourceRef, meta, "active")
}

func insertMemory(ctx context.Context, queryRow func(context.Context, string, ...any) pgx.Row, agentID int64, mtype, text string, importance int, sourceRef string, meta map[string]any, status string) (int64, error) {
	// content_json 基对象：{"text": "..."}
	obj := map[string]any{"text": text}
	for k, v := range meta {
		if v == nil {
			continue
		}
		// 零值/空数组跳过，避免污染 JSON
		switch val := v.(type) {
		case int:
			if val == 0 {
				continue
			}
		case float64:
			if val == 0 {
				continue
			}
		case string:
			if val == "" {
				continue
			}
		case []string:
			if len(val) == 0 {
				continue
			}
		}
		obj[k] = v
	}
	contentJSON, err := json.Marshal(obj)
	if err != nil {
		return 0, fmt.Errorf("marshal content_json: %w", err)
	}
	var id int64
	err = queryRow(ctx,
		`INSERT INTO memories (agent_id, type, content_json, importance, source_ref, status)
		 VALUES ($1, $2, $3::jsonb, $4, $5, $6)
		 RETURNING id`,
		agentID, mtype, string(contentJSON), importance, sourceRef, status).Scan(&id)
	return id, err
}

// MemoryKeywordRow 关键词命中的记忆行。
type MemoryKeywordRow struct {
	ID   int64
	Type string
	Text string
}

// SearchMemoriesByKeyword 对记忆文本做 pg_trgm 子串匹配，按命中关键词数+重要性排序。
// 与知识 chunk 关键词路对称，补记忆的精确词召回（人名/公司名/术语等向量盲区）。
func (db *DB) SearchMemoriesByKeyword(ctx context.Context, agentID int64, keywords []string, topK int) ([]MemoryKeywordRow, error) {
	if len(keywords) == 0 {
		return nil, nil
	}
	// WHERE 里的 OR 条件
	conds := ""
	for i := range keywords {
		if i > 0 {
			conds += " OR "
		}
		conds += fmt.Sprintf("m.content_json->>'text' ILIKE $%d", i+2)
	}
	// ORDER BY 里的命中计数
	hit := ""
	for i := range keywords {
		if i > 0 {
			hit += "+"
		}
		hit += fmt.Sprintf("CASE WHEN m.content_json->>'text' ILIKE $%d THEN 1 ELSE 0 END", i+2)
	}
	args := make([]any, 0, len(keywords)+2)
	args = append(args, agentID)
	for _, kw := range keywords {
		args = append(args, "%"+kw+"%")
	}
	args = append(args, topK)
	sql := fmt.Sprintf(`
		SELECT m.id, m.type, m.content_json->>'text' AS text
		FROM memories m
		WHERE m.agent_id = $1 AND m.status = 'active'
		  AND (%s)
		ORDER BY %s DESC, (m.corrected_at IS NOT NULL) DESC, m.importance DESC
		LIMIT $%d`, conds, hit, len(keywords)+2)
	rows, err := db.PG.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("memory keyword search: %w", err)
	}
	defer rows.Close()
	var out []MemoryKeywordRow
	for rows.Next() {
		var r MemoryKeywordRow
		if err := rows.Scan(&r.ID, &r.Type, &r.Text); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (db *DB) ActiveMemoriesByID(ctx context.Context, agentID int64, ids []int64) (map[int64]MemoryRecord, error) {
	rows, err := db.PG.Query(ctx, `SELECT `+memoryFields+` FROM memories
		WHERE agent_id = $1 AND id = ANY($2) AND status = 'active'`, agentID, ids)
	if err != nil {
		return nil, err
	}
	found, err := pgx.CollectRows(rows, pgx.RowToStructByPos[MemoryRecord])
	if err != nil {
		return nil, err
	}
	active := make(map[int64]MemoryRecord, len(found))
	for _, memory := range found {
		active[memory.ID] = memory
	}
	return active, nil
}

func (db *DB) SaveExtractedMemories(ctx context.Context, kind JobKind, agentID, id int64, source string, memories []MemoryDraft) error {
	tx, err := db.PG.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	table, running, _ := kind.queue()
	query := fmt.Sprintf(`UPDATE %s SET memories_saved = true
		WHERE id = $1 AND agent_id = $2 AND status = $3 AND NOT memories_saved`, table)
	tag, err := tx.Exec(ctx, query, id, agentID, running)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("记忆抽取任务状态已变化")
	}
	for _, item := range memories {
		if _, err := insertMemory(ctx, tx.QueryRow, agentID, item.Type, item.Text, item.Importance, source, item.Meta, "indexing"); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (db *DB) ListStagedMemories(ctx context.Context, agentID int64, source string) ([]MemoryDraft, error) {
	rows, err := db.PG.Query(ctx, `SELECT id, type, content_json->>'text', importance, content_json FROM memories
		WHERE agent_id = $1 AND source_ref = $2 AND status = 'indexing' ORDER BY id`, agentID, source)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[MemoryDraft])
}

func (db *DB) CompleteMemoryJob(ctx context.Context, kind JobKind, agentID, id int64, source string) error {
	tx, err := db.PG.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	table, running, _ := kind.queue()
	completionColumn := "digest_at"
	if kind == ConversationJob {
		completionColumn = "mem_extracted_at"
	}
	query := fmt.Sprintf(`UPDATE %s SET status = 'ready', %s = now(), error_msg = ''
		WHERE id = $1 AND agent_id = $2 AND status = $3 AND memories_saved`, table, completionColumn)
	tag, err := tx.Exec(ctx, query, id, agentID, running)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("记忆抽取任务状态已变化")
	}
	if _, err := tx.Exec(ctx, `UPDATE memories SET status = 'active'
		WHERE agent_id = $1 AND source_ref = $2 AND status = 'indexing'`, agentID, source); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
