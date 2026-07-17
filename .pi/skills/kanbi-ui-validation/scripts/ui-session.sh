#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
SOCKET_DIR="${CLAUDE_TMUX_SOCKET_DIR:-${TMPDIR:-/tmp}/claude-tmux-sockets}"
SOCKET="${KANBI_UI_TMUX_SOCKET:-$SOCKET_DIR/kanbi-ui.sock}"
SESSION="${KANBI_UI_TMUX_SESSION:-kanbi-ui}"
WORK="${KANBI_UI_WORKDIR:-${TMPDIR:-/tmp}/kanbi-ui-validation-${USER:-agent}}"
TARGET="$SESSION:board"
BIN="$WORK/kanbi"
DB="$WORK/kanbi.db"
CONFIG="$WORK/config.yaml"
STATE="$WORK/state"
DATA="$WORK/data"

usage() {
  cat <<EOF
Usage: $0 start [--clone-db PATH] | capture [--ansi] | key KEY... | text TEXT | resize WIDTH HEIGHT | status | stop
Workspace: $WORK
Socket:    $SOCKET
Session:   $SESSION
EOF
}

tmux_ui() { tmux -S "$SOCKET" "$@"; }

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
  run_cli boards add "agent-kanban" --cwd "$ROOT" >/dev/null
  run_cli boards add "personal-finance" --cwd "$ROOT" >/dev/null
  run_cli boards add "kanbi" --cwd "$ROOT" >/dev/null

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
}

start() {
  local clone_db=""
  shift
  while (($#)); do
    case "$1" in
      --clone-db)
        [[ $# -ge 2 ]] || { echo "--clone-db requires a path" >&2; exit 2; }
        clone_db="$2"
        shift 2
        ;;
      *) echo "unknown start option: $1" >&2; usage; exit 2 ;;
    esac
  done

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

  if [[ -n "$clone_db" ]]; then
    [[ -f "$clone_db" ]] || { echo "database not found: $clone_db" >&2; exit 2; }
    sqlite3 "$clone_db" ".backup '$DB'"
  else
    seed_fixture
  fi

  local command
  printf -v command 'exec env KANBI_CONFIG=%q KANBI_DB=%q KANBI_STATE_DIR=%q KANBI_DATA_DIR=%q KANBI_TMUX_SESSION=%q TERM=xterm-256color %q' \
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
  status)
    require_session
    tmux_ui display-message -p -t "$TARGET" 'session=#{session_name} window=#{window_name} size=#{window_width}x#{window_height} pane=#{pane_id}'
    ;;
  stop)
    if tmux_ui has-session -t "$SESSION" 2>/dev/null; then
      tmux_ui kill-session -t "$SESSION"
    fi
    rm -rf "$WORK"
    echo "Kanbi UI session stopped and workspace removed."
    ;;
  *) usage; exit 2 ;;
esac
