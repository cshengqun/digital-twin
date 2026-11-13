package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---------- 账号 ----------

// EnsureAdminUser 在 users 表为空时写入初始管理员账号（密码以 argon2id 哈希落库）。
func (d *DB) EnsureAdminUser(ctx context.Context, username, password string) error {
	var n int
	if err := d.PG.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&n); err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if n > 0 {
		return nil
	}
	hash, err := hashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if _, err := d.PG.Exec(ctx,
		"INSERT INTO users (username, password_hash) VALUES ($1, $2)", username, hash); err != nil {
		return fmt.Errorf("insert admin: %w", err)
	}
	return nil
}

func (d *DB) VerifyPassword(ctx context.Context, username, password string) (bool, error) {
	var hash string
	err := d.PG.QueryRow(ctx, "SELECT password_hash FROM users WHERE username = $1", username).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return verifyPassword(hash, password)
}

// ---------- 会话 ----------

type Conversation struct {
	ID             int64
	UserID         int64
	Title          string
	Summary        string
	SummaryUntilID int64 // 滚动摘要已并入的最大消息 id（增量压缩标记）
	LastAt         time.Time
	CreateAt       time.Time
	ExtractStatus  string
}

// UserIDByUsername 按用户名查用户 ID（会话归属校验用）。
func (d *DB) UserIDByUsername(ctx context.Context, username string) (int64, error) {
	var id int64
	err := d.PG.QueryRow(ctx, "SELECT id FROM users WHERE username = $1", username).Scan(&id)
	return id, err
}

func (d *DB) CreateConversation(ctx context.Context, userID int64) (*Conversation, error) {
	var c Conversation
	err := d.PG.QueryRow(ctx,
		"INSERT INTO conversations (agent_id, user_id) VALUES (1, $1) RETURNING id, user_id, title, summary, COALESCE(summary_until_id,0), last_active_at, created_at", userID).
		Scan(&c.ID, &c.UserID, &c.Title, &c.Summary, &c.SummaryUntilID, &c.LastAt, &c.CreateAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (d *DB) GetConversation(ctx context.Context, id int64) (*Conversation, error) {
	var c Conversation
	err := d.PG.QueryRow(ctx,
		"SELECT id, user_id, title, summary, COALESCE(summary_until_id,0), last_active_at, created_at FROM conversations WHERE id = $1", id).
		Scan(&c.ID, &c.UserID, &c.Title, &c.Summary, &c.SummaryUntilID, &c.LastAt, &c.CreateAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ListConversations 只返回当前用户自己的会话（多人共享分身时列表彼此隔离）。
func (d *DB) ListConversations(ctx context.Context, userID int64) ([]Conversation, error) {
	rows, err := d.PG.Query(ctx,
		`SELECT id, user_id, title, summary, COALESCE(summary_until_id,0), last_active_at, created_at, COALESCE(status, '') FROM conversations
		 WHERE user_id = $1 ORDER BY last_active_at DESC LIMIT 30`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.Summary, &c.SummaryUntilID, &c.LastAt, &c.CreateAt, &c.ExtractStatus); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// IsConversationActive 判断会话是否仍在超时窗口内（会话边界判定：显式新建 + 超时兜底）。
func (d *DB) IsConversationActive(ctx context.Context, id int64, idle time.Duration) bool {
	var last time.Time
	err := d.PG.QueryRow(ctx, "SELECT last_active_at FROM conversations WHERE id = $1", id).Scan(&last)
	if err != nil {
		return false
	}
	return time.Since(last) <= idle
}

func (d *DB) TouchConversation(ctx context.Context, id int64) error {
	_, err := d.PG.Exec(ctx, "UPDATE conversations SET last_active_at = now() WHERE id = $1", id)
	return err
}

func (d *DB) UpdateConversationSummary(ctx context.Context, id int64, summary string) error {
	_, err := d.PG.Exec(ctx, "UPDATE conversations SET summary = $1 WHERE id = $2", summary, id)
	return err
}

// UpdateConversationSummaryUntil 更新滚动摘要并记录已并入的最大消息 id（增量压缩标记）。
func (d *DB) UpdateConversationSummaryUntil(ctx context.Context, id int64, summary string, untilID int64) error {
	_, err := d.PG.Exec(ctx,
		"UPDATE conversations SET summary = $1, summary_until_id = $2 WHERE id = $3", summary, untilID, id)
	return err
}

// CountConversationMessages 统计会话消息总数（判断是否超窗）。
func (d *DB) CountConversationMessages(ctx context.Context, conversationID int64) (int, error) {
	var n int
	err := d.PG.QueryRow(ctx,
		`SELECT count(*) FROM messages WHERE conversation_id = $1`, conversationID).Scan(&n)
	return n, err
}

// ListOldestMessages 按时间正序取最旧 N 条消息（滚动摘要的溢出区）。
func (d *DB) ListOldestMessages(ctx context.Context, conversationID int64, n int) ([]Message, error) {
	rows, err := d.PG.Query(ctx,
		`SELECT id, role, content, tokens, created_at FROM messages
		 WHERE conversation_id = $1 ORDER BY created_at ASC LIMIT $2`, conversationID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.Tokens, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (d *DB) SetConversationTitle(ctx context.Context, id int64, title string) error {
	_, err := d.PG.Exec(ctx, "UPDATE conversations SET title = $1 WHERE id = $2", title, id)
	return err
}

// ---------- 消息 ----------

type Message struct {
	ID        int64      `json:"id"`
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Tokens    int        `json:"tokens"`
	CreatedAt time.Time  `json:"created_at"`
	Citations []Citation `json:"citations,omitempty"`
}

// RecentMessages 按 conversation_id 取最近 N 条原文（工作记忆装载，倒序取再正序返回）。
func (d *DB) RecentMessages(ctx context.Context, conversationID int64, n int) ([]Message, error) {
	return d.recentMessages(ctx, conversationID, n, false)
}

func (d *DB) RecentMessagesWithCitations(ctx context.Context, conversationID int64, n int) ([]Message, error) {
	return d.recentMessages(ctx, conversationID, n, true)
}

func (d *DB) recentMessages(ctx context.Context, conversationID int64, n int, withCitations bool) ([]Message, error) {
	citations := "'[]'::jsonb"
	if withCitations {
		citations = "citations"
	}
	rows, err := d.PG.Query(ctx,
		`SELECT id, role, content, tokens, created_at, citations FROM (
			SELECT id, role, content, tokens, created_at, `+citations+` AS citations FROM messages
			WHERE conversation_id = $1 ORDER BY created_at DESC LIMIT $2
		) t ORDER BY created_at ASC`, conversationID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.Tokens, &m.CreatedAt, &m.Citations); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (d *DB) AppendMessage(ctx context.Context, conversationID int64, role, content string, tokens int) (int64, error) {
	return d.appendMessage(ctx, conversationID, role, content, tokens, nil)
}

func (d *DB) AppendReply(ctx context.Context, conversationID int64, content string, tokens int, citations []Citation) (int64, error) {
	return d.appendMessage(ctx, conversationID, "assistant", content, tokens, citations)
}

func (d *DB) appendMessage(ctx context.Context, conversationID int64, role, content string, tokens int, citations []Citation) (int64, error) {
	if citations == nil {
		citations = []Citation{}
	}
	data, err := json.Marshal(citations)
	if err != nil {
		return 0, err
	}
	var id int64
	err = d.PG.QueryRow(ctx,
		"INSERT INTO messages (conversation_id, role, content, tokens, citations) VALUES ($1,$2,$3,$4,$5) RETURNING id",
		conversationID, role, content, tokens, data).Scan(&id)
	return id, err
}

// RecordUsage 落一条模型用量（成本看板的数据来源）。
func (d *DB) RecordUsage(ctx context.Context, model string, in, out int) error {
	_, err := d.PG.Exec(ctx,
		"INSERT INTO usage_records (agent_id, model, input_tokens, output_tokens) VALUES (1, $1, $2, $3)",
		model, in, out)
	return err
}
