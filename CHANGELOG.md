# Changelog

Kanbi follows [Semantic Versioning](https://semver.org/). Release-specific upgrade notes are also published with each GitHub Release.

## Unreleased

### Added

- Board-scoped ticket templates with keyboard-first `N` creation and `T` management flows, Master board routing, CLI CRUD and `kanbi add --template`, snapshot semantics, provider-safe single creation, and board-package portability.

### Changed

- Database schema advances from 8 to 9 to persist local ticket templates. Older binaries reject the migrated database; restore a pre-upgrade backup to roll back.

## [0.3.0-beta.4] - 2026-10-09

### Improved

- Keep keyboard input responsive while runtime observation and SQLite writes are in progress; reject stale refresh results while preserving selection and unsaved edits.
- Reduce named-board projection work, cache unchanged projections and Git observations, and avoid repeatedly rendering hidden descriptions and notes.
- Improve long-description editing and small-terminal inspector behavior while preserving complete saved bodies, Unicode text, and note selection.
- Raise the maximum renderer cadence to 120 FPS; unchanged frames still emit no output, with a small measured increase in idle CPU.
- Include the license for the bundled Bubbles textarea in release archives.

### Security

- Require Go 1.26.9 for patched standard-library HTTP, TLS, and MIME handling, and update indirect networking dependencies.

There is no database schema change from beta.3 (schema 8). See the [beta.4 release notes](./docs/releases/v0.3.0-beta.4.md) for upgrade, rollback, and remaining beta limitations.


## [0.3.0-beta.3] - 2026-07-29

Beta release adding global Focus Mode, project-file completion, and safer session repair while consolidating runtime, storage, CLI, and modal behavior behind shared application seams.

### Added

- A global `F` settings flow for enabling/disabling Focus Mode and editing its limit/workflow keys without manually changing YAML.
- Opt-in Global Focus Mode (GH-334): a configurable cross-board commitment limit, SQLite-serialized admission/replacement, provider-overflow visibility, required pause handoffs, resume briefs and structured handoff delivery, focus/paused card sections, and portable append-only checkpoint history.
- Project-file completion in ticket bodies and notes: type `@` to search the ticket worktree or board directory and insert a textual relative path.
- A tab-accessible harness session-ref field in the ticket editor when a verified ref already exists, allowing repair without flattening session history.

### Fixed

- Submit Pi's initial positional prompt explicitly so supported Pi versions start the requested turn instead of leaving it in the editor.
- Standardize single-line editing behavior and preserve bounded cursor/view state across ticket, board, filter, integration, focus, and session-repair modals.
- Keep Focus Mode replacement, pause, and resume modals responsive and bounded through 80-column layouts and live resize; preserve ticket selection and paused-section labels while grouped cards reorder or scroll.
- Preserve overflow warnings with long board names, separate archived history from focus commitments, progressively resolve provider overflow, and carry resume-and-send handoffs through repair/start-fresh.
- Retry Herdr's transient `agent_pane_busy` response while a newly split shell initializes, prefer a non-agent pane as the split anchor, and defer focus until agent launch succeeds so opening a new ticket cannot appear to switch to an existing agent after failed cleanup.
- Schedule and drain owning-board provider sync after CLI ticket creation, update, movement, and note addition, matching TUI mutation behavior and preventing pending remote updates from being abandoned at command exit.
- Reject non-regular, traversing, oversized, backslash-path, symlinked, or invalid imported data at shared backup and board-package archive boundaries.
- Keep repository integration readiness/completion bound to authenticated reports; native and transcript runtime observation can project attention states only.
- Centralize archive validation, CLI ticket mutations, runtime-state semantics, and runtime/workspace boundaries so TUI and CLI paths enforce the same lifecycle and storage rules.

This release migrates databases from schema 7 to schema 8 to store append-only Focus Mode pause checkpoints. See the reviewed [beta.3 release notes](./docs/releases/v0.3.0-beta.3.md) for compatibility, migration, rollback, support, and security guidance.

## [0.3.0-beta.2] - 2026-07-23

Corrective beta superseding `v0.3.0-beta.1`, which accidentally omitted two completed Herdr compatibility commits from its frozen candidate.

### Fixed

- Launch agents against current Herdr releases through the pane-first `agent start --kind <harness> --pane <id>` contract while preserving a capability-detected legacy fallback.
- Normalize internal Herdr agent names to the required lowercase 32-character format, with a pane-bound suffix that prevents retry collisions, without shortening ticket tab/container labels.
- Preserve integration environment variables during Herdr pane creation and reject unsupported custom pane-first harness executables instead of silently substituting another command.
- Deliver multiline ticket and integration prompts through literal Herdr pane input after readiness instead of unsafe `agent start` argument encoding.

There is no schema change from beta.1. See the reviewed [beta.2 release notes](./docs/releases/v0.3.0-beta.2.md) for compatibility, migration, rollback, support, and security guidance.

## [0.3.0-beta.1] - 2026-07-23

First public beta. This release defines a testable beta surface; it does not claim feature completeness or stable `v1.0.0` readiness. See the reviewed [beta release notes](./docs/releases/v0.3.0-beta.1.md) for install, migration, rollback, support, and security guidance.

### Added

- Agent-assisted repository integration runs: press `I` to select ticket worktrees, supervise a dedicated integration agent, accept a structured candidate report, and safely promote the verified combined result.
- Starship-style custom status bars with left, center, and right module zones plus bounded periodically refreshed command modules (GH-325).
- Opt-in bounded diagnostics file logging (`diagnostics.level` / `KANBI_LOG_LEVEL`) with `0600` permissions, rotation, and structured JSON records.
- `kanbi support-bundle PATH` for redacted, path-safe diagnostic archives usable in degraded environments.
- Expanded secret redaction for tokens, headers, URLs, session-ref patterns, and config/env sanitization with automated tests.
- Compatibility matrix, support policy, security policy, contribution guide, issue templates, and release checklist.
- Release `BUILDINFO.json` provenance alongside archives and checksums; upgrade/rollback drill script (`scripts/upgrade-rollback-drill.sh`).
- Beta-to-stable release channels with prerelease-safe publishing, four-platform exact-binary smoke validation, combined candidate bundles, and stable-release workflow locking.
- Schema migrations v5–v7: board UUID/archive/sync flags, column `workflow_key`, Master filter presets, ticket workspaces/session launch directories, and repository integration runs.
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

- Runtime attention detection no longer auto-closes active agent sessions; waiting and permission states remain open until explicitly closed or archived (GH-322).
- Ticket-body `Ctrl+V` now reads desktop clipboard images asynchronously, falls back to normal text paste, and copies pasted image file paths into durable attachment storage.
- Concurrent provider sync preserves ticket moves, coalesces scheduling storms, paginates Jira results, excludes GitHub pull requests, and binds provider columns by workflow key.
- Session lifecycle claims reject archived tickets and keep prompt/ref capture bound to the exact launch attempt.
- Backup restore rejects symlinked attachment destinations, while board-package imports normalize ticket counters safely.
- Release smoke avoids `pipefail`/SIGPIPE false failures, and the vulnerable `golang.org/x/text` dependency is upgraded to a fixed version.

### Changed

- Local ticket archive no longer closes GitHub issues; only terminal columns (`Done`/`Closed`) push closed.
- Archived / sync-disabled boards are skipped by sync manager and CLI reports `sync_skipped`.
- Configured Herdr availability and unknown multiplexer values are fatal doctor results.
- The Go module now uses its canonical GitHub import path.

### Important missing features and known limitations

- Stable qualification remains blocked on the broader multi-day operational soak, native/external matrices, accessibility/usability review, and explicit product-readiness approval.
- Homebrew, distro, Windows, and other Unix packages are not available; install one of the four native archives manually.
- Some real Herdr, harness, GitHub, and Jira combinations remain opt-in/manual checks because they require local authentication, quota, and disposable external resources.
- Resume requires a verified harness session ref. The TUI repair flow can enter one or start fresh, but a separate non-interactive session-ref management command is not yet available.
- Git-worktree mode cannot be disabled after workspace history exists; Master workflow keys have no automatic synonym mapping; board-package import does not rewrite every absolute attachment link and does not support merge-import.

## Pre-release alpha

Kanbi established multi-board SQLite-backed ticket management, tmux and optional Herdr runtime adapters, Pi/Codex/Copilot/Claude harness lifecycle support, remote GitHub/Jira synchronization, backup/restore, and production-safety checks. Earlier alpha work was not published under a stable versioned release contract.
