package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"digital-twin/internal/rag"
	"digital-twin/internal/store"

	"github.com/jackc/pgx/v5"
)

// knowledgeHandler 提供知识库管理 API：上传 TXT/HTML、导入 URL、列表、删除。
// 入库为异步任务：接口立即返回，列表里的 status 展示进度（pending/indexing/ready/failed）。
type knowledgeHandler struct {
	db      *store.DB
	indexer *rag.Indexer
	agentID int64

	maxUploadMB int64
	httpClient  *http.Client
}

func newKnowledgeHandler(db *store.DB, indexer *rag.Indexer, agentID int64) *knowledgeHandler {
	return &knowledgeHandler{
		db:          db,
		indexer:     indexer,
		agentID:     agentID,
		maxUploadMB: 10,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}
}

type knowledgeItem struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	SourceType string `json:"source_type"`
	SourceURL  string `json:"source_url"`
	Status     string `json:"status"`
	ErrorMsg   string `json:"error_msg"`
	ChunkCount int    `json:"chunk_count"`
	CreatedAt  string `json:"created_at"`
}

// importFile POST /api/knowledge/import（multipart：file + 可选 title）
func (h *knowledgeHandler) importFile(w http.ResponseWriter, r *http.Request) {
	if h.indexer == nil {
		http.Error(w, "知识入库未配置", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseMultipartForm(h.maxUploadMB << 20); err != nil {
		http.Error(w, "文件过大或格式错误", http.StatusBadRequest)
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "缺少 file 字段", http.StatusBadRequest)
		return
	}
	defer f.Close()
	if hdr.Size > h.maxUploadMB<<20 {
		http.Error(w, "文件超过 10MB 限制", http.StatusBadRequest)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(f, h.maxUploadMB<<20))
	if err != nil {
		http.Error(w, "读取文件失败", http.StatusBadRequest)
		return
	}

	kind := strings.ToLower(strings.TrimPrefix(filepath.Ext(hdr.Filename), "."))
	switch kind {
	case "txt", "text":
		kind = "txt"
	case "html", "htm":
		kind = "html_file"
	case "md", "markdown":
		kind = "md"
	default:
		http.Error(w, "仅支持 .txt / .md / .html 文件", http.StatusBadRequest)
		return
	}
	title := r.FormValue("title")
	if strings.TrimSpace(title) == "" {
		title = hdr.Filename
	}

	docID, err := h.db.InsertKnowledgeDoc(r.Context(), h.agentID, kind, "", title, string(raw))
	if err != nil {
		slog.Error("insert doc", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"id": docID, "status": "pending"})
}

// importURL POST /api/knowledge/import-url {"url": "...", "title": "..."}
func (h *knowledgeHandler) importURL(w http.ResponseWriter, r *http.Request) {
	if h.indexer == nil {
		http.Error(w, "知识入库未配置", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		http.Error(w, "仅支持 http/https URL", http.StatusBadRequest)
		return
	}
	if isPrivateHost(u.Hostname()) {
		http.Error(w, "不允许访问内网地址", http.StatusBadRequest)
		return
	}

	resp, err := h.httpClient.Get(u.String())
	if err != nil {
		http.Error(w, "抓取 URL 失败: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("目标返回 HTTP %d", resp.StatusCode), http.StatusBadGateway)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, h.maxUploadMB<<20))
	if err != nil {
		http.Error(w, "读取内容失败", http.StatusBadGateway)
		return
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = u.Host + u.Path
	}
	docID, err := h.db.InsertKnowledgeDoc(r.Context(), h.agentID, "html_url", u.String(), title, string(raw))
	if err != nil {
		slog.Error("insert doc", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"id": docID, "status": "pending"})
}

// list GET /api/knowledge
func (h *knowledgeHandler) list(w http.ResponseWriter, r *http.Request) {
	docs, err := h.db.ListKnowledgeDocs(r.Context(), h.agentID)
	if err != nil {
		slog.Error("list docs", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]knowledgeItem, 0, len(docs))
	for _, d := range docs {
		out = append(out, knowledgeItem{
			ID:         d.ID,
			Title:      d.Title,
			SourceType: d.SourceType,
			SourceURL:  d.SourceURL,
			Status:     d.Status,
			ErrorMsg:   d.ErrorMsg,
			ChunkCount: d.ChunkCount,
			CreatedAt:  d.CreatedAt.Format("01-02 15:04"),
		})
	}
	writeJSON(w, out)
}

// delete DELETE /api/knowledge/{id}
func (h *knowledgeHandler) delete(w http.ResponseWriter, r *http.Request) {
	if h.indexer == nil {
		http.Error(w, "知识入库未配置", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	err = h.indexer.DeleteDoc(r.Context(), h.agentID, id)
	switch {
	case errors.Is(err, store.ErrKnowledgeBusy):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, pgx.ErrNoRows):
		http.Error(w, "文档不存在", http.StatusNotFound)
	case err != nil:
		slog.Error("delete doc", "id", id, "err", err)
		http.Error(w, "删除失败", http.StatusInternalServerError)
	default:
		writeJSON(w, map[string]any{"ok": true})
	}
}

// isPrivateHost 阻止 SSRF：拒绝 localhost 与内网/保留地址。
func isPrivateHost(host string) bool {
	ips, err := net.LookupIP(host)
	if err != nil {
		return true // 解析失败直接拒绝
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return true
		}
	}
	return false
}
