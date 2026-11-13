// 个人数字分身 Agent · 服务入口（Phase 0）
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"digital-twin/internal/config"
	"digital-twin/internal/embedding"
	"digital-twin/internal/llm"
	"digital-twin/internal/rag"
	"digital-twin/internal/server"
	"digital-twin/internal/session"
	"digital-twin/internal/store"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()
	db, err := store.New(ctx, cfg.PostgresDSN, cfg.RedisAddr, cfg.RedisPass)
	if err != nil {
		slog.Error("store connect", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}
	if err := db.EnsurePartitions(ctx); err != nil {
		slog.Error("ensure partitions", "err", err)
		os.Exit(1)
	}
	if err := db.EnsureAdminUser(ctx, cfg.AdminUsername, cfg.AdminPassword); err != nil {
		slog.Error("ensure admin", "err", err)
		os.Exit(1)
	}

	sm := session.NewManager(db.Redis, cfg.SessionTTL, cfg.SessionIDBytes)

	var lc llm.Client
	var emb *embedding.Client
	var qd *rag.QdrantClient
	var ret *rag.Retriever
	var indexer *rag.Indexer
	var extractor *rag.MemoryExtractor
	var weeklyReporter *rag.WeeklyReporter

	if cfg.ArkAPIKey == "" {
		slog.Warn("ARK_API_KEY 未配置 —— 对话走 Mock 模式（检索/记忆/知识库暂不启用）")
		lc = llm.NewMockClient()
	} else {
		lc = llm.NewArkClient(cfg.ArkBaseURL, cfg.ArkAPIKey, cfg.ArkModelID)
		emb = embedding.New(cfg.ArkBaseURL, cfg.ArkAPIKey, cfg.EmbeddingModel)
		qd = rag.NewQdrant(cfg.QdrantAddr, "")
		ret = rag.NewRetriever(db, emb, qd)
		indexer = rag.NewIndexer(db, emb, qd)
		// 记忆抽取用关闭深度思考的客户端：结构化 JSON 任务不需要思考链，避免超长推理耗时
		extractor = rag.NewMemoryExtractor(db, llm.NewArkClientNoThink(cfg.ArkBaseURL, cfg.ArkAPIKey, cfg.ArkModelID), emb, qd)
		// 周报也用 NoThink 客户端（结构化输出）
		weeklyReporter = rag.NewWeeklyReporter(db, llm.NewArkClientNoThink(cfg.ArkBaseURL, cfg.ArkAPIKey, cfg.ArkModelID))
		slog.Info("llm + rag ready", "model", cfg.ArkModelID, "embedding", cfg.EmbeddingModel, "qdrant", cfg.QdrantAddr)
	}

	srv := server.New(cfg, db, sm, lc, ret, indexer, extractor, weeklyReporter)
	// 画像每天凌晨 02:30 自动重建（不随日志触发；含当天归档的对话记忆）
	srv.StartProfileRebuilder(ctx)
	// 每周日晚 20:00 自动生成个人周报
	srv.StartWeeklyReporter(ctx)
	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	runCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	jobsDone := make(chan struct{})
	go func() {
		defer close(jobsDone)
		srv.RunBackgroundJobs(runCtx)
	}()
	partitionsDone := make(chan struct{})
	go func() {
		defer close(partitionsDone)
		db.MaintainPartitions(runCtx)
	}()

	go func() {
		slog.Info("server started", "addr", cfg.Addr, "env", cfg.Env)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	<-runCtx.Done()

	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	<-partitionsDone
	<-jobsDone
	slog.Info("server stopped")
}
