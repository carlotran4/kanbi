# Multiplexer Contracts

Kanbi launches agent harnesses inside a configured multiplexer runtime substrate. The multiplexer owns the terminal container; the harness owns the agent command and any harness-native session ref.

## Terminology

- **Configured multiplexer**: the runtime substrate selected by `multiplexer.default` in config.
- **Terminal container**: the live process container for one active Kanbi session.
- **Herdr pane/agent**: the terminal container type used by the sole `herdr` implementation.
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

Session rows persist the generic fields `multiplexer`, `mux_namespace`, `mux_container_id`, `mux_container_name`, and `mux_metadata`. Legacy tmux fields remain readable for historical rows and exports; new Herdr launches write generic references. Retired integration-run container metadata remains readable solely for explicit legacy cancellation.

## Herdr Behavior

Herdr 0.9.3 or newer is the supported runtime baseline; its `agent start` must expose both `--kind` and `--pane`. Herdr is the only runtime and the default:

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

For current Herdr releases, Kanbi creates a dedicated tab with a shell pane in the selected workspace and working directory without focusing it, then starts the harness with Herdr's pane-first `agent start --kind <harness> --pane <id>` contract. Creating the tab directly avoids temporarily splitting the board pane and collapsing the home screen to half width during launch. A newly created Herdr pane can briefly report `agent_pane_busy` before its shell becomes available, so Kanbi retries that specific transient response for a bounded interval; other launch errors fail immediately and clean up the pane. Kanbi focuses the tab only after launch and initial prompt delivery succeed, preventing failed cleanup from returning the user to an unrelated existing agent. For Pi and Codex, Kanbi also waits for their visible input editor before sending text, because a cold-start executable installer can be mistaken for a ready agent. Initial ticket prompts use `agent prompt --wait` after the harness is ready, waiting for `working`, `done` or `blocked` to confirm submission. Herdr handles multiline paste and Enter timing for the harness; separate immediate pane input can leave the prompt unsubmitted in Pi or Codex. If Herdr reports `agent_not_running`, Kanbi checks the pane for the known Codex account/workspace bootstrap failure before closing it and records that cause with the failed attempt. A visible editor marker alone does not prove the process is still running. If Herdr reports `agent_prompt_stalled`, Kanbi sends one Enter to the existing editor input and waits again for acceptance, without pasting the prompt twice. The initial acceptance check allows at least six seconds for Herdr’s fixed five-second stalled check. Prompts remain literal text rather than `agent start` arguments. The internal Herdr agent identifier is normalized to Herdr's lowercase 32-character format and includes a pane-bound suffix so retries cannot collide with completed Herdr agents, while the full ticket label remains the tab/container display name. This Herdr API selects the harness's canonical executable; custom executable paths and wrapper commands launch through `pane run` with a shell-quoted `exec` command. They retain configured prompt argument/readiness/ref-marker behavior and do not scan native user histories. Before each launch Kanbi checks `agent start --help` once and rejects unsupported command surfaces before creating resources. There is no legacy launch fallback.

When Herdr reports an agent state, Kanbi prefers that native state for ticket sessions. If Herdr state is unavailable or `unknown`, Kanbi falls back to reading pane output and applying harness/pattern detection. Explicit Herdr `agent_not_found` and `pane_not_found` responses are authoritative container absence rather than generic command failures; Kanbi deactivates that ticket attempt as exited/resumable or repair-needed according to whether a verified harness session ref exists.

## Read-only inspector preview

The optional `ScreenReader` capability is separate from transcript `Read`. Herdr captures `pane read <stored-pane-id> --source visible --format ansi --lines 160`, using pane metadata from the selected active session. Runtime detection and session-ref capture retain `recent-unwrapped` text reads. Preview validates the current attempt and full stored reference, performs no lifecycle writes, focus, input, launch, or terminal resize, and uses a three-second deadline. Capture drains CLI output while retaining at most 256 KiB. A screen-read failure may fall back to a bounded recent transcript, explicitly labeled; validation/read failures preserve the previous preview with a stale/unavailable warning.

The TUI permits one capture at a time, cancels and invalidates results when selection/attempt/section/modal visibility changes, and refreshes no faster than two seconds after a result while Output is visible. It preserves numeric SGR styling and cell layout, clips within panel bounds, supports horizontal scrolling, and strips cursor controls, OSC/clipboard/hyperlinks, DCS/APC/graphics and other controls. No cursor overlay is synthesized. Streaming observers are not used, and the agent's terminal dimensions are never changed to fit the panel. Historical tmux attempts remain unavailable; this feature does not restore the retired backend.

## Doctor And Verification

`kanbi doctor` reports build/schema compatibility and checks `herdr status --json` for a running, compatible server. Missing Herdr, unavailable/incompatible servers and unsupported runtime settings are fatal. Missing optional harness commands remain warnings. It performs no tmux checks.

Normal tests use fake Herdr command runners. `scripts/smoke.sh` runs fake harnesses on a real, isolated Herdr server and client. Authenticated harness checks remain opt-in through `scripts/real-harness-lifecycle.sh`.

## Agent-driven UI verification

The project [UI-validation skill](../.agents/skills/kanbi-ui-validation/SKILL.md) runs Kanbi as an ordinary terminal process in a disposable Herdr workspace. Agents use explicit pane IDs with `pane send-keys`, `pane send-text` and `pane read --source visible --format ansi`; agent prompt/readiness commands are for harnesses, not the board UI. The fixture uses local boards, disabled sync and disabled real harness commands. `scripts/herdr-ui-smoke.sh` verifies this workflow without changing user focus or stopping the parent server.

Runtime IDs are server-local. The helper records and pins its socket and IDs. It owns an isolated server and a rendering client PTY with private configuration and XDG state; the caller's Herdr server and focus remain untouched. Temporary nested-client permission applies only to the fixture configuration.

Exact-size validation resizes the owned rendering client's outer PTY and lets Herdr resize the application. The helper verifies the application's PTY dimensions independently of layout rectangles. It needs no alternate runtime; `scripts/herdr-ui-smoke.sh` covers 160x45 and 80x24.
