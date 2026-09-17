"""生成托盘图标的状态变体。

- 几何以本脚本为唯一来源：直接产出矢量 SVG，再逐尺寸渲染成 PNG 与 ICO
- 环形颜色表达状态，内部图形（指针 / 暂停条 / 感叹号）再给一次形状线索，
  避免只靠颜色区分
- 深浅两套任务栏主题各一份：浅色主题用深色显示器轮廓，深色主题用白色轮廓
"""
import struct
from pathlib import Path

import cairosvg
from PIL import Image

ASSETS = Path('D:/ScreenTimeObserver/assets')
STATES = ASSETS / 'states'
PNG_DIR = ASSETS / 'png'

RING = {
    'active': '#4B90E8',
    'idle': '#94A3B8',
    'paused': '#F59E0B',
    'alert': '#EF4444',
}
INK = {'light': '#29323D', 'dark': '#FFFFFF'}

HANDS = '<path d="M96 64v22l16 11" fill="none" stroke="{ink}" stroke-width="9" stroke-linecap="round" stroke-linejoin="round"/>'
PAUSE = '<path d="M86 70v34M106 70v34" fill="none" stroke="{ink}" stroke-width="10" stroke-linecap="round"/>'
BANG = ('<path d="M96 64v24" fill="none" stroke="{ink}" stroke-width="10" stroke-linecap="round"/>' +
        '<circle cx="96" cy="103" r="5.5" fill="{ink}"/>')

GLYPH = {'active': HANDS, 'idle': HANDS, 'paused': PAUSE, 'alert': BANG}

TEMPLATE = '''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 192 192" width="192" height="192" role="img" aria-labelledby="title">
  <title id="title">屏幕时间观测器</title>
  <g fill="none" stroke="{ink}" stroke-width="9" stroke-linecap="round" stroke-linejoin="round">
    <rect x="16" y="32" width="160" height="108" rx="11"/>
    <path d="M96 141v21M59 162h74"/>
  </g>
  <circle cx="96" cy="86" r="37" fill="none" stroke="{ring}" stroke-width="8"/>
  {glyph}
</svg>
'''

SIZES = [256, 200, 128, 96, 64, 48, 40, 32, 24, 20, 16]
ICO_SIZES = [256, 128, 64, 48, 40, 32, 24, 20, 16]
PREVIEW_SIZES = [16, 20, 24, 32, 48]
STATE_ORDER = ['active', 'idle', 'paused', 'alert']


def svg_for(ink_name, state):
    return TEMPLATE.format(ink=INK[ink_name], ring=RING[state], glyph=GLYPH[state].format(ink=INK[ink_name]))


def render(svg_text, size):
    return cairosvg.svg2png(bytestring=svg_text.encode('utf-8'), output_width=size, output_height=size)


def pack_ico(items, out_path):
    """items: [(size, png_bytes)]，写 Vista+ 支持的 PNG 内嵌 ICO。"""
    header = struct.pack('<HHH', 0, 1, len(items))
    offset = 6 + 16 * len(items)
    entries = b''
    body = b''
    for size, png in items:
        dim = 0 if size >= 256 else size
        entries += struct.pack('<BBBBHHII', dim, dim, 0, 0, 1, 32, len(png), offset)
        offset += len(png)
        body += png
    out_path.write_bytes(header + entries + body)


def build_state(ink_name, state):
    svg = svg_for(ink_name, state)
    stem = ink_name + '-' + state
    (STATES / (stem + '.svg')).write_text(svg, encoding='utf-8')
    rendered = {}
    for size in SIZES:
        png = render(svg, size)
        rendered[size] = png
        (PNG_DIR / (stem + '-' + str(size) + '.png')).write_bytes(png)
    ico = STATES / (stem + '.ico')
    pack_ico([(s, rendered[s]) for s in ICO_SIZES], ico)
    return ico


def build_preview():
    zoom, pad = 96, 8
    cell = zoom + pad
    rows = [('light', s) for s in STATE_ORDER] + [('dark', s) for s in STATE_ORDER]
    width = cell * len(PREVIEW_SIZES) + pad
    height = cell * len(rows) + pad
    canvas = Image.new('RGBA', (width, height), (255, 255, 255, 255))
    for r, (ink_name, state) in enumerate(rows):
        bg = (246, 247, 249, 255) if ink_name == 'light' else (28, 30, 34, 255)
        for c, size in enumerate(PREVIEW_SIZES):
            box = Image.new('RGBA', (zoom, zoom), bg)
            png = PNG_DIR / (ink_name + '-' + state + '-' + str(size) + '.png')
            if png.exists():
                with Image.open(png) as img:
                    box.alpha_composite(img.convert('RGBA').resize((zoom, zoom), Image.Resampling.NEAREST))
            canvas.alpha_composite(box, (pad + c * cell, pad + r * cell))
    canvas.save(ASSETS / 'preview-states.png')
    print('preview:', ASSETS / 'preview-states.png')


def main():
    STATES.mkdir(parents=True, exist_ok=True)
    PNG_DIR.mkdir(parents=True, exist_ok=True)
    for ink_name in ('light', 'dark'):
        for state in STATE_ORDER:
            ico = build_state(ink_name, state)
            print('%-22s %d bytes' % (ico.name, ico.stat().st_size))
    (ASSETS / 'logo.svg').write_text(svg_for('light', 'active'), encoding='utf-8')
    (ASSETS / 'logo-mono.svg').write_text(svg_for('dark', 'active'), encoding='utf-8')
    build_preview()


if __name__ == '__main__':
    main()

