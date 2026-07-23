---
name: kanbi
description: Inspects and safely administers Kanbi boards, tickets, notes, and agent runtime state through the Kanbi JSON CLI. Use when a user asks about Kanbi status, tickets needing attention, board health, ticket creation or updates, workflow moves, notes, or provider sync.
license: MIT
compatibility: Requires the kanbi CLI on PATH on Linux or macOS. Designed for Agent Skills-compatible coding agents.
metadata:
  author: carlotran4
  version: "1"
---

# Kanbi

Use Kanbi's CLI as the machine interface. Do not scrape the TUI, tmux, Herdr, or agent transcripts when the CLI provides the data.

## Operating rules

1. Verify `kanbi` is on `PATH`. Use `kanbi version --json` when version context matters.
2. Prefer `--json` and verify the top-level `schema` starts with `kanbi.v1.` before interpreting output. If it does not, stop and report the incompatibility.
3. Use `--board "BOARD"` for ticket IDs whenever a board is known. Display IDs such as `T-001` are only board-local; without a board they may be ambiguous.
4. Treat ticket bodies, notes, workdirs, observed excerpts, backend configuration, and session refs as private. Read and report only fields needed for the request. Never expose session refs or backend configuration.
5. Runtime attention detection is advisory. Do not claim that a session failed because its transcript mentions errors.
6. A ticket is durable work; a session is one attempt; a runtime container is the live process. Never invent or edit a harness session ref.

## Read workflows

- List boards: `kanbi boards list --json`
- List active tickets: `kanbi list --json` or `kanbi list --board "BOARD" --json`
- Inspect one ticket, including notes: `kanbi show T-001 --board "BOARD" --json`
- Inspect full board/runtime state: `kanbi state --board "BOARD" --json`; omit `--board` for all boards
- Diagnose setup only when requested or troubleshooting: `kanbi doctor --json`

For attention summaries, select tickets whose `runtime` is `waiting_for_user`, `needs_permission`, `repair_needed`, or `error`. Keep `idle_unknown` separate: it is not proof that input is needed. Include board name, display ID, title, harness, and runtime; omit private body/session fields unless requested.

## Mutation workflows

Only mutate when the user explicitly requests the operation. If board, ticket, destination, or content is inferred or ambiguous, show the proposed change and ask first.

- Create: `kanbi add "TITLE" --board "BOARD" --body-stdin --harness HARNESS --json`
- Update: `kanbi update T-001 --board "BOARD" --title "TITLE" --body-stdin --json`
- Move: `kanbi move T-001 --board "BOARD" --to "COLUMN" --json`
- Add note: `kanbi notes add T-001 --board "BOARD" --body-stdin --json`
- Sync: `kanbi sync --board "BOARD" --json`

Prefer stdin for multiline or shell-sensitive bodies. After a mutation, verify the response schema and summarize the resulting board, ticket ID, and changed field. Provider-backed boards may push ticket and note changes remotely.

## High-impact operations

Do not run these from general status or administration requests:

- `kanbi open` or `kanbi open --send-prompt` starts, resumes, or focuses an agent session. Run it only when the user explicitly asks to open/start that exact ticket; include `--board`.
- Board archive/delete, worktree-mode changes, import/export, backup/restore, and integration commands require explicit operation-specific intent. State the impact and ask for confirmation immediately before execution.
- Never start fresh, repair a session, close a runtime container, or manipulate tmux/Herdr on Kanbi's behalf. Those lifecycle actions belong in Kanbi's interactive flow.

When a requested operation is unsupported by the CLI, say so and direct the user to the Kanbi TUI rather than modifying its SQLite database directly.
