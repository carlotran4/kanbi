# Architecture

Agent Kanban is a Go/Bubble Tea TUI and CLI for supervising multiple resumable agent sessions across one or more kanban boards. The application keeps durable state in SQLite and uses tmux windows as the v1 runtime backend.

## Core Model

```mermaid
flowchart LR
    CLI[CLI commands] --> Store[(SQLite)]
    TUI[Bubble Tea TUI] --> Store
    TUI --> Manager[tmux manager]
    CLI --> Manager
    Manager --> Tmux[tmux session/windows]
    Manager --> Harness[Pi/Codex/Copilot/Fake harness]
    Harness --> Ref[Harness session ref]
    Manager --> Store
    Store --> Projection[Board/ticket projection]
    Projection --> TUI
    Sync[Ticket backend sync] --> Store
```

- **SQLite is canonical durable state for local boards and runtime/session state.** Tickets, columns, boards, and session history live there; external ticket backends, when implemented, are cached/projected through SQLite while owning their board's ticket metadata.
- **tmux is observed runtime state.** A stored tmux window id/name is only trusted after validation against live tmux.
- **Harnesses are compiled adapters.** v1 intentionally does not support arbitrary user-defined harness adapters.
- **The TUI is a projection plus command surface.** It renders board/session state and dispatches lifecycle actions.
- **Ticket backend adapters are board-scoped.** Each board has exactly one ticket metadata backend chosen at creation. `local` and `github` are implemented today; the adapter seam remains prepared for Atlassian/Jira, Asana, and similar systems.

## Current Objective And Scope

Keep Agent Kanban a trustworthy alpha for multi-board, tmux-backed ticket/session lifecycle management across Pi, Codex, Copilot, and fake harnesses.

Maintain these behaviors as boring, reliable, documented alpha behavior:

- starting a ticket creates exactly one active session attempt;
- opening an active ticket switches to the right tmux window;
- closed/error sessions remain visible as meaningful ticket state;
- stale tmux window ids never attach one ticket to another ticket's session;
- resumable sessions use the correct harness-native resume command;
- unresumable sessions route through repair/start-fresh without corrupting history;
- each board owns its ticket numbers and working directory;
- Master aggregates tickets across boards without changing ticket ownership;
- each harness has clearly documented behavior and verification coverage.

Current follow-up work is tracked as tickets on the board. Known focus areas are docs reconciliation, runtime detection confidence, TUI readability/attention styling, board deletion/archive/export semantics, and possible canonical column types for Master aggregation.

## Stop Conditions

Stop and ask before:

- changing current project scope;
- deleting, rewriting, or flattening ticket/session history;
- making real harness behavior assumptions that cannot be tested locally;
- adding a persistent background process;
- changing supported harness command names;
- making any harness appear resumable without a stored or user-provided session ref;
- replacing tmux as the v1 runtime backend.

## Package Map

| Path | Responsibility |
| --- | --- |
| `cmd/agent-kanban` | CLI entrypoint, command parsing, board startup, doctor command, board/ticket CLI actions. |
| `internal/config` | Config loading, XDG/env path resolution, and applying built-in harness defaults from `internal/harness`. |
| `internal/storage` | SQLite migrations, board/column/ticket/session persistence, external ticket identity/cache fields, board projections, master-board filtering. |
| `internal/ticketbackend` | Board-scoped ticket metadata backend registry and startup/periodic sync orchestration. Implements the no-op `local` backend and GitHub Issues sync. |
| `internal/tmux` | Dedicated tmux session/window orchestration, ticket open/resume/close/start-fresh, runtime polling, session reconciliation. |
| `internal/harness` | Localized built-in harness contracts, command construction, prompt mode/ref capture behavior, output/runtime detection helpers. |
| `internal/tui` | Bubble Tea model/update/view, keybindings, board picker, cards, filters, repair/prompt fallback screens, and terminal-gated image previews. |
| `internal/prompt` | Ticket body/prompt rendering. |
| `internal/attachments` | XDG data-dir ticket attachment storage and pasted image detection. |
| `scripts/` | Development launcher, deterministic smoke tests, fake harnesses, opt-in real harness lifecycle script. |
| `docs/` | Source-of-truth docs for state, lifecycle, harness contracts, verification, multi-board behavior, and archived product context. |

## Runtime Topology

Agent Kanban uses tmux sessions per board executable instance by default:

```text
tmux session: agent-kanban-board-<pid>-<time>-1
windows:
  board
  <board-id>-T-001-some-ticket
  <board-id>-T-002-another-ticket

tmux session: agent-kanban-board-<pid>-<time>-2
windows:
  board
  <board-id>-T-003-other-ticket
```

The board process runs in the stable `board` window of its instance session. Each active ticket session gets its own tmux window in the runtime session owned by the board instance that launched it. The app uses windows, not panes, for ticket sessions.

When launched outside tmux, the CLI creates a unique board/client tmux session and sets that same session as the ticket runtime for the inner board process. When launched directly inside tmux without an explicit `AGENT_KANBAN_TMUX_SESSION`, the current tmux session is used as that executable's runtime. `AGENT_KANBAN_INNER=1` prevents recursive launching.

Session rows persist `tmux_session_name` as well as `tmux_window_id/name`. Other Agent Kanban instances can see these rows through SQLite and validate/switch/capture/close using the stored tmux session instead of assuming their own runtime session.

## Durable Data Relationships

```mermaid
erDiagram
  boards ||--o{ columns : owns
  boards ||--o{ tickets : owns
  columns ||--o{ tickets : contains
  tickets ||--o{ sessions : launches
  tickets ||--o{ ticket_notes : has
```

- A **board** owns columns, display numbering, a working directory, and exactly one ticket metadata backend.
- The **Master board** is a synthetic all-boards view; it is not a stored board row.
- A **ticket** is durable work metadata: title, body, harness preference, workflow column, archive status.
- A **session** is one attempt to run an agent for a ticket.
- **Ticket notes** are durable notes per ticket; local-board notes remain personal/local, while future external backends should map notes to provider comments.
- An **active session** is a session believed to own a live tmux window, but it must still pass validation before being trusted.
- A **tmux window** is the live process container for an active session.
- A **harness session ref** is the harness-native resume handle when the harness exposes one.
- **Start fresh** creates a new active session attempt while preserving prior session rows.

See [`docs/multi-board-behavior.md`](./multi-board-behavior.md) for board aggregation and Master view behavior. See [`docs/ticket-backends.md`](./ticket-backends.md) for the board-scoped ticket backend model.

## Ticket/Session Lifecycle Invariants

These are core architecture rules, not optional implementation details:

1. Only one active session per ticket is allowed.
2. Starting fresh deactivates any old active session and creates a new session row; it must not delete old session history.
3. Opening an already-active valid window switches to it without creating a new session row.
4. A stored tmux window id is valid only when live tmux still reports that id with the expected ticket window name; name-based fallback must target the session row's stored tmux session name.
5. Inactive latest sessions project as terminal states such as `closed` or `error`, not `not_started`.
6. `send prompt` is allowed only for never-started tickets.
7. Repair/start-fresh flows must preserve history and avoid silently attaching a ticket to the wrong live window.
8. Real harness session refs must come from verified local evidence, not assumptions.

For the full state model, read [`docs/state-management.md`](./state-management.md) and [`docs/ticket-session-lifecycle.md`](./ticket-session-lifecycle.md).

## Command/Data Flows

### Add Ticket

```mermaid
flowchart LR
    CLIorTUI[CLI/TUI add] --> Store[(storage)]
    Store --> Column[First or selected column]
    Store --> Ticket[Ticket row with board-local display id]
```

### Default Ticket Action

```mermaid
flowchart TD
    Enter[Enter on selected ticket] --> Latest{Latest session?}
    Latest -- none --> Start[Start harness with rendered prompt]
    Latest -- active --> Validate[Validate tmux id/name]
    Validate -- valid --> Switch[Switch to window]
    Validate -- invalid --> Ref{Session ref?}
    Latest -- inactive --> Ref
    Ref -- yes --> Resume[Resume harness]
    Ref -- no --> Repair[Repair/start-fresh screen]
    Start --> Capture[Best-effort session ref capture]
    Capture --> Store[(Create/update active session metadata)]
    Resume --> Store[(Create active session)]
```

### Runtime Refresh

```mermaid
flowchart TD
    Tick[TUI tick] --> Active[Projected active sessions]
    Active --> Validate[Validate tmux window]
    Validate --> Capture[Capture pane output]
    Capture --> Detect[Harness/pattern/idle detection]
    Detect --> Store[(Runtime metadata update)]
    Store --> TUI[Updated card labels/indicators]
```

## Harness Architecture

Built-in harness contracts are localized in `internal/harness`: command defaults, prompt mode, exit keys, ref capture, and docs anchors are grouped per supported harness. `internal/config` applies those defaults and preserves YAML overrides, while `internal/tmux` owns orchestration. Current supported harnesses:

| Harness | Start with prompt | Resume | Ref source |
| --- | --- | --- | --- |
| Pi | `pi <prompt>` plus bundled ref extension | `pi --session <ref>` | extension handoff, fallback session JSONL scan |
| Codex | `codex --no-alt-screen <prompt>` | `codex resume --no-alt-screen <ref>` | `~/.codex/history.jsonl` |
| Copilot | `gh copilot -- -i <prompt>` | `gh copilot -- --resume=<ref>` | `~/.copilot/session-store.db` |
| Fake/smoke | script-dependent | script-dependent | pane marker such as `SESSION_REF=` |

Always update [`docs/harness-contracts.md`](./harness-contracts.md) when harness behavior changes.

## Where To Make Common Changes

| Task | Likely files | Required doc updates |
| --- | --- | --- |
| Add/change CLI command | `cmd/agent-kanban/main.go`, command tests | `README.md` if user-facing |
| Change config/defaults | `internal/config/*` | `README.md`, possibly `docs/harness-contracts.md` |
| Change schema/storage behavior | `internal/storage/*` | `docs/state-management.md` or lifecycle docs |
| Change ticket/session lifecycle | `internal/tmux/*`, `internal/storage/*`, `internal/tui/*` | `docs/state-management.md`, `docs/ticket-session-lifecycle.md` |
| Change harness command/ref capture | `internal/harness/*`, `internal/config/*`, `internal/tmux/*` | `docs/harness-contracts.md` |
| Change card rendering/keybindings | `internal/tui/model.go`, `internal/tui/model_test.go` | `README.md` or a controls doc if user-facing |
| Change multi-board behavior | `internal/storage/*`, `internal/tui/*`, CLI board commands | `docs/multi-board-behavior.md`, `README.md` |
| Change verification process | `scripts/*`, tests | `docs/autonomous-verification.md`, `AGENTS.md` if onboarding changes |

## Testing Strategy

- Prefer fast deterministic unit/model/storage tests.
- Use fake harnesses for automated lifecycle coverage.
- Keep real harness tests opt-in because they can consume quota and depend on auth/local history.
- Tmux tests must isolate session names and clean up immediately.
- After meaningful changes, follow the verification loop in [`AGENTS.md`](../AGENTS.md) and [`docs/autonomous-verification.md`](./autonomous-verification.md).

## Historical Design Context

[`docs/archive/design-spec.md`](./archive/design-spec.md) contains historical product context and earlier design decisions. Do not read it for normal implementation work. When it conflicts with current docs, prefer this order:

1. `AGENTS.md`
2. `docs/architecture.md`
3. `docs/state-management.md`
4. `docs/ticket-session-lifecycle.md`
5. `docs/harness-contracts.md`
6. `docs/multi-board-behavior.md`
7. `README.md`
8. `docs/archive/design-spec.md` (historical context only)
