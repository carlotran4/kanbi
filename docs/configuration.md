# Configuration

Kanbi reads YAML configuration from `${XDG_CONFIG_HOME:-~/.config}/kanbi/config.yaml`. Set `KANBI_CONFIG` to use another file.

## Custom status bar

The board header is a module-based status bar with independently anchored left, center, and right zones. Without configuration it remains `Kanbi <board>` on the left.

```yaml
status_bar:
  left: "$kanbi $board"
  center: "${custom.weekly_usage}"
  right: "$time"
  command_timeout: 500ms

  time:
    # Go time layout syntax.
    format: "15:04"

  custom:
    weekly_usage:
      command: ["/home/me/.local/bin/weekly-usage"]
      refresh_interval: 5m
      timeout: 1s
      format: "weekly $output"
```

Available built-in modules:

- `$kanbi` — the application name.
- `$board` — the selected board. In Master, this includes the active filter summary.
- `$time` — local time rendered with `status_bar.time.format`.
- `${custom.NAME}` — cached output from a named custom command.

Use `$$` for a literal dollar sign. Unknown modules and invalid durations stop startup with a configuration error rather than silently rendering a broken bar.

All three zones are optional. The left and right zones are anchored to their edges and the center zone is centered against the full terminal width. On collision, Kanbi truncates center first, right second, and left last.

### Custom command behavior

- `command` is an argument array executed directly, without an implicit shell. To opt into shell syntax, configure it explicitly, for example `command: ["sh", "-lc", "..."]`.
- `refresh_interval` defaults to `1m`.
- `timeout` defaults to the status bar's `command_timeout`, which defaults to `500ms`.
- Only modules referenced by a zone are executed. One invocation in a module's refresh chain runs at a time, and the next refresh is scheduled after the current invocation completes.
- A successful first line of stdout is cached. Output is bounded, stripped of ANSI/control sequences, and truncated to a safe display width.
- On a later failure, Kanbi retains the last successful value and adds `!`. A module that has never succeeded renders as `!NAME`.
- Commands run in the selected board's working directory and inherit Kanbi's environment plus `KANBI_BOARD_NAME`, `KANBI_BOARD_WORKDIR`, and `KANBI_MASTER`.
- Switching boards starts a new refresh and ignores late results from the previous board.

## Timeouts

```yaml
# Legacy duration setting.
prompt_ready_timeout: 5s

timeouts:
  idle_unknown_after_seconds: 120
  graceful_exit_timeout_seconds: 15
  prompt_ready_timeout_seconds: 5
```

`timeouts.prompt_ready_timeout_seconds`, when set, takes precedence over the legacy `prompt_ready_timeout`. Other nested timeout settings—and comments or text that mention `timeouts:`—do not change the legacy prompt-ready timeout.

Configuration is loaded when Kanbi starts; live reload is not currently supported.
