package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"digital-twin/internal/embedding"
	"digital-twin/internal/llm"
	"digital-twin/internal/store"
)

// MemoryExtractor 从会话中抽取"值得长期记住"的用户画像/事件/方法，写入 PG + 向量库。
// 由后台任务调用，先保存抽取结果，再索引并发布。
type MemoryExtractor struct {
	db  *store.DB
	llm llm.Client
	emb *embedding.Client
	qd  *QdrantClient

	importance int // 默认记忆重要度 1-10
}

func NewMemoryExtractor(db *store.DB, lc llm.Client, emb *embedding.Client, qd *QdrantClient) *MemoryExtractor {
	return &MemoryExtractor{db: db, llm: lc, emb: emb, qd: qd, importance: 6}
}

// MemItem 单条抽取记忆（含长期重要度打分 1-10 + 结构化事件字段）。
type MemItem struct {
	Text       string   `json:"text"`
	Importance int      `json:"importance"`
	EventTime  string   `json:"event_time,omitempty"` // 事件发生时间（YYYY-MM-DD 或"今天/昨天/上周"），未知留空
	Mood       int      `json:"mood,omitempty"`       // 情绪值 1-10（1=极差，10=极佳），0=未知
	Amount     float64  `json:"amount,omitempty"`     // 涉及金额（人民币元），0=无/未知
	Tags       []string `json:"tags,omitempty"`       // 事件标签 2-5 个（项目名/人物/领域），如"面试""OKX""房贷"
}

// ExtractedMemories 是 LLM 抽取结果的 JSON 结构。
type ExtractedMemories struct {
	Facts      []MemItem `json:"facts"`
	Episodic   []MemItem `json:"episodic"`
	Procedural []MemItem `json:"procedural"`
}

// ExtractConversation resumes the pinned range for the current extraction job.
func (e *MemoryExtractor) ExtractConversation(ctx context.Context, agentID int64, ownerDesc string, id int64) error {
	work, err := e.db.GetConversationExtractWork(ctx, agentID, id)
	if err != nil {
		return err
	}
	msgs := make([]llm.Message, 0, len(work.Messages)+1)
	if work.Summary != "" {
		msgs = append(msgs, llm.Message{Role: "system", Content: "【本会话早期内容摘要】\n" + work.Summary})
	}
	for _, msg := range work.Messages {
		msgs = append(msgs, llm.Message{Role: msg.Role, Content: msg.Content})
	}
	source := fmt.Sprintf("conversation:%d:%d", id, work.UntilID)
	return e.runMemoryJob(ctx, store.ConversationJob, agentID, ownerDesc, id, source, work.Saved, msgs)
}

func (e *MemoryExtractor) runMemoryJob(ctx context.Context, kind store.JobKind, agentID int64, ownerDesc string, id int64, source string, saved bool, msgs []llm.Message) error {
	if !saved {
		items, err := e.extractMemories(ctx, agentID, ownerDesc, source, msgs)
		if err != nil {
			return err
		}
		if err := e.db.SaveExtractedMemories(ctx, kind, agentID, id, source, items); err != nil {
			return err
		}
	}
	items, err := e.db.ListStagedMemories(ctx, agentID, source)
	if err != nil {
		return err
	}
	if err := e.indexMemories(ctx, items); err != nil {
		return err
	}
	return e.db.CompleteMemoryJob(ctx, kind, agentID, id, source)
}

func (e *MemoryExtractor) extractMemories(ctx context.Context, agentID int64, ownerDesc, sourceRef string, msgs []llm.Message) ([]store.MemoryDraft, error) {
	if len(msgs) == 0 {
		return nil, nil
	}
	slog.Info("memory extract start", "source", sourceRef, "msgs", len(msgs))
	if e.llm == nil {
		return nil, fmt.Errorf("记忆抽取: LLM 未配置")
	}

	corrections, err := e.db.RecentCorrections(ctx, agentID)
	if err != nil {
		return nil, err
	}
	var confirmed strings.Builder
	writeConfirmedCorrections(&confirmed, corrections)
	if confirmed.Len() > 0 {
		confirmed.WriteString("这些纠正已经保存。不要再次抽取，也不要把与其冲突的旧说法作为当前事实。")
	}

	const win = 30
	all := make([]*ExtractedMemories, 0, (len(msgs)+win-1)/win)
	for start := 0; start < len(msgs); start += win {
		end := start + win
		if end > len(msgs) {
			end = len(msgs)
		}
		seg := msgs[start:end]
		prompt := buildExtractPrompt(ownerDesc, seg) + confirmed.String()
		resp, _, err := e.llm.Complete(ctx, []llm.Message{{Role: "user", Content: prompt}})
		if err != nil {
			return nil, fmt.Errorf("抽取第 %d 段: %w", start/win+1, err)
		}
		ext, err := parseExtraction(resp)
		if err != nil {
			return nil, fmt.Errorf("解析第 %d 段: %w", start/win+1, err)
		}
		all = append(all, ext)
	}

	var items []store.MemoryDraft
	seen := map[string]bool{}
	collect := func(typ string, list []MemItem) {
		for _, it := range list {
			t := strings.TrimSpace(it.Text)
			if t == "" || seen[t] {
				continue
			}
			seen[t] = true
			imp := it.Importance
			if imp < 1 {
				imp = e.importance // 缺失/0 分用默认 6 兜底
			}
			if imp > 10 {
				imp = 10
			}
			// 构造结构化 meta
			meta := map[string]any{}
			if it.EventTime != "" {
				meta["event_time"] = it.EventTime
			}
			if it.Mood >= 1 && it.Mood <= 10 {
				meta["mood"] = it.Mood
			}
			if it.Amount > 0 {
				meta["amount"] = it.Amount
			}
			if len(it.Tags) > 0 {
				meta["tags"] = it.Tags
			}
			items = append(items, store.MemoryDraft{Type: typ, Text: t, Importance: imp, Meta: meta})
		}
	}
	for _, ext := range all {
		collect("fact", ext.Facts)
		collect("episodic", ext.Episodic)
		collect("procedural", ext.Procedural)
	}
	return items, nil
}

func (e *MemoryExtractor) indexMemories(ctx context.Context, items []store.MemoryDraft) error {
	if len(items) == 0 {
		return nil
	}
	// 向量化并写入 Qdrant，召回后由 PostgreSQL 提供当前正文。
	texts := make([]string, len(items))
	for i, it := range items {
		texts[i] = it.Text
	}
	vectors, err := e.emb.EmbedTexts(ctx, texts)
	if err != nil {
		return fmt.Errorf("记忆向量化失败: %w", err)
	}
	points := make([]Point, 0, len(items))
	for i, it := range items {
		payload := map[string]any{
			"memory_id": it.ID,
			"type":      it.Type,
			"text":      it.Text,
		}
		if tags, ok := it.Meta["tags"]; ok {
			payload["tags"] = tags
		}
		points = append(points, Point{
			ID:      uint64(it.ID),
			Vector:  vectors[i],
			Payload: payload,
		})
	}
	if err := e.qd.EnsureCollection(ctx, CollectionMemory, len(vectors[0])); err != nil {
		return err
	}
	if err := e.qd.Upsert(ctx, CollectionMemory, points); err != nil {
		return fmt.Errorf("记忆向量入库失败: %w", err)
	}
	return nil
}

// SyncEditedMemory processes the vector update or removal for a user edit.
func (e *MemoryExtractor) SyncEditedMemory(ctx context.Context, agentID, id int64) error {
	memory, err := e.db.GetMemory(ctx, agentID, id)
	if err != nil {
		return err
	}
	if memory.Status == "active" {
		err = e.indexMemories(ctx, []store.MemoryDraft{{ID: id, Type: memory.Type, Text: memory.Text}})
	} else {
		err = e.qd.DeleteByIDs(ctx, CollectionMemory, []uint64{uint64(id)})
	}
	if err != nil {
		return err
	}
	return e.db.CompleteMemoryVector(ctx, id, memory.Revision)
}

// buildExtractPrompt 构造单段抽取 prompt。
func buildExtractPrompt(ownerDesc string, messages []llm.Message) string {
	var dial strings.Builder
	for _, m := range messages {
		role := "用户"
		if m.Role == "assistant" {
			role = "分身"
		}
		dial.WriteString(role + ": " + strings.TrimSpace(m.Content) + "\n")
	}
	if dial.Len() > 12000 { // 截断超长对话（约 4k token 上限内的保护）
		text := dial.String()
		dial.Reset()
		dial.WriteString(text[len(text)-12000:])
	}
	return `你是个人数字分身的人格养成系统。从下面的对话中抽取关于"分身主人"的长期记忆，只抽取值得长期记住的信息，分三类：
- facts：关于分身主人的事实（职业、家庭、偏好、健康状况、投资、习惯等）
- episodic：分身主人经历的具体事件或情景（含时间、地点、经过等关键信息）
- procedural：分身主人的做事方法、规则、流程偏好（如何做某事、偏好的步骤）
重要规则：
1. 分身主人是：` + ownerDesc + `。本系统可能被多人使用，对话中出现的其他人的私人信息（他们的职业、家庭、偏好、事务等）一律不抽取
2. 只有能明确归因于分身主人本人的信息才入库；不确定归属的，宁可不抽
3. 忽略寒暄、泛泛的问答，以及不构成长期画像的临时信息。分身自己的回答不能作为事实来源；用户明确纠正过的说法只保留纠正后的版本。
4. 每条记忆是一句简洁、独立、陈述句，不超过100字；避免"用户说"这类转述
5. importance：每条记忆给出 1-10 的长期重要度。8-10 只给影响分身主人画像、有长期价值的事实（身份、价值观、重大决策、长期习惯、关键偏好）；5-7 一般性事实；4 及以下的细节琐碎，宁可不抽
6. 结构化字段（能从对话中明确提取就填，不确定就留空/0/省略）：
   - event_time：事件发生时间，YYYY-MM-DD 格式；对话里只说"今天/昨天/上周"就按对话当天换算；事实类长期记忆（"我在深圳"）不需要填
   - mood：事件发生时主人的情绪，1-10（1=极差，10=极佳）；事实类记忆不填
   - amount：涉及金额（人民币元）；没有金额或谈钱不相关就不填
   - tags：2-5 个短标签，用于后续聚合分析，如"面试""OKX""房贷""羽毛球""投资""英语"
7. 只输出一个 JSON 对象，不要任何其他文字或 markdown 代码块标记
8. 没有对应类别就输出空数组
格式：{"facts":[{"text":"...","importance":8}],"episodic":[{"text":"...","importance":6,"event_time":"2026-11-07","mood":7,"tags":["面试","OKX"]}],"procedural":[{"text":"...","importance":7}]}

对话内容：
` + dial.String()
}

// parseExtraction 解析 LLM 输出：容忍 ```json 包裹与前后杂质；兼容新旧两种格式
// （新格式每条带 importance；旧格式为纯字符串数组，由 collect 用默认值兜底）。
func parseExtraction(s string) (*ExtractedMemories, error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		s = s[i:]
	}
	if i := strings.LastIndex(s, "}"); i >= 0 {
		s = s[:i+1]
	}
	var out ExtractedMemories
	if err := json.Unmarshal([]byte(s), &out); err == nil {
		return &out, nil
	}
	var legacy struct {
		Facts      []string `json:"facts"`
		Episodic   []string `json:"episodic"`
		Procedural []string `json:"procedural"`
	}
	if err := json.Unmarshal([]byte(s), &legacy); err != nil {
		return nil, err
	}
	toItems := func(ss []string) []MemItem {
		items := make([]MemItem, 0, len(ss))
		for _, v := range ss {
			items = append(items, MemItem{Text: v})
		}
		return items
	}
	return &ExtractedMemories{
		Facts:      toItems(legacy.Facts),
		Episodic:   toItems(legacy.Episodic),
		Procedural: toItems(legacy.Procedural),
	}, nil
}
