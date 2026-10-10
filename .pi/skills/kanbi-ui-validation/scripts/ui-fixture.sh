#!/usr/bin/env bash
# Sourced by the UI helpers; never accepts a production database.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
CHECKOUT_KEY="$(printf '%s' "$ROOT" | cksum | awk '{print $1}')"
WORK="${KANBI_UI_WORKDIR:-${TMPDIR:-/tmp}/kanbi-ui-validation-${USER:-agent}-$CHECKOUT_KEY-${RUNTIME:-herdr}}"
BIN="$WORK/kanbi"
DB="$WORK/kanbi-ui-test.db"
CONFIG="$WORK/config.yaml"
STATE="$WORK/state"
DATA="$WORK/data"

prepare_fixture() {
  [[ ! -L "$WORK" ]] || { echo "refusing symlink fixture directory" >&2; exit 1; }
  if [[ -d "$WORK" && ! -f "$WORK/.kanbi-ui-owned" ]]; then
    echo "refusing unowned fixture directory: $WORK" >&2; exit 1
  fi
  mkdir -p "$WORK" "$STATE" "$DATA"
  chmod 700 "$WORK"
  touch "$WORK/.kanbi-ui-owned"
  rm -f "$DB" "$DB-wal" "$DB-shm"
  (cd "$ROOT" && GOCACHE="${GOCACHE:-/tmp/kanbi-go-build}" go build -buildvcs=false -o "$BIN" ./cmd/kanbi)
}

write_fixture_config() {
  cat >"$CONFIG" <<EOF
db_path: "$DB"
multiplexer:
  default: $1
  herdr:
    session: "${2:-default}"
    focus_on_open: false
focus:
  enabled: ${3:-true}
  limit: 3
  workflow_keys: ["In Progress", "Review"]
EOF
  # Layout/navigation fixtures must never start authenticated real harnesses.
  for harness in pi codex copilot claude; do
    [[ "$harness" != pi ]] || echo 'harnesses:' >>"$CONFIG"
    cat >>"$CONFIG" <<EOF
  $harness:
    start: ["/usr/bin/false"]
    resume: ["/usr/bin/false", "{session_ref}"]
EOF
  done
}

clean_fixture() {
  [[ -f "$WORK/.kanbi-ui-owned" && ! -L "$WORK" ]] || { echo "refusing to clean unowned fixture" >&2; exit 1; }
  rm -rf "$WORK"
}

run_cli() {
  KANBI_CONFIG="$CONFIG" KANBI_DB="$DB" KANBI_STATE_DIR="$STATE" KANBI_DATA_DIR="$DATA" "$BIN" "$@"
}

seed_fixture() {
  write_fixture_config "$RUNTIME" "${HERDR_TEST_SESSION:-default}" false
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
  multiplexer, mux_container_name, status, is_active, created_at, updated_at, started_at, closed_at,
  last_state_change_at, last_detected_state, last_detection_source
)
select t.id, t.harness, 'fixture-ref-' || t.id, 'kanbi-ui-fixture',
       'fixture-' || t.id, 'herdr', 'fixture-' || t.id, 'error', 0,
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
