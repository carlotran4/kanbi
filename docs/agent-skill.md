# Kanbi agent skill

Kanbi includes an optional, standards-compatible Agent Skill for agents that need to inspect or administer Kanbi itself. The skill turns requests such as “show tickets needing attention” or “move T-014 to Review” into safe uses of Kanbi's JSON CLI.

The skill is useful because `kanbi --help` can describe syntax, but it cannot teach an agent Kanbi's operational rules: ticket IDs are board-local, runtime detection is advisory, session refs are private, and opening a ticket may start or resume another agent. The skill encodes those rules while keeping the CLI and SQLite database as the source of truth.

## Scope

The initial skill is deliberately instruction-only. It contains no executable scripts and grants no pre-approved tools.

It supports:

- board and ticket discovery;
- attention and runtime-state summaries;
- ticket inspection, creation, updates, and moves;
- ticket notes and on-demand provider sync;
- setup diagnosis when requested.

Read-only inspection is the default. Mutations require explicit user intent, and ambiguous targets require clarification. Session launch, destructive board operations, backup/restore, worktree policy changes, and repository integration are excluded from ordinary administration and require operation-specific confirmation.

Review [`skills/kanbi/SKILL.md`](../skills/kanbi/SKILL.md) before installing it. Skills are instructions to an agent and should be treated as executable trust, even when they contain no scripts.

## Install

The skill follows the [Agent Skills specification](https://agentskills.io/specification). From the Kanbi GitHub repository, an Agent Skills installer can discover and install it:

```bash
npx skills add carlotran4/kanbi --skill kanbi
```

To install manually from a source checkout, copy or symlink the `skills/kanbi` directory into a location recognized by the agent:

| Agent | User-level location |
| --- | --- |
| Pi | `~/.agents/skills/kanbi` or `~/.pi/agent/skills/kanbi` |
| Codex | `~/.agents/skills/kanbi` |
| GitHub Copilot CLI | `~/.agents/skills/kanbi` |
| Claude Code | `~/.claude/skills/kanbi` |

Example:

```bash
mkdir -p "$HOME/.agents/skills"
cp -R skills/kanbi "$HOME/.agents/skills/kanbi"
```

Claude Code uses its own user-level directory by default:

```bash
mkdir -p "$HOME/.claude/skills"
cp -R skills/kanbi "$HOME/.claude/skills/kanbi"
```

Restart the agent after installation if it does not refresh skills automatically. The `kanbi` binary must be installed and available on `PATH` in the agent's environment.

## Use

Most compatible agents load the skill automatically when a request mentions Kanbi administration. Explicit invocation varies by agent; in Pi, use:

```text
/skill:kanbi show tickets that need my attention
```

Example requests:

- “Summarize all Kanbi tickets waiting for me.”
- “Show T-331 on the Kanbi board, including its notes.”
- “Create a Kanbi ticket from this plan on the Client board using Pi.”
- “Move T-014 on Client to Review and add a progress note.”

The skill never makes unsupported direct database changes. If a workflow is available only in the TUI, the agent should direct the user there.

## Why this is optional

Kanbi remains fully usable without the skill. The TUI is the primary human interface and the CLI remains the stable automation interface. The skill is a thin, portable policy layer for natural-language agent use; it does not add a daemon, API, or alternate state store.

## Project UI validation skill

Contributors validating the actual board UI should use the separate [kanbi-ui-validation skill](../.pi/skills/kanbi-ui-validation/SKILL.md). It creates a disposable database and non-focused Herdr workspace, then exposes frame capture, keys and literal text input. It includes a tmux fallback for exact terminal-size checks. This development fixture is separate from the administration skill and never reads the user's canonical database or launches authenticated agents.
