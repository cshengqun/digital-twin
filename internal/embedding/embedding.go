// Package embedding 封装火山方舟向量化（多模态向量化 /api/v3/embeddings/multimodal）。
// 同一把 ARK_API_KEY 即可调用对话与 embedding 接入点，无需额外申请。
// 注意：多模态向量化一次请求 = 一个样本（可含多模态），返回单个向量；
// 因此这里按条向量化，并通过 instructions 区分「语料入库」与「查询检索」两种角色，
// 保证向量表示与检索场景一致（见方舟向量化文档「设置 instructions 字段」）。
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// CorpusInstruction 语料侧指令：入库的 chunk/记忆都属于被检索的纯文本语料。
const CorpusInstruction = "Instruction:Compress the text into one word.\nQuery:"

// QueryInstruction 查询侧指令：短 query 检索长文本知识/记忆（非对称检索）。
const QueryInstruction = "Target_modality: text.\nInstruction:为这个句子生成表示以用于检索相关文章\nQuery:"

type Client struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

func New(baseURL, apiKey, model string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// EmbedTexts 批量向量化（语料侧，入库用）。返回与输入等长、同序的向量列表。
func (c *Client) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(texts))
	for _, t := range texts {
		vec, err := c.embedOne(ctx, t, CorpusInstruction)
		if err != nil {
			return nil, err
		}
		out = append(out, vec)
	}
	return out, nil
}

// EmbedQuery 向量化单条查询（检索侧）。
func (c *Client) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	if query == "" {
		return nil, fmt.Errorf("embedding: 空查询")
	}
	return c.embedOne(ctx, query, QueryInstruction)
}

// embedOne 调用方舟多模态向量化，对单段文本返回一个稠密向量。
func (c *Client) embedOne(ctx context.Context, text, instructions string) ([]float32, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("embedding: ARK_API_KEY 未配置，无法向量化")
	}
	body, _ := json.Marshal(map[string]any{
		"model":           c.model,
		"instructions":    instructions,
		"encoding_format": "float",
		"input": []map[string]any{
			{"type": "text", "text": text},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v3/embeddings/multimodal", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("embedding: HTTP %d: %s", resp.StatusCode, string(b))
	}

	// 多模态接口响应：data 为对象（非数组），含 embedding 字段。
	var out struct {
		Data struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("embedding: 解码响应失败: %w", err)
	}
	if out.Error.Message != "" {
		return nil, fmt.Errorf("embedding: %s", out.Error.Message)
	}
	if len(out.Data.Embedding) == 0 {
		return nil, fmt.Errorf("embedding: 返回空向量")
	}
	return out.Data.Embedding, nil
}
