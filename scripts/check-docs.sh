#!/usr/bin/env bash
# 文档编号与一致性检查（DR1–DR4）：施工单/设计文档编号唯一、索引与文件一致、状态合法。
# 不通过直接退出非零，make check 会红。
set -uo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
CDIR="$ROOT/docs/construction"
DDIR="$ROOT/docs/designs"
fail=0
err() { printf 'docs: %s\n' "$*" >&2; fail=1; }

# 1) 施工单：NNN- 前缀，编号唯一
for f in "$CDIR"/*.md; do
  base=$(basename "$f")
  [ "$base" = "README.md" ] && continue
  case "$base" in
    [0-9][0-9][0-9]-*.md) ;;
    *) err "施工单命名不合规（应为 NNN-名称.md）: $base" ;;
  esac
done
dups=$(ls "$CDIR"/*.md 2>/dev/null | xargs -n1 basename | grep -E '^[0-9]{3}-' | cut -d- -f1 | sort | uniq -d)
[ -n "$dups" ] && err "施工单编号重复: $(echo "$dups" | tr '\n' ' ')"

# 2) 索引必须列出每个施工单，且不能列出不存在的
while IFS= read -r f; do
  base=$(basename "$f")
  [ "$base" = "README.md" ] && continue
  grep -qF "\`$base\`" "$CDIR/README.md" || err "施工单未登记到 index: $base"
done < <(ls "$CDIR"/*.md 2>/dev/null)
while IFS= read -r name; do
  [ -f "$CDIR/$name" ] || err "index 指向不存在的施工单: $name"
done < <(grep -oE '`[0-9]{3}-[^`]+\.md`' "$CDIR/README.md" | tr -d '`' | sort -u)

# 3) 状态字段合法
while IFS= read -r f; do
  base=$(basename "$f")
  [ "$base" = "README.md" ] && continue
  status=$(grep -m1 '^状态：' "$f" | sed 's/^状态：//')
  case "$status" in
    待开工*|进行中*|施工中*|完成*|已作废*) ;;
    *) err "施工单状态缺失或非法（$base）：${status:-<空>}" ;;
  esac
done < <(ls "$CDIR"/*.md 2>/dev/null)

# 4) 设计文档：NN- 前缀，编号唯一
for f in "$DDIR"/*.md; do
  base=$(basename "$f")
  [ "$base" = "README.md" ] && continue
  case "$base" in
    [0-9][0-9]-*.md) ;;
    *) err "设计文档命名不合规（应为 NN-名称.md）: $base" ;;
  esac
done
ddups=$(ls "$DDIR"/*.md 2>/dev/null | xargs -n1 basename | grep -E '^[0-9]{2}-' | cut -d- -f1 | sort | uniq -d)
[ -n "$ddups" ] && err "设计文档编号重复: $(echo "$ddups" | tr '\n' ' ')"

if [ "$fail" -ne 0 ]; then
  echo "docs: 检查失败（见上）" >&2
  exit 1
fi
echo "docs: 编号与索引检查通过"
