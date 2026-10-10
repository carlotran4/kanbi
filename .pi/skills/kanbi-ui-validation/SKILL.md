---
name: kanbi-ui-validation
description: Drive the real Kanbi UI in a disposable Herdr workspace to validate navigation, editing, modals and rendered frames. Use for Kanbi UI, layout, scrolling, keybinding, readability changes or visual bug reports.
metadata:
  requirements: bash, Go, Python 3, sqlite3 and Herdr (tested with 0.9.3).
---

# Kanbi UI Validation

Use the real application after deterministic tests for UI-related changes.

## Start and inspect

At the repository root (including from inside Herdr):

```bash
UI=.pi/skills/kanbi-ui-validation/scripts/ui-session.sh
$UI start
$UI status
$UI capture
$UI capture --ansi
```

The helper builds current source, seeds a private `kanbi-ui-test.db`, and creates a workspace in an **owned isolated Herdr server and rendering client**. It prints and records the socket, workspace, tab and board pane IDs. Later commands pin that socket and target the recorded pane, even if the caller's inherited Herdr context changes. `status` reports initial PTY rows/columns and current Herdr layout; layout rectangles include decorations. The fixture isolates socket, configuration and XDG state, and enables nesting only in that temporary configuration. It never modifies the parent server or user config.

The fixture includes four local boards, overflow, elapsed error/resumable cards and Focus Mode handoffs. It accepts no production database path, refuses provider-backed/sync-enabled fixtures, and disables real harness commands. Enter in the board picker selects a board; Enter on a ticket may attempt a session launch. This fixture validates the board UI, **not authenticated agent lifecycle**.

`start` refuses an already recorded fixture; stop it first. Defaults are scoped to the checkout and backend. Use a distinct `KANBI_UI_WORKDIR` under `/tmp` for concurrent fixtures; retain the same value for every command.

## Drive the UI

```bash
$UI key Enter          # select Master in the initial picker
$UI key j             # move one card
$UI capture
$UI key l             # move one column
$UI key '?'           # help
$UI key Escape
$UI key b             # board picker
$UI key Down
$UI key Enter         # select a named board
$UI key e             # ticket inspector/editor
$UI key Home
$UI text 'Example prefix '
$UI key C-s           # save fixture-only change
$UI capture
```

`text` takes one quoted literal string. `key` accepts Herdr logical keys (`enter`, `esc`, `ctrl+s`, `shift+tab`) and the common aliases above. Home/End use Kanbi’s Ctrl+A/Ctrl+E equivalents because Herdr 0.9.3 rejects the named Home key. Read a frame before input and after each meaningful transition. Poll/re-capture if rendering has not settled; do not send a large navigation burst that hides failures. Use **visible** snapshots for layout: transcript/scrollback sources can hide clipping or return old screens. ANSI capture preserves styling.

The board is an ordinary terminal process, so control it through `pane` commands, not `agent prompt`. For direct commands use the recorded socket and IDs from `status`. Do not use the user's focused pane or stale inherited caller IDs. Additional test tabs can be created with `herdr tab create --workspace <recorded-workspace> --cwd <fixture-directory> --no-focus`; parse returned IDs and use them for subsequent reads/input. Keep app paths bound to the fixture when running another Kanbi process. Workspace cleanup closes all its tabs.

The fixture's client runs on an owned PTY, so it does not change user focus. For manual visible interaction, attach a separate client using the recorded socket. Never stop the parent Herdr server.

## Exact terminal sizes

```bash
$UI resize 80 24
$UI capture --ansi
$UI key Enter
$UI resize 160 45
```

The helper resizes the actual rendering client's PTY. Herdr then resizes the application's PTY and rendered grid; the helper polls the application PTY to verify the requested dimensions. Do not use `stty cols/rows` alone or infer application dimensions from layout rectangles. No alternate runtime is needed.

## Evidence and cleanup

Exercise Master, a named board, horizontal/vertical overflow and relevant modals. Confirm header/footer visibility, focused controls on-screen, no clipped borders, stale frames or overlaps. Supplement with a deterministic regression test where appropriate.

```bash
$UI fixture-path
$UI stop              # closes only the recorded workspace; retains fixture
$UI clean             # removes the owned fixture too
./scripts/herdr-ui-smoke.sh
```

Always stop test workspaces. The opt-in smoke runs the real UI and checks navigation, literal text/save, ANSI capture, inherited-context isolation, sibling-tab commands, exact terminal sizes and unchanged parent focus and cleanup. Report terminal sizes, key sequence, observations and automated checks.
