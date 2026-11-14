# Kanbi in the Herdr app

Product proposal · GH-404 · 14 November 2026

## Recommendation

Offer Kanbi as a native board inside Herdr app, backed by Kanbi’s existing application services and SQLite state, with agent terminals running in herdr-core. Keep the Kanbi TUI and CLI available. The desktop experience should make a ticket, its live agent and its working context visible together.

The proposed destination is a Kanbi mod and personal Herdr layout. Running the existing TUI in an app terminal remains a useful alternative, but a native panel gives us greater freedom to keep tickets readable beside interactive agents. This proposal describes product behavior, not an implementation commitment or a change to current runtime support. It contains no build-time or effort estimates.

## What the user gains

Kanbi already has a board, inspector and live Fleet panes. The app’s additional value is a flexible arrangement of those experiences: keep the board on screen while answering an agent; expand a ticket’s context without replacing the agent; review its changes beside the conversation; and choose a layout that suits the work. The gain is continuity of context and easier spatial navigation. It is not a new claim that only the app can keep agents alive or reach remote machines.

| Situation | Current Kanbi in Herdr TUI | Proposed app experience | Value added |
| --- | --- | --- | --- |
| An agent needs an answer | Open its ticket to focus the terminal; return to the board afterward | Select the card and interact in the adjacent live terminal | The board stays visible while answering |
| Resume a paused task | Read the checkpoint in the inspector and open/resume the session | Read the next action beside the selected ticket and conversation | Less navigation between handoff and execution |
| Supervise several agents | Enter Fleet to gather live panes and return to restore them | Open a Fleet layout with board navigation retained | More freedom to arrange and resize supervision views |
| Review a worktree | Inspect worktree health and use terminal/Git tools | Show ticket-owned changes and diff beside the live agent | Easier comparison between requested work and changed code |
| Move between projects | Use the board picker or Master | Keep Master and board switching in the Kanbi panel | Ticket ownership remains visible across project navigation |
| Work over SSH | Use the existing terminal experience | Continue using the TUI or attach to a core terminal | Desktop adoption does not remove terminal access |

## Proposed screens

The visual document includes three illustrative screens using the same sample ticket and coherent state. These are proposed Kanbi layouts, not screenshots of a shipped Herdr integration. Sample terminal output and diffs are illustrative. The native design deliberately departs from terminal cell geometry; it retains the board hierarchy, separate runtime row, title, metadata and attention rail.

### Board and selected agent

The default desktop view has a narrow Herdr navigation area, the Kanbi board in the center and a live terminal on the right. Four workflow columns remain in their configured order. Horizontal overflow stays inside the board; terminal width does not squeeze every column into unreadable cards.

Selecting a card updates its read-only context and identifies the associated attempt. Open agent focuses that attempt’s live terminal. A selected ticket that has never started shows Start agent; an inactive attempt with a verified ref shows Resume; an unavailable or unverified attempt offers repair. Selection alone never starts an agent or submits a prompt.

The terminal is an interactive core-backed terminal, not the inspector’s captured output. Clicking it moves keyboard focus to the harness, visibly marked in the header. Board shortcuts run only while the board has focus. Escape follows the focused surface’s behavior; a dedicated Return to board command always returns focus without killing the agent. Starting fresh remains an explicit, confirmed action.

### Ticket context and review

A review layout keeps the selected ticket’s title, body, notes, latest checkpoint and session history in one panel. A second panel shows the exact ticket worktree’s files and diff; the agent conversation remains accessible beside it. Board ownership and the checkout path are always visible because a board can have several ticket worktrees.

The review panel must resolve the ticket’s recorded worktree rather than infer a directory from Herdr’s selected space. Herdr’s shipped Changes panel is useful inspiration, but exact ticket binding and Kanbi’s merge action are proposed integration behavior. Merge retains Kanbi’s existing preflight, confirmation, session close and history rules. A diff is evidence for review, not proof that a ticket is complete.

### Fleet with the board retained

Fleet shows a compact board rail and a grid of existing live attempts. Each terminal has its ticket ID, board, harness and observed state. Selecting a tile focuses it; zoom expands it temporarily. Returning to the board restores the prior arrangement and selection.

This is a proposed desktop counterpart to current Fleet, not a claim of command parity. It must preserve terminal/session identity and journal any structural changes. Layout changes never launch replacement agents. Membership is explicit for the visit; a new agent does not silently rearrange a terminal the user is typing into. Only one controlling view should own a terminal’s size at a time; other previews observe without resizing it.

## A normal working session

1. Open Herdr and choose the Kanbi layout. Master shows tickets across boards, with owning board labels and the existing workflow filters.
2. Select a ticket waiting for permission. Read its task and checkpoint, then use Open agent. The existing validated attempt appears beside the board; no duplicate session is created.
3. Click into the terminal and answer the harness. Permission approval stays in the harness’s real interface. Kanbi updates the card when it receives a trustworthy observation.
4. Move to Review. Open the ticket’s worktree diff, inspect the conversation and run the normal merge flow when ready. Workflow placement and runtime state remain separate.
5. Close the desktop window. Agents remain owned by core. Reopening reconstructs the view; a lost program that recovered as a shell is presented as an ended or repair-needed attempt, not as a resumed conversation.

## Product boundaries

Kanbi owns tickets, columns, Master filters, provider sync, attempts, harness resume refs, worktrees, checkpoints, messaging and Operators. Herdr app owns the desktop window, graphical layout, terminal presentation and focus. Core owns terminal processes, addresses, input/output and program-reported status. Kanbi’s SQLite database remains the durable record for local workflow and attempt history; provider-backed ticket metadata retains its existing source of truth.

A board can be associated with a Herdr space for convenience, but the mapping is presentation metadata. Moving a pane never changes a ticket’s board or launch directory. Master can show several boards while one agent is focused. Operators remain workspace-wide and do not become tickets merely because they appear in a sidebar.

The mod should invoke Kanbi application actions through a defined command/API boundary. It must not write SQLite directly or independently implement launch, merge, pause or resume rules. Existing CLI JSON surfaces can cover supported actions; additional structured surfaces may be needed. The desktop window is not the scheduler owner: closing or opening it must not implicitly create a second scheduler. The exact ownership model must be decided before promising closed-window Operator scheduling; this proposal adds no background daemon.

## Runtime and interface choices

Use herdr-core’s structured API as the new runtime boundary. Keep desktop commands in a separate presentation adapter so core-backed sessions can still be used from a terminal. Store backend and endpoint ownership on every attempt, and route existing attempts through those stored references. The current Herdr TUI backend remains independently supported; old live panes are not automatically shared with core or migrated into the app.

Core documents a preview generation-1 protocol with an additive compatibility policy and released-schema checks. That is a sound basis for a versioned adapter, but behavior still needs validation. In particular, core status requires program reporting; an absent report must not become a healthy green state. Its current terminal schema has no equivalent of the older Herdr agent_session resume object. Harness-specific ref capture remains necessary.

The app supports custom layouts, JavaScript mods, live terminal blocks and commands for focus and terminal adoption. Mods describe native GPUI elements; this HTML proposal illustrates the intended result and is not code that can be installed as a mod. Runtime support, exact worktree binding and the native Kanbi board are proposed additions.

## States that must stay understandable

| Event | What the user sees | Required behavior |
| --- | --- | --- |
| Existing valid attempt opened twice | The same agent terminal | One active attempt, no duplicate process |
| Core or remote connection unavailable | Last known state with an unavailable or stale label | No guessed closure, launch or automatic resubmission |
| Native status not reported | Unknown state or a clearly identified fallback observation | No fabricated native confidence |
| Agent lost and kept terminal becomes a shell | Ended attempt with Resume or Repair as appropriate | Address existence cannot validate the original agent |
| No verified harness ref | Repair or Start fresh | Never advertise safe resume from an address alone |
| App window closed | Agents continue; layout can be restored | No parent-child teardown of independent ticket agents |
| Ticket moved into Done | Workflow changes independently of observed process state | Do not invent an implicit kill or completion rule |
| Old-runtime ticket selected in the app | Clear existing-backend action | Do not reinterpret legacy pane IDs as core addresses |

## Presentation and accessibility

Use text labels with attention colors and a visible focus indicator. Permission and input requests remain distinguishable from errors. Keep card navigation, search, board switching and inspector actions available from the keyboard, while allowing pointer selection and resizable panels. Put a ticket’s board and harness near its title; keep long descriptions in context rather than expanding every card.

At a wide desktop size, show board and agent together. At smaller widths, switch between Board, Agent and Context rather than compressing them all. The 80 by 24 terminal experience retains the current Kanbi TUI’s compact navigation. A native mod is a desktop alternative; it is not expected to reproduce pixel-based splits inside an 80-column terminal.

## Scope and decision

Recommend the native board plus live-agent layout as the product destination, with the existing TUI as a supported alternative. Include ticket context, explicit agent focus, worktree review and a Fleet layout. Preserve the current workflow and session model. Defer remote ticket execution promises until worktree paths, provider access and harness refs have been validated on the execution machine.

Accept the experience when a user can answer a blocked agent without losing board context, review the correct worktree beside its ticket, return from Fleet without changing attempts, and switch between graphical and terminal access without duplicate processes. Verify the same lifecycle outcomes for Pi, Codex, Claude and Copilot; verify focus, reconnect, stale observations and shell recovery. These are acceptance conditions, not delivery estimates.

The decision requested is whether this combined board-and-terminal workspace is a desirable product direction. Adoption as the default runtime remains a separate decision after compatibility evidence. The desktop app’s feature cadence should not alone determine core adoption, and core adoption should not require a desktop-only Kanbi.

## Evidence and references

Upstream capabilities were checked on 14 November 2026. Default-branch documentation can include unreleased behavior; implementation should select and verify a released core/app combination.

- [Herdr app overview](https://github.com/herdrdev/herdr-app/blob/master/README.md) — desktop model, platform requirements and terminal lifetime.
- [App layout and mod guide](https://github.com/herdrdev/herdr-app/blob/master/defaults/AGENTS.md) — native mods, terminal blocks, labels, focus and shared layout behavior.
- [App commands](https://github.com/herdrdev/herdr-app/blob/master/docs/commands.md) — presentation command surfaces.
- [Core API](https://github.com/herdrdev/herdr-core/blob/master/schema/README.md) — protocol compatibility, events, terminal lifetime and viewer ownership.
- [Core schema](https://github.com/herdrdev/herdr-core/blob/master/schema/hc.openrpc.json) — terminal and status fields.
- [Core templates](https://github.com/herdrdev/herdr-core/blob/master/docs/templates.md) — reported status and readiness semantics.
- [GH-404 investigation](../../herdr-app-investigation-gh404.md) — current Kanbi integration and compatibility gaps.

The visual baseline was captured from the repository’s disposable Herdr fixture at 160 by 45 cells. It shows Master, four workflow columns, filled cards, focus groups and the keyboard footer. Proposed desktop screens deliberately simplify the fixture content and use a native board; they do not claim pixel fidelity to either shipped application.
