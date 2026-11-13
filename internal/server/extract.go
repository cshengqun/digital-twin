package server

import (
	"context"
	"log/slog"
	"time"
)

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
