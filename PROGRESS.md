# Progress

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

## Incomplete TUI Niceties

- The TUI remains intentionally keyboard-first and compact. It now has repair and prompt fallback screens, but not a richer mouse/modal UI.
- Runtime detection is conservative pattern/idle detection over tmux pane output. Native machine-readable harness event integrations can still improve accuracy later.
