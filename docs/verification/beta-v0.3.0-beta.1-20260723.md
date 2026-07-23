# v0.3.0-beta.1 Qualification Record — 2026-07-23

Issue: GH-296  
Status: **PUBLISHED, THEN SUPERSEDED BY `v0.3.0-beta.2`**

Beta.1's publication attestation passed for its frozen commit, but the candidate accidentally omitted two completed Herdr compatibility commits that existed only on local `master`. Its immutable tag remains for provenance; users should install [`v0.3.0-beta.2`](https://github.com/carlotran4/kanbi/releases/tag/v0.3.0-beta.2). This record retains completed evidence for Kanbi's first public beta. The maintainer accepted the explicitly documented unavailable/partial real-service checks on 2026-07-23 and authorized publication. This qualification does not imply feature completeness or stable readiness.

## Candidate identity

| Field | Value |
| --- | --- |
| Version | `0.3.0-beta.1` |
| Candidate SHA | `c263b19a4d4af8bb57d5df295bf1227a756e8082` |
| Qualification date UTC | `2026-07-23` |
| GitHub CI | [run 29976823338](https://github.com/carlotran4/kanbi/actions/runs/29976823338) — PASS |
| Workflow-dispatch run | [run 29976938673](https://github.com/carlotran4/kanbi/actions/runs/29976938673) — PASS |
| Combined candidate bundle | `kanbi-release-bundle` artifact from run 29976938673 |
| Tag workflow | [run 29977458307](https://github.com/carlotran4/kanbi/actions/runs/29977458307) — PASS |
| Published release | [`v0.3.0-beta.1`](https://github.com/carlotran4/kanbi/releases/tag/v0.3.0-beta.1) |
| Decision | **PUBLISHED — ATTESTED** |

The annotated immutable tag resolves to the exact candidate commit. GitHub published it as a prerelease, and the stable `/releases/latest` endpoint remains unset.

## Beta scope and limitations

The intended feature surface, missing features, known limitations, migration/rollback instructions, and support/security posture are recorded in:

- [`CHANGELOG.md`](../../CHANGELOG.md), section `0.3.0-beta.1`;
- [`docs/releases/v0.3.0-beta.1.md`](../releases/v0.3.0-beta.1.md).

The notes explicitly preserve the future stable gates and do not claim `v1.0.0` readiness.

## Defect review

Queried all open GitHub issues on 2026-07-23. GH-276 and GH-295 were empty `New ticket` placeholders with no labels, comments, assignees, milestones, projects, or actionable content. The maintainer confirmed they were disposable placeholders, and they were permanently deleted on 2026-07-23. The maintainer also confirmed GH-329 is a non-blocking feature request, not a defect.

| Issue | Available description | Qualification disposition |
| --- | --- | --- |
| GH-276 | Empty `New ticket` placeholder | Deleted with maintainer authorization; not a defect |
| GH-295 | Empty `New ticket` placeholder | Deleted with maintainer authorization; not a defect |
| [GH-296](https://github.com/carlotran4/kanbi/issues/296) | This release qualification | Tracking issue; not a product defect |
| [GH-329](https://github.com/carlotran4/kanbi/issues/329) | Add non-interactive session-id management | Maintainer-accepted, non-blocking feature request; the TUI repair/start-fresh flow remains available |

Result: **PASS — zero known open P0/P1 defects** in the reviewed issue set.

## Exact-commit deterministic verification

Environment and complete retained output: [`evidence/beta-20260723-c263b19/`](./evidence/beta-20260723-c263b19/).

| Gate | Result | Evidence |
| --- | --- | --- |
| `go fmt ./...` and clean `gofmt -l` | PASS | [`deterministic.log`](./evidence/beta-20260723-c263b19/deterministic.log) |
| `go test ./...` | PASS | [`deterministic.log`](./evidence/beta-20260723-c263b19/deterministic.log) |
| `go test -race ./...` | PASS | [`race.log`](./evidence/beta-20260723-c263b19/race.log) |
| `go vet ./...` | PASS | [`deterministic.log`](./evidence/beta-20260723-c263b19/deterministic.log) |
| `govulncheck ./...` | PASS — zero reachable vulnerabilities | [`govulncheck.log`](./evidence/beta-20260723-c263b19/govulncheck.log) |
| `./scripts/smoke.sh --skip-checks` | PASS — real tmux + fake harnesses | [`smoke.log`](./evidence/beta-20260723-c263b19/smoke.log) |
| 60-second deterministic soak | PASS | [`soak.log`](./evidence/beta-20260723-c263b19/soak.log) |
| Focused harness/tmux/storage/backend/diagnostics tests | PASS | [`deterministic.log`](./evidence/beta-20260723-c263b19/deterministic.log) |
| Local exact-binary build/artifact/backup/restore/tmux smoke | PASS | [`local-artifact.log`](./evidence/beta-20260723-c263b19/local-artifact.log) |
| GitHub CI on exact SHA | PASS | [run 29976823338](https://github.com/carlotran4/kanbi/actions/runs/29976823338) |

The dependency upgrade to `golang.org/x/text v0.39.0` removes the reachable `GO-2026-5970` finding that blocked the prior scheduled run. `scripts/smoke.sh` also no longer uses early-exiting `grep -q` pipelines that could turn successful producer output into a `pipefail`/SIGPIPE exit 141.

## Diagnostics and support-bundle leakage

| Gate | Result | Evidence |
| --- | --- | --- |
| Synthetic token/header/session-ref/config/content canaries excluded or redacted | PASS | [`diagnostics.log`](./evidence/beta-20260723-c263b19/diagnostics.log) |
| Logging disabled by default; enabled files `0600`; rotation bounded | PASS | [`diagnostics.log`](./evidence/beta-20260723-c263b19/diagnostics.log) |
| Degraded bundle with unavailable database/runtime | PASS | [`support-bundle-manual.txt`](./evidence/beta-20260723-c263b19/support-bundle-manual.txt) |
| Archive inventory/path policy | PASS | [`support-bundle-inventory.txt`](./evidence/beta-20260723-c263b19/support-bundle-inventory.txt) |

The real-harness evidence was sanitized before retention so captured Pi and Codex resume handles are not committed.

## Real runtime, harness, and provider matrix

| Check | Result | Reason/evidence |
| --- | --- | --- |
| tmux | PASS | Deterministic and real-harness lifecycle logs |
| Pi | PASS | Start, prompt, verified ref capture, close; [`real-harnesses.log`](./evidence/beta-20260723-c263b19/real-harnesses.log) |
| Codex | PASS | Real start/prompt/ref capture/close passed; [`real-harnesses.log`](./evidence/beta-20260723-c263b19/real-harnesses.log) |
| Copilot | ACCEPTED PARTIAL | Real start/close passed; ref remained pending after eight seconds, so repair/start-fresh fallback was verified and accepted for this beta by the maintainer |
| Claude | UNAVAILABLE | `claude` binary absent on qualification host |
| Herdr | NOT RUN | Binary is installed, but no isolated disposable real-Herdr fixture was available; the host was attached to an operator workspace. Deterministic fake-Herdr doctor/adapter checks passed. |
| GitHub backend | NOT RUN | Authentication exists, but no disposable repository was designated; the smoke mutates remote issues/comments. Deterministic backend tests passed. |
| Jira backend | UNAVAILABLE | No disposable project or Jira qualification credentials were configured. Deterministic backend tests passed. |

Unavailable and partial checks are not represented as passes.

## Four-platform candidate bundle

Workflow-dispatch [run 29976938673](https://github.com/carlotran4/kanbi/actions/runs/29976938673) passed verify, all four native jobs, and bundle assembly. The dispatch correctly skipped publishing.

Downloaded-bundle verification: [`candidate-bundle.log`](./evidence/beta-20260723-c263b19/candidate-bundle.log).

| Artifact | Checksum | Version/commit | Platform/CGO/schema | Exact-binary runner smoke |
| --- | --- | --- | --- | --- |
| `linux_amd64` | PASS | PASS | PASS | PASS |
| `linux_arm64` | PASS | PASS | PASS | PASS |
| `darwin_amd64` | PASS | PASS | PASS | PASS |
| `darwin_arm64` | PASS | PASS | PASS | PASS |

`BUNDLE_MANIFEST.txt` reports the candidate SHA, all four checksums verify, and every `BUILDINFO.json` reports version `0.3.0-beta.1`, the full candidate SHA, native OS/arch, CGO enabled, and schema 7.

## Compatibility and recovery

- Fresh database initialization, CLI mutations, attachment mutation, backup/restore, SQLite integrity, and fake-harness tmux lifecycle passed on every native workflow runner.
- This is the first published Kanbi artifact if approved; there is no previous distributed release artifact. The conditional prior-distributed-artifact upgrade/rollback comparison is therefore **not applicable** to this beta. Historical schema 3→5 drill evidence remains in [`disaster-recovery-20260717.md`](./disaster-recovery-20260717.md), but it is not claimed as exact-candidate evidence.
- Existing development databases migrate forward to schema 7. Release notes require a pre-upgrade backup and explain that rollback restores that backup and loses post-backup changes.

## Publication authorization and attestation

- [x] Maintainer triaged GH-276, GH-295, and GH-329: the empty placeholders were deleted and GH-329 was accepted as a non-blocking feature request.
- [x] Candidate landed on the repository default branch and exact-commit local/CI/release-dispatch verification passed.
- [x] Maintainer accepted the documented Copilot partial result and unavailable Claude/Herdr/GitHub/Jira checks for this beta.
- [x] Maintainer authorized publication on 2026-07-23.
- [x] Created annotated immutable `v0.3.0-beta.1` at `c263b19a4d4af8bb57d5df295bf1227a756e8082`.
- [x] Tag workflow [29977458307](https://github.com/carlotran4/kanbi/actions/runs/29977458307) passed and published a GitHub prerelease, not latest stable.
- [x] Downloaded every published asset; checksums, manifest, and all four `BUILDINFO.json` records passed.
- [x] Confirmed every published file is byte-identical to the tag workflow's `kanbi-release-bundle`.
- [x] Installed the published Linux amd64 binary to a throwaway path and passed `version`, `doctor`, database/attachment/backup/restore, and disposable tmux session smoke.

Post-publication evidence: [`evidence/beta-20260723-c263b19-publication/`](./evidence/beta-20260723-c263b19-publication/). No mismatch requiring a yank was found; the tag must never move.

## Stable path preserved

`docs/verification/stable-v1.0-qualification.md` remains `NOT QUALIFIED`. No stable approval variable was set, no stable tag was created, and no beta evidence is treated as a waiver of future exact-candidate stable gates.
