#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
SOCKET_DIR="${CLAUDE_TMUX_SOCKET_DIR:-${TMPDIR:-/tmp}/claude-tmux-sockets}"
SOCKET="${KANBI_UI_TMUX_SOCKET:-$SOCKET_DIR/kanbi-ui.sock}"
SESSION="${KANBI_UI_TMUX_SESSION:-kanbi-ui}"
WORK="${KANBI_UI_WORKDIR:-${TMPDIR:-/tmp}/kanbi-ui-validation-${USER:-agent}}"
TARGET="$SESSION:board"
BIN="$WORK/kanbi"
DB="$WORK/kanbi-ui-test.db"
CONFIG="$WORK/config.yaml"
STATE="$WORK/state"
DATA="$WORK/data"

usage() {
  cat <<EOF
Usage: $0 start | fixture-path | capture [--ansi] | key KEY... | text TEXT | resize WIDTH HEIGHT | status | stop | clean
Workspace: $WORK
Socket:    $SOCKET
Session:   $SESSION
EOF
}

tmux_ui() { tmux -f /dev/null -S "$SOCKET" "$@"; }

require_session() {
  tmux_ui has-session -t "$SESSION" 2>/dev/null || {
    echo "Kanbi UI session is not running; run: $0 start" >&2
    exit 1
  }
}

run_cli() {
  KANBI_CONFIG="$CONFIG" KANBI_DB="$DB" KANBI_STATE_DIR="$STATE" KANBI_DATA_DIR="$DATA" "$BIN" "$@"
}

seed_fixture() {
  run_cli boards add "agent-kanban" --cwd "$ROOT" --backend local >/dev/null
  run_cli boards add "personal-finance" --cwd "$ROOT" --backend local >/dev/null
  run_cli boards add "kanbi" --cwd "$ROOT" --backend local >/dev/null

  local board i id destination title
  for board in Default agent-kanban personal-finance kanbi; do
    for i in $(seq 1 18); do
      case $((i % 5)) in
        0) title="Release qualification and production hardening $i" ;;
        1) title="Bug - scrolling and viewport behavior $i" ;;
        2) title="Add backend integration support $i" ;;
        3) title="Improve image paste and attachment workflow $i" ;;
        *) title="Set up Actual on the homelab $i" ;;
      esac
      run_cli add "$title" --body "Fixture description for interactive UI validation. Card $i on $board." --harness pi --board "$board" >/dev/null
      printf -v id 'T-%03d' "$i"
      case $((i % 4)) in
        1) destination="In Progress" ;;
        2) destination="Review" ;;
        3) destination="Done" ;;
        *) continue ;;
      esac
      run_cli move "$id" --to "$destination" --board "$board" >/dev/null
    done
  done

  # Add realistic terminal session projections without ever launching a harness.
  # Done cards intentionally include elapsed error/resumable labels because they
  # exercise the tallest production card shape.
  sqlite3 "$DB" <<'SQL'
insert into sessions (
  ticket_id, harness, harness_session_ref, tmux_session_name, tmux_window_name,
  status, is_active, created_at, updated_at, started_at, closed_at,
  last_state_change_at, last_detected_state, last_detection_source
)
select t.id, t.harness, 'fixture-ref-' || t.id, 'kanbi-ui-fixture',
       'fixture-' || t.id, 'error', 0,
       datetime('now', '-37 days'), datetime('now', '-37 days'),
       datetime('now', '-37 days'), datetime('now', '-37 days'),
       datetime('now', '-37 days'), 'error', 'fixture'
  from tickets t
  join columns c on c.id = t.column_id
 where c.name = 'Done';

insert into pause_checkpoints(ticket_id, why, completed, next_action, paused_at)
select t.id, 'Fixture focus handoff', 'Fixture work completed',
       'Verify the next focused UI action at 80x24.', datetime('now', '-2 hours')
  from tickets t join columns c on c.id=t.column_id
 where c.workflow_key in ('In Progress','Review')
   and t.id not in (
     select t2.id from tickets t2 join columns c2 on c2.id=t2.column_id
      where c2.workflow_key in ('In Progress','Review')
      order by t2.id limit 4
   );
update tickets set focus_paused=1
 where id in (select ticket_id from pause_checkpoints where resumed_at is null);

update boards set sync_enabled = 0, backend_query = null, backend_config = null;
SQL
}

assert_safe_fixture() {
  local unsafe
  unsafe="$(sqlite3 "$DB" "select count(*) from boards where ticket_backend <> 'local' or sync_enabled <> 0 or coalesce(backend_config, '') <> '';")"
  [[ "$unsafe" == "0" ]] || {
    echo "refusing to launch unsafe UI fixture: provider-backed or sync-enabled board found" >&2
    exit 1
  }
}

start() {
  shift
  (($# == 0)) || { echo "start takes no database options" >&2; usage; exit 2; }

  mkdir -p "$SOCKET_DIR"
  if tmux_ui has-session -t "$SESSION" 2>/dev/null; then
    tmux_ui kill-session -t "$SESSION"
  fi
  rm -rf "$WORK"
  mkdir -p "$WORK" "$STATE" "$DATA"

  (cd "$ROOT" && go build -buildvcs=false -o "$BIN" ./cmd/kanbi)
  cat >"$CONFIG" <<EOF
db_path: "$DB"
tmux_session: "$SESSION"
EOF

  seed_fixture
  cat >"$CONFIG" <<EOF
db_path: "$DB"
tmux_session: "$SESSION"
focus:
  enabled: true
  limit: 3
  workflow_keys: ["In Progress", "Review"]
EOF
  assert_safe_fixture

  local command
  # Match the private tmux terminal so automatic styling does not issue OSC
  # color queries to an unattached xterm and consume scripted keyboard input.
  printf -v command 'exec env KANBI_CONFIG=%q KANBI_DB=%q KANBI_STATE_DIR=%q KANBI_DATA_DIR=%q KANBI_TMUX_SESSION=%q TERM=tmux-256color %q' \
    "$CONFIG" "$DB" "$STATE" "$DATA" "$SESSION" "$BIN"
  tmux_ui new-session -d -s "$SESSION" -n board -x 160 -y 45 "$command"
  sleep 1

  echo "Kanbi UI session started."
  echo "Attach:  tmux -S '$SOCKET' attach -t '$SESSION'"
  echo "Capture: $0 capture"
  echo "Drive:   $0 key Enter; $0 key j; $0 resize 80 24"
}

case "${1:-}" in
  start) start "$@" ;;
  capture)
    require_session
    if [[ "${2:-}" == "--ansi" ]]; then
      tmux_ui capture-pane -p -e -J -t "$TARGET"
    else
      tmux_ui capture-pane -p -J -t "$TARGET"
    fi
    ;;
  key)
    require_session
    shift
    (($#)) || { echo "key requires one or more tmux key names" >&2; exit 2; }
    tmux_ui send-keys -t "$TARGET" "$@"
    ;;
  text)
    require_session
    shift
    (($#)) || { echo "text requires literal input" >&2; exit 2; }
    tmux_ui send-keys -t "$TARGET" -l -- "$*"
    ;;
  resize)
    require_session
    [[ $# -eq 3 ]] || { echo "resize requires WIDTH HEIGHT" >&2; exit 2; }
    tmux_ui resize-window -t "$SESSION:board" -x "$2" -y "$3"
    ;;
  fixture-path)
    [[ -f "$DB" ]] || { echo "fixture has not been built; run: $0 start" >&2; exit 1; }
    assert_safe_fixture
    printf '%s\n' "$DB"
    ;;
  status)
    require_session
    tmux_ui display-message -p -t "$TARGET" 'session=#{session_name} window=#{window_name} size=#{window_width}x#{window_height} pane=#{pane_id}'
    ;;
  stop)
    if tmux_ui has-session -t "$SESSION" 2>/dev/null; then
      tmux_ui kill-session -t "$SESSION"
    fi
    echo "Kanbi UI session stopped. Fixture retained at $DB"
    ;;
  clean)
    if tmux_ui has-session -t "$SESSION" 2>/dev/null; then
      tmux_ui kill-session -t "$SESSION"
    fi
    rm -rf "$WORK"
    echo "Kanbi UI session and fixture removed."
    ;;
  *) usage; exit 2 ;;
esac
