package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// 博客正文的存储格式。text 为早期纯文本文章，markdown 为现在的默认格式。
const (
	BlogFormatText     = "text"
	BlogFormatMarkdown = "markdown"
)

type BlogSummary struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Excerpt   string    `json:"excerpt"`
	Format    string    `json:"format"`
	CreatedAt time.Time `json:"created_at"`
}

type BlogPost struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Format    string    `json:"format"`
	CreatedAt time.Time `json:"created_at"`
}

type BlogJobStatus struct {
	ID              int64     `json:"id"`
	Title           string    `json:"title"`
	Status          string    `json:"status"`
	KnowledgeID     int64     `json:"knowledge_id"`
	KnowledgeStatus string    `json:"knowledge_status"`
	ErrorMsg        string    `json:"error_msg"`
	CreatedAt       time.Time `json:"created_at"`
}

// InsertBlog atomically creates the public post, its memory job and its knowledge document.
func (db *DB) InsertBlog(ctx context.Context, agentID int64, title, content, format string) (int64, int64, error) {
	tx, err := db.PG.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)
	var journalID int64
	if err = tx.QueryRow(ctx, `INSERT INTO journals (agent_id, content, source, title, format)
		VALUES ($1, $2, 'blog', $3, $4) RETURNING id`, agentID, content, title, format).Scan(&journalID); err != nil {
		return 0, 0, err
	}
	var knowledgeID int64
	sourceURL := fmt.Sprintf("/blogs/?id=%d", journalID)
	if err = tx.QueryRow(ctx, `INSERT INTO knowledge_docs (agent_id, source_type, source_url, title, status, content)
		VALUES ($1, 'md', $2, $3, 'pending', $4) RETURNING id`,
		agentID, sourceURL, title, content).Scan(&knowledgeID); err != nil {
		return 0, 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE journals SET knowledge_doc_id = $2 WHERE id = $1`, journalID, knowledgeID); err != nil {
		return 0, 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return journalID, knowledgeID, nil
}

// ErrBlogNotFound 表示指定 id 的博客不存在或不属于该 agent；ErrBlogBusy 表示后台正在处理，暂不能改。
var (
	ErrBlogNotFound = errors.New("blog not found")
	ErrBlogBusy     = errors.New("blog is being processed")
)

// UpdateBlog 覆盖已有文章的标题与正文，并把关联的知识文档重新排队索引：
// chunks_saved 置回 false 后 IndexDoc 会删掉旧向量、按新正文重新分块。
// 日志侧的记忆抽取不重跑（memories 按 source_ref 追加式写入，重跑会产生重复记忆）。
func (db *DB) UpdateBlog(ctx context.Context, agentID, id int64, title, content, format string) (int64, error) {
	tx, err := db.PG.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var journalStatus string
	var knowledgeID *int64
	err = tx.QueryRow(ctx, `SELECT status, knowledge_doc_id FROM journals
		WHERE id = $1 AND agent_id = $2 AND source = 'blog' FOR UPDATE`, id, agentID).Scan(&journalStatus, &knowledgeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrBlogNotFound
	}
	if err != nil {
		return 0, err
	}
	if journalStatus == "digesting" {
		return 0, ErrBlogBusy
	}
	if _, err = tx.Exec(ctx, `UPDATE journals SET title = $3, content = $4, format = $5 WHERE id = $1 AND agent_id = $2`,
		id, agentID, title, content, format); err != nil {
		return 0, err
	}

	var docID int64
	if knowledgeID != nil {
		docID = *knowledgeID
		var docStatus string
		err = tx.QueryRow(ctx, `SELECT status FROM knowledge_docs WHERE id = $1 AND agent_id = $2 FOR UPDATE`, docID, agentID).Scan(&docStatus)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return 0, err
		}
		if err == nil && docStatus != "deleted" { // 已被单独删除的知识文档不复活
			if docStatus == "indexing" {
				return 0, ErrBlogBusy
			}
			if _, err = tx.Exec(ctx, `UPDATE knowledge_docs SET title = $3, content = $4, status = 'pending', chunks_saved = false,
				error_msg = '', job_attempts = 0, job_run_at = now() WHERE id = $1 AND agent_id = $2`, docID, agentID, title, content); err != nil {
				return 0, err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return docID, nil
}

func (db *DB) ListPublicBlogs(ctx context.Context, agentID int64) ([]BlogSummary, error) {
	// 摘取前 2000 字符交给上层去 Markdown 标记后再截断，避免整篇正文进内存。
	rows, err := db.PG.Query(ctx, `SELECT id, title, left(content, 2000), format, created_at FROM journals
		WHERE agent_id = $1 AND source = 'blog' AND title <> '' ORDER BY id DESC LIMIT 100`, agentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[BlogSummary])
}

func (db *DB) GetPublicBlog(ctx context.Context, agentID, id int64) (BlogPost, error) {
	var post BlogPost
	err := db.PG.QueryRow(ctx, `SELECT id, title, content, format, created_at FROM journals
		WHERE agent_id = $1 AND source = 'blog' AND id = $2`, agentID, id).Scan(
		&post.ID, &post.Title, &post.Content, &post.Format, &post.CreatedAt)
	return post, err
}

func (db *DB) ListBlogJobs(ctx context.Context, agentID int64) ([]BlogJobStatus, error) {
	rows, err := db.PG.Query(ctx, `SELECT j.id, j.title, j.status, j.knowledge_doc_id,
		COALESCE(k.status, 'pending'), j.error_msg, j.created_at
		FROM journals j LEFT JOIN knowledge_docs k ON k.id = j.knowledge_doc_id
		WHERE j.agent_id = $1 AND j.source = 'blog' ORDER BY j.id DESC LIMIT 100`, agentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[BlogJobStatus])
}
