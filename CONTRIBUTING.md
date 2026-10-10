# Contributing to Kanbi

Thanks for helping improve Kanbi. This guide covers development setup, quality bar, and how to propose changes.

## Before you start

1. Read [`AGENTS.md`](./AGENTS.md) and the current source-of-truth docs it lists (`docs/architecture.md`, state/lifecycle/harness/multiplexer docs).
2. Prefer small, reviewable changes that match existing package boundaries.
3. Do not expand product scope, flatten history, introduce a background daemon, or change runtime ownership without maintainer agreement.

## Development setup

```bash
git clone https://github.com/carlotran4/kanbi.git
cd kanbi
go test ./...
./scripts/install-dev.sh   # optional: PATH launcher that rebuilds on change
```

Requirements: Go version from `go.mod`, a C compiler (CGO / `go-sqlite3`), and Herdr, Python 3 and sqlite3 for smoke tests.

## Coding standards

- Keep SQLite as the durable source of truth; do not invent parallel state stores.
- Preserve ticket/session history invariants listed in the architecture docs.
- Redact secrets in diagnostics; never log prompts, session refs, or terminal excerpts.
- Update the closest user-facing or contract doc in the same change when behavior shifts.
- Prefer deterministic tests and fake harnesses; real provider/harness checks stay opt-in.

## Verification

Before opening a PR for meaningful code changes:

```bash
go fmt ./...
go test ./...
go vet ./...
./scripts/smoke.sh --skip-checks
```

Also run focused packages when you touch them (examples):

```bash
go test ./internal/harness ./internal/runtime ./internal/storage
go test ./internal/diagnostics
```

For lifecycle/runtime hardening, prefer:

```bash
go test -race ./...
```

Use plain `./scripts/smoke.sh` if you want the script to run fmt/test/vet itself.

## Pull requests

- Describe the user-visible change and linked issue when applicable.
- List verification commands and results in the PR body.
- Keep commits focused; do not sweep unrelated tree drift.
- Add or update tests for bug fixes and new behavior.
- For release-impacting work (schema, install, support tools), update `CHANGELOG.md` under Unreleased.

## Issue reports

Use the GitHub issue templates. Bug reports should include:

- `kanbi version` output
- OS/arch and install method
- Steps to reproduce
- Expected vs actual behavior
- `kanbi doctor` output (redact secrets)
- Optional path to a reviewed `kanbi support-bundle` archive

Security issues: see [`SECURITY.md`](./SECURITY.md).

For release triage, use these severities:

- **P0:** security/credential leakage, durable-data or history loss/corruption, unsafe active-work termination, unrecoverable backup/restore, or duplicate remote mutation under supported concurrency.
- **P1:** a supported install, migration, core lifecycle, provider sync, diagnostics, destructive workflow, or recovery path is unusable or materially misleading without a safe workaround.
- **P2/P3:** lower-impact defects and improvements. Acceptance for a stable release requires a documented workaround when applicable and a follow-up issue.

Any open P0 or P1 blocks a stable release.

## Release and support docs

- Compatibility claims: [`docs/compatibility.md`](./docs/compatibility.md)
- Support / diagnostics: [`docs/support.md`](./docs/support.md)
- Release process and stable gates: [`docs/release-checklist.md`](./docs/release-checklist.md)

## License

By contributing, you agree that your contributions are licensed under the project’s [MIT License](./LICENSE).
