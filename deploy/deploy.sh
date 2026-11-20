#!/usr/bin/env bash
# B 方案一键部署：本地编译 → 上传到服务器 → 起数据库 → 安装 systemd 服务
# 用法：bash deploy/deploy.sh <user>@<server-ip> [twin-domain]
# 前提：
#   - 本地已安装 go
#   - 服务器已安装 docker + docker compose 插件（Ubuntu 22.04: apt install docker.io docker-compose-v2）
#   - 服务器已配置 SSH 密钥登录
set -euo pipefail

REMOTE="${1:?用法: deploy.sh user@server-ip [域名]}"
DOMAIN="${2:-}"
DIR=/opt/twin

echo "[1/6] 本地交叉编译 linux/amd64 ..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o /tmp/twin ./cmd/server

echo "[2/6] 上传二进制、页面与迁移脚本 ..."
ssh "$REMOTE" "mkdir -p $DIR"
scp /tmp/twin "$REMOTE:$DIR/twin.next"
ssh "$REMOTE" "chmod 755 $DIR/twin.next && mv $DIR/twin.next $DIR/twin"
scp -r web migrations docker-compose.yml .env.example "$REMOTE:$DIR/"

echo "[3/6] 生成 .env（已存在则跳过）..."
ssh "$REMOTE" "test -f $DIR/.env || cp $DIR/.env.example $DIR/.env"
echo ">>> 下一步需要你手动完成："
echo "    ssh $REMOTE 然后编辑 $DIR/.env，至少填写："
echo "      - POSTGRES_DSN / REDIS_ADDR（127.0.0.1 即可，docker compose 已绑定本机）"
echo "      - REDIS_PASSWORD（与 docker compose 的 REDIS_PASSWORD 一致）"
echo "      - ARK_API_KEY / ARK_MODEL_ID（火山方舟）"
echo "      - ADMIN_PASSWORD（强密码）"
echo "    编辑完执行：cd $DIR && docker compose up -d"

echo "[4/6] 启动数据库三件套（PG + Redis + Qdrant）..."
ssh "$REMOTE" "cd $DIR && docker compose up -d"

echo "[5/6] 安装 systemd 服务 ..."
scp deploy/twin.service "$REMOTE:/etc/systemd/system/twin.service"
ssh "$REMOTE" "systemctl daemon-reload && systemctl enable twin && systemctl restart twin"

if [ -n "$DOMAIN" ]; then
  echo "[6/6] 配置 Caddy HTTPS（域名 $DOMAIN）..."
  sed "s/twin.example.com/$DOMAIN/" deploy/Caddyfile > /tmp/Caddyfile.twin
  scp /tmp/Caddyfile.twin "$REMOTE:/etc/caddy/Caddyfile"
  ssh "$REMOTE" "caddy reload --config /etc/caddy/Caddyfile 2>/dev/null || echo 'caddy 未安装，跳过（可稍后 apt install caddy）'"
else
  echo "[6/6] 未提供域名，跳过 HTTPS。临时访问：http://<服务器IP>:8080"
fi

echo
echo "完成。验证："
echo "  ssh $REMOTE 'systemctl status twin --no-pager'"
echo "  curl -s http://127.0.0.1:8080/login | head -1"
