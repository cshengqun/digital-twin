package impl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"digital-twin/internal/tools"
)

// WebFetchSchema 网页抓取工具：给定 URL 获取正文文本。
const WebFetchSchema = `{
  "type": "object",
  "properties": {
    "url": {"type": "string", "description": "要抓取的网页 URL（http/https）"}
  },
  "required": ["url"]
}`

var (
	scriptTag = regexp.MustCompile(`(?is)<(?:script|style|noscript)\b[^>]*>.*?</(?:script|style|noscript)>`)
	htmlTag   = regexp.MustCompile(`(?is)<[^>]+>`)
	multiWS   = regexp.MustCompile(`\s+`)
)

// NewWebFetch 抓取网页正文（只读、幂等）。含 SSRF 防护：拒绝内网/保留地址。
func NewWebFetch() tools.Tool {
	return tools.Tool{
		Name:        "web_fetch",
		Description: "抓取指定网页的正文文本（去掉脚本和样式，保留文字内容）。适合需要获取网页内容、查看链接指向的信息时使用。",
		Parameters:  json.RawMessage(WebFetchSchema),
		Handler:     webFetchHandler,
	}
}

func webFetchHandler(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("参数解析失败：%v", err)
	}
	u, err := url.Parse(strings.TrimSpace(in.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("url 必须是 http/https 链接")
	}
	if isPrivateHost(u.Hostname()) {
		return "", fmt.Errorf("拒绝访问内网或保留地址")
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; DigitalTwin/1.0)")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return "", fmt.Errorf("读取失败：%w", err)
	}
	return cleanHTML(string(body)), nil
}

// cleanHTML 去除脚本/样式/标签并压缩空白（html.UnescapeString 处理实体）。
func cleanHTML(s string) string {
	s = scriptTag.ReplaceAllString(s, " ")
	s = htmlTag.ReplaceAllString(s, " ")
	s = htmlUnescape(s)
	s = multiWS.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func htmlUnescape(s string) string {
	r := strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ",
	)
	return r.Replace(s)
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
