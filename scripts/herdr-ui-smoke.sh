#!/usr/bin/env bash
# Opt-in real Herdr UI check. Uses only a workspace/database created by this run.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UI="$ROOT/.pi/skills/kanbi-ui-validation/scripts/ui-session.sh"
if [[ "${1:-}" == --help || "${1:-}" == -h ]]; then
  echo 'Usage: ./scripts/herdr-ui-smoke.sh'
  echo 'Run inside Herdr. Checks real Kanbi navigation, editing, capture and cleanup.'
  exit 0
fi
(($# == 0)) || { echo 'No arguments accepted' >&2; exit 2; }
[[ "${HERDR_ENV:-}" == 1 ]] || { echo 'Run inside Herdr' >&2; exit 1; }
WORK="$(mktemp -d "${TMPDIR:-/tmp}/kanbi-herdr-ui-smoke.XXXXXX")"
export KANBI_UI_WORKDIR="$WORK/fixture" KANBI_UI_RUNTIME=herdr
cleanup() {
  if "$UI" stop; then
    rm -rf "$WORK"
  else
    echo "cleanup failed; retained fixture and runtime IDs at $WORK" >&2
  fi
}
trap cleanup EXIT

# Check focus invariance against the same server, not another client's selection.
focused_workspace() {
  herdr workspace list | python3 -c 'import json,sys; print(next((w["workspace_id"] for w in json.load(sys.stdin)["result"]["workspaces"] if w["focused"]), ""))'
}
BEFORE="$(focused_workspace)"
"$UI" start

frame_has() {
  local expected="$1" frame
  for attempt in {1..30}; do
    frame="$("$UI" capture)"
    if [[ "$frame" == *"$expected"* ]]; then
      return 0
    fi
    sleep 0.1
  done
  printf 'Missing frame text: %s\n%s\n' "$expected" "$frame" >&2
  return 1
}
frame_has 'Select board'
"$UI" key Enter
frame_has 'Kanbi · Master'
"$UI" key '?'
frame_has 'KEYBINDINGS AND LEGEND'
"$UI" key Escape b
frame_has 'Select board'
"$UI" key Down Enter
frame_has 'Kanbi · agent-kanban'
"$UI" key e Home
frame_has 'editing title'
"$UI" text 'Herdr UI smoke X'
"$UI" key Backspace C-s
frame_has 'Herdr UI smoke'
DB="$("$UI" fixture-path)"
[[ "$(sqlite3 "$DB" "select count(*) from tickets where title like 'Herdr UI smoke %';")" == 1 ]]
# Changing inherited context must not redirect a recorded fixture to another server.
HERDR_SOCKET_PATH="$WORK/absent.sock" HERDR_SESSION=absent HERDR_PANE_ID=absent "$UI" capture >"$WORK/frame.txt"
"$UI" capture --ansi >"$WORK/frame.ansi"
python3 - "$WORK/frame.ansi" <<'PY'
import sys
assert '\x1b[' in open(sys.argv[1]).read(), 'capture lost ANSI styling'
PY
[[ "$(focused_workspace)" == "$BEFORE" ]]
"$UI" status
# A sibling test tab is independently addressable while the board stays live.
SOCKET="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["socket"])' "$KANBI_UI_WORKDIR/herdr-runtime.json")"
SPACE="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["workspace"])' "$KANBI_UI_WORKDIR/herdr-runtime.json")"
herdr_test() { env -u HERDR_SESSION HERDR_SOCKET_PATH="$SOCKET" herdr "$@"; }
TAB="$(herdr_test tab create --workspace "$SPACE" --cwd "$WORK" --label smoke-shell --no-focus)"
PANE="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["result"]["root_pane"]["pane_id"])' <<<"$TAB")"
herdr_test pane run "$PANE" 'printf "KANBI_TEST_TAB_READY\n"'
herdr_test pane wait-output "$PANE" --regex '(?m)^KANBI_TEST_TAB_READY\r?$' --source recent --timeout 5000 >/dev/null
frame_has 'Kanbi · agent-kanban'
[[ "$(focused_workspace)" == "$BEFORE" ]]
"$UI" stop
if herdr_test pane get "$PANE" >"$WORK/closed-pane.json" 2>&1; then
  echo 'workspace cleanup left sibling tab alive' >&2; exit 1
fi
grep -q pane_not_found "$WORK/closed-pane.json"
[[ ! -f "$KANBI_UI_WORKDIR/herdr-runtime.json" ]]
[[ "$(focused_workspace)" == "$BEFORE" ]]
echo 'Herdr UI smoke: PASS (navigation, literal input/save, ANSI, routing, sibling tab, focus, cleanup)'
