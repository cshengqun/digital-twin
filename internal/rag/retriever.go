package rag

import (
	"context"
	"fmt"
	"strings"
	"time"

	"digital-twin/internal/embedding"
	"digital-twin/internal/store"
)

// RetrievalItem 融合后的单条召回结果。
type RetrievalItem struct {
	Kind  string         // "knowledge" | "memory"
	ID    uint64         // chunk id / memory id（用于回查与溯源）
	Text  string         // 直接可注入上下文的文本
	Score float64        // RRF 融合分
	Meta  map[string]any // doc_id / title / memory type 等
}

// Step 检索链路中的一个环节（供前端展示"本次执行流程"）。
type Step struct {
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	Ms     int64  `json:"ms"`
}

// RetrieveStats 检索过程统计，逐环节记录耗时与命中情况。
type RetrieveStats struct {
	Steps []Step
}

func (s *RetrieveStats) Add(name, detail string, start time.Time) {
	if s == nil {
		return
	}
	s.Steps = append(s.Steps, Step{Name: name, Detail: detail, Ms: time.Since(start).Milliseconds()})
}

// Retriever 混合检索：Qdrant 向量（知识库+记忆）+ PG 关键词（知识库）→ RRF 融合。
type Retriever struct {
	db    *store.DB
	emb   *embedding.Client
	qd    *QdrantClient
	rrfK  int // RRF 常数，常用 60
	perK  int // 每路召回条数
	final int // 融合后返回条数
}

func NewRetriever(db *store.DB, emb *embedding.Client, qd *QdrantClient) *Retriever {
	return &Retriever{db: db, emb: emb, qd: qd, rrfK: 60, perK: 20, final: 12}
}

// Retrieve 输入用户 query，返回融合后的知识/记忆片段（按 RRF 分降序）。
// stats 可空；非空时记录各环节耗时与命中，供前端展示执行流程。
func (r *Retriever) Retrieve(ctx context.Context, agentID int64, query string, stats *RetrieveStats) ([]RetrievalItem, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	st := time.Now()
	vec, err := r.emb.EmbedQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query embedding: %w", err)
	}
	stats.Add("向量化问题", "", st)

	// 三路召回
	st = time.Now()
	kwItems, err := r.keywordRetrieve(ctx, agentID, query)
	if err != nil {
		return nil, err
	}
	stats.Add("关键词检索", fmt.Sprintf("命中 %d 条", len(kwItems)), st)

	st = time.Now()
	kdItems, err := r.vectorRetrieve(ctx, CollectionKnowledge, vec)
	if err != nil {
		return nil, err
	}
	stats.Add("知识向量召回", fmt.Sprintf("命中 %d 条", len(kdItems)), st)

	st = time.Now()
	mmItems, err := r.vectorRetrieve(ctx, CollectionMemory, vec)
	if err != nil {
		return nil, err
	}
	stats.Add("记忆向量召回", fmt.Sprintf("命中 %d 条", len(mmItems)), st)

	// 4) 记忆关键词路（精确词召回，补向量盲区：人名/公司名/术语）
	st = time.Now()
	mkItems, err := r.memoryKeywordRetrieve(ctx, agentID, query)
	if err != nil {
		return nil, err
	}
	stats.Add("记忆关键词召回", fmt.Sprintf("命中 %d 条", len(mkItems)), st)

	acc := map[string]*RetrievalItem{}

	// 1) 关键词路（知识 chunk）
	for i, row := range kwItems {
		key := fmt.Sprintf("knowledge:%d", row.ID)
		if _, ok := acc[key]; !ok {
			acc[key] = &RetrievalItem{Kind: "knowledge", ID: uint64(row.ID), Text: row.Content, Meta: map[string]any{"doc_id": row.DocID}}
		}
		acc[key].Score += rrf(i, r.rrfK)
	}
	// 2) 向量路（知识 chunk）——只记录 ID/meta，文本最后统一回查
	for i, s := range kdItems {
		key := fmt.Sprintf("knowledge:%d", s.ID)
		if _, ok := acc[key]; !ok {
			m := map[string]any{}
			if t, ok := s.Payload["title"]; ok {
				m["title"] = t
			}
			if d, ok := s.Payload["doc_id"]; ok {
				m["doc_id"] = d
			}
			acc[key] = &RetrievalItem{Kind: "knowledge", ID: s.ID, Meta: m}
		}
		acc[key].Score += rrf(i, r.rrfK)
	}
	// 3) 向量路（记忆）——payload 自带 text
	for i, s := range mmItems {
		key := fmt.Sprintf("memory:%d", s.ID)
		if _, ok := acc[key]; !ok {
			m := map[string]any{}
			if t, ok := s.Payload["type"]; ok {
				m["type"] = t
			}
			if mid, ok := s.Payload["memory_id"]; ok {
				m["memory_id"] = mid
			}
			acc[key] = &RetrievalItem{Kind: "memory", ID: s.ID, Text: str(s.Payload["text"]), Meta: m}
		}
		acc[key].Score += rrf(i, r.rrfK)
	}
	// 4) 关键词路（记忆）——与向量路同 key，RRF 分数自然累加
	for i, row := range mkItems {
		key := fmt.Sprintf("memory:%d", row.ID)
		if _, ok := acc[key]; !ok {
			acc[key] = &RetrievalItem{Kind: "memory", ID: uint64(row.ID), Text: row.Text,
				Meta: map[string]any{"type": row.Type, "memory_id": row.ID}}
		}
		acc[key].Score += rrf(i, r.rrfK)
	}

	// 回查知识 chunk 文本（一次批量）
	if err := r.fillKnowledgeTexts(ctx, acc); err != nil {
		return nil, err
	}

	// 排序取 topK
	st = time.Now()
	items := make([]*RetrievalItem, 0, len(acc))
	for _, it := range acc {
		if strings.TrimSpace(it.Text) == "" {
			continue
		}
		items = append(items, it)
	}
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].Score > items[j-1].Score; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	out := make([]RetrievalItem, 0, min(r.final, len(items)))
	for i := 0; i < len(items) && i < r.final; i++ {
		out = append(out, *items[i])
	}
	kCnt, mCnt := 0, 0
	for _, it := range out {
		if it.Kind == "memory" {
			mCnt++
		} else {
			kCnt++
		}
	}
	stats.Add("RRF 融合", fmt.Sprintf("取前 %d 条（知识 %d / 记忆 %d）", len(out), kCnt, mCnt), st)
	return out, nil
}

// fillKnowledgeTexts 批量回查 knowledge_chunks 内容，填充文本。
func (r *Retriever) fillKnowledgeTexts(ctx context.Context, acc map[string]*RetrievalItem) error {
	ids := make([]uint64, 0, len(acc))
	for _, it := range acc {
		if it.Kind == "knowledge" && it.Text == "" {
			ids = append(ids, it.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := r.db.PG.Query(ctx, `SELECT id, content FROM knowledge_chunks WHERE id = ANY($1)`, ids)
	if err != nil {
		return fmt.Errorf("回查 chunk 文本: %w", err)
	}
	defer rows.Close()
	texts := map[uint64]string{}
	for rows.Next() {
		var id int64
		var content string
		if err := rows.Scan(&id, &content); err != nil {
			return err
		}
		texts[uint64(id)] = content
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, it := range acc {
		if it.Kind == "knowledge" {
			if t, ok := texts[it.ID]; ok {
				it.Text = t
			}
		}
	}
	return nil
}

// rrf 倒数排名融合：score = 1/(k + rank)，rank 从 0 起。
func rrf(rank, k int) float64 {
	return 1.0 / float64(k+rank+1)
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// keywordRetrieve：query 拆 2-gram 关键词 → PG ILIKE 检索知识 chunk。
func (r *Retriever) keywordRetrieve(ctx context.Context, agentID int64, query string) ([]store.KnowledgeChunkRow, error) {
	kws := SplitKeywords(query, 8)
	if len(kws) == 0 {
		return nil, nil
	}
	rows, err := r.db.SearchKnowledgeByKeyword(ctx, agentID, kws, r.perK/2)
	if err != nil {
		return nil, fmt.Errorf("keyword search: %w", err)
	}
	return rows, nil
}

// memoryKeywordRetrieve：query 拆 2-gram 关键词 → PG ILIKE 检索记忆文本。
func (r *Retriever) memoryKeywordRetrieve(ctx context.Context, agentID int64, query string) ([]store.MemoryKeywordRow, error) {
	kws := SplitKeywords(query, 8)
	if len(kws) == 0 {
		return nil, nil
	}
	rows, err := r.db.SearchMemoriesByKeyword(ctx, agentID, kws, r.perK/2)
	if err != nil {
		return nil, fmt.Errorf("memory keyword search: %w", err)
	}
	return rows, nil
}

// vectorRetrieve：对指定集合做向量检索。集合尚未创建（知识库/记忆为空）是常态，视为无结果不中断检索。
func (r *Retriever) vectorRetrieve(ctx context.Context, collection string, vec []float32) ([]SearchResult, error) {
	res, err := r.qd.Search(ctx, collection, vec, r.perK)
	if err != nil {
		if strings.Contains(err.Error(), "doesn't exist") {
			return nil, nil
		}
		return nil, fmt.Errorf("vector search %s: %w", collection, err)
	}
	return res, nil
}

// SplitKeywords 把 query 拆成中文 2-gram 关键词（去重、去纯标点），最多 max 个。
// 中文没有空格分词，bigram 子串匹配配合 pg_trgm 对 ILIKE 友好。
func SplitKeywords(query string, max int) []string {
	runes := []rune(query)
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if len([]rune(s)) < 2 || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for i := 0; i+1 < len(runes); i++ {
		a, b := runes[i], runes[i+1]
		if isSkip(a) || isSkip(b) {
			continue
		}
		add(string(a) + string(b))
		if len(out) >= max {
			break
		}
	}
	return out
}

func isSkip(r rune) bool {
	switch r {
	case ' ', '，', '。', '！', '？', '；', '、', '：', '"', '\'', '（', '）', '【', '】', '《', '》', '-', '_', '\n', '\t', '?', '!', '.', ',', ';', ':', '(', ')', '[', ']', '{', '}', '/', '\\', '|', '@', '#', '$', '%', '^', '&', '*', '+', '=', '<', '>', '~', '`':
		return true
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
