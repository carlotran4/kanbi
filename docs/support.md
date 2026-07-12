# Support And Maintenance Policy

## Goals

When Kanbi fails, a user should be able to:

1. identify the failing subsystem (`kanbi doctor`, runtime diagnostics, logs);
2. generate a **redacted** support bundle;
3. report an issue with enough version/runtime context for a maintainer to reproduce.

Kanbi never requires sharing ticket bodies, notes, prompts, tokens, session refs, attachments, or terminal transcripts for a default report.

## Supported releases

- **Current semantic release** on GitHub Releases is the primary supported line.
- Security fixes are backported only to releases explicitly listed as still supported in `SECURITY.md` / release notes (beta may mean “latest only”).
- Pre-release / `dev` builds and unchecked-out development launchers are best-effort.
- Schema migrations are forward-only. Rolling back the binary without restoring a pre-upgrade backup is unsupported.

## How to get help

1. Run `kanbi version` and `kanbi doctor`.
2. Read [`docs/installation.md`](./installation.md) and the relevant architecture/lifecycle doc for the subsystem.
3. Search existing GitHub Issues.
4. Open a bug report with the template under `.github/ISSUE_TEMPLATE/`.
5. Optionally attach a support bundle (inspect it yourself first).

Security vulnerabilities must follow [`SECURITY.md`](../SECURITY.md), not public issues.

## Diagnostics users can enable

### Opt-in file logging

Logging is **off by default**. Enable temporarily:

```yaml
# ~/.config/kanbi/config.yaml
diagnostics:
  level: debug   # off | error | warn | info | debug
  max_bytes: 1048576
  max_files: 3
```

Or for one shell:

```bash
export KANBI_LOG_LEVEL=debug
```

Behavior:

- Writes `kanbi-diagnostics.log` under the configured state directory (`$XDG_STATE_HOME/kanbi` or `$KANBI_STATE_DIR`).
- File mode `0600`, directory mode `0700` when Kanbi creates them.
- Structured JSON lines with `time`, `severity`, `subsystem`, `operation`, `correlation`, `message`, optional `fields`.
- Bounded size with numbered rotation (`*.1`, `*.2`, …); no daemon process.
- Call sites must redacting-sensitive data; still treat logs as private.

Disable by setting `level: off` or unsetting `KANBI_LOG_LEVEL`.

### Durable runtime diagnostics

SQLite table `runtime_diagnostics` stores redacted failure rows for sync/runtime reconciliation (operation, board/ticket ids, attempt, message, cause). Causes run through `RedactSecretText` before insert. Ticket bodies and prompts are never written by design.

### Support bundle

```bash
kanbi support-bundle ~/kanbi-support.zip
kanbi support-bundle ~/kanbi-support.zip --json   # also print JSON summary to stdout
```

Creates a versioned, path-safe zip (`format: kanbi-support-bundle`) without mutating application state and without requiring a healthy multiplexer or provider.

#### Fields collected (default)

| Field / path | Contents | Redaction / inclusion policy |
| --- | --- | --- |
| `manifest.json` | format version, Kanbi version/commit/schema | included; no secrets |
| `support-bundle.json` | full structured payload | see below; text re-redacted |
| `README.txt` | human summary | redacted details only |
| `field-policies.json` | this policy inventory | included |
| `build.*` | version, commit, build date, go, os/arch, schema | included |
| `platform.*` | OS/arch, TERM, shell, inside-tmux, limited env | home→`~`; tokens env values `[REDACTED]` |
| `paths.*` | config/data/state/db paths | home redacted |
| `config_redacted` | parsed user YAML | token/secret/password keys `[REDACTED]`; URLs sanitized |
| `multiplexer.*` | default runtime, tmux/Herdr non-secret settings | included |
| `migrations` | schema_migrations version/name/applied_at | included when DB openable |
| `schema_version` | max applied / binary schema | included |
| `doctor` | severity/name/detail/error | redacted text |
| `runtime_diagnostics` | durable redacted failure rows | no bodies/prompts/session refs |
| `harnesses` | binary presence only | no auth state |
| `recent_diagnostics_log` | tail of opt-in log if present | redacted |
| `degraded` | collection problems | included |

#### Never included by default

- Ticket titles/bodies, notes, prompts
- Harness session refs / resume handles as free text (matched patterns redacted)
- Attachment file contents
- Raw terminal / pane captures
- Provider tokens, API keys, `Authorization` headers
- Full environment dumps

#### Inspect before sharing

```bash
unzip -l ~/kanbi-support.zip
unzip -p ~/kanbi-support.zip README.txt
unzip -p ~/kanbi-support.zip support-bundle.json | less
```

If anything looks sensitive, do not upload it. Re-generate after removing secrets from config/env.

## Maintenance expectations

Maintainers aim to:

- keep `docs/compatibility.md` accurate when support claims change;
- run the baseline verification loop before releases (`docs/release-checklist.md`);
- exercise upgrade/rollback with `scripts/upgrade-rollback-drill.sh` when schema or release packaging changes;
- respond to security reports per `SECURITY.md`.

Community contributions are welcome per [`CONTRIBUTING.md`](../CONTRIBUTING.md).

## Related commands

| Command | Purpose |
| --- | --- |
| `kanbi version [--json]` | Build/schema identity |
| `kanbi doctor [--json]` | Prerequisites and config probes |
| `kanbi support-bundle PATH` | Redacted diagnostic archive |
| `kanbi backup` / `kanbi restore` | Full data recovery (contains private board data — not a support bundle) |
