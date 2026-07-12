# Changelog

Kanbi follows [Semantic Versioning](https://semver.org/). Release-specific upgrade notes are also published with each GitHub Release.

## Unreleased

### Added

- Production release archives for Linux and macOS on x86-64 and ARM64.
- Build/version and database-schema reporting.
- Root and per-command CLI help.
- Installation, first-run, upgrade, rollback, and uninstall guidance.
- MIT licensing.

### Changed

- Configured Herdr availability and unknown multiplexer values are fatal doctor results.
- The Go module now uses its canonical GitHub import path.

## Pre-release alpha

Kanbi established multi-board SQLite-backed ticket management, tmux and optional Herdr runtime adapters, Pi/Codex/Copilot/Claude harness lifecycle support, remote GitHub/Jira synchronization, backup/restore, and production-safety checks. Earlier alpha work was not published under a stable versioned release contract.
