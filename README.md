# Kanbi

**A keyboard-first command center for coding agents.**

<p align="center">
  <a href="#what-it-supports"><img alt="Linux" src="https://img.shields.io/badge/Linux-supported-blue.svg" /></a>
  <a href="#what-it-supports"><img alt="macOS" src="https://img.shields.io/badge/macOS-supported-blue.svg" /></a>
  <a href="#what-it-supports"><img alt="Pi" src="https://img.shields.io/badge/Pi-supported-blueviolet.svg" /></a>
  <a href="#what-it-supports"><img alt="Codex" src="https://img.shields.io/badge/Codex-supported-blueviolet.svg" /></a>
  <a href="#what-it-supports"><img alt="GitHub Copilot CLI" src="https://img.shields.io/badge/GitHub_Copilot_CLI-supported-blueviolet.svg" /></a>
  <a href="#what-it-supports"><img alt="Claude Code" src="https://img.shields.io/badge/Claude_Code-supported-blueviolet.svg" /></a>
</p>

Kanbi puts Pi, Codex, Copilot, and Claude sessions on a Kanban board. Each ticket opens in its own terminal session, keeps its project context, and shows whether the agent is running, waiting for input, asking permission, or ready to resume.

Use separate boards for separate projects, or supervise everything from one Master view.

> Kanbi is beta software for Linux and macOS. It uses Herdr and requires at least one supported agent CLI.

## Why Kanbi?

- **One active agent session per ticket** — reopen a ticket without spawning duplicates.
- **See what needs attention** — responsive cards show compact agent state and, for worktrees, Git health at a glance.
- **Leave agents running** — exiting Kanbi and runtime attention detection do not terminate active sessions.
- **Keep projects isolated** — every board has its own working directory, with opt-in per-ticket Git worktrees for concurrent implementations.
- **Work across projects** — Master combines and filters all your boards.
- **Protect attention** — opt-in Global Focus Mode limits active commitments across every unarchived board and records a required handoff checkpoint before pausing work. Press `F` on the board to enable, disable, or configure it.
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
- [Herdr](https://herdr.dev/docs/install/) (tested with 0.9.3)
- Pi, Codex, GitHub Copilot CLI, or Claude Code, installed and authenticated

See [Installation](./docs/installation.md) for downloads, checksum verification, upgrades, and source builds.

## Quick start

```bash
cd /path/to/project
kanbi boards add "My Project" --cwd "$PWD"
kanbi
```

Select the board, press `n` to create a blank ticket, or press `N` to choose a board-scoped ticket template. Press `T` to create and manage templates. Template selection asks for the ticket-specific title before creating one ordinary ticket; on Master it first asks for the owning board. Templates are local Kanbi metadata—even on GitHub/Jira boards—and are copied by value, so later template changes never alter existing tickets. Save the ticket editor with `Ctrl+S`, then press `Enter` to start the agent. Single-line fields consistently support Left/Right, Home/End, Backspace/Delete, spaces, and insertion at the cursor. When a ticket already has a captured harness session ref, its ticket editor shows that ref as a tab-accessible field so it can be repointed after a bad capture or transient error. While editing a ticket body, `Ctrl+V` attaches PNG, JPEG, GIF, or WebP clipboard images and pastes text normally; pasted image file paths are copied into Kanbi's attachment storage. In the agent prompt, pasted images are labeled `[image 1]`, `[image 2]`, and so on in prompt order. In ticket bodies and notes, type `@` to find a project file, use Up/Down and Enter or Tab to select it, and Kanbi inserts a textual relative path such as `@internal/tui/model.go`; it does not expand or attach file contents. Search uses the existing ticket worktree when available, otherwise the board working directory, and skips common generated dependency/build directories.

Runtime refresh preserves unsaved edits and keeps the last valid board visible with a stale warning if observation fails. Navigation and refresh keep the viewport anchored; resizing fills newly available space. The notes tab shows the selected note and following notes; `j/k` moves through the thread, and saving an older note keeps it selected.

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
| Runtime | Herdr |
| Ticket backends | Local, GitHub Issues, Jira |
| Platforms | Linux x86-64/ARM64; macOS Intel/Apple Silicon |
| Automation | CLI commands with JSON output; optional installable [Agent Skill](./docs/agent-skill.md) |

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
- [Ticket templates](./docs/ticket-templates.md)
- [Multi-board behavior](./docs/multi-board-behavior.md)
- [Compatibility](./docs/compatibility.md)
- [Support](./docs/support.md)
- [Kanbi agent skill](./docs/agent-skill.md)
- [Contributing](./CONTRIBUTING.md)

## License

[MIT](./LICENSE) © 2026 Carlo Tran
