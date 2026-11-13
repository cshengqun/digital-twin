package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const jobLockNamespace int32 = 1901460217

type JobKind int32

const (
	KnowledgeJob    JobKind = 1
	JournalJob      JobKind = 2
	ConversationJob JobKind = 3
	MemoryVectorJob JobKind = 4
)

func (kind JobKind) queue() (table, running, state string) {
	switch kind {
	case KnowledgeJob:
		return "knowledge_docs", "indexing", "status"
	case JournalJob:
		return "journals", "digesting", "status"
	case ConversationJob:
		return "conversations", "extracting", "status"
	case MemoryVectorJob:
		return "memories", "indexing", "vector_status"
	default:
		panic("unknown background job kind")
	}
}

// RunNextJob keeps the queue lock for the duration of one attempt. PostgreSQL
// releases it on disconnect, so a replacement worker can immediately reclaim
// a running row left behind by an interrupted process.
func (d *DB) RunNextJob(ctx context.Context, kind JobKind, agentID int64, run func(context.Context, int64) error) (bool, error) {
	table, running, state := kind.queue()
	var waiting bool
	query := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %[1]s
		WHERE agent_id = $1 AND (%[2]s = $2 OR (%[2]s = 'pending' AND job_run_at <= now())))`, table, state)
	if err := d.PG.QueryRow(ctx, query, agentID, running).Scan(&waiting); err != nil || !waiting {
		return false, err
	}

	// Use a dedicated connection so long-running model calls never hold a pool slot.
	conn, err := pgx.ConnectConfig(ctx, d.PG.Config().ConnConfig)
	if err != nil {
		return false, err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()

	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1, $2)`, jobLockNamespace, kind).Scan(&locked); err != nil {
		return false, err
	}
	if !locked {
		return false, nil
	}

	query = fmt.Sprintf(`UPDATE %[1]s SET %[2]s = $2, error_msg = ''
		WHERE id = (SELECT id FROM %[1]s
			WHERE agent_id = $1 AND (%[2]s = $2 OR (%[2]s = 'pending' AND job_run_at <= now()))
			ORDER BY (%[2]s = $2) DESC, job_run_at, id LIMIT 1)
		AND agent_id = $1 AND %[2]s IN ('pending', $2)
		RETURNING id`, table, state)
	var id int64
	if err := conn.QueryRow(ctx, query, agentID, running).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	err = run(runCtx, id)
	cancel()
	if err == nil {
		return true, nil
	}

	// The execution context may have expired. A shutdown leaves the row
	// claimable without charging a retry to a job that was interrupted.
	if ctx.Err() != nil {
		return true, nil
	}
	finishCtx, finish := context.WithTimeout(ctx, 5*time.Second)
	defer finish()
	query = fmt.Sprintf(`UPDATE %[1]s
		SET %[2]s = CASE WHEN job_attempts + 1 >= 3 THEN 'failed' ELSE 'pending' END,
		    job_attempts = job_attempts + 1,
		    job_run_at = now() + interval '15 seconds' * (job_attempts + 1),
		    error_msg = '处理失败，请重试'
		WHERE id = $1 AND agent_id = $2 AND %[2]s = $3`, table, state)
	if _, updateErr := conn.Exec(finishCtx, query, id, agentID, running); updateErr != nil {
		return true, errors.Join(err, updateErr)
	}
	return true, fmt.Errorf("job %d: %w", id, err)
}

// RetryJob requeues an exhausted job using the original source and saved work.
func (d *DB) RetryJob(ctx context.Context, kind JobKind, agentID, id int64) (bool, error) {
	table, _, state := kind.queue()
	query := fmt.Sprintf(`UPDATE %[1]s SET %[2]s = 'pending', error_msg = '', job_attempts = 0, job_run_at = now()
		WHERE id = $1 AND agent_id = $2 AND %[2]s = 'failed'`, table, state)
	tag, err := d.PG.Exec(ctx, query, id, agentID)
	return tag.RowsAffected() == 1, err
}
