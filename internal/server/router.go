package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"digital-twin/internal/config"
	"digital-twin/internal/llm"
	"digital-twin/internal/rag"
	"digital-twin/internal/session"
	"digital-twin/internal/store"
	"digital-twin/internal/tools"
	"digital-twin/internal/tools/impl"
)

const cookieName = "twin_sid"

const defaultAgentID = 1 // 单用户架构：默认分身

type Server struct {
	cfg            *config.Config
	db             *store.DB
	sm             *session.Manager
	llm            llm.Client
	ret            *rag.Retriever
	indexer        *rag.Indexer
	extractor      *rag.MemoryExtractor
	weeklyReporter *rag.WeeklyReporter
	agentID        int64
	ownerDesc      string
}

func New(cfg *config.Config, db *store.DB, sm *session.Manager, lc llm.Client, ret *rag.Retriever, indexer *rag.Indexer, extractor *rag.MemoryExtractor, weeklyReporter *rag.WeeklyReporter) *Server {
	return &Server{cfg: cfg, db: db, sm: sm, llm: lc, ret: ret, indexer: indexer, extractor: extractor, weeklyReporter: weeklyReporter,
		agentID: defaultAgentID, ownerDesc: "你（系统账号本人）"}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	auth := &authHandler{
		db: s.db, sm: s.sm, cookie: cookieName, ttl: s.cfg.SessionTTL,
		lim: &loginRateLimiter{rdb: s.db.Redis, limit: s.cfg.LoginRateLimit,
			window: s.cfg.LoginRateWindow, lock: s.cfg.LoginLockout},
	}
	// 工具注册表（白名单）：Phase A 内置只读/幂等工具
	reg := tools.NewRegistry()
	reg.Register(impl.NewCalculator())
	reg.Register(impl.NewCurrentTime())
	reg.Register(impl.NewWebFetch())
	if s.cfg.TavilyAPIKey != "" { // 配置了 Tavily key 才注册全网搜索
		reg.Register(impl.NewWebSearch(s.cfg.TavilyAPIKey))
	}
	// 读取最近一期个人周报
	reg.Register(tools.Tool{
		Name:        "get_latest_weekly_report",
		Description: "获取分身主人最近一期个人周报（每周日晚自动生成）。当用户问'我上周怎么样''最近状态''周报''这周复盘'时使用。",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			row, err := s.db.GetLatestWeeklyReport(ctx, s.agentID)
			if err != nil {
				return "", fmt.Errorf("读取周报失败：%v", err)
			}
			return fmt.Sprintf("周报周期：%s ~ %s\n纳入分析记忆数：%d\n\n%s",
				row.WeekStart.Format("2006-01-02"),
				row.WeekEnd.Format("2006-01-02"),
				row.MemoryCount,
				string(row.ContentJSON)), nil
		},
	})

	// 工具循环用关闭深度思考的客户端（结构化 tool_calls 解析，秒级返回）
	var toolLLM llm.Client
	if s.cfg.ArkAPIKey != "" {
		toolLLM = llm.NewArkClientNoThink(s.cfg.ArkBaseURL, s.cfg.ArkAPIKey, s.cfg.ArkModelID)
	}

	chat := &chatHandler{
		db: s.db, llm: s.llm, cfg: s.cfg, model: modelName(s.llm),
		agentID: defaultAgentID, ownerDesc: "你（系统账号本人）",
		ret: s.ret, extractor: s.extractor,
		tools: reg, toolLLM: toolLLM,
	}
	knowledge := newKnowledgeHandler(s.db, s.indexer, defaultAgentID)
	journal := &journalHandler{db: s.db, agentID: defaultAgentID, ownerDesc: "你（系统账号本人）", extractor: s.extractor}
	blog := &blogHandler{db: s.db, agentID: defaultAgentID, extractor: s.extractor, indexer: s.indexer,
		mediaDir: s.cfg.BlogMediaDir, importToken: s.cfg.BlogImportToken}

	protected := AuthRequired(s.sm, cookieName)

	// 公开路径：个人主页、登录页与认证接口
	mux.HandleFunc("GET /{$}", s.homePage)
	mux.HandleFunc("GET /login", auth.loginPage)
	mux.HandleFunc("POST /api/login", auth.login)
	mux.HandleFunc("GET /favicon.png", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "web/favicon.png")
	})
	// 页面与前端资源都要求每次向服务器校验（未变化则 304），避免浏览器按启发式规则沿用旧版 JS/CSS。
	mux.Handle("GET /assets/", noCache(http.StripPrefix("/assets/", http.FileServer(http.Dir("web/assets")))))
	mux.Handle("POST /api/utils/proxy", newWeutilProxyHandler(newWeutilProxyClient()))
	mux.Handle("POST /api/weutil/proxy", newWeutilProxyHandler(newWeutilProxyClient()))
	mux.HandleFunc("GET /blogs", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/blogs/", http.StatusPermanentRedirect)
	})
	mux.HandleFunc("GET /blogs/{$}", s.blogsPage)
	mux.HandleFunc("GET /api/blogs", blog.listPublic)
	mux.HandleFunc("GET /api/blogs/{id}", blog.getPublic)
	mux.Handle("GET "+blogMediaURLPrefix, s.serveBlogMedia())
	// 飞书导入：Bearer Token 鉴权，未配置 token 时一律 404。
	mux.Handle("POST /api/blogs/import", http.HandlerFunc(blog.importPost))
	mux.HandleFunc("GET /twin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/twin/", http.StatusPermanentRedirect)
	})

	// 数字分身页面与其余 API 要求登录
	mux.Handle("GET /twin/{$}", protected(http.HandlerFunc(s.twinPage)))
	mux.Handle("POST /api/logout", protected(http.HandlerFunc(auth.logout)))
	mux.Handle("POST /api/chat", protected(http.HandlerFunc(chat.chat)))
	mux.Handle("GET /api/conversations", protected(http.HandlerFunc(chat.listConversations)))
	mux.Handle("POST /api/conversations/{id}/retry", protected(s.retryJob(store.ConversationJob, s.extractor != nil)))
	mux.Handle("GET /api/conversations/{id}/messages", protected(http.HandlerFunc(chat.conversationMessages)))

	// Phase 1 · 知识库
	mux.Handle("POST /api/knowledge/import", protected(http.HandlerFunc(knowledge.importFile)))
	mux.Handle("POST /api/knowledge/import-url", protected(http.HandlerFunc(knowledge.importURL)))
	mux.Handle("GET /api/knowledge", protected(http.HandlerFunc(knowledge.list)))
	mux.Handle("POST /api/knowledge/{id}/retry", protected(s.retryJob(store.KnowledgeJob, s.indexer != nil)))
	mux.Handle("DELETE /api/knowledge/{id}", protected(http.HandlerFunc(knowledge.delete)))

	mux.Handle("GET /api/memories", protected(s.memoryOwner(s.listMemories)))
	mux.Handle("GET /api/memories/{id}", protected(s.memoryOwner(s.getMemory)))
	mux.Handle("PATCH /api/memories/{id}", protected(s.memoryOwner(s.editMemory)))
	mux.Handle("GET /api/memories/source", protected(s.memoryOwner(s.readMemorySource)))
	mux.Handle("POST /api/memories/{id}/retry", protected(s.memoryOwner(s.retryJob(store.MemoryVectorJob, s.extractor != nil))))

	// 养成：日志喂养 + 人格画像
	mux.Handle("POST /api/journal", protected(http.HandlerFunc(journal.submitJournal)))
	mux.Handle("GET /api/journal", protected(http.HandlerFunc(journal.listJobs)))
	mux.Handle("POST /api/journal/{id}/retry", protected(s.retryJob(store.JournalJob, s.extractor != nil)))
	mux.Handle("POST /api/journal/file", protected(http.HandlerFunc(journal.uploadJournalFile)))
	mux.Handle("GET /api/agent/profile", protected(http.HandlerFunc(journal.getProfile)))
	mux.Handle("POST /api/agent/profile/rebuild", protected(http.HandlerFunc(journal.rebuildProfile)))

	// Blogs: public writing that feeds both long-term memory and the knowledge base.
	mux.Handle("POST /api/blogs", protected(http.HandlerFunc(blog.publish)))
	mux.Handle("PUT /api/blogs/{id}", protected(http.HandlerFunc(blog.update)))
	mux.Handle("POST /api/blogs/upload", protected(http.HandlerFunc(blog.uploadImage)))
	mux.Handle("GET /api/blogs/manage", protected(http.HandlerFunc(blog.listJobs)))
	mux.Handle("POST /api/blogs/{id}/retry", protected(s.retryJob(store.JournalJob, s.extractor != nil)))

	return Recovery(AccessLog(mux))
}

func (s *Server) homePage(w http.ResponseWriter, r *http.Request) {
	servePage(w, "web/index.html")
}

func (s *Server) twinPage(w http.ResponseWriter, r *http.Request) {
	servePage(w, "web/chat.html")
}

func (s *Server) blogsPage(w http.ResponseWriter, r *http.Request) {
	servePage(w, "web/blogs.html")
}

// ---------- 小工具 ----------

func servePage(w http.ResponseWriter, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

// noCache 让浏览器每次都带条件请求回来校验（Last-Modified → 304），而不是在本地缓存期内直接沿用旧文件。
func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func modelName(c llm.Client) string {
	if c == nil {
		return "unknown"
	}
	return "ark"
}
