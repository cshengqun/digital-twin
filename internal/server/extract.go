package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"digital-twin/internal/llm"
	"digital-twin/internal/rag"
	"digital-twin/internal/store"
)

// TriggerConversationExtract 对单个会话做整段记忆抽取（异步，不阻塞调用方；带防重标记）。
// 输入 = 对话过程中维护的滚动摘要（若有）+ 最近 500 条原文，保证超长会话早期信息不硬丢。
func TriggerConversationExtract(extractor *rag.MemoryExtractor, db *store.DB, agentID int64, ownerDesc string, convID int64) {
	if extractor == nil {
		return
	}
	go func() {
		ectx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()

		// 先读会话，拿对话过程中维护的滚动摘要
		conv, err := db.GetConversation(ectx, convID)
		if err != nil {
			slog.Warn("conv extract get conv", "conv", convID, "err", err)
			return
		}

		msgs, err := db.RecentMessages(ectx, convID, 500)
		if err != nil {
			slog.Warn("conv extract msgs", "conv", convID, "err", err)
			return
		}
		llmMsgs := make([]llm.Message, 0, len(msgs)+1)
		// 摘要作为第一条 system message 注入（对话时超过 40 条窗口就会滚动维护）
		if conv.Summary != "" {
			llmMsgs = append(llmMsgs, llm.Message{
				Role:    "system",
				Content: "【本会话早期内容摘要（时间较早，从中抽取仍成立的长期记忆）】\n" + conv.Summary,
			})
		}
		for _, m := range msgs {
			llmMsgs = append(llmMsgs, llm.Message{Role: m.Role, Content: m.Content})
		}
		if _, err := extractor.ExtractConversation(ectx, agentID, ownerDesc, fmt.Sprintf("conversation:%d", convID), llmMsgs); err != nil {
			slog.Warn("memory extract", "conv", convID, "err", err)
			return
		}
		_ = db.MarkConversationExtracted(ectx, convID)
	}()
}

// TriggerJournalDigest 异步消化一篇日志：只抽取三类记忆入库。
// 画像重建不再随日志触发，由 StartProfileRebuilder 每天定时执行。
// 幂等（digest_at 防重）；抽取失败会撤销标记以便重试。
func TriggerJournalDigest(extractor *rag.MemoryExtractor, db *store.DB, agentID int64, ownerDesc string, journalID int64) {
	if extractor == nil {
		return
	}
	go func() {
		ectx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		claimed, err := db.ClaimJournalDigest(ectx, journalID)
		if err != nil || !claimed {
			return // 已消化或不存在
		}
		content, err := db.GetJournalContent(ectx, journalID)
		if err != nil {
			slog.Warn("journal content", "journal", journalID, "err", err)
			return
		}
		n, err := extractor.ExtractJournal(ectx, agentID, ownerDesc, content)
		if err != nil {
			slog.Warn("journal extract", "journal", journalID, "err", err)
			_ = db.UnclaimJournalDigest(ectx, journalID) // 失败可重试
			return
		}
		slog.Info("journal digested", "journal", journalID, "memories", n)
	}()
}

// StartProfileRebuilder 每天凌晨 02:30 定时重建一次画像（Profile + State）：
// 基于"当时的全量记忆（含当天会话超时归档的）+ 最近日志"重新归纳。
// 无论当天是否写过日记都会执行；每次全量覆盖旧画像。
func (s *Server) StartProfileRebuilder(ctx context.Context) {
	if s.extractor == nil {
		slog.Warn("profile rebuilder skipped (extractor 未配置)")
		return
	}
	go func() {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), 2, 30, 0, 0, time.Local)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		slog.Info("profile rebuilder started", "next", next.Format("2006-01-02 15:04"))
		t := time.NewTimer(time.Until(next))
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
				if err := s.extractor.BuildProfile(pctx, s.agentID, s.ownerDesc); err != nil {
					slog.Warn("profile daily rebuild", "err", err)
				} else {
					slog.Info("profile daily rebuilt")
				}
				cancel()
				t.Reset(24 * time.Hour)
			}
		}
	}()
}

// StartConversationReaper 后台扫描器：每 60s 找一批"已超时且未抽取"的会话自动收尾抽取，
// 保证会话 30 分钟无活动后记忆一定能归档（即使主人不再回来碰它）。
func (s *Server) StartConversationReaper(ctx context.Context, idle time.Duration) {
	if s.extractor == nil {
		slog.Warn("conversation reaper skipped (extractor 未配置)")
		return
	}
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		slog.Info("conversation reaper started", "idle", idle.String())
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ids, err := s.db.ExpiredConversationsToExtract(ctx, idle, 20)
				if err != nil {
					slog.Warn("reaper scan", "err", err)
					continue
				}
				for _, id := range ids {
					slog.Info("reaper extract", "conv", id)
					TriggerConversationExtract(s.extractor, s.db, s.agentID, s.ownerDesc, id)
				}
			}
		}
	}()
}

// StartWeeklyReporter 每周日晚 20:00 自动生成个人周报。
func (s *Server) StartWeeklyReporter(ctx context.Context) {
	if s.weeklyReporter == nil {
		slog.Warn("weekly reporter skipped (未配置)")
		return
	}
	go func() {
		next := nextSunday8PM(time.Now())
		slog.Info("weekly reporter started", "next", next.Format("2006-01-02 15:04"))
		t := time.NewTimer(time.Until(next))
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				wctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				if err := s.weeklyReporter.GenerateWeeklyReport(wctx, s.agentID, s.ownerDesc); err != nil {
					slog.Warn("weekly report generate", "err", err)
				} else {
					slog.Info("weekly report done")
				}
				cancel()
				t.Reset(7 * 24 * time.Hour)
			}
		}
	}()
}

// nextSunday8PM 计算从 now 起下一个周日 20:00。
func nextSunday8PM(now time.Time) time.Time {
	weekday := int(now.Weekday())
	daysUntilSunday := (7 - weekday) % 7
	candidate := time.Date(now.Year(), now.Month(), now.Day()+daysUntilSunday, 20, 0, 0, 0, now.Location())
	if !candidate.After(now) {
		candidate = candidate.AddDate(0, 0, 7)
	}
	return candidate
}
