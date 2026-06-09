# Autonomous Verification Guide

This document defines how an autonomous coding agent should verify its own work while building Kanbi.

## Verification Principle

Every change should be independently checkable by the agent without relying on subjective human review. Prefer fast, local, deterministic checks.

The agent should not claim completion unless it has run the relevant verification commands and recorded the result.

## Baseline Commands

Run these before considering any implementation task complete:

```bash
go test ./...
go vet ./...
go fmt ./...
```

If linting is later added, also run:

```bash
golangci-lint run
```

## Required Verification Layers

### 1. Unit Tests
Use unit tests for pure logic:

- config loading/defaults
- XDG path resolution
- YAML parsing
- SQLite migrations
- ticket display ID generation
- column ordering
- ticket movement/reordering
- prompt template rendering
- runtime state transitions
- timeout calculations
- harness command construction
- output pattern detection

Unit tests must not require tmux, real harness CLIs, or a real terminal.

### 2. Integration Tests With Temporary SQLite DB
Use integration tests for DB-backed behavior:

- initial schema creation
- default board/columns creation
- create/list/update/archive tickets
- create sessions and mark active/inactive
- startup reconciliation state changes
- migration idempotency

Tests should use a temporary directory and temporary SQLite DB.

### 3. Fake Harnesses
Do not rely on real `pi`, `codex`, or `gh copilot` in automated tests.

Create fake harness binaries/scripts in temporary directories that simulate:

- normal startup
- prompt-ready output
- session ref emission
- resume success
- resume failure
- permission prompt
- waiting-for-user prompt
- silent/idle command
- graceful exit

The test should prepend the fake binary directory to `PATH`.

### 4. Tmux Integration Tests
Tmux-dependent tests should be separate from normal unit tests because they require the environment to have tmux installed.

Use a build tag or explicit environment variable, e.g.:

```bash
KANBI_TMUX_TESTS=1 go test ./internal/tmux ./internal/harness
```

Tmux integration tests should:

- create a uniquely named test tmux session, e.g. `kanbi-test-$PID`
- create a board window
- create ticket windows
- send/paste text into panes
- capture pane output
- gracefully kill the test session in cleanup

Every tmux test must register cleanup immediately after creating a session.

### 5. TUI Model Tests
Bubble Tea apps can be tested at the model/update level without rendering a real terminal.

Test:

- keybindings move focus correctly
- tickets move between columns
- tickets reorder within columns
- archive removes from visible board
- attention cycling jumps to the right cards
- runtime state messages update cards
- error messages appear in model state

Avoid screenshot/golden terminal tests in early v1 unless necessary.

### 6. Doctor Self-Test
`kanbi doctor` should have testable internals.

Separate probe logic from output formatting so tests can simulate:

- tmux missing
- missing optional harness
- unwritable config dir
- unwritable DB dir
- inside tmux vs outside tmux

Expected behavior:

- missing tmux: fatal doctor failure
- DB/config path unavailable: fatal doctor failure
- missing `pi`, `codex`, or `gh`: warning only

### 7. Manual Smoke Test Script
Maintain a script such as:

```bash
./scripts/smoke.sh
```

By default, the smoke script should run baseline checks plus the tmux-backed end-to-end path:

```bash
go fmt ./...
go vet ./...
go test ./...
kanbi doctor
kanbi add "Smoke test ticket" --body "Verify smoke path" --harness pi
kanbi list
```

When the current verification loop has already run `go fmt ./...`, `go test ./...`, and `go vet ./...`, use the faster end-to-end-only mode to avoid duplicate baseline work:

```bash
./scripts/smoke.sh --skip-checks
```

For early development, the script may use a temporary config/DB via env vars:

```bash
KANBI_DB=$(mktemp -d)/test.db
KANBI_CONFIG=$(mktemp -d)/config.yaml
```

## Feature-Specific Acceptance Checks

### Config/XDG
A change is verified when:

- defaults load with no config file
- config file overrides defaults
- env vars override XDG paths
- invalid YAML produces a clear error
- missing config dirs are created when appropriate

### SQLite/Data Model
A change is verified when:

- migrations run on empty DB
- migrations are idempotent
- default board exists after init
- default columns are created exactly once
- ticket display IDs increment correctly
- archived tickets are hidden from normal board queries

### Ticket Movement
A change is verified when:

- moving left/right updates `column_id`
- moving up/down updates position
- positions remain contiguous after reorder
- moving from any column is allowed
- deleting non-empty columns is rejected

### Prompt Rendering
A change is verified when:

- title/body ticket renders as:

```md
# T-001: Title

Body
```

- empty body renders as:

```md
# T-001: Title

Title
```

- multiline bodies are preserved exactly

### Harness Command Construction
A change is verified when:

- Pi start/resume command is correct
- Codex start/resume command is correct
- Copilot start/resume command is correct
- `{session_ref}` substitution works
- command path override works

### Runtime Detection
A change is verified when fake outputs produce expected states:

- active output -> `running`
- known user prompt -> `waiting_for_user`
- known approval prompt -> `needs_permission`
- no output past threshold -> `idle_unknown`
- unknown idle state is not auto-close eligible
- manual override is later replaceable by confident detection

### Auto-Close
A change is verified when:

- `waiting_for_user` past timeout triggers graceful close
- `needs_permission` past timeout triggers graceful close
- `idle_unknown` does not auto-close
- `running` does not auto-close
- graceful close timeout is handled
- session becomes `closed` and remains resumable

### Tmux Manager
A change is verified when:

- dedicated session is created if missing
- board window exists
- ticket window names include display ID and slug
- title changes rename tmux window only
- missing windows are detected on reconcile
- cleanup removes only managed test/session resources

### TUI
A change is verified when:

- board loads from DB
- default columns render in order
- card display includes ID, title, harness, indicator, state/time
- attention states highlight/flash in model state
- keybindings update model as expected
- actions call service interfaces, not shell commands directly

## Autonomous Agent Completion Checklist

For every task, the agent should report:

1. Files changed
2. Behavior implemented
3. Verification commands run
4. Test results
5. Any known gaps or unverified assumptions

Template:

```md
## Verification
- Ran `go fmt ./...`: pass
- Ran `go test ./...`: pass
- Ran `go vet ./...`: pass
- Added/updated tests: yes/no
- Manual smoke test: pass/skip, reason

## Notes
- Known limitation: ...
```

## When the Agent Must Stop and Ask

An autonomous agent should stop and ask before:

- changing the product scope documented in current source-of-truth docs
- adding a background daemon
- adding a web UI
- replacing tmux as v1 backend
- making real harness behavior assumptions that cannot be simulated or verified
- deleting ticket/session history
- introducing external services
- changing the command names for supported harnesses

## Opt-In Real Backend Smoke Tests

Real ticket backend smoke tests are not part of normal smoke or CI because they can consume quota, mutate external projects, and depend on auth. For Jira/Atlassian, set a disposable-capable project and auth in the environment, then run:

```bash
KANBI_JIRA_SITE_URL="https://ORG.atlassian.net" \
KANBI_JIRA_PROJECT_KEY="AK" \
KANBI_JIRA_EMAIL="you@example.com" \
KANBI_JIRA_API_TOKEN="TOKEN" \
./scripts/jira-backend-smoke.sh
```

The Jira smoke test verifies issue pull, remote comment pull, local update push, local issue creation push, and local note/comment push against a real project. Cleanup is best-effort: Delete Issues permission removes smoke issues; otherwise the script tries to transition them to a terminal status and reports any leftovers.

## Recommended CI Later

When the repo is ready for CI, add GitHub Actions that run:

```bash
go fmt ./...
go vet ./...
go test ./...
```

Tmux integration tests should be optional or run only on Linux CI with tmux installed.
