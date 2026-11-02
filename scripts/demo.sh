#!/usr/bin/env bash
# 一键体验：构建、写一份指向本机 Magpie 的示例配置，然后在前台启动服务。
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
APP="$ROOT/app"
MAGPIE_URL=${MAGPIE_URL:-http://127.0.0.1:3425/v1}
MAGPIE_MODEL=${MAGPIE_MODEL:-auto/deepseek-v4.1-flash}
PORT=${PORT:-5173}
HOME_DIR=${GQY_DEMO_HOME:-"$APP/tmp/demo-home"}

(cd "$ROOT" && make build >/dev/null)
mkdir -p "$HOME_DIR"
cat > "$HOME_DIR/providers.json" <<EOF
{
  "providers": [{
    "id": "magpie",
    "display_name": "Magpie · 本机测试",
    "base_url": "${MAGPIE_URL}",
    "protocol": "openai-chat",
    "default_model": "${MAGPIE_MODEL}",
    "models": ["${MAGPIE_MODEL}"],
    "accounts": [{"id": "local", "api_key": ""}],
    "max_concurrency": 2
  }]
}
EOF
printf '{"default":{"provider":"magpie","model":"%s"}}' "$MAGPIE_MODEL" > "$HOME_DIR/routing.json"
printf '{}' > "$HOME_DIR/settings.json"

echo "GQY demo"
echo "  UI      http://127.0.0.1:${PORT}/"
echo "  Demo    http://127.0.0.1:${PORT}/demos.html"
echo "  Home    ${HOME_DIR}"
echo "  Upstream ${MAGPIE_URL} (${MAGPIE_MODEL})"
echo "按 Ctrl+C 停止。"
echo

exec env GQY_HOME="$HOME_DIR" GQY_WEB_DIR="$APP" HOST=127.0.0.1 PORT="$PORT" \
  "$APP/bin/gqy-server"
