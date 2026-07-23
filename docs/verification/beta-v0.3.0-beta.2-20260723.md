# v0.3.0-beta.2 Qualification Record — 2026-07-23

Issue: GH-296  
Status: **PUBLISHED — POST-PUBLICATION ATTESTATION PASSED**

This corrective beta supersedes `v0.3.0-beta.1`, which accidentally omitted two completed Herdr compatibility commits that existed only on the maintainer's local `master`. The beta.1 tag remains immutable. This record does not imply feature completeness or stable readiness.

## Candidate identity

| Field | Value |
| --- | --- |
| Version | `0.3.0-beta.2` |
| Candidate SHA | `8b209c580281589f2afb1c02495769976e985260` |
| Qualification date UTC | `2026-07-23` |
| GitHub CI | [run 29979232625](https://github.com/carlotran4/kanbi/actions/runs/29979232625) — PASS |
| Workflow-dispatch run | [run 29979397553](https://github.com/carlotran4/kanbi/actions/runs/29979397553) — PASS |
| Combined candidate bundle | `kanbi-release-bundle` artifact from run 29979397553 |
| Tag workflow | [run 29979868971](https://github.com/carlotran4/kanbi/actions/runs/29979868971) — PASS |
| Published release | [`v0.3.0-beta.2`](https://github.com/carlotran4/kanbi/releases/tag/v0.3.0-beta.2) |
| Decision | **PUBLISHED — ATTESTED** |

The annotated immutable beta.2 tag resolves to the exact candidate. GitHub published it as a prerelease, not latest stable.

## Corrective scope and qualification findings

The intended correction and limitations are recorded in [`CHANGELOG.md`](../../CHANGELOG.md) and [`docs/releases/v0.3.0-beta.2.md`](../releases/v0.3.0-beta.2.md).

Included corrections:

- current Herdr pane-first `agent start --kind <harness> --pane <id>` support;
- lowercase bounded internal agent names with pane-bound uniqueness;
- integration environment propagation and explicit custom-executable rejection;
- capability-gated legacy Herdr fallback;
- literal multiline ticket/integration prompt delivery after pane-first readiness;
- synchronous and attempt-bound resume-ref capture for short-lived CLI opens.

Qualification reproduced the same `invalid_agent_argument` failure reported by the maintainer on the live `ricing` board T-001. The first attempted correction then exposed two additional failures before tagging: unsafe prompt argument encoding and retry collisions with completed Herdr agent names. The final candidate passed the original real-Herdr scenario, retry-safe naming, prompt transport, cleanup, and Pi ref persistence. No beta.2 tag was created until these failures were fixed.

## Defect review

The beta.1 issue disposition remains current: GH-276/GH-295 were deleted placeholders, GH-329 is a maintainer-accepted feature request, and GH-296 is this qualification tracker. Result: **PASS — zero known open P0/P1 defects** in the reviewed issue set.

## Exact-candidate verification

Complete retained output: [`evidence/beta-20260723-8b209c5/`](./evidence/beta-20260723-8b209c5/).

| Gate | Result | Evidence |
| --- | --- | --- |
| fmt and clean `gofmt -l` | PASS | [`deterministic.log`](./evidence/beta-20260723-8b209c5/deterministic.log) |
| `go test ./...` | PASS | [`deterministic.log`](./evidence/beta-20260723-8b209c5/deterministic.log) |
| `go test -race ./...` | PASS | [`race.log`](./evidence/beta-20260723-8b209c5/race.log) |
| `go vet ./...` | PASS | [`deterministic.log`](./evidence/beta-20260723-8b209c5/deterministic.log) |
| `govulncheck ./...` | PASS — zero reachable vulnerabilities | [`govulncheck.log`](./evidence/beta-20260723-8b209c5/govulncheck.log) |
| tmux/fake-harness smoke | PASS | [`smoke.log`](./evidence/beta-20260723-8b209c5/smoke.log) |
| 60-second deterministic soak | PASS | [`soak.log`](./evidence/beta-20260723-8b209c5/soak.log) |
| Focused harness/tmux/storage/backend/diagnostics/Herdr tests | PASS | [`deterministic.log`](./evidence/beta-20260723-8b209c5/deterministic.log) |
| Diagnostics/leakage tests | PASS | [`diagnostics.log`](./evidence/beta-20260723-8b209c5/diagnostics.log) |
| Local exact artifact/database/backup/restore/tmux smoke | PASS | [`local-artifact.log`](./evidence/beta-20260723-8b209c5/local-artifact.log) |
| GitHub CI on exact SHA | PASS | [run 29979232625](https://github.com/carlotran4/kanbi/actions/runs/29979232625) |

## Real integration matrix

| Check | Result | Evidence/reason |
| --- | --- | --- |
| Real Herdr 0.7.5 + Pi | PASS | Pane-first multiline prompt launch, bounded unique target, live pane/agent validation, synchronous verified ref capture, cleanup; [`real-herdr.log`](./evidence/beta-20260723-8b209c5/real-herdr.log) |
| Real tmux + Pi | PASS | Start/prompt/ref/close; [`real-harnesses.log`](./evidence/beta-20260723-8b209c5/real-harnesses.log) |
| Real tmux + Codex | PASS | Start/prompt/ref/close; same evidence |
| Real tmux + Copilot | ACCEPTED PARTIAL | Start/close passed; ref remained pending after the script window, with repair/start-fresh fallback verified |
| Claude | UNAVAILABLE | Binary absent on qualification host |
| GitHub backend | NOT RUN | No disposable repository designated; deterministic backend tests passed |
| Jira backend | UNAVAILABLE | No disposable project/credentials configured; deterministic backend tests passed |

Captured resume handles were redacted before evidence retention. The maintainer had already accepted the explicitly documented unavailable/partial real-service checks for this beta line.

## Four-platform candidate bundle

Workflow-dispatch [29979397553](https://github.com/carlotran4/kanbi/actions/runs/29979397553) passed verify, all four native jobs, and bundle assembly. Downloaded-bundle verification is retained in [`candidate-bundle.log`](./evidence/beta-20260723-8b209c5/candidate-bundle.log).

| Artifact | Checksum | Version/commit | Platform/CGO/schema | Exact-binary runner smoke |
| --- | --- | --- | --- | --- |
| `linux_amd64` | PASS | PASS | PASS | PASS |
| `linux_arm64` | PASS | PASS | PASS | PASS |
| `darwin_amd64` | PASS | PASS | PASS | PASS |
| `darwin_arm64` | PASS | PASS | PASS | PASS |

Every `BUILDINFO.json` reports beta.2, the exact candidate SHA, native OS/arch, CGO enabled, and schema 7.

## Compatibility and recovery

There is no schema or package-format change from beta.1, so the conditional cross-schema artifact upgrade/rollback drill is not applicable. Fresh initialization, attachment mutation, backup/restore, integrity, and fake-harness lifecycle passed on every native runner. Existing development/beta.1 databases remain schema 7.

## Publication authorization

- [x] Corrective commits and discovered Herdr launch bugs are included in the exact candidate.
- [x] Exact local, CI, real-Herdr, real-harness, and four-platform dispatch gates passed.
- [x] Candidate is on the repository default branch.
- [x] Maintainer authorized corrective publication on 2026-07-23.
- [x] Created annotated immutable `v0.3.0-beta.2` at the exact candidate SHA.
- [x] Tag workflow [29979868971](https://github.com/carlotran4/kanbi/actions/runs/29979868971) published a prerelease, not latest stable.
- [x] Published checksums and all four `BUILDINFO.json` records passed; every asset is byte-identical to the tag workflow bundle.
- [x] Downloaded Linux artifact passed install/version/doctor/database/attachment/backup/restore/tmux-session smoke.
- [x] Marked beta.1 release notes as superseded without moving or deleting its tag.

Post-publication evidence: [`evidence/beta-20260723-8b209c5-publication/`](./evidence/beta-20260723-8b209c5-publication/).

## Stable path preserved

`docs/verification/stable-v1.0-qualification.md` remains `NOT QUALIFIED`. No stable approval variable is set, and beta evidence does not waive future exact-candidate stable gates.
