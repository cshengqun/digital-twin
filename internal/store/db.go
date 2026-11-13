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

// EnsurePartitions 为按 created_at 月度分区的表创建本月与下月分区。
// 第一版覆盖 messages 与 knowledge_chunks；缺失分区会导致插入报错，因此每次启动都检查。
// 分区边界对齐到自然月 1 号，避免因重启日期不同产生漂移与范围重叠。
func (d *DB) EnsurePartitions(ctx context.Context) error {
	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	tables := []string{"messages", "knowledge_chunks"}
	for _, tbl := range tables {
		for _, t := range []time.Time{monthStart, monthStart.AddDate(0, 1, 0)} {
			name := fmt.Sprintf("%s_%s", tbl, t.Format("200601"))
			from := t.UTC().Format("2006-01-02")
			to := t.AddDate(0, 1, 0).UTC().Format("2006-01-02")
			sql := fmt.Sprintf(
				"CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM ('%s') TO ('%s')",
				name, tbl, from, to,
			)
			if _, err := d.PG.Exec(ctx, sql); err != nil {
				return fmt.Errorf("ensure partition %s: %w", name, err)
			}
		}
	}
	slog.Info("partitions ensured", "tables", strings.Join(tables, ","))
	return nil
}

func (d *DB) Close() {
	d.PG.Close()
	_ = d.Redis.Close()
}
