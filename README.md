# Kanbi

**A keyboard-first command center for coding agents.**

Kanbi turns tickets into durable agent workspaces. Create a task, choose Pi, Codex, Copilot, or Claude, and Kanbi opens the agent in its own terminal session—then keeps the board, runtime state, and resume path together.

Use one board for a project or the **Master** view to supervise work across every project without juggling terminal tabs.

> [!NOTE]
> Kanbi is currently beta software. It supports Linux and macOS, uses tmux by default, and expects at least one supported agent CLI to be installed and authenticated.

## Why Kanbi?

Running several coding agents usually means losing track of which terminal belongs to which task, which agents need input, and whether a closed session can be resumed.

Kanbi gives that work a home:

- **One active agent session per ticket** — opening an active ticket returns to the right terminal instead of starting a duplicate.
- **See what needs you** — cards show states such as `running`, `waiting for user`, `permission required`, `closed / resumable`, and `error` in plain text.
- **Leave and come back** — quitting Kanbi does not kill active agents. When an agent exposes a verified session reference, Kanbi can resume it later.
- **Keep projects separated** — every board has its own working directory, so agents start in the correct repository.
- **Supervise everything together** — Master combines tickets from all your boards and supports filtering by project, state, harness, and text.
- **Stay local or bring your tracker** — use local tickets, GitHub Issues, or Jira-backed boards.

## Quick start

### 1. Install the prerequisites

You need:

- Linux or macOS
- a UTF-8 terminal
- [tmux](https://github.com/tmux/tmux) on your `PATH`
- at least one authenticated agent CLI: **Pi**, **Codex**, **GitHub Copilot CLI**, or **Claude Code**

Kanbi also supports [Herdr](https://herdr.dev/) as an opt-in runtime, but tmux is the recommended default.

### 2. Install Kanbi

Download the archive for your platform and `SHA256SUMS` from [GitHub Releases](https://github.com/carlotran4/kanbi/releases). Kanbi is in beta, so select the prerelease you want explicitly.

```bash
# After verifying and extracting the downloaded archive:
mkdir -p "$HOME/.local/bin"
install -m 0755 kanbi "$HOME/.local/bin/kanbi"
export PATH="$HOME/.local/bin:$PATH"

kanbi version
kanbi doctor
```

Resolve every `fatal` result from `kanbi doctor`. Missing agent CLIs that you do not plan to use are warnings.

See the [installation guide](./docs/installation.md) for platform archives, checksum verification, upgrades, rollback, and source builds.

### 3. Create your first board

Run this from the project you want an agent to work on:

```bash
cd /path/to/project
kanbi boards add "My Project" --cwd "$PWD"
kanbi
```

Then:

1. Select **My Project** in the board picker.
2. Press `n` to create a ticket and open its inspector.
3. Add a title and description, choose an agent, then press `Ctrl+S` to save.
4. Back on the board, press `Enter` to send the ticket to the agent.
5. Press `x` when you want to close the session gracefully.

That is the core loop: **plan on the board, work in the agent terminal, return to the board to supervise**.

## How it feels to use

```text
Create a ticket
      │
      ▼
Press Enter ───────► agent opens in its own terminal session
      │                              │
      │                              ├── running
      │                              ├── waiting for user
      │                              └── permission required
      │
      ▼
Open again ───────► focus the existing session
      │
      ▼
Close with x ─────► resume later when a session reference is available
```

Kanbi keeps session history when you start fresh or repair a ticket. If a session cannot be resumed safely, Kanbi asks what to do instead of silently attaching the ticket to the wrong process.

## Essential controls

| Key | Action |
| --- | --- |
| `Enter` | Start, open, or resume the selected ticket |
| `n` | Create a ticket |
| `e` | Open the ticket inspector/editor |
| `x` | Gracefully close the ticket's agent session |
| `b` | Switch boards or open Master |
| `f` | Filter tickets in Master |
| `H` / `L` | Move a ticket between columns |
| `J` / `K` | Reorder a ticket within its column |
| `a` | Archive a ticket |
| `g` | Open a linked GitHub issue in the browser |
| `?` | Show all controls and the runtime-state legend |
| `q` / `Ctrl+C` | Exit Kanbi **without terminating active agents** |

From the board view, press `?` for the core controls and runtime-state legend. Contextual actions also appear in modals and the footer.

## Built for multiple projects

Each board owns its working directory, workflow, tickets, and local session history. Different boards can both have a `T-001`; Kanbi keeps their work and terminal sessions separate.

Open **Master (all boards)** to:

- see active work across every project;
- group columns with the same workflow key;
- search titles, descriptions, IDs, boards, and harnesses;
- filter by runtime state or project;
- create and move tickets without changing their owning board when the target board has the matching workflow column.

Press `b` from the board view to switch between Master and a project board.

## Your tickets, your choice of backend

A board chooses one ticket backend when it is created:

- **Local** — private tickets stored by Kanbi on your machine.
- **GitHub Issues** — sync issue titles, descriptions, workflow labels, state, and comments.
- **Jira** — sync issue summaries, descriptions, statuses, and comments.

```bash
# Local board (the default)
kanbi boards add "Website" --cwd ~/code/website

# GitHub-backed board
kanbi boards add "Website Issues" \
  --cwd ~/code/website \
  --backend github \
  --config '{"owner":"ACME","repo":"website"}' \
  --query 'state=open,closed&labels=kanbi'

kanbi sync --board "Website Issues"
```

Provider credentials should be supplied through the documented environment variables, not embedded in `--config`, because board configuration is stored unencrypted. Runtime sessions and agent resume data always remain local and are never synced to GitHub or Jira.

See [ticket backends](./docs/ticket-backends.md) for GitHub and Jira setup.

## More than a TUI

Kanbi also has a scriptable CLI. Most non-interactive commands support `--json` or `--format json` with versioned response schemas.

```bash
kanbi boards --json
kanbi list --json
kanbi add "Investigate flaky checkout test" --body-file ./ticket.md --json
kanbi move T-001 --to "In Progress" --json
kanbi state --json
kanbi open T-001
```

When a ticket ID exists on more than one board, add `--board "Board Name"`.

## Data safety

Agent work can be expensive. Kanbi is deliberately conservative around active sessions and history:

- `q` exits the interface but leaves agents running; `x` explicitly closes a session.
- Starting fresh preserves previous session attempts.
- Archiving a board is the safe, non-destructive way to hide it.
- Hard deletion is permanent, local-only, strongly confirmed, and blocked while sessions are active.
- Full backups include Kanbi's database and attachments.

```bash
kanbi backup "$HOME/kanbi-backup-$(date +%Y%m%d).kanbi"
```

Create a backup before upgrades or destructive changes. Restoring requires every Kanbi process using the database to be stopped. See the [installation guide](./docs/installation.md#upgrade) for safe upgrade and rollback steps.

## Compatibility

| Area | Support |
| --- | --- |
| Platforms | Linux x86-64/ARM64; macOS Intel/Apple Silicon |
| Agent CLIs | Pi, Codex, GitHub Copilot CLI, Claude Code |
| Terminal runtime | tmux by default; Herdr opt-in |
| Ticket backends | Local, GitHub Issues, Jira |
| Terminal size | 80×24 minimum practical size; 256 colors recommended |
| Windows | Not currently a published target |

Agent resume depends on Kanbi capturing a verified session reference from the agent CLI. Upstream CLI changes can affect that integration; exact command and capture behavior is documented in [harness contracts](./docs/harness-contracts.md). See the full [compatibility matrix](./docs/compatibility.md) for verification status.

## Documentation

### Using Kanbi

- [Installation, first run, upgrades, and uninstall](./docs/installation.md)
- [GitHub and Jira ticket backends](./docs/ticket-backends.md)
- [Multi-board and Master behavior](./docs/multi-board-behavior.md)
- [Backup, diagnostics, and support bundles](./docs/support.md)
- [Compatibility matrix](./docs/compatibility.md)
- [tmux and Herdr runtime behavior](./docs/multiplexer-contracts.md)

### Contributing

- [Contributing guide](./CONTRIBUTING.md)
- [Architecture](./docs/architecture.md)
- [Agent contributor instructions](./AGENTS.md)
- [Changelog](./CHANGELOG.md)
- [Security policy](./SECURITY.md)

## Support and security

Start troubleshooting with:

```bash
kanbi version
kanbi doctor
kanbi support-bundle ~/kanbi-support.zip
```

Support bundles are redacted by default, but inspect the archive before sharing it. Report vulnerabilities privately using the [security policy](./SECURITY.md).

## License

Kanbi is available under the [MIT License](./LICENSE). Copyright © 2026 Carlo Tran.
