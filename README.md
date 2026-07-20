# Kanbi

**A keyboard-first command center for coding agents.**

Kanbi puts Pi, Codex, Copilot, and Claude sessions on a Kanban board. Each ticket opens in its own terminal session, keeps its project context, and shows whether the agent is running, waiting for input, asking permission, or ready to resume.

Use separate boards for separate projects, or supervise everything from one Master view.

> Kanbi is beta software for Linux and macOS. It uses tmux by default and requires at least one supported agent CLI.

## Why Kanbi?

- **One active agent session per ticket** — reopen a ticket without spawning duplicates.
- **See what needs attention** — responsive cards show compact agent state and, for worktrees, Git health at a glance.
- **Leave agents running** — exiting Kanbi and runtime attention detection do not terminate active sessions.
- **Keep projects isolated** — every board has its own working directory, with opt-in per-ticket Git worktrees for concurrent implementations.
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

Select the board, press `n` to create a ticket, save it with `Ctrl+S`, then press `Enter` to start the agent. While editing a ticket body, `Ctrl+V` attaches PNG, JPEG, GIF, or WebP clipboard images and pastes text normally; pasted image file paths are copied into Kanbi's attachment storage. In the agent prompt, pasted images are labeled `[image 1]`, `[image 2]`, and so on in prompt order.

Choose `shared board directory` or `isolated Git worktrees` when creating a board. Existing boards can migrate with `b`, select the board, then `t` and confirm **Enable worktrees**. This is a durable board policy: once workspace history exists it cannot be switched back; create a separate shared-directory board instead. First ticket start asks for an editable branch name and records the currently checked-out branch as its source.

Press `I` on a named worktree board to select clean ticket worktrees and launch a repository integration agent. Reopen `I` to focus the agent, review a ready candidate with `p`, or cancel with `x`. Integration agents use `integration.harness` (default `pi`) and optional `integration.validation_command` from config. The agent reports a committed candidate through `kanbi integration report`; Kanbi independently verifies and promotes it rather than trusting transcript text or allowing the agent to mutate source.

The header status bar is configurable with Starship-style modules and independently aligned left, center, and right zones. It can show the board, time, or cached output from bounded custom commands such as a weekly usage script. See [Configuration](./docs/configuration.md).

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
- [Configuration](./docs/configuration.md)
- [Ticket backends](./docs/ticket-backends.md)
- [Multi-board behavior](./docs/multi-board-behavior.md)
- [Compatibility](./docs/compatibility.md)
- [Support](./docs/support.md)
- [Contributing](./CONTRIBUTING.md)

## License

[MIT](./LICENSE) © 2026 Carlo Tran
