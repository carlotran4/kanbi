# Kanbi State Management

This document defines the state model the implementation should follow. SQLite is the canonical record; the configured multiplexer (tmux by default, Herdr when opted in) is an observed runtime substrate; the TUI is a projection plus command surface.

## Runtime State Machine

```mermaid
stateDiagram-v2
    [*] --> not_started: ticket created
    not_started --> starting: open/send prompt
    starting --> running: terminal container created
    starting --> error: launch/resume failure
    starting --> repair_needed: started before, no usable window/ref

    running --> waiting_for_user: confident prompt/wait pattern
    running --> needs_permission: permission pattern
    running --> idle_unknown: no output after idle timeout
    running --> error: process/session error

    waiting_for_user --> running: new output/user action
    waiting_for_user --> needs_permission: permission pattern
    waiting_for_user --> closing: auto-close timeout/manual close/archive

    needs_permission --> running: user approves/new output
    needs_permission --> closing: auto-close timeout/manual close/archive

    idle_unknown --> running: new output
    idle_unknown --> waiting_for_user: confident wait pattern
    idle_unknown --> needs_permission: confident permission pattern

    closing --> closed: graceful exit/window closed
    closing --> error: close failure
    closed --> starting: resume/start fresh
    error --> repair_needed: no usable window/ref
    error --> starting: retry/edit ref/start fresh
    repair_needed --> starting: retry/edit ref/start fresh
```

## Session Activity Rules

```mermaid
stateDiagram-v2
    [*] --> no_session
    no_session --> active_session: start/open
    active_session --> inactive_resumable: graceful close with session_ref
    active_session --> inactive_error: missing terminal container / launch error / stale container id
    inactive_resumable --> active_session: resume
    inactive_error --> active_session: repair or start fresh
```

- `sessions.is_active = 1` means the session owns the ticket's single runtime slot. A `starting` row is a durable pre-launch claim and may not have a container reference yet; after launch it represents the live tmux window or Herdr agent/pane owned by the ticket.
- `sessions.is_active = 0` does not mean the ticket is `not_started`. The ticket should project the latest session's terminal state (`closed`, `error`, `exited`) and any `session_ref`.
- A tmux `window_id` is valid only if tmux still reports that id with the expected ticket window name. Name fallback must use the session row's stored `tmux_session_name`, not the current process's runtime session. Window ids can be reused after windows close. Herdr sessions store generic `multiplexer`, `mux_namespace`, `mux_container_id`, `mux_container_name`, and `mux_metadata` fields; Herdr-native agent status is authoritative when it is not `unknown`.
- Only one active session per ticket is allowed and SQLite enforces that invariant. Start/resume first writes a `starting` claim before launching a container, so concurrent Kanbi processes cannot both launch the same ticket. Starting fresh atomically deactivates the old active session and creates the new claim in the currently configured multiplexer. tmux launches use the current executable's runtime tmux session; if a same-named tmux window already exists in that runtime session, the new window uses a unique suffix. Herdr launches use the configured Herdr session/workspace strategy.
- Launch, resume-liveness, persistence, and paste-prompt failures transition the claim to inactive `error`; any container created for the failed attempt is closed best-effort so it cannot remain untracked.

## Ticket Projection Data Flow

```mermaid
flowchart LR
    tickets[(tickets)] --> BoardView
    sessions_latest[(latest session per ticket)] --> BoardView
    BoardView --> TUI[TUI cards]
    TUI --> Commands[open/send/close/mark/archive/edit]
    Commands --> Store[(SQLite)]
    Commands --> Runtime[Runtime manager]
    Runtime --> Store
```

Projection rules:

- Tickets with no session project as `not_started`.
- Tickets with a latest active session project that session's runtime status and active container indicator.
- Tickets with a latest inactive session project that terminal status (`closed`, `error`, `exited`) and resumable/error indicator.
- Cards show active indicator only for active sessions with a validated terminal container.
- Cards show resumable indicator when no active window exists but a `session_ref` exists.
- Cards show error indicator for `error`.

## Runtime Watcher Data Flow

```mermaid
flowchart TD
    Tick[2s TUI tick] --> List[List projected tickets]
    List --> Active{latest session active?}
    Active -- no --> Skip[do not poll]
    Active -- yes --> Validate[validate container ref]
    Validate -- invalid --> Missing[mark latest session inactive error]
    Validate -- valid --> Capture[capture pane]
    Capture --> Detect[adapter/pattern/idle detection]
    Detect --> Manual{previous source manual?}
    Manual -- yes + non-pattern heuristic --> Preserve[preserve manual state]
    Manual -- no or confident pattern --> Update[update runtime metadata]
    Update --> AutoClose{eligible and stable long enough?}
    AutoClose -- yes --> Close[graceful close]
    AutoClose -- no --> Done
```

Watcher rules:

- Pattern detections (`waiting_for_user`, `needs_permission`, `error`) may overwrite manual state.
- Heuristics (`running`, `idle_unknown`, generic pane output) should not immediately overwrite a manual override.
- Auto-close should never trigger in the same tick that changes a ticket into an eligible state; timeout age starts at `last_state_change_at`.

## Open/Resume Data Flow

```mermaid
flowchart TD
    Open[Open selected ticket] --> Active{latest session active?}
    Active -- yes --> Validate[validate window id/name]
    Validate -- valid --> Switch[focus terminal container]
    Validate -- invalid --> Ref{session_ref exists?}
    Active -- no --> Ref
    Ref -- yes --> Resume[create window with harness resume]
    Ref -- no --> Started{any prior session?}
    Started -- no --> Start[start harness]
    Started -- yes --> Repair[repair/start-fresh prompt]
```

Open rules:

- Do not create a new DB session when merely switching to an already-active valid window.
- Do not trust stale terminal container ids without multiplexer validation.
- If a previous session exists but no valid window/ref exists, show repair/start-fresh.
- `send prompt` is only valid for a never-started ticket.

## Column State Rules

```mermaid
stateDiagram-v2
    [*] --> visible_column
    visible_column --> deleted: no active tickets
    visible_column --> blocked: active tickets exist
```

- Column deletion is blocked by active tickets only, because archived tickets are not visible board cards.
- Archived tickets are preserved by moving them to a surviving column before deleting the column.
- Ticket archival is rejected at the storage seam while a session is active. Lifecycle callers must successfully close the active runtime container before archiving; remote backend sync follows the same rule.

## Implementation Audit Notes

The current implementation is intentionally split this way:

- `internal/storage`: owns canonical ticket/session rows, ticket notes, and the ticket projection used by `BoardView`.
- `internal/tmux`: owns lifecycle orchestration, tmux validation, Herdr adapter dispatch, start/resume/switch/close, runtime refresh, and repair errors.
- `internal/tui`: owns transient UI states such as edit mode, repair screen, prompt fallback, column edit, and manual mark menu.

State bugs to avoid:

- Do not derive ticket runtime from only active sessions. A closed or error latest session is still meaningful board state.
- Do not treat every latest session as active. `is_active` must be projected separately from `status`.
- Do not trust `tmux_window_id` without confirming tmux still reports the expected ticket window name for that id. Do not treat Herdr `unknown`/unavailable agent state as authoritative; fall back to pane-output/harness detection.
- Do not validate or control an existing active session against the current process's tmux session; use the stored `tmux_session_name` from the session row.
- Do not create a new DB session row just because a valid active ticket window was opened again.
- Do not let heuristic watcher output immediately overwrite a manual runtime override.
- Do not auto-close in the same tick that first detects a wait/permission state.
