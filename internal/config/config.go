// Package config 负责从环境变量加载全部运行配置。
// 第一版全部走环境变量（.env 由 docker compose / 用户注入），不引入配置文件解析依赖。
package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	Addr    string        // HTTP 监听地址，如 ":8080"
	BaseURL string        // 对外基础地址，用于 cookie / 跳转
	Env     string        // dev | prod

	PostgresDSN string
	RedisAddr   string
	RedisPass   string

	// 火山方舟（Ark）配置。ARK_API_KEY 为空时，对话走 Mock 模式。
	ArkBaseURL string // 例如 https://ark.cn-beijing.volces.com
	ArkAPIKey  string
	ArkModelID string // 方舟接入点 ID / 模型 ID

	// Phase 1 · 检索与向量（B 方案：Qdrant 自建在服务器上，docker 部署）
	EmbeddingModel string // 方舟 embedding 模型 ID，如 doubao-embedding-text-240715
	QdrantAddr    string // 如 http://localhost:6333（本地开发）/ http://<服务器内网IP>:6333

	// Phase A · 工具：Tavily 搜索（免费额度 1000 次/月）。为空时不注册 web_search 工具。
	TavilyAPIKey string

	// 初始管理员账号：首次启动时若 users 表为空，用这两项写入
	AdminUsername string
	AdminPassword string

	// 会话与安全
	SessionTTL      time.Duration // 登录态有效期
	SessionIDBytes  int
	LoginRateLimit  int           // 每窗口允许失败次数
	LoginRateWindow time.Duration
	LoginLockout    time.Duration

	// 会话边界：距上次消息超过该时长则视为新会话
	ConversationIdleTimeout time.Duration
	// 工作记忆装载：最近 N 条消息原文
	WorkingMemoryMessages int
}

func Load() (*Config, error) {
	c := &Config{
		Addr:    getenv("APP_ADDR", ":8080"),
		BaseURL: strings.TrimRight(getenv("APP_BASE_URL", "http://localhost:8080"), "/"),
		Env:     getenv("APP_ENV", "dev"),

		PostgresDSN: getenv("POSTGRES_DSN", "postgres://twin:twin@localhost:5432/twin?sslmode=disable"),
		RedisAddr:   getenv("REDIS_ADDR", "localhost:6379"),
		RedisPass:   getenv("REDIS_PASSWORD", ""),

		ArkBaseURL: strings.TrimRight(getenv("ARK_BASE_URL", "https://ark.cn-beijing.volces.com"), "/"),
		ArkAPIKey:  os.Getenv("ARK_API_KEY"),
		ArkModelID: os.Getenv("ARK_MODEL_ID"),

		EmbeddingModel: getenv("EMBEDDING_MODEL", "doubao-embedding-large"),
		QdrantAddr:    getenv("QDRANT_ADDR", "http://localhost:6333"),
		TavilyAPIKey:  os.Getenv("TAVILY_API_KEY"),

		AdminUsername: getenv("ADMIN_USERNAME", "admin"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),

		SessionTTL:            7 * 24 * time.Hour,
		SessionIDBytes:        32,
		LoginRateLimit:        5,
		LoginRateWindow:       time.Minute,
		LoginLockout:          15 * time.Minute,
		ConversationIdleTimeout: 30 * time.Minute,
		WorkingMemoryMessages: 40,
	}

	if c.AdminPassword == "" {
		// dev 默认弱口令仅用于本地；prod 必须显式配置
		c.AdminPassword = "change-me-please"
	}
	if c.ArkModelID == "" {
		c.ArkModelID = "doubao-seed-1-250615" // 占位，按你的方舟接入点修改
	}
	if c.Env == "prod" && c.AdminPassword == "change-me-please" {
		return nil, fmt.Errorf("config: prod 环境必须显式设置 ADMIN_PASSWORD")
	}
	return c, nil
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
