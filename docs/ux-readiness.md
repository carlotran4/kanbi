# UX Readiness and Manual Matrix

Kanbi's TUI is keyboard-first and does not use color, animation, or a standalone glyph as the only carrier of runtime meaning.

## First-run path

In an empty XDG environment, startup presents a dismissible three-page guide before the board picker. It covers:

1. Herdr and authenticated harness prerequisites, plus `kanbi doctor`;
2. board working directories, ticket creation, `Enter` start/open behavior, safe `x` close, and non-destructive `q` exit;
3. resumability/repair, preserved session history, `kanbi backup PATH`, and in-app help.

`Esc` skips immediately. The guide is transient and does not write a preference or alter SQLite state. It appears only on the normal startup picker path when there are no tickets; embedded/model uses are unaffected.

## Runtime and indicator semantics

Cards use one compact runtime row with icon, harness, textual state, and elapsed time when it fits. Waiting for ordinary input and permission approval remain explicitly different strings in monochrome output. Symbols are redundant shorthand:

| Text and symbol | Meaning |
| --- | --- |
| `● running` | A live multiplexer container was validated. |
| `○ resumable` | No live container exists and a verified harness ref can resume the adjacent state, including closed, exited, or error. |
| `? waiting` / `! permission` | User input or approval is required. |
| `◐ starting` / `◐ closing` | A runtime transition is underway. |
| `◌ idle` / `× error` | Activity is unknown, or a non-resumable operation failed. |
| `· not started` | No special runtime capability is present. |
| `> focused` | Current keyboard target. |

Normal container absence is not repeated on a separate card row. Worktree cards use a second compact `git` row for branch health, prioritizing repair, cleanup, and conflicts; the inspector shows the full branch name.

Press `?` for this legend and the implemented controls. Help scrolls with `j`/`k` or arrows.

## Degraded and error states

The board renders from the local SQLite projection rather than blocking on a provider. An empty column says how to create a ticket; an empty filtered Master view says how to change filters. Provider failures appear in the board picker as **provider sync degraded (local data available)** with the preserved cause and a `kanbi sync --board NAME` retry action. Startup runtime reconciliation failures use the same degraded class: **runtime reconciliation degraded (local data available)** with cause and a `kanbi doctor` next step, while the local SQLite projection remains usable.

Runtime refresh/open/close/multiplexer-move errors render the failed operation, underlying cause, and a concrete next action on separate lines. They never imply that session history was discarded. Other validation errors remain inline beside their focused control. Kanbi has no remote-loading screen because provider sync is deliberately non-blocking and the local projection is available immediately; this is the useful loading behavior rather than an indeterminate blocker.

## Responsive behavior

Board columns expand evenly between 30 and 44 cells. At 80x24, two columns fit at 39 cells; additional columns remain horizontally scrollable instead of being squeezed. Four default columns expand to 39 cells at 160 columns and cap at 44 on wider terminals. Below 30 columns, Kanbi retains the minimum card width and clips safely.

At 80x24, major popups are constrained to the terminal. List dialogs follow their focused textual `>` marker, keeping the selected control reachable. Help has explicit scrolling. Below that size, long content is clipped with `more`; focus-following dialogs keep the selected control visible rather than pretending their navigation keys scroll content. Ticket description and notes fields retain their own textarea/thread navigation.

## Manual UX matrix

Exercise this before a beta release with a disposable environment:

```bash
root=$(mktemp -d)
export XDG_CONFIG_HOME="$root/config"
export XDG_DATA_HOME="$root/data"
export XDG_STATE_HOME="$root/state"
export KANBI_DB="$root/data/kanbi/kanbi.db"
kanbi doctor
kanbi
```

First-run walkthrough checklist (last exercised 2026-07-12 in a disposable XDG environment; session lifecycle also exercised by `scripts/smoke.sh --skip-checks` with fake harnesses):

- [x] Page 1 names Herdr, supported harness authentication, and `kanbi doctor`.
- [x] Pages 2–3 explain cwd, create/start/open/close/quit, repair/history, and backup.
- [x] `Esc` skips immediately; completing the guide reaches the picker.
- [x] Select the default board; smoke creates tickets and opens fake Pi/Codex/Claude sessions.
- [x] Smoke verifies graceful lifecycle completion without discarding session history.
- [x] `Ctrl+C` exits the walkthrough safely; smoke cleanup is isolated.

Terminal/theme/runtime matrix:

| Layout | Theme/output | Runtime | Checks |
| --- | --- | --- | --- |
| 80x24 | dark | Herdr | two 39-cell columns, horizontal/vertical focus-follow, inspector, filters, help, repair |
| 80x24 | light | Herdr | focus border/text contrast; waiting vs permission text |
| 120x40 | dark | Herdr | responsive width threshold, long ticket content, notes, long board/column names |
| 120x40 | light | Herdr | all semantic colors remain supplemental |
| 60x18 | monochrome (`NO_COLOR=1` where supported) | Herdr | textual states/indicators, clipped modal guidance, controls reachable |
| 160x45 | dark | Herdr | four equal expanded columns, compact runtime/Git rows, resize wide → narrow → wide |
| 40x12 | monochrome | no session launch | 30-cell minimum, safe clipping, and `Ctrl+C` exit |

Also include Unicode ticket titles/body, combining characters, emoji, very long unbroken words, provider sync failure, database-load failure, and a missing Herdr/harness in the pass. Record terminal emulator, `TERM`, theme, Herdr version, and any discrepancy in the release report.

## Intentionally deferred limitations

- Kanbi does not currently expose user-selectable high-contrast or reduced-color themes; adaptive light/dark colors and redundant text are used instead.
- Unicode width relies on Lip Gloss/terminal width behavior. Complex grapheme clusters may truncate imperfectly, but state and controls remain textual.
- Inline Kitty images remain terminal-capability dependent and always retain text placeholders.
- The first-run guide is inferred from an empty ticket set rather than a durable “seen” preference, avoiding a new schema/config mutation. An experienced user with a deliberately empty database may see it again and can dismiss it with one `Esc`.
