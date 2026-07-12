# Release Checklist

Use this list before tagging a semantic release (`vMAJOR.MINOR.PATCH`). For the first stable release, this checklist is necessary but not sufficient: every gate in [`verification/stable-v1.0-qualification.md`](./verification/stable-v1.0-qualification.md) must be complete for the exact candidate commit. Until then, do not create or publish `v1.0.0`.

## Release blockers

P0 covers security/credential leakage, durable-data or history loss/corruption, unsafe active-work termination, unrecoverable backup/restore, or duplicate remote mutation under supported concurrency. P1 covers an unusable or materially misleading supported installation, migration, core lifecycle, sync, diagnostics, destructive, or recovery path without a safe workaround. Any open P0/P1 blocks release; lower-severity acceptance requires a documented workaround and follow-up issue.

## 1. Content freeze

- [ ] Intended changes landed on the main branch; CI green on the commit to tag.
- [ ] `CHANGELOG.md` Unreleased section rewritten into a dated version section; links for upgrade impact.
- [ ] User-facing docs updated (`README.md`, `docs/installation.md`, contracts as needed).
- [ ] Compat matrix (`docs/compatibility.md`) still accurate for claimed platforms/deps.

## 2. Local verification

```bash
go fmt ./...
test -z "$(gofmt -l .)"
go test ./...
go test -race ./...
go vet ./...
govulncheck ./...
./scripts/smoke.sh --skip-checks
```

Optional when storage/lifecycle touched:

```bash
go test ./internal/harness ./internal/tmux ./internal/storage ./internal/diagnostics
./scripts/upgrade-rollback-drill.sh
```

## 3. Local release artifact snapshot

```bash
./scripts/build-release.sh X.Y.Z
tar -tzf ./dist/kanbi_X.Y.Z_$(go env GOOS)_$(go env GOARCH).tar.gz
# extract and run:
#   ./kanbi version
# expect version, commit, UTC build date, platform, database schema
# confirm BUILDINFO.json matches version/commit/schema_version fields
sha256sum -c dist/SHA256SUMS --ignore-missing 2>/dev/null || true
```

Confirm provenance ldflags and packaged `BUILDINFO.json`:

- `internal/buildinfo.Version`
- `internal/buildinfo.Commit` (full git SHA)
- `internal/buildinfo.BuildDate` (UTC RFC3339)
- archive membership: `kanbi`, `README.md`, `LICENSE`, `BUILDINFO.json`

## 4. Tag and publish

- [ ] Record the full frozen commit SHA and qualification evidence before tagging.
- [ ] Confirm there are no open P0/P1 defects.
- [ ] Create annotated tag `vX.Y.Z` on the verified commit.
- [ ] Push tag to GitHub to trigger `.github/workflows/release.yml`.
- [ ] Confirm workflow: verify job (fmt/tests/race/vet/govulncheck/smoke) then native builds for linux/darwin amd64/arm64.
- [ ] Confirm release assets: four archives + `SHA256SUMS`.
- [ ] Download all four artifacts and confirm each `BUILDINFO.json` and `kanbi version` commit equals the tag target SHA; record native checks in the release evidence matrix.

## 5. Post-publish smoke

- [ ] Download one archive, verify checksum, install to a throwaway PATH entry.
- [ ] `kanbi doctor` on a clean data dir.
- [ ] Open board UI once, or run CLI `boards`/`list` smoke.
- [ ] For schema-changing releases, run `./scripts/upgrade-rollback-drill.sh` and keep the log under `docs/verification/` if requested.

## 6. Immediate response kit

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
| Verification status | green release workflow + checklist above |

Workflow and `scripts/build-release.sh` inject version/commit/date via `-ldflags -X`.
