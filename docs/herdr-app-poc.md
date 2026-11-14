# Herdr app proof of concept

This opt-in Linux experiment puts a native Kanbi board beside actual herdr-core
terminals. It retains the existing Kanbi application and SQLite lifecycle through
CLI calls and a narrow prototype bridge. This is a historical graphical experiment
and a private sandbox, not the app/core host adapter. For the unchanged Kanbi TUI
on either supported Herdr host, use [Herdr app/core setup](herdr-core.md).

## Open the installed POC

Choose **Kanbi Herdr POC** in the application launcher, or run:

```bash
~/.local/bin/kanbi-herdr-poc
```

The dedicated launcher selects its own app home and `HC_SESSION=kanbi-poc`.
Opening the ordinary **Herdr** launcher uses Herdr's ordinary configuration;
use **Kanbi Herdr POC** for this experiment.

The app is installed separately as `~/.local/bin/herdr-app`, with core available
as `~/.local/bin/hc` and `herdr-core`. The existing `herdr` executable, running
TUI server, database and agent sessions are not replaced.

## Try it

1. Select a card. **Open demo** starts or focuses an offline interactive terminal
   beside the board. Type `work`, `permission`, `approve`, or `quit`. The demo's
   status is reported through core; it makes no model requests.
2. Type a title and press Enter or **Create demo ticket**. Use **In progress**,
   **Move to review** and **Done** to move the selected ticket. Type a note and
   click **Save note**; **Details** shows the latest attempt and saved notes.
3. Select **Try real Codex in Herdr app**, then **Open Codex**, to return to the
   tested real conversation. **New Codex ticket** creates another opt-in test
   ticket; **Open Codex** starts it using your installed/authenticated Codex.
   Real Codex uses your account and quota. It runs in the private sandbox project
   with workspace-write sandboxing and on-request approvals.
4. **Full TUI** opens the existing Kanbi TUI in a core terminal beside the native
   panel. Select **App Sandbox** in its initial picker. Its normal watcher keeps
   durable session observations current. Press `x` in the TUI to close an agent;
   use `S` for session settings and confirmed start-fresh. `q` exits the board,
   leaving independently owned agent processes running.
5. **Expand board** gives the native board the window. **Open demo**, **Open Codex**
   or **Full TUI** restores a terminal alongside it. Click a terminal before typing
   to the harness; click a native field before entering ticket text.
6. Close and reopen the POC window. Core terminals continue running; reopening
   restores the custom layout. This was verified with unchanged process IDs.

The native board has horizontally scrolling columns and vertically scrolling card
lists. Native shortcuts use Ctrl+Alt+N for the title input, Ctrl+Alt+J for next
selection, Ctrl+Alt+O for open, Ctrl+Alt+R for Review and Ctrl+Alt+H for details
while the board owns keyboard focus. These do not override Herdr's global
Ctrl+Shift+N/O actions. Mouse controls and the full TUI are the primary POC paths.

## Isolation and ownership

All POC data is under:

```text
~/.local/share/kanbi/herdr-app-poc/
  app/           separate Herdr settings, native mod and personal layout
  bin/           copied POC scripts and private Kanbi build
  config.yaml    points only at the private DB and prototype bridge
  kanbi.db       private durable ticket/session history
  bridge.json    prototype pane to core-terminal ownership mapping
  project/       working directory for POC tickets
  state/ data/   private Kanbi state and attachments
```

The desktop entry is `~/.local/share/applications/kanbi-herdr-poc.desktop`.
No autostart entry, web service or extra scheduling daemon is installed. Herdr's
own core manages process persistence. On this Linux machine, core startup reported
that it enabled user lingering so it can outlive logins. POC telemetry is disabled
in its separate app settings.

Tickets and sessions are created by Kanbi, not by the mod writing SQLite. Reopening
an active ticket validates its unique private pane label and preserves its process
and session row. Every ticket agent starts without a parent and without `keep`,
so a lost program is not silently replaced by a shell and presented as healthy.
The demo uses the configurable `pi` harness slot; the native board labels it
**Offline demo**. The full TUI will show `pi` for that slot.

## Limits

- Core 0.1.21 and app 0.1.15 were tested. Default-branch mod docs include exports
  absent from the installed app; the mod uses the installed API surface.
- This bridge supports the tested launch, validation, read, literal input, key,
  focus and close paths. Fleet and pane moves are unsupported. Inspector reads
  are plain core screen text, not full ANSI-visible-screen parity.
- Real Codex launch and interaction were verified. Real Pi, Claude and Copilot
  are not certified here; their configurable slots run the offline demo.
- No native harness resume ref is fabricated. After an attempt ends without a
  verified ref, use the TUI's explicit repair/start-fresh flow. The POC deliberately
  does not promise safe resume for its wrapper-launched sessions.
- Native Details shows the latest attempt and notes. It is not a full graphical
  history, worktree-review, checkpoint, Operators or provider-sync implementation.
- The private core session is separate from ordinary core sessions. Mobile access
  to this non-default session has not been configured or tested.
- Keep Full TUI running for Kanbi's durable runtime watcher. The native panel also
  shows current core-reported state; a native panel by itself is not a replacement
  scheduler or runtime-refresh service.

## Rebuild and verify

Install the official app/core first, then from the repository root:

```bash
python3 scripts/prototypes/herdr-app-poc/setup.py
~/.local/bin/kanbi-herdr-poc check \
  ~/.local/share/kanbi/herdr-app-poc/app/mods/kanbi-poc
# Open Full TUI once and select App Sandbox before this smoke:
python3 scripts/prototypes/herdr-app-poc/verify.py
```

Setup preserves the private DB, but replaces its configuration, mod and copied
scripts. Do not use it as a settings editor after customizing those files.

Verification completed on 14 November 2026:

- `go fmt ./...`, `go test ./...`, `go vet ./...`: passed.
- `go test ./internal/harness ./internal/runtime ./internal/storage`: passed.
- `./scripts/smoke.sh --skip-checks`: passed against the existing real isolated
  Herdr TUI runtime and fake harnesses.
- Native `herdr-app check`: passed; the running app reported no mod errors.
- Actual native pointer/keyboard title entry, card move and note/save controls
  persisted data through the real CLI; screenshots were inspected.
- Offline core tests verified unique ownership, one attempt and identical PID on
  reopen, blocked/working/done reporting, literal input, ticket/note persistence,
  and repair after program loss with prior history retained.
- Real Codex 0.169.0 received the ticket prompt and returned `KANBI_CORE_POC_OK`.
  It remained interactive afterward. This was one short authenticated turn, not
  certification of real resume across harnesses.
- Closing/reopening the app preserved exact demo, full-TUI and Codex process IDs.
- Actual core-hosted Kanbi TUI grids were checked while attached at 80x24 and
  160x45. The required old-Herdr disposable fixture also rendered Master at
  160x45 and 80x24 with its header, footer and overflow controls visible; it was
  stopped afterward.

## Stop or remove

Closing the window leaves agents running. For an orderly stop, use Full TUI to
close POC ticket sessions, then `q` to exit that board. The remaining core session
can be inspected explicitly with `HC_SESSION=kanbi-poc hc ls`.

To end the entire disposable core session, including all its POC programs, use:

```bash
HC_SESSION=kanbi-poc ~/.local/bin/hc server stop
```

This is separate from the existing `herdr` TUI server. Preserve `kanbi.db` if you
want to retain the experiment's ticket/session history. Removing only the POC
launcher/desktop entry and its own app/mod files does not uninstall Herdr app or
core. No cleanup of the user's ordinary Kanbi state is required.
