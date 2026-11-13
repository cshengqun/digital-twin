package server

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"digital-twin/internal/store"
)

// RunBackgroundJobs drains the durable queues until the server shuts down.
func (s *Server) RunBackgroundJobs(ctx context.Context) {
	var workers sync.WaitGroup
	if s.indexer != nil {
		workers.Add(1)
		go func() {
			defer workers.Done()
			s.runJobQueue(ctx, store.KnowledgeJob, s.indexer.IndexDoc)
		}()
	}
	if s.extractor != nil {
		workers.Add(1)
		go func() {
			defer workers.Done()
			s.runJobQueue(ctx, store.JournalJob, func(ctx context.Context, id int64) error {
				return s.extractor.ExtractJournal(ctx, s.agentID, s.ownerDesc, id)
			})
		}()
		workers.Add(1)
		go func() {
			defer workers.Done()
			s.runJobQueue(ctx, store.ConversationJob, func(ctx context.Context, id int64) error {
				return s.extractor.ExtractConversation(ctx, s.agentID, s.ownerDesc, id)
			})
		}()
		workers.Add(1)
		go func() {
			defer workers.Done()
			s.runJobQueue(ctx, store.MemoryVectorJob, func(ctx context.Context, id int64) error {
				return s.extractor.SyncEditedMemory(ctx, s.agentID, id)
			})
		}()
		workers.Add(1)
		go func() {
			defer workers.Done()
			s.scheduleConversationExtracts(ctx)
		}()
	}
	workers.Wait()
}

func (s *Server) scheduleConversationExtracts(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := s.db.QueueExpiredConversationExtracts(ctx, s.agentID, s.cfg.ConversationIdleTimeout); err != nil && ctx.Err() == nil {
			slog.Warn("queue conversation extraction", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) runJobQueue(ctx context.Context, kind store.JobKind, run func(context.Context, int64) error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		ran, err := s.db.RunNextJob(ctx, kind, s.agentID, run)
		if err == nil && ran {
			continue
		}
		if err != nil && ctx.Err() == nil {
			slog.Warn("background job", "kind", kind, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) retryJob(kind store.JobKind, enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !enabled {
			http.Error(w, "后台处理未配置", http.StatusServiceUnavailable)
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var ok bool
		if kind == store.ConversationJob {
			userID, userErr := s.db.UserIDByUsername(r.Context(), userFrom(r))
			if userErr != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			ok, err = s.db.RetryConversationExtract(r.Context(), userID, id)
		} else {
			ok, err = s.db.RetryJob(r.Context(), kind, s.agentID, id)
		}
		if err != nil {
			slog.Error("retry job", "kind", kind, "id", id, "err", err)
			http.Error(w, "重试请求失败", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "任务不存在或当前状态不可重试", http.StatusConflict)
			return
		}
		writeJSON(w, map[string]string{"status": "pending"})
	}
}
