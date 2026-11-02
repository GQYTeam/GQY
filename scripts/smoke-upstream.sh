#!/usr/bin/env bash
# 对任意 OpenAI 兼容上游做一次端到端冒烟：网关 → 上游 → 三种对外协议。
# 默认指向本机 Magpie（http://127.0.0.1:3425/v1）。
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
APP="$ROOT/app"
UPSTREAM_BASE_URL=${UPSTREAM_BASE_URL:-http://127.0.0.1:3425/v1}
UPSTREAM_MODEL=${UPSTREAM_MODEL:-auto/deepseek-v4.1-flash}
UPSTREAM_API_KEY=${UPSTREAM_API_KEY:-}
PORT=${PORT:-5181}
TOKEN=${GQY_AUTH_TOKEN:-local-smoke-token}

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

(cd "$ROOT" && make build >/dev/null)
mkdir -p "$WORK/home"
cat > "$WORK/home/providers.json" <<EOF
{
  "providers": [{
    "id": "upstream",
    "display_name": "Upstream",
    "base_url": "${UPSTREAM_BASE_URL}",
    "protocol": "openai-chat",
    "default_model": "${UPSTREAM_MODEL}",
    "models": ["${UPSTREAM_MODEL}"],
    "accounts": [{"id": "local", "api_key": "${UPSTREAM_API_KEY}"}],
    "max_concurrency": 2
  }]
}
EOF
printf '{"default":{"provider":"upstream","model":"%s"}}' "$UPSTREAM_MODEL" > "$WORK/home/routing.json"
printf '{}' > "$WORK/home/settings.json"

GQY_HOME="$WORK/home" HOST=127.0.0.1 PORT="$PORT" GQY_AUTH_TOKEN="$TOKEN" \
  "$APP/bin/gqy-server" >"$WORK/server.log" 2>&1 &
PID=$!

BASE="http://127.0.0.1:${PORT}"
AUTH="Authorization: Bearer ${TOKEN}"
CT="Content-Type: application/json"

if ! curl -fsS --retry 30 --retry-delay 1 --retry-connrefused "${BASE}/healthz" >/dev/null 2>&1; then
  echo "gqy-server 未启动：" >&2
  cat "$WORK/server.log" >&2
  exit 1
fi

MODEL="upstream/${UPSTREAM_MODEL}"
echo "== /v1/models =="
curl -fsS -H "$AUTH" "${BASE}/v1/models" | grep -q '"object":"list"'
echo "ok"

echo "== /v1/chat/completions 非流式 =="
NONSTREAM=$(curl -fsS -m 120 -H "$AUTH" -H "$CT" \
  -d "{\"model\":\"${MODEL}\",\"messages\":[{\"role\":\"user\",\"content\":\"只回复两个字：你好\"}],\"max_tokens\":32}" \
  "${BASE}/v1/chat/completions")
echo "$NONSTREAM" | grep -q '"object":"chat.completion"'
echo "$NONSTREAM" | grep -q '"usage"'
echo "ok"

echo "== /v1/chat/completions 流式 =="
STREAM=$(curl -fsS -N -m 120 -H "$AUTH" -H "$CT" \
  -d "{\"model\":\"${MODEL}\",\"messages\":[{\"role\":\"user\",\"content\":\"只回复两个字：你好\"}],\"max_tokens\":32,\"stream\":true}" \
  "${BASE}/v1/chat/completions")
echo "$STREAM" | grep -q 'chat.completion.chunk'
echo "$STREAM" | grep -q 'data: \[DONE\]'
echo "ok"

echo "== /v1/messages =="
ANTHROPIC=$(curl -fsS -m 120 -H "$AUTH" -H "$CT" \
  -d "{\"model\":\"${MODEL}\",\"max_tokens\":32,\"messages\":[{\"role\":\"user\",\"content\":\"只回复两个字：你好\"}]}" \
  "${BASE}/v1/messages")
echo "$ANTHROPIC" | grep -q '"type":"message"'
echo "$ANTHROPIC" | grep -q '"stop_reason"'
echo "ok"

echo "== /v1beta generateContent =="
GEMINI=$(curl -fsS -m 120 -H "$AUTH" -H "$CT" \
  -d '{"contents":[{"role":"user","parts":[{"text":"只回复两个字：你好"}]}],"generationConfig":{"maxOutputTokens":32}}' \
  "${BASE}/v1beta/models/${MODEL}:generateContent")
echo "$GEMINI" | grep -q '"candidates"'
echo "$GEMINI" | grep -q '"finishReason"'
echo "ok"

echo "== 请求日志 =="
grep -q '"provider":"upstream"' "$WORK/home/gateway/requests.jsonl"
echo "ok"

echo "live smoke passed: ${UPSTREAM_BASE_URL} (${UPSTREAM_MODEL})"
