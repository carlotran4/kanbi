#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
RUNTIME=tmux
source "$(dirname "${BASH_SOURCE[0]}")/ui-fixture.sh"
SOCKET_DIR="${CLAUDE_TMUX_SOCKET_DIR:-${TMPDIR:-/tmp}/claude-tmux-sockets}"
SOCKET="${KANBI_UI_TMUX_SOCKET:-$SOCKET_DIR/kanbi-ui-$CHECKOUT_KEY.sock}"
SESSION="${KANBI_UI_TMUX_SESSION:-kanbi-ui-$CHECKOUT_KEY}"
TARGET="$SESSION:board"

usage() {
  cat <<EOF
Usage: $0 start | fixture-path | capture [--ansi] | key KEY... | text TEXT | resize WIDTH HEIGHT | status | stop | clean
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

start() {
  shift
  (($# == 0)) || { echo "start takes no database options" >&2; usage; exit 2; }

  mkdir -p "$SOCKET_DIR"
  if tmux_ui has-session -t "$SESSION" 2>/dev/null; then
    echo "fixture already running; use stop before start" >&2
    exit 1
  fi
  prepare_fixture
  seed_fixture
  write_fixture_config tmux
  printf 'tmux_session: "%s"\n' "$SESSION" >>"$CONFIG"
  assert_safe_fixture

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
    clean_fixture
    echo "Kanbi UI session and fixture removed."
    ;;
  *) usage; exit 2 ;;
esac
