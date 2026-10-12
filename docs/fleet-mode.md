# Fleet Mode proposal (GH-372)

Status: specification for review, following GH-370. Fleet Mode is not implemented;
the controls and thresholds below are proposed defaults. This document does not
change the current lifecycle or multiplexer contracts.

## User flow

Press `v` from the normal board to gather its active agents into the current Kanbi
tab. From Master, gather active agents across unarchived boards on the same Herdr
server. Master filters and title search do not redefine Fleet scope: the entry
summary explicitly says **all boards**, including the number hidden by filters.
Named-board scope includes all its active ticket sessions, regardless of column.
Fleet and Global Focus Mode are independent; gathering does not admit, pause, or
change any commitment.

Use `v` because `f` already opens Master filters and `F` opens Focus settings.
Entry is available with board or inspector focus, but not while a modal, editor,
or search field owns text input. Preserve board identity, filters, search state
if retained outside editing, selected ticket/column IDs, viewport anchors, and
inspector visibility/section/scroll/focus. Returning restores that view against
the latest projection, using existing selection fallback if a ticket vanished.

Before any movement, show an entry summary with scope, eligible count, excluded
starting/closing/unavailable sessions, and the proposed arrangement. `Enter`
gathers; `Esc` cancels without moving anything. No eligible panes is an ordinary
empty-state message. A server mismatch, unsupported API, unsafe destination, or
unresolved prior restoration blocks entry with a specific reason.

Eligibility requires the latest active Herdr attempt and a validated live pane
on the destination server. Waiting, permission, and idle states remain eligible.
Never launch or resume a ticket merely to include it. A transient validation
failure blocks gathering until retried rather than silently omitting a possibly
live agent. Known absent panes follow ordinary lifecycle reconciliation first.

## Header, focus, and return

Keep Kanbi as the top pane, requesting one content row plus unavoidable Herdr
decorations. Its row contains `FLEET`, scope, live/total and attention counts, and
an always-visible **[Return to board · v]** control. Drop optional counts before
truncating scope; preserve the return control at 80 columns. Stale observation
is labeled rather than counted as agent failure. The standard board, inspector,
custom status modules, and shortcut footer are hidden while this header renders.

Entry finishes with the header focused and Return selected. `v`, `Enter`, or
`Esc` in the header begins restoration. A left click on Return does the same;
clicking elsewhere in the header only focuses it. Enable terminal mouse reporting
only for this view and restore the previous setting on return. Implement a real
hit region for Return; its appearance alone is insufficient.

Use Herdr's configured native focus controls or mouse selection to reach agent
panes and return to the top header. Do not install a global Fleet binding or
intercept harness keystrokes. While an agent pane is focused, `v`, `Esc`, `q`,
`Ctrl+C`, and all other terminal input belong to that harness. Header help must
explain this focus distinction. Verify a keyboard-only route back to the header
with the installed Herdr bindings before enabling entry; do not assume a prefix
or replace the user's configuration.

Board navigation, edits, launch/close controls, filters, and scope switching are
unavailable in the header. Return first, then change board or start another agent.
`q` in the header restores panes and then exits Kanbi. `Ctrl+C`/SIGTERM requests
bounded best-effort restoration during shutdown; it never closes ticket panes.
If restoration fails, retain the recovery journal and print its recovery location.

## Equal panes and small terminals

Allocate the space below the header to these arrangements. Number slots in
reading order; initially order by board ID, ticket ID, then session ID. Pane
area should differ by no more than decoration/cell rounding requires.

| Agents | Arrangement below header |
| --- | --- |
| 1 | One full-width pane |
| 2 | Two equal columns |
| 3 | Three equal columns |
| 4 | Two columns, two equal rows |
| 5 | Three panes in the top row, two in the bottom; row heights 3:2 |
| 6 | Three columns, two equal rows |

For five agents, weighting row heights by occupancy gives approximately equal
area despite different pane widths. Use BSP splits and ratio updates that retain
live terminals. At entry, if three columns would fall below 36 content columns,
choose a two-column arrangement: three agents use a two-pane row over one
full-width pane, with row heights 2:1; five and six cannot meet the small-screen
minimum below. Count decorations using actual PTY sizes, not just outer rectangles.

Proposed entry floor: every agent has at least 36 content columns and eight
content rows. At 80×24, target two columns and up to four agents; verify four
actually fit with the installed client's decorations. If the floor cannot be
met, retain the board and explain that a larger terminal is required. Do not
gather only a subset without saying so. More than six eligible agents also blocks
entry in the first version, with the total and a named-board scope suggestion.

Resizing keeps slot order and the chosen split topology, recalculating equal-area
ratios and the header height. It does not move agents between slots. If a Fleet
entered at a larger size is resized to 80×24 below the floor, keep its terminals
alive and add a compact **Small panes: use Herdr zoom** hint. This fallback supports
individual reading/input, not a claim that six full harness screens are readable
at 80×24. Below the header's own minimum, retain the keyboard return path even
if text must clip.

Temporarily enlarge an agent with native Herdr pane zoom. Unzoom restores the same
slots and ratios. Zoom can hide the header: the configured native unzoom control
must remain reachable before the user can return to Kanbi. Do not add an ordinary
harness key for enlargement. Entry refuses an already-zoomed or multi-pane Kanbi
tab in the first version rather than disturbing unrelated panes or custom layout.

## Membership and stable positions

Membership is a snapshot of session attempts at confirmed entry. New agents
started elsewhere remain in their original tabs; the header shows a count of
new eligible attempts and says to return/re-enter to gather them. An old member
is never silently replaced by a newer attempt for the same ticket.

State changes update counts and labels, never sort, move, or resize slots. If an
agent exits but its pane survives, leave it where it is. If Herdr removes its
pane, preserve that slot with a Kanbi-owned passive placeholder labeled with the
ticket and exit state. The placeholder runs no harness and owns no session;
remove it on exit. This replacement is permitted only for an authoritatively
absent pane, never on a read timeout. If all agents exit, keep Return available
rather than unexpectedly switching views.

Placeholder placement without shuffling surviving panes is a live verification
gate. If Herdr cannot support it reliably, revisit this design before shipping;
do not weaken the stable-position requirement silently.

## Restoration and interruption

Persist a SQLite restoration journal before the first move. It identifies the
server endpoint/identity, Fleet owner, destination workspace/tab/header pane,
view snapshot, and each exact session attempt and validated pane identity. Each
member records original workspace/tab IDs, labels, cwd, original pane location,
current authoritative IDs, and move/restore progress. Source tabs must contain
only the ticket pane for the first version; reject shared source tabs rather
than promising restoration of arbitrary layouts. Preserve source workspaces;
if a move removes an empty workspace, recovery must recreate its recorded
identity metadata and track the returned IDs.

Serialize Fleet ownership across Kanbi processes sharing the database/server.
Coordinate moves with lifecycle operations, polling, inspector reads, ref capture,
and rename/focus/close commands from every Kanbi instance. A per-process pause
cannot protect a second TUI or CLI. Record a per-member move intent before the
Herdr request, then persist the returned workspace/pane/agent references on the
same existing attempt and journal atomically. Invalidate observations using old
references. Preserve launch cwd, harness ref, session history, and active-slot
ownership; moving is never a new attempt or a close/start cycle.

Database transactions cannot make Herdr moves atomic. If a request times out or
Kanbi dies between a move and persistence, reconcile against the server using
verified stable occupant identity or tested correlation metadata. A recycled
public pane ID, label, or cwd alone cannot prove ownership. How to carry and
verify that identity across moves is a blocking spike, not an assumed API guarantee.
Observers must consult pending journal entries before treating old IDs as absent.

Return disables further gathering, clears Fleet-owned zoom, and restores members
in a deterministic order. Reuse each original tab when it still exists and is
safe; otherwise move the existing pane into a newly created tab with the recorded
label/cwd in its source workspace. Track all returned IDs. Original emptied tab
IDs and exact tab order need not survive; ticket processes and logical destinations
must. Never move a pane into a tab now occupied by unrelated work.

Revalidate each exact attempt and occupant before restoring. Truly absent panes
use ordinary exited/repair projection and require no replacement harness or empty
source tab. A pane moved externally requires identity reconciliation; if its
location/ownership cannot be proved, stop that member's restoration with a
diagnostic rather than moving an uncertain occupant. Do not touch a newer attempt.

An entry failure rolls back only completed, verified moves. An exit failure keeps
the header in recovery mode with **Retry restoration**, outstanding count, and
the specific error. Successfully restored agents stay restored. Journal completion
requires authoritative destination checks, reference persistence, placeholder
cleanup, and restoration of Kanbi's full-height board pane. Only then release
ownership and restore the saved board view.

On startup, inspect incomplete journals before normal runtime reconciliation.
Offer **Restore Fleet panes** for an interrupted owner; prevent new Fleet entry
until resolved. Never seize ownership from a proven live owner. Server unavailable
or identity ambiguous means degraded recovery with a retry path and preserved
journal, not guessed IDs, inactive sessions, or automatic harness relaunches.
A killed Kanbi process can leave gathered agents running until recovery; no new
daemon is introduced. Whole-server loss follows normal verified runtime absence
rules; restoration must not manufacture continuity after a terminal was destroyed.

## Implementation and verification gates

Keep orchestration in `internal/runtime`, durable journal/ownership and conditional
reference updates in `internal/storage`, live move/layout capabilities in the
Herdr adapter, and header/input/view restoration in `internal/tui`. Expose an app
use case rather than issuing shell commands from the TUI. Finalize backup/export
and migration treatment for journals before adding schema; machine-local recovery
records must not cause imported databases to move another machine's panes.

Required pre-implementation spikes in an owned isolated server:

1. Same-workspace and cross-workspace move/return: record returned IDs, empty-tab
   and workspace behavior, process PID/PTY continuity, native agent identity,
   and harness-ref capture while an attempt is moved.
2. Header height, mouse activation, keyboard-only focus return, native zoom/unzoom,
   and live equal-area splits for 3–6 panes at 160×45 and 80×24, including entry
   refusal and resize fallback. Check actual agent PTYs and stable slots on exit.
3. Inject failure before/after each move and reference write, including ambiguous
   timeouts, SIGKILL, server restart, an unrelated replacement occupant, a concurrent
   CLI close/start, and a second Kanbi process. Repeated recovery must be idempotent.

Implementation acceptance requires unchanged terminal processes, no prompt replay,
no session-row duplication/history loss, and safe restoration after every tested
partial operation. Run the repository fmt/test/vet/smoke checks, focused
harness/runtime/storage tests, and the project real-UI validation skill. Real
authenticated harness checks are opt-in after deterministic checks pass.

## Evidence for this specification

On 2026-10-11, local Herdr 0.9.3 CLI help exposed `pane move` with destination tab,
split direction/ratio, target pane, workspace, and new-tab options, and native
`pane zoom`. The installed socket schema includes move and split-ratio methods.
The [Herdr socket API documentation](https://herdr.dev/docs/socket-api/) describes
live pane control and distinguishes portable layout export/apply from live pane
operations. `layout.apply` must not be used to gather or restore Fleet terminals.

The disposable UI fixture was exercised on Master at 160×45 and 80×24; both
retained the current compact header and visible shortcut footer. At 80×24 the
board shows two columns and horizontal-overflow information, supporting a compact
Fleet header with explicit fallback rather than squeezing six harnesses into
claimed readable screens. This was baseline validation only: no Fleet header,
mouse path, equal-pane arrangement, or live move/return round trip has been tested.
