#!/usr/bin/env bash
# 把一篇 Markdown（通常是飞书文档导出的）连同图片发布到博客。
# 用法：bash deploy/blog-import.sh [-u 文章ID] <标题> <正文.md> [图片...]
#   新发：bash deploy/blog-import.sh "我的文章" doc.md images/*.png
#   覆盖更新已发布的第 5 篇：bash deploy/blog-import.sh -u 5 "我的文章" doc.md
# 环境变量：
#   BLOG_IMPORT_TOKEN  必填，服务器 .env 里的同名值
#   BLOG_BASE_URL      默认 https://iamcsq.top
#
# 正文里以图片文件名结尾的 Markdown 图片地址（如 ![](images/a.png)）会被自动改写为站内 URL。
# 更新时服务端不会删除旧文章里已不再引用的图片文件。
set -euo pipefail

UPDATE_ID=""
while getopts "u:" opt; do
  case "$opt" in
    u) UPDATE_ID="$OPTARG" ;;
    *) echo "用法: blog-import.sh [-u 文章ID] <标题> <正文.md> [图片...]" >&2; exit 1 ;;
  esac
done
shift $((OPTIND - 1))
[[ "$UPDATE_ID" =~ ^[0-9]+$ ]] || { echo "-u 后面必须是文章 ID（数字）" >&2; exit 1; }

TITLE="${1:?用法: blog-import.sh [-u 文章ID] <标题> <正文.md> [图片...]}"
FILE="${2:?缺少正文 .md}"
shift 2
BASE="${BLOG_BASE_URL:-https://iamcsq.top}"
: "${BLOG_IMPORT_TOKEN:?请设置 BLOG_IMPORT_TOKEN（服务器 .env 里的同名值）}"
[ -f "$FILE" ] || { echo "找不到文件：$FILE" >&2; exit 1; }

args=(-sS -F "title=$TITLE" -F "format=markdown" -F "content=<$FILE" -H "Authorization: Bearer $BLOG_IMPORT_TOKEN")
[ -n "$UPDATE_ID" ] && args+=(-F "id=$UPDATE_ID")
for img in "$@"; do
  [ -f "$img" ] || { echo "找不到图片：$img" >&2; exit 1; }
  args+=(-F "images=<$img")
done
curl "${args[@]}" "$BASE/api/blogs/import"
echo
