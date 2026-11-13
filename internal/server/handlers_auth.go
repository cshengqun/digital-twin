package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"digital-twin/internal/session"
	"digital-twin/internal/store"
)

// loginRateLimiter 基于 Redis 的登录失败限流：key = login:{ip}:{username}
// 窗口内超过上限则锁定 LoginLockout 时长（防公网爆破；用户名维度避免多人共享 IP 误伤）。
type loginRateLimiter struct {
	rdb    *redis.Client
	limit  int
	window time.Duration
	lock   time.Duration
}

func (l *loginRateLimiter) allow(ip, username string) (bool, time.Duration) {
	key := "login:" + ip + ":" + username
	n, _ := l.rdb.Incr(context.Background(), key).Result()
	if n == 1 {
		l.rdb.Expire(context.Background(), key, l.window)
		return true, 0
	}
	if n > int64(l.limit) {
		l.rdb.Expire(context.Background(), key, l.lock) // 超限：延长过期形成锁定窗口
		return false, l.lock
	}
	return true, 0
}

// ---------- 认证处理器 ----------

type authHandler struct {
	db     *store.DB
	sm     *session.Manager
	lim    *loginRateLimiter
	cookie string
	ttl    time.Duration
}

func (h *authHandler) loginPage(w http.ResponseWriter, r *http.Request) {
	servePage(w, "web/login.html")
}

func (h *authHandler) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	ok, wait := h.lim.allow(r.RemoteAddr, req.Username)
	if !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
		http.Error(w, "too many attempts, try later", http.StatusTooManyRequests)
		return
	}

	valid, err := h.db.VerifyPassword(r.Context(), req.Username, req.Password)
	if err != nil {
		slog.Error("verify password", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !valid {
		slog.Warn("login failed", "user", req.Username, "ip", r.RemoteAddr) // 审计
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	// 登录成功：重新生成 session id（防会话固定）
	id, err := h.sm.Create(r.Context(), req.Username)
	if err != nil {
		slog.Error("session create", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     h.cookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil, // dev 无 TLS 时允许；prod 走 HTTPS 自动 Secure
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(h.ttl.Seconds()),
	})
	slog.Info("login ok", "user", req.Username, "ip", r.RemoteAddr)
	writeJSON(w, map[string]string{"ok": "true"})
}

func (h *authHandler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(h.cookie); err == nil {
		h.sm.Delete(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: h.cookie, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, map[string]string{"ok": "true"})
}
