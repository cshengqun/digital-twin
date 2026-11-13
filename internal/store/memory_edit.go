package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type MemoryRecord struct {
	ID           int64      `json:"id"`
	Type         string     `json:"type"`
	Text         string     `json:"text"`
	SourceRef    string     `json:"source_ref,omitempty"`
	Status       string     `json:"status"`
	Revision     int64      `json:"revision"`
	CorrectedAt  *time.Time `json:"corrected_at,omitempty"`
	VectorStatus string     `json:"vector_status,omitempty"`
}

const memoryFields = `id, type, content_json->>'text', COALESCE(source_ref, ''), status,
	revision, corrected_at, COALESCE(vector_status, '')`

func (db *DB) ListMemories(ctx context.Context, agentID int64, query string, before int64) ([]MemoryRecord, error) {
	rows, err := db.PG.Query(ctx, `SELECT `+memoryFields+` FROM memories
		WHERE agent_id = $1 AND status IN ('active', 'archived', 'pending_confirm')
		  AND ($2 = 0 OR id < $2)
		  AND ($3 = '' OR strpos(lower(content_json->>'text'), lower($3)) > 0)
		ORDER BY id DESC LIMIT 50`, agentID, before, query)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[MemoryRecord])
}

func (db *DB) GetMemory(ctx context.Context, agentID, id int64) (MemoryRecord, error) {
	rows, err := db.PG.Query(ctx, `SELECT `+memoryFields+` FROM memories
		WHERE agent_id = $1 AND id = $2 AND status IN ('active', 'archived', 'pending_confirm')`, agentID, id)
	if err != nil {
		return MemoryRecord{}, err
	}
	return pgx.CollectOneRow(rows, pgx.RowToStructByPos[MemoryRecord])
}

func (db *DB) IsAgentOwner(ctx context.Context, agentID int64, username string) (bool, error) {
	var owner bool
	err := db.PG.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM agents a JOIN users u ON u.id = a.owner_id
		WHERE a.id = $1 AND u.username = $2)`, agentID, username).Scan(&owner)
	return owner, err
}

// EditMemory commits the authoritative text before queuing vector synchronization.
func (db *DB) EditMemory(ctx context.Context, agentID, id, revision int64, text, status string) (MemoryRecord, error) {
	tx, err := db.PG.Begin(ctx)
	if err != nil {
		return MemoryRecord{}, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `UPDATE memories SET status = $4,
		content_json = CASE WHEN $4 = 'active' THEN jsonb_set(content_json, '{text}', to_jsonb($5::text))
		                    ELSE content_json END,
		revision = revision + 1, corrected_at = now(), updated_at = now(),
		vector_status = 'pending', job_attempts = 0, job_run_at = now(), error_msg = ''
		WHERE agent_id = $1 AND id = $2 AND revision = $3
		  AND status IN ('active', 'archived', 'pending_confirm')
		RETURNING `+memoryFields, agentID, id, revision, status, text)
	if err != nil {
		return MemoryRecord{}, err
	}
	memory, err := pgx.CollectOneRow(rows, pgx.RowToStructByPos[MemoryRecord])
	if err != nil {
		return MemoryRecord{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET memory_revision = memory_revision + 1 WHERE id = $1`, agentID); err != nil {
		return MemoryRecord{}, err
	}
	return memory, tx.Commit(ctx)
}

func MemoryTypeLabel(kind string) string {
	switch kind {
	case "fact":
		return "长期事实"
	case "episodic":
		return "情景记忆"
	case "procedural":
		return "做事方法"
	default:
		return "记忆"
	}
}

func (db *DB) RecentCorrections(ctx context.Context, agentID int64) ([]MemoryRecord, error) {
	rows, err := db.PG.Query(ctx, `SELECT `+memoryFields+` FROM memories
		WHERE agent_id = $1 AND corrected_at IS NOT NULL AND status = 'active'
		ORDER BY corrected_at DESC, id DESC LIMIT 12`, agentID)
	if err != nil {
		return nil, fmt.Errorf("load confirmed corrections: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[MemoryRecord])
}

func (db *DB) CompleteMemoryVector(ctx context.Context, id, revision int64) error {
	// A correction made during indexing has already queued the newer revision.
	_, err := db.PG.Exec(ctx, `UPDATE memories SET vector_status = 'ready', error_msg = ''
		WHERE id = $1 AND revision = $2 AND vector_status = 'indexing'`, id, revision)
	return err
}
