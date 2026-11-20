# 个人数字分身 Agent（Phase 1）

单用户、Go 全自研编排、火山方舟模型生态的个人数字分身系统。
**Phase 0**：项目骨架 + 登录认证 + Web 对话闭环（SSE 流式）+ 会话管理 + 消息持久化。
**Phase 1**：知识库 RAG（TXT/HTML 入库 + 向量检索）+ 分层记忆抽取 + 混合检索（关键词 + 向量 + RRF）。

## 技术栈

| 层 | 选型 |
|---|---|
| 编排 | Go 1.24（标准库路由 + 自研中间件） |
| 数据库 | PostgreSQL 16（docker 自建，schema 见 `migrations/`） |
| 缓存/会话 | Redis 7（docker 自建） |
| 向量库 | Qdrant（docker 自建，REST 客户端零重依赖） |
| 模型 | 火山方舟（Doubao 对话 / Embedding，一套 Key） |
| 密码 | argon2id |

> 全部组件跑在自己一台服务器上（B 方案）：低成本、数据自持；模型走火山方舟按量。部署见 `deploy/README.md`。

## 目录结构

```
digital-twin/
├── cmd/server/main.go        # 入口：装配 + 优雅停机
├── internal/
│   ├── config/               # 环境变量配置
│   ├── store/                # PG/Redis 连接、schema、分区、repo、argon2id
│   ├── session/              # Redis 服务端会话
│   ├── llm/                  # 火山方舟流式客户端 + Mock
│   └── server/               # 路由、中间件、认证/对话处理器
├── web/                      # 登录页 + 对话页（内联 CSS/JS）
├── migrations/001_init.sql   # 全量表结构
└── docker-compose.yml        # 可选：容器化起 PG+Redis
```

## 快速开始（B 方案：一台服务器全自建）

完整部署见 **`deploy/README.md`**（一键脚本 + systemd + Caddy HTTPS）。本地快速开发：

```bash
# 1) 起三件套（PG + Redis + Qdrant）
docker compose up -d
export PG_PASSWORD=twin REDIS_PASSWORD=twin   # 与 .env 保持一致

# 2) 配置
cp .env.example .env
# .env 至少填：POSTGRES_DSN / REDIS_ADDR / REDIS_PASSWORD / ARK_API_KEY / ARK_MODEL_ID / ADMIN_PASSWORD

# 3) 启动
go run ./cmd/server
```

打开 `http://localhost:8080` 查看个人主页；进入 `http://localhost:8080/twin/` → 登录 → 对话。数字分身页右上角「知识库」支持上传 TXT / Markdown / HTML 或导入网页。

## 已实现

### Phase 0
- [x] 登录认证：argon2id 密码哈希、Redis 会话、HttpOnly/Secure/SameSite Cookie、登录限流（IP+用户名双维度）、防会话固定、审计日志
- [x] 会话管理：显式新建 + 30 分钟超时兜底判定；标题自动生成；最近 40 条工作记忆装载
- [x] 对话闭环：消息落库 → 工作记忆装载 → 火山方舟流式 → SSE 转发 → 回复落库 → 用量记录
- [x] 全量表结构：users/agents/conversations/messages(分区)/memories/memory_relations/knowledge_docs/knowledge_chunks(分区)/scheduled_tasks/usage_records
- [x] Mock 模式：无 Key 全链路可跑通

### Phase 1
- [x] 方舟 Embedding 客户端（/embeddings，批量向量化）
- [x] 文本解析与分块：TXT 直读、HTML（x/net/html）提取正文、按段落+重叠分块（含单测）
- [x] Qdrant 客户端：REST 建集合/upsert/检索/删除，零重依赖
- [x] 知识库入库：上传 TXT/HTML、导入网页 URL（含 SSRF 防护）→ 解析 → 分块 → PG + Qdrant；异步入库 + 状态轮询
- [x] 记忆抽取：会话完成后台异步抽取事实/情景/方法 → PG + Qdrant（向量库 payload 带文本）
- [x] 混合检索：PG pg_trgm 关键词（2-gram 拆词）+ Qdrant 向量（知识库+记忆）→ RRF 融合 → TopK 注入对话
- [x] Web 知识库管理：上传/URL 导入/列表（状态实时刷新）/删除

## 下一步（Phase 2 候选）

- 记忆复核与遗忘调度（decay_at / pending_confirm / archived）
- Rerank 精排开关（召回规模上来后再开）
- 记忆冲突消解与关系图谱（memory_relations 使用）
- 定时任务（scheduled_tasks）落地
