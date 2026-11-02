# GQY 应用图标（macOS + Web）

从 `source.jpg` 生成的全套图标资产。**所有 PNG 与 `.icns` 都由脚本生成**，不要手改。

## 产物

| 文件 | 用途 |
| --- | --- |
| `AppIcon.icns` | macOS 应用图标（10 个尺寸，`iconutil` 打包） |
| `AppIcon.appiconset/` | Xcode 资源目录（含 `Contents.json`），拖进工程即用 |
| `AppIcon.iconset/` | `.icns` 的源目录（16–512 的 1x/2x） |
| `icon-1024.png` | 1024 透明圆角母版（可直接用于 dmgn/商店预览） |
| `icon-small-focus-1024.png` | 小尺寸专用母版（面部聚焦） |
| `favicon-32.png` / `apple-touch-icon.png` | Web 图标（32px 圆角 / 180px 不透明方图） |
| `preview.png` | 浅色/深色底、多尺寸对照 |
| `preview-small.png` | 16/32/64px 的「原生缩放 vs 面部聚焦」像素级对比 |
| `source.jpg` | 原图（脚本输入） |

## 设计规则（macOS Big Sur+ 网格）

- 画布 1024×1024，内容区 824×824 居中（四边各 100），圆角 185。
- 内容外透明，交给系统加阴影与高亮。
- **≤64px 使用面部聚焦裁切**：小尺寸下整身像会糊成一团，裁到上半身让脸可读（`--no-small-focus` 可关）。
- 极细内描边（白色 18% alpha），避免浅色插画在浅色桌面上糊边。

## 重新生成

```bash
# 需要 Pillow（macOS 上还需要 iconutil，随 Xcode Command Line Tools 提供）
make icon
# 或自定义源图
python3 scripts/make-icon.py --source path/to/your.jpg --out assets/icons
```

## 用到 .app 里

```xml
<!-- GQY.app/Contents/Info.plist -->
<key>CFBundleIconFile</key>
<string>AppIcon</string>
```

把 `AppIcon.icns` 放进 `GQY.app/Contents/Resources/` 即可；Xcode 工程直接加 `AppIcon.appiconset`。

## 注意

- Web 前端目前用 `app/assets/icon.svg`（品牌「G」字标）作为 favicon；本目录的头像图标用于**应用外壳**（macOS/Win/Linux 打包）。若要统一切换，把 `favicon-32.png` 与 `apple-touch-icon.png` 拷到 `app/assets/` 并改 `index.html` 的 `<link rel="icon">`。
