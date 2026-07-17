# Security Policy

## Supported versions

Kanbi is a UX-ready beta. Until a stable release exists, security fixes target the **latest published beta prerelease** on GitHub Releases. Beta support is best-effort and may require upgrading to a newer beta. After stable publication, the latest non-prerelease stable line becomes the supported default.

| Version line | Supported |
| --- | --- |
| Latest published `v0.x` beta/prerelease | Best-effort; upgrade to the latest beta |
| Latest stable release, once available | Yes |
| Older beta/patch/minor releases | No (upgrade to latest applicable channel) |
| Untagged `dev` / local builds | Best-effort only |

Database schema migrations are forward-only. Restoring a pre-upgrade backup is the supported rollback path when a newer release is unusable; see `docs/installation.md`.

## Reporting a vulnerability

**Do not open a public GitHub Issue for security vulnerabilities.**

Prefer one of:

1. GitHub Security Advisories / private vulnerability reporting for this repository (if enabled on the repo): use *Security → Report a vulnerability*.
2. Email the maintainer listed on the GitHub profile for `carlotran4/kanbi` with a clear subject such as `[SECURITY] Kanbi …`.

Please include:

- Kanbi version (`kanbi version`), OS/arch, and how you installed it;
- a description of the issue and its security impact;
- minimal reproduction steps or a proof of concept;
- whether the issue is already public elsewhere;
- an optional redacted `kanbi support-bundle` (inspect it before sending — never include real tokens).

Do **not** send live production credentials, private ticket contents you do not control, or unnecessary personal data.

## Response expectations

This is a small maintained project. Reasonable targets (not contractual SLAs):

| Stage | Target |
| --- | --- |
| Acknowledge receipt | Within 7 days |
| Initial severity triage | Within 14 days of a usable report |
| Fix or mitigation guidance for confirmed issues in the latest release | As soon as practical; coordinated disclosure preferred |

Complex issues may take longer. We may ask for more detail or a private test branch.

## Disclosure process

1. Reporter submits privately.
2. Maintainer confirms the issue, agrees on severity, and prepares a fix when validated.
3. A fixed release is published when ready, with credit if the reporter wants it.
4. Public disclosure (advisory, release notes, or issue) should wait until a fixed release is available **or** 90 days after the report, whichever comes first, unless both parties agree otherwise.
5. Actively exploited issues may require faster public guidance.

## Security-relevant product behavior

- Config files and SQLite database/WAL/SHM files are restricted to mode `0600` when Kanbi manages them; application directories default to `0700`.
- Provider tokens should live in environment variables, not board `--config` JSON (JSON is stored unencrypted in SQLite).
- Diagnostic logs and support bundles are designed to redact credentials and to exclude ticket bodies, prompts, harness session refs, attachments, and terminal captures by default. Callers and users must still treat them as private and review output before sharing.
- There is no persistent background daemon; remote sync is in-process only.

## Out of scope

- Security of third-party agent CLIs (Pi, Codex, Copilot, Claude) or multiplexers (tmux, Herdr)
- Security of remote ticket providers (GitHub, Jira)
- Issues that require physical access to an unlocked user account with local file read rights
- Social engineering against repository maintainers

## Safe harbor

Good-faith security research that avoids privacy violations, service degradation, and data destruction is welcome. Do not attempt to access data that is not yours or to publish unrestricted public exploit details before coordinated disclosure.
