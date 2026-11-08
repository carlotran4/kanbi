"""Build the self-contained sidebar comparison from captured terminal frames."""
import html
import json
import re
from pathlib import Path

HERE = Path(__file__).resolve().parent
COLORS = {
 '#1a1b26':'var(--kb-bg)', '#24283b':'var(--kb-card)', '#292e42':'var(--kb-selected)',
 '#414868':'var(--kb-line)', '#a9b1d6':'var(--kb-fg)', '#c0caf5':'var(--kb-bright)',
 '#7aa2f7':'var(--kb-accent)', '#79a2f7':'var(--kb-accent)',
 '#f7768e':'var(--kb-red)', '#e0af68':'var(--kb-yellow)', '#9ece6a':'var(--kb-green)',
}

def convert(source):
    fg = bg = None
    bold = False
    output = []
    for part in re.split(r'(\x1b\[[0-9;]*m)', source):
        if part.startswith('\x1b['):
            codes = [int(x or '0') for x in part[2:-1].split(';')]
            i = 0
            while i < len(codes):
                c = codes[i]
                if c == 0: fg = bg = None; bold = False
                elif c == 1: bold = True
                elif c == 22: bold = False
                elif c == 39: fg = None
                elif c == 49: bg = None
                elif c in (38,48) and i+4<len(codes) and codes[i+1] == 2:
                    color = '#%02x%02x%02x' % tuple(codes[i+2:i+5]);i+=4
                    if c == 38: fg = COLORS.get(color, 'var(--kb-fg)')
                    else: bg = COLORS.get(color, 'var(--kb-card)')
                i += 1
            continue
        style = ';'.join(x for x in [f'color:{fg}' if fg else '', f'background:{bg}' if bg else '', 'font-weight:500' if bold else ''] if x)
        value = html.escape(part)
        output.append(f'<span style="{style}">{value}</span>' if style else value)
    return ''.join(output)

frames = {}
for key in ['120x36', '160x45', '80x24']:
    frames[key] = convert((HERE/f'current-{key}.ansi').read_text())
frames['board80'] = convert((HERE/'board-80x24.ansi').read_text())
frames['currentDetail'] = html.escape('\n'.join(line[85:] for line in (HERE/'current-120x36.text').read_text().splitlines()[1:-1]))
source = (HERE/'sidebar.template.html').read_text()
source = source.replace('__FRAMES__', json.dumps(frames, ensure_ascii=False))
(HERE/'operator-sidebars.html').write_text(source)
assert len(source.encode()) < 1_000_000
