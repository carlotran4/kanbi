# Ticket Backend Architecture

Agent Kanban supports one ticket metadata backend per board. Implemented backends are `local` and `github`. The `local` backend stores ticket metadata in SQLite. The `github` backend syncs a board with GitHub Issues through the GitHub REST API. The architecture remains prepared for future Atlassian/Jira and Asana adapters.

## Core Rules

- A board chooses its ticket backend at creation time.
- Only one ticket backend is active for a board.
- SQLite remains the local cache/projection store for tickets and the canonical store for local runtime/session state.
- External backends own ticket metadata for their boards once implemented.
- tmux session history remains local and is never synced to ticketing providers.
- Sync runs on executable startup and periodically while the executable is running. There is no background daemon after Agent Kanban exits.
- Conflict resolution is newest `updated_at` wins.
- When a provider exposes a query language, board config stores the query used to scope the synced subset. Atlassian/Jira must use JQL.

## Data Model

Boards store backend metadata:

- `ticket_backend`: backend kind, currently `local` or `github`; planned kinds include `atlassian` and `asana`.
- `backend_query`: optional remote query/filter. GitHub uses URL query parameters for the Issues list API (`state`, `labels`, `assignee`, `mentioned`, `milestone`, `since`). For Atlassian/Jira this must be JQL.
- `backend_config`: provider-specific JSON config such as site/repo/project identifiers.
- sync bookkeeping fields for last sync time/error.

Tickets and notes have optional external identity/version fields so adapters can map local cached rows to remote issues and comments.

## Sync Boundary

External adapters should sync all ticket metadata that can be represented locally, including:

- remote columns/statuses, inherited by the board rather than manually mapped;
- ticket title/body/status/archive-or-close state and provider fields represented by local metadata;
- notes as remote comments and remote comments as local notes;
- external URLs and update/version markers.

Local runtime/session data is out of scope for external ticket sync.

## Adapter Contract

Ticket backend adapters live behind `internal/ticketbackend.Backend`:

```go
type Backend interface {
    Kind() string
    Sync(ctx context.Context, store *storage.Store, board storage.Board) (Result, error)
}
```

A backend implementation should:

1. load its board-scoped config and query;
2. pull the remote board columns/statuses and make local columns mirror them;
3. compare local and remote tickets/comments by external IDs and update timestamps;
4. apply newest-updated-at-wins conflict resolution;
5. push local changes and pull remote changes;
6. persist external IDs, URLs, update timestamps, and sync versions.

## GitHub Issues Backend

Create a GitHub-backed board with:

```bash
agent-kanban boards add "Repo" \
  --backend github \
  --config '{"owner":"OWNER","repo":"REPO"}' \
  --query 'state=open,closed&labels=agent-kanban'
```

Auth uses `token` in `backend_config`, or `AGENT_KANBAN_GITHUB_TOKEN`, or `GITHUB_TOKEN`. `owner`/`repo` may also come from `AGENT_KANBAN_GITHUB_OWNER` and `AGENT_KANBAN_GITHUB_REPO`.

GitHub sync behavior:

- Pulls issues selected by `backend_query` and projects them as local cached tickets with display IDs like `GH-42`.
- Uses labels with `status:` prefix as workflow columns (`status:Review` -> `Review`). Open issues without a status label go to `Open`; closed issues go to `Closed`. These names can be changed in `backend_config` with `column_label_prefix`, `default_open_column`, and `closed_column`.
- Pulls issue comments into ticket notes and pushes local notes as issue comments.
- Pushes local ticket title/body/closed state/status-label changes back to GitHub. Local tickets created on a GitHub board are created as remote issues on the next sync.
- Uses newest `updated_at` wins for ticket and comment conflicts.
- Does not sync tmux windows, sessions, harness refs, runtime state, or other local session history.

## Current Non-Goals

- No Atlassian/Asana API calls are implemented yet.
- No backend migration for existing boards; backend is chosen at creation.
- No background daemon.
