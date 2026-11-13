package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"digital-twin/internal/rag"
	"digital-twin/internal/store"
)

// journalHandler 每日日志喂养 + 人格画像（Profile/State）。
type journalHandler struct {
	db        *store.DB
	agentID   int64
	ownerDesc string
	extractor *rag.MemoryExtractor
}

type journalRequest struct {
	Content string `json:"content"`
	Source  string `json:"source,omitempty"` // journal | correction
}

// submitJournal 写日志并触发异步消化（抽取三类记忆 + 重建画像）。
func (h *journalHandler) submitJournal(w http.ResponseWriter, r *http.Request) {
	var req journalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		http.Error(w, "empty content", http.StatusBadRequest)
		return
	}
	if len([]rune(req.Content)) > 20000 {
		http.Error(w, "content too long", http.StatusRequestEntityTooLarge)
		return
	}
	source := req.Source
	if source != "correction" && source != "file" {
		source = "journal"
	}
	h.saveJournal(w, r, req.Content, source)
}

// uploadJournalFile 上传 TXT 日志文件（multipart 字段名 file，≤ 2MB）。
func (h *journalHandler) uploadJournalFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		http.Error(w, "上传失败：文件过大或格式错误", http.StatusBadRequest)
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "缺少 file 字段", http.StatusBadRequest)
		return
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 2<<20))
	if err != nil {
		http.Error(w, "读取失败", http.StatusBadRequest)
		return
	}
	content := strings.TrimSpace(string(b))
	if content == "" {
		http.Error(w, "文件内容为空", http.StatusBadRequest)
		return
	}
	h.saveJournal(w, r, content, "file")
}

func (h *journalHandler) saveJournal(w http.ResponseWriter, r *http.Request, content, source string) {
	id, err := h.db.InsertJournal(r.Context(), h.agentID, content, source)
	if err != nil {
		slog.Error("insert journal", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	TriggerJournalDigest(h.extractor, h.db, h.agentID, h.ownerDesc, id)
	writeJSON(w, map[string]any{"journal_id": id, "status": "digesting"})
}

// getProfile 返回当前人设画像（Profile + State）。
func (h *journalHandler) getProfile(w http.ResponseWriter, r *http.Request) {
	profileJSON, stateJSON, err := h.db.GetAgentProfile(r.Context(), h.agentID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"profile": store.ScanJSON(profileJSON),
		"state":   store.ScanJSON(stateJSON),
	})
}

// rebuildProfile 手动重建画像（同步：一次 LLM 调用，秒级返回）。
func (h *journalHandler) rebuildProfile(w http.ResponseWriter, r *http.Request) {
	if h.extractor == nil {
		http.Error(w, "LLM 未配置", http.StatusServiceUnavailable)
		return
	}
	if err := h.extractor.BuildProfile(r.Context(), h.agentID, h.ownerDesc); err != nil {
		slog.Warn("rebuild profile", "err", err)
		http.Error(w, "画像重建失败："+err.Error(), http.StatusInternalServerError)
		return
	}
	profileJSON, stateJSON, err := h.db.GetAgentProfile(r.Context(), h.agentID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"profile": store.ScanJSON(profileJSON),
		"state":   store.ScanJSON(stateJSON),
	})
}
