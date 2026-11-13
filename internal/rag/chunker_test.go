package rag

import "testing"

func TestParseTextMarkdown(t *testing.T) {
	in := "# 标题一\n\n## 小节\n\n这是一个**加粗**和 `行内代码` 的段落。\n\n- 列表项一\n- 列表项二\n\n```go\npackage main\nfunc main() {}\n```\n\n[链接文字](https://example.com)\n\n> 引用内容\n"
	got := ParseText([]byte(in), "md")
	for _, want := range []string{"标题一", "小节", "加粗", "行内代码", "列表项一", "列表项二", "package main", "func main() {}", "链接文字", "引用内容"} {
		if !contains(got, want) {
			t.Fatalf("markdown 解析缺失 %q, got:\n%s", want, got)
		}
	}
	if contains(got, "https://example.com") {
		t.Fatalf("markdown 链接应提取文字而非 URL:\n%s", got)
	}
}

func TestParseTextTXT(t *testing.T) {
	in := "  第一行内容。\r\n\r\n  第二行，带多余空白。  \n"
	got := ParseText([]byte(in), "txt")
	want := "第一行内容。\n第二行，带多余空白。"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestParseTextHTML(t *testing.T) {
	in := `<html><head><title>t</title><style>body{}</style></head>
<body>
<h1>标题</h1>
<p>第一个段落，有一些<b>加粗</b>文字。</p>
<script>alert('x')</script>
<p>第二个段落。</p>
</body></html>`
	got := ParseText([]byte(in), "html")
	for _, want := range []string{"标题", "第一个段落，有一些加粗文字。", "第二个段落。"} {
		if !contains(got, want) {
			t.Fatalf("html 解析缺失 %q, got:\n%s", want, got)
		}
	}
	if contains(got, "alert") || contains(got, "body{}") {
		t.Fatalf("html 解析未剔除 script/style:\n%s", got)
	}
}

func TestChunkTextBasic(t *testing.T) {
	// 构造 3 段各 ~200 字 → 3 个块
	var p1, p2, p3 string
	for i := 0; i < 50; i++ {
		p1 += "第一段句子。"
		p2 += "第二段句子。"
		p3 += "第三段句子。"
	}
	text := p1 + "\n" + p2 + "\n" + p3
	chunks := ChunkText(text, ChunkOptions{MaxLen: 500, Overlap: 50})
	if len(chunks) != 3 {
		t.Fatalf("want 3 chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if c.Index != i {
			t.Fatalf("chunk index = %d, want %d", c.Index, i)
		}
		if len([]rune(c.Text)) > 500 {
			t.Fatalf("chunk %d 超长: %d", i, len([]rune(c.Text)))
		}
	}
}

func TestChunkTextLongParagraph(t *testing.T) {
	var long string
	for i := 0; i < 200; i++ {
		long += "这是一句用来测试超长段落切分的中文。"
	}
	chunks := ChunkText(long, ChunkOptions{MaxLen: 300, Overlap: 30})
	if len(chunks) < 2 {
		t.Fatalf("超长段应切出多块, got %d", len(chunks))
	}
	for _, c := range chunks {
		if len([]rune(c.Text)) > 300 {
			t.Fatalf("chunk 超长: %d", len([]rune(c.Text)))
		}
	}
	// 验证重叠：相邻块前 30 字应出现在前一块尾部附近（近似检查）
	joined := chunks[0].Text + chunks[1].Text
	if !contains(joined, "用来测试超长段落切分") {
		t.Fatalf("重叠/拼接异常: %q", joined[:min(60, len(joined))])
	}
}

func TestChunkTextEmpty(t *testing.T) {
	if c := ChunkText("   \n  ", DefaultChunkOptions()); c != nil {
		t.Fatalf("空文本应返回 nil, got %v", c)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
