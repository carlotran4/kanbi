# Ticket Templates

Ticket templates are reusable, board-scoped defaults for ordinary Kanbi tickets.
They are local Kanbi metadata, including on GitHub- and Jira-backed boards.

## Template model

A template stores:

- a name, unique without regard to case within its board;
- an optional initial ticket title;
- a literal Markdown body;
- a supported harness.

Applying a template copies its current values into a new ticket. The ticket is
not linked back to the template: later template edits or deletion never change
existing tickets. The focused column chooses the destination; templates do not
store workflow columns, sessions, notes, attachments, workspaces, or provider
fields.

## TUI interactions

- `n` keeps the fast blank-ticket path.
- `N` opens the template picker for the focused column.
- `T` opens the template manager.

The picker supports typing to filter, Up/Down or `j`/`k` to select, Enter to
continue, and Escape to cancel without creating a ticket. After selection,
Kanbi asks for the ticket-specific title, prefilled from the template. Enter
creates exactly one ordinary ticket and opens the normal ticket editor.

On Master, `N` first asks for the real board, resolves the focused synthetic
workflow key on that board, and then shows only that board's templates. `T`
first asks which real board to manage.

The manager supports `c` create, `e` edit, `d` confirmed delete, and Enter to
use a template when opened from a named board. The editor uses Tab/Shift+Tab,
Ctrl+S, and Escape. Template bodies are text-only: ticket attachment paste is
not available because no ticket owns the template.

## CLI

```bash
kanbi templates list --board Repo
kanbi templates add "Bug report" --board Repo --title "Bug:" --body-file bug.md --harness pi
kanbi templates update "Bug report" --board Repo --body-file revised.md
kanbi templates delete "Bug report" --board Repo
kanbi add "Bug: resize failure" --board Repo --template "Bug report"
```

For `kanbi add --template`, explicit title, body, and harness values override
the template. Without an explicit title, the template title is used; if that is
blank, the template name becomes the ticket title.

## Provider-backed boards

Template CRUD never schedules provider sync. Applying a template creates one
normal ticket and schedules the same owning-board sync as ordinary creation.
GitHub or Jira receives only the resulting ticket metadata, never the template
or its provenance. This avoids intermediate placeholder issues.

Templates do not automatically follow a GitHub-backed board to another
computer. Full Kanbi backup/restore transfers them, and single-board package
export/import preserves them when creating a new imported board. Repository-
backed shared templates remain a possible follow-up.

## Portability

Board packages include templates as an optional backward-compatible payload.
Older packages without templates continue to import. Imported templates belong
to the newly created archived, sync-disabled board. Full backups preserve the
SQLite table automatically.

## Non-goals

- global or inherited templates;
- variables, scripts, conditionals, or date expansion;
- GitHub Issue Forms or Jira issue-type synchronization;
- template-selected columns or provider statuses;
- attachments, notes, sessions, branches, or workspaces;
- applying templates to existing tickets;
- updating previously-created tickets from a template;
- importing repository `.github/ISSUE_TEMPLATE` files.
