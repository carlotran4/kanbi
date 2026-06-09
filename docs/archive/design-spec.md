---
tags:
  - "#AI"
  - "#OpenClaw"
links:
  - "[[AI Kanban]]"
---

> [!warning] Historical context
> This file preserves the original product/design discussion and decision log. It is not the current implementation contract. When this file disagrees with current docs, prefer `AGENTS.md`, `docs/architecture.md`, `docs/state-management.md`, `docs/ticket-session-lifecycle.md`, `docs/harness-contracts.md`, `docs/multi-board-behavior.md`, and `README.md` in that order.

## Goal
Kanbi is an agent-agnostic terminal orchestration system built around a Kanban board. It reduces cognitive load and lost sessions by giving the user one control center for all active agent work. Each ticket represents a unit of work and is tied to one or more resumable agent sessions.

Core problem: when multiple agent sessions are running, it is hard to know where the user is needed, what is still running, and which sessions can be safely resumed later.

Core experience: tickets live on a Kanban board, agent sessions run in tmux windows, and tickets flash/highlight when they need the user's attention.

## Confirmed Product Direction

### Stack
- Language: **Go**
- TUI framework: **Bubble Tea** with supporting Charm libraries such as Lip Gloss and Bubbles
- Storage: **SQLite** as canonical app state
- Config: **YAML**
- Session backend for v1: **tmux**
- Binary name: `kanbi`
- Repo: standalone Go repo under `~/Developer`
- Initial distribution: private personal tool, structured like a clean CLI project

### Supported Harnesses in v1
Each ticket/session can use one of:

- **Pi**
  - start/open-only: `pi`
  - start with prompt: `pi <prompt>`
  - resume: `pi --session <session_ref>`
- **Codex**
  - start/open-only: `codex --no-alt-screen`
  - start with prompt: `codex --no-alt-screen <prompt>`
  - resume: `codex resume --no-alt-screen <session_ref>`
- **GitHub Copilot CLI**
  - start/open-only: `gh copilot --`
  - start with prompt: `gh copilot -- -i <prompt>`
  - resume: `gh copilot -- --resume=<session_ref>`

The app should be agent-agnostic through compiled Go harness adapters. Harness command paths and basic args should be configurable, but v1 should not support fully user-defined adapters.

## Board Model

### Boards
Historical note: the original v1 plan allowed only one visible board while reserving schema room for multiple boards later. Current behavior is multi-board: startup opens a board picker, `Master` aggregates tickets from all boards, and named boards own columns, ticket numbering, and working directories. See `docs/multi-board-behavior.md`.

Default board columns:

1. Open
2. In Progress
3. Review
4. Done

Columns are arbitrary and user-defined. They should be editable in the TUI.

### Columns
Users can:

- add columns
- rename columns
- reorder columns
- delete only empty columns

If a column has tickets, deletion should be blocked with a message like: `Cannot delete non-empty column. Move tickets first.`

### Tickets
The unit of work is **title + body**.

A ticket has:

- immutable internal DB ID
- human display ID, e.g. `T-001`
- title
- body / prompt
- preferred harness, defaulting to `pi`
- workflow column
- runtime status derived from active session state
- archived timestamp if archived

The display ID should be sequential per board. Use a stable human ID like `T-001`, not UUID-only and not title-only.

Ticket title changes should:

- update the ticket title
- rename the tmux window if the ticket has an active window
- **not** rename or mutate the harness session reference after launch

### Sessions
Tickets can have multiple sessions over time. There should be a separate `sessions` table.

A session has:

- session ID/internal DB ID
- ticket ID
- harness used
- harness session ref
- harness session name, when supported
- tmux session/window metadata
- status
- timestamps
- `is_active` flag

The ticket has a preferred/default harness, but each session records the actual harness used. Starting fresh on an old ticket should preserve previous session records and create a new active session.

When possible, harness sessions should be named from the ticket title, preferably including the display ID, e.g. `T-001-fix-oauth-redirect`.

## Tmux Architecture

### Dedicated tmux Session
v1 should use a dedicated tmux session named `kanbi`.

Structure:

```text
tmux session: kanbi
windows:
  board
  b1-T-001-fix-oauth-redirect
  b1-T-002-review-api
  b2-T-001-write-tests
```

The board runs in a stable `board` window. Each active ticket gets one tmux window. Use windows, not panes, for ticket sessions.

### Running Outside tmux
If the user runs `kanbi` outside tmux, it should automatically create/attach to the dedicated tmux session.

Expected behavior:

```bash
kanbi
```

If not inside tmux, it effectively runs something like:

```bash
tmux new-session -A -s kanbi kanbi --board
```

Use an environment variable such as `KANBI_INNER=1` to avoid recursive launching.

### Navigation Between Board and Sessions
v1 is a tmux-window control center. It should not embed terminal sessions inside the Bubble Tea UI.

- Board controls tickets and session lifecycle.
- Agent sessions live in separate tmux windows.
- Selecting a ticket can switch to its tmux window or create/resume it.
- Returning to the board uses normal tmux navigation.
- Do not mutate the user's global tmux bindings in v1.
- Keep the board window consistently named `board` and display hints for returning.

### Tmux Reconciliation
SQLite is canonical for tickets and sessions, but startup should reconcile tmux state.

Store:

- `tmux_session_name`
- `tmux_window_id` if available
- `tmux_window_name`
- `last_seen_tmux_at`

Startup behavior:

- Ensure the dedicated tmux session/window exists.
- For each active ticket/session, check whether the stored tmux window exists.
- If missing while DB says running/waiting, mark runtime as `closed_unknown` or appropriate error/closed state but keep resumability if `session_ref` exists.
- If extra managed windows exist, warn and offer cleanup later.

## Session Lifecycle

### Starting a Session
A never-started ticket supports two launch modes:

1. **Send prompt**
   - Start the selected harness.
   - Wait for prompt readiness.
   - Paste/send the ticket prompt.
2. **Open only**
   - Start the selected harness.
   - Do not send the ticket prompt.
   - User types manually.

Both modes attach a harness session to the ticket and attempt to capture a session ref.

### First Prompt Format
When sending the first prompt, use Markdown:

```md
# T-001: Ticket title

Ticket body here...
```

If body is empty:

```md
# T-001: Ticket title

Ticket title
```

### Prompt Injection
Historical note: the original plan used tmux paste-buffer for most multiline prompts. Current real harness adapters use argument-mode prompt injection where supported: Pi runs `pi <prompt>`, Codex runs `codex --no-alt-screen <prompt>`, and Copilot runs `gh copilot -- -i <prompt>`. Paste-mode remains useful for fake/smoke harnesses and fallback flows. See `docs/harness-contracts.md` and `docs/ticket-session-lifecycle.md`.

### Resuming a Session
If a ticket has a known `session_ref`, opening it should create/switch to a tmux window and run the harness-native resume command.

If no session ref exists and the ticket has never started, use the start flow.

If a ticket claims to have started but no session ref is known, show a repair/start-fresh prompt.

### Resume Failure
On resume failure:

- mark runtime status `error`
- set attention reason to a short resume error summary
- show options:
  - retry
  - edit session ref
  - start fresh
  - cancel

Do not automatically start a new session without confirmation.

### Send Prompt After Launch
`send prompt` should only be allowed on a never-started ticket in v1.

If a session already exists, pressing the send-prompt action should show something like: `Prompt already sent; open session instead?`

Ticket body edits after launch do not automatically sync to the agent. The body remains human-tracking metadata unless the user manually sends text in the agent window.

### Closing Sessions
Auto-close should mean:

1. gracefully exit the harness with an adapter-specific exit command/key sequence
2. wait for process exit
3. close/clean up the tmux window only after success or after graceful-exit timeout

Do not hard-kill first.

Column movement should not directly control session lifetime. Session lifetime is controlled by runtime detection and timeout policy.

If the agent is currently in a turn, keep it alive. If it is confirmed waiting/done and has timed out, gracefully close it and rely on harness-native resume.

## Runtime Detection

### Native Detection
There is no reliable generic native way across all three harnesses. Build a layered adapter strategy:

1. Use native machine-readable events if a harness supports them.
2. Otherwise use adapter-specific prompt/pattern detection.
3. Otherwise use conservative idle heuristics.
4. Allow temporary manual user override.

Possible adapter interface shape:

```go
type HarnessAdapter interface {
    Start(ticket Ticket) error
    Resume(sessionRef string) error
    DetectState(output []byte) RuntimeState
    SupportsNativeEvents() bool
}
```

### Conservative Policy
When detection is uncertain, do **not** auto-close.

- Confirmed waiting/permission states are attention states and eligible for auto-close after timeout.
- Quiet/unknown states are marked `idle_unknown` but not auto-closed.

### Runtime States
v1 runtime enum:

```text
not_started
starting
running
waiting_for_user
needs_permission
idle_unknown
closing
closed
exited
error
```

Meanings:

- `not_started`: no session launched yet
- `starting`: session process/window being created
- `running`: agent is actively in a turn or command is active
- `waiting_for_user`: agent appears ready for user input
- `needs_permission`: agent specifically asks for approval/permission
- `idle_unknown`: no output recently, but not confirmed safe to close
- `closing`: app is gracefully exiting harness/window
- `closed`: app closed the session safely; resumable
- `exited`: process ended on its own
- `error`: launch/detection/session problem

Attention states:

- `waiting_for_user`
- `needs_permission`
- `error`

Auto-close eligible:

- `waiting_for_user`
- `needs_permission`
- `exited` cleanup as appropriate

Not auto-close eligible:

- `running`
- `idle_unknown`

### Permissions vs Waiting
`needs_permission` should be visually distinct from normal `waiting_for_user` because permission prompts block progress.

- `waiting_for_user`: normal highlight
- `needs_permission`: stronger/different highlight

### Timeouts
Default timeouts:

```yaml
timeouts:
  idle_unknown_after_seconds: 120
  auto_close_waiting_after_minutes: 10
  graceful_exit_timeout_seconds: 15
  prompt_ready_timeout_seconds: 5
```

### Minimal Debug Metadata
Do not capture full transcripts in v1. Store enough to debug flashing/detection bugs:

- `last_output_at`
- `last_state_change_at`
- `last_detected_state`
- `last_attention_reason`
- `last_detection_source` (`native`, `pattern`, `idle`, `manual`)
- short `last_observed_excerpt`, capped around 1–2KB

### Watcher Lifecycle
No background daemon in v1. The watcher runs inside the board process.

If the TUI is closed, sessions can keep running in tmux, but no detection/highlighting happens until the board is reopened and reconciles state.

### Manual Override
Users should be able to temporarily mark runtime state when detection is wrong.

Command idea: `m` opens mark-state menu.

Options:

- running
- waiting_for_user
- idle_unknown
- error

Manual state uses `last_detection_source = manual`. A later confident adapter detection can overwrite it.

## TUI UX

### Board Display
Cards should be compact.

Example:

```text
T-001 Fix OAuth redirect
[pi] ● waiting 8m
```

Fields:

- display ID
- title
- harness
- active/closed/error window indicator
- runtime state
- time since state change or last output

Window/session indicator:

- `●` = tmux window active
- `○` = no active window, resumable
- `!` = error/missing session ref

No body preview by default.

### Flashing / Highlighting
When a ticket needs attention, it should flash or be highlighted in the TUI.

No external notifications in v1:

- no desktop notifications
- no terminal bell requirement
- no webhooks

### Attention Navigation
Support cycling through attention-needed tickets.

- `Tab`: jump to next attention ticket
- `Shift+Tab`: previous attention ticket, if feasible

Attention tickets are those in:

- `waiting_for_user`
- `needs_permission`
- `error`

### Ticket Creation
Use a two-step creation/edit flow.

- `n`: quick-create title-only ticket in current column using default harness `pi`
- `e`: edit details, including title/body/harness
- body/prompt editing should use `$EDITOR`

If `$EDITOR` is unset, default to `nano` or `vi`.

### Movement and Keybindings
Use Vim-style keys as primary and arrow keys as secondary.

Default key model:

- `h/j/k/l` or arrows: move focus
- `H/L`: move selected ticket between columns
- `J/K`: reorder ticket within column
- `Enter`: open/switch to ticket session, resuming if needed
- `n`: new ticket
- `e`: edit ticket
- `s`: start and send prompt, only if not started
- `o`: open only, only if not started or as appropriate
- `a`: archive selected ticket
- `m`: manually mark runtime state
- `Tab`: next attention ticket
- `q`: quit board

Launching/resuming should be allowed from any column. Do not require moving to In Progress. Optionally, later, prompt to move to In Progress if that column exists.

### Archiving
v1 supports archive, not delete.

Archiving behavior:

- If session is running, follow normal graceful-close rules when safe.
- Mark `archived_at`.
- Hide from active board.
- Keep title/body, harness, session ref/name, timestamps, and final runtime status.

No hard delete in v1.

## CLI Surface

Default command opens/attaches the board:

```bash
kanbi
```

v1 should also include basic subcommands:

```bash
kanbi doctor
kanbi add "title" --body "..." --harness pi
kanbi list
```

`doctor` should be lenient.

It should fail only for core requirements such as tmux/DB/config being unusable. Missing harnesses should produce warnings, not fatal failures.

Doctor checks:

- `tmux` installed and version visible
- can create/attach to `kanbi` session
- SQLite DB path writable
- config path writable
- shell detected
- terminal supports color/alternate screen reasonably
- whether currently inside tmux
- `pi` installed/version works, warn if missing
- `codex` installed/version works, warn if missing
- `gh` installed, warn if missing
- `gh copilot` usable, warn if missing

## Config and Storage

### XDG Paths
Use XDG conventions:

- Config: `~/.config/kanbi/config.yaml`
- DB: `~/.local/share/kanbi/kanbi.db`
- Logs/state: `~/.local/state/kanbi/`

Env overrides:

- `KANBI_CONFIG`
- `KANBI_DB`
- `KANBI_STATE_DIR`

### Config Format
Use YAML.

Example shape:

```yaml
default_harness: pi

tmux:
  session_name: kanbi
  board_window_name: board

timeouts:
  idle_unknown_after_seconds: 120
  auto_close_waiting_after_minutes: 10
  graceful_exit_timeout_seconds: 15
  prompt_ready_timeout_seconds: 5

harnesses:
  pi:
    command: pi
    start_args: []
    resume_args: ["--session", "{session_ref}"]
  codex:
    command: codex
    start_args: ["--no-alt-screen"]
    resume_args: ["resume", "--no-alt-screen", "{session_ref}"]
  copilot:
    command: gh
    start_args: ["copilot", "--"]
    prompt_args: ["-i", "{prompt}"]
    resume_args: ["copilot", "--", "--resume={session_ref}"]
```

Harness adapters are hardcoded in Go, but command paths, args, environment variables, and timeouts should be configurable where safe.

## Data Model Sketch

```sql
boards(
  id,
  name,
  created_at
);

columns(
  id,
  board_id,
  name,
  position,
  created_at,
  updated_at
);

tickets(
  id,
  board_id,
  column_id,
  display_id,
  title,
  body,
  preferred_harness,
  position,
  archived_at,
  created_at,
  updated_at
);

sessions(
  id,
  ticket_id,
  harness,
  session_ref,
  session_name,
  tmux_session_name,
  tmux_window_id,
  tmux_window_name,
  runtime_status,
  is_active,
  started_at,
  closed_at,
  last_seen_tmux_at,
  last_output_at,
  last_state_change_at,
  last_detected_state,
  last_attention_reason,
  last_detection_source,
  last_observed_excerpt,
  created_at,
  updated_at
);
```

## MVP Exclusions
Explicitly exclude from v1:

- list-level system prompts
- automatic column movement
- embedded terminal panes inside the TUI
- background daemon
- full transcript storage
- custom user-defined harness adapters
- historical only: multiple-board screens were deferred in the original plan; they are now implemented
- external/desktop notifications
- Obsidian/Markdown sync/export
- web UI
- remote agents
- multi-user collaboration
- hard delete of tickets

## Future State

- List-level system prompts, e.g. Review column injects review mode
- Automatic ticket movement based on agent state
- Pre-type prompt in harness without sending, if harness supports it
- Additional harness support beyond Pi/Codex/Copilot
- Fully user-defined harness adapters
- Further multi-board polish, such as canonical column types and archive/export semantics
- Optional Markdown/Obsidian export
- Embedded terminal pane mode
- Background watcher daemon
- Searchable session history or summaries
- Configurable prompt templates
- More advanced tmux integration/keybindings
- Optional alias such as `ak`

## Implementation Milestones

1. **Data model + config + CLI skeleton**
   - SQLite schema
   - YAML config
   - XDG paths
   - `kanbi doctor`
   - `kanbi add/list`

2. **Static Bubble Tea board**
   - Load board, columns, and tickets from DB
   - Navigate, move, reorder, archive
   - Edit title/body/harness
   - No tmux yet

3. **Tmux session/window manager**
   - Ensure dedicated tmux session
   - Create board window
   - Create/switch ticket windows
   - Reconcile windows on startup

4. **Harness adapters**
   - Pi/Codex/Copilot start/resume
   - session ref capture stubs/manual repair
   - send prompt/open only
   - prompt paste flow

5. **Runtime watcher**
   - Poll tmux pane output
   - adapter detection
   - state transitions
   - flashing/highlighting
   - minimal debug metadata

6. **Auto-close**
   - confirmed waiting timeout
   - graceful exit adapter commands
   - mark closed/resumable

7. **Hardening**
   - resume failure flow
   - doctor improvements
   - tmux reconciliation polish
   - manual runtime override
   - keybinding polish

## Grill-Me Decision Log
This section preserves the resolved 65-question design context.

1. **Language/stack:** Go.
2. **Go TUI framework:** Bubble Tea.
3. **Persistence:** SQLite canonical state store.
4. **Board count:** historical decision allowed only one visible board in v1 while reserving schema room for multiple boards later; current implementation is multi-board with a Master aggregate view.
5. **Ticket fields:** title + body, not single text only.
6. **Workflow vs runtime:** separate workflow column and runtime status.
7. **Session hosting:** tmux backend for v1.
8. **Tmux mapping:** one dedicated tmux session with one window per active ticket.
9. **Column moves vs session lifecycle:** movement does not directly kill sessions; idle/turn detection controls closing.
10. **Turn/wait detection:** layered native events, prompt-pattern detection, then conservative idle heuristic.
11. **Uncertain detection:** conservative; do not auto-close `idle_unknown`.
12. **Close semantics:** graceful exit harness, then close tmux window.
13. **Session ref capture:** adapter-specific, prefer explicit naming/ref, fallback to output parsing/manual repair.
14. **Ticket identifier:** sequential display IDs like `T-001` plus internal DB ID.
15. **Title rename behavior:** rename tmux window only; do not mutate harness session ref.
16. **Default columns:** Open / In Progress / Review / Done.
17. **Launch from columns:** allow start/resume from any column.
18. **Multi-session UI:** board is a tmux control center, not embedded terminal UI.
19. **Return to board:** use normal tmux navigation; stable `board` window; no custom tmux binding mutation.
20. **Tmux session ownership:** dedicated `kanbi` tmux session by default.
21. **Outside tmux behavior:** auto-create/attach to dedicated tmux session.
22. **CLI surface:** default interactive command plus subcommands such as doctor/add/list.
23. **Doctor strictness:** lenient; warn for missing harnesses, fail core tmux/DB/config problems.
24. **Config/storage paths:** XDG locations.
25. **Config format:** YAML.
26. **Harness configurability:** hardcoded adapter logic plus configurable command paths/basic args/timeouts.
27. **Default harness:** global default with per-ticket override; default is `pi`.
28. **Ticket creation UX:** two-step quick-create plus edit details.
29. **Body editor:** use `$EDITOR` for long body/prompt editing.
30. **First prompt content:** Markdown title/body.
31. **Prompt heading format:** `# T-001: Title` followed by body.
32. **Movement/keybindings:** Vim-style keys primary, arrows secondary.
33. **Column editing:** add/rename/reorder/delete-empty columns in TUI.
34. **Column deletion:** only delete empty columns.
35. **Ticket removal:** archive only for v1, no delete.
36. **Archive metadata:** keep session metadata and final status; hide from active board.
37. **Logs/transcripts:** no full transcripts; minimal debug metadata enough to fix detection.
38. **Watching when TUI closed:** no daemon; watcher runs inside board process.
39. **Startup reconciliation:** DB canonical with tmux inspection/reconciliation.
40. **Manual runtime override:** allow temporary manual state marking.
41. **Attention navigation:** support cycling through attention tickets.
42. **Runtime enum:** `not_started`, `starting`, `running`, `waiting_for_user`, `needs_permission`, `idle_unknown`, `closing`, `closed`, `exited`, `error`.
43. **Default timeouts:** idle unknown 120s, auto-close waiting 10m, graceful exit 15s, prompt-ready 5s.
44. **Permissions:** distinct `needs_permission` state and stronger visual treatment.
45. **Card display:** compact ID/title/harness/status/time display.
46. **Window indicator:** show active/resumable/error indicator such as `●`, `○`, `!`.
47. **Opening closed ticket:** resume automatically if session ref exists; otherwise start/repair flow.
48. **Resume failure:** mark error and offer retry/edit ref/start fresh/cancel.
49. **Starting fresh after old session:** preserve old sessions in a sessions table.
50. **Harness field:** both ticket preferred harness and session actual harness.
51. **Open-only for never-started ticket:** start harness with no initial prompt.
52. **Send prompt after start:** not allowed in v1; first-launch only.
53. **Body edits after launch:** no automatic effect on agent session.
54. **MVP exclusions:** exclude deferred features listed above.
55. **Harness commands:** historical commands were later corrected. Current commands are Pi `pi --session <ref>`, Codex `codex resume --no-alt-screen <ref>`, and Copilot `gh copilot -- --resume=<ref>`.
56. **Copilot resume ref:** accepts explicit session ref.
57. **Initial prompt injection:** use tmux paste-buffer.
58. **Prompt-ready failure:** wait timeout, then modal: paste now/open without sending/cancel.
59. **Submit method:** paste text then send normal Enter.
60. **Multiline paste risk:** accepted MVP risk; test per harness later.
61. **Milestones:** data/config/CLI → static board → tmux → adapters → watcher → auto-close → hardening.
62. **Project/binary name:** `kanbi`.
63. **Repo location:** standalone Go repo under `~/Developer`.
64. **License/distribution:** private personal tool initially.
65. **Documentation update:** update this note with all decisions and do not lose context.
