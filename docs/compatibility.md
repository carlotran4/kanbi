# Compatibility Matrix

This document states which platforms, dependencies, and schema versions Kanbi
claims to support, and how those claims are verified. Claims that are only
documented or manually verified are labeled as such — they are not automatic
CI guarantees.

## Product versions

| Component | Supported | Verification |
| --- | --- | --- |
| Kanbi release artifacts | Semantic tags `vMAJOR.MINOR.PATCH` on GitHub Releases | Automated native CGO builds in `.github/workflows/release.yml`; local `./scripts/build-release.sh` |
| Go toolchain | Version in root `go.mod` | CI `setup-go` with `go-version-file: go.mod` |
| Module path | `github.com/carlotran4/kanbi` | Build system / docs |

## Operating systems and architectures

| Platform | Status | Verification |
| --- | --- | --- |
| Linux x86-64 (`linux_amd64`) | Supported release target | CI smoke + release build |
| Linux ARM64 (`linux_arm64`) | Supported release target | Release workflow native runner |
| macOS Intel (`darwin_amd64`) | Supported release target | Release workflow native runner |
| macOS Apple Silicon (`darwin_arm64`) | Supported release target | Release workflow native runner |
| Windows | Not a published release target | Not verified |
| FreeBSD / other Unix | Not supported | Not verified |

Published archives are native CGO-enabled binaries (SQLite via `go-sqlite3`). Cross-compiling without the matching C toolchain is unsupported.

## Terminal expectations

| Expectation | Status | Verification |
| --- | --- | --- |
| UTF-8 locale | Required for correct text | Documented; exercised in interactive use |
| 256-color terminal | Recommended | Documented; TUI styles degrade without theme colors |
| Minimum practical size | 80×24 controls remain reachable | UX matrix in `docs/ux-readiness.md` (manual) |
| Kitty graphics protocol | Optional image previews | Documented optional; terminal detection tested in unit tests |
| Non-interactive stdout/JSON CLI | Supported | Deterministic CLI tests |

Kanbi is a keyboard-first TUI. Pure headless automation should use the CLI (`--json`) rather than driving the TUI.

## Multiplexer runtimes

| Multiplexer | Status | Verification |
| --- | --- | --- |
| tmux (default) | Supported | Deterministic manager tests + `scripts/smoke.sh` real tmux |
| Herdr (opt-in) | Supported when configured | Deterministic fake-Herdr doctor/smoke probes; **real Herdr lifecycle is opt-in manual only** |

`kanbi doctor` treats a selected Herdr installation that is missing or whose `herdr status` fails as fatal. Existing tmux sessions remain controllable only when `tmux` is installed.

Recommended: a maintained tmux 3.x series. Exact distro package versions are not pinned; smoke validates whichever `tmux` is on `PATH` in CI.

## SQLite schema

| Item | Status | Verification |
| --- | --- | --- |
| Schema version | `storage.CurrentSchemaVersion()` reported by `kanbi version` / doctor | Migration tests + release metadata |
| Migrations | Forward-only; older binary refuses newer schema | `internal/storage` migration tests |
| Rollback | Restore pre-upgrade backup with older binary; no in-place downgrade | Documented upgrade/rollback + `scripts/upgrade-rollback-drill.sh` |

Schema version is embedded in release artifacts and support bundles.

## Agent harnesses

Harness adapters are compiled-in. Presence of the binary is probed by doctor; authentication and quota are outside Kanbi.

| Harness | Binary | Verified command surface | Ref capture verification | Real CLI status |
| --- | --- | --- | --- | --- |
| Pi | `pi` | Documented in `docs/harness-contracts.md` | Extension + fallback unit/integration tests | Real lifecycle opt-in via `scripts/real-harness-lifecycle.sh` |
| Codex | `codex` | Documented | History JSONL unit test | Real lifecycle opt-in |
| Copilot | `copilot` | Documented | Session-store unit test | Real lifecycle opt-in |
| Claude | `claude` | Documented | Projects JSONL unit test | Real lifecycle opt-in |
| Fake/smoke | scripts | Full lifecycle | Pane marker `SESSION_REF=` | Automated CI smoke |

When a real harness CLI changes its flags or history layout, contracts may need updates. Published “supported” means the command surface in `docs/harness-contracts.md` — not every upstream patch version.

## Ticket backends

| Backend | Status | Verification |
| --- | --- | --- |
| `local` | Supported | Default tests |
| `github` | Supported | Deterministic fake client tests; real smoke opt-in (`scripts/github-backend-smoke.sh`) |
| `atlassian` (Jira) | Supported | Deterministic tests; real smoke opt-in (`scripts/jira-backend-smoke.sh`) |

Provider API versions are whatever the live service returns for the documented calls (GitHub Issues REST; Jira Cloud REST). Exact third-party API version pins are not claimed.

## Diagnostics and support tools

| Feature | Status | Verification |
| --- | --- | --- |
| Opt-in diagnostics log (`diagnostics.level` / `KANBI_LOG_LEVEL`) | Supported | Unit tests for 0600 mode, rotation, structured fields |
| `kanbi support-bundle PATH` | Supported | Unit tests for redaction, degraded DB, archive path safety |
| `kanbi doctor` | Supported | Unit probes + CLI tests |
| `kanbi version --json` | Supported | CLI/meta tests |

## What “supported” means

- **Release target**: CI or release automation builds and checksums an artifact for that OS/arch.
- **Automated verification**: Covered by `go test`, `scripts/smoke.sh`, or other non-interactive jobs in CI.
- **Opt-in / manual**: Requires local binaries, auth, quota, or human judgment. Absence of a green automated job does not invalidate the documented contract, but also does not prove every environment works.
- Unsupported platforms and backends must not be advertised as production-ready without updating this matrix and verification.

## Related docs

- [`docs/installation.md`](./installation.md) — install, upgrade, rollback
- [`docs/harness-contracts.md`](./harness-contracts.md) — exact harness commands
- [`docs/multiplexer-contracts.md`](./multiplexer-contracts.md) — tmux/Herdr contract
- [`docs/support.md`](./support.md) — diagnostics content and support process
- [`docs/release-checklist.md`](./release-checklist.md) — release provenance steps
