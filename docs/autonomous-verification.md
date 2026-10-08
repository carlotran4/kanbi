# Autonomous Verification Guide

This document defines how an autonomous coding agent should verify its own work while building Kanbi.

## Verification Principle

Every change should be independently checkable by the agent without relying on subjective human review. Prefer fast, local, deterministic checks.

The agent should not claim completion unless it has run the relevant verification commands and recorded the result.

## Baseline Commands

Run these before considering any implementation task complete:

```bash
go fmt ./...
go test ./...
go vet ./...
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
- migration idempotency and rollback/recovery
- concurrent initialization against one file database
- foreign-key integrity rejection
- busy-timeout behavior for concurrent writers
- WAL configuration for file databases

Tests should use a temporary directory and temporary SQLite DB. Migration tests must exercise both a pre-ledger database and repeated initialization of the current schema; they must not rewrite or discard durable history.

### 3. Fake Harnesses
Do not rely on real `pi`, `codex`, `copilot`, or `claude` in automated tests.

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

### 4. Tmux Manager And Real-Tmux Tests

Deterministic manager tests in `internal/tmux` use injected fake command runners and belong in the normal `go test ./...` suite. They verify command construction, lifecycle decisions, stored-container routing, and failure handling without requiring a tmux binary or live server.

Tests that execute the real `tmux` binary are environment-dependent integration tests. Keep them clearly identified and make them skip when tmux is unavailable; they may run during `go test ./...` on hosts with tmux. Real-tmux tests should:

- create a uniquely named test tmux session, e.g. `kanbi-test-$PID`
- register cleanup immediately after creating the session
- create a board window and isolated ticket windows
- send/paste text into panes and capture pane output
- remove only their own session during cleanup

`scripts/smoke.sh` is the required real-tmux end-to-end check. It creates a unique temporary session, uses fake harnesses, and cleans up on exit. Do not describe an environment variable or build tag as supported unless the test implementation actually checks it.

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

### 6. Interactive TUI Validation

For TUI/UI, layout, scrolling, modal, readability, or keybinding changes, use the project-scoped `kanbi-ui-validation` skill after model tests. Its helper builds current source, seeds a disposable multi-board database, and launches the real Bubble Tea application on a private tmux socket.

Drive the UI one keypress at a time and capture frames before and after meaningful transitions. Validate the reported terminal size plus 80x24, and exercise Master, a named board, overflow, relevant modals, and resize behavior. Confirm the application header and footer remain visible and the focused control stays on-screen. Never copy or open the user's canonical database: the helper accepts no database path and only launches its deterministic local-backend, sync-disabled `kanbi-ui-test.db` fixture.

Interactive validation supplements rather than replaces a deterministic regression test.

### 7. Doctor Self-Test
`kanbi doctor` should have testable internals.

Separate probe logic from output formatting so tests can simulate:

- tmux missing
- configured multiplexer (`tmux` default, optional `herdr`)
- missing/unreachable configured Herdr binary
- missing optional harness
- unwritable config dir
- unwritable DB dir
- inside tmux vs outside tmux

Expected behavior:

- configured multiplexer is displayed
- missing tmux: fatal doctor failure for existing/default tmux runtime checks
- selected Herdr missing or `herdr status` unavailable: warning with setup guidance
- DB/config path unavailable: fatal doctor failure
- a missing configured harness start binary (including `pi`, `codex`, `copilot`, or `claude` defaults): warning only

### 8. Manual Smoke Test Script
Maintain a script such as:

```bash
./scripts/smoke.sh
```

By default, the smoke script should run baseline checks plus the tmux-backed end-to-end path and deterministic fake-multiplexer probes:

```bash
go fmt ./...
go test ./...
go vet ./...
kanbi doctor
# with a temporary config, also run kanbi doctor against a fake Herdr binary
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
- Claude start/resume command is correct
- `{session_ref}` substitution works
- command path override works

### Runtime Detection
A change is verified when fake outputs produce expected states:

- active output -> `running`
- known user prompt -> `waiting_for_user`
- known approval prompt -> `needs_permission`
- no output past threshold -> `idle_unknown`
- runtime detection never closes the terminal container
- manual override is later replaceable by confident detection

### Runtime Refresh Safety
A change is verified when:

- `waiting_for_user` remains active until explicitly closed
- `needs_permission` remains active until explicitly closed
- stale attention text cannot cause runtime refresh to close a session
- `idle_unknown` and `running` remain active
- explicit graceful close timeout is handled
- an explicitly closed session becomes `closed` and remains resumable

### Multiplexers

A multiplexer change is verified when:

- config defaults still select `tmux`
- `multiplexer.default: herdr` is accepted without changing harness config
- doctor reports the configured multiplexer
- doctor preserves tmux checks
- fake Herdr scripts can simulate `herdr status` in deterministic tests/smoke
- real Herdr lifecycle checks are opt-in only and not run by normal smoke

### Repository Integration Runs

A change is verified when temporary Git repositories prove selection snapshots exact source/item SHAs, integration agents launch only in the managed candidate checkout, token-authenticated reports reject dirty/wrong/incomplete candidates, promotion closes agents and revalidates clean ticket/source worktrees, source updates once by fast-forward, crash-after-fast-forward reconciliation is idempotent, and cancellation never mutates ticket worktrees or source. TUI validation must cover `I` selection, disabled dirty rows, waiting/permission notice, ready detail, promotion confirmation, cancellation confirmation, scrolling, and 80x24.

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
- adding a background daemon (in-process Start/Stop sync loops are not a daemon)
- adding a web UI
- removing tmux as the default v1 backend
- making real harness behavior assumptions that cannot be simulated or verified
- deleting ticket/session history
- introducing external services
- changing the command names for supported harnesses

## Opt-In Real Multiplexer Checks

Real Herdr checks are not part of normal smoke or CI because they depend on a locally installed Herdr server/session and can alter the user's workspaces. To verify manually, install Herdr, run `herdr` once, configure a disposable Kanbi config with `multiplexer.default: herdr`, run `kanbi doctor`, then open a disposable ticket and confirm the Herdr pane/agent is created and focusable.

## Opt-In Real Backend Smoke Tests

Real ticket backend smoke tests are not part of normal smoke or CI because they can consume quota, mutate external projects, and depend on auth. For GitHub, use a disposable repository or a repository where temporary label-scoped issues are acceptable, then run:

```bash
KANBI_GITHUB_OWNER="OWNER" \
KANBI_GITHUB_REPO="REPO" \
./scripts/github-backend-smoke.sh
```

The script uses `KANBI_GITHUB_TOKEN`, `GITHUB_TOKEN`, or `gh auth token`.

The GitHub smoke test verifies auth, query filtering, issue pull, remote edit/comment observation, repeat sync idempotency, close projection, and best-effort cleanup against a real repository. Deterministic fake-client tests remain the source of truth for conflict/comment update edge cases.

For Jira/Atlassian, set a disposable-capable project and auth in the environment, then run:

```bash
KANBI_JIRA_SITE_URL="https://ORG.atlassian.net" \
KANBI_JIRA_PROJECT_KEY="AK" \
KANBI_JIRA_EMAIL="you@example.com" \
KANBI_JIRA_API_TOKEN="TOKEN" \
./scripts/jira-backend-smoke.sh
```

The Jira smoke test verifies issue pull, remote comment pull, local update push, local issue creation push, and local note/comment push against a real project. Cleanup is best-effort: Delete Issues permission removes smoke issues; otherwise the script tries to transition them to a terminal status and reports any leftovers.

## Runtime Hardening / Soak

After lifecycle, storage, or provider sync changes, prefer the Milestone-4 matrix:

```bash
go fmt ./...
go test ./...
go test -race ./...
go vet ./...
govulncheck ./...   # if installed
./scripts/smoke.sh --skip-checks
go test ./internal/harness ./internal/tmux ./internal/storage ./internal/ticketbackend
```

Deterministic failure-injection coverage for sync ownership lives in `internal/ticketbackend` (stop drain, scheduling-storm coalescing, lease renew/loss, pending create recover, GET retries, create-once). Optional longer soak (default 60s, race on, temp dirs only):

```bash
KANBI_SOAK_SECONDS=60 ./scripts/soak-runtime.sh
```

`KANBI_SOAK=1` enables the soak test path. No real providers or network are used.

## Support Bundle And Diagnostics Verification

When changing diagnostics or support bundles:

```bash
go test ./internal/diagnostics ./internal/storage
```

Confirm:

- logging stays off by default and creates `0600` files when enabled;
- redaction tests cover tokens, auth headers, session refs, and config secrets;
- support bundles work when the database/multiplexer is unavailable;
- archives reject path traversal entries and exclude ticket bodies/notes by construction.

Manual inspect after generating a bundle:

```bash
kanbi support-bundle /tmp/kanbi-support.zip
unzip -l /tmp/kanbi-support.zip
unzip -p /tmp/kanbi-support.zip support-bundle.json | head
```

## Upgrade / Rollback Drill

Schema or packaging changes should run:

```bash
./scripts/upgrade-rollback-drill.sh
```

Optional real artifact comparison:

```bash
OLD_ARCHIVE=/path/to/old.tar.gz NEW_ARCHIVE=/path/to/new.tar.gz ./scripts/upgrade-rollback-drill.sh
```

The script records a timestamped result under ignored `dist/verification/` and updates a local `upgrade-rollback-drill-latest.txt`. Attach relevant output to the release issue or workflow run; do not commit generated verification logs. With explicit old/new archives it rejects identical commits or schema versions and verifies required attachment data, active/inactive session history, downgrade rejection, SQLite/foreign-key integrity, and rollback removal of post-backup changes.

## CI And Release Verification

GitHub Actions runs formatting, unit/integration tests, the race detector, vet, vulnerability analysis, and an isolated Linux tmux smoke job. Keep deterministic tmux manager tests in the normal CI suite; real harness/provider checks remain opt-in. The scheduled daily and manually dispatched CI workflow runs `scripts/soak-runtime.sh` for 60 seconds with the race detector; its fake provider and temporary databases require no credentials and leave no external resources.

CI and release jobs select the latest available Go 1.26 patch release while `go.mod` records the minimum supported patch. Dependabot checks Go modules and pinned GitHub Actions weekly. This keeps standard-library security fixes flowing into builds without automatically adopting a new Go minor release.

Supported release tags (`vMAJOR.MINOR.PATCH`, `vMAJOR.MINOR.PATCH-beta.N`, and `vMAJOR.MINOR.PATCH-rc.N`) trigger `.github/workflows/release.yml`. All `v0.x` and suffixed tags publish as prereleases; stable major versions remain workflow-locked until stable qualification is approved. Release builds use native GitHub runners because `go-sqlite3` requires CGO; the supported matrix is Linux and macOS on amd64 and arm64. Every native job validates `BUILDINFO.json`, database initialization, backup/restore, and the fake-harness tmux lifecycle against the exact built binary. The bundle job requires four archives, verifies a shared `SHA256SUMS`, and records `BUNDLE_MANIFEST.txt`; workflow dispatch produces the same combined bundle without publishing. Before tagging, test the native local artifact path with:

```bash
./scripts/build-release.sh 0.3.0-beta.1
archive=./dist/kanbi_0.3.0-beta.1_$(go env GOOS)_$(go env GOARCH).tar.gz
tar -tzf "$archive"
tmp=$(mktemp -d) && tar -xzf "$archive" -C "$tmp"
./scripts/release-artifact-smoke.sh "$tmp/kanbi" 0.3.0-beta.1 "$(git rev-parse HEAD)" "$tmp/BUILDINFO.json"
```

After extracting the snapshot, verify `kanbi version` reports the supplied version, commit, UTC build date, runtime platform, and current database schema, and that `BUILDINFO.json` matches. Release notes and upgrade-impacting changes belong in `CHANGELOG.md`. Channel policy: [`docs/release-channels.md`](./release-channels.md). Release and stable gates: [`docs/release-checklist.md`](./release-checklist.md).

## Performance issue #358

The opt-in measurement sources in `internal/tui/performance*_test.go` compile on both the PR base and candidate. The Performance evidence workflow runs both on the same Linux runner, then runs the full suite, race detector, vet, smoke, and `python3 scripts/performance-ui.py`. That driver uses the project `kanbi-ui-validation` helper's isolated fixture and captures/checks each meaningful input at 160x40 and 80x24: Master and named boards, vertical/horizontal overflow, help/filters, resize, unsaved title drafts across ticks, 50 notes with edit/add/delete, a 64 KiB Unicode/image body with edit/save, and a real SQLite writer across a polling tick. It stops the disposable tmux session in a `finally` block.

```bash
KANBI_PERFORMANCE=1 KANBI_PERFORMANCE_TMUX=1 go test -v ./internal/tui -run '^TestPerformance(Evidence|Program|External)$' -count=1
python3 scripts/performance-ui.py
```

CPU/service samples report p50/p95/p99, allocations, and bytes allocated. The real Bubble Tea program test uses production inline mode at its default 60 FPS and records reader receipt to the writer's changed selection frame while observation is deliberately delayed 300 ms. It also reports terminal bytes/writes per trial. Its output is a measurement sink; physical terminal painting is not measured. Actual tmux and Git fixtures are labeled separately. The 10-worktree case reports a first uncached pass and a warm polling burst within the five-second cache lifetime. Use `KANBI_PERFORMANCE_ENFORCE=1` only on the candidate to enforce the 50 ms program p95 goal; the baseline intentionally fails it. Opt-in tests skip normally and do not introduce timing gates into deterministic CI.

Performance evidence and remaining limits are recorded in the PR for #358. Repeat measurements on the user's WSL/terminal before claiming physical input-to-paint latency. Bubble Tea v2, synchronized-output negotiation, alternate-screen changes, stable-height preview layout, and a revision/data-version board projection cache remain separate experiments; this change retains inline mode and 60 FPS.
