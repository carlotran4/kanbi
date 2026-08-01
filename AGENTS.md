# Agent Instructions

This file is the onboarding entrypoint for autonomous coding agents working in this repo.

## Read First

Read these in order before making non-trivial changes:

1. [`README.md`](./README.md) — user-facing overview and current behavior.
2. [`docs/architecture.md`](./docs/architecture.md) — current architecture, package map, invariants, scope, and stop conditions.
3. [`docs/state-management.md`](./docs/state-management.md) — canonical runtime/session state model.
4. [`docs/ticket-session-lifecycle.md`](./docs/ticket-session-lifecycle.md) — command-specific lifecycle rules.
5. [`docs/harness-contracts.md`](./docs/harness-contracts.md) — supported harness command surfaces and session-ref capture contracts.
6. [`docs/multi-board-behavior.md`](./docs/multi-board-behavior.md) — board picker, Master aggregation, filters, and board CLI behavior.
7. [`docs/multiplexer-contracts.md`](./docs/multiplexer-contracts.md) — configured runtime multiplexer adapter contract.
8. [`docs/autonomous-verification.md`](./docs/autonomous-verification.md) — detailed verification guidance.

## Current Source of Truth

- SQLite is canonical durable state.
- The configured multiplexer is the runtime substrate; tmux is the default and Herdr is optional.
- A ticket is durable work metadata.
- A session is one attempt to run an agent for a ticket.
- A terminal container is the live process container for an active session (tmux window or Herdr pane/agent).
- A harness session ref is the harness-native resume handle, when available.
- Only one active session per ticket is allowed.
- Starting fresh must preserve prior session history and create a new active session row.
- Opening an already-active valid window must switch to it without creating a duplicate session row.
- Inactive latest sessions must project as meaningful terminal state (`closed`, `error`, etc.), not `not_started`.

## Current Work

- Keep docs reconciled with current behavior.
- Improve runtime detection confidence when locally verifiable sources become available.
- Polish TUI readability and attention styling without weakening compact keyboard-first usage.
- Improve board deletion/archive/export semantics before making destructive workflows more prominent.
- Consider canonical column types if exact column-name Master aggregation becomes confusing.

## Required Verification

For meaningful code changes, run:

```bash
go fmt ./...
go test ./...
go vet ./...
./scripts/smoke.sh --skip-checks
```

Use plain `./scripts/smoke.sh` when you want the script to run fmt/test/vet itself.

For TUI/UI, layout, scrolling, modal, readability, or keybinding changes, also load the project `kanbi-ui-validation` skill and drive the real application in its isolated tmux fixture. Validate the relevant flow at the reported terminal size and at 80x24; do not rely only on model tests or inspect the user's live database.

For lifecycle, tmux, storage, or harness changes, also run focused tests:

```bash
go test ./internal/harness ./internal/tmux ./internal/storage
```

For real harness changes, only after deterministic checks pass, use the opt-in script:

```bash
KANBI_REAL_HARNESS_TESTS=1 KANBI_REAL_HARNESSES=pi,codex ./scripts/real-harness-lifecycle.sh
```

Report verification commands and results in your final response.

## Commit Expectations

After completing a requested change and passing the appropriate verification, commit your own work before handing back unless the user explicitly asks not to commit. Keep commits focused: include only files you intentionally changed for the task, do not sweep in unrelated working-tree changes, and mention the commit hash in your final response.

## Stop Conditions

Stop and ask before:

- changing the documented project scope;
- deleting, rewriting, or flattening ticket/session history;
- making real harness behavior assumptions that cannot be tested locally;
- adding a persistent background process;
- changing supported harness command names;
- making a harness appear resumable without verified session refs;
- removing tmux as the default v1 runtime backend.

## Common Change Map

| Change | Read | Edit | Test |
| --- | --- | --- | --- |
| TUI/card/keybinding | architecture, state docs, multi-board doc if relevant | `internal/tui` | `go test ./internal/tui` |
| Lifecycle/session | state + lifecycle docs | `internal/tmux`, `internal/storage`, `internal/tui` | `go test ./internal/harness ./internal/tmux ./internal/storage` |
| Harness | harness contracts + lifecycle docs | `internal/harness`, `internal/config`, `internal/tmux` | harness/storage/tmux tests, smoke, opt-in real harness if needed |
| Storage/schema | state docs + multi-board doc if relevant | `internal/storage` | `go test ./internal/storage` plus focused lifecycle tests |
| CLI | README + architecture | `cmd/kanbi`, storage/tmux as needed | `go test ./cmd/kanbi ./internal/storage` |
| Verification scripts | autonomous verification doc | `scripts/*`, tests | changed script directly; baseline if dev loop changes |

## Documentation Expectations

When changing behavior, update the closest source-of-truth doc in the same change:

- User-facing behavior: `README.md`
- Architecture, scope, invariants, package ownership, or stop conditions: `docs/architecture.md`
- Runtime state model: `docs/state-management.md`
- Ticket/session command behavior: `docs/ticket-session-lifecycle.md`
- Harness command/ref behavior: `docs/harness-contracts.md`
- Multi-board behavior: `docs/multi-board-behavior.md`
- Verification process: `docs/autonomous-verification.md`
