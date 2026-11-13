// Package session 实现基于 Redis 的服务端会话与登录限流。
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type Manager struct {
	rdb      *redis.Client
	ttl      time.Duration
	idBytes  int
	prefix   string
}

func NewManager(rdb *redis.Client, ttl time.Duration, idBytes int) *Manager {
	return &Manager{rdb: rdb, ttl: ttl, idBytes: idBytes, prefix: "session:"}
}

// Create 生成新的 session id 并写入 Redis；登录成功后必须重新生成（防会话固定）。
func (m *Manager) Create(ctx context.Context, username string) (string, error) {
	id, err := m.newID()
	if err != nil {
		return "", err
	}
	if err := m.rdb.Set(ctx, m.prefix+id, username, m.ttl).Err(); err != nil {
		return "", err
	}
	return id, nil
}

// Get 校验 session；未命中返回 (username, false)。
func (m *Manager) Get(ctx context.Context, id string) (string, bool) {
	v, err := m.rdb.Get(ctx, m.prefix+id).Result()
	if err != nil {
		return "", false
	}
	return v, true
}

func (m *Manager) Delete(ctx context.Context, id string) {
	_ = m.rdb.Del(ctx, m.prefix+id)
}

func (m *Manager) newID() (string, error) {
	b := make([]byte, m.idBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}
