# Kanbi

Kanbi is a Go/Bubble Tea TUI for orchestrating multiple resumable agent CLI sessions across one or more Kanban boards.

## Project Documents

- [`AGENTS.md`](./AGENTS.md) — onboarding instructions for autonomous coding agents
- [`docs/architecture.md`](./docs/architecture.md) — current architecture, scope, invariants, and source-of-truth document order
- [`docs/multi-board-behavior.md`](./docs/multi-board-behavior.md) — current multi-board and Master view behavior
- [`docs/harness-contracts.md`](./docs/harness-contracts.md) — current supported harness commands/ref capture contracts
- [`docs/multiplexer-contracts.md`](./docs/multiplexer-contracts.md) — tmux/Herdr runtime substrate contract
- [`docs/tmux-to-herdr-migration.md`](./docs/tmux-to-herdr-migration.md) — recommended semantics for moving existing tmux workflows to Herdr
- [`docs/autonomous-verification.md`](./docs/autonomous-verification.md) — how autonomous agents should verify their work
- [`docs/archive/design-spec.md`](./docs/archive/design-spec.md) — historical product/design context; current docs win on conflicts

## Multi-board behavior

- `kanbi` opens with a board picker. Choose `Master (all boards)` or a named board.
- Press `b` inside the TUI to switch boards without restarting.
- Press `g` on a GitHub-backed ticket to open its GitHub issue URL in your browser.
- `Master` aggregates unarchived tickets from every board by matching column name (for example, all `Open` tickets together).
- Press `f` in `Master` to filter/search by board, runtime/state, harness, text, or archived tickets. Filters reset on app restart but persist while switching boards during one run; press `C` in the filter panel to clear them.
- Pressing `n` in `Master` prompts for the target board, then creates the ticket in that board's matching column.
- Each board has a working directory. Opening/sending a ticket starts its agent terminal container in the ticket's board directory, including from `Master`.
- Board-local ticket numbers are preserved, so different boards may both have `T-001`; CLI ticket commands accept `--board NAME` when needed.
- tmux is the default multiplexer. Each launched board UI uses its own tmux runtime session for ticket windows; session rows store that tmux session name so other board instances can validate or switch to it through the shared database.
- Agent terminal container names include the board id to avoid cross-board collisions within a runtime namespace.
- Create boards from the CLI with `kanbi boards add "Board Name" --cwd /path/to/project`; `--cwd` defaults to the current directory. Boards use one ticket metadata backend chosen at creation; `local`, `github`, and `atlassian` (Jira) are implemented. List boards with `kanbi boards`.
- Sync ticket backends from the CLI with `kanbi sync` or `kanbi sync --board "Board Name"`.
- Rename/update boards with `kanbi boards rename OLD NEW` and `kanbi boards set-cwd NAME /path/to/project`.
- In the TUI board picker: `c` creates a board, `r` renames, `w` sets cwd, and `d` deletes.

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

Existing active sessions keep using the multiplexer stored in their session row, so tmux sessions continue to validate/focus through tmux after switching the default for new launches. Stale or inactive tmux sessions can resume into Herdr only through a valid harness session ref, while start-fresh creates a new Herdr attempt and preserves old tmux history; see [`docs/tmux-to-herdr-migration.md`](./docs/tmux-to-herdr-migration.md). With `default: herdr`, running `kanbi` outside Herdr starts the board UI in a focused Herdr pane and attaches to Herdr; when already inside a Herdr pane it runs the board directly to avoid nesting. For one-off testing, `KANBI_MULTIPLEXER=herdr` overrides `multiplexer.default`.

Herdr basics:

- Install Herdr from <https://herdr.dev/docs/install/> and run `herdr` once so its server/session is available.
- `multiplexer.herdr.session` selects the Herdr session namespace (`default` is fine for most users).
- Ticket panes always open as a new tab in the Herdr workspace the Kanbi board itself is running in — never a separate workspace — mirroring tmux windows inside one session.
- `workspace_strategy: board` groups ticket panes by Kanbi board/project when Kanbi isn't running inside a Herdr pane (e.g. detection falls back to matching an existing workspace by board directory).
- `focus_on_open: false` lets Kanbi start/focus containers without stealing focus unless requested.
- Harness config remains separate; `pi`, `codex`, `copilot`, and `claude` still define agent commands and resume refs.
- Run `kanbi doctor` after changing multiplexer config. If Herdr is selected, doctor checks the configured Herdr binary and `herdr status`.

## Development

Install a development launcher on your `PATH`:

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

## Status

Alpha lifecycle hardening is complete: multi-board TUI/CLI behavior, configurable multiplexer-backed ticket sessions, Pi/Codex/Copilot command wiring and ref capture, fake and real harness verification, tests, and smoke verification are in place. Current follow-up work is tracked as tickets on the board.

## Ticket Inspector And Notes

Press `e` to open the unified ticket inspector/editor. The inspector renders the ticket as a polished document while keeping fields editable in place:

- `Tab` / `Shift+Tab` — move focus between title, description, harness, and notes
- `Ctrl+S` — save the ticket
- `Ctrl+E` — open the description in `$EDITOR`
- paste base64 image data while the description is focused — save it under `~/.local/share/kanbi/attachments/<ticket-id>/` and insert a Markdown image reference
- image references render as inline Kitty graphics in capable terminals during description preview, including Kitty-compatible terminals detected through tmux's environment, or as `[image: filename]` placeholders otherwise
- `Esc` — cancel/close the inspector

Inside the notes section:

- `a` — add a new note
- `e` — edit the selected note
- `d` — delete the selected note
- `j`/`k` — navigate notes
- `Ctrl+S` while editing — save the note
- `Esc` while editing — cancel

Notes are not sent to the agent session. On local boards they are personal/local annotations; future external ticket backends should sync them as provider comments.
