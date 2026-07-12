# Stable v1.0 Qualification Record

Issue: GH-292
Target: `v1.0.0`
Status: **NOT QUALIFIED — do not tag or publish**

This is the canonical, version-controlled gate record for Kanbi's first stable release. A checked box must link to durable evidence for the exact candidate commit. `PASS` without a link, command output, environment, date, and reviewer is not evidence.

## Candidate identity and freeze

| Field | Value |
| --- | --- |
| Candidate version | `v1.0.0-rc.___` |
| Candidate commit (full SHA) | `NOT SET` |
| Freeze start/end (UTC) | `NOT SET` |
| Qualification owner | `NOT SET` |
| RC workflow-dispatch run and artifact bundle | `NOT SET` |
| Final decision | `BLOCKED` |

During the freeze, changes to the SQLite schema, CLI command names, JSON schemas, lifecycle semantics, supported platform matrix, backup format, or harness commands invalidate affected evidence and require a new candidate. Material fixes require rerunning all gates.

## Blocker policy

- **P0:** demonstrated or credible security/credential leak, durable-data or history loss/corruption, unsafe termination of active work, unrecoverable backup/restore, or duplicate remote mutation under supported concurrency.
- **P1:** a supported install, migration, core lifecycle, provider sync, diagnostics, destructive workflow, or recovery path is unusable or materially misleading without a safe workaround.
- Any open P0 or P1 blocks release. Lower severities may be accepted only with a documented workaround and follow-up issue.
- Any non-negotiable blocker from GH-292 blocks release regardless of its existing issue label.

## Gate summary

| Gate | Status | Evidence | Owner/reviewer |
| --- | --- | --- | --- |
| Contract and compatibility freeze | NOT RUN | — | — |
| Exact-commit deterministic verification | NOT RUN | — | — |
| Multi-day soak | NOT RUN | [Soak report](#soak-report) | — |
| Upgrade, downgrade rejection, rollback, restore | NOT RUN | [Disaster-recovery report](#disaster-recovery-report) | — |
| Four-platform artifact matrix | NOT RUN | [Artifact matrix](#artifact-matrix) | — |
| Accessibility/usability matrix | NOT RUN | [Accessibility report](#accessibility-and-usability-report) | — |
| Diagnostics/support-bundle leakage | NOT RUN | — | — |
| P0/P1 issue review | NOT RUN | — | — |
| External RC feedback | NOT RUN | [Feedback report](#external-feedback-report) | — |
| Release notes/support/security review | NOT RUN | — | — |
| RC artifacts match candidate | NOT RUN | — | — |

## 1. Frozen contracts

- [ ] `docs/compatibility.md` records every supported and unsupported platform/runtime check.
- [ ] `docs/state-management.md` and `docs/ticket-session-lifecycle.md` are reviewed and frozen.
- [ ] `docs/harness-contracts.md` and `docs/multiplexer-contracts.md` are reviewed and frozen.
- [ ] Every JSON schema emitted by supported CLI commands is inventoried and fixture-tested.
- [ ] `storage.CurrentSchemaVersion()`, migration policy, backup format, and rollback warning are recorded.
- [ ] Any post-freeze contract change has a new RC and invalidated evidence is rerun.

Evidence: `NOT SET`

## 2. Exact-candidate automated verification

Run from a clean checkout at the candidate SHA and attach complete output plus tool versions:

```bash
go fmt ./...
test -z "$(gofmt -l .)"
go test ./...
go test -race ./...
go vet ./...
govulncheck ./...
./scripts/smoke.sh --skip-checks
go test ./internal/harness ./internal/tmux ./internal/storage ./internal/ticketbackend ./internal/diagnostics
```

Also link green CI and a workflow-dispatch release-candidate build for the same SHA. Record every unavailable check explicitly; a skip is not a pass.

Available opt-in checks are required unless the qualification owner records why the environment cannot support one and identifies equivalent evidence:

- [ ] `KANBI_REAL_HARNESS_TESTS=1 KANBI_REAL_HARNESSES=pi,codex,copilot,claude ./scripts/real-harness-lifecycle.sh`
- [ ] Real tmux lifecycle through `./scripts/smoke.sh --skip-checks`.
- [ ] Real Herdr lifecycle in a disposable workspace, when a supported Herdr runner is available.
- [ ] `./scripts/github-backend-smoke.sh` against a disposable-capable repository.
- [ ] `./scripts/jira-backend-smoke.sh` against a disposable-capable project.

Record credentials/quota/platform limitations without recording secrets. Supported provider or harness checks may not be silently omitted.

Evidence: `NOT SET`

## Soak report

A qualifying soak spans multiple calendar days and includes multiple boards and Kanbi processes, active/resumed/closed sessions, periodic and mutation-triggered provider sync, runtime refresh, backups, restarts, and injected transient failures. The deterministic soak is supporting evidence, not a substitute for the calendar soak.

| Field | Value |
| --- | --- |
| Candidate SHA | `NOT SET` |
| Start/end UTC and duration | `NOT SET` |
| Hosts/platforms | `NOT SET` |
| Kanbi processes / boards / tickets | `NOT SET` |
| Runtime/harness/provider mix | `NOT SET` |
| Failure injections and restarts | `NOT SET` |
| Backups created/verified | `NOT SET` |

Record baseline, periodic, and final measurements for RSS, open file descriptors, goroutine count (when instrumented), SQLite busy/locked errors, sync lease recovery, duplicate remote creates, active-session claims, stale/degraded state, and ticket/session row counts. Explain the measurement method and acceptance threshold before starting.

- [ ] No race reports, unbounded resource growth, goroutine leak, persistent lock contention, or silent stale state.
- [ ] No duplicate remote mutation.
- [ ] No incorrect simultaneous active-session claim.
- [ ] Ticket, note, attachment, and session-history integrity checks match the baseline plus expected mutations.
- [ ] `KANBI_SOAK_SECONDS=___ ./scripts/soak-runtime.sh` passes on the candidate.

Timeline, raw logs, metrics, anomalies, and issue links: `NOT SET`

## Disaster-recovery report

Use a prior tagged artifact and an RC workflow artifact, not two labels built from the same source tree. Set `OLD_ARCHIVE` and `NEW_ARCHIVE` when running the drill. The script verifies the baseline ticket/note/optional-attachment restore path; it does **not** by itself verify downgrade rejection or seeded session history. Complete and record the additional manual checks below rather than treating script `PASS` as the whole gate.

```bash
OLD_ARCHIVE=/path/to/prior.tar.gz \
NEW_ARCHIVE=/path/to/v1.0.0-rc.tar.gz \
./scripts/upgrade-rollback-drill.sh
```

- [ ] Old artifact initializes and seeds tickets, notes, a required attachment, and active/inactive session-history fixtures (using a disposable runtime or reviewed fixture setup).
- [ ] Old artifact creates a verified pre-upgrade backup.
- [ ] Candidate migrates and normal post-migration use succeeds.
- [ ] Old artifact rejects the newer database when schema versions differ.
- [ ] Prior artifact plus pre-upgrade backup restores successfully.
- [ ] Post-restore integrity verifies tickets, notes, attachments, session rows, and foreign keys.
- [ ] Expected loss of post-backup changes and the no-in-place-downgrade rule are documented.

| Field | Value |
| --- | --- |
| Old version / SHA / schema | `NOT SET` |
| New version / SHA / schema | `NOT SET` |
| Host | `NOT SET` |
| Drill output | `NOT SET` |
| Backup hash | `NOT SET` |
| Reviewer | `NOT SET` |

## Artifact matrix

Validate artifacts downloaded from the candidate release, not locally substituted files. Verify each archive against the published `SHA256SUMS`, inspect `BUILDINFO.json`, and confirm its commit equals the frozen candidate SHA.

| Artifact | Checksum | startup/help/version | doctor | DB init/migrate | backup/restore | runtime lifecycle | Evidence |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `linux_amd64` | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | — |
| `linux_arm64` | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | — |
| `darwin_amd64` | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | — |
| `darwin_arm64` | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | — |

For runner limitations (for example unavailable tmux/Herdr or real harness credentials), write `UNSUPPORTED ON RUNNER: reason` and link the separate native/manual check. Never convert an unavailable check into `PASS`.

## Accessibility and usability report

Execute every row in `docs/ux-readiness.md` and record terminal emulator, `TERM`, OS/arch, dimensions, theme, date, tester, result, and evidence.

- [ ] Keyboard-only onboarding, picker, board, inspector, help, filters, repair, backup guidance, and destructive confirmations.
- [ ] Monochrome operation; color/glyphs are never the only meaning.
- [ ] Light and dark themes.
- [ ] 80x24 reachability and safe smaller-terminal degradation.
- [ ] Long content, Unicode, combining characters, emoji, and long unbroken words.
- [ ] First-run, empty, error, missing dependency, provider-degraded, and runtime-degraded states.
- [ ] Exact-name destructive confirmation and active-session deletion/archive blocks.
- [ ] Accepted limitations have workaround text and follow-up issues.

Matrix results and issue links: `NOT SET`

## Diagnostics and leakage

- [ ] Default diagnostics and generated support bundles contain no credentials, auth headers, prompts, ticket bodies, notes, attachment contents, terminal excerpts, or harness session refs.
- [ ] Synthetic secret/content canaries are searched in extracted archives and logs.
- [ ] Degraded support-bundle creation passes with unavailable DB/runtime.
- [ ] File modes, archive path safety, and log rotation pass.

Commands, canary list, archive inventory, and evidence: `NOT SET`

## Defect review

Query all open GitHub issues and the project board at freeze time. Attach an exported list including URL, severity, disposition, workaround, and follow-up owner.

- [ ] Zero open P0.
- [ ] Zero open P1.
- [ ] Every accepted lower-severity issue appears in release known limitations with a workaround when one exists.

Query URL/export and reviewer: `NOT SET`

## External feedback report

At least one external user must use a candidate artifact for checksum/install, first run/doctor, ticket session start/close/resume or repair, backup, and restore. Do not record private data in this repository.

| Tester/environment | RC | Workflow completed | Feedback/issues | Disposition |
| --- | --- | --- | --- | --- |
| `NOT SET` | `NOT SET` | `NOT SET` | `NOT SET` | `NOT SET` |

- [ ] Every regression is triaged.
- [ ] Material fixes produced a new RC and reran affected/full qualification.

## Release documentation and publication authorization

These are qualification gates completed **before** tagging:

- [ ] `CHANGELOG.md` has a prepared dated `1.0.0` section with breaking changes and migration behavior.
- [ ] Prepared release notes include rollback steps, compatibility matrix, known limitations, and support/security links.
- [ ] The exact candidate SHA has green CI, vulnerability, race, smoke, migration, soak, and RC-build evidence.
- [ ] All four RC workflow artifacts report the candidate SHA/version/schema and their generated checksums verify.
- [ ] Final approver records `QUALIFIED — AUTHORIZED TO TAG` in the linked immutable qualification review without changing the candidate tree.

Only after those gates pass, create the annotated `v1.0.0` tag at that SHA and publish through the tag workflow. The following post-publication attestation is a response check, not a prerequisite gate (published assets cannot exist before authorization). A mismatch requires yanking the release:

- [ ] Tag target equals the qualified SHA.
- [ ] All four published `BUILDINFO.json` files equal the tag SHA/version/schema.
- [ ] Published `SHA256SUMS`, assets, and prepared notes are complete.

Final approver, UTC timestamp, tag SHA, release URL, post-publication attestation: `NOT SET`

## Invalidation log

| Date UTC | Change | Evidence invalidated | New RC/SHA | Owner |
| --- | --- | --- | --- | --- |
| — | — | — | — | — |
