# Kanbi

**A keyboard-first command center for coding agents.**

Kanbi puts Pi, Codex, Copilot, and Claude sessions on a Kanban board. Each ticket opens in its own terminal session, keeps its project context, and shows whether the agent is running, waiting for input, asking permission, or ready to resume.

Use separate boards for separate projects, or supervise everything from one Master view.

> Kanbi is beta software for Linux and macOS. It uses tmux by default and requires at least one supported agent CLI.

## Why Kanbi?

- **One active agent session per ticket** — reopen a ticket without spawning duplicates.
- **See what needs attention** — runtime state is visible directly on each card.
- **Leave agents running** — exiting Kanbi does not terminate active sessions.
- **Keep projects isolated** — every board has its own working directory.
- **Work across projects** — Master combines and filters all your boards.
- **Use your own tickets** — work locally or sync with GitHub Issues and Jira.

## Install

Download the archive for your platform and `SHA256SUMS` from [GitHub Releases](https://github.com/carlotran4/kanbi/releases). Kanbi is currently distributed as a prerelease, so choose the release explicitly.

```bash
mkdir -p "$HOME/.local/bin"
install -m 0755 kanbi "$HOME/.local/bin/kanbi"
export PATH="$HOME/.local/bin:$PATH"

kanbi doctor
```

Requirements:

- Linux or macOS
- tmux
- Pi, Codex, GitHub Copilot CLI, or Claude Code, installed and authenticated

See [Installation](./docs/installation.md) for downloads, checksum verification, upgrades, and source builds.

## Quick start

```bash
cd /path/to/project
kanbi boards add "My Project" --cwd "$PWD"
kanbi
```

Select the board, press `n` to create a ticket, save it with `Ctrl+S`, then press `Enter` to start the agent. While editing a ticket body, `Ctrl+V` attaches PNG, JPEG, GIF, or WebP clipboard images and pastes text normally; pasted image file paths are copied into Kanbi's attachment storage.

Press `?` for controls. The important distinction is:

- `x` closes the selected agent session.
- `q` exits Kanbi but leaves agents running.

## What it supports

| | |
| --- | --- |
| Agent CLIs | Pi, Codex, GitHub Copilot CLI, Claude Code |
| Runtime | tmux by default; Herdr opt-in |
| Ticket backends | Local, GitHub Issues, Jira |
| Platforms | Linux x86-64/ARM64; macOS Intel/Apple Silicon |
| Automation | CLI commands with JSON output |

Session resume depends on Kanbi capturing a verified session reference from the agent CLI. If a session cannot be resumed safely, Kanbi offers repair or start-fresh instead of guessing.

## Useful commands

```bash
kanbi                    # open the TUI
kanbi doctor             # check your setup
kanbi boards             # list boards
kanbi list --json        # inspect tickets from scripts
kanbi sync               # sync provider-backed boards
kanbi backup backup.kanbi
```

Run `kanbi --help` or `kanbi COMMAND --help` for the rest.

## Documentation

- [Installation](./docs/installation.md)
- [Ticket backends](./docs/ticket-backends.md)
- [Multi-board behavior](./docs/multi-board-behavior.md)
- [Compatibility](./docs/compatibility.md)
- [Support](./docs/support.md)
- [Contributing](./CONTRIBUTING.md)

## License

[MIT](./LICENSE) © 2026 Carlo Tran
