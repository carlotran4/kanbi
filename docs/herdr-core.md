# Herdr app/core host

Kanbi's existing terminal interface can run on either Herdr TUI or Herdr
app/core. The board, keys, editing, tickets, providers, worktrees, Operators and
session history remain shared Kanbi behavior. No native graphical board or mod
is needed. The earlier [graphical POC](herdr-app-poc.md) is a separate experiment.

## Setup

Install Herdr app and `hc` (tested with app 0.1.15 and core 0.1.21, protocol
generation 1). Open Herdr app, then configure Kanbi:

```yaml
multiplexer:
  default: herdr-core
  herdr_core:
    binary: hc
    app_binary: herdr-app
    session: default
    focus_on_open: true
```

Run `kanbi doctor`, then `kanbi`. From outside core, Kanbi creates a terminal
for its ordinary TUI and shows it in the running app. From an existing core
terminal, it runs there. `kanbi --board` always runs the TUI directly. CLI
commands can launch/observe core agents without a graphical window when
`focus_on_open: false`; use `HC_SESSION=default hc attach TERMINAL` for terminal
access. Reopening an active ticket through Kanbi requires the app for focus.

For a separate core session, launch the app with `HC_SESSION=YOUR_SESSION` and
set `herdr_core.session` to the same value. If using a private `HERDR_APP_HOME`,
pass it to both the app and Kanbi. App and Kanbi must reach the same core; focus
checks the terminal's app space/tab and address, so an unrelated `t1` in another
window cannot be focused accidentally. If app discovery cannot find `hc`, set
the app's `core` setting to the installed executable's absolute path.

The existing Herdr TUI host remains the default:

```yaml
multiplexer:
  default: herdr
```

`KANBI_MULTIPLEXER=herdr-core` can override the selected host. This changes where
**new attempts** launch; active attempts retain their stored host and must be
closed explicitly before moving. Kanbi never converts old runtime IDs, rewrites
session ownership or drops history. Host binary and session settings for active
attempts must remain reachable. Avoid two Kanbi instances managing the same
board while experimenting with different hosts; use a separate `KANBI_CONFIG`
and `KANBI_DB` for a trial.

## Colors and graphical trial launches

Kanbi honors `NO_COLOR` on both hosts. A nonempty inherited `NO_COLOR` disables
card fills, attention colors and selection styling, even when the terminal
reports true-color support. An automation-launched graphical trial must pass
`NO_COLOR=''` explicitly so it also overrides a core server started with the
runner's no-color environment. Merely dropping the variable from the launcher
is insufficient if it remains in the server's environment. The separate
`kanbi-herdr-core` trial launcher applies this override; ordinary Kanbi CLI
invocations still honor an intentional `NO_COLOR` preference.

The app/core smoke checks real VT paint for foreground colors and distinct
header/selected-card background fills at 160x45 and 80x24, in addition to text
and controls. Text-only `terminal.read` snapshots cannot verify appearance.
On 2026-11-14 the actual card/header colors matched the disposable Herdr TUI
fixture at both sizes; deliberately reintroducing `NO_COLOR=1` failed the new
visual assertion. No theme, card layout or renderer behavior was changed.

## Runtime behavior

- Core launches the configured harness argv directly, with its working directory,
  environment and existing prompt/ref contract. There is no compatibility shim
  or intermediate shell. Agents have no parent and outlive the board/window.
- Each reference records the core identity, session namespace, random ownership
  token, terminal address, program PID and creation time. Validation checks all
  of them before input, focus, reads and close. A recovered shell at the same
  address is not the original agent and follows normal missing-session repair.
- Ticket tabs inherit the calling board terminal's current `herdr.space` label,
  read on every new attempt. Selecting another GUI space does not redirect a
  board's agents; moving the board changes future launch destinations. The
  caller's core identity and live address are checked before process creation.
- Starting Kanbi outside core uses the selected local app space. Only an empty
  app needs a new space. The board receives its space/tab labels atomically at
  launch, before its UI or scheduler can start agents. There is no shared,
  hard-coded Kanbi destination space.
- Headless launches without a calling terminal remain possible without a GUI;
  their first presentation uses the selected local app space, or creates one
  if the app is empty. Reopening already-presented agents preserves their
  current labels and focuses the exact tab, including sessions from older
  versions. Existing terminals are never moved automatically.
- App adoption uses public `herdr.space`/`herdr.tab` labels. Kanbi disables core
  keep after adoption completes; the app may enable it again when adopting a
  terminal after restart. PID/creation/reason checks remain authoritative.
- Core OSC 7501 reports supply running/permission/input/error state. Without a
  report, existing Kanbi output detection remains available. Idle alone does
  not prove an input editor is ready. Core status is never a conversation ref.
- Verified harness refs still come from the existing Pi extension, configured
  transcript markers or cwd/time/prompt-validated harness histories. There is
  no core equivalent of old Herdr's `agent_session` report. Missing refs use
  existing repair/start-fresh controls. Harness names/commands are unchanged.
- The inspector uses a labeled text transcript fallback on core. Core's public
  `terminal.read` API does not provide the old ANSI visible-screen snapshot.
- Fleet's pane relocation currently requires the Herdr TUI host. On core, `v`
  explains this without moving/closing anything; use the app's native layouts.
  This is a host capability limit, not a redesigned Kanbi interface.
- Mobile clients connecting to the same core can display Kanbi's terminal;
  phone connection/setup and mobile input are not validated by this change.

## Verification

Normal Go tests include core ownership fences, status/absence/error handling,
all four harness lifecycle contracts with fake agents, duplicate open,
close/resume/history, outage preservation and rejecting a replacement shell.
They do not require installed core/app or model authentication.

Opt-in real-core transport check (isolated session, cleaned up):

```bash
KANBI_CORE_TESTS=1 go test -v ./internal/multiplexer/herdrcore -run '^TestRealCore$' -count=1
```

Real desktop/CLI smoke with an offline fixture harness, private DB, app home and
core session; stops only its own app/core and retains artifacts under `/tmp`:

```bash
go build -o /tmp/kanbi-core-candidate ./cmd/kanbi
python3 scripts/herdr-core-smoke.py /tmp/kanbi-core-candidate
```

It checks existing-space board placement, first-open with unrelated GUI focus,
reopen space/tab/PID identity, app focus/adoption, TUI Enter, and tab renaming without focus changes,
unchanged board at 160x45 and 80x24, duplicate-open
PID/attempt identity, TUI close, fixture-ref resume, app restart persistence and
missing-program reconciliation. These are offline fixture refs, not evidence
of authenticated model resume. Existing authenticated Herdr TUI lifecycle checks
remain separately opt-in through `scripts/real-harness-lifecycle.sh`.

Authenticated Pi/Codex verification (consumes model quota and writes their native
session histories, while Kanbi/app/core resources remain isolated):

```bash
KANBI_REAL_HARNESS_TESTS=1 python3 scripts/herdr-core-smoke.py /tmp/kanbi-core-candidate --real-harnesses=pi,codex
```

Validated locally on 2026-11-14: both Pi and Codex produced the requested real
model response, yielded verified native refs, resumed after terminal kill, and
restored their conversation output. Copilot/Claude use deterministic lifecycle
coverage in this change; authenticated core turns for them were not run.

Public integration surfaces: [core API/schema](https://github.com/herdrdev/herdr-core/blob/master/schema/README.md)
and [app commands/adoption contract](https://github.com/herdrdev/herdr-app/blob/master/defaults/AGENTS.md).
