package store

import (
	"context"
	"fmt"
	"time"
)

// Chunk 是向量检索的最小单位（定义在 store，避免 rag↔store 循环依赖）。
type Chunk struct {
	Index int    // 在文档内的序号（从 0 起）
	Text  string // 分块后的纯文本
}

// KnowledgeDoc 对应 knowledge_docs 一行（含 Phase 1 新增原文列）。
type KnowledgeDoc struct {
	ID         int64
	AgentID    int64
	SourceType string
	SourceURL  string
	Title      string
	Status     string
	Content    string
	ErrorMsg   string
	ChunkCount int
	CreatedAt  time.Time
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
		`SELECT id, agent_id, source_type, source_url, title, status, content, error_msg, chunk_count, created_at
		 FROM knowledge_docs WHERE id = $1`, id).
		Scan(&d.ID, &d.AgentID, &d.SourceType, &d.SourceURL, &d.Title, &d.Status, &d.Content, &d.ErrorMsg, &d.ChunkCount, &d.CreatedAt)
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

// UpdateKnowledgeStatus 更新入库状态；errorMsg 为空串时保留原值。
func (db *DB) UpdateKnowledgeStatus(ctx context.Context, id int64, status string, chunkCount int, errorMsg string) error {
	_, err := db.PG.Exec(ctx,
		`UPDATE knowledge_docs SET status = $2, chunk_count = $3,
		        error_msg = CASE WHEN $4 = '' THEN error_msg ELSE $4 END
		 WHERE id = $1`, id, status, chunkCount, errorMsg)
	return err
}

// InsertKnowledgeChunks 批量写入分块（分区表按 created_at 落月分区），返回带自增 id 的行。
func (db *DB) InsertKnowledgeChunks(ctx context.Context, docID int64, chunks []Chunk) ([]KnowledgeChunkRow, error) {
	out := make([]KnowledgeChunkRow, 0, len(chunks))
	for _, c := range chunks {
		var row KnowledgeChunkRow
		err := db.PG.QueryRow(ctx,
			`INSERT INTO knowledge_chunks (doc_id, content, metadata_json)
			 VALUES ($1, $2, jsonb_build_object('idx', $3::int))
			 RETURNING id, doc_id, content, created_at`,
			docID, c.Text, c.Index).Scan(&row.ID, &row.DocID, &row.Content, &row.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("insert chunk %d: %w", c.Index, err)
		}
		out = append(out, row)
	}
	return out, nil
}

// DeleteKnowledgeDoc 软删除文档并返回被删 chunk id（供 Qdrant 清理）。
func (db *DB) DeleteKnowledgeDoc(ctx context.Context, id int64) ([]uint64, error) {
	rows, err := db.PG.Query(ctx, `SELECT id FROM knowledge_chunks WHERE doc_id = $1`, id)
	if err != nil {
		return nil, err
	}
	var ids []uint64
	for rows.Next() {
		var cid int64
		if err := rows.Scan(&cid); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, uint64(cid))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_, err = db.PG.Exec(ctx, `UPDATE knowledge_docs SET status = 'deleted' WHERE id = $1`, id)
	return ids, err
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
