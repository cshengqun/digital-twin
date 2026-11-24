package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	pathpkg "path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"

	"digital-twin/internal/rag"
	"digital-twin/internal/store"
)

const (
	blogMaxBodyBytes    = 4 << 20  // JSON 发布请求体上限
	blogMaxContentRunes = 200000   // 正文上限（字符）
	blogExcerptRunes    = 300      // 列表摘要长度
	blogMaxImageBytes   = 8 << 20  // 单张图片上限
	blogMaxImportBytes  = 40 << 20 // 飞书导入请求体上限（正文 + 多图）
	blogMediaURLPrefix  = "/media/blog/"
)

// blogImageExt 按文件内容嗅探出的 MIME 类型决定扩展名；不放行 SVG（可内嵌脚本）。
var blogImageExt = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

type blogHandler struct {
	db          *store.DB
	agentID     int64
	extractor   *rag.MemoryExtractor
	indexer     *rag.Indexer
	mediaDir    string // 博客图片落盘目录（相对工作目录）
	importToken string // 非空时开启 POST /api/blogs/import（Bearer Token）
}

type blogRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Format  string `json:"format"` // text | markdown；缺省按 text 处理以兼容旧页面
}

// normalizeBlogFormat 把任意输入归一到合法值，def 为缺省值。
func normalizeBlogFormat(format, def string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case store.BlogFormatMarkdown:
		return store.BlogFormatMarkdown
	case store.BlogFormatText:
		return store.BlogFormatText
	}
	return def
}

// blogInputError 校验标题与正文，返回 HTTP 错误文案与状态码；ok 为 true 时通过。
func blogInputError(title, content string) (string, int, bool) {
	switch {
	case title == "" || content == "":
		return "title and content are required", http.StatusBadRequest, false
	case len([]rune(title)) > 160:
		return "title is too long", http.StatusRequestEntityTooLarge, false
	case len([]rune(content)) > blogMaxContentRunes:
		return "content is too long", http.StatusRequestEntityTooLarge, false
	}
	return "", 0, true
}

func (h *blogHandler) publish(w http.ResponseWriter, r *http.Request) {
	if h.extractor == nil || h.indexer == nil {
		http.Error(w, "Blog learning is not configured", http.StatusServiceUnavailable)
		return
	}
	req, ok := h.readBlogRequest(w, r)
	if !ok {
		return
	}
	blogID, knowledgeID, err := h.db.InsertBlog(r.Context(), h.agentID, req.Title, req.Content, req.Format)
	if err != nil {
		slog.Error("publish blog", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"blog_id": blogID, "knowledge_id": knowledgeID, "status": "pending"})
}

// readBlogRequest 解析并校验 JSON 发布请求；format 缺省按 text 以兼容旧页面。
func (h *blogHandler) readBlogRequest(w http.ResponseWriter, r *http.Request) (blogRequest, bool) {
	var req blogRequest
	r.Body = http.MaxBytesReader(w, r.Body, blogMaxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return req, false
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Content = strings.TrimSpace(req.Content)
	if msg, status, ok := blogInputError(req.Title, req.Content); !ok {
		http.Error(w, msg, status)
		return req, false
	}
	req.Format = normalizeBlogFormat(req.Format, store.BlogFormatText)
	return req, true
}

// update 覆盖已有文章（PUT /api/blogs/{id}，登录态）。成功后知识文档重新排队索引。
func (h *blogHandler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := blogPathID(w, r)
	if !ok {
		return
	}
	req, ok := h.readBlogRequest(w, r)
	if !ok {
		return
	}
	knowledgeID, err := h.db.UpdateBlog(r.Context(), h.agentID, id, req.Title, req.Content, req.Format)
	switch {
	case errors.Is(err, store.ErrBlogNotFound):
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	case errors.Is(err, store.ErrBlogBusy):
		http.Error(w, "blog is being processed, try again shortly", http.StatusConflict)
		return
	case err != nil:
		slog.Error("update blog", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Info("blog updated", "blog_id", id, "knowledge_id", knowledgeID, "chars", len([]rune(req.Content)))
	writeJSON(w, map[string]any{"blog_id": id, "knowledge_id": knowledgeID, "status": "pending",
		"url": "/blogs/?id=" + strconv.FormatInt(id, 10)})
}

// blogPathID 读取路径参数 {id}，非法时直接写 400。
func blogPathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func (h *blogHandler) listPublic(w http.ResponseWriter, r *http.Request) {
	items, err := h.db.ListPublicBlogs(r.Context(), h.agentID)
	if err != nil {
		slog.Error("list public blogs", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	for i := range items {
		items[i].Excerpt = blogExcerpt(items[i].Excerpt, items[i].Format)
	}
	writeJSON(w, items)
}

func (h *blogHandler) getPublic(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	post, err := h.db.GetPublicBlog(r.Context(), h.agentID, id)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "blog not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("get public blog", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, post)
}

func (h *blogHandler) listJobs(w http.ResponseWriter, r *http.Request) {
	items, err := h.db.ListBlogJobs(r.Context(), h.agentID)
	if err != nil {
		slog.Error("list blog jobs", "err", err)
		http.Error(w, "failed to load blog learning status", http.StatusInternalServerError)
		return
	}
	writeJSON(w, items)
}

// blogExcerpt 生成列表摘要：Markdown 先去掉标记，再折叠空白并截断。
func blogExcerpt(content, format string) string {
	text := content
	if format == store.BlogFormatMarkdown {
		text = rag.ParseText([]byte(content), "md")
	}
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, ln := range lines {
		if ln = strings.TrimSpace(ln); ln != "" {
			kept = append(kept, ln)
		}
	}
	text = strings.Join(kept, " ")
	if runes := []rune(text); len(runes) > blogExcerptRunes {
		return strings.TrimSpace(string(runes[:blogExcerptRunes])) + "…"
	}
	return text
}

// ---------- 图片 ----------

// uploadImage 编辑器里拖拽/粘贴/选择图片时调用（multipart 字段 file），返回可直接写进 Markdown 的 URL。
func (h *blogHandler) uploadImage(w http.ResponseWriter, r *http.Request) {
	if h.mediaDir == "" {
		http.Error(w, "image upload is not configured", http.StatusServiceUnavailable)
		return
	}
	data, err := readImageUpload(w, r, blogMaxImageBytes)
	if err != nil {
		http.Error(w, err.Error(), blogUploadStatus(err))
		return
	}
	name, err := randomHex(8)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	ext := blogImageExt[http.DetectContentType(data)]
	if err := os.MkdirAll(h.mediaDir, 0o755); err == nil {
		err = os.WriteFile(path.Join(h.mediaDir, name+ext), data, 0o644)
	}
	if err != nil {
		slog.Error("save blog image", "err", err)
		http.Error(w, "failed to store image", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"url": blogMediaURLPrefix + name + ext, "name": name + ext})
}

// readImageUpload 读取 multipart 里的 file 字段并校验大小与图片类型。
func readImageUpload(w http.ResponseWriter, r *http.Request, maxBytes int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+(64<<10))
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		return nil, errBlogImageTooLarge
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		return nil, errBlogNoFile
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes {
		return nil, errBlogImageTooLarge
	}
	if len(data) == 0 {
		return nil, errBlogNoFile
	}
	if _, ok := blogImageExt[http.DetectContentType(data)]; !ok {
		return nil, errBlogBadType
	}
	return data, nil
}

var (
	errBlogNoFile         = errors.New("missing file field")
	errBlogImageTooLarge  = errors.New("image is too large (max 8MB)")
	errBlogBadType        = errors.New("only PNG, JPEG, GIF and WebP images are allowed")
	errBlogImportDisabled = errors.New("import is not enabled")
)

func blogUploadStatus(err error) int {
	switch err {
	case nil:
		return http.StatusOK
	case errBlogImageTooLarge:
		return http.StatusRequestEntityTooLarge
	case errBlogImportDisabled:
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ---------- 飞书文档导入 ----------

type blogImportImage struct {
	name string
	data []byte
}

// importPost 供本机脚本调用：Bearer Token 鉴权，multipart 字段 title / content / format，
// 可选 id（覆盖更新已有文章），以及任意命名的文件字段作为图片。正文里以图片文件名结尾的 Markdown 链接会被改写为站内 URL。
func (h *blogHandler) importPost(w http.ResponseWriter, r *http.Request) {
	got := []byte(strings.TrimSpace(r.Header.Get("Authorization")))
	if h.importToken == "" || subtle.ConstantTimeCompare(got, []byte("Bearer "+h.importToken)) != 1 {
		http.NotFound(w, r)
		return
	}
	if h.extractor == nil || h.indexer == nil || h.mediaDir == "" {
		http.Error(w, "blog import is not configured", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, blogMaxImportBytes)
	if err := r.ParseMultipartForm(blogMaxImageBytes); err != nil {
		http.Error(w, "import body is too large or malformed (max 40MB)", http.StatusRequestEntityTooLarge)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	content := strings.TrimSpace(r.FormValue("content"))
	format := normalizeBlogFormat(r.FormValue("format"), store.BlogFormatMarkdown)
	if msg, status, ok := blogInputError(title, content); !ok {
		http.Error(w, msg, status)
		return
	}
	// 可选字段 id：非空时覆盖更新已有文章，而不是新发一篇。
	updateID, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	images, err := readImportImages(r.MultipartForm.File)
	if err != nil {
		http.Error(w, err.Error(), blogUploadStatus(err))
		return
	}

	// 先把图片写到独立目录，正文改写成功并入库后目录才会被公开引用；入库失败则回滚目录。
	dir, err := randomHex(8)
	if err != nil {
		slog.Error("prepare blog import dir", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(images) > 0 {
		if err := os.MkdirAll(path.Join(h.mediaDir, dir), 0o755); err != nil {
			slog.Error("prepare blog import dir", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	for _, img := range images {
		if err := os.WriteFile(path.Join(h.mediaDir, dir, img.name), img.data, 0o644); err != nil {
			_ = os.RemoveAll(path.Join(h.mediaDir, dir))
			slog.Error("save blog import image", "err", err)
			http.Error(w, "failed to store images", http.StatusInternalServerError)
			return
		}
	}
	urls := make(map[string]string, len(images))
	for _, img := range images {
		urls[img.name] = blogMediaURLPrefix + dir + "/" + img.name
	}
	content = rewriteImageRefs(content, urls)

	rollback := func() {
		if len(images) > 0 {
			_ = os.RemoveAll(path.Join(h.mediaDir, dir))
		}
	}
	var blogID, knowledgeID int64
	action := "import blog"
	if updateID > 0 {
		action, blogID = "update blog", updateID
		knowledgeID, err = h.db.UpdateBlog(r.Context(), h.agentID, updateID, title, content, format)
	} else {
		blogID, knowledgeID, err = h.db.InsertBlog(r.Context(), h.agentID, title, content, format)
	}
	if err != nil {
		rollback()
		switch {
		case errors.Is(err, store.ErrBlogNotFound):
			http.Error(w, "blog not found", http.StatusNotFound)
		case errors.Is(err, store.ErrBlogBusy):
			http.Error(w, "blog is being processed, try again shortly", http.StatusConflict)
		default:
			slog.Error(action, "blog_id", updateID, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	uploaded := make([]map[string]string, 0, len(images))
	for _, img := range images {
		uploaded = append(uploaded, map[string]string{"name": img.name, "url": urls[img.name]})
	}
	slog.Info(action, "blog_id", blogID, "images", len(images), "chars", len([]rune(content)))
	writeJSON(w, map[string]any{
		"blog_id": blogID, "knowledge_id": knowledgeID, "status": "pending",
		"url": "/blogs/?id=" + strconv.FormatInt(blogID, 10), "images": uploaded,
	})
}

// readImportImages 读取 multipart 中所有文件字段，按内容嗅探类型并用安全文件名去重。
func readImportImages(files map[string][]*multipart.FileHeader) ([]blogImportImage, error) {
	// 固定字段顺序，保证同名文件的去重结果可复现。
	fields := make([]string, 0, len(files))
	for field := range files {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	var images []blogImportImage
	seen := map[string]bool{}
	var total int64
	for _, field := range fields {
		for _, fh := range files[field] {
			if fh.Size > blogMaxImageBytes {
				return nil, errBlogImageTooLarge
			}
			total += fh.Size
			if total > blogMaxImportBytes {
				return nil, errBlogImageTooLarge
			}
			file, err := fh.Open()
			if err != nil {
				return nil, errBlogNoFile
			}
			data, err := io.ReadAll(io.LimitReader(file, blogMaxImageBytes+1))
			file.Close()
			if err != nil || int64(len(data)) > blogMaxImageBytes {
				return nil, errBlogImageTooLarge
			}
			ext, ok := blogImageExt[http.DetectContentType(data)]
			if !ok {
				return nil, errBlogBadType
			}
			name := dedupeName(sanitizeFilename(fh.Filename)+ext, seen)
			seen[name] = true
			images = append(images, blogImportImage{name: name, data: data})
		}
	}
	return images, nil
}

// sanitizeFilename 去掉目录与扩展名，只保留字母、数字、连字符和 CJK，其余折叠为 "-"。
func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = pathpkg.Base(name)
	if dot := strings.LastIndex(name, "."); dot > 0 {
		name = name[:dot]
	}
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_':
			if !lastDash {
				b.WriteByte('-')
			}
			lastDash = true
		case unicode.Is(unicode.Han, r):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
			}
			lastDash = true
		}
		if b.Len() >= 120 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "image"
	}
	return out
}

// dedupeName 同名文件依次追加 -2、-3。
func dedupeName(name string, seen map[string]bool) string {
	if !seen[name] {
		return name
	}
	base, ext := name, ""
	if dot := strings.LastIndex(name, "."); dot > 0 {
		base, ext = name[:dot], name[dot:]
	}
	for i := 2; ; i++ {
		candidate := base + "-" + strconv.Itoa(i) + ext
		if !seen[candidate] {
			return candidate
		}
	}
}

// markdownLinkDest 匹配 ]( … ) 并捕获其中的地址（不含可选的尖括号与标题）。
var markdownLinkDest = regexp.MustCompile(`\]\(\s*<?([^\s<>)]+)>?[^)]*\)`)

// rewriteImageRefs 把 Markdown 链接/图片地址中"以某个已上传文件名结尾"的相对或临时路径，
// 改写为对应的站内 URL。例如 ![a](images/abc.png) → ![a](/media/blog/<dir>/abc.png)。
func rewriteImageRefs(content string, urls map[string]string) string {
	if len(urls) == 0 {
		return content
	}
	locs := markdownLinkDest.FindAllStringSubmatchIndex(content, -1)
	if len(locs) == 0 {
		return content
	}
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		// 整段替换 "](" 与 ")" 之间的内容，连尖括号和标题一起去掉，只留站内 URL。
		open, close := loc[0]+len("]("), loc[1]-len(")")
		raw := content[loc[2]:loc[3]]
		if target, ok := urls[linkBasename(raw)]; ok {
			b.WriteString(content[last:open])
			b.WriteString(target)
			last = close
		}
	}
	if last == 0 {
		return content
	}
	b.WriteString(content[last:])
	return b.String()
}

// linkBasename 取链接地址（已 URL 解码）的最后一段，忽略 query 与 fragment。
func linkBasename(raw string) string {
	p := raw
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		p = u.Path
	}
	if decoded, err := url.PathUnescape(p); err == nil {
		p = decoded
	}
	p = strings.TrimSuffix(p, "/")
	return pathpkg.Base(p)
}

// serveBlogMedia 以只读方式公开博客图片：仅放行图片扩展名，长缓存，不开目录列表。
func (s *Server) serveBlogMedia() http.Handler {
	fs := http.StripPrefix(blogMediaURLPrefix, http.FileServer(http.Dir(s.cfg.BlogMediaDir)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ext := strings.ToLower(pathpkg.Ext(pathpkg.Clean(r.URL.Path)))
		switch ext {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fs.ServeHTTP(w, r)
	})
}
