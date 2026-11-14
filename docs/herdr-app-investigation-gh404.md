# GH-404: Herdr app and core investigation

Investigated 2026-11-14. This is a source/documentation assessment, not a
compatibility certification or a change to Kanbi's supported runtime.

## Recommendation

Keep the existing Herdr TUI integration supported. Investigate herdr-core as a
second, explicitly selected runtime, retaining Kanbi's Go TUI/CLI first. Make
desktop presentation optional. Consider a native Kanbi mod only after the core
lifecycle works reliably. Do not replace the board with a desktop-only UI now.

The important split is runtime versus presentation: adopting the new runtime
does not require rewriting Kanbi's interface, and displaying Kanbi inside the
desktop app does not automatically migrate its runtime integration.

## What was released

| Component | Responsibility | Relationship to Kanbi |
| --- | --- | --- |
| [herdr](https://github.com/herdrdev/herdr) | Existing terminal client, server, agent detection and workspace/pane APIs | Our current integration; local executable reports 0.9.3 |
| [herdr-core / hc](https://github.com/herdrdev/herdr-core/blob/master/README.md) | Headless persistent terminals, process ownership, status, input, events, remote machines | Candidate new runtime adapter |
| [herdr-app](https://github.com/herdrdev/herdr-app/blob/master/README.md) | Native desktop window, spaces/tabs/panes, layouts and JavaScript mods, backed by core | Optional presentation and focus adapter; possible future Kanbi panel |

The old repository's latest GitHub release is v0.9.5 (October 14). The new app's
changelog begins with 0.1.15 (November 10); core's latest numbered changelog entry
is 0.1.21 (November 12), followed by unreleased changes. These are separate version
lines, not a Herdr 0.9.x drop-in upgrade. The new repositories' GitHub
`releases/latest` endpoints returned 404; their documented installers use
releases.herdr.dev. Changelog entries are not proof of the installed/downloaded
version. Sources: [old release](https://github.com/herdrdev/herdr/releases/tag/v0.9.5),
[app changelog](https://github.com/herdrdev/herdr-app/blob/master/CHANGELOG.md),
[core changelog](https://github.com/herdrdev/herdr-core/blob/master/CHANGELOG.md).

The inspected Linux/macOS app installer creates `herdr-app`, and installs core
if missing. It does not replace the `herdr` command. Side-by-side binaries are
therefore plausible; this does not imply shared servers, shared terminal IDs or
an automatic session migration. No old-to-new live-session bridge was established
by this investigation. No upstream TUI retirement commitment was established
either. [Installer source](https://github.com/herdrdev/herdr-app/blob/master/distribution/install.sh).

## What changes for Kanbi

Our current contracts are in [multiplexer-contracts.md](multiplexer-contracts.md).
The implementation is in `internal/multiplexer/herdr`, with additional direct
Herdr coupling in `internal/runtime/fleet.go`.

| Kanbi requirement | Current Herdr | New core/app assessment |
| --- | --- | --- |
| Start a harness in its recorded cwd | Create tab/shell, then `agent start --kind --pane` | `hc run --bg --cwd ... -- <argv>` / `terminal.run`; new response parsing and ownership validation |
| Read, input and close | Agent/pane read, send-text, send-keys, pane close | `hc read`, `send`, `keys`, `kill`; semantics need tests, especially literal text versus submitted prompts |
| Focus an existing ticket | Agent/tab/pane focus | Core has `hc attach`; app has `agent.focus` and `pane.focus`. Terminal attachment and desktop focus are different operations |
| Native status | Herdr brand detection/integrations plus Kanbi fallback | Core stores only program-reported OSC 7501 status. No report means no native status; preserve fallback/unknown handling |
| Harness resume handle | Herdr `agent_session` plus Kanbi fallbacks | Current core Terminal/Status schemas expose no equivalent native resume field. Verify harness-specific capture independently |
| Container identity | Stored namespace, agent, pane and terminal metadata | Core address is stable on its core; pin runtime endpoint/machine and attempt ownership, not just `t7` |
| Inspector | Bounded ANSI visible screen, no resize/input | Core read API and observe attachments are candidates; verify exact formatting, bounds and non-interference |
| Fleet | Moves real panes and journals their restoration | Existing implementation cannot transfer unchanged; app layouts can show live terminals, but require a separate capability and recovery design |

Sources: [core commands](https://github.com/herdrdev/herdr-core/blob/master/docs/commands.md),
[core protocol](https://github.com/herdrdev/herdr-core/blob/master/schema/README.md),
[terminal/status schema](https://github.com/herdrdev/herdr-core/blob/master/schema/hc.openrpc.json),
[app commands](https://github.com/herdrdev/herdr-app/blob/master/docs/commands.md).

Two lifecycle traps deserve explicit tests:

1. Core defaults to parent-child ownership for launches from an hc terminal;
   children normally end with the parent. Kanbi must deliberately select independent
   ownership (for example `--no-parent`) so quitting the board leaves agents alive.
2. Kept terminals recover lost programs as fresh shells, potentially at the same
   address. Existence of that address is insufficient to validate the original
   agent attempt. Recovery must preserve meaningful exited/repair state and resume
   only with a verified harness ref. A shell is not a resumed conversation.

Core templates wait for reported status by default; their `ready: quiet` option
explicitly does not prove readiness. Do not assume the old integrations supply
new-core status or refs, or replace Kanbi's tested startup/prompt gates with
output quietness. [Template semantics](https://github.com/herdrdev/herdr-core/blob/master/docs/templates.md).

## Can we support both?

Yes in principle, as two backends with separate persisted identities. Kanbi
already has generic container fields and a multiplexer interface. Its runtime
selection, doctor checks, prompt delivery, bootstrap and Fleet still need auditing;
this is more than changing a binary path. Existing attempts must continue routing
to their stored backend. Only new attempts should use a newly selected backend.
Never reinterpret old pane IDs as hc addresses or rewrite historical ownership.

For new-core sessions, the same terminal could be viewed through `hc attach` or
the desktop app. The app adopts core terminals carrying `herdr.space` and
`herdr.tab` labels; unlabelled independent terminals do not automatically appear
as app panes. The app also supports peeking at terminals and terminal blocks in
custom layouts. This creates a useful route to a board beside live agent panes.
Multiple controlling viewers compete for terminal size; observation must remain
read-only. [App model and labels](https://github.com/herdrdev/herdr-app/blob/master/defaults/AGENTS.md),
[viewer ownership](https://github.com/herdrdev/herdr-core/blob/master/schema/README.md).

## Should Kanbi become an app mod?

A native board is technically plausible: mods can render panels, run commands,
fetch data and embed live terminals. They use JavaScript describing native GPUI
elements, not HTML, a browser or Node. Our Bubble Tea UI would require a new
presentation implementation. A mod could call Kanbi's CLI for supported actions,
but must not become a second database/lifecycle authority. App shared state/core
documents should hold presentation state, not replace SQLite ticket history.
[Mod capabilities](https://github.com/herdrdev/herdr-app/blob/master/defaults/AGENTS.md).

The upside is a board and real agent terminals in one graphical workspace, with
custom layout options and mouse interaction. The cost is another UI to maintain,
desktop/GPU requirements, new extension contracts, and lifecycle integration work
that remains necessary underneath it. The new app/core already supply agent lists,
remote terminals and coordination; Kanbi's distinctive value remains ticket
workflow, provider sync, durable attempts, worktrees, checkpoints and Operators.

Retaining the TUI protects terminal/SSH use and avoids making a desktop session
mandatory. App support can be additive; a native mod should follow demonstrated
user benefit rather than being a prerequisite for core adoption.

## Proposed bounded follow-up

1. Use a pinned core build and private session/state to test fake harness launch,
   exact cwd/argv, concurrent opens, literal/multiline input, readiness, reads and
   close. Do not use or migrate the user's current Herdr sessions.
2. Prove board exit leaves agents running, endpoint/label ownership validation,
   observation failure versus absence, and crash/restart recovery into a shell.
3. Verify status and native resume-ref capture separately for Pi, Codex, Claude
   and Copilot. Missing status must stay unknown/fallback; missing refs must route
   to repair. Run real harness checks only after deterministic checks pass.
4. Test the existing Kanbi TUI in an app terminal, then focus/adoption of the same
   core-backed ticket from the board. Gate app-only features on app availability.
5. Evaluate a disposable app layout displaying the board and live agent terminals;
   prove restoration and viewer-size behavior before promising Fleet parity.
6. Decide on a native mod after this evidence. Preserve the old adapter until a
   deliberate support decision; dual support means two lifecycle test matrices.

The core protocol is documented as preview, generation 1/version 0.x, with an
additive compatibility test against its released schema. Pin capabilities and
validate against a released build; default-branch docs may include unreleased
features. [Protocol compatibility policy](https://github.com/herdrdev/herdr-core/blob/master/schema/README.md).

## Verification and limits

Read Kanbi's runtime contracts and inspected its adapter, generic interface and
Fleet coupling; checked local command availability/version. Only `herdr` 0.9.3
was found; `hc` and `herdr-app` were absent. Reviewed upstream README files,
commands, schemas, app mod guide, installer and changelogs. No app/core was
installed, no runtime launched, and no live database or session modified.

This documentation-only investigation does not claim tested new-runtime support.
Go fmt/test/vet, smoke and UI validation were not run because no code or supported
behavior changed. A future implementation must run the repository's required
checks and isolated real UI/runtime validation.
