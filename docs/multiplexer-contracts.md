# Multiplexer Contracts

Kanbi launches agent harnesses inside a configured multiplexer runtime substrate. The multiplexer owns the terminal container; the harness owns the agent command and any harness-native session ref.

## Terminology

- **Configured multiplexer**: the runtime substrate selected by `multiplexer.default` in config.
- **Terminal container**: the live process container for one active Kanbi session.
- **tmux window**: the terminal container type used by the default `tmux` implementation.
- **Herdr pane/agent**: the terminal container type used by the optional `herdr` implementation.
- **Harness**: the agent CLI adapter (`pi`, `codex`, `copilot`, `claude`, fake smoke harnesses). Harnesses are separate from multiplexers.

## Adapter Contract

A multiplexer adapter must provide these operations for one terminal container:

| Operation | Contract |
| --- | --- |
| Launch | Start a harness command in the ticket board working directory and return a durable container reference. |
| Validate | Confirm that a durable container reference still identifies the expected live container in its stored namespace. Validation failures during startup reconcile are persisted as redacted runtime diagnostics and surface as **runtime reconciliation degraded (local data available)** rather than being discarded. |
| Focus | Bring an existing live container to the user without creating a new session row. |
| Read | Return recent terminal output for pattern detection and session-ref capture. |
| Send text | Deliver literal text without interpreting it as shell input or key names. |
| Send keys | Deliver harness exit keys or user input to the container. |
| Close | Close the container after graceful harness exit attempts. |
| Detect | Report provider-native state when available, otherwise allow pane-output fallback. |

Session rows persist the generic fields `multiplexer`, `mux_namespace`, `mux_container_id`, `mux_container_name`, and `mux_metadata`. Legacy tmux fields remain populated for tmux sessions and are backfilled into generic fields during migration. Repository integration agents use the same adapter launch/focus/read/close contract, but their container metadata belongs to `integration_runs`; Kanbi never creates a synthetic ticket session for them.

## tmux Behavior

`tmux` is the default implementation. Kanbi creates or reuses the configured tmux runtime session, keeps the board UI in a stable `board` window, and starts each ticket session in a separate tmux window.

A tmux container is valid only when live tmux still reports the stored window id with the expected ticket window name. Name fallback must use the session row's stored tmux session name, not the current process's runtime session.

## Herdr Behavior

`herdr` is optional and opt-in through config:

```yaml
multiplexer:
  default: herdr
  herdr:
    binary: herdr
    session: default
    workspace_strategy: board
    focus_on_open: false
```

Kanbi launches harness commands through the Herdr CLI. Herdr concepts map as follows:

| Herdr concept | Kanbi use |
| --- | --- |
| Session | Runtime namespace selected by `multiplexer.herdr.session` / `HERDR_SESSION`. |
| Workspace | Board/project-level container, depending on workspace strategy. |
| Pane | Terminal container that runs the harness command. |
| Agent | Herdr-detected coding agent identity/state inside the pane. |

For current Herdr releases, Kanbi creates a dedicated tab with a shell pane in the selected workspace and working directory without focusing it, then starts the harness with Herdr's pane-first `agent start --kind <harness> --pane <id>` contract. Creating the tab directly avoids temporarily splitting the board pane and collapsing the home screen to half width during launch. A newly created Herdr pane can briefly report `agent_pane_busy` before its shell becomes available, so Kanbi retries that specific transient response for a bounded interval; other launch errors fail immediately and clean up the pane. Kanbi focuses the tab only after launch and initial prompt delivery succeed, preventing failed cleanup from returning the user to an unrelated existing agent. Initial ticket and integration prompts are delivered through literal pane input after the harness is ready rather than encoded as `agent start` arguments; this supports multiline Markdown without Herdr shell-argument rejection. The internal Herdr agent identifier is normalized to Herdr's lowercase 32-character format and includes a pane-bound suffix so retries cannot collide with completed Herdr agents, while the full ticket label remains the tab/container display name. Integration-run environment variables are applied while creating that pane. This Herdr API selects the harness's canonical executable; custom executable paths or wrapper commands must use the tmux multiplexer instead of being silently replaced. Before each launch Kanbi checks the local `agent start --help` capability surface; older Herdr releases that do not expose both `--kind` and `--pane` continue to use the legacy `agent start --cwd ... --workspace ... -- <command>` path, including its configured command.

When Herdr reports an agent state, Kanbi prefers that native state for both ticket sessions and repository integration runs. If Herdr state is unavailable or `unknown`, Kanbi falls back to reading pane output and applying harness/pattern detection. For integration runs, native and transcript observation can project only active attention states; authenticated integration reports remain the only completion/readiness signal. Explicit Herdr `agent_not_found` and `pane_not_found` responses are authoritative container absence rather than generic command failures; Kanbi deactivates that ticket attempt as exited/resumable or repair-needed according to whether a verified harness session ref exists.

## Doctor And Verification

`kanbi doctor` reports build/schema compatibility and the configured multiplexer. It always preserves existing tmux checks used by Kanbi's default board runtime. When Herdr is selected, doctor also checks the configured Herdr binary and runs `herdr status`; a missing or unreachable configured Herdr installation is fatal with setup guidance. An unknown configured multiplexer is also fatal. Missing optional harness commands remain warnings.

Normal tests and `scripts/smoke.sh` use fake harnesses and a fake Herdr doctor probe. Real Herdr verification is opt-in only: install Herdr, configure `multiplexer.default: herdr`, then run targeted manual lifecycle checks in a disposable board/workspace.
