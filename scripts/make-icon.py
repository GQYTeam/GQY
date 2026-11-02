#!/usr/bin/env python3
"""make-icon：从一张方图生成 macOS 应用图标全集与 Web 图标。

用法：
    python3 scripts/make-icon.py [--source assets/icons/source.jpg] [--out assets/icons]

产出（默认 out=assets/icons）：
    icon-1024.png            透明圆角母版（macOS 网格：1024 画布，824 内容，圆角 185）
    AppIcon.iconset/         10 个尺寸（16–512 @1x/@2x）
    AppIcon.icns             由 iconutil 打包（macOS）
    AppIcon.appiconset/      Xcode 资源目录（含 Contents.json）
    favicon-32.png           网页 favicon
    apple-touch-icon.png     180px 不透明方图（iOS 自加圆角）
    preview.png              多尺寸对照预览（浅色/深色棋盘）

依赖：Pillow；iconutil 仅在 macOS 上可用（缺失时跳过 .icns，其余照常产出）。
"""

from __future__ import annotations

import argparse
import json
import shutil
import subprocess
import sys
from pathlib import Path

try:
    from PIL import Image, ImageDraw
except ImportError:  # pragma: no cover - 环境提示
    print("需要 Pillow：python3 -m pip install pillow（或用带 Pillow 的解释器）", file=__import__("sys").stderr)
    raise SystemExit(2)

# macOS Big Sur+ 图标网格（以 1024 画布计）：内容 824×824，四周 100，圆角约 185.4。
CANVAS = 1024
MARGIN = 100
CONTENT = CANVAS - MARGIN * 2
RADIUS = 185

ICONSET_SIZES = [(16, 1), (16, 2), (32, 1), (32, 2), (128, 1), (128, 2), (256, 1), (256, 2), (512, 1), (512, 2)]


def load_source(path: Path) -> Image.Image:
    img = Image.open(path).convert("RGB")
    side = min(img.size)
    left = (img.width - side) // 2
    top = (img.height - side) // 2
    return img.crop((left, top, left + side, top + side))


def rounded_mask(size: int, radius: int) -> Image.Image:
    mask = Image.new("L", (size, size), 0)
    draw = ImageDraw.Draw(mask)
    draw.rounded_rectangle((0, 0, size - 1, size - 1), radius=radius, fill=255)
    return mask


def build_master(src: Image.Image) -> Image.Image:
    """生成 1024 透明圆角母版：内容 824 居中，圆角 185。"""
    content = src.resize((CONTENT, CONTENT), Image.LANCZOS)
    master = Image.new("RGBA", (CANVAS, CANVAS), (0, 0, 0, 0))
    mask = rounded_mask(CONTENT, RADIUS)
    master.paste(content, (MARGIN, MARGIN), mask)
    # 极细内描边，避免浅色插画在浅色桌面上"糊边"。
    draw = ImageDraw.Draw(master)
    draw.rounded_rectangle(
        (MARGIN, MARGIN, CANVAS - MARGIN - 1, CANVAS - MARGIN - 1),
        radius=RADIUS,
        outline=(255, 255, 255, 46),
        width=2,
    )
    return master


def focus_source(src: Image.Image) -> Image.Image:
    """小尺寸用面部聚焦：裁掉下部裙摆，脸部占比更大，16/32px 仍能读成"一个人脸"。"""
    w, h = src.size
    crop_h = int(h * 0.78)
    left = int(w * 0.12)
    return src.crop((left, 0, w - left, crop_h)).resize((w, w), Image.LANCZOS)


def resized(master: Image.Image, px: int) -> Image.Image:
    return master.resize((px, px), Image.LANCZOS)


SMALL_LIMIT = 64


def variant_for(master: Image.Image, small_master: Image.Image | None, px: int) -> Image.Image:
    if small_master is not None and px <= SMALL_LIMIT:
        return resized(small_master, px)
    return resized(master, px)


def write_iconset(master: Image.Image, out: Path, small_master: Image.Image | None = None) -> Path:
    iconset = out / "AppIcon.iconset"
    if iconset.exists():
        shutil.rmtree(iconset)
    iconset.mkdir(parents=True)
    for size, scale in ICONSET_SIZES:
        px = size * scale
        name = f"icon_{size}x{size}@{scale}x.png" if scale > 1 else f"icon_{size}x{size}.png"
        variant_for(master, small_master, px).save(iconset / name)
    return iconset


def write_appiconset(master: Image.Image, out: Path, small_master: Image.Image | None = None) -> Path:
    appiconset = out / "AppIcon.appiconset"
    if appiconset.exists():
        shutil.rmtree(appiconset)
    appiconset.mkdir(parents=True)
    images = []
    for size, scale in ICONSET_SIZES:
        px = size * scale
        name = f"icon_{size}x{size}@{scale}x.png" if scale > 1 else f"icon_{size}x{size}.png"
        variant_for(master, small_master, px).save(appiconset / name)
        images.append({
            "filename": name,
            "idiom": "mac",
            "scale": f"{scale}x",
            "size": f"{size}x{size}",
        })
    (appiconset / "Contents.json").write_text(
        json.dumps({"images": images, "info": {"author": "gqy", "version": 1}}, ensure_ascii=False, indent=2) + "\n"
    )
    return appiconset


def write_icns(iconset: Path, out: Path) -> bool:
    if shutil.which("iconutil") is None:
        return False
    icns = out / "AppIcon.icns"
    if icns.exists():
        icns.unlink()
    proc = subprocess.run(
        ["iconutil", "-c", "icns", str(iconset), "-o", str(icns)],
        capture_output=True,
        text=True,
    )
    return proc.returncode == 0 and icns.exists()


def opaque_square(src: Image.Image, px: int) -> Image.Image:
    """iOS 触摸图标：不透明方图（系统自己加圆角）。"""
    return src.resize((px, px), Image.LANCZOS).convert("RGB")


def write_web(master: Image.Image, src: Image.Image, out: Path) -> tuple[Path, Path]:
    favicon = out / "favicon-32.png"
    resized(master, 32).save(favicon)
    touch = out / "apple-touch-icon.png"
    opaque_square(src, 180).save(touch)
    return favicon, touch


def label(size: tuple[int, int], text: str) -> Image.Image:
    img = Image.new("RGB", size, (250, 250, 250))
    draw = ImageDraw.Draw(img)
    draw.text((8, size[1] - 18), text, fill=(90, 90, 90))
    return img


def write_preview(master: Image.Image, small_master: Image.Image | None, out: Path) -> Path:
    """多尺寸对照预览：浅底 / 深底两行，1024 按 50% 缩放展示。"""
    # (实际尺寸, 展示尺寸)
    sizes = [(1024, 512), (512, 512), (256, 256), (128, 128), (64, 64), (32, 32), (16, 16)]
    pad = 28
    band_h = 560
    title_h = 44
    width = sum(shown for _, shown in sizes) + pad * (len(sizes) + 1)
    height = title_h + band_h * 2 + pad
    stage = Image.new("RGB", (width, height), (250, 249, 247))
    light = Image.new("RGB", (width, band_h), (245, 243, 240))
    dark = Image.new("RGB", (width, band_h), (24, 26, 30))
    stage.paste(light, (0, title_h))
    stage.paste(dark, (0, title_h + band_h))

    def checker(size: int, tone: int) -> Image.Image:
        tile = 12
        img = Image.new("RGB", (size, size), (tone, tone, tone))
        draw = ImageDraw.Draw(img)
        alt = tone - 10 if tone > 128 else tone + 10
        for y in range(0, size, tile):
            for x in range(0, size, tile):
                if (x // tile + y // tile) % 2 == 0:
                    draw.rectangle((x, y, x + tile, y + tile), fill=(alt, alt, alt))
        return img

    draw = ImageDraw.Draw(stage)
    draw.text((pad, 14), "GQY app icon - macOS squircle set (1024 shown at 50%; checker = transparent)", fill=(60, 60, 60))

    x = pad
    for actual, shown in sizes:
        icon = resized(master, shown) if (small_master is None or actual > SMALL_LIMIT) else resized(small_master, shown)
        for band_top, tone in ((title_h, 245), (title_h + band_h, 24)):
            y = band_top + (band_h - shown) // 2 - 14
            stage.paste(checker(shown, tone), (x, y))
            stage.paste(icon, (x, y), icon)
            label = f"{actual}px" if actual == shown else f"{actual}px (50% preview)"
            color = (70, 70, 70) if tone > 128 else (200, 200, 200)
            draw.text((x, y + shown + 8), label, fill=color)
        x += shown + pad

    path = out / "preview.png"
    stage.save(path)
    if small_master is not None:
        write_small_zoom(master, small_master, out)
    return path


def write_small_zoom(master: Image.Image, small_master: Image.Image, out: Path) -> None:
    """16/32/64 的「原生缩放」vs「面部聚焦」并排对比（像素放大，检查可读性）。"""
    sizes = [16, 32, 64]
    cell = 190
    pad = 24
    label_w = 64
    cols = [("actual", master), ("face-focus", small_master)]
    width = pad + label_w + len(cols) * (cell + pad)
    height = pad + 30 + len(sizes) * (cell + pad) - pad
    stage = Image.new("RGB", (width, height), (250, 249, 247))
    draw = ImageDraw.Draw(stage)
    draw.text((pad, 10), "small sizes: actual vs face-focus (pixel-zoomed, transparent = checker)", fill=(60, 60, 60))

    for col, (name, source) in enumerate(cols):
        x0 = pad + label_w + col * (cell + pad)
        draw.text((x0, 32), name, fill=(60, 60, 60))
        for row, size in enumerate(sizes):
            y0 = 32 + 24 + row * (cell + pad)
            fit = cell - 12
            shown = int(fit) if fit < size * 8 else size * 8
            zoom_icon = resized(source, size).resize((shown, shown), Image.NEAREST)
            # 棋盘底，突出透明圆角
            tile = 8
            checker = Image.new("RGB", (shown, shown), (242, 242, 242))
            cd = ImageDraw.Draw(checker)
            for yy in range(0, shown, tile):
                for xx in range(0, shown, tile):
                    if (xx // tile + yy // tile) % 2 == 0:
                        cd.rectangle((xx, yy, xx + tile, yy + tile), fill=(230, 230, 230))
            stage.paste(checker, (x0, y0))
            stage.paste(zoom_icon, (x0, y0), zoom_icon)
            draw.text((pad, y0 + shown // 2 - 6), f"{size}px", fill=(90, 90, 90))
            draw.text((x0, y0 + shown + 4), f"{size}px x{shown // size}", fill=(120, 120, 120))
    stage.save(out / "preview-small.png")


def main() -> int:
    ap = argparse.ArgumentParser(description="生成 macOS 应用图标全集与 Web 图标")
    ap.add_argument("--source", default="assets/icons/source.jpg")
    ap.add_argument("--out", default="assets/icons")
    ap.add_argument("--no-small-focus", action="store_true", help="关闭小尺寸面部聚焦（默认开启，≤64px 用面部裁切）")
    args = ap.parse_args()

    src_path = Path(args.source)
    out = Path(args.out)
    if not src_path.exists():
        print(f"源图不存在: {src_path}", file=sys.stderr)
        return 1
    out.mkdir(parents=True, exist_ok=True)

    src = load_source(src_path)
    master = build_master(src)
    small_master = None if args.no_small_focus else build_master(focus_source(src))
    master.save(out / "icon-1024.png")
    if small_master is not None:
        small_master.save(out / "icon-small-focus-1024.png")

    iconset = write_iconset(master, out, small_master)
    appiconset = write_appiconset(master, out, small_master)
    icns_ok = write_icns(iconset, out)
    favicon, touch = write_web(master, src, out)
    preview = write_preview(master, small_master, out)

    print(f"母版     : {out / 'icon-1024.png'}")
    print(f"iconset  : {iconset} ({len(ICONSET_SIZES)} 个尺寸)")
    print(f"appiconset: {appiconset}")
    print(f"icns     : {'AppIcon.icns' if icns_ok else '跳过（无 iconutil）'}")
    print(f"favicon  : {favicon} / {touch}")
    print(f"预览     : {preview}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
