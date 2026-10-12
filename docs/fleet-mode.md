# Fleet Mode (GH-372)

Fleet is opt-in live-pane supervision, following GH-370. It moves existing agent
terminals into the Kanbi tab and returns them to their source tabs; it never
launches, resumes, replays a prompt, or creates an agent session attempt.

## Enter and return

Press `v` from the board or inspector to preview Fleet. `f` remains Master filters
and `F` remains Global Focus settings. Editors, search fields, and other modals
retain their own input. The preview shows scope, eligible/excluded counts, layout,
and the configured Herdr cycle-focus and zoom bindings. `Enter` gathers;
`Esc` cancels before any pane moves.

A named board gathers its active sessions. Master gathers active sessions across
unarchived boards on the same configured Herdr server, regardless of Master
filters. Starting, closing, unsupported-runtime, and known other-server attempts
are excluded explicitly. Waiting, permission, and idle attempts remain eligible.
A validation failure blocks entry rather than silently omitting a potentially
live agent. Fleet does not change Focus commitments or pause checkpoints.

The Kanbi pane becomes the top header with scope, live/total and attention counts,
plus a prominent **Return to board** control. Entry finishes with that header
focused. `v`, `Enter`, or `Esc` there returns to the board; clicking the control
also returns. `q` or `Ctrl+C` there restores panes and then quits Kanbi.

Use native Herdr cycle-focus controls or the mouse to reach agents and return to
the header. All input in a focused agent pane goes directly to its harness,
including `v`, `Esc`, `q`, and `Ctrl+C`. Kanbi does not install a global binding or
rewrite Herdr configuration. Native zoom/unzoom temporarily enlarges an agent;
unzoom before returning to the header. The preview reads the local Herdr key
configuration and refuses entry if required cycle-focus/zoom controls are disabled.

Board editing, launching, closing, filters, and scope switching are unavailable
in the Fleet header. Return first. The saved board, filters, selected ticket,
viewport anchors, and inspector section/focus/scroll are restored against current
data; ordinary selection fallback applies if the selected ticket disappeared.
The view snapshot is also stored in the recovery journal.

## Layout and membership

Slot order is fixed at entry by board ID, ticket ID, then session ID.

| Agents | Layout below the header |
| --- | --- |
| 1 | Full width |
| 2 | Equal columns |
| 3 | Three equal columns; a two-over-one alternative on narrower screens |
| 4 | Two columns, two equal rows |
| 5 | Three panes above two, with row heights weighted 3:2 |
| 6 | Three columns, two equal rows |

Five agents have approximately equal areas despite differing widths. Rounding
and Herdr decorations can account for small area differences. All splits and
ratio updates operate on live panes. `layout.apply` is never used.

Herdr 0.9.3 clamps split ratios to at least 10%, so the header cannot always be
one content row. It uses the thinnest supported split: five outer rows at 160×45
and four at 80×24, retaining an interactive content row beneath decorations. Each agent also has a thin, labeled, Kanbi-owned inert slot
pane above it. This keeps the slot's BSP position intact even when Herdr removes
an exited agent pane. Source-tab anchors similarly prevent empty source tabs and
workspaces from disappearing during gathering. Anchors run no harness, own no
session row, and are removed on successful return.

Entry supports at most six agents and conservatively requires at least 36 content
columns and eight content rows per agent, accounting for slot/header overhead.
At 80×24, one or two agents fit this floor; larger fleets require a larger
terminal. Entry refuses the entire gathering rather than silently taking a
subset. A pre-existing zoomed or shared Kanbi tab, or a zoomed/shared source tab,
also blocks entry.

Resize retains membership, slot order, and split topology. If an existing fleet
is resized below the floor, terminals remain running and the header says **small
panes: Herdr zoom**. Return stays visible at 80 columns. This fallback enables
individual reading/input; it does not claim six complete harness screens are
readable at 80×24.

Membership is a snapshot of exact session attempts. New agents started elsewhere
stay in their original tabs; the header counts new active attempts and advises
re-entry. State changes update counts without sorting or moving panes. An old
member is never replaced by a new attempt for that ticket. Agent-pane disappearance
leaves its labeled slot in place; all surviving agent rectangles stay stable.
All agents exiting leaves Return available rather than automatically changing views.

## Durable moves and recovery

SQLite stores one machine-local `fleet_journal` before rearrangement. It contains
machine/server identity, original and current pane references, stable terminal
IDs, exact ticket/session IDs, source workspace labels, owned anchors, split
ratios, per-member restoration progress, and the saved TUI view.

Herdr public pane IDs can change across workspace moves. Terminal IDs survive
live moves and are used to find the same terminal after a timeout or interruption.
Anchor creation records an intent and a nonce supplied atomically in the new
shell environment. If a split reply was lost, read-only local process inspection
verifies that nonce before adopting or cleaning up the unrecorded anchor; an
uninspectable process leaves recovery pending. Environment contents are never
shown in diagnostics.
Updated pane/workspace/tab references and journal progress commit together on
that existing session row. Native named-agent targets, harness refs, immutable
launch cwd, active-slot ownership, and prior session history are preserved.
New launches record terminal identity and the observed server socket; reads,
input, focus, close, and detection reject a recycled public ID when its stored
terminal identity differs.

A file lock beside the canonical database serializes Kanbi runtime operations
across processes. A separate owner lock remains held for the Fleet visit and is
released by process exit. Lifecycle, polling, preview, rename, and message commands
use the runtime lock. CLI children inherit that lock until their request finishes,
so a killed parent cannot expose an in-flight mutation to a second Kanbi process.
Pending entry/restoration journals block ordinary runtime
operations until recovery; active visits allow commands against current refs.
Ref capture retains its exact session ID and launch-directory contract.

Return locates each original terminal by identity. Source anchors normally retain
its original tab/workspace. If an anchor was removed externally, return recreates
a source workspace/tab as necessary and persists the returned IDs. A live already
restored member proves the identity of a recreated workspace before it is reused.
A move completed before reference persistence is reconciled from terminal identity;
repeated restoration is idempotent. Agents that genuinely disappeared follow the
ordinary exited/repair rules, and newer attempts remain untouched.

Externally moved agents or source anchors with ambiguous destinations produce an
explicit recovery error rather than moving an uncertain pane. Successfully restored
members stay restored. The header offers **Retry return** while outstanding work
remains. Cleanup closes only the recorded owned terminal identities and never a
replacement occupying a recycled public pane ID.

Startup offers **Restore Fleet panes** for an incomplete journal. Another live
Fleet owner cannot be displaced. An unavailable server, wrong machine/socket, or
unprovable identity leaves the journal available for retry. Whole-server loss
uses normal verified absence rules and never fabricates resumed terminal processes.

Normal shutdown makes a bounded 15-second best-effort restoration attempt before
storage closes. Forced termination can leave gathered agents running until startup
recovery. No background daemon is added. Return also restores Kanbi's full-height
pane; a killed header is not relaunched during recovery.

Whole-database backup and board deletion are blocked while a Fleet journal exists;
return/recover first. This prevents portable backups from carrying actionable
machine-local recovery records and protects member history during restoration.
Board packages retain their existing active-session checks and do not export the
Fleet journal. Migration 10 adds the journal without rewriting session history.

## Implementation and verification

Runtime orchestration is in `internal/runtime/fleet.go`; the app/TUI seam is in
`internal/app/fleet.go` and `internal/tui/fleet.go`. Storage owns journal/reference
transactions and the shared runtime lock. The Herdr adapter provides live moves,
identity checks, inert anchors, native focus/zoom, and split-ratio RPCs.

Deterministic tests cover equal-layout gathering/return for 3–6 members, unchanged
attempts/native targets/refs, move failures before and after side effects, interrupted
reference persistence, recreated sources, ambiguous return retries, recycled IDs,
newer attempts, cross-manager locks, minimum-size refusals, migration idempotency,
atomic journal writes, and header focus/selection/mouse/retry behavior.

Run the standard fmt/test/vet/smoke checks and focused harness/runtime/storage tests.
The project UI-validation skill's isolated fixture is extended by:

```bash
python3 scripts/fleet-ui.py
```

This uses interactive Python fake agents only, a private database, and an owned
Herdr server/client. It checks live equal-area layouts for 3–6 agents at 160×45,
direct input, native cycle-focus and zoom/unzoom, mouse Return, 80×24 resize/refusal,
source-tab/session/terminal continuity, stable positions after pane disappearance,
and startup recovery after SIGKILL, including an unacknowledged anchor split. It always stops its owned fixture.

Authenticated harness checks remain opt-in after deterministic verification.
Terminal-continuity evidence comes from fake interactive processes on the installed
Herdr 0.9.3 (protocol 22), not an authenticated model run. Socket contracts:
[Herdr socket API](https://herdr.dev/docs/socket-api/).
