package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type Citation struct {
	Number    int    `json:"number"`
	Kind      string `json:"kind"`
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Text      string `json:"text"`
	URL       string `json:"url,omitempty"`
	Chunk     int    `json:"chunk,omitempty"`
	SourceRef string `json:"source_ref,omitempty"`
	Revision  int64  `json:"revision,omitempty"`
	Corrected bool   `json:"corrected,omitempty"`
}

func (db *DB) KnowledgeCitations(ctx context.Context, agentID int64, ids []int64) ([]Citation, error) {
	rows, err := db.PG.Query(ctx, `SELECT c.id, d.title, c.content, d.source_url,
		COALESCE((c.metadata_json->>'idx')::int + 1, 0)
		FROM knowledge_chunks c JOIN knowledge_docs d ON d.id = c.doc_id
		WHERE c.id = ANY($1) AND d.agent_id = $2 AND d.status = 'ready'`, ids, agentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Citation, error) {
		c := Citation{Kind: "knowledge"}
		err := row.Scan(&c.ID, &c.Title, &c.Text, &c.URL, &c.Chunk)
		return c, err
	})
}

func (db *DB) JournalSource(ctx context.Context, agentID, id int64) (content string, date time.Time, err error) {
	err = db.PG.QueryRow(ctx, `SELECT content, created_at FROM journals WHERE agent_id = $1 AND id = $2`,
		agentID, id).Scan(&content, &date)
	return
}

func (db *DB) ConversationSource(ctx context.Context, agentID, userID, id, until int64) ([]Message, error) {
	rows, err := db.PG.Query(ctx, `
		SELECT id, role, content, tokens, created_at FROM (
			SELECT m.id, m.role, m.content, m.tokens, m.created_at
			FROM messages m JOIN conversations c ON c.id = m.conversation_id
			WHERE c.id = $1 AND c.agent_id = $2 AND c.user_id = $3 AND ($4 = 0 OR m.id <= $4)
			ORDER BY m.id DESC LIMIT 500
		) recent ORDER BY id`, id, agentID, userID, until)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Message, error) {
		var msg Message
		err := row.Scan(&msg.ID, &msg.Role, &msg.Content, &msg.Tokens, &msg.CreatedAt)
		return msg, err
	})
}
