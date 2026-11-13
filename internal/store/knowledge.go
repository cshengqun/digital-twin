package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrKnowledgeBusy = errors.New("知识库正在入库，请稍后删除")

// Chunk 是向量检索的最小单位（定义在 store，避免 rag↔store 循环依赖）。
type Chunk struct {
	Index int    // 在文档内的序号（从 0 起）
	Text  string // 分块后的纯文本
}

// KnowledgeDoc 对应 knowledge_docs 一行（含 Phase 1 新增原文列）。
type KnowledgeDoc struct {
	ID          int64
	AgentID     int64
	SourceType  string
	SourceURL   string
	Title       string
	Status      string
	Content     string
	ErrorMsg    string
	ChunksSaved bool
	ChunkCount  int
	CreatedAt   time.Time
}

// KnowledgeChunkRow 对应 knowledge_chunks 一行（含自增 id，用于 Qdrant point id）。
type KnowledgeChunkRow struct {
	ID        int64
	DocID     int64
	Content   string
	CreatedAt time.Time
}

// InsertKnowledgeDoc 创建知识文档记录，返回 doc id。
func (db *DB) InsertKnowledgeDoc(ctx context.Context, agentID int64, sourceType, sourceURL, title, content string) (int64, error) {
	var id int64
	err := db.PG.QueryRow(ctx,
		`INSERT INTO knowledge_docs (agent_id, source_type, source_url, title, status, content)
		 VALUES ($1, $2, $3, $4, 'pending', $5) RETURNING id`,
		agentID, sourceType, sourceURL, title, content).Scan(&id)
	return id, err
}

func (db *DB) GetKnowledgeDoc(ctx context.Context, id int64) (*KnowledgeDoc, error) {
	d := &KnowledgeDoc{}
	err := db.PG.QueryRow(ctx,
		`SELECT id, agent_id, source_type, source_url, title, status, content, error_msg, chunk_count, created_at, chunks_saved
		 FROM knowledge_docs WHERE id = $1`, id).
		Scan(&d.ID, &d.AgentID, &d.SourceType, &d.SourceURL, &d.Title, &d.Status, &d.Content, &d.ErrorMsg, &d.ChunkCount, &d.CreatedAt, &d.ChunksSaved)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (db *DB) ListKnowledgeDocs(ctx context.Context, agentID int64) ([]KnowledgeDoc, error) {
	rows, err := db.PG.Query(ctx,
		`SELECT id, agent_id, source_type, source_url, title, status, content, error_msg, chunk_count, created_at
		 FROM knowledge_docs
		 WHERE agent_id = $1 AND status <> 'deleted'
		 ORDER BY id DESC
		 LIMIT 500`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnowledgeDoc
	for rows.Next() {
		var d KnowledgeDoc
		if err := rows.Scan(&d.ID, &d.AgentID, &d.SourceType, &d.SourceURL, &d.Title, &d.Status, &d.Content, &d.ErrorMsg, &d.ChunkCount, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListKnowledgeChunks reads checkpointed chunks in document order.
func (db *DB) ListKnowledgeChunks(ctx context.Context, docID int64) ([]KnowledgeChunkRow, error) {
	rows, err := db.PG.Query(ctx, `SELECT id, doc_id, content, created_at FROM knowledge_chunks
		WHERE doc_id = $1 ORDER BY (metadata_json->>'idx')::int, id`, docID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[KnowledgeChunkRow])
}

// SaveKnowledgeChunks replaces any partial legacy chunks and stores the new
// chunks and checkpoint in one transaction.
func (db *DB) SaveKnowledgeChunks(ctx context.Context, docID int64, chunks []Chunk) ([]KnowledgeChunkRow, error) {
	tx, err := db.PG.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE knowledge_docs SET chunks_saved = true
		WHERE id = $1 AND status = 'indexing' AND NOT chunks_saved`, docID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != 1 {
		return nil, fmt.Errorf("文档状态已变化")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM knowledge_chunks WHERE doc_id = $1`, docID); err != nil {
		return nil, err
	}
	out := make([]KnowledgeChunkRow, 0, len(chunks))
	for _, c := range chunks {
		var row KnowledgeChunkRow
		err := tx.QueryRow(ctx,
			`INSERT INTO knowledge_chunks (doc_id, content, metadata_json)
			 VALUES ($1, $2, jsonb_build_object('idx', $3::int))
			 RETURNING id, doc_id, content, created_at`,
			docID, c.Text, c.Index).Scan(&row.ID, &row.DocID, &row.Content, &row.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("insert chunk %d: %w", c.Index, err)
		}
		out = append(out, row)
	}
	return out, tx.Commit(ctx)
}

func (db *DB) CompleteKnowledgeJob(ctx context.Context, docID int64, count int) error {
	tag, err := db.PG.Exec(ctx, `UPDATE knowledge_docs SET status = 'ready', chunk_count = $2, error_msg = ''
		WHERE id = $1 AND status = 'indexing'`, docID, count)
	if err == nil && tag.RowsAffected() != 1 {
		return fmt.Errorf("文档状态已变化")
	}
	return err
}

// DeleteKnowledgeDoc serializes deletion with the indexing attempt.
func (db *DB) DeleteKnowledgeDoc(ctx context.Context, agentID, id int64, removeVectors func([]uint64) error) error {
	tx, err := db.PG.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1, $2)`, jobLockNamespace, KnowledgeJob).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return ErrKnowledgeBusy
	}
	var existingID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM knowledge_docs WHERE id = $1 AND agent_id = $2 FOR UPDATE`, id, agentID).Scan(&existingID); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM knowledge_chunks WHERE doc_id = $1`, id)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uint64])
	if err != nil {
		return err
	}
	if err := removeVectors(ids); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE knowledge_docs SET status = 'deleted' WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SearchKnowledgeByKeyword 关键词检索：pg_trgm 子串匹配，按命中数排序。
// query 拆成关键词（简单按空白/标点拆），每个关键词 ILIKE 匹配，返回最多 topK 个 chunk。
func (db *DB) SearchKnowledgeByKeyword(ctx context.Context, docAgentID int64, keywords []string, topK int) ([]KnowledgeChunkRow, error) {
	if len(keywords) == 0 {
		return nil, nil
	}
	// 构造 OR 条件 + 命中计数
	conds := ""
	for i := range keywords {
		if i > 0 {
			conds += " OR "
		}
		conds += fmt.Sprintf("c.content ILIKE $%d", i+2)
	}
	args := make([]any, 0, len(keywords)+1)
	args = append(args, docAgentID)
	for _, kw := range keywords {
		args = append(args, "%"+kw+"%")
	}
	// 命中次数排序：每条记录计算命中关键词数
	hit := ""
	for i := range keywords {
		if i > 0 {
			hit += "+"
		}
		hit += fmt.Sprintf("CASE WHEN c.content ILIKE $%d THEN 1 ELSE 0 END", i+2)
	}
	sql := fmt.Sprintf(
		`SELECT c.id, c.doc_id, c.content, c.created_at
		 FROM knowledge_chunks c
		 JOIN knowledge_docs d ON d.id = c.doc_id
		 WHERE d.agent_id = $1 AND d.status = 'ready' AND (%s)
		 ORDER BY (%s) DESC, c.created_at DESC
		 LIMIT $%d`, conds, hit, len(args)+1)
	args = append(args, topK)

	rows, err := db.PG.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KnowledgeChunkRow
	for rows.Next() {
		var r KnowledgeChunkRow
		if err := rows.Scan(&r.ID, &r.DocID, &r.Content, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
