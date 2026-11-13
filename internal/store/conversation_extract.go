package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// QueueExpiredConversationExtracts pins the message range for each extraction.
// Completed ranges are skipped on subsequent runs, including after a restart.
func (db *DB) QueueExpiredConversationExtracts(ctx context.Context, agentID int64, idle time.Duration) error {
	_, err := db.PG.Exec(ctx, `
		WITH eligible AS (
			SELECT c.id, last_message.id AS until_id
			FROM conversations c
			CROSS JOIN LATERAL (
				SELECT m.id, m.created_at FROM messages m
				WHERE m.conversation_id = c.id AND m.id > c.extract_until_id
					AND (c.extract_until_id > 0 OR c.mem_extracted_at IS NULL OR m.created_at > c.mem_extracted_at)
				ORDER BY m.id DESC LIMIT 1
			) last_message
			WHERE c.agent_id = $1 AND c.last_active_at < now() - $2::double precision * interval '1 second'
				AND last_message.created_at < now() - $2::double precision * interval '1 second'
				AND (c.status IS NULL OR c.status = 'ready')
			ORDER BY c.last_active_at, c.id LIMIT 20
		)
		UPDATE conversations c
		SET status = 'pending', job_run_at = now(), job_attempts = 0, error_msg = '',
		    memories_saved = false, extract_from_id = c.extract_until_id, extract_until_id = e.until_id
		FROM eligible e
		WHERE c.id = e.id AND c.extract_until_id < e.until_id
			AND (c.status IS NULL OR c.status = 'ready')`, agentID, idle.Seconds())
	return err
}

type ConversationExtractWork struct {
	FromID, UntilID int64
	Saved           bool
	Summary         string
	Messages        []Message
}

func (db *DB) GetConversationExtractWork(ctx context.Context, agentID, id int64) (ConversationExtractWork, error) {
	var work ConversationExtractWork
	err := db.PG.QueryRow(ctx, `SELECT extract_from_id, extract_until_id, memories_saved,
		CASE WHEN extract_from_id = 0 AND mem_extracted_at IS NULL AND summary_until_id <= extract_until_id
			THEN summary ELSE '' END
		FROM conversations WHERE id = $1 AND agent_id = $2 AND status = 'extracting'`, id, agentID).
		Scan(&work.FromID, &work.UntilID, &work.Saved, &work.Summary)
	if err != nil || work.Saved {
		return work, err
	}
	rows, err := db.PG.Query(ctx, `SELECT id, role, content, tokens, created_at FROM (
		SELECT id, role, content, tokens, created_at FROM messages
		WHERE conversation_id = $1 AND id > $2 AND id <= $3 AND (
			$2 > 0 OR created_at > COALESCE((SELECT mem_extracted_at FROM conversations WHERE id = $1), '-infinity'))
		ORDER BY id DESC LIMIT 500
	) recent ORDER BY id`, id, work.FromID, work.UntilID)
	if err != nil {
		return work, err
	}
	work.Messages, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Message, error) {
		var msg Message
		err := row.Scan(&msg.ID, &msg.Role, &msg.Content, &msg.Tokens, &msg.CreatedAt)
		return msg, err
	})
	return work, err
}

func (db *DB) RetryConversationExtract(ctx context.Context, userID, id int64) (bool, error) {
	tag, err := db.PG.Exec(ctx, `UPDATE conversations SET status = 'pending', error_msg = '',
		job_attempts = 0, job_run_at = now() WHERE id = $1 AND user_id = $2 AND status = 'failed'`, id, userID)
	return tag.RowsAffected() == 1, err
}
