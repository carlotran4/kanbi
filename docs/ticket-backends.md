# Ticket Backend Architecture

Kanbi supports one ticket metadata backend per board. Implemented backends are `local`, `github`, and `atlassian`. The `local` backend stores ticket metadata in SQLite. The `github` backend syncs a board with GitHub Issues through the GitHub REST API. The `atlassian` backend syncs a board with Jira issues through Atlassian's REST API. The architecture remains prepared for future Asana adapters.

## Core Rules

- A board chooses its ticket backend at creation time.
- Only one ticket backend is active for a board.
- SQLite remains the local cache/projection store for tickets and the canonical store for local runtime/session state.
- The implemented GitHub and Atlassian/Jira backends own ticket metadata for their boards.
- tmux session history remains local and is never synced to ticketing providers.
- Sync starts in the background on executable startup, then runs periodically while the executable is running, and on demand through `kanbi sync`. Startup does not block the TUI on remote/cloud ticket providers. There is no background daemon after Kanbi exits.
- Conflict resolution is newest `updated_at` wins.
- When a provider exposes a query language, board config stores the query used to scope the synced subset. Atlassian/Jira must use JQL.

## Data Model

Boards store backend metadata:

- `ticket_backend`: backend kind, currently `local`, `github`, or `atlassian`; planned kinds include `asana`.
- `backend_query`: optional remote query/filter. GitHub uses URL query parameters for the Issues list API (`state`, `labels`, `assignee`, `mentioned`, `milestone`, `since`). For Atlassian/Jira this must be JQL.
- `backend_config`: provider-specific JSON config such as site/repo/project identifiers. This value is stored unencrypted in SQLite; prefer environment variables for credentials and do not embed tokens unless the database is protected accordingly.
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

Run on-demand sync for all boards with `kanbi sync`, or one board with `kanbi sync --board "Board Name"`. Inside the TUI, successful ticket metadata saves trigger a background sync for that ticket's board: saving a newly-created ticket or edited ticket, moving/archive changes, and note/comment saves are pushed without waiting for the next periodic tick.

## GitHub Issues Backend

Create a GitHub-backed board with:

```bash
kanbi boards add "Repo" \
  --backend github \
  --config '{"owner":"OWNER","repo":"REPO"}' \
  --query 'state=open,closed&labels=kanbi'
```

Auth uses `KANBI_GITHUB_TOKEN`, `GITHUB_TOKEN`, or `gh auth token`; `token` in `backend_config` is also supported. Prefer the environment/CLI sources because `backend_config` is persisted unencrypted in SQLite. `owner`/`repo` may also come from `KANBI_GITHUB_OWNER` and `KANBI_GITHUB_REPO`.

GitHub sync behavior:

- Pulls issues selected by `backend_query` and projects them as local cached tickets with display IDs like `GH-42`. Queries should usually include a narrow label such as `labels=kanbi` (or a project/team label) plus `state=open,closed` when Kanbi should observe closed/reopened issues. Supported query parameters are GitHub Issues list API parameters: `state`, `labels`, `assignee`, `mentioned`, `milestone`, and `since`.
- Uses GitHub-native issue state for terminal work: closed issues appear visibly in the `Done` column by default, and moving a ticket to `Done` or `Closed` closes the GitHub issue. Only Kanbi's local archive action hides a ticket from the board. Open issues without a workflow label go to `Open`.
- Uses plain workflow labels for non-terminal columns by default: `in-progress` -> `In Progress`, `needs-review` -> `Review`, and `blocked` -> `Blocked`. Override these with `workflow_labels` in `backend_config`. Legacy `status:*` labels are still read during transition but are stripped on the next push.
- Pulls issue comments into ticket notes and pushes local notes as issue comments.
- Pushes local ticket title/body/closed state/status-label changes back to GitHub. Local tickets created on a GitHub board are temporary local placeholders until the next sync creates the remote issue; that same local row is then linked to the GitHub issue and its display ID changes from `T-*` to `GH-*`.
- Uses newest `updated_at` wins for ticket and comment conflicts. GitHub issue/comment `updated_at` can lag immediately after writes; repeated syncs are expected to be idempotent, and automation should tolerate eventual consistency by polling/retrying before declaring a mismatch.
- Follows GitHub pagination for issue and comment list pages (`per_page=100`) and includes rate-limit response headers in sync errors when GitHub returns them.
- Does not sync terminal containers, sessions, harness refs, runtime state, or other local session history.

Run the opt-in real GitHub smoke test with:

```bash
KANBI_GITHUB_OWNER="OWNER" \
KANBI_GITHUB_REPO="REPO" \
./scripts/github-backend-smoke.sh
```

Auth uses `KANBI_GITHUB_TOKEN`, `GITHUB_TOKEN`, or `gh auth token`. The token must be able to create/edit/close issues and comments in the configured repository. Use a disposable repository or a repository where temporary label-scoped smoke issues are acceptable. Troubleshooting: if `kanbi sync` reports `last_sync_error` or `sync-error`, check token scope, owner/repo spelling, query label filters, and GitHub rate-limit reset details in the error text.

## Atlassian/Jira Backend

Create a Jira-backed board with:

```bash
KANBI_JIRA_EMAIL="you@example.com" \
KANBI_JIRA_API_TOKEN="TOKEN" \
kanbi boards add "Jira" \
  --backend atlassian \
  --config '{"site_url":"https://ORG.atlassian.net","project_key":"AK"}' \
  --query 'project = AK AND labels = kanbi ORDER BY updated DESC'
```

Auth uses `KANBI_JIRA_EMAIL` plus `KANBI_JIRA_API_TOKEN`, or `KANBI_JIRA_BEARER_TOKEN`. `email` plus `api_token`, or `bearer_token`, in `backend_config` are also supported, but are stored unencrypted in SQLite and should be avoided when environment-based auth is available. Site/project values also fall back to `KANBI_JIRA_SITE_URL` and `KANBI_JIRA_PROJECT_KEY`.

Run the opt-in real Jira smoke test with:

```bash
KANBI_JIRA_SITE_URL="https://ORG.atlassian.net" \
KANBI_JIRA_PROJECT_KEY="AK" \
KANBI_JIRA_EMAIL="you@example.com" \
KANBI_JIRA_API_TOKEN="TOKEN" \
./scripts/jira-backend-smoke.sh
```

For bearer-token auth, set `KANBI_JIRA_BEARER_TOKEN` instead of `KANBI_JIRA_EMAIL`/`KANBI_JIRA_API_TOKEN`. The smoke test uses a disposable Kanbi config/data/state directory, creates a temporary Jira issue labeled with a unique smoke label, creates an `atlassian` board scoped to that label with JQL, verifies issue and comment pull sync, updates the pulled issue locally and verifies push sync, adds a local ticket plus note, verifies remote Jira issue creation and Jira comment creation, then best-effort cleans up created Jira issues. The Jira account needs Browse Projects, Create Issues, Edit Issues, Add Comments, and usually Transition Issues; Delete Issues is optional but enables full cleanup. If delete is not permitted, the script tries a `Done`/`Closed`/`Complete`/`Resolved` transition and may leave closed smoke issues behind.

Jira sync behavior:

- Treats board `BackendQuery` as JQL. If empty, it defaults to `project = <project_key> ORDER BY updated DESC`.
- Pulls issues selected by JQL and projects them as local cached tickets with display IDs matching Jira keys such as `AK-42`.
- Inherits workflow columns from remote issue status names. Local column changes are pushed by requesting a matching Jira transition when available.
- Pulls/pushes issue summary, description, status, and comments. Notes map to Jira issue comments.
- Uses newest `updated_at` wins for ticket and comment conflicts.
- Does not sync terminal containers, sessions, harness refs, runtime state, or other local session history.

## Current Non-Goals

- No Asana API calls are implemented yet.
- No backend migration for existing boards; backend is chosen at creation.
- No background daemon.
