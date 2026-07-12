# Kanbi

Kanbi is a Go/Bubble Tea TUI for orchestrating multiple resumable agent CLI sessions across one or more Kanban boards.

## Install

Download the native archive for Linux or macOS from [GitHub Releases](https://github.com/carlotran4/kanbi/releases/latest), verify it against `SHA256SUMS`, extract `kanbi`, and place it on your `PATH`. Kanbi requires tmux by default and at least one authenticated supported agent CLI.

```bash
kanbi version
kanbi doctor
kanbi --help
```

See [`docs/installation.md`](./docs/installation.md) for supported platforms, prerequisites, checksum verification, first-run setup, upgrades, rollback, and uninstall. Source-checkout development installation remains documented separately below.

## Project Documents

- [`AGENTS.md`](./AGENTS.md) — onboarding instructions for autonomous coding agents
- [`docs/architecture.md`](./docs/architecture.md) — current architecture, scope, invariants, and source-of-truth document order
- [`docs/multi-board-behavior.md`](./docs/multi-board-behavior.md) — current multi-board and Master view behavior
- [`docs/harness-contracts.md`](./docs/harness-contracts.md) — current supported harness commands/ref capture contracts
- [`docs/multiplexer-contracts.md`](./docs/multiplexer-contracts.md) — tmux/Herdr runtime substrate contract
- [`docs/tmux-to-herdr-migration.md`](./docs/tmux-to-herdr-migration.md) — recommended semantics for moving existing tmux workflows to Herdr
- [`docs/autonomous-verification.md`](./docs/autonomous-verification.md) — how autonomous agents should verify their work
- [`docs/installation.md`](./docs/installation.md) — production installation, first run, upgrade, rollback, and uninstall
- [`CHANGELOG.md`](./CHANGELOG.md) — release and upgrade history
- [`docs/archive/design-spec.md`](./docs/archive/design-spec.md) — historical product/design context; current docs win on conflicts

## First run and multi-board behavior

- On an empty installation, `kanbi` opens with a three-page, dismissible first-run guide covering tmux/harness prerequisites, `kanbi doctor`, board working directories, session start/close semantics, repair, and backups. Press `Esc` to skip it immediately.
- `kanbi` then opens with a board picker. Choose `Master (all boards)` or a named board; press `c` there to create a board and set the directory where its agent commands will run.
- Press `b` inside the TUI to switch boards without restarting.
- Press `g` on a GitHub-backed ticket to open its GitHub issue URL in your browser.
- `Master` aggregates unarchived tickets from non-archived boards by matching column `workflow_key` (defaults to each column's display name; rename does not change the key).
- Press `f` in `Master` to filter/search by board, runtime/state, harness, text, or archived tickets. Save (`S`) / apply (`P`) named presets; filters are never auto-applied on startup. Press `C` to clear.
- Pressing `n` in `Master` prompts for the target board, then creates the ticket in that board's column with the same workflow key.
- Each board has a working directory. Opening/sending a ticket starts its agent terminal container in the ticket's board directory, including from `Master`.
- Board-local ticket numbers are preserved, so different boards may both have `T-001`; CLI ticket commands accept `--board NAME` when needed.
- tmux is the default multiplexer. Each launched board UI uses its own tmux runtime session for ticket windows; session rows store that tmux session name so other board instances can validate or switch to it through the shared database.
- Agent terminal container names include the board id to avoid cross-board collisions within a runtime namespace.
- Create boards from the CLI with `kanbi boards add "Board Name" --cwd /path/to/project`; `--cwd` defaults to the current directory. Boards use one ticket metadata backend chosen at creation; `local`, `github`, and `atlassian` (Jira) are implemented. List boards with `kanbi boards`. Provider `--config` JSON is stored unencrypted in SQLite, so keep credentials in the documented environment variables rather than embedding tokens in `--config`.
- Sync ticket backends from the CLI with `kanbi sync` or `kanbi sync --board "Board Name"`.
- Rename/update boards with `kanbi boards rename OLD NEW` and `kanbi boards set-cwd NAME /path/to/project`.
- In the TUI board picker: `c` creates, `r` renames, `w` sets cwd, `a` archives/unarchives, `s` toggles provider sync, `e`/`i` export/import board packages, `A` shows archived boards, and `d` hard-deletes. Archive is the non-destructive default hide path; hard delete is permanent, blocked while sessions are active, local-only, and removes attachments after the SQL commit.

## Backup, restore, and deletion safety

Create a consistent backup (SQLite including committed WAL data, plus attachments) with:

```bash
kanbi backup ~/kanbi-backup.kanbi
```

Restore only after stopping every Kanbi process that uses the target database:

```bash
kanbi restore ~/kanbi-backup.kanbi          # only when no database exists
kanbi restore ~/kanbi-backup.kanbi --force  # atomically replace an existing database
```

Full backups contain a versioned `kanbi-backup` manifest, a SQLite snapshot, and attachment files. Restore validates archive paths, the manifest/schema version, and SQLite integrity before replacement. It refuses an existing database without `--force` and refuses restore while SQLite WAL/SHM sidecars exist. Database replacement uses an atomic rename; database and attachment replacement are rollback-protected but cannot be one filesystem transaction, so do not run restore concurrently with Kanbi. External ticket providers are not a backup of local runtime/session history or attachments.

Single-board packages use a separate `kanbi-board-package` format (`kanbi boards export|import`). Import is create-new-only (no merge), remaps IDs in one SQLite transaction, stages attachments with compensating board delete on failure, and always leaves the imported board archived with sync disabled.

Prefer board archive over hard delete. Hard delete is permanent, local-only, blocked while sessions are active, and requires typing the exact board name in the TUI.

## CLI automation surface

Most non-interactive commands support machine-readable output with `--json` or `--format json`.
JSON payloads include a `schema` field such as `kanbi.v1.tickets` for agent/orchestrator consumers.

Useful agent-facing commands:

```bash
kanbi boards --json
kanbi list --json [--board NAME]
kanbi show T-001 --json [--board NAME]
kanbi state --json [--board NAME]

kanbi add "Title" --body "..." --json
kanbi add "Title" --body-file ./ticket.md --json
kanbi update T-001 --title "New title" --body-file ./body.md --json
kanbi move T-001 --to "In Progress" --json

kanbi notes list T-001 --json
kanbi notes add T-001 --body "Progress update" --json
```

`kanbi open`, `kanbi sync`, and `kanbi doctor` also accept `--json`. Board-local ticket IDs may be ambiguous across boards; pass `--board NAME` when needed.

## Multiplexer configuration

Kanbi defaults to tmux as its configured multiplexer runtime substrate. To launch new ticket sessions through Herdr instead, configure `~/.config/kanbi/config.yaml` (or `$KANBI_CONFIG`):

```yaml
multiplexer:
  default: herdr
  herdr:
    binary: herdr
    session: default
    workspace_strategy: board
    focus_on_open: false
```

Existing active sessions keep using the multiplexer stored in their session row, so tmux sessions continue to validate/focus through tmux after switching the default for new launches. Press `M` on a tmux-backed ticket with a session ref to explicitly move it to Herdr by gracefully closing tmux and resuming in a new Herdr pane. Stale or inactive tmux sessions can resume into Herdr only through a valid harness session ref, while start-fresh creates a new Herdr attempt and preserves old tmux history; see [`docs/tmux-to-herdr-migration.md`](./docs/tmux-to-herdr-migration.md). With `default: herdr`, running `kanbi` outside Herdr starts the board UI in a focused Herdr pane and attaches to Herdr; when already inside a Herdr pane it runs the board directly to avoid nesting. For one-off testing, `KANBI_MULTIPLEXER=herdr` overrides `multiplexer.default`.

Herdr basics:

- Install Herdr from <https://herdr.dev/docs/install/> and run `herdr` once so its server/session is available.
- `multiplexer.herdr.session` selects the Herdr session namespace (`default` is fine for most users).
- Ticket panes always open as a new tab in the Herdr workspace the Kanbi board itself is running in — never a separate workspace — mirroring tmux windows inside one session.
- `workspace_strategy: board` groups ticket panes by Kanbi board/project when Kanbi isn't running inside a Herdr pane (e.g. detection falls back to matching an existing workspace by board directory).
- `focus_on_open: false` lets Kanbi start/focus containers without stealing focus unless requested.
- Harness config remains separate; `pi`, `codex`, `copilot`, and `claude` still define agent commands and resume refs.
- Run `kanbi doctor` after changing multiplexer config. If Herdr is selected, doctor checks the configured Herdr binary and `herdr status`.

## Development

This path is for contributors working from a source checkout, not production installation. Install a development launcher on your `PATH`:

```bash
./scripts/install-dev.sh
```

By default this writes `~/.local/bin/kanbi`. The launcher rebuilds `.bin/kanbi` from this checkout whenever `cmd/`, `internal/`, `go.mod`, or `go.sum` are newer than the cached binary, then execs it. Set `KANBI_BIN_DIR=/some/path` to install the launcher somewhere else.

Baseline checks:

```bash
go fmt ./...
go test ./...
go vet ./...
```

Opt-in real GitHub backend smoke test (mutates the configured repository; not run by normal smoke):

```bash
KANBI_GITHUB_OWNER="OWNER" \
KANBI_GITHUB_REPO="REPO" \
./scripts/github-backend-smoke.sh
```

Auth uses `KANBI_GITHUB_TOKEN`, `GITHUB_TOKEN`, or `gh auth token`. The token needs repo issue read/write permission. The script creates a temporary label-scoped issue, pulls it, observes remote edits/comments, closes it, and leaves cleanup best-effort.

Opt-in real Jira backend smoke test (mutates the configured Jira project; not run by normal smoke):

```bash
KANBI_JIRA_SITE_URL="https://ORG.atlassian.net" \
KANBI_JIRA_PROJECT_KEY="AK" \
KANBI_JIRA_EMAIL="you@example.com" \
KANBI_JIRA_API_TOKEN="TOKEN" \
./scripts/jira-backend-smoke.sh
```

## Data privacy and process behavior

Kanbi creates application directories for the current user (`0700`) and restricts sensitive files, including existing config and SQLite database/WAL/SHM files, to `0600`. Explicit pre-existing parent-directory overrides keep their existing permissions. Pressing `q` from the board or `Ctrl+C` anywhere exits the UI without terminating active ticket agent sessions; close a ticket session explicitly with `x`.

Remote-provider sync is owned by the in-process sync manager (startup, periodic, and mutation-triggered) with durable per-board SQLite leases across Kanbi processes, lease renewal during long ops, and cancel-on-lease-loss. Provider HTTP calls have explicit timeouts; only idempotent GET/list requests retry with backoff. Issue create is fail-closed after a durable pending push token so crashes cannot silently duplicate remote tickets; recovery is find-or-link via a body marker. Stale leases recover automatically after expiry. Sync/runtime failures write redacted diagnostics (operation, board/ticket context, timestamp, attempt, cause)—never tokens, prompts, session refs, or terminal excerpts. Deleting a provider-backed note creates a durable local tombstone: the remote comment is not deleted, but it cannot be re-imported into Kanbi on later sync.

## License

Kanbi is available under the [MIT License](./LICENSE). Copyright © 2026 Carlo Tran.

## Status

Kanbi is a UX-ready beta with multi-board TUI/CLI behavior, configurable multiplexer-backed ticket sessions, Pi/Codex/Copilot/Claude command wiring and ref capture, deterministic fake-harness coverage, runtime/sync hardening (owned background work, provider timeouts/retries, lease renew/loss, durable diagnostics, soak coverage), and opt-in real-harness verification. Further polish remains trackable on the board.

## Runtime states and accessible indicators

Every card prints a runtime state in words: `not started`, `starting`, `running`, `waiting for user`, `permission required`, `idle / unknown`, `closing`, `closed / resumable`, `repair required`, or `error`. Waiting and permission requests are therefore distinguishable without theme colors. Cards also pair symbols with text: `● active container`, `○ resumable`, `! error / repair`, and `- no active container`. Press `?` for the complete in-product legend and controls; scroll long help with `j`/`k` or arrow keys.

Major list dialogs follow their focused control in short terminals and help is scrollable. At 80x24 controls remain reachable; below that size Kanbi clips safely and marks hidden content with `more`. Session action failures name the failed operation, retain the underlying cause, and show a concrete next step on a separate line. Provider sync failures are degraded/offline states: Kanbi continues from its local SQLite projection and shows a `kanbi sync --board` retry command. Startup reconciliation failures use the parallel **runtime reconciliation degraded (local data available)** banner with a `kanbi doctor` next step; they are never silently discarded.

See [`docs/ux-readiness.md`](./docs/ux-readiness.md) for the manual terminal/theme/tmux matrix and known accessibility limitations.

## Ticket Inspector And Notes

Press `e` to open the unified ticket inspector/editor. The inspector renders the ticket as a polished document while keeping fields editable in place:

- `Tab` / `Shift+Tab` — move focus between title, description, harness, and notes
- `Ctrl+S` — save the ticket
- `Ctrl+E` — open the description in `$EDITOR`
- paste base64 image data while the description is focused — save it under `~/.local/share/kanbi/attachments/<internal-ticket-database-id>/` and insert a Markdown image reference
- image references render as inline Kitty graphics in capable terminals during description preview, including Kitty-compatible terminals detected through tmux's environment, or as `[image: filename]` placeholders otherwise
- `Esc` — cancel/close the inspector

Inside the notes section:

- `a` — add a new note
- `e` — edit the selected note
- `d` — delete the selected note
- `j`/`k` — navigate notes
- `Ctrl+S` while editing — save the note
- `Esc` while editing — cancel

Notes are not sent to the agent session. On local boards they remain personal/local annotations. On GitHub and Atlassian/Jira boards, sync maps notes to provider issue comments.
