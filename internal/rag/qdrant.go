package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// QdrantClient 是 Qdrant 的轻量 REST 客户端（自建 docker 版）。
// 只用三组 API：建 collection、批量 upsert points、向量检索，避免引入重 SDK。
type QdrantClient struct {
	addr   string
	apiKey string // 可为空（自建未启用 key）
	http   *http.Client
}

func NewQdrant(addr, apiKey string) *QdrantClient {
	return &QdrantClient{
		addr:   addr,
		apiKey: apiKey,
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

// CollectionName 区分知识库与记忆两个向量集合。
const (
	CollectionKnowledge = "knowledge_chunks"
	CollectionMemory    = "memories"
)

// EnsureCollection 幂等创建 collection；已存在则跳过。
func (c *QdrantClient) EnsureCollection(ctx context.Context, name string, dim int) error {
	if dim <= 0 {
		return fmt.Errorf("qdrant: 非法维度 %d", dim)
	}
	body, _ := json.Marshal(map[string]any{
		"vectors": map[string]any{
			"size":     dim,
			"distance": "Cosine",
		},
		"optimizers_config": map[string]any{
			"indexing_threshold": 200, // 小规模先全内存，超过阈值建 HNSW
		},
	})
	req, err := c.newReq(ctx, http.MethodPut, "/collections/"+name, body)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		return nil
	}
	// 409 = 已存在，视为成功（幂等）
	if resp.StatusCode == http.StatusConflict {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return fmt.Errorf("qdrant: 创建 collection %s 失败 HTTP %d: %s", name, resp.StatusCode, string(b))
}

// Point 一个向量点。
type Point struct {
	ID      uint64
	Vector  []float32
	Payload map[string]any
}

// Upsert 批量写入（含覆盖语义：同 ID 覆盖）。
func (c *QdrantClient) Upsert(ctx context.Context, collection string, points []Point) error {
	if len(points) == 0 {
		return nil
	}
	type pt struct {
		ID      uint64         `json:"id"`
		Vector  []float32      `json:"vector"`
		Payload map[string]any `json:"payload"`
	}
	pts := make([]pt, 0, len(points))
	for _, p := range points {
		pts = append(pts, pt{ID: p.ID, Vector: p.Vector, Payload: p.Payload})
	}
	body, _ := json.Marshal(map[string]any{"points": pts})
	req, err := c.newReq(ctx, http.MethodPut, "/collections/"+collection+"/points?wait=true", body)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("qdrant: upsert 失败 HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// DeleteByIDs 按向量点 ID 删除（重传覆盖、文档删除时用）。
func (c *QdrantClient) DeleteByIDs(ctx context.Context, collection string, ids []uint64) error {
	if len(ids) == 0 {
		return nil
	}
	body, _ := json.Marshal(map[string]any{
		"points": ids,
	})
	req, err := c.newReq(ctx, http.MethodPost, "/collections/"+collection+"/points/delete?wait=true", body)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("qdrant: delete 失败 HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// SearchResult 单条检索结果。
type SearchResult struct {
	ID      uint64         `json:"id"`
	Score   float32        `json:"score"`
	Payload map[string]any `json:"payload"`
}

// Search 向量检索，返回按相似度降序的 topK。
func (c *QdrantClient) Search(ctx context.Context, collection string, vector []float32, topK int) ([]SearchResult, error) {
	body, _ := json.Marshal(map[string]any{
		"vector":      vector,
		"limit":       topK,
		"with_payload": true,
	})
	req, err := c.newReq(ctx, http.MethodPost, "/collections/"+collection+"/points/search", body)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qdrant: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("qdrant: search 失败 HTTP %d: %s", resp.StatusCode, string(b))
	}
	var out struct {
		Result []SearchResult `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("qdrant: %w", err)
	}
	return out.Result, nil
}

func (c *QdrantClient) newReq(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.addr+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("api-key", c.apiKey)
	}
	return req, nil
}
