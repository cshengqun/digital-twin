# B 方案部署手册（一台 4核8G+40G 服务器自建全部组件）

目标形态：**你的 Go 服务 + PostgreSQL + Redis + Qdrant 全部跑在一台云服务器上**（docker 起三件套，Go 服务由 systemd 守护），前端走 Caddy 自动 HTTPS。全部数据都在自己服务器上，月成本 ≈ 服务器价格。

## 一、服务器准备（以火山 ECS 为例）

1. 创建 ECS：Ubuntu 22.04，4核8G，40G 系统盘，地域随意（华南广州离深圳近）
2. 安全组放行：`22`（SSH）、`80`、`443`（有域名时）；`8080` 可选（无域名临时访问）
3. 拿到公网 IP，配置 SSH 密钥登录（推荐）或强密码
4. SSH 到服务器安装基础软件：

```bash
ssh ubuntu@<IP>
sudo apt update && sudo apt install -y docker.io docker-compose-v2 caddy
sudo systemctl enable --now docker
```

## 二、本地编译并上传（在你自己电脑上执行）

```bash
cd digital-twin
bash deploy/deploy.sh ubuntu@<服务器IP>            # 不带域名
bash deploy/deploy.sh ubuntu@<服务器IP> twin.example.com   # 带域名自动配 HTTPS
```

## 三、首次必做：填写 .env

```bash
ssh ubuntu@<服务器IP>
sudo nano /opt/twin/.env
```

至少填写：

```dotenv
POSTGRES_DSN=postgres://twin:<PG密码>@127.0.0.1:5432/twin?sslmode=disable
REDIS_ADDR=127.0.0.1:6379
REDIS_PASSWORD=<Redis密码>          # 必须与 docker compose 的 REDIS_PASSWORD 一致
ARK_API_KEY=<方舟Key>
ARK_MODEL_ID=<方舟接入点ID>
ADMIN_PASSWORD=<登录强密码>
QDRANT_ADDR=http://127.0.0.1:6333
EMBEDDING_MODEL=doubao-embedding-text-240715
```

> docker compose 里 PG/Redis 密码从环境变量读取，所以启动前先在同一 shell 里
> `cd /opt/twin && export $(grep -v '^#' .env | xargs) && docker compose up -d`
> （deploy.sh 第 4 步已执行；若你改过 .env 里的密码，重新 export 后再 up 一次）

## 四、验证

```bash
sudo systemctl status twin --no-pager        # 服务状态
curl -s http://127.0.0.1:8080/login | head -1   # 页面可达
docker compose -f /opt/twin/docker-compose.yml ps  # 三件套健康
```

浏览器打开 `http://<IP>:8080`（或 `https://twin.example.com`）查看个人主页；进入 `/twin/` → 用 ADMIN 账号登录 → 对话页右上角「知识库」上传第一篇文档试试。

## 五、运维

| 操作 | 命令 |
|---|---|
| 查看日志 | `sudo journalctl -u twin -f` |
| 重启服务 | `sudo systemctl restart twin` |
| 更新版本 | 本地改代码 → `bash deploy/deploy.sh ...`（自动重编译+上传+重启） |
| 备份数据库 | `docker compose exec postgres pg_dump -U twin twin > backup.sql`（建议 cron 定时备份） |
| 磁盘监控 | `df -h /`，数据在 docker volume 里（`/var/lib/docker/volumes`） |

## 六、容量预期

按个人使用量（对话 + 文本知识库），**40G 磁盘 10 年+ 无压力**；若上传大量大文件，挂一块数据盘把 docker 数据目录挪过去即可。详见主 README。

## 七、安全提醒

- 服务器只对公网开放 22/80/443；PG/Redis/Qdrant 只绑定 127.0.0.1（docker-compose 已配）
- 登录密码走 argon2id + 登录限流；个人主页公开，数字分身页面与业务 API 均需登录
- 定期 `pg_dump` 备份到本机或对象存储

## 八、博客：Markdown、图片与飞书导入

- 文章以 **Markdown** 存储和渲染（marked + DOMPurify，库已内置于 `web/assets/`，不走 CDN）；早期的纯文本文章按原样显示。
- 编辑器（`/twin/` → Blogs）左侧写、右侧实时预览；图片可拖拽或粘贴进编辑框自动上传，也可「Import .md」导入本地 Markdown 文件。
- 图片落盘在 `BLOG_MEDIA_DIR`（默认 `media/blog`，即 `/opt/twin/media/blog`），通过 `/media/blog/<目录>/<文件名>` 公开只读，只放行 PNG/JPEG/GIF/WebP。部署脚本不会动这个目录。

**从飞书导入**（在本机执行，正文为飞书导出的 Markdown，图片一并传入）：

```bash
# 服务器 .env 里设置 BLOG_IMPORT_TOKEN（openssl rand -hex 24 生成），重启服务生效
BLOG_IMPORT_TOKEN=<token> bash deploy/blog-import.sh "文章标题" doc.md images/*.png
```

脚本调用 `POST /api/blogs/import`（Bearer Token 鉴权，未配置 token 时接口返回 404）。正文中以图片文件名结尾的地址（如 `![](images/a.png)`）会被改写为站内 URL。发布后的学习和知识索引与网页发布完全一致。

**修改已发布的文章**（博客没有删除接口，改内容用覆盖更新，不要再发一篇）：

```bash
# 覆盖更新第 5 篇（内容、图片处理与新发一致；旧图片文件不会被清理）
bash deploy/blog-import.sh -u 5 "文章标题" doc.md
```

- 网页端：`/blogs/?id=N` 登录后标题下有「Edit」，或 `/twin/` → Blogs 列表里每篇都有「Edit」，保存走 `PUT /api/blogs/{id}`。
- 保存后知识库会按新正文**重新分块并替换旧向量**（分身回答用的是新内容）；日志侧的记忆**不会重抽**（memories 按 source_ref 追加写入，重跑会重复）。
- 后台正在学习/索引中的文章会返回 409，稍后再保存即可。

**写作模板与正文样式**：编辑器工具栏的「Insert template…」提供技术方案 / 分析报告 / 读书笔记 / 随笔四种骨架（Markdown，可自由增删）。正文支持 GitHub 风格提示框：

```markdown
> [!NOTE] 可选标题
> 内容。类型：NOTE / TIP / IMPORTANT / WARNING / CAUTION
```

**目录导航**：文章页自动从正文的 h1/h2/h3 生成目录（少于两个小节则不显示）。窗口 ≥1280px 时固定在正文左侧留白并随滚动高亮当前小节，更窄时收成标题下方的「目录」折叠块；锚点形如 `#s-<标题>`，可直接分享带锚点的链接。

页面与 `/assets/` 均下发 `Cache-Control: no-cache`（每次条件请求，未变化返回 304），改前端文件后 scp 覆盖即可生效，无需让访客强刷。
