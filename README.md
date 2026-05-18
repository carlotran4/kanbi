# Agent Kanban

Agent Kanban is a Go/Bubble Tea TUI for orchestrating multiple resumable agent CLI sessions through a Kanban board.

## Project Documents

- [`design-spec.md`](./design-spec.md) — full product/design specification and decision log
- [`docs/autonomous-verification.md`](./docs/autonomous-verification.md) — how autonomous agents should verify their work

## Development

Baseline checks:

```bash
go fmt ./...
go test ./...
go vet ./...
```

## Status

Local MVP skeleton implemented: CLI/config/storage, Bubble Tea board model, tmux-backed ticket windows, fake harnesses, tests, and smoke verification. See [`PROGRESS.md`](./PROGRESS.md) for current TUI limitations.
