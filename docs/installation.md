# Installation And First Run

## Supported release platforms

Kanbi publishes native, CGO-enabled archives for:

- Linux x86-64 (`linux_amd64`)
- Linux ARM64 (`linux_arm64`)
- macOS Intel (`darwin_amd64`)
- macOS Apple Silicon (`darwin_arm64`)

A UTF-8 terminal with 256-color support is recommended. Kitty-compatible image rendering is optional. Kanbi defaults to tmux and requires `tmux` on `PATH`; Herdr is an opt-in alternative, but tmux remains recommended and is needed to control existing tmux sessions. Install at least one supported agent CLI: Pi, Codex, Copilot, or Claude, and authenticate it using that tool's own instructions.

The full matrix—including schema, harness verification status, and what is only manually verified—is in [`docs/compatibility.md`](./compatibility.md).

## Install a release

Kanbi is currently in beta. Open <https://github.com/carlotran4/kanbi/releases> and select the intended prerelease explicitly. GitHub's `/releases/latest` endpoint is reserved for a future non-prerelease stable version.

1. Open the release page and read its beta limitations and upgrade notes.
2. Download the archive matching your operating system and architecture plus `SHA256SUMS`.
3. Verify the archive before extracting:

   ```bash
   # Linux
   sha256sum -c SHA256SUMS --ignore-missing

   # macOS (verify only the archive you downloaded)
   grep "  kanbi_VERSION_OS_ARCH.tar.gz$" SHA256SUMS | shasum -a 256 -c -
   ```

4. Extract and install the binary:

   ```bash
   tar -xzf kanbi_VERSION_OS_ARCH.tar.gz
   install -m 0755 kanbi "$HOME/.local/bin/kanbi"
   export PATH="$HOME/.local/bin:$PATH"
   ```

5. Confirm the artifact and prerequisites:

   ```bash
   kanbi version
   kanbi doctor
   ```

Homebrew and system packages are not currently published. Building from source requires the Go version in `go.mod`, a C compiler, and CGO because Kanbi uses SQLite through `go-sqlite3`.

## First-run workflow

1. Install and authenticate one supported harness CLI (`pi`, `codex`, `copilot`, or `claude`).
2. Install tmux, then run `kanbi doctor`. Resolve every `fatal` result. Missing harnesses you do not intend to use are warnings.
3. Create a board rooted at the project the agent should edit:

   ```bash
   cd /path/to/project
   kanbi boards add "My Project" --cwd "$PWD"
   ```

4. Start Kanbi, select the board, and press `n` to create a ticket. Press `e` to edit its description and harness, then `Enter` to start the agent session.
5. Press `x` to close a ticket session gracefully. Pressing `q` or `Ctrl+C` exits Kanbi without terminating active ticket sessions.
6. Create a recovery archive before destructive actions or upgrades:

   ```bash
   kanbi backup "$HOME/kanbi-backup-$(date +%Y%m%d).kanbi"
   ```

Use `kanbi --help` and `kanbi COMMAND --help` to discover non-interactive workflows.

## Upgrade

1. Read [`CHANGELOG.md`](../CHANGELOG.md) and the release notes.
2. Stop all Kanbi UI/CLI processes that use the database. Active agent containers may remain running, but no Kanbi process should be writing state.
3. Create and verify a backup with the currently installed version.
4. Verify the new archive checksum and replace the binary.
5. Run `kanbi version` and `kanbi doctor`. Opening the database applies forward migrations automatically.

Kanbi never supports downgrading a database in place. An older binary refuses a schema created by a newer unsupported release. To roll back, stop Kanbi, reinstall the older binary, then run `kanbi restore PATH-TO-OLDER-BACKUP --force` to replace the newer database with the backup made by that older version. Runtime changes made after the backup will be lost. Keep the prior binary and backup until the upgraded installation has been exercised.

## Uninstall

Remove the binary from the location where it was installed. Kanbi data is intentionally retained unless you remove it explicitly:

- Config: `${XDG_CONFIG_HOME:-~/.config}/kanbi/config.yaml`
- Database and attachments: `${XDG_DATA_HOME:-~/.local/share}/kanbi/`
- Runtime state/ref handoffs: `${XDG_STATE_HOME:-~/.local/state}/kanbi/`

`KANBI_CONFIG`, `KANBI_DB`, `KANBI_DATA_DIR`, and `KANBI_STATE_DIR` may override these locations. Back up before deleting them. Kanbi does not delete tmux/Herdr containers as part of uninstall; close or remove those separately.

## Diagnostics when something fails

1. `kanbi version` and `kanbi doctor`
2. Optional: set `KANBI_LOG_LEVEL=debug` or `diagnostics.level: debug` for bounded private logs under the state directory
3. `kanbi support-bundle ~/kanbi-support.zip` — redacted, inspect before sharing ([`docs/support.md`](./support.md))

Full backups (`kanbi backup`) include private board content; do not use them as support attachments.

## Development installation

For contributors working from a checkout, `./scripts/install-dev.sh` installs a launcher that rebuilds on source changes. This is not the production release installation path. See [`CONTRIBUTING.md`](../CONTRIBUTING.md).
