# v0.3.0-beta.3 Qualification Record — 2026-07-29

Issue: GH-342  
Status: **QUALIFIED FOR TAGGING — NOT PUBLISHED**

This beta adds global Focus Mode, project-file completion, safer session repair, and shared lifecycle/storage enforcement. It remains beta software and does not imply stable readiness.

## Candidate identity

| Field | Value |
| --- | --- |
| Version | `0.3.0-beta.3` |
| Candidate SHA | `a711aceedb758598974700aa39deeb7d168d2ee5` |
| Qualification date UTC | `2026-07-29` |
| GitHub CI | [run 30420704202](https://github.com/carlotran4/kanbi/actions/runs/30420704202) — PASS |
| Workflow-dispatch run | [run 30420765221](https://github.com/carlotran4/kanbi/actions/runs/30420765221) — PASS |
| Combined candidate bundle | `kanbi-release-bundle` artifact from run 30420765221 |
| Decision | **QUALIFIED FOR TAGGING — NOT PUBLISHED** |

The workflow-dispatch run intentionally did not publish. Publication requires an annotated immutable `v0.3.0-beta.3` tag at the exact candidate SHA.

## Scope and defect review

The intended scope and limitations are recorded in [`CHANGELOG.md`](../../CHANGELOG.md) and [`docs/releases/v0.3.0-beta.3.md`](../releases/v0.3.0-beta.3.md).

The open issue review found GH-342 (this release-preparation tracker) and GH-338 (an empty, unprioritized placeholder). Neither reports a known P0/P1 defect. Result: **PASS — zero known open P0/P1 defects in the reviewed issue set**.

## Exact-candidate verification

Retained local evidence: [`evidence/beta-20260729-a711ace/`](./evidence/beta-20260729-a711ace/).

| Gate | Result | Evidence |
| --- | --- | --- |
| fmt and clean `gofmt -l` | PASS | release run verify job; [local summary](./evidence/beta-20260729-a711ace/local-qualification-summary.txt) |
| `go test ./...` | PASS | CI and release verify jobs; local summary |
| `go test -race ./...` | PASS | CI and release verify jobs; local summary |
| `go vet ./...` | PASS | CI and release verify jobs; local summary |
| `govulncheck ./...` | PASS — zero reachable vulnerabilities | CI and release verify jobs; local summary |
| tmux/fake-harness smoke | PASS | CI smoke and release verify jobs; local summary |
| 60-second deterministic soak | PASS | [`soak.log`](./evidence/beta-20260729-a711ace/soak.log) |
| Focused harness/tmux/storage/diagnostics tests | PASS | local summary |
| Local exact release artifact smoke | PASS | local summary; release native jobs |
| Schema-7 to schema-8 upgrade/rollback drill | PASS | [`upgrade-rollback-drill.log`](./evidence/beta-20260729-a711ace/upgrade-rollback-drill.log) |
| Downloaded four-platform bundle inspection | PASS | [`candidate-bundle.log`](./evidence/beta-20260729-a711ace/candidate-bundle.log) |
| Isolated real TUI at 160x45 and 80x24 | PASS | [`ui-validation.log`](./evidence/beta-20260729-a711ace/ui-validation.log) |
| GitHub CI on exact SHA | PASS | [run 30420704202](https://github.com/carlotran4/kanbi/actions/runs/30420704202) |

## Real integration matrix

| Check | Result | Evidence/reason |
| --- | --- | --- |
| Deterministic tmux/fake harnesses | PASS | local and GitHub smoke jobs |
| Deterministic Herdr adapter | PASS | normal and race suites |
| Real tmux + Pi | PASS | Prompt submitted, pane text visible, verified ref captured and redacted, and window cleanup passed. The helper's stale-window check did not reconcile the durable row and is not used as inactivity evidence; [`real-pi.log`](./evidence/beta-20260729-a711ace/real-pi.log) |
| Real tmux + Codex | PASS | Prompt visible, verified ref captured and redacted, and window cleanup passed. The same helper limitation applies; [`real-tmux-harnesses.log`](./evidence/beta-20260729-a711ace/real-tmux-harnesses.log) |
| Real tmux + Copilot | ACCEPTED PARTIAL | Start/window/cleanup passed; ref remained pending after the bounded wait, so the documented repair/start-fresh fallback is expected rather than proven by this helper; same evidence. |
| Real Herdr 0.7.5 + Pi | PASS | Exact candidate, pane-first launch, multiline literal prompt, verified ref capture (redacted), live container inspection, and cleanup; [`real-herdr.log`](./evidence/beta-20260729-a711ace/real-herdr.log) |
| Claude | UNAVAILABLE | Binary absent on the qualification host. |
| GitHub/Jira backends | NOT RUN | No disposable external repository/project designated; deterministic backend and provider-sync tests passed. |

No real-service behavior was inferred from unavailable checks. The documented repair/start-fresh fallback remains the safe path when a verified harness session ref is unavailable.

## Four-platform candidate bundle

Workflow-dispatch [30420765221](https://github.com/carlotran4/kanbi/actions/runs/30420765221) passed verify, all four native jobs, and bundle assembly. The downloaded bundle's `SHA256SUMS`, `BUNDLE_MANIFEST.txt`, and every archive's `BUILDINFO.json` were checked locally.

| Artifact | Checksum | Version/commit | Platform/CGO/schema | Exact-binary runner smoke |
| --- | --- | --- | --- | --- |
| `linux_amd64` | PASS | PASS | PASS — schema 8 | PASS |
| `linux_arm64` | PASS | PASS | PASS — schema 8 | PASS |
| `darwin_amd64` | PASS | PASS | PASS — schema 8 | PASS |
| `darwin_arm64` | PASS | PASS | PASS — schema 8 | PASS |

`BUNDLE_MANIFEST.txt` records version `0.3.0-beta.3` and exact candidate commit `a711aceedb758598974700aa39deeb7d168d2ee5`.

## Compatibility and recovery

Beta.3 advances the database from schema 7 to schema 8 for Focus Mode pause state and append-only checkpoints. An explicit beta.2-to-beta.3 artifact drill passed:

- schema-7 ticket, note, attachment, and active/inactive session history survived migration;
- SQLite integrity and foreign-key checks passed;
- beta.2 rejected the migrated schema-8 database with the expected newer-schema explanation;
- restoring the pre-upgrade backup with beta.2 recovered the original state and removed post-backup changes.

The reviewed release notes document the backup-before-upgrade requirement and the expected loss of post-backup changes during rollback.

## Tagging handoff

- [x] Release content and reviewed notes are present on the exact candidate.
- [x] Candidate is reachable from the repository default branch.
- [x] Local, CI, race, vulnerability, smoke, soak, UI, artifact, recovery, and four-platform dispatch gates passed.
- [x] Candidate bundle checksums and provenance match the exact candidate.
- [ ] Maintainer creates annotated immutable tag `v0.3.0-beta.3` at `a711aceedb758598974700aa39deeb7d168d2ee5` and pushes it.
- [ ] Tag workflow publishes the GitHub prerelease and all assets verify.
- [ ] A downloaded published artifact passes post-publication `version`, `doctor`, and disposable board/session smoke.

Do not tag the qualification-record commit in place of the exact candidate above. Do not move the tag after publication.

## Stable path preserved

`docs/verification/stable-v1.0-qualification.md` remains `NOT QUALIFIED`. No stable approval is implied by this beta record.
