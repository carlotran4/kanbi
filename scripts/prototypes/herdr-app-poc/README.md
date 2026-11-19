# Herdr app Kanbi proof of concept

Opt-in, Linux-only GH-404 experiment. The native Herdr mod uses the real Kanbi CLI
for tickets, moves, notes and session opens. A deliberately narrow CLI bridge
runs those sessions in herdr-core. Production Go code and runtime defaults are
unchanged. This is not a general Herdr-to-core compatibility layer.

See [setup, controls and validation](../../../docs/herdr-app-poc.md).

## Files

- `setup.py`: builds a private Kanbi binary, installs source copies, seeds a local
  board and creates a separate app launcher. Rerunning preserves ticket/session
  history but replaces POC configuration and mod files.
- `main.js`: native GPUI board, title/note inputs, action buttons and observed
  core status. No browser, web server or direct SQLite writes.
- `ui.py`: bounded structured actions restricted to the private board/database.
- `core_bridge.py`: subset of old adapter CLI operations mapped to core JSON-RPC;
  unique pane ownership labels, serialized creation and literal framed input.
- `demo_agent.py`: offline interactive harness with actual OSC 7501 reporting;
  it never creates a fake harness resume ref.
- `codex_agent.py`: explicit real Codex wrapper, independent daemon disabled,
  workspace-write sandbox and on-request approvals.
- `verify.py`: real-core offline smoke. It creates a verification ticket, retains
  its ended attempt, and expects Full TUI to be running for normal reconciliation.

Never configure a production Kanbi database to use `core_bridge.py`. The bridge
uses Kanbi's existing `herdr` configuration slot only inside its private database;
those prototype container IDs are not ordinary Herdr TUI IDs. Do not copy or
merge its database into your live Kanbi installation.
