# Workspace Operators

Operators are ordinary managed harness conversations with a stable workspace-wide
identity, standing instructions and optional wake conditions. They have no board,
ticket or workflow column. The working directory is execution context only.
Pi, Codex, Claude Code and Copilot CLI use their usual authentication, tools,
skills and permissions. Existing sessions cannot be converted; the harness is
chosen at creation and cannot be edited.

Press `O` on the board to open the collapsible right panel. At widths below 120
columns the board retains a one-line summary and the focused list uses an overlay.
`Esc` returns focus to the board; `O` focuses the list again, and `O` while focused
collapses it. Operators stay available across board changes. No inspector is needed.

Within the list: `n` creates, `e` edits, `Enter` opens the actual Herdr session,
`r` queues Run now, `p` pauses/resumes scheduling, and `s` sends the normal interrupt
to stop a turn. `x` explicitly closes the session, `f` starts fresh, and `d` deletes;
these last three actions require confirmation. Fresh refuses an active session:
close it explicitly first. Delete retires triggers and the messaging address,
retains configuration/history/instructions, and leaves an active harness tab open.
Retired tabs continue normal runtime observation until closed or exited.
Close the session first if you also want to close its tab.

## Instructions and configuration

The authoritative file is `<resolved-config-directory>/operators/<id>/instructions.md`.
The resolved directory is the parent of Kanbi's configuration file, including a
`KANBI_CONFIG` override. The editor reads/writes this file; direct file edits take
effect at the next wake. Every generated wake explicitly directs the agent to
read its absolute path before acting and identifies its trigger. Initialization
explains this contract. Fresh conversations preserve the same file and identity.
Kanbi prescribes no `state.md`, custom-agent definition or agent memory framework.
A turn already in progress may finish with its previously read instructions.

The editor supports name, immutable harness, working directory, five-field cron,
timezone, scheduled prompt and multiline instructions. The name sits in the title
border, with harness and directory above the main instructions editor. `Tab` /
`Shift+Tab` move between fields; the harness is skipped after creation. Focus
Schedule and press `Enter` for cron, timezone and the multiline scheduled prompt.
`Ctrl+S` or `Esc` returns from scheduling to the draft; save the main editor with
`Ctrl+S` to persist all changes, or `Esc` to discard the draft. Instructions and
scheduled prompts share the ticket description editor, including word deletion
and `Ctrl+E` to open `$EDITOR`. External-editor changes return to the draft before
the main save. The instructions viewport grows with the terminal.
An empty schedule disables recurrence. Presets are `15m`, `hourly` and `daily`
(daily means 09:00 in the selected timezone). Cron supports standard lists, ranges,
steps and names through robfig/cron; `@every` intervals are deliberately excluded.
Use an explicit IANA timezone, for example `America/Chicago` or `UTC`.

```sh
kanbi operators create --name Intake --harness pi --cwd /projects \
  --instructions-file ./intake.md --schedule 15m --timezone America/Chicago \
  --prompt 'Inspect incoming requests and reconcile existing work.'
kanbi operators list
kanbi operators edit 1 --instructions-file ./revised-intake.md
kanbi operators run 1
kanbi operators pause 1
kanbi operators resume 1
kanbi operators open 1
kanbi operators stop 1
kanbi operators close 1
kanbi operators fresh 1
kanbi operators retry 1
kanbi operators delete 1
```

`run` and `retry` explicitly clear a surfaced launch/resume failure; retry releases
pending work without generating another Run now. Neither silently replaces an
unresumable conversation. Use close/fresh deliberately to create a new conversation.
Working-directory edits apply to the next launch; a running harness retains its
existing launch directory.

The immutable harness can only be changed by deleting and recreating an Operator.

## Wakeups and recovery

```sh
kanbi operators wakeups create 1 --at 2026-12-01T15:30:00-06:00 \
  --prompt 'Follow up on the deployment decision.'
kanbi operators wakeups list 1
kanbi operators wakeups cancel 7
```

Run now, recurrence, individual followups and agent messages use the existing
session mailbox and native notification adapters. No second mailbox is installed.
System wake rows commit their one mailbox entry atomically. Recurring occurrences
coalesce while a check is pending, including downtime; the next occurrence is
computed from the fixed clock. Distinct followups retain separate prompts and IDs.
Confirmed receipts complete wake rows; completed entries never replay. Ambiguous
transport outcomes retain the mailbox's uncertain fence and require explicit
`kanbi messages retry ID` after inspecting the session. There are no automatic
retries of failed turns or failed resume operations.

The board process owns one cancellable, joined in-process scheduling worker.
Multiple instances serialize runtime operations through Kanbi's existing database
runtime lock; SQLite independently deduplicates wake materialization and mailbox
handoff claims. Scheduler presence is registered per manager/process and removed
on shutdown. Native receivers require a live registered Kanbi process before
claiming Operator work. Kanbi exit stops new activations and leaves harness tabs
running. No detached service, external scheduler or OS wake management is used.

Schedule pause disables recurring and one-time activation. It cancels unsent
recurring checks and advances the recurring clock when resumed, so deliberate
pause creates no catch-up. One-time followups remain pending. Peer messages,
Run now and direct user conversation continue working. Pause does not interrupt
a running turn; stop turn is a separate operation.

## Messaging and lifecycle limits

Discovery includes stable addresses such as `operator:1` in
`kanbi messages recipients`. Send using that address:

```sh
kanbi messages send operator:1 --key intake-request-42 --body 'Review this request.'
```

Operator addresses are represented as negative IDs in mailbox JSON and are
separate from positive session-attempt IDs. Replies route through the existing
message thread to the original sender. Operator mail remains addressed to the
Operator across close, resume and explicit fresh conversations. Ticket-session
addresses retain their existing per-attempt behavior.

Activation starts an ordinary session or resumes only a captured native ref.
Resume failure is visible and blocks automatic relaunch until manual retry or
explicit fresh. Existing valid sessions are reused. Native Herdr `done` is
projected as `completed` for Operators; native `idle` with explicit editor
readiness and no input-request reason is `ready`, including after resume.
`blocked` remains awaiting user input or permission. Running, unknown/quiet and
permission states do not authorize Operator input. Missing native completion or
readiness signals defer delivery rather than guessing.
The initial harness prompt explains the instructions file and waits for queued
work; responsibility wakes then arrive through the normal mailbox path.

Pi checks native idle state, pending work and an empty editor again before native
followup submission. Codex uses its verified thread's native queue. Claude uses
its native inbox and retains hold/refuse behavior. Copilot uses the accepted
best-effort terminal paste plus Enter: known busy/blocked/permission states defer,
but an unsent draft can combine with the notice. Draft preservation is **not
guaranteed for Copilot**. No adapter bypasses harness permissions. See
[session messaging](session-messaging.md) for transport receipts and native evidence.

SQLite holds schedules, pause state, wake timestamps, pending prompts, mail and
session attempts. The instructions file is not an execution log. A database-only
backup contains Operator metadata/history/mail, but not the separately stored
instructions files; preserve the config directory alongside database backups.


## Verification evidence (2026-11-07)

Deterministic coverage exercises persistence/migration, session-ID high-water
preservation, stable addressing/replies, coalescing and recovery, pause and
followup cancellation, competing connections, native blocked/ready distinction,
retired addresses, and the four harness lifecycle adapters. Full Go tests, vet,
focused storage/runtime race checks, isolated Herdr smoke and backup/rollback
checks passed during implementation. The actual TUI was driven at 160x45 and
80x24 through creation, instruction editing, pause, board switching, narrow
modals and deletion cancellation.

Authenticated pi and Codex Operators produced unique wake responses, reread
instructions edited directly on disk without replacing their conversations,
and preserved identity/history through close and wake-triggered verified resume. The opt-in
reproduction is:

```sh
KANBI_REAL_HARNESS_TESTS=1 KANBI_REAL_HARNESSES=pi,codex \
  python3 scripts/prototypes/real-operators.py
```

`KANBI_OPERATOR_TEST_BIN` optionally selects an already-built binary. Claude
provider verification is unavailable without a subscription in this environment.
Copilot's real fixture stopped at Herdr's `agent_not_ready` blocked-startup state;
its terminal fallback and busy/permission deferral are covered deterministically,
with the accepted draft limitation retained. No provider success is inferred
from those deterministic checks or from mere terminal submission.
