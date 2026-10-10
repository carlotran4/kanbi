---
name: kanbi-ui-validation
description: Drive the real Kanbi UI in a disposable Herdr workspace to validate navigation, editing, modals and rendered frames. Use for Kanbi UI, layout, scrolling, keybinding, readability changes or visual bug reports; use the explicit tmux fallback for exact terminal sizes.
metadata:
  requirements: bash, Go, Python 3, sqlite3 and a running Herdr session; tmux only for exact-size checks.
---

# Kanbi UI Validation

Use the real application after deterministic tests for UI-related changes.

## Start and inspect

From inside Herdr, at the repository root:

```bash
UI=.pi/skills/kanbi-ui-validation/scripts/ui-session.sh
$UI start
$UI status
$UI capture
$UI capture --ansi
```

The helper builds current source, seeds a private `kanbi-ui-test.db`, and creates a **non-focused workspace in the caller's Herdr session**. It prints and records the socket, workspace, tab and board pane IDs. Later commands pin that socket and target the recorded pane, even if the caller's inherited Herdr context changes. `status` reports initial PTY rows/columns and current Herdr layout; layout rectangles include decorations, so do not treat their width as the application's exact column count.

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

Only focus the printed board tab when the user requests visible interaction. Background validation should preserve user focus. Use a separate named **Herdr session** for experiments that stop a server; never stop the caller session to clean up a workspace.

## Exact terminal sizes

Herdr 0.9.3 exposes split-ratio resizing, not an exact cell-size CLI. `stty cols/rows` changes the child PTY without resizing Herdr's rendered grid and is not a valid substitute. For 80x24 or a reported failure size, run the explicit fallback:

```bash
KANBI_UI_RUNTIME=tmux $UI start
KANBI_UI_RUNTIME=tmux $UI resize 80 24
KANBI_UI_RUNTIME=tmux $UI status
KANBI_UI_RUNTIME=tmux $UI capture --ansi
KANBI_UI_RUNTIME=tmux $UI key Enter
KANBI_UI_RUNTIME=tmux $UI stop
```

This uses a separate fixture and private socket. Herdr remains the default for ordinary interaction; exact-size checks still require tmux until Herdr provides equivalent sizing controls.

## Evidence and cleanup

Exercise Master, a named board, horizontal/vertical overflow and relevant modals. Confirm header/footer visibility, focused controls on-screen, no clipped borders, stale frames or overlaps. Supplement with a deterministic regression test where appropriate.

```bash
$UI fixture-path
$UI stop              # closes only the recorded workspace; retains fixture
$UI clean             # removes the owned fixture too
./scripts/herdr-ui-smoke.sh
```

Always stop test workspaces. The opt-in smoke runs the real UI and checks navigation, literal text/save, ANSI capture, inherited-context isolation, sibling-tab commands, unchanged workspace focus and cleanup. Report terminal sizes, key sequence, observations and automated checks.
