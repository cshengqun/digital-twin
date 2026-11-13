package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

func (s *Server) memoryOwner(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ok, err := s.db.IsAgentOwner(r.Context(), s.agentID, userFrom(r))
		if err != nil {
			http.Error(w, "读取账户失败", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *Server) listMemories(w http.ResponseWriter, r *http.Request) {
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	items, err := s.db.ListMemories(r.Context(), s.agentID, strings.TrimSpace(r.URL.Query().Get("q")), before)
	if err != nil {
		slog.Error("list memories", "err", err)
		http.Error(w, "读取记忆失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, items)
}

func (s *Server) getMemory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	item, err := s.db.GetMemory(r.Context(), s.agentID, id)
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "读取记忆失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, item)
}

func (s *Server) editMemory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Revision int64  `json:"revision"`
		Text     string `json:"text"`
		Status   string `json:"status"`
	}
	reqBody := http.MaxBytesReader(w, r.Body, 16<<10)
	if err != nil || id <= 0 || json.NewDecoder(reqBody).Decode(&req) != nil || req.Revision < 1 {
		http.Error(w, "请求格式错误", http.StatusBadRequest)
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if (req.Status != "active" && req.Status != "archived") ||
		(req.Status == "active" && (req.Text == "" || utf8.RuneCountInString(req.Text) > 2000)) {
		http.Error(w, "记忆正文需为 1 至 2000 字", http.StatusBadRequest)
		return
	}
	item, err := s.db.EditMemory(r.Context(), s.agentID, id, req.Revision, req.Text, req.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "记忆版本已变化，请重新打开后再试", http.StatusConflict)
		return
	}
	if err != nil {
		slog.Error("edit memory", "id", id, "err", err)
		http.Error(w, "保存记忆失败", http.StatusInternalServerError)
		return
	}
	writeJSON(w, item)
}

func (s *Server) readMemorySource(w http.ResponseWriter, r *http.Request) {
	// Parse the saved source reference so older answers continue opening their
	// original source after a memory is edited or archived.
	parts := strings.Split(r.URL.Query().Get("ref"), ":")
	if len(parts) < 2 || len(parts) > 3 {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}

	switch parts[0] {
	case "journal":
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		content, date, err := s.db.JournalSource(r.Context(), s.agentID, id)
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "读取日记原文失败", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"title": "日记原文", "date": date, "content": content})
	case "conversation":
		var until int64
		if len(parts) == 3 {
			until, err = strconv.ParseInt(parts[2], 10, 64)
			if err != nil || until <= 0 {
				http.NotFound(w, r)
				return
			}
		}
		userID, err := s.db.UserIDByUsername(r.Context(), userFrom(r))
		if err != nil {
			http.Error(w, "读取账户失败", http.StatusUnauthorized)
			return
		}
		messages, err := s.db.ConversationSource(r.Context(), s.agentID, userID, id, until)
		if err != nil {
			http.Error(w, "读取对话原文失败", http.StatusInternalServerError)
			return
		}
		if len(messages) == 0 {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{"title": "对话原文", "messages": messages})
	default:
		http.NotFound(w, r)
	}
}
