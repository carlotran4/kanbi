# GOAL

Harden Agent Kanban from MVP skeleton into a trustworthy alpha for real
ticket/session lifecycle management across multiple agent harnesses.

Read these files first:

- `design-spec.md`
- `docs/autonomous-verification.md`
- `docs/state-management.md`
- `docs/ticket-session-lifecycle.md`
- `PROGRESS.md`
- `README.md`

## Current Baseline

The original MVP foundation is implemented:

- CLI/config/storage with SQLite persistence and default board setup.
- Bubble Tea kanban board with ticket navigation, movement, editing, archiving, column operations, and session actions.
- tmux-backed board/ticket windows with session metadata and startup reconciliation.
- fake harness smoke testing.
- real command wiring for Pi, Codex, and Copilot command surfaces.
- runtime polling, conservative state detection, manual state marking, repair/start-fresh flows, graceful close, and autoclose plumbing.

## Objective

Make the real harness lifecycle boring and reliable:

- starting a ticket creates exactly one active session attempt;
- opening an active ticket switches to the right tmux window;
- closed/error sessions remain visible as meaningful ticket state;
- stale tmux window ids never attach one ticket to another ticket's session;
- resumable sessions use the correct harness-native resume command;
- unresumable sessions route through repair/start-fresh without corrupting history;
- each harness has clearly documented behavior and verification coverage.

## Scope

### 1. Real-Harness Lifecycle Verification

Add an opt-in real-harness integration script separate from `scripts/smoke.sh`,
for example `scripts/real-harness-lifecycle.sh`.

The script should:

- use temporary `AGENT_KANBAN_DB`, config, data, and state paths;
- use a uniquely named tmux session;
- run `agent-kanban doctor`;
- add one ticket for each enabled real harness;
- open/send prompt for each ticket;
- verify a managed tmux window exists for each ticket;
- verify the prompt appears in the agent pane or the harness starts with the prompt argument;
- capture the resulting session row from SQLite;
- close the session and confirm it becomes inactive;
- reopen/resume when a `session_ref` exists;
- clean up the tmux session and temp files.

This script must be opt-in because real harnesses can consume model quota, depend on auth, and may alter local harness histories.

Suggested controls:

- `AGENT_KANBAN_REAL_HARNESS_TESTS=1` must be set.
- `AGENT_KANBAN_REAL_HARNESSES=pi,codex,copilot` selects harnesses.
- Default to no-op with a clear message when the opt-in variable is absent.

### 2. Harness-Specific Contracts

Keep hardcoded Go adapters, but document and test each adapter's actual contract.

Pi:

- start/send prompt: `pi <prompt>`
- resume: `pi --session <session_ref>`
- session ref capture: scan `~/.pi/agent/sessions` for a matching prompt
- verification must include start, ref capture when available, close, and resume

Codex:

- start/send prompt: `codex --no-alt-screen <prompt>`
- resume: `codex resume --no-alt-screen <session_ref>`
- session ref capture: scan `~/.codex/history.jsonl` for a matching prompt
- verification must include start, ref capture when available, close, and resume

Copilot:

- start/send prompt: `gh copilot -- -i <prompt>`
- resume: `gh copilot -- --resume=<session_ref>`
- session ref capture is not yet known to be automatic
- until a stable ref source is found, Copilot should be treated as start-capable and manually repairable/resumable only when the user provides a ref

Do not silently pretend Copilot has automatic resume refs. The UI/docs should make that limitation clear.

### 3. Ticket/Session State Machine Hardening

Use `docs/state-management.md` and `docs/ticket-session-lifecycle.md` as the target model.

Verify and, if needed, fix:

- no duplicate session rows when opening an already-active valid window;
- inactive latest sessions project as `closed` or `error`, not `not_started`;
- stale `tmux_window_id` values are validated against the expected window name before switch/close/capture;
- `send prompt` is rejected for any ticket with prior session/window/ref metadata;
- start-fresh preserves previous session history and creates a new active session;
- repair flow handles missing window, missing ref, resume failure, and edited ref;
- autoclose never closes on the same tick that first detects an eligible attention state;
- manual state marks are not overwritten by weak heuristics.

### 4. Verification Loops

Every implementation checkpoint must run the deterministic loop:

```bash
go fmt ./...
go test ./...
go vet ./...
./scripts/smoke.sh
```

For lifecycle or harness changes, also run targeted checks:

```bash
go test ./internal/harness ./internal/tmux ./internal/storage
```

For real harness work, run the opt-in loop only after deterministic tests pass:

```bash
AGENT_KANBAN_REAL_HARNESS_TESTS=1 AGENT_KANBAN_REAL_HARNESSES=pi,codex ./scripts/real-harness-lifecycle.sh
```

If Copilot is included, record whether the run verified only start/open behavior or also a real resume ref:

```bash
AGENT_KANBAN_REAL_HARNESS_TESTS=1 AGENT_KANBAN_REAL_HARNESSES=copilot ./scripts/real-harness-lifecycle.sh
```

Agents must record verification results in `PROGRESS.md` after meaningful checkpoints.

### 5. TUI Alpha Polish After Lifecycle Stability

Only after lifecycle behavior is stable:

- make runtime state/repair/resumable status easier to read on cards;
- reduce footer density without hiding critical controls;
- improve repair and prompt fallback screens;
- consider richer attention styling while preserving compact kanban layout.

## Do Not Implement Yet

- background daemon;
- web UI;
- multi-board UI;
- full transcript storage;
- generic user-defined harness adapters;
- replacing tmux as the v1 runtime backend;
- automatic Copilot session-ref capture unless a stable, locally verifiable source is found.

## Stop Conditions

Stop and explain the decision needed before:

- changing `design-spec.md` scope;
- deleting or rewriting ticket/session history;
- making real harness behavior assumptions that cannot be tested locally;
- adding a persistent background process;
- changing supported harness command names;
- making Copilot appear fully resumable without verified session refs.
