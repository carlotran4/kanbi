# Ticket Backend Architecture

Agent Kanban supports one ticket metadata backend per board. The current implemented backend is `local`, which stores ticket metadata in SQLite. The architecture is prepared for external backends such as GitHub Issues, Atlassian/Jira, and Asana, but those adapters are intentionally not implemented yet.

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

- `ticket_backend`: backend kind, currently `local`; planned kinds include `github`, `atlassian`, and `asana`.
- `backend_query`: optional remote query/filter. For Atlassian/Jira this is JQL.
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

## Current Non-Goals

- No GitHub/Atlassian/Asana API calls are implemented yet.
- No backend migration for existing boards; backend is chosen at creation.
- No background daemon.
