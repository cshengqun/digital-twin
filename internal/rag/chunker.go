// Package rag 提供知识库入库与检索的纯 Go 实现：
// 文本解析（TXT/HTML）→ 分块 → 向量化 → Qdrant；以及混合检索（PG 关键词 + Qdrant 向量 + RRF）。
package rag

import (
	"bytes"
	"strings"

	"github.com/gomarkdown/markdown"
	"golang.org/x/net/html"

	"digital-twin/internal/store"
)

// ChunkText 把纯文本切成有重叠的块。
// 优先按段落边界切；段落超长时在标点/空格处切分。
func ChunkText(text string, opt ChunkOptions) []store.Chunk {
	if opt.MaxLen <= 0 {
		opt.MaxLen = 500
	}
	if opt.Overlap < 0 {
		opt.Overlap = 0
	}
	if opt.MaxBatch <= 0 {
		opt.MaxBatch = 2000
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	// 第一遍：按段落（换行）合并，段内若超长则二次切
	paras := splitParagraphs(text)

	var chunks []store.Chunk
	var buf strings.Builder

	flush := func() {
		if s := strings.TrimSpace(buf.String()); s != "" {
			chunks = append(chunks, store.Chunk{Index: len(chunks), Text: s})
		}
		buf.Reset()
	}

	for _, p := range paras {
		// 段本身不超长 → 尝试追加进当前块
		if len([]rune(p)) <= opt.MaxLen {
			if buf.Len() > 0 && len([]rune(buf.String()))+len([]rune(p))+1 > opt.MaxLen {
				flush()
			}
			if buf.Len() > 0 {
				buf.WriteByte('\n')
			}
			buf.WriteString(p)
			if len([]rune(buf.String())) >= opt.MaxLen {
				flush()
			}
			continue
		}
		// 超长段：先冲刷当前缓冲，再按句子切
		flush()
		for _, sub := range splitLongParagraph(p, opt.MaxLen, opt.Overlap) {
			chunks = append(chunks, store.Chunk{Index: len(chunks), Text: sub})
		}
	}
	flush()

	if len(chunks) > opt.MaxBatch {
		chunks = chunks[:opt.MaxBatch]
	}
	return chunks
}

// ParseText 按文档类型把原始内容解析成可分块的纯文本。
// kind: "txt" | "html" | "md"（自动按内容探测兜底）。
func ParseText(raw []byte, kind string) string {
	switch strings.ToLower(kind) {
	case "html", "htm":
		return htmlToText(raw)
	case "md", "markdown":
		// Markdown → HTML → 复用正文提取：标题/列表/表格/代码块全部保真
		return htmlToText(markdown.ToHTML(raw, nil, nil))
	default:
		return normalizeSpace(string(raw))
	}
}

// htmlToText 用标准 html 解析器提取正文：去除 script/style/noscript/注释，
// 块级元素后追加换行，保证段落边界可读。
func htmlToText(raw []byte) string {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		// 解析失败时退回纯文本，避免整篇丢弃
		return normalizeSpace(string(raw))
	}

	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(n.Data)
		case html.ElementNode:
			switch n.Data {
			case "script", "style", "noscript", "template", "iframe", "svg":
				return // 跳过非正文子树
			case "p", "div", "section", "article", "li", "tr", "br", "h1", "h2", "h3", "h4", "h5", "h6",
				"blockquote", "pre", "table", "ul", "ol", "header", "footer":
				b.WriteByte('\n')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "p", "div", "section", "article", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6",
				"blockquote", "pre", "table":
				b.WriteByte('\n')
			}
		}
	}
	walk(doc)
	return normalizeSpace(b.String())
}

// normalizeSpace 折叠连续空白为单个空格（保留换行折叠后的段落结构）。
func normalizeSpace(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	var out []string
	for _, ln := range lines {
		ln = strings.Join(strings.Fields(ln), " ")
		if strings.TrimSpace(ln) != "" {
			out = append(out, strings.TrimSpace(ln))
		}
	}
	return strings.Join(out, "\n")
}

// ChunkOptions 分块参数。
type ChunkOptions struct {
	MaxLen   int // 单块目标字数（含重叠）
	Overlap  int // 相邻块重叠字数
	MaxBatch int // 单文档最大块数（保护：超大文档截断）
}

func DefaultChunkOptions() ChunkOptions {
	return ChunkOptions{MaxLen: 500, Overlap: 50, MaxBatch: 2000}
}

// splitParagraphs 按换行拆段，并去掉空段。
func splitParagraphs(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// splitLongParagraph 在句号/问号/叹号/分号/空格处把超长段切成 ≤MaxLen 的块，并带重叠。
func splitLongParagraph(p string, maxLen, overlap int) []string {
	runes := []rune(p)
	var out []string
	start := 0
	for start < len(runes) {
		if len(runes)-start <= maxLen {
			out = append(out, string(runes[start:]))
			break
		}
		end := start + maxLen
		// 向前找最近的自然断点
		cut := end
		for i := end; i > start+maxLen/2; i-- {
			switch runes[i-1] {
			case '。', '！', '？', '；', '.', '!', '?', ';', ' ', '\n', '，', ',':
				cut = i
				goto found
			}
		}
	found:
		out = append(out, string(runes[start:cut]))
		start = cut - overlap
		if start < 0 {
			start = 0
		}
	}
	return out
}
