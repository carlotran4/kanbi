"""Exact terminal-cell sketches. Run from any directory; no app state changes."""
from pathlib import Path
import textwrap

HERE = Path(__file__).resolve().parent

def render(cols, rows, schedule=False):
    w = 74 if cols == 80 else 116
    inner = w - 6
    title = ' Schedule · Intake ' if schedule else ' New Operator · Intake '
    content = ['workspace  ·  codex  ·  unsaved', 'Directory  /home/carlotran4/Developer/kanbi', '']
    if schedule:
        content = ['Schedule · Intake', '', 'Repeat     Every 15 minutes', 'Timezone   America/Chicago', '', '▸ Scheduled prompt', 'Check incoming work for new requests.', '', 'Fixed clock · while Kanbi is running', '', '─' * inner, 'Tab fields · Enter done · Esc back']
    else:
        content += ['▸ Standing instructions']
        for paragraph in ['Inspect incoming requests and find related work across boards.', '', 'Reconcile new requests with existing tickets and sessions.', 'Ask me before creating or moving tickets.', '', 'Use the tools configured in your harness.']:
            content.extend(textwrap.wrap(paragraph, inner - 2) or [''])
        content += [''] * (1 if rows == 24 else 7)
        content += ['Schedule  Every 15 minutes · America/Chicago', '─' * inner, 'Tab fields · Ctrl+E editor · Ctrl+S save · Esc cancel']
    box = ['╭─' + title + '─' * (w - len(title) - 3) + '╮']
    box += ['│  ' + line.ljust(inner) + '  │' for line in content]
    box += ['╰' + '─' * (w - 2) + '╯']
    assert all(len(line) == w for line in box)
    board = (HERE / 'board-160x45.txt').read_text().splitlines()
    grid = [list((board[i] if i < len(board) else '').ljust(cols)[:cols]) for i in range(rows)]
    x, y = (cols-w)//2, max(2, (rows-len(box))//2)
    assert y + len(box) <= rows - 1
    for i, line in enumerate(box):
        grid[y+i][x:x+w] = list(line)
    grid[-1] = list('Enter:send/open  /:search  i:inspect  n:new  b:boards  O:operators'.ljust(cols)[:cols])
    return '\n'.join(''.join(row) for row in grid) + '\n'

for cols, rows in [(80, 24), (160, 45)]:
    for schedule in [False, True]:
        suffix = 'schedule' if schedule else 'editor'
        (HERE / f'proposed-{suffix}-{cols}x{rows}.txt').write_text(render(cols, rows, schedule))
