# Progress

## Alpha Hardening Complete

The following lifecycle hardening work is done. All deterministic checks pass.

### Implemented

- **Opt-in real harness lifecycle script**: `scripts/real-harness-lifecycle.sh` with `AGENT_KANBAN_REAL_HARNESS_TESTS=1` guard, isolated temp DB/config/state paths, unique tmux session, per-harness step-by-step verification of open, pane capture, session ref, close, and resume path.
- **Harness contracts documented**: `docs/harness-contracts.md` with Pi, Codex, Copilot commands, session ref capture method, and Copilot limitation clearly stated.
- **Copilot limitation surfaced**: `✕` indicator on cards with prior session but no ref; explicit note in repair screen for Copilot tickets.
- **State machine hardening tests added**:
  - `TestStartFreshPreservesSessionHistory` (storage): verifies UpsertActiveSession deactivates old session without deleting it.
  - `TestStartFreshTicketPreservesOldSessionAndCreatesNew` (tmux): verifies StartFreshTicket creates a new session row while old session remains.
  - `TestCardRuntimeLabelsAndWindowIndicators` (tui): verifies runtimeLabel outputs and windowIndicator states including repair-needed (✕).
  - `TestRepairViewShowsCopilotNote` (tui): verifies Copilot limitation note appears in repair screen for Copilot tickets without a ref.
- **TUI improvements**:
  - `runtimeLabel()` converts internal state names to readable labels (e.g., `waiting_for_user` → `waiting`, `closed` with ref → `closed (resumable)`).
  - `windowIndicator()` now shows `✕` for tickets that had a session but lost their window/ref (needs repair).
  - Repair view shows Copilot-specific note when harness is copilot and no ref is stored.
  - Footer condensed to single line without removing any controls.

### Verified State Machine Invariants (all covered by tests)

- No duplicate session rows when opening an already-active valid window: `TestOpenTicketExistingActiveWindowDoesNotCreateNewSessionRow`
- Inactive latest sessions project as `closed` or `error`, not `not_started`: `TestSessionsRecordRuntimeMetadata`
- Stale `tmux_window_id` rejected when window name doesn't match: `TestSwitchToTicketRejectsStaleWindowIDWithWrongName`
- `send prompt` rejected for any ticket with prior session metadata: `TestOpenTicketRejectsSecondPromptSend`
- Start-fresh preserves previous session history: `TestStartFreshPreservesSessionHistory`, `TestStartFreshTicketPreservesOldSessionAndCreatesNew`
- Auto-close never triggers on the same tick as first attention state: `TestRefreshRuntimeDoesNotAutoCloseImmediatelyOnNewAttentionState`
- Manual state marks not overwritten by weak heuristics: `TestRefreshRuntimePreservesManualStateFromHeuristics`

## Verification

- Ran `go fmt ./...`: pass
- Ran `go test -count=1 ./...`: pass
- Ran `go vet ./...`: pass
- Ran `./scripts/smoke.sh`: pass
- Real harness tests (`AGENT_KANBAN_REAL_HARNESS_TESTS=1 AGENT_KANBAN_REAL_HARNESSES=pi,codex`): **PASS**
  - Pi: session ref `019e3954-cdb4-7343-bbe3-f8995397d6b5` captured from `~/.pi/agent/sessions/` within 8s; resume command `pi --session 019e3954-...` verified
  - Codex: session ref `019e3954-e85c-7921-b1c0-60290e5ea84e` captured from `~/.codex/history.jsonl`; resume command `codex resume --no-alt-screen 019e3954-...` verified
  - Root cause fix: Pi takes ~3.5s to write its JSONL session file; extended `captureSessionRef` polling from 2s to 8s for both Pi and Copilot
- Real harness tests (`AGENT_KANBAN_REAL_HARNESS_TESTS=1 AGENT_KANBAN_REAL_HARNESSES=copilot`): **PASS** (verified automatic session ref capture)
  - session ref `80eacae1-3aa5-4a62-91fc-c6821a82b787` captured from `~/.copilot/session-store.db`; resume command `gh copilot -- --resume=80eacae1-...` verified

## Remaining TUI Niceties

- Runtime detection is conservative pattern/idle detection over tmux pane output. Native machine-readable harness event integrations can improve accuracy later.
- Mouse/modal UI not yet implemented (intentional; keyboard-first).
- Richer attention styling (flashing/animation) deferred.

- Implemented the `agent-kanban` CLI with `doctor`, `add`, `list`, `open`, and `--board`.
- Added XDG/env path resolution, YAML config loading, SQLite migrations, default board columns, tickets, sessions, and sequential display IDs.
- Added a Bubble Tea board model that loads persistent tickets and supports navigation, moving, reordering, title/body/harness editing, opening/switching ticket sessions, new tickets, and archive.
- Added tmux session/window orchestration for the dedicated `agent-kanban` session, `board` window, ticket windows, prompt paste, title-based window naming, session metadata including `tmux_window_id`, and startup reconciliation.
- Added fake harness support under `scripts/fake-harnesses/` and `scripts/smoke.sh`.
- Wired Codex as the first real harness path: `codex --no-alt-screen` starts interactive sessions, `codex resume --no-alt-screen <session_ref>` resumes when a ref is known, and first prompts are sent as the Codex `[PROMPT]` argument instead of via fake prompt-ready paste.
- Wired Pi to its real CLI shape: `pi <prompt>` starts an interactive session with the ticket prompt, `pi --session <session_ref>` resumes, and session refs are captured from `~/.pi/agent/sessions`.
- Added runtime state metadata, tmux pane polling from the board process, conservative pattern/idle detection, attention highlighting, `Tab` attention navigation, manual runtime marking, and graceful session close/autoclose plumbing.
- Added TUI controls for send-prompt (`s`), body editing through `$EDITOR` (`E` / `Ctrl+E` in edit mode), column add/rename/reorder/delete-empty, prompt-ready fallback choices, and repair/start-fresh handling for missing session refs or resume failures.
- Expanded config support for design-spec style nested tmux/timeouts settings and improved `doctor` checks for tmux version/session, paths, shell, terminal, and harness presence.

## Current Focus

The next project phase is alpha hardening for ticket/session lifecycle behavior with real harnesses. See `GOAL.md`, `docs/state-management.md`, and `docs/ticket-session-lifecycle.md`.

Priority work:

- Add an opt-in real-harness lifecycle script separate from `scripts/smoke.sh`.
- Exercise Pi, Codex, and Copilot against temporary DB/config/state paths and an isolated tmux session.
- Verify start/send-prompt, active open/switch, close, inactive state projection, repair/start-fresh, and resume when a `session_ref` exists.
- Keep Copilot honest: it is wired to its real start/resume command shape, but automatic session-ref capture is not yet verified.
- Continue using fake harnesses for deterministic CI-style smoke coverage.

Required deterministic verification after meaningful changes:

```bash
go fmt ./...
go test ./...
go vet ./...
./scripts/smoke.sh
```

Required focused verification after lifecycle or harness changes:

```bash
go test ./internal/harness ./internal/tmux ./internal/storage
```

Opt-in real harness verification, once implemented:

```bash
AGENT_KANBAN_REAL_HARNESS_TESTS=1 AGENT_KANBAN_REAL_HARNESSES=pi,codex ./scripts/real-harness-lifecycle.sh
```

## Incomplete TUI Niceties

- The TUI remains intentionally keyboard-first and compact. It now has repair and prompt fallback screens, but not a richer mouse/modal UI.
- Runtime detection is conservative pattern/idle detection over tmux pane output. Native machine-readable harness event integrations can still improve accuracy later.
- Card status readability, footer density, repair screens, and attention styling should be polished after lifecycle behavior is stable.
