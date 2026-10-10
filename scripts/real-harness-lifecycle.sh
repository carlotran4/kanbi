#!/usr/bin/env bash
# Opt-in authenticated lifecycle validation. May consume quota and write native
# harness histories. The test database, Herdr server and panes are disposable.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ "${KANBI_REAL_HARNESS_TESTS:-}" != 1 ]]; then
  echo 'Opt in: KANBI_REAL_HARNESS_TESTS=1 KANBI_REAL_HARNESSES=pi,codex ./scripts/real-harness-lifecycle.sh'
  exit 0
fi
TMP="$(mktemp -d)"
cleanup() { python3 "$ROOT/scripts/herdr-fixture.py" stop "$TMP/runtime"; rm -rf "$TMP"; }
trap cleanup EXIT
export GOCACHE="${GOCACHE:-/tmp/kanbi-go-build}"
export KANBI_CONFIG="$TMP/config.yaml" KANBI_DB="$TMP/kanbi.db" KANBI_STATE_DIR="$TMP/state" KANBI_DATA_DIR="$TMP/data"
cat >"$KANBI_CONFIG" <<YAML
multiplexer:
  default: herdr
prompt_ready_timeout: 15s
YAML
python3 "$ROOT/scripts/herdr-fixture.py" start "$TMP/runtime" 160 45
unset HERDR_SESSION HERDR_CLIENT_SOCKET_PATH HERDR_PANE_ID HERDR_TAB_ID HERDR_WORKSPACE_ID HERDR_ENV
eval "$(python3 "$ROOT/scripts/herdr-fixture.py" env "$TMP/runtime")"
cd "$ROOT"
go build -buildvcs=false -o "$TMP/kanbi" ./cmd/kanbi
BIN="$TMP/kanbi"
"$BIN" doctor
IFS=',' read -ra harnesses <<<"${KANBI_REAL_HARNESSES:-pi}"
for harness in "${harnesses[@]}"; do
  case "$harness" in pi|codex|copilot|claude) ;; *) echo "Unknown harness: $harness" >&2; exit 2 ;; esac
  command -v "$harness" >/dev/null
  ticket="$("$BIN" add "Lifecycle $harness" --harness "$harness" --body 'Reply with exactly KANBI_LIFECYCLE_OK. Do not use tools or modify files.' | sed -n 's/^\(T-[0-9]*\).*/\1/p')"
  "$BIN" open "$ticket" --send-prompt
  query="from sessions s join tickets t on t.id=s.ticket_id where t.display_id='$ticket' order by s.id desc limit 1"
  [[ "$(sqlite3 "$KANBI_DB" "select s.multiplexer $query;")" == herdr ]]
  pane="$(sqlite3 "$KANBI_DB" "select json_extract(s.mux_metadata,'$.pane_id') $query;")"
  herdr pane get "$pane" >/dev/null
  ref=""
  for attempt in {1..30}; do
    ref="$(sqlite3 "$KANBI_DB" "select s.harness_session_ref $query;")"
    [[ -z "$ref" ]] || break
    sleep 1
  done
  [[ -n "$ref" ]] || { echo "$harness: no verified resume ref captured" >&2; exit 1; }
  "$BIN" open "$ticket"
  [[ "$(sqlite3 "$KANBI_DB" "select count(*) from sessions s join tickets t on t.id=s.ticket_id where t.display_id='$ticket';")" == 1 ]]
  # Simulate terminal exit, then actually resume through the stored native ref.
  herdr pane close "$pane" >/dev/null
  "$BIN" open "$ticket"
  [[ "$(sqlite3 "$KANBI_DB" "select count(*) from sessions s join tickets t on t.id=s.ticket_id where t.display_id='$ticket';")" == 2 ]]
  [[ "$(sqlite3 "$KANBI_DB" "select s.harness_session_ref $query;")" == "$ref" ]]
  [[ "$(sqlite3 "$KANBI_DB" "select sum(s.is_active) from sessions s join tickets t on t.id=s.ticket_id where t.display_id='$ticket';")" == 1 ]]
  echo "$harness: PASS (launch, verified ref, focus, terminal exit, actual resume, preserved history)"
done
echo 'Real Herdr harness lifecycle: PASS'
