#!/usr/bin/env bash
# UI 冒烟：启动临时服务，用 headless Chrome dump DOM，检查聊天页、命令面板、控制台、供应商、工具、PWA 与 Demo 页。
# 需要 Chrome/Chromium；找不到时跳过（退出 0）。
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
APP="$ROOT/app"
PORT=${UI_SMOKE_PORT:-5188}
WORK=$(mktemp -d)
PID=""
cleanup() {
  if [ -n "$PID" ]; then
    kill "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

CHROME=${CHROME:-}
if [ -z "$CHROME" ]; then
  for candidate in \
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
    "$(command -v google-chrome 2>/dev/null || true)" \
    "$(command -v chromium 2>/dev/null || true)" \
    "$(command -v chromium-browser 2>/dev/null || true)"; do
    if [ -n "$candidate" ] && [ -x "$candidate" ]; then
      CHROME="$candidate"
      break
    fi
  done
fi
if [ -z "$CHROME" ]; then
  echo "未找到 Chrome/Chromium，跳过 UI 冒烟" >&2
  exit 0
fi

(cd "$ROOT" && make build >/dev/null)
mkdir -p "$WORK/home"
cat > "$WORK/home/providers.json" <<'EOF'
{"providers":[{"id":"demo","display_name":"Demo Provider","base_url":"http://127.0.0.1:1/v1","default_model":"demo-model","models":["demo-model"],"accounts":[{"id":"a1","api_key":""}]}]}
EOF
printf '{"default":{"provider":"demo","model":"demo-model"}}' > "$WORK/home/routing.json"
printf '{}' > "$WORK/home/settings.json"

GQY_HOME="$WORK/home" GQY_WEB_DIR="$APP" HOST=127.0.0.1 PORT="$PORT" \
  "$APP/bin/gqy-server" >"$WORK/server.log" 2>&1 &
PID=$!
if ! curl -fsS --retry 30 --retry-delay 1 --retry-connrefused "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1; then
  echo "服务未启动" >&2
  cat "$WORK/server.log" >&2
  exit 1
fi

dump() {
  "$CHROME" --headless=new --disable-gpu --hide-scrollbars --virtual-time-budget=4000 \
    --dump-dom "$1" 2>/dev/null
}

BASE="http://127.0.0.1:$PORT"
echo "== 对话页 =="
DOM=$(dump "$BASE/?onboarded=1&noanim=1&theme=paper")
echo "$DOM" | grep -q 'id="modelPickerLabel"'
echo "$DOM" | grep -q 'id="sessionSearch"'
echo "$DOM" | grep -q 'demo-model'
echo "$DOM" | grep -q 'id="messages"'
echo "$DOM" | grep -q 'id="paletteOverlay"'
echo "$DOM" | grep -q 'id="jumpBottom"'
echo "ok"

curl -fsS "$BASE/manifest.webmanifest" | grep -q '"name": "GQY"'
echo "manifest ok"

echo "== 命令面板 =="
DOM=$(dump "$BASE/?onboarded=1&noanim=1&theme=paper&palette=1")
echo "$DOM" | grep -q 'id="paletteList"'
echo "$DOM" | grep -q '控制台 · 用量'
echo "ok"

echo "== 控制台 =="
DOM=$(dump "$BASE/?onboarded=1&noanim=1&theme=paper&console=usage")
echo "$DOM" | grep -q 'id="consoleNav"'
echo "$DOM" | grep -q '本会话 tokens'
echo "$DOM" | grep -q 'id="consoleBody"'
echo "ok"

echo "== 供应商面板 =="
DOM=$(dump "$BASE/?onboarded=1&noanim=1&theme=paper&console=providers")
echo "$DOM" | grep -q 'Demo Provider'
echo "$DOM" | grep -q 'id="consoleBody"'
echo "ok"

echo "== 工具面板 =="
DOM=$(dump "$BASE/?onboarded=1&noanim=1&theme=paper&console=tools")
echo "$DOM" | grep -q 'id="consoleBody"'
echo "$DOM" | grep -q 'read'
echo "ok"

echo "== 视觉 Demo =="
DOM=$(dump "$BASE/demos.html")
echo "$DOM" | grep -q 'data-theme="dawn"'
echo "$DOM" | grep -q 'data-theme="dusk"'
echo "$DOM" | grep -q '六种工作台气质'
echo "ok"

echo "ui smoke passed: $BASE"
