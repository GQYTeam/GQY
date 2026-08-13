#!/bin/zsh
# 一键安装顾清影克隆音色 TTS（Qwen3-TTS + mlx-audio，Apple Silicon 本地）
#
# 用法：
#   源码开发:  gqy/src/scripts/tts-setup.sh
#              # venv 建在仓库 gqy/venv，脚本已在 src/scripts/（解析器自动找到）
#   App/CLI:   gqy/src/scripts/tts-setup.sh <GQY_HOME>
#              # 脚本 + venv 装到 GQY_HOME（随备份迁移，换机 restore 后仍在）
#
# 装完还需准备参考音频（音色克隆三要素之二，缺一不可）：
#   ① 参考音频：5-15 秒干净人声 wav
#      · 放到 <目标>/assets/voice/ref.wav（默认位置）
#      · 或设置环境变量 GQY_TTS_REF=<任意路径>
#   ② 参考文本：录音里的原话（逐字一致），设置 GQY_TTS_REF_TEXT
# 首次合成自动下载模型（mlx-community/Qwen3-TTS-12Hz-1.7B-Base-6bit，约 1GB，走 hf-mirror）。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

TARGET="$1"
if [ -z "$TARGET" ]; then
    TARGET="$REPO_ROOT/gqy"
    echo "==> 源码模式：venv 建在 $TARGET/venv"
else
    echo "==> 自托管模式：安装到 $TARGET"
    mkdir -p "$TARGET/scripts"
    cp "$SCRIPT_DIR/tts-server.py" "$TARGET/scripts/tts-server.py"
    chmod +x "$TARGET/scripts/tts-server.py"
fi

if [ ! -x "$TARGET/venv/bin/python" ]; then
    echo "==> 创建 venv ..."
    python3 -m venv "$TARGET/venv"
fi

echo "==> 安装 mlx-audio（首次较慢）..."
"$TARGET/venv/bin/pip" install --upgrade pip --quiet
"$TARGET/venv/bin/pip" install mlx-audio

mkdir -p "$TARGET/assets/voice"
echo ""
echo "✅ TTS 环境就绪：$TARGET/venv"
echo ""
echo "下一步（音色克隆三要素）："
echo "  ① 参考音频 → $TARGET/assets/voice/ref.wav"
echo "     （5-15 秒干净人声；或设 GQY_TTS_REF=<任意 wav>）"
echo "  ② 参考文本 → 设置 GQY_TTS_REF_TEXT="录音里的原话（逐字一致）""
echo "  ③ 模型 → 首次合成自动下载（约 1GB，走 hf-mirror）"
echo ""
echo "试听：gqy tts --clone "你好，我是顾清影""
