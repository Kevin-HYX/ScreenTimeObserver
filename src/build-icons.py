"""把 SVG 标识渲染成多尺寸 PNG 与多尺寸 .ico，并生成 16px 可读性质检图。

- 每个尺寸都直接从矢量渲染，绝不从大图缩小，保证 16px 依然清晰
- 默认处理 assets/logo.svg（彩色）与 assets/logo-mono.svg（单色）
- 可用 --asset-dir / --name 指向别的候选目录，便于 A/B 比稿
"""
import argparse
import os
import struct

import cairosvg
from PIL import Image

DEFAULT_ASSETS = "D:/ScreenTimeObserver/assets"
SIZES = [256, 200, 128, 96, 64, 48, 40, 32, 24, 20, 16]
ICO_SIZES = [256, 128, 64, 48, 40, 32, 24, 20, 16]
PREVIEW_SIZES = [16, 20, 24, 32, 48, 64]


def render(svg_path, size):
    return cairosvg.svg2png(url=svg_path, output_width=size, output_height=size)


def pack_ico(items, out_path):
    """items: [(size, png_bytes)]，写 Vista+ 支持的 PNG 内嵌 ICO。"""
    n = len(items)
    header = struct.pack("<HHH", 0, 1, n)
    offset = 6 + 16 * n
    entries = b""
    body = b""
    for size, png in items:
        dim = 0 if size >= 256 else size
        entries += struct.pack("<BBBBHHII", dim, dim, 0, 0, 1, 32, len(png), offset)
        offset += len(png)
        body += png
    with open(out_path, "wb") as f:
        f.write(header + entries + body)


def build(assets, name, svg_file, png_dir):
    svg_path = os.path.join(assets, svg_file)
    if not os.path.exists(svg_path):
        print("skip %s: %s not found" % (name, svg_path))
        return False
    rendered = {}
    for s in SIZES:
        b = render(svg_path, s)
        rendered[s] = b
        with open(os.path.join(png_dir, name + "-%d.png" % s), "wb") as f:
            f.write(b)
    items = [(s, rendered[s]) for s in ICO_SIZES if s in rendered]
    out = os.path.join(assets, name + ".ico")
    pack_ico(items, out)
    print("%-16s sizes=%s bytes=%d" % (name + ".ico", [s for s, _ in items], os.path.getsize(out)))
    return True


def preview(assets, name, mono_name, png_dir, out_path):
    """质检图：每个尺寸放大到同一显示大小，看 16px 是否还立得住。"""
    zoom = 128
    pad = 10
    rows = [(name, (246, 247, 249, 255)), (name, (28, 30, 34, 255)),
            (mono_name, (28, 30, 34, 255))]
    cw = zoom + pad
    w = cw * len(PREVIEW_SIZES) + pad
    h = cw * len(rows) + pad
    canvas = Image.new("RGBA", (w, h), (255, 255, 255, 255))
    for r, (src, bg) in enumerate(rows):
        y = pad + r * cw
        for c, s in enumerate(PREVIEW_SIZES):
            x = pad + c * cw
            cell = Image.new("RGBA", (zoom, zoom), bg)
            p = os.path.join(png_dir, "%s-%d.png" % (src, s))
            if os.path.exists(p):
                img = Image.open(p).convert("RGBA")
                cell.alpha_composite(img.resize((zoom, zoom), Image.Resampling.NEAREST))
            canvas.alpha_composite(cell, (x, y))
    canvas.save(out_path)
    print("preview:", out_path)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--asset-dir", default=DEFAULT_ASSETS)
    ap.add_argument("--name", default="logo")
    ap.add_argument("--mono-name", default="logo-mono")
    ap.add_argument("--no-preview", action="store_true")
    a = ap.parse_args()

    png_dir = os.path.join(a.asset_dir, "png")
    os.makedirs(png_dir, exist_ok=True)
    build(a.asset_dir, a.name, a.name + ".svg", png_dir)
    build(a.asset_dir, a.mono_name, a.mono_name + ".svg", png_dir)
    if not a.no_preview:
        preview(a.asset_dir, a.name, a.mono_name, png_dir,
                os.path.join(a.asset_dir, "preview-sizes.png"))


if __name__ == "__main__":
    main()