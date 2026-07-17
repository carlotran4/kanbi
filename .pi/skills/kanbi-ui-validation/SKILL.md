---
name: kanbi-ui-validation
description: Drives the real Kanbi Bubble Tea UI in an isolated tmux terminal and inspects rendered frames, navigation, scrolling, resizing, and modals. Use proactively for Kanbi TUI/UI/layout/scrolling/keybinding/readability changes, visual bug reports, screenshots, or when asked to look at or validate the board as a human would.
compatibility: Requires bash, Go, tmux, and sqlite3.
---

# Kanbi UI Validation

Use the real application, not only model tests. Run this after deterministic tests for every UI-related change.

## Start an isolated realistic board

From the repository root:

```bash
.pi/skills/kanbi-ui-validation/scripts/ui-session.sh start
```

The script builds current source, recreates the canonical `kanbi-ui-test.db` fixture, and launches Kanbi at 160x45 on a private tmux socket. The fixture has a stable multi-board shape, realistic elapsed error/resumable cards, local backends only, and provider sync forcibly disabled.

The helper accepts no production database path and refuses to launch if its fixture contains a provider-backed or sync-enabled board. Never copy or open the user's canonical database for UI validation.

Inspect the retained fixture path with:

```bash
.pi/skills/kanbi-ui-validation/scripts/ui-session.sh fixture-path
```

## Drive it like a user

```bash
UI=.pi/skills/kanbi-ui-validation/scripts/ui-session.sh
$UI capture
$UI key Enter          # choose focused picker item
$UI key j j j           # navigate cards
$UI key l               # navigate columns
$UI key b               # board picker
$UI key f               # Master filters
$UI key Escape
$UI resize 100 28
$UI capture
```

Use `text VALUE` for literal typing and `key Enter`, `key Escape`, or `key C-c` for terminal keys. Capture before input, after each meaningful transition, at the reported failure size, and at 80x24.

## Visual validation checklist

- Header and column headers remain visible; terminal itself never scrolls.
- Focused card/control remains visible after every navigation key.
- Footer, status/remediation, and overflow hints remain reachable.
- No clipped borders, overlapping columns, broken ANSI, or stale frames.
- Master board, a named board, horizontal overflow, vertical overflow, modal open/close, and resize are exercised when relevant.
- Pair interactive evidence with a regression test at the model/render seam.

For repeated navigation, run a shell loop one key at a time and capture each frame; do not send a large key burst that hides intermediate failures.

## Cleanup

```bash
.pi/skills/kanbi-ui-validation/scripts/ui-session.sh stop
```

Always stop the disposable session when validation finishes. `stop` retains the canonical fixture for inspection; `clean` removes both session and fixture. Report terminal sizes, key sequence, observed result, and automated checks in the final response.
