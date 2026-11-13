// Package llm 封装火山方舟（Ark）对话模型客户端，提供统一流式接口；
// 未配置 API Key 时由 MockClient 提供可演示的模拟流。
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Message struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"` // role=tool 时回填对应的调用 id
}

type Usage struct {
	InputTokens  int
	OutputTokens int
}

// ToolDef 工具声明：name + description + JSON Schema parameters（tools 请求体）。
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolCall 模型发起的一次工具调用请求。
type ToolCall struct {
	ID        string // tool_call_id（回填 tool 消息时使用）
	Name      string
	Arguments string // 参数 JSON 字符串
}

// Completion 带工具调用的非流式响应：要么有文本（最终回复），要么有工具调用。
type Completion struct {
	Text      string
	ToolCalls []ToolCall
	Usage     Usage
}

// Client 是编排层使用的统一对话接口。
type Client interface {
	// Stream 流式调用模型；每个增量片段经 onDelta 回调（可为空字符串）；
	// 返回完整文本与用量统计。
	Stream(ctx context.Context, messages []Message, onDelta func(string)) (string, Usage, error)
	// Complete 非流式调用（记忆抽取等结构化任务用）。
	Complete(ctx context.Context, messages []Message) (string, Usage, error)
	// CompleteWithTools 非流式调用并携带工具注册表；响应可能含 ToolCalls（需执行后回填）。
	CompleteWithTools(ctx context.Context, messages []Message, tools []ToolDef) (*Completion, error)
}

// ---------- 火山方舟实现 ----------

type ArkClient struct {
	baseURL         string
	apiKey          string
	model           string
	http            *http.Client
	disableThinking bool // 深度思考模型默认思考；结构化任务（记忆抽取）可关闭
}

func NewArkClient(baseURL, apiKey, model string) *ArkClient {
	return NewArkClientWithOptions(baseURL, apiKey, model, false)
}

// NewArkClientNoThink 创建关闭深度思考的客户端，用于记忆抽取等结构化 JSON 任务：
// 深度思考模型对抽取任务会输出极长思考链（实测 2 分钟+），关闭后秒级返回。
func NewArkClientNoThink(baseURL, apiKey, model string) *ArkClient {
	return NewArkClientWithOptions(baseURL, apiKey, model, true)
}

func NewArkClientWithOptions(baseURL, apiKey, model string, disableThinking bool) *ArkClient {
	return &ArkClient{
		baseURL:         baseURL,
		apiKey:          apiKey,
		model:           model,
		http:            &http.Client{Timeout: 5 * time.Minute},
		disableThinking: disableThinking,
	}
}

type thinkingCfg struct {
	Type string `json:"type"`
}

type streamOptionsCfg struct {
	IncludeUsage bool `json:"include_usage"`
}

type arkToolCfg struct {
	Type     string  `json:"type"`
	Function ToolDef `json:"function"`
}

type arkChatRequest struct {
	Model         string            `json:"model"`
	Messages      []Message         `json:"messages"`
	Stream        bool              `json:"stream"`
	Thinking      *thinkingCfg      `json:"thinking,omitempty"`
	StreamOptions *streamOptionsCfg `json:"stream_options,omitempty"`
	Tools         []arkToolCfg      `json:"tools,omitempty"`
}

type arkChatResponse struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (c *ArkClient) Stream(ctx context.Context, messages []Message, onDelta func(string)) (string, Usage, error) {
	payload := arkChatRequest{Model: c.model, Messages: messages, Stream: true, StreamOptions: &streamOptionsCfg{IncludeUsage: true}}
	if c.disableThinking {
		payload.Thinking = &thinkingCfg{Type: "disabled"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", Usage{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v3/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", Usage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", Usage{}, fmt.Errorf("ark request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", Usage{}, fmt.Errorf("ark status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var full strings.Builder
	usage := Usage{}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk arkChatResponse
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue // 跳过无法解析的心跳/注释行
		}
		if chunk.Usage != nil {
			usage.InputTokens = chunk.Usage.PromptTokens
			usage.OutputTokens = chunk.Usage.CompletionTokens
		}
		if len(chunk.Choices) > 0 {
			delta := chunk.Choices[0].Delta.Content
			if delta == "" {
				continue
			}
			full.WriteString(delta)
			if onDelta != nil {
				onDelta(delta)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return full.String(), usage, fmt.Errorf("ark stream read: %w", err)
	}
	return full.String(), usage, nil
}

// Complete 非流式调用（stream=false），返回完整文本与用量。
func (c *ArkClient) Complete(ctx context.Context, messages []Message) (string, Usage, error) {
	payload := arkChatRequest{Model: c.model, Messages: messages, Stream: false}
	if c.disableThinking {
		payload.Thinking = &thinkingCfg{Type: "disabled"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", Usage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v3/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", Usage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", Usage{}, fmt.Errorf("ark request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", Usage{}, fmt.Errorf("ark status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", Usage{}, fmt.Errorf("ark decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", Usage{}, fmt.Errorf("ark: 空 choices")
	}
	usage := Usage{}
	if out.Usage != nil {
		usage.InputTokens = out.Usage.PromptTokens
		usage.OutputTokens = out.Usage.CompletionTokens
	}
	return out.Choices[0].Message.Content, usage, nil
}

// CompleteWithTools 非流式调用并携带工具注册表（function calling）。
// 响应可能含 ToolCalls（需要系统执行后以 role=tool 回填再继续）；也可能直接返回文本。
func (c *ArkClient) CompleteWithTools(ctx context.Context, messages []Message, tools []ToolDef) (*Completion, error) {
	payload := arkChatRequest{Model: c.model, Messages: messages, Stream: false}
	if c.disableThinking {
		payload.Thinking = &thinkingCfg{Type: "disabled"}
	}
	for _, t := range tools {
		payload.Tools = append(payload.Tools, arkToolCfg{Type: "function", Function: t})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v3/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ark request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("ark status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("ark decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("ark: 空 choices")
	}
	comp := &Completion{Usage: Usage{}}
	if out.Usage != nil {
		comp.Usage.InputTokens = out.Usage.PromptTokens
		comp.Usage.OutputTokens = out.Usage.CompletionTokens
	}
	comp.Text = out.Choices[0].Message.Content
	for _, tc := range out.Choices[0].Message.ToolCalls {
		comp.ToolCalls = append(comp.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	return comp, nil
}
