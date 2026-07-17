# Beta Release Checklist

Use this checklist for `v0.x` beta artifacts such as `v0.3.0-beta.1`. It establishes a repeatable stability floor without claiming the complete v1 product or support contract.

Copy the evidence table into a dated file under `docs/verification/` for each public beta. Every `PASS` must identify the exact full commit SHA and link the workflow or retained output.

## Candidate identity

| Field | Value |
| --- | --- |
| Version | `0.3.0-beta.1` |
| Full commit SHA | `NOT SET` |
| Qualification date UTC | `NOT SET` |
| Workflow-dispatch run | `NOT SET` |
| Combined bundle/checksums | `NOT SET` |
| Decision | `NOT QUALIFIED` |

## Beta blockers

Do not publish a beta with a known instance of:

- credential, token, prompt, ticket-content, or session-ref leakage in diagnostics/support output;
- ticket/session history corruption, flattening, or loss;
- unexpected termination of active ticket work;
- duplicate remote creation under supported concurrency;
- backup/restore unable to recover documented durable state;
- silent stale/degraded runtime state;
- an artifact unable to initialize a supported database;
- an unresolved P0 defect.

P1 defects require explicit maintainer disposition. A beta may accept a P1 only when the release notes clearly identify the limitation, safe workaround, and follow-up issue; stable qualification still requires zero open P0/P1.

## 1. Freeze and release content

- [ ] Intended beta scope and known missing features are listed in `CHANGELOG.md` and draft release notes.
- [ ] Compatibility, install, upgrade, rollback, support, and security documentation match the beta.
- [ ] No schema/CLI JSON/lifecycle/harness contract changes occur after the candidate SHA without a new beta candidate.
- [ ] Open P0/P1 issues are reviewed under the policy above.

## 2. Exact-commit verification

From a clean checkout of the candidate:

```bash
go fmt ./...
test -z "$(gofmt -l .)"
go test ./...
go test -race ./...
go vet ./...
govulncheck ./...
./scripts/smoke.sh --skip-checks
KANBI_SOAK_SECONDS=60 ./scripts/soak-runtime.sh
```

- [ ] Commands pass and complete output is retained.
- [ ] GitHub CI is green for the same SHA.
- [ ] Diagnostics/support-bundle redaction tests pass.
- [ ] Available real harness/provider checks are run or explicitly marked unavailable with a reason.

## 3. Candidate bundle

Dispatch `.github/workflows/release.yml` with the beta version, for example `0.3.0-beta.1`.

- [ ] Verify job passes.
- [ ] All four native matrix jobs pass their exact-binary artifact and tmux lifecycle smoke checks.
- [ ] Combined `kanbi-release-bundle` contains four archives, `SHA256SUMS`, and `BUNDLE_MANIFEST.txt`.
- [ ] `BUNDLE_MANIFEST.txt` commit equals the frozen SHA.
- [ ] Every checksum verifies.
- [ ] Every archive's `BUILDINFO.json` reports the version, frozen commit, native OS/arch, CGO enabled, and current schema.

## 4. Compatibility and recovery

- [ ] Fresh database initialization, CLI use, backup, attachment mutation, restore, and integrity checks pass on each native runner through `scripts/release-artifact-smoke.sh`.
- [ ] If schema changed since the previous published beta, `scripts/upgrade-rollback-drill.sh` passes with the previous published archive and exact candidate archive.
- [ ] Upgrade and rollback warnings identify expected loss of post-backup changes.
- [ ] A deterministic soak appropriate to the changes passes without race reports or unbounded resource growth.

## 5. Publish and attest

- [ ] Create an annotated immutable tag such as `v0.3.0-beta.1` at the qualified SHA.
- [ ] Tag workflow passes again and publishes the release as a GitHub **prerelease**.
- [ ] Published assets exactly match the workflow bundle and `SHA256SUMS` verifies.
- [ ] Release notes list missing features, known limitations, migration behavior, rollback, and support/security links.
- [ ] Install one downloaded published artifact into a throwaway path and run `version`, `doctor`, and a disposable board/session smoke.

If publication differs from the qualified bundle or tag SHA, mark/yank the release and investigate; never move the tag.

## Promotion toward stable

Preserve beta reports and regression evidence. Stable qualification may reuse them as historical evidence, but all exact-commit gates in [`verification/stable-v1.0-qualification.md`](./verification/stable-v1.0-qualification.md) must be rerun on the future stable candidate.
