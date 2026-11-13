package rag

import (
	"context"
	"fmt"

	"digital-twin/internal/embedding"
	"digital-twin/internal/store"
)

// Indexer 执行"解析 → 分块 → 落库 → 向量化 → Qdrant"的完整入库流程。
// 一个 doc 一条流水线；失败时写 error_msg 并把状态置为 failed（可重跑）。
type Indexer struct {
	db  *store.DB
	emb *embedding.Client
	qd  *QdrantClient

	embedBatch int // 每次 embedding 的最大文本数（方舟批量上限，保守取 16）
}

func NewIndexer(db *store.DB, emb *embedding.Client, qd *QdrantClient) *Indexer {
	return &Indexer{db: db, emb: emb, qd: qd, embedBatch: 16}
}

// IndexDoc 全流程入库文档。幂等：同 doc 重复调用会覆盖 chunk 与向量。
func (ix *Indexer) IndexDoc(ctx context.Context, docID int64) error {
	doc, err := ix.db.GetKnowledgeDoc(ctx, docID)
	if err != nil {
		return fmt.Errorf("get doc: %w", err)
	}
	if err := ix.db.UpdateKnowledgeStatus(ctx, docID, "indexing", 0, ""); err != nil {
		return err
	}

	// 1. 解析原文为纯文本
	text := ParseText([]byte(doc.Content), doc.SourceType)
	if text == "" {
		_ = ix.db.UpdateKnowledgeStatus(ctx, docID, "failed", 0, "解析后内容为空")
		return fmt.Errorf("doc %d 内容为空", docID)
	}

	// 2. 分块
	chunks := ChunkText(text, DefaultChunkOptions())
	if len(chunks) == 0 {
		_ = ix.db.UpdateKnowledgeStatus(ctx, docID, "failed", 0, "分块结果为空")
		return fmt.Errorf("doc %d 分块为空", docID)
	}

	// 3. 写 PG（拿到 chunk 自增 id，作为 Qdrant point id）
	rows, err := ix.db.InsertKnowledgeChunks(ctx, docID, chunks)
	if err != nil {
		_ = ix.db.UpdateKnowledgeStatus(ctx, docID, "failed", 0, "chunk 落库失败: "+err.Error())
		return err
	}

	// 4. 向量化（分批）
	texts := make([]string, len(rows))
	for i, r := range rows {
		texts[i] = r.Content
	}
	vectors, err := ix.embedAll(ctx, texts)
	if err != nil {
		_ = ix.db.UpdateKnowledgeStatus(ctx, docID, "failed", 0, "向量化失败: "+err.Error())
		return err
	}

	// 5. 写 Qdrant（同 ID 覆盖）
	points := make([]Point, 0, len(rows))
	for i, r := range rows {
		points = append(points, Point{
			ID:     uint64(r.ID),
			Vector: vectors[i],
			Payload: map[string]any{
				"doc_id":   r.DocID,
				"idx":      i,
				"title":    doc.Title,
				"doc_type": doc.SourceType,
			},
		})
	}
	// 确保集合存在（维度取第一个向量）
	if err := ix.qd.EnsureCollection(ctx, CollectionKnowledge, len(vectors[0])); err != nil {
		_ = ix.db.UpdateKnowledgeStatus(ctx, docID, "failed", 0, "Qdrant 集合初始化失败: "+err.Error())
		return err
	}
	if err := ix.qd.Upsert(ctx, CollectionKnowledge, points); err != nil {
		_ = ix.db.UpdateKnowledgeStatus(ctx, docID, "failed", 0, "Qdrant 写入失败: "+err.Error())
		return err
	}

	// 6. 完成
	if err := ix.db.UpdateKnowledgeStatus(ctx, docID, "ready", len(points), ""); err != nil {
		return err
	}
	return nil
}

func (ix *Indexer) embedAll(ctx context.Context, texts []string) ([][]float32, error) {
	var out [][]float32
	for start := 0; start < len(texts); start += ix.embedBatch {
		end := start + ix.embedBatch
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := ix.emb.EmbedTexts(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// DeleteDoc 删除文档：PG 软删除 + Qdrant 清理对应向量。
func (ix *Indexer) DeleteDoc(ctx context.Context, docID int64) error {
	ids, err := ix.db.DeleteKnowledgeDoc(ctx, docID)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		if err := ix.qd.DeleteByIDs(ctx, CollectionKnowledge, ids); err != nil {
			return fmt.Errorf("qdrant 清理失败: %w", err)
		}
	}
	return nil
}
