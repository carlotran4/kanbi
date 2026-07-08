# Ticket And Session Lifecycle

This document specializes `docs/state-management.md` for ticket/session commands. The important separation is:

- A **ticket** is durable work metadata: title, body, harness preference, workflow column, archive status.
- A **session** is one attempt to run an agent for a ticket.
- A **runtime container** is the live process container for an active session. In tmux this is a window; in Herdr this is an agent/pane in a workspace.
- A **harness session ref** is the agent-native resume handle, when the harness exposes one.

## Lifecycle State Diagram

```mermaid
stateDiagram-v2
    [*] --> ticket_created
    ticket_created --> never_started

    never_started --> active_prompt_sent: Enter / send prompt

    active_prompt_sent --> running

    running --> waiting_for_user: detected prompt/wait
    running --> needs_permission: detected approval request
    running --> idle_unknown: idle heuristic
    running --> error: launch/resume/poll error

    waiting_for_user --> running: user/agent output
    needs_permission --> running: user approves
    idle_unknown --> running: new output

    running --> closing: x/archive/autoclose
    waiting_for_user --> closing: x/archive/autoclose
    needs_permission --> closing: x/archive/autoclose
    idle_unknown --> closing: x/archive

    closing --> closed_resumable: graceful close + session_ref
    closing --> closed_unresumable: graceful close without session_ref
    closing --> error: close failure

    closed_resumable --> active_resumed: open
    closed_unresumable --> repair_needed: open
    error --> repair_needed: open

    repair_needed --> active_resumed: edit ref / retry
    repair_needed --> active_prompt_sent: start fresh with prompt

    running --> archived: archive after safe close
    closed_resumable --> archived: archive
    closed_unresumable --> archived: archive
    error --> archived: archive
```

## Command Data Flow

```mermaid
flowchart TD
    TUI[TUI Enter action] --> Ticket[projected ticket]
    Ticket --> Decision{latest session?}
    Decision -- none --> Start[start harness with rendered prompt]
    Decision -- active --> Validate[validate container ref]
    Validate -- valid --> Switch[focus container]
    Validate -- invalid --> Ref{session_ref?}
    Decision -- inactive --> Ref
    Ref -- yes --> Resume[start harness resume command]
    Ref -- no --> Repair[repair/start fresh screen]
    Start --> Upsert[upsert active session with multiplexer container ref]
    Resume --> Upsert
    Switch --> NoWrite[no DB session row]
    Upsert --> CaptureRef[best-effort session ref capture]
    CaptureRef --> Board[reload board]
```

## Harness Matrix

| Harness | Start, open-only | Start, send prompt | Resume | Prompt injection mode | Ref capture |
| --- | --- | --- | --- | --- | --- |
| Pi | `pi` | `pi <prompt>` | `pi --session <ref>` | arg | scan `~/.pi/agent/sessions` for matching prompt |
| Codex | `codex --no-alt-screen` | `codex --no-alt-screen <prompt>` | `codex resume --no-alt-screen <ref>` | arg | scan `~/.codex/history.jsonl` for matching prompt |
| Copilot | `copilot` | `copilot -i <prompt>` | `copilot --resume=<ref>` | arg | query `~/.copilot/session-store.db` for matching cwd/prompt |
| Fake/smoke paste harness | configured command | start, wait for ready, tmux paste | configured resume | paste | parse pane marker such as `SESSION_REF=` |

## Command Rules

### Send Prompt Semantics

For a never-started ticket, the default `Enter` action sends the prompt and opens the ticket:

- Allowed only when the ticket has never started.
- Uses the ticket body rendered as Markdown prompt.
- For arg-mode harnesses, append prompt to the harness command.
- For paste-mode harnesses, wait for prompt-ready, paste via tmux buffer, send Enter.
- Creates exactly one active session row.
- Attempts harness-specific session ref capture.

### `Enter`: Default Ticket Action

- If the ticket has never started, send the rendered prompt and open the ticket.
- If the latest session is active and its stored multiplexer container validates, switch/focus it. Existing tmux sessions still validate in their stored `tmux_session_name`; Herdr sessions focus their stored agent/pane target.
- If the latest session is inactive and has a `session_ref`, resume it and create a new active session row.
- If the latest session is inactive and has no `session_ref`, show repair/start-fresh.
- Opening an already-active valid terminal container must not create a new session row.

### Repair

- `r` retries the open path.
- `e` edits the session ref, saves it, then tries to open/resume.
- `f` starts fresh with the rendered ticket prompt. The previous session history remains; the new run becomes the only active session and is launched through the currently configured multiplexer. tmux launches use the current executable's runtime tmux session and a separate window rather than reusing any existing same-named window; Herdr launches use the configured Herdr session/workspace strategy.
- `c` cancels.

### Close / Archive

- `x` sends harness exit keys first, waits for process/container exit, then closes the terminal container after timeout if needed.
- A successfully closed session is inactive and keeps its session ref if one was captured.
- Archiving a running ticket uses the same safe close path before hiding the ticket.

## Audit Findings Applied

- Copilot was previously configured like a paste-mode fake harness. Current Copilot CLI help exposes `-i <prompt>` for interactive prompt execution and `--resume=<id>` for resume, so the default adapter now uses arg mode through the `copilot` binary; session refs are captured from `~/.copilot/session-store.db` only after the first user turn matches the rendered prompt.
- Active vs inactive session state now controls whether terminal container metadata is trusted.
- A valid active open switches without writing a duplicate session row.
- Inactive latest sessions remain visible as `closed` or `error`, not `not_started`.
