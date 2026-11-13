package server

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"digital-twin/internal/session"
)

type ctxKey int

const ctxUserKey ctxKey = iota

// userFrom 从请求上下文取当前登录用户名。
func userFrom(r *http.Request) string {
	if v, ok := r.Context().Value(ctxUserKey).(string); ok {
		return v
	}
	return ""
}

// withUser 把登录用户名注入上下文。
func withUser(r *http.Request, username string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxUserKey, username))
}

// ---------- 中间件 ----------

// Recovery 兜底 panic，避免单请求崩溃整个进程。
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("panic", "err", err, "stack", string(debug.Stack()))
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// AccessLog 记录每个请求的方法/路径/耗时/状态（审计与排障）。
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		slog.Info("http",
			"method", r.Method, "path", r.URL.Path, "status", sw.status,
			"dur_ms", time.Since(start).Milliseconds(), "ip", r.RemoteAddr)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Flush 透传 SSE 流式刷新能力（缺了它对话流式会断）。
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// AuthRequired 校验会话 cookie；未登录时：
//   - API 请求（/api/...）返回 401 JSON/文本，由前端 fetch 自行跳转；
//   - 页面导航（浏览器直接访问）302 跳转到 /login，避免用户看到裸 "unauthorized"。
func AuthRequired(sm *session.Manager, cookieName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(cookieName)
			username, ok := "", false
			if err == nil {
				username, ok = sm.Get(r.Context(), c.Value)
			}
			if !ok {
				if isAPIPath(r.URL.Path) {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				 // 带上当前路径，登录后可跳回原页（简单实现，登录页默认跳 /）
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			next.ServeHTTP(w, withUser(r, username))
		})
	}
}

// isAPIPath 判断是否为 API 接口（未登录时返回 401 而非 302）。
func isAPIPath(path string) bool {
	// /api/login 本身在中间件外，不受影响；这里只兜底其它受保护 API
	return len(path) >= 5 && path[:5] == "/api/"
}
