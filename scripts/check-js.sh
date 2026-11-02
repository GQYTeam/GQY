#!/usr/bin/env bash
# 语法检查：把 ES modules 复制成 .mjs 后交给 node --check。
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
TMP=$(mktemp -d)
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT

if ! command -v node >/dev/null 2>&1; then
  echo "node 未安装，跳过 JS 语法检查" >&2
  exit 0
fi

count=0
for file in "$ROOT"/app/js/*.js; do
  [ -e "$file" ] || continue
  cp "$file" "$TMP/$(basename "${file%.js}").mjs"
  count=$((count + 1))
done

for file in "$TMP"/*.mjs; do
  node --check "$file"
done
echo "js syntax ok: ${count} files"
