# Release Channels and Version Policy

Kanbi uses one release pipeline from development betas through stable releases. Channel differences are policy and qualification gates, not separate build paths.

## Version formats

| Channel | Tag example | GitHub Release | Intended use |
| --- | --- | --- | --- |
| Development snapshot | no tag; workflow version `0.3.0-beta.1` | Actions artifact only | Maintainer qualification |
| Public beta | `v0.3.0-beta.1` | Prerelease | Early adopters; incomplete product surface |
| Beta release candidate | `v0.3.0-rc.1` | Prerelease | Final validation before a beta-series release |
| Beta-series release | `v0.3.0` | Prerelease while major version is zero | Broader beta use without a v1 stability claim |
| Stable | `v1.0.0` or later | Full release | Supported stable contract |

The release workflow accepts only `MAJOR.MINOR.PATCH`, `MAJOR.MINOR.PATCH-beta.N`, and `MAJOR.MINOR.PATCH-rc.N`, with an optional leading `v`. All `0.x` tags and every suffixed tag are published as GitHub prereleases, so they do not replace GitHub's latest stable release.

The current published beta is **`v0.3.0-beta.3`**, which superseded `v0.3.0-beta.2` without moving either immutable tag. Future candidates are qualified through a workflow-dispatch run and durable GitHub release issue rather than committed dated verification records.

## One artifact path

Every channel uses `.github/workflows/release.yml` and the same native CGO matrix:

- Linux amd64
- Linux arm64
- macOS amd64
- macOS arm64

Each native runner builds the binary, validates `BUILDINFO.json`, runs metadata/database/backup/restore checks against that exact binary, and runs the fake-harness tmux lifecycle against that exact binary. The bundle job requires four archives, produces and verifies `SHA256SUMS`, records `BUNDLE_MANIFEST.txt`, and uploads one combined Actions artifact. Tag pushes publish that already-validated bundle with the reviewed `docs/releases/vVERSION.md` file as the GitHub Release body. The workflow rejects a candidate when that notes file is absent or empty; generated notes are not a substitute for reviewed limitations, migration, rollback, support, and security guidance.

Workflow dispatch never publishes a GitHub Release. Use it first to qualify an exact commit and download the combined candidate bundle.

## Beta gate

Follow the common [`release-checklist.md`](./release-checklist.md). Betas may have incomplete features and documented usability limitations, but they may not knowingly ship credential leakage, durable-state/history corruption, unsafe active-work termination, duplicate remote creation, unusable backup/restore, or an artifact unable to initialize its database. A material fix creates a new beta number and reruns affected qualification; never move an existing tag.

## Stable gate

Stable publishing is technically locked by default. A tag with major version 1 or greater fails unless repository variable `KANBI_STABLE_RELEASE_APPROVAL` exactly matches `VERSION@FULL_COMMIT_SHA` (for example `1.0.0@0123…`). A maintainer may set that candidate-bound value only after the exact candidate satisfies the stable-only gates in [`release-checklist.md`](./release-checklist.md) and receives final approval. Change or remove it after publication so it cannot authorize another version or commit.

The variable is an accidental-release guard, not evidence. Setting it cannot waive any stable gate.

## Support posture

During beta, the latest published beta receives best-effort fixes and security updates; older betas may be superseded without backports. Stable support policy begins only when a stable release is published. Security reports always use the private process in [`SECURITY.md`](../SECURITY.md).
