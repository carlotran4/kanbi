#!/usr/bin/env bash
set -euo pipefail
umask 077
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ "${KANBI_UI_RUNTIME:-herdr}" == tmux ]]; then
  exec bash "$HERE/ui-tmux-session.sh" "$@"
fi
[[ "${KANBI_UI_RUNTIME:-herdr}" == herdr ]] || { echo 'KANBI_UI_RUNTIME must be herdr or tmux' >&2; exit 2; }
RUNTIME=herdr
source "$HERE/ui-fixture.sh"
RECORD="$WORK/herdr-runtime.json"

usage() {
  echo "Usage: $0 start | fixture-path | capture [--ansi] | key KEY... | text TEXT | status | stop | clean"
  echo 'Default: Herdr workspace in the caller session. Exact resize: KANBI_UI_RUNTIME=tmux.'
  echo "Fixture: $WORK"
}

field() {
  python3 - "$RECORD" "$1" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))[sys.argv[2]])
PY
}

herdr_ui() {
  # IDs are server-local. Pin the recorded socket and discard inherited caller
  # IDs/session selectors so another invocation cannot target a different pane.
  env -u HERDR_SESSION -u HERDR_PANE_ID -u HERDR_TAB_ID -u HERDR_WORKSPACE_ID \
    HERDR_SOCKET_PATH="$(field socket)" herdr "$@"
}

require_workspace() {
  [[ -f "$RECORD" ]] || { echo 'UI fixture is not running; use start' >&2; exit 1; }
  herdr_ui workspace list | python3 -c '
import json,sys
obj=json.load(sys.stdin)
record=json.load(open(sys.argv[1]))
workspaces=obj["result"]["workspaces"]
workspace=next((w for w in workspaces if w["workspace_id"] == record["workspace"]), None)
if workspace is None:
    print("recorded fixture workspace is already closed", file=sys.stderr)
    sys.exit(3)
if workspace.get("label") != record["label"]:
    sys.exit("recorded workspace label changed; refusing to control it")
' "$RECORD"
}

stop_workspace() {
  if [[ -f "$RECORD" ]]; then
    if require_workspace; then
      herdr_ui workspace close "$(field workspace)"
    else
      local result=$?
      [[ "$result" == 3 ]] || return "$result"
    fi
    rm -f "$RECORD"
  fi
}

start() {
  [[ $# == 1 ]] || { echo 'start takes no database options' >&2; exit 2; }
  [[ "${HERDR_ENV:-}" == 1 ]] || { echo 'Run the Herdr fixture from inside Herdr (HERDR_ENV=1).' >&2; exit 1; }
  command -v herdr >/dev/null
  [[ ! -f "$RECORD" ]] || { echo "fixture already recorded; use stop before start" >&2; exit 1; }
  prepare_fixture
  local status socket created command
  status="$(herdr status --json)"
  socket="$(python3 -c 'import json,sys; s=json.load(sys.stdin); assert s["server"]["running"] and s["server"]["compatible"]; print(s["server"]["socket"])' <<<"$status")"
  HERDR_TEST_SESSION="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["server"].get("session") or "default")' <<<"$status")"
  seed_fixture
  write_fixture_config herdr "$HERDR_TEST_SESSION"
  assert_safe_fixture
  created="$(herdr workspace create --cwd "$ROOT" --label "kanbi-ui-$CHECKOUT_KEY" --no-focus)"
  python3 - "$RECORD" "$socket" "$HERDR_TEST_SESSION" 3<<<"$created" <<'PY'
import json, os, sys
obj=json.load(os.fdopen(3))["result"]
json.dump(dict(socket=sys.argv[2],session=sys.argv[3],workspace=obj["workspace"]["workspace_id"],
    label=obj["workspace"]["label"],pane=obj["root_pane"]["pane_id"],tab=obj["tab"]["tab_id"]),open(sys.argv[1],"w"))
PY
  # If launch fails, close only the workspace this invocation created.
  trap 'stop_workspace' ERR
  command="$(python3 - "$CONFIG" "$DB" "$STATE" "$DATA" "$BIN" <<'PY'
from pathlib import Path
import shlex,sys
config,db,state,data,binary=sys.argv[1:]
print("stty size > " + shlex.quote(str(Path(config).parent / "terminal-size")) + "; " + shlex.join(["exec","env","KANBI_INNER=1",f"KANBI_CONFIG={config}",f"KANBI_DB={db}",f"KANBI_STATE_DIR={state}",f"KANBI_DATA_DIR={data}","TERM=xterm-256color",binary,"--board"]))
PY
)"
  herdr_ui pane run "$(field pane)" "$command"
  herdr_ui pane wait-output "$(field pane)" --match 'Select board' --source visible --timeout 15000 >/dev/null
  trap - ERR
  echo 'Kanbi UI started in a non-focused disposable Herdr workspace.'
  cat "$RECORD"
  echo
  echo "Capture: $0 capture; controls: $0 key Enter; $0 key j"
  echo "Optional focus: HERDR_SOCKET_PATH='$socket' herdr tab focus '$(field tab)'"
  echo "Initial PTY rows/columns: $(cat "$WORK/terminal-size")"
  herdr_ui pane layout --pane "$(field pane)"
}

case "${1:-}" in
  start) start "$@" ;;
  capture)
    require_workspace
    format=text
    [[ "${2:-}" != --ansi ]] || format=ansi
    herdr_ui pane read "$(field pane)" --source visible --format "$format"
    ;;
  key)
    require_workspace
    shift
    (($#)) || { echo 'key requires logical keys' >&2; exit 2; }
    for key in "$@"; do
      case "$key" in
        Enter) key=enter ;; Escape) key=esc ;; C-c) key=ctrl+c ;;
        C-s) key=ctrl+s ;; Tab) key=tab ;; BTab) key=shift+tab ;;
        Up) key=up ;; Down) key=down ;; Left) key=left ;; Right) key=right ;;
        Home) key=ctrl+a ;; End) key=ctrl+e ;; Backspace) key=backspace ;; Delete) key=delete ;;
      esac
      herdr_ui pane send-keys "$(field pane)" "$key"
    done
    ;;
  text)
    require_workspace
    [[ $# == 2 ]] || { echo 'text takes one quoted literal argument' >&2; exit 2; }
    herdr_ui pane send-text "$(field pane)" "$2"
    ;;
  status)
    require_workspace
    cat "$RECORD"
    echo
    echo "Initial PTY rows/columns: $(cat "$WORK/terminal-size")"
    herdr_ui pane layout --pane "$(field pane)"
    ;;
  resize)
    echo 'Herdr pane resize changes split ratios, not exact terminal cells.' >&2
    echo 'For exact-size validation use KANBI_UI_RUNTIME=tmux with start/resize/capture/stop.' >&2
    exit 2
    ;;
  fixture-path)
    [[ -f "$DB" ]] || { echo 'fixture not built; use start' >&2; exit 1; }
    assert_safe_fixture
    echo "$DB"
    ;;
  stop) stop_workspace; echo "Fixture retained at $DB" ;;
  clean) stop_workspace; clean_fixture ;;
  -h|--help|'') usage ;;
  *) usage >&2; exit 2 ;;
esac
