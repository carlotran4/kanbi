# Agent Kanban

Agent Kanban is a Go/Bubble Tea TUI for orchestrating multiple resumable agent CLI sessions through a Kanban board.

## Project Documents

- [`design-spec.md`](./design-spec.md) — full product/design specification and decision log
- [`docs/autonomous-verification.md`](./docs/autonomous-verification.md) — how autonomous agents should verify their work

## Multi-board behavior

- `agent-kanban` opens with a board picker. Choose `Master (all boards)` or a named board.
- Press `b` inside the TUI to switch boards without restarting.
- `Master` aggregates active tickets from every board by matching column name (for example, all `Open` tickets together).
- Pressing `n` in `Master` prompts for the target board, then creates the ticket in that board's matching column.
- Each board has a working directory. Opening/sending a ticket starts its agent tmux window in the ticket's board directory, including from `Master`.
- Board-local ticket numbers are preserved, so different boards may both have `T-001`; CLI ticket commands accept `--board NAME` when needed.
- Agent tmux window names include the board id to avoid cross-board collisions.
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

Local MVP skeleton implemented: CLI/config/storage, Bubble Tea board model, tmux-backed ticket windows, fake harnesses, tests, and smoke verification. See [`PROGRESS.md`](./PROGRESS.md) for current TUI limitations.
