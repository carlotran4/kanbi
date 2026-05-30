# Agent Kanban

Agent Kanban is a Go/Bubble Tea TUI for orchestrating multiple resumable agent CLI sessions across one or more Kanban boards.

## Project Documents

- [`AGENTS.md`](./AGENTS.md) — onboarding instructions for autonomous coding agents
- [`docs/architecture.md`](./docs/architecture.md) — current architecture, scope, invariants, and source-of-truth document order
- [`docs/multi-board-behavior.md`](./docs/multi-board-behavior.md) — current multi-board and Master view behavior
- [`docs/harness-contracts.md`](./docs/harness-contracts.md) — current supported harness commands/ref capture contracts
- [`docs/autonomous-verification.md`](./docs/autonomous-verification.md) — how autonomous agents should verify their work
- [`docs/archive/design-spec.md`](./docs/archive/design-spec.md) — historical product/design context; current docs win on conflicts

## Multi-board behavior

- `agent-kanban` opens with a board picker. Choose `Master (all boards)` or a named board.
- Press `b` inside the TUI to switch boards without restarting.
- `Master` aggregates unarchived tickets from every board by matching column name (for example, all `Open` tickets together).
- Press `f` in `Master` to filter/search by board, runtime/state, harness, text, or archived tickets. Filters reset on app restart but persist while switching boards during one run; press `C` in the filter panel to clear them.
- Pressing `n` in `Master` prompts for the target board, then creates the ticket in that board's matching column.
- Each board has a working directory. Opening/sending a ticket starts its agent tmux window in the ticket's board directory, including from `Master`.
- Board-local ticket numbers are preserved, so different boards may both have `T-001`; CLI ticket commands accept `--board NAME` when needed.
- Each launched board UI uses its own tmux runtime session for ticket windows; session rows store that tmux session name so other board instances can validate or switch to it through the shared database.
- Agent tmux window names include the board id to avoid cross-board collisions within a runtime session.
- Create boards from the CLI with `agent-kanban boards add "Board Name" --cwd /path/to/project`; `--cwd` defaults to the current directory. List boards with `agent-kanban boards`.
- Rename/update boards with `agent-kanban boards rename OLD NEW` and `agent-kanban boards set-cwd NAME /path/to/project`.
- In the TUI board picker: `c` creates a board, `r` renames, `w` sets cwd, and `d` deletes.

## Development

Install a development launcher on your `PATH`:

```bash
./scripts/install-dev.sh
```

By default this writes `~/.local/bin/agent-kanban`. The launcher rebuilds `.bin/agent-kanban` from this checkout whenever `cmd/`, `internal/`, `go.mod`, or `go.sum` are newer than the cached binary, then execs it. Set `AGENT_KANBAN_BIN_DIR=/some/path` to install the launcher somewhere else.

Baseline checks:

```bash
go fmt ./...
go test ./...
go vet ./...
```

## Status

Alpha lifecycle hardening is complete: multi-board TUI/CLI behavior, tmux-backed ticket sessions, Pi/Codex/Copilot command wiring and ref capture, fake and real harness verification, tests, and smoke verification are in place. Current follow-up work is tracked as tickets on the board.
