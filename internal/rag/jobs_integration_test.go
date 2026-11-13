package rag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"digital-twin/internal/embedding"
	"digital-twin/internal/llm"
	"digital-twin/internal/store"

	"github.com/jackc/pgx/v5/pgxpool"
)

type jobTestModel struct {
	llm.Client
	calls  int
	failOn int
}

func (m *jobTestModel) Complete(context.Context, []llm.Message) (string, llm.Usage, error) {
	m.calls++
	if m.calls == m.failOn {
		return "", llm.Usage{}, errors.New("temporary extraction error")
	}
	return `{"facts":[{"text":"我每周整理读书笔记","tags":["阅读"]}]}`, llm.Usage{}, nil
}

// Run with TWIN_TEST_POSTGRES pointing to a disposable PostgreSQL database.
func TestRecoverableIndexing(t *testing.T) {
	dsn := os.Getenv("TWIN_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("TWIN_TEST_POSTGRES is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	admin, err := pgxpool.New(ctx, dsn)
	requireJobTest(t, err)
	defer admin.Close()
	schema := fmt.Sprintf("jobs_test_%d", time.Now().UnixNano())
	_, err = admin.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public")
	requireJobTest(t, err)
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+schema)
	requireJobTest(t, err)
	defer admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	requireJobTest(t, err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	requireJobTest(t, err)
	defer pool.Close()
	db := &store.DB{PG: pool}

	// Include records written by the previous release before applying the new migration.
	for _, file := range []string{"001_init.sql", "002_knowledge_phase1.sql", "003_conversation_user.sql", "004_conversation_extract.sql", "005_journal_profile.sql", "006_conversation_summary.sql"} {
		sql, err := os.ReadFile("../../migrations/" + file)
		requireJobTest(t, err)
		_, err = pool.Exec(ctx, string(sql))
		requireJobTest(t, err)
	}
	requireJobTest(t, db.EnsureAdminUser(ctx, "test", "test-password"))
	_, err = pool.Exec(ctx, "INSERT INTO agents(owner_id,name) VALUES (1,'test')")
	requireJobTest(t, err)
	_, err = pool.Exec(ctx, "INSERT INTO journals(agent_id,content,digest_at) VALUES(1,'legacy completed',now())")
	requireJobTest(t, err)
	for _, file := range []string{"010_recoverable_jobs.sql", "011_recoverable_conversation_extract.sql", "012_memory_corrections_and_citations.sql"} {
		sql, err := os.ReadFile("../../migrations/" + file)
		requireJobTest(t, err)
		for range 2 {
			_, err = pool.Exec(ctx, string(sql))
			requireJobTest(t, err)
		}
	}
	requireJobTest(t, db.EnsurePartitions(ctx))
	docID, err := db.InsertKnowledgeDoc(ctx, 1, "txt", "", "test", "knowledge text")
	requireJobTest(t, err)
	journalID, err := db.InsertJournal(ctx, 1, "journal text", "journal")
	requireJobTest(t, err)

	var bad atomic.Bool
	bad.Store(true)
	var mu sync.Mutex
	writes := make(map[string][][]uint64)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/embeddings/"):
			fmt.Fprint(w, `{"data":{"embedding":[1,0]}}`)
		case strings.HasSuffix(r.URL.Path, "/points") && r.Method == http.MethodPut:
			var payload struct {
				Points []struct {
					ID uint64 `json:"id"`
				} `json:"points"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			var ids []uint64
			for _, p := range payload.Points {
				ids = append(ids, p.ID)
			}
			mu.Lock()
			writes[r.URL.Path] = append(writes[r.URL.Path], ids)
			mu.Unlock()
			if bad.Load() {
				http.Error(w, "lost response after writing points", http.StatusServiceUnavailable)
			}
		case strings.HasSuffix(r.URL.Path, "/points/delete"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer fake.Close()
	emb, qd := embedding.New(fake.URL, "test", "test"), NewQdrant(fake.URL, "")
	indexer := NewIndexer(db, emb, qd)
	model := &jobTestModel{}
	extractor := NewMemoryExtractor(db, model, emb, qd)

	for _, tc := range []struct {
		kind                       store.JobKind
		table, running, collection string
		id                         int64
		run                        func(context.Context, int64) error
	}{
		{store.KnowledgeJob, "knowledge_docs", "indexing", CollectionKnowledge, docID, indexer.IndexDoc},
		{store.JournalJob, "journals", "digesting", CollectionMemory, journalID, func(ctx context.Context, id int64) error {
			return extractor.ExtractJournal(ctx, 1, "test", id)
		}},
	} {
		t.Run(tc.table, func(t *testing.T) {
			bad.Store(true)
			stopCtx, stop := context.WithCancel(ctx)
			defer stop()
			ran, err := db.RunNextJob(stopCtx, tc.kind, 1, func(ctx context.Context, id int64) error {
				if id != tc.id {
					return fmt.Errorf("claimed %d, expected %d", id, tc.id)
				}
				duplicate, err := db.RunNextJob(ctx, tc.kind, 1, func(context.Context, int64) error {
					return errors.New("second worker executed a claimed job")
				})
				if err != nil || duplicate {
					t.Errorf("duplicate claim: ran=%v err=%v", duplicate, err)
				}
				if err := tc.run(ctx, id); err == nil {
					t.Error("expected vector write failure")
				}
				stop()
				return ctx.Err()
			})
			requireJobTest(t, err)
			if !ran {
				t.Fatal("worker did not claim job")
			}
			assertState := func(status string, attempts int) {
				t.Helper()
				var got string
				var count int
				err := pool.QueryRow(ctx, "SELECT status, job_attempts FROM "+tc.table+" WHERE id=$1", tc.id).Scan(&got, &count)
				requireJobTest(t, err)
				if got != status || count != attempts {
					t.Fatalf("state = %s/%d, want %s/%d", got, count, status, attempts)
				}
			}
			assertState(tc.running, 0)
			for attempt := 1; attempt <= 3; attempt++ {
				ran, err := db.RunNextJob(ctx, tc.kind, 1, tc.run)
				if !ran || err == nil {
					t.Fatalf("attempt %d: ran=%v err=%v", attempt, ran, err)
				}
				if attempt < 3 {
					assertState("pending", attempt)
					ran, err = db.RunNextJob(ctx, tc.kind, 1, tc.run)
					requireJobTest(t, err)
					if ran {
						t.Fatal("automatic retry ignored its backoff")
					}
					_, err := pool.Exec(ctx, "UPDATE "+tc.table+" SET job_run_at=now() WHERE id=$1", tc.id)
					requireJobTest(t, err)
				}
			}
			assertState("failed", 3)
			if tc.kind == store.JournalJob {
				var visible int
				err := pool.QueryRow(ctx, "SELECT count(*) FROM memories WHERE status='active'").Scan(&visible)
				requireJobTest(t, err)
				if visible != 0 {
					t.Fatalf("failed journal exposed %d memories", visible)
				}
			}
			requeued, err := db.RetryJob(ctx, tc.kind, 1, tc.id)
			requireJobTest(t, err)
			if !requeued {
				t.Fatal("manual retry did not requeue failed job")
			}
			bad.Store(false)
			ran, err = db.RunNextJob(ctx, tc.kind, 1, tc.run)
			requireJobTest(t, err)
			if !ran {
				t.Fatal("manual retry did not run")
			}
			assertState("ready", 0)
			mu.Lock()
			defer mu.Unlock()
			attempts := writes["/collections/"+tc.collection+"/points"]
			if len(attempts) != 5 || len(attempts[0]) != 1 {
				t.Fatalf("unexpected point writes: %v", attempts)
			}
			for _, ids := range attempts[1:] {
				if !slices.Equal(attempts[0], ids) {
					t.Fatalf("point IDs changed across retries: %v", attempts)
				}
			}
		})
	}

	var memoryCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM memories WHERE source_ref=$1 AND status='active'", fmt.Sprintf("journal:%d", journalID)).Scan(&memoryCount)
	requireJobTest(t, err)
	if model.calls != 1 || memoryCount != 1 {
		t.Fatalf("journal produced %d model calls and %d active memories", model.calls, memoryCount)
	}
	jobs, err := db.ListJournalJobs(ctx, 1)
	requireJobTest(t, err)
	if len(jobs) != 2 || jobs[1].Status != "ready" {
		t.Fatalf("legacy journal status was not preserved: %+v", jobs)
	}
	requireJobTest(t, indexer.DeleteDoc(ctx, 1, docID))
	t.Run("conversations", func(t *testing.T) {
		conv, err := db.CreateConversation(ctx, 1)
		requireJobTest(t, err)
		for range 31 {
			_, err := db.AppendMessage(ctx, conv.ID, "user", "conversation text", 0)
			requireJobTest(t, err)
		}
		const query = "SELECT status, extract_until_id, mem_extracted_at IS NOT NULL FROM conversations WHERE id=$1"
		var status *string
		var untilID int64
		var completed bool
		assertState := func(want string, wantComplete bool) {
			t.Helper()
			requireJobTest(t, pool.QueryRow(ctx, query, conv.ID).Scan(&status, &untilID, &completed))
			if (status == nil && want != "") || (status != nil && *status != want) || completed != wantComplete {
				t.Fatalf("unexpected extraction state: status=%v completed=%v", status, completed)
			}
		}
		requireJobTest(t, db.QueueExpiredConversationExtracts(ctx, 1, time.Minute))
		assertState("", false)
		requireJobTest(t, db.QueueExpiredConversationExtracts(ctx, 1, 0))
		assertState("pending", false)
		initialUntil := untilID
		run := func(ctx context.Context, id int64) error {
			return extractor.ExtractConversation(ctx, 1, "test", id)
		}
		attempt := func(runCtx context.Context, wantError bool) {
			t.Helper()
			ran, err := db.RunNextJob(runCtx, store.ConversationJob, 1, run)
			if !ran || (err != nil) != wantError {
				t.Fatalf("conversation attempt: ran=%v err=%v", ran, err)
			}
		}
		model.failOn = model.calls + 2
		attempt(ctx, true)
		assertState("pending", false)
		var staged int
		requireJobTest(t, pool.QueryRow(ctx, "SELECT count(*) FROM memories WHERE status='indexing'").Scan(&staged))
		if staged != 0 {
			t.Fatalf("partial extraction staged %d memories", staged)
		}
		lastMessage, err := db.AppendMessage(ctx, conv.ID, "user", "message added after queueing", 0)
		requireJobTest(t, err)
		_, err = pool.Exec(ctx, "UPDATE conversations SET job_run_at=now() WHERE id=$1", conv.ID)
		requireJobTest(t, err)
		bad.Store(true)
		attempt(ctx, true)
		assertState("pending", false)
		if untilID != initialUntil || lastMessage <= untilID {
			t.Fatal("a retry changed the pinned message range")
		}
		var firstDraftID int64
		requireJobTest(t, pool.QueryRow(ctx, "SELECT id FROM memories WHERE status='indexing'").Scan(&firstDraftID))
		_, err = pool.Exec(ctx, "UPDATE conversations SET job_run_at=now() WHERE id=$1", conv.ID)
		requireJobTest(t, err)
		attempt(ctx, true)
		assertState("failed", false)
		requireJobTest(t, db.QueueExpiredConversationExtracts(ctx, 1, 0))
		assertState("failed", false)
		ok, err := db.RetryConversationExtract(ctx, 2, conv.ID)
		requireJobTest(t, err)
		if ok {
			t.Fatal("another user could retry this conversation")
		}
		ok, err = db.RetryConversationExtract(ctx, 1, conv.ID)
		requireJobTest(t, err)
		if !ok {
			t.Fatal("owner could not retry the conversation")
		}
		callsBefore := model.calls
		bad.Store(false)
		attempt(ctx, false)
		assertState("ready", true)
		if model.calls != callsBefore {
			t.Fatal("retry repeated completed model extraction")
		}
		var activeDraftID int64
		requireJobTest(t, pool.QueryRow(ctx, "SELECT id FROM memories WHERE source_ref=$1 AND status='active'", fmt.Sprintf("conversation:%d:%d", conv.ID, initialUntil)).Scan(&activeDraftID))
		if activeDraftID != firstDraftID {
			t.Fatal("retry replaced the saved memory ID")
		}
		requireJobTest(t, db.QueueExpiredConversationExtracts(ctx, 1, 0))
		assertState("pending", true)
		if untilID != lastMessage {
			t.Fatalf("next job ended at %d, want %d", untilID, lastMessage)
		}
		attempt(ctx, false)
		requireJobTest(t, db.QueueExpiredConversationExtracts(ctx, 1, 0))
		assertState("ready", true)
		if model.calls != callsBefore+1 {
			t.Fatal("the new message did not run as a separate extraction")
		}
	})

	t.Run("memory corrections and citation snapshots", func(t *testing.T) {
		ref := fmt.Sprintf("journal:%d", journalID)
		conv, err := db.CreateConversation(ctx, 1)
		requireJobTest(t, err)
		_, err = db.AppendMessage(ctx, conv.ID, "user", "原始消息", 0)
		requireJobTest(t, err)

		original := "每周五整理读书笔记"
		memID, err := db.InsertMemory(ctx, 1, "fact", original, 6, ref, nil)
		requireJobTest(t, err)
		memory, err := db.GetMemory(ctx, 1, memID)
		requireJobTest(t, err)
		citation := store.Citation{Number: 1, Kind: "memory", ID: memID, Text: original, SourceRef: ref, Revision: memory.Revision}
		_, err = db.AppendReply(ctx, conv.ID, "我在周五整理笔记 [1]", 5, []store.Citation{citation})
		requireJobTest(t, err)

		_, _, oldProfileRev, err := db.ProfileForRebuild(ctx, 1)
		requireJobTest(t, err)
		requireJobTest(t, db.SaveAgentProfile(ctx, 1, oldProfileRev, `{"identity":"旧画像"}`, `{}`))
		corrected := "每周日整理读书笔记"
		updated, err := db.EditMemory(ctx, 1, memID, memory.Revision, corrected, "active")
		requireJobTest(t, err)
		if updated.Revision != memory.Revision+1 || updated.Text != corrected || updated.VectorStatus != "pending" {
			t.Fatalf("edit did not commit the new text and queue the vector: %+v", updated)
		}
		if _, err := db.EditMemory(ctx, 1, memID, memory.Revision, "过期的修改", "active"); err == nil {
			t.Fatal("stale edit overwrote a newer memory")
		}
		profile, _, err := db.GetAgentProfile(ctx, 1)
		requireJobTest(t, err)
		if profile != "{}" || db.SaveAgentProfile(ctx, 1, oldProfileRev, `{"identity":"旧画像"}`, `{}`) == nil {
			t.Fatal("a stale profile remained usable after correction")
		}

		retriever := NewRetriever(db, emb, qd)
		candidates := map[string]*RetrievalItem{"memory": {Citation: store.Citation{Kind: "memory", ID: memID}}}
		requireJobTest(t, retriever.fillSources(ctx, 1, candidates))
		if got := candidates["memory"].Citation; got.Text != corrected || !got.Corrected || got.SourceRef != ref {
			t.Fatalf("candidate did not hydrate the corrected source: %+v", got)
		}
		history, err := db.RecentMessagesWithCitations(ctx, conv.ID, 2)
		requireJobTest(t, err)
		if len(history) != 2 || len(history[0].Citations) != 0 ||
			len(history[1].Citations) != 1 || history[1].Citations[0] != citation {
			t.Fatalf("history did not retain the original citation: %+v", history)
		}
		work, err := db.RecentMessages(ctx, conv.ID, 2)
		requireJobTest(t, err)
		if len(work) != 2 || len(work[1].Citations) != 0 {
			t.Fatalf("unexpected citations in working memory: %+v", work)
		}
		content, _, err := db.JournalSource(ctx, 1, journalID)
		requireJobTest(t, err)
		if content != "journal text" {
			t.Fatalf("journal source was changed: %q", content)
		}
		messages, err := db.ConversationSource(ctx, 1, 2, conv.ID, 0)
		requireJobTest(t, err)
		if len(messages) != 0 {
			t.Fatal("another user could read the conversation source")
		}

		syncMemory := func(ctx context.Context, id int64) error {
			return extractor.SyncEditedMemory(ctx, 1, id)
		}
		ran, err := db.RunNextJob(ctx, store.MemoryVectorJob, 1, syncMemory)
		requireJobTest(t, err)
		if !ran {
			t.Fatal("corrected memory was not indexed")
		}
		updated, err = db.GetMemory(ctx, 1, memID)
		requireJobTest(t, err)
		if updated.VectorStatus != "ready" {
			t.Fatalf("vector status = %q", updated.VectorStatus)
		}
		_, err = db.EditMemory(ctx, 1, memID, updated.Revision, "", "archived")
		requireJobTest(t, err)
		candidates["memory"].Text = ""
		requireJobTest(t, retriever.fillSources(ctx, 1, candidates))
		if candidates["memory"].Text != "" {
			t.Fatal("archived memory was hydrated")
		}
		ran, err = db.RunNextJob(ctx, store.MemoryVectorJob, 1, syncMemory)
		requireJobTest(t, err)
		if !ran {
			t.Fatal("archived vector was not removed")
		}
	})

}

func requireJobTest(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
