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
| Focus | Bring an existing live container to the user without creating a new session row. |
| Read | Return recent terminal output for pattern detection and session-ref capture. |
| Send keys | Deliver harness exit keys or user input to the container. |
| Close | Close the container after graceful harness exit attempts. |
| Detect | Report provider-native state when available, otherwise allow pane-output fallback. |

Session rows persist the generic fields `multiplexer`, `mux_namespace`, `mux_container_id`, `mux_container_name`, and `mux_metadata`. Legacy tmux fields remain populated for tmux sessions and are backfilled into generic fields during migration.

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

When Herdr reports an agent state, Kanbi prefers that native state. If Herdr state is unavailable or `unknown`, Kanbi falls back to reading pane output and applying harness/pattern detection.

## Doctor And Verification

`kanbi doctor` reports the configured multiplexer. It always preserves existing tmux checks used by Kanbi's default board runtime. When Herdr is selected, doctor also checks the configured Herdr binary and runs `herdr status`; a missing or unreachable Herdr installation is reported as a warning with setup guidance.

Normal tests and `scripts/smoke.sh` use fake harnesses and a fake Herdr doctor probe. Real Herdr verification is opt-in only: install Herdr, configure `multiplexer.default: herdr`, then run targeted manual lifecycle checks in a disposable board/workspace.
