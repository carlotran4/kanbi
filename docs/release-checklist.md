# Release Checklist

Use this list before tagging any release. Channel and version rules live in [`release-channels.md`](./release-channels.md). Keep exact-commit evidence in the GitHub Actions run, release issue, or GitHub Release rather than committing generated logs and dated attestations to the source tree. Stable workflow publishing must remain locked until every stable-only gate below passes.

## Release blockers

P0 covers security/credential leakage, durable-data or history loss/corruption, unsafe active-work termination, unrecoverable backup/restore, or duplicate remote mutation under supported concurrency. P1 covers an unusable or materially misleading supported installation, migration, core lifecycle, sync, diagnostics, destructive, or recovery path without a safe workaround. Any open P0/P1 blocks release; lower-severity acceptance requires a documented workaround and follow-up issue.

## 1. Content freeze

- [ ] Intended changes landed on the repository default branch; CI green on the commit to tag.
- [ ] `CHANGELOG.md` Unreleased section rewritten into a dated version section; links for upgrade impact.
- [ ] Reviewed notes exist at `docs/releases/vVERSION.md`; the release workflow rejects missing notes and publishes this file verbatim.
- [ ] User-facing docs updated (`README.md`, `docs/installation.md`, contracts as needed).
- [ ] Compat matrix (`docs/compatibility.md`) still accurate for claimed platforms/deps.
- [ ] Material changes after the frozen SHA invalidate affected evidence and require a new candidate.

## 2. Local verification

```bash
go fmt ./...
test -z "$(gofmt -l .)"
go test ./...
go test -race ./...
go vet ./...
govulncheck ./...
./scripts/smoke.sh --skip-checks
KANBI_SOAK_SECONDS=60 ./scripts/soak-runtime.sh
go test ./internal/harness ./internal/runtime ./internal/storage ./internal/diagnostics
```

- [ ] Diagnostics redaction and degraded support-bundle checks pass with synthetic secret/content canaries.
- [ ] Applicable real harness, Herdr, GitHub, and Jira checks pass, or each unavailable check has a recorded reason and equivalent evidence. A skip is not a pass.

## 3. Local release artifact snapshot

```bash
version=0.3.0-beta.1
./scripts/build-release.sh "$version"
archive="./dist/kanbi_${version}_$(go env GOOS)_$(go env GOARCH).tar.gz"
tar -tzf "$archive"
work=$(mktemp -d) && trap 'rm -rf "$work"' EXIT
tar -xzf "$archive" -C "$work"
./scripts/release-artifact-smoke.sh "$work/kanbi" "$version" "$(git rev-parse HEAD)" "$work/BUILDINFO.json"
(
  cd dist
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c SHA256SUMS
  else
    shasum -a 256 -c SHA256SUMS
  fi
)
```

Confirm provenance ldflags and packaged `BUILDINFO.json`:

- `internal/buildinfo.Version`
- `internal/buildinfo.Commit` (full git SHA)
- `internal/buildinfo.BuildDate` (UTC RFC3339)
- archive membership: `kanbi`, `README.md`, `LICENSE`, `BUILDINFO.json`

## 4. Candidate workflow and recovery

- [ ] Dispatch `.github/workflows/release.yml` for the frozen SHA before tagging; record the full SHA and run URL in a durable release issue.
- [ ] Confirm the verify job and all four native jobs pass, then download the combined bundle.
- [ ] Verify `BUNDLE_MANIFEST.txt`, `SHA256SUMS`, and every archive's `BUILDINFO.json` against the frozen SHA, version, platform, and schema.
- [ ] For every schema-changing release, run the prior published artifact against the exact candidate artifact before tagging:

```bash
OLD_ARCHIVE=/path/to/prior-release.tar.gz \
NEW_ARCHIVE=/path/to/exact-candidate.tar.gz \
./scripts/upgrade-rollback-drill.sh
```

Attach the ignored `dist/verification/` result to the release issue. The drill must cover migration, downgrade rejection, backup/restore, attachments, and complete session history.

## 5. Tag and publish

- [ ] Confirm there are no open P0/P1 defects.
- [ ] Create an annotated immutable tag on the verified commit (`v0.3.0-beta.1` for a beta; `vX.Y.Z` for an authorized stable release).
- [ ] Push the tag and confirm the release workflow reruns successfully for that exact SHA.
- [ ] Confirm release assets: four archives + `SHA256SUMS` + `BUNDLE_MANIFEST.txt`.
- [ ] Confirm every `v0.x` or suffixed release is marked GitHub prerelease and does not replace the latest stable release.

## 6. Post-publish smoke

- [ ] Download one published archive, verify checksum, and install to a throwaway PATH entry.
- [ ] Run `version`, `doctor`, database initialization, and a disposable board/session smoke.
- [ ] Record the post-publication result in the GitHub Release or release issue.

## 7. Stable-only gates

Before setting `KANBI_STABLE_RELEASE_APPROVAL` or publishing a stable major release:

- [ ] Run all deterministic and applicable real Herdr, harness, GitHub, and Jira checks on the exact candidate; document unavailable environments and equivalent evidence without calling a skip a pass.
- [ ] Complete a multi-day, multi-process operational soak without data loss, duplicate mutation, persistent lock contention, or unbounded resource growth.
- [ ] Run upgrade, downgrade-rejection, backup, restore, and rollback checks using the prior tagged artifact and exact candidate artifact.
- [ ] Validate all four native artifacts and complete the keyboard-only, 80x24, light/dark, and degraded-state UX matrix in [`ux-readiness.md`](./ux-readiness.md).
- [ ] Search diagnostics/support bundles for synthetic credential and private-content canaries, including degraded operation.
- [ ] Confirm zero open P0/P1 issues and have an external user complete checksum/install, first run, session start/close/resume or repair, backup, and restore.
- [ ] Record final approval as `VERSION@FULL_COMMIT_SHA`, then set the repository approval variable only for that candidate. Remove or change it after publication.

## 8. Immediate response kit

If regression appears:

1. Yank or mark the GitHub Release if severe.
2. Publish a fix patch release when ready.
3. Document rollback via prior binary + `kanbi restore --force` of pre-upgrade backup.

## Provenance requirements

Every published archive must allow a maintainer to identify:

| Field | Source |
| --- | --- |
| Exact source commit | `kanbi version` commit + GitHub tag target + `BUILDINFO.json` |
| Toolchain | Go version from `go.mod` / build environment / `BUILDINFO.json` |
| Binary checksum | `SHA256SUMS` beside archives |
| Schema compatibility | `database schema` line in `kanbi version` and `BUILDINFO.json` `schema_version` |
| Verification status | green release workflow + linked release issue/checklist |

Workflow and `scripts/build-release.sh` inject version/commit/date via `-ldflags -X`.
