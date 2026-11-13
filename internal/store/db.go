// Package store 封装 PostgreSQL 与 Redis 的连接、schema 初始化与分区维护。
package store

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// DB 聚合两个连接，供 repo 层使用。
type DB struct {
	PG    *pgxpool.Pool
	Redis *redis.Client
}

func New(ctx context.Context, dsn, redisAddr, redisPass string) (*DB, error) {
	pg, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pg connect: %w", err)
	}
	if err := pg.Ping(ctx); err != nil {
		return nil, fmt.Errorf("pg ping: %w", err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr, Password: redisPass})
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	return &DB{PG: pg, Redis: rdb}, nil
}

// Migrate 按文件名顺序幂等执行 migrations/ 下所有 .sql（所有语句均 IF NOT EXISTS，可安全重复运行）。
func (d *DB) Migrate(ctx context.Context) error {
	entries, err := os.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join("migrations", f))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", f, err)
		}
		if _, err := d.PG.Exec(ctx, string(b)); err != nil {
			return fmt.Errorf("run migration %s: %w", f, err)
		}
		slog.Info("migration applied", "file", f)
	}
	slog.Info("schema ready")
	return nil
}

// EnsurePartitions 为 messages 和 knowledge_chunks 补齐本月与下月的分区。
func (d *DB) EnsurePartitions(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tx, err := d.PG.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin partition maintenance: %w", err)
	}
	defer tx.Rollback(ctx)

	// 用同一个数据库会话确定当前月份并解释分区边界，沿用已有分区的日期写法。
	var monthStart time.Time
	if err := tx.QueryRow(ctx, `SELECT date_trunc('month', now())::date`).Scan(&monthStart); err != nil {
		return fmt.Errorf("get database month: %w", err)
	}
	var created []string
	for _, tbl := range []string{"messages", "knowledge_chunks"} {
		for _, t := range []time.Time{monthStart, monthStart.AddDate(0, 1, 0)} {
			name := fmt.Sprintf("%s_%s", tbl, t.Format("200601"))
			// 已存在时跳过 DDL，避免例行检查申请父表的排他锁。
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists); err != nil {
				return fmt.Errorf("check partition %s: %w", name, err)
			}
			if exists {
				continue
			}
			sql := fmt.Sprintf(
				"CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')",
				name, tbl, t.Format(time.DateOnly), t.AddDate(0, 1, 0).Format(time.DateOnly),
			)
			if _, err := tx.Exec(ctx, sql); err != nil {
				return fmt.Errorf("ensure partition %s: %w", name, err)
			}
			created = append(created, name)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit partition maintenance: %w", err)
	}
	if len(created) > 0 {
		slog.Info("monthly partitions created", "partitions", created)
	}
	return nil
}

// MaintainPartitions 在启动检查后定期续建分区，失败时在下一轮重试。
func (d *DB) MaintainPartitions(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.EnsurePartitions(ctx); err != nil && ctx.Err() == nil {
				slog.Error("maintain monthly partitions", "err", err)
			}
		}
	}
}

func (d *DB) Close() {
	d.PG.Close()
	_ = d.Redis.Close()
}
