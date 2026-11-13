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

浏览器打开 `http://<IP>:8080`（或 `https://twin.example.com`）→ 用 ADMIN 账号登录 → 对话页右上角「知识库」上传第一篇文档试试。

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
- 登录密码走 argon2id + 登录限流；全站除 /login 外都需登录
- 定期 `pg_dump` 备份到本机或对象存储
