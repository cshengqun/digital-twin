package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"digital-twin/internal/llm"
	"digital-twin/internal/store"
)

// WeeklyReporter 每周日晚自动生成个人周报。
type WeeklyReporter struct {
	db  *store.DB
	llm llm.Client
}

func NewWeeklyReporter(db *store.DB, lc llm.Client) *WeeklyReporter {
	return &WeeklyReporter{db: db, llm: lc}
}

// GenerateWeeklyReport 生成最近一周的个人周报并入库。
func (w *WeeklyReporter) GenerateWeeklyReport(ctx context.Context, agentID int64, ownerDesc string) error {
	if w.llm == nil {
		return fmt.Errorf("周报: LLM 未配置")
	}

	// 本周一 00:00 到现在
	now := time.Now()
	weekday := int(now.Weekday()) // 周日=0
	if weekday == 0 {
		weekday = 7
	}
	weekStart := time.Date(now.Year(), now.Month(), now.Day()-weekday+1, 0, 0, 0, 0, now.Location())
	weekEnd := weekStart.AddDate(0, 0, 7)

	mems, err := w.db.ListMemoriesSince(ctx, agentID, weekStart)
	if err != nil {
		return fmt.Errorf("取本周记忆: %w", err)
	}
	if len(mems) == 0 {
		slog.Info("weekly report skipped: 本周无记忆", "agent_id", agentID)
		return nil
	}

	// 当前画像作为背景
	curProfile, curState, _ := w.db.GetAgentProfile(ctx, agentID)

	corrections, err := w.db.RecentCorrections(ctx, agentID)
	if err != nil {
		return err
	}
	prompt := buildWeeklyPrompt(ownerDesc, weekStart, weekEnd, mems, corrections, curProfile, curState)
	resp, _, err := w.llm.Complete(ctx, []llm.Message{{Role: "user", Content: prompt}})
	if err != nil {
		return fmt.Errorf("周报 LLM: %w", err)
	}

	// 解析 LLM 输出为 JSON
	s := strings.TrimSpace(resp)
	if i := strings.Index(s, "{"); i >= 0 {
		s = s[i:]
	}
	if i := strings.LastIndex(s, "}"); i >= 0 {
		s = s[:i+1]
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		return fmt.Errorf("周报解析: %w", err)
	}

	contentJSON, _ := json.Marshal(obj)
	id, err := w.db.InsertWeeklyReport(ctx, agentID, weekStart, weekEnd, contentJSON, len(mems))
	if err != nil {
		return fmt.Errorf("周报入库: %w", err)
	}
	slog.Info("weekly report generated", "id", id, "memories", len(mems), "week_start", weekStart.Format("2006-01-02"))
	return nil
}

func buildWeeklyPrompt(ownerDesc string, weekStart, weekEnd time.Time, mems []store.MemText, corrections []store.MemoryRecord, profile, state string) string {
	var sb strings.Builder
	sb.WriteString(`你是个人数字分身的"周报生成器"。基于本周记忆，为分身主人生成一份简洁、真诚、有洞察的周度复盘。

分身主人是：` + ownerDesc + `

【本周时间范围】` + weekStart.Format("1月2日") + ` 至 ` + weekEnd.Format("1月2日") + `

【当前画像背景】
Profile: ` + strings.TrimSpace(profile) + `
State: ` + strings.TrimSpace(state) + `

【本周记忆证据】（按时间升序）
`)
	for _, m := range mems {
		sb.WriteString("- [" + m.Type + "] " + strings.TrimSpace(m.Text) + "\n")
	}

	writeConfirmedCorrections(&sb, corrections)
	sb.WriteString(`
要求：
1. summary：一句话总结本周整体状态（不超过50字）
2. 已确认的纠正用于核实事实，事件仍以本周时间范围为准。themes：本周聊得最多/最重要的2-4个主题（从记忆里归纳，不要编造）
3. mood_trend：本周情绪/状态趋势（一句话，如"整体平稳，周中因面试焦虑，周末放松"）
4. highlights：本周重要事件2-4条（从 episodic 记忆里挑，带具体时间/事件）
5. concerns：本周需要关注的问题或隐忧1-3条（没有就空数组）
6. next_week_suggestions：下周建议1-3条（具体、可执行，不是鸡汤）
7. 真诚、具体、有观察，不要套话；记忆里没有的事不要编
8. 只输出一个 JSON 对象，不要 markdown 标记

格式：
{"summary":"...","themes":["..."],"mood_trend":"...","highlights":["..."],"concerns":["..."],"next_week_suggestions":["..."]}`)
	return sb.String()
}
