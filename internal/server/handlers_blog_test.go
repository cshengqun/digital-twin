package server

import (
	"bytes"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"digital-twin/internal/config"
	"digital-twin/internal/store"
)

func TestNormalizeBlogFormat(t *testing.T) {
	cases := []struct{ in, def, want string }{
		{"markdown", store.BlogFormatText, store.BlogFormatMarkdown},
		{" Markdown ", store.BlogFormatText, store.BlogFormatMarkdown},
		{"text", store.BlogFormatMarkdown, store.BlogFormatText},
		{"", store.BlogFormatText, store.BlogFormatText},
		{"", store.BlogFormatMarkdown, store.BlogFormatMarkdown},
		{"html", store.BlogFormatText, store.BlogFormatText},
	}
	for _, c := range cases {
		if got := normalizeBlogFormat(c.in, c.def); got != c.want {
			t.Errorf("normalizeBlogFormat(%q, %q) = %q, want %q", c.in, c.def, got, c.want)
		}
	}
}

func TestBlogExcerpt(t *testing.T) {
	md := "# 标题\n\n这是**第一段**，带 `code` 和 [链接](https://example.com)。\n\n- 列表项一\n- 列表项二\n\n```go\nfmt.Println(1)\n```\n"
	got := blogExcerpt(md, store.BlogFormatMarkdown)
	for _, unwanted := range []string{"**", "`", "](", "# 标题", "```"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("excerpt still contains markdown %q: %q", unwanted, got)
		}
	}
	for _, wanted := range []string{"标题", "这是第一段", "列表项一", "fmt.Println(1)"} {
		if !strings.Contains(got, wanted) {
			t.Errorf("excerpt lost %q: %q", wanted, got)
		}
	}
	plain := blogExcerpt("  第一行  \n\n\n第二行\n", store.BlogFormatText)
	if plain != "第一行 第二行" {
		t.Errorf("plain excerpt = %q", plain)
	}
	long := strings.Repeat("字", blogExcerptRunes+50)
	if got := blogExcerpt(long, store.BlogFormatText); len([]rune(got)) != blogExcerptRunes+1 || !strings.HasSuffix(got, "…") {
		t.Errorf("excerpt should be truncated to %d runes + ellipsis, got %d", blogExcerptRunes, len([]rune(got)))
	}
}

func TestRewriteImageRefs(t *testing.T) {
	urls := map[string]string{
		"架构图.png":    "/media/blog/ab12/架构图.png",
		"shot-1.jpg": "/media/blog/ab12/shot-1.jpg",
	}
	content := "![](images/架构图.png)\n\n![shot](images/shot/shot-1.jpg \"截图\")\n\n<img src=\"x.png\">\n\n[外链](https://example.com/shot-1.jpg)\n\n[保留](https://example.com/other.png)\n\n![空格](images/my%20pic.png)\n\n![t](<images/shot-1.jpg>)"
	got := rewriteImageRefs(content, urls)
	for _, want := range []string{
		"![](/media/blog/ab12/架构图.png)",
		"![shot](/media/blog/ab12/shot-1.jpg)",
		"[外链](/media/blog/ab12/shot-1.jpg)",
		"[保留](https://example.com/other.png)",
		"![空格](images/my%20pic.png)",
		"![t](/media/blog/ab12/shot-1.jpg)",
		"<img src=\"x.png\">",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rewritten content missing %q:\n%s", want, got)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd.png": "passwd",
		`..\..\evil.jpg`:       "evil",
		"My Shot (1).PNG":      "My-Shot-1",
		"截图 2.png":             "截图-2",
		".hidden":              "hidden",
		"///":                  "image",
		"日本語.png":              "日本語",
	}
	for in, want := range cases {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDedupeName(t *testing.T) {
	seen := map[string]bool{}
	var got []string
	for _, n := range []string{"a.png", "a.png", "a-2.png", "a.png"} {
		n = dedupeName(n, seen)
		seen[n] = true
		got = append(got, n)
	}
	if strings.Join(got, ",") != "a.png,a-2.png,a-2-2.png,a-3.png" {
		t.Errorf("dedupe order = %v", got)
	}
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// importRequest 构造 multipart 导入请求：fields 为文本字段，files 的 key 为文件名。
func importRequest(t *testing.T, fields map[string]string, files map[string][]byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range files {
		part, _ := mw.CreateFormFile("images", name)
		_, _ = part.Write(data)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/blogs/import", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r
}

// importPost 落库需要数据库，这里只验证鉴权与配置校验在落库前的拦截。
func TestImportPostGuards(t *testing.T) {
	noToken := &blogHandler{mediaDir: t.TempDir()}
	for _, token := range []string{"", "Bearer wrong"} {
		r := importRequest(t, map[string]string{"title": "t", "content": "c"}, nil)
		r.Header.Set("Authorization", token)
		w := httptest.NewRecorder()
		noToken.importPost(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("token %q without configured secret: got %d, want 404", token, w.Code)
		}
	}

	withToken := &blogHandler{mediaDir: t.TempDir(), importToken: "secret"}
	r := importRequest(t, map[string]string{"title": "t", "content": "c"}, nil)
	r.Header.Set("Authorization", "Bearer wrong")
	w := httptest.NewRecorder()
	withToken.importPost(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("wrong token: got %d, want 404", w.Code)
	}

	// 鉴权通过但学习组件未配置 → 503。
	r = importRequest(t, map[string]string{"title": "t", "content": "c"}, nil)
	r.Header.Set("Authorization", "Bearer secret")
	w = httptest.NewRecorder()
	withToken.importPost(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("authorized request without learning configured: got %d, want 503", w.Code)
	}
}

func TestReadImportImages(t *testing.T) {
	build := func(name string, data []byte) map[string][]*multipart.FileHeader {
		r := importRequest(t, nil, map[string][]byte{name: data})
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		return r.MultipartForm.File
	}
	images, err := readImportImages(build("架构 图.png", pngBytes(t)))
	if err != nil || len(images) != 1 || images[0].name != "架构-图.png" {
		t.Fatalf("valid png: images=%+v err=%v", images, err)
	}
	if _, err := readImportImages(build("evil.svg", []byte("<svg onload=alert(1)></svg>"))); err != errBlogBadType {
		t.Errorf("svg accepted: err=%v", err)
	}
	if _, err := readImportImages(build("x.png", []byte("not an image"))); err != errBlogBadType {
		t.Errorf("non-image accepted: err=%v", err)
	}
	if images, err := readImportImages(map[string][]*multipart.FileHeader{}); err != nil || len(images) != 0 {
		t.Errorf("no files should be fine: images=%v err=%v", images, err)
	}
}

func TestServeBlogMedia(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), pngBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := (&Server{cfg: &config.Config{BlogMediaDir: dir}}).serveBlogMedia()
	cases := map[string]int{
		"/media/blog/pic.png":     http.StatusOK,
		"/media/blog/secret.txt":  http.StatusNotFound,
		"/media/blog/missing.png": http.StatusNotFound,
		"/media/blog/":            http.StatusNotFound,
	}
	for target, want := range cases {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		if w.Code != want {
			t.Errorf("GET %s = %d, want %d", target, w.Code, want)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/media/blog/pic.png", nil))
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q", cc)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestReadBlogRequest(t *testing.T) {
	h := &blogHandler{}
	long := strings.Repeat("字", blogMaxContentRunes+1)
	cases := []struct {
		name   string
		body   string
		status int
		title  string
		format string
	}{
		{name: "ok markdown", body: `{"title":" 标题 ","content":" 正文 ","format":"Markdown"}`, status: 0, title: "标题", format: store.BlogFormatMarkdown},
		{name: "format defaults to text", body: `{"title":"t","content":"c"}`, status: 0, title: "t", format: store.BlogFormatText},
		{name: "unknown format falls back", body: `{"title":"t","content":"c","format":"html"}`, status: 0, title: "t", format: store.BlogFormatText},
		{name: "missing content", body: `{"title":"t","content":"  "}`, status: http.StatusBadRequest},
		{name: "title too long", body: `{"title":"` + strings.Repeat("a", 161) + `","content":"c"}`, status: http.StatusRequestEntityTooLarge},
		{name: "content too long", body: `{"title":"t","content":"` + long + `"}`, status: http.StatusRequestEntityTooLarge},
		{name: "bad json", body: `{`, status: http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPut, "/api/blogs/1", strings.NewReader(c.body))
			w := httptest.NewRecorder()
			req, ok := h.readBlogRequest(w, r)
			if c.status == 0 {
				if !ok || req.Title != c.title || req.Format != c.format {
					t.Fatalf("got ok=%v title=%q format=%q", ok, req.Title, req.Format)
				}
				return
			}
			if ok || w.Code != c.status {
				t.Fatalf("want %d, got ok=%v code=%d body=%s", c.status, ok, w.Code, w.Body.String())
			}
		})
	}
}

func TestBlogPathID(t *testing.T) {
	for _, in := range []string{"0", "-1", "abc", ""} {
		r := httptest.NewRequest(http.MethodPut, "/api/blogs/"+in, nil)
		r.SetPathValue("id", in)
		w := httptest.NewRecorder()
		if _, ok := blogPathID(w, r); ok || w.Code != http.StatusBadRequest {
			t.Fatalf("id %q: want 400", in)
		}
	}
	r := httptest.NewRequest(http.MethodPut, "/api/blogs/12", nil)
	r.SetPathValue("id", "12")
	if id, ok := blogPathID(httptest.NewRecorder(), r); !ok || id != 12 {
		t.Fatalf("want 12, got %d ok=%v", id, ok)
	}
}
