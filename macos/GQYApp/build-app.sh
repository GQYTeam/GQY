#!/bin/zsh
# 编译一体化桌面壳（内嵌 gqy 二进制 + 图标）并组装成 .app（无需 Xcode）
set -euo pipefail
cd "$(dirname "$0")"

swift build -c release

APP=build/GQYApp.app
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

BIN="$(swift build -c release --show-bin-path)/GQYApp"
cp "$BIN" "$APP/Contents/MacOS/"

# ── 内嵌 gqy 后端（一体化：不依赖系统已装 gqy）─────────────────────
GQY_SOURCE="${GQY_BIN:-}"
if [ -z "$GQY_SOURCE" ] || [ ! -x "$GQY_SOURCE" ]; then
    for cand in /opt/homebrew/bin/gqy /usr/local/bin/gqy ../../gqy/target/release/gqy; do
        if [ -x "$cand" ]; then GQY_SOURCE="$cand"; break; fi
    done
fi
if [ -n "$GQY_SOURCE" ] && [ -x "$GQY_SOURCE" ]; then
    cp "$GQY_SOURCE" "$APP/Contents/Resources/gqy"
    echo "内嵌 gqy: $GQY_SOURCE"
else
    echo "警告: 找不到 gqy 二进制，App 将依赖系统已安装的 gqy"
fi

# ── 内嵌 share 资源（scripts/memes/kb，供内嵌 gqy 按 <exe 向上找>/share/gqy 解析）─
SHARE="$APP/Contents/Resources/share/gqy"
for pair in "gqy/src/scripts:scripts" "gqy/src/memes:memes" "kb:kb"; do
    src="../../${pair%%:*}"; dst="${pair##*:}"
    if [ -d "$src" ]; then
        mkdir -p "$SHARE/$dst"
        cp -R "$src"/* "$SHARE/$dst/" 2>/dev/null || true
        echo "内嵌资源: $dst"
    fi
done

# ── 图标（GQY-icon.png → .icns）────────────────────────────────────
ICON_SRC="../../pics/GQY-icon.png"
if [ -f "$ICON_SRC" ]; then
    ICONSET=build/GQYIcon.iconset
    rm -rf "$ICONSET"
    mkdir -p "$ICONSET"
    for spec in 16 32 128 256 512; do
        sips -z "$spec" "$spec" "$ICON_SRC" --out "$ICONSET/icon_${spec}x${spec}.png" >/dev/null
        sips -z $((spec * 2)) $((spec * 2)) "$ICON_SRC" --out "$ICONSET/icon_${spec}x${spec}@2x.png" >/dev/null
    done
    iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/GQYIcon.icns"
    rm -rf "$ICONSET"
    echo "图标: GQYIcon.icns"
fi

cat > "$APP/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleExecutable</key><string>GQYApp</string>
  <key>CFBundleIdentifier</key><string>dev.gqy.app</string>
  <key>CFBundleName</key><string>顾清影</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>CFBundleIconFile</key><string>GQYIcon</string>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSAppTransportSecurity</key>
  <dict>
    <key>NSAllowsLocalNetworking</key><true/>
  </dict>
</dict>
</plist>
PLIST

codesign --force --deep -s - "$APP" >/dev/null 2>&1 || true
echo "✓ $APP"
# 不自动 open：由调用方决定（发布时 cp 到 /Applications 再启动，避免 build/ 与 /Applications 两份同名 App 并存）
if [ "${GQY_OPEN:-1}" = "1" ]; then
  open "$APP"
fi
