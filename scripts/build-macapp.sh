#!/usr/bin/env bash
# 构建 macOS .app：通用 Go 服务 + 静态 Web UI + WKWebView 壳，产出 dist/GQY.app。
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
APP="$ROOT/app"
DIST="$ROOT/dist"
BUNDLE="$DIST/GQY.app"
CONTENTS="$BUNDLE/Contents"

rm -rf "$BUNDLE"
mkdir -p "$CONTENTS/MacOS" "$CONTENTS/Resources/bin" "$CONTENTS/Resources/web"

echo "== build gqy-server (darwin universal) =="
for arch in arm64 amd64; do
  (cd "$APP" && CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w" -o "$DIST/gqy-server-$arch" ./cmd/gqy-server)
done
lipo -create "$DIST/gqy-server-arm64" "$DIST/gqy-server-amd64" -output "$CONTENTS/Resources/bin/gqy-server"
chmod +x "$CONTENTS/Resources/bin/gqy-server"
rm -f "$DIST/gqy-server-arm64" "$DIST/gqy-server-amd64"

echo "== copy web assets =="
for item in index.html demos.html manifest.webmanifest assets js styles; do
  cp -R "$APP/$item" "$CONTENTS/Resources/web/"
done

echo "== build Swift shell =="
swiftc -O "$ROOT"/desktop/macos/*.swift \
  -framework Cocoa -framework WebKit \
  -o "$CONTENTS/MacOS/GQY"

cp "$ROOT/desktop/macos/Info.plist" "$CONTENTS/Info.plist"

echo "== icon =="
# 优先使用 assets/icons/AppIcon.icns（make icon 产出的头像图标）；没有再从 icon.svg 兜底生成。
if [ -f "$ROOT/assets/icons/AppIcon.icns" ]; then
  cp "$ROOT/assets/icons/AppIcon.icns" "$CONTENTS/Resources/AppIcon.icns"
elif command -v qlmanage >/dev/null 2>&1 && command -v iconutil >/dev/null 2>&1; then
  ICON_TMP=$(mktemp -d)
  if qlmanage -t -s 1024 -o "$ICON_TMP" "$APP/assets/icon.svg" >/dev/null 2>&1 && [ -f "$ICON_TMP/icon.svg.png" ]; then
    mkdir -p "$ICON_TMP/AppIcon.iconset"
    for size in 16 32 128 256 512; do
      sips -z "$size" "$size" "$ICON_TMP/icon.svg.png" --out "$ICON_TMP/AppIcon.iconset/icon_${size}x${size}.png" >/dev/null
      double=$((size * 2))
      sips -z "$double" "$double" "$ICON_TMP/icon.svg.png" --out "$ICON_TMP/AppIcon.iconset/icon_${size}x${size}@2x.png" >/dev/null
    done
    iconutil -c icns "$ICON_TMP/AppIcon.iconset" -o "$CONTENTS/Resources/AppIcon.icns" 2>/dev/null || true
  fi
  rm -rf "$ICON_TMP"
fi

echo "== sign (ad-hoc) =="
xattr -cr "$BUNDLE" 2>/dev/null || true
codesign --force --deep --sign - "$BUNDLE" >/dev/null 2>&1 || true

echo "built: $BUNDLE"
du -sh "$BUNDLE"
