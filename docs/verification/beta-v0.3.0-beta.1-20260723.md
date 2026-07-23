# v0.3.0-beta.1 Qualification Record — 2026-07-23

Issue: GH-296  
Status: **BLOCKED — NOT QUALIFIED; DO NOT TAG OR PUBLISH**

This record retains completed evidence for a provisional first-public-beta candidate while preserving every unmet publication gate. It does not imply feature completeness or stable readiness.

## Candidate identity

| Field | Value |
| --- | --- |
| Version | `0.3.0-beta.1` |
| Provisional candidate SHA | `86fbee85337dbf36041c1dc6055acc97c923dd18` |
| Qualification date UTC | `2026-07-23` |
| GitHub CI | [run 29975842589](https://github.com/carlotran4/kanbi/actions/runs/29975842589) — PASS |
| Workflow-dispatch run | [run 29975845153](https://github.com/carlotran4/kanbi/actions/runs/29975845153) — PASS |
| Combined bundle | `kanbi-release-bundle` artifact from run 29975845153 |
| Decision | **BLOCKED** — candidate is not on the repository default branch and P0/P1 review cannot be completed from the current issue data |

The candidate commit is pushed only to `release-qualification-publish-the-next-beta-and-preserve-the-path-to-stable`. No tag or GitHub Release was created.

## Beta scope and limitations

The intended feature surface, missing features, known limitations, migration/rollback instructions, and support/security posture are recorded in:

- [`CHANGELOG.md`](../../CHANGELOG.md), section `0.3.0-beta.1`;
- [`docs/releases/v0.3.0-beta.1.md`](../releases/v0.3.0-beta.1.md).

The notes explicitly preserve the future stable gates and do not claim `v1.0.0` readiness.

## Defect review

Queried all open GitHub issues on 2026-07-23. The repository has no P0/P1 labels, so absence of a priority label is not evidence of absence. Two open issues have blank titles/bodies and cannot be safely classified without maintainer triage.

| Issue | Available description | Qualification disposition |
| --- | --- | --- |
| [GH-276](https://github.com/carlotran4/kanbi/issues/276) | `New ticket`; blank body | **UNTRIAGED — BLOCKS priority attestation** |
| [GH-295](https://github.com/carlotran4/kanbi/issues/295) | `New ticket`; blank body | **UNTRIAGED — BLOCKS priority attestation** |
| [GH-296](https://github.com/carlotran4/kanbi/issues/296) | This release qualification | Tracking issue; not a product defect |
| [GH-329](https://github.com/carlotran4/kanbi/issues/329) | Add non-interactive session-id management | Provisional beta feature gap with safe TUI repair/start-fresh workaround; explicit maintainer severity/disposition still required |

Result: no P0 is known from the available descriptions, but **zero-open-P0 cannot be attested** until GH-276 and GH-295 are triaged. Publication remains blocked.

## Exact-commit deterministic verification

Environment and complete retained output: [`evidence/beta-20260723-86fbee8/`](./evidence/beta-20260723-86fbee8/).

| Gate | Result | Evidence |
| --- | --- | --- |
| `go fmt ./...` and clean `gofmt -l` | PASS | [`deterministic.log`](./evidence/beta-20260723-86fbee8/deterministic.log) |
| `go test ./...` | PASS | [`deterministic.log`](./evidence/beta-20260723-86fbee8/deterministic.log) |
| `go test -race ./...` | PASS | [`race.log`](./evidence/beta-20260723-86fbee8/race.log) |
| `go vet ./...` | PASS | [`deterministic.log`](./evidence/beta-20260723-86fbee8/deterministic.log) |
| `govulncheck ./...` | PASS — zero reachable vulnerabilities | [`govulncheck.log`](./evidence/beta-20260723-86fbee8/govulncheck.log) |
| `./scripts/smoke.sh --skip-checks` | PASS — real tmux + fake harnesses | [`smoke.log`](./evidence/beta-20260723-86fbee8/smoke.log) |
| 60-second deterministic soak | PASS | [`soak.log`](./evidence/beta-20260723-86fbee8/soak.log) |
| Focused harness/tmux/storage/backend/diagnostics tests | PASS | [`deterministic.log`](./evidence/beta-20260723-86fbee8/deterministic.log) |
| Local exact-binary build/artifact/backup/restore/tmux smoke | PASS | [`local-artifact.log`](./evidence/beta-20260723-86fbee8/local-artifact.log) |
| GitHub CI on exact SHA | PASS | [run 29975842589](https://github.com/carlotran4/kanbi/actions/runs/29975842589) |

The dependency upgrade to `golang.org/x/text v0.39.0` removes the reachable `GO-2026-5970` finding that blocked the prior scheduled run. `scripts/smoke.sh` also no longer uses early-exiting `grep -q` pipelines that could turn successful producer output into a `pipefail`/SIGPIPE exit 141.

## Diagnostics and support-bundle leakage

| Gate | Result | Evidence |
| --- | --- | --- |
| Synthetic token/header/session-ref/config/content canaries excluded or redacted | PASS | [`diagnostics.log`](./evidence/beta-20260723-86fbee8/diagnostics.log) |
| Logging disabled by default; enabled files `0600`; rotation bounded | PASS | [`diagnostics.log`](./evidence/beta-20260723-86fbee8/diagnostics.log) |
| Degraded bundle with unavailable database/runtime | PASS | [`support-bundle-manual.txt`](./evidence/beta-20260723-86fbee8/support-bundle-manual.txt) |
| Archive inventory/path policy | PASS | [`support-bundle-inventory.txt`](./evidence/beta-20260723-86fbee8/support-bundle-inventory.txt) |

The real-harness evidence was sanitized before retention so its captured Pi resume handle is not committed.

## Real runtime, harness, and provider matrix

| Check | Result | Reason/evidence |
| --- | --- | --- |
| tmux | PASS | Deterministic and real-harness lifecycle logs |
| Pi | PASS | Start, prompt, verified ref capture, close; [`real-harnesses.log`](./evidence/beta-20260723-86fbee8/real-harnesses.log) |
| Codex | PARTIAL | Real start/prompt/close passed; ref was not captured within the script window, so repair/start-fresh fallback was verified |
| Copilot | PARTIAL | Real start/close passed; ref remained pending after eight seconds, so repair/start-fresh fallback was verified |
| Claude | UNAVAILABLE | `claude` binary absent on qualification host |
| Herdr | NOT RUN | Binary is installed, but no isolated disposable real-Herdr fixture was available; the host was attached to an operator workspace. Deterministic fake-Herdr doctor/adapter checks passed. |
| GitHub backend | NOT RUN | Authentication exists, but no disposable repository was designated; the smoke mutates remote issues/comments. Deterministic backend tests passed. |
| Jira backend | UNAVAILABLE | No disposable project or Jira qualification credentials were configured. Deterministic backend tests passed. |

Unavailable and partial checks are not represented as passes.

## Four-platform candidate bundle

Workflow-dispatch [run 29975845153](https://github.com/carlotran4/kanbi/actions/runs/29975845153) passed verify, all four native jobs, and bundle assembly. The dispatch correctly skipped publishing.

Downloaded-bundle verification: [`candidate-bundle.log`](./evidence/beta-20260723-86fbee8/candidate-bundle.log).

| Artifact | Checksum | Version/commit | Platform/CGO/schema | Exact-binary runner smoke |
| --- | --- | --- | --- | --- |
| `linux_amd64` | PASS | PASS | PASS | PASS |
| `linux_arm64` | PASS | PASS | PASS | PASS |
| `darwin_amd64` | PASS | PASS | PASS | PASS |
| `darwin_arm64` | PASS | PASS | PASS | PASS |

`BUNDLE_MANIFEST.txt` reports the provisional SHA, all four checksums verify, and every `BUILDINFO.json` reports version `0.3.0-beta.1`, the full provisional SHA, native OS/arch, CGO enabled, and schema 7.

## Compatibility and recovery

- Fresh database initialization, CLI mutations, attachment mutation, backup/restore, SQLite integrity, and fake-harness tmux lifecycle passed on every native workflow runner.
- This is the first published Kanbi artifact if approved; there is no previous distributed release artifact. The conditional prior-distributed-artifact upgrade/rollback comparison is therefore **not applicable** to this beta. Historical schema 3→5 drill evidence remains in [`disaster-recovery-20260717.md`](./disaster-recovery-20260717.md), but it is not claimed as exact-candidate evidence.
- Existing development databases migrate forward to schema 7. Release notes require a pre-upgrade backup and explain that rollback restores that backup and loses post-backup changes.

## Publication gates still open

- [ ] Maintainer explicitly triages GH-276, GH-295, and GH-329 and records P0/P1 dispositions.
- [ ] Candidate lands on the repository default branch without changing the qualified tree; otherwise freeze and qualify a new SHA.
- [ ] Maintainer accepts or reruns the partial Codex/Copilot checks and records whether real Herdr/GitHub/Jira checks can be safely run in disposable resources.
- [ ] Create the annotated immutable `v0.3.0-beta.1` tag only after the gates above close.
- [ ] Confirm the tag workflow republishes the same four-platform content as a GitHub prerelease, not latest stable.
- [ ] Download a published asset and complete checksum/install/version/doctor/disposable-session/backup smoke.

Until those items are complete, the retained evidence supports continued qualification work only. It does not authorize publication.

## Stable path preserved

`docs/verification/stable-v1.0-qualification.md` remains `NOT QUALIFIED`. No stable approval variable was set, no stable tag was created, and no beta evidence is treated as a waiver of future exact-candidate stable gates.
