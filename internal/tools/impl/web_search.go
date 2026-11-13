package impl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"digital-twin/internal/tools"
)

// WebSearchSchema Tavily 全网搜索工具参数。
const WebSearchSchema = `{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "搜索关键词，与用户在找的主题直接相关"},
    "max_results": {"type": "integer", "description": "返回结果条数（1-10，默认 5）"}
  },
  "required": ["query"]
}`

// NewWebSearch 全网搜索（Tavily API，免费额度约 1000 次/月）。
// 返回每个结果条目的标题、链接和内容摘要。
func NewWebSearch(apiKey string) tools.Tool {
	return tools.Tool{
		Name:        "web_search",
		Description: "全网搜索，返回与关键词相关的网页标题、链接和内容摘要。适合需要查找最新信息、新闻、资料时使用。",
		Parameters:  json.RawMessage(WebSearchSchema),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Query      string `json:"query"`
				MaxResults int    `json:"max_results"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return "", fmt.Errorf("参数解析失败：%v", err)
			}
			in.Query = trimAll(in.Query)
			if in.Query == "" {
				return "", fmt.Errorf("query 不能为空")
			}
			if in.MaxResults <= 0 {
				in.MaxResults = 5
			}
			if in.MaxResults > 10 {
				in.MaxResults = 10
			}
			body, _ := json.Marshal(map[string]any{
				"api_key":      apiKey,
				"query":        in.Query,
				"max_results":  in.MaxResults,
				"search_depth": "basic",
			})
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(body))
			if err != nil {
				return "", err
			}
			req.Header.Set("Content-Type", "application/json")
			client := &http.Client{Timeout: 15 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				return "", fmt.Errorf("搜索请求失败：%w", err)
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
			if resp.StatusCode != http.StatusOK {
				return "", fmt.Errorf("Tavily HTTP %d：%s", resp.StatusCode, trimAll(string(raw)))
			}
			var out struct {
				Answer  string `json:"answer"`
				Results []struct {
					Title   string  `json:"title"`
					URL     string  `json:"url"`
					Content string  `json:"content"`
					Score   float64 `json:"score"`
				} `json:"results"`
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				return "", fmt.Errorf("解析搜索结果失败：%v", err)
			}
			if len(out.Results) == 0 {
				return "没有找到相关结果，建议更换关键词。", nil
			}
			var sb bytes.Buffer
			if out.Answer != "" {
				sb.WriteString("【综合回答】" + out.Answer + "\n\n")
			}
			for i, r := range out.Results {
				title := trimAll(r.Title)
				content := trimAll(r.Content)
				if len([]rune(content)) > 240 {
					content = string([]rune(content)[:240]) + "…"
				}
				fmt.Fprintf(&sb, "%d. %s\n   链接：%s\n   摘要：%s\n\n", i+1, title, trimAll(r.URL), content)
			}
			return strings.TrimSpace(sb.String()), nil
		},
	}
}

func trimAll(s string) string {
	return strings.TrimSpace(s)
}
