# Changelog

Kanbi follows [Semantic Versioning](https://semver.org/). Release-specific upgrade notes are also published with each GitHub Release.

## Unreleased

Next planned prerelease: `v0.3.0-beta.1` (not yet qualified or published).

### Added

- Opt-in bounded diagnostics file logging (`diagnostics.level` / `KANBI_LOG_LEVEL`) with `0600` permissions, rotation, and structured JSON records.
- `kanbi support-bundle PATH` for redacted, path-safe diagnostic archives usable in degraded environments.
- Expanded secret redaction for tokens, headers, URLs, session-ref patterns, and config/env sanitization with automated tests.
- Compatibility matrix, support policy, security policy, contribution guide, issue templates, and release checklist.
- Release `BUILDINFO.json` provenance alongside archives and checksums; upgrade/rollback drill script (`scripts/upgrade-rollback-drill.sh`).
- Beta-to-stable release channels with prerelease-safe publishing, four-platform exact-binary smoke validation, combined candidate bundles, and stable-release workflow locking.
- Schema migration v5: board UUID/archive/sync flags, column `workflow_key`, and Master filter presets.
- Board archive/unarchive and enable/disable-sync via CLI and TUI (archive pauses sync; unarchive does not auto-enable).
- Versioned `kanbi-board-package` export/import with preview, path safety, checksum inventory, create-new-only remap, and attachment rollback.
- Master aggregation by workflow key; `kanbi boards set-column-key` maps display columns to keys.
- Master filter presets persisted in SQLite by board UUID (never auto-applied on startup).
- Production release archives for Linux and macOS on x86-64 and ARM64.
- Build/version and database-schema reporting.
- Root and per-command CLI help.
- Installation, first-run, upgrade, rollback, and uninstall guidance.
- MIT licensing.

### Fixed

- Ticket-body `Ctrl+V` now reads desktop clipboard images asynchronously, falls back to normal text paste, and copies pasted image file paths into durable attachment storage.

### Changed

- Local ticket archive no longer closes GitHub issues; only terminal columns (`Done`/`Closed`) push closed.
- Archived / sync-disabled boards are skipped by sync manager and CLI reports `sync_skipped`.
- Configured Herdr availability and unknown multiplexer values are fatal doctor results.
- The Go module now uses its canonical GitHub import path.

## Pre-release alpha

Kanbi established multi-board SQLite-backed ticket management, tmux and optional Herdr runtime adapters, Pi/Codex/Copilot/Claude harness lifecycle support, remote GitHub/Jira synchronization, backup/restore, and production-safety checks. Earlier alpha work was not published under a stable versioned release contract.
