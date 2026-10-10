#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUN_CHECKS=1
for arg in "$@"; do
  case "$arg" in
    --skip-checks)
      RUN_CHECKS=0
      ;;
    -h|--help)
      cat <<'USAGE'
Usage: ./scripts/smoke.sh [--skip-checks]

Runs the Herdr-backed end-to-end smoke test with fake harnesses. By default this also runs go fmt, go test, and
go vet first. Use --skip-checks when those baseline checks have already
passed in the same verification loop. Set KANBI_SMOKE_BIN to validate an
existing release binary instead of rebuilding Kanbi from source.
USAGE
      exit 0
      ;;
    *)
      echo "unknown argument: $arg" >&2
      echo "usage: ./scripts/smoke.sh [--skip-checks]" >&2
      exit 2
      ;;
  esac
done

TMP="$(mktemp -d)"
BIN="$TMP/kanbi"
FAKE_PI="$ROOT/scripts/fake-harnesses/pi"
FAKE_CODEX="$ROOT/scripts/fake-harnesses/codex"
FAKE_CLAUDE="$ROOT/scripts/fake-harnesses/claude"

cleanup() {
  python3 "$ROOT/scripts/herdr-fixture.py" stop "$TMP/runtime"
  rm -rf "$TMP"
}
trap cleanup EXIT

cd "$ROOT"
export GOCACHE="${GOCACHE:-/tmp/kanbi-go-build}"
if [[ "$RUN_CHECKS" == "1" ]]; then
  # Run the repository checks before exporting smoke-only Kanbi paths. Those
  # variables intentionally affect application behavior and would otherwise
  # leak into unit tests that exercise XDG and runtime configuration defaults.
  go fmt ./...
  go test ./...
  go vet ./...
fi

export KANBI_CONFIG="$TMP/config.yaml"
export KANBI_DB="$TMP/kanbi.db"
export KANBI_STATE_DIR="$TMP/state"
export KANBI_DATA_DIR="$TMP/data"
python3 "$ROOT/scripts/herdr-fixture.py" start "$TMP/runtime" 160 45
unset HERDR_SESSION HERDR_CLIENT_SOCKET_PATH HERDR_PANE_ID HERDR_TAB_ID HERDR_WORKSPACE_ID HERDR_ENV
eval "$(python3 "$ROOT/scripts/herdr-fixture.py" env "$TMP/runtime")"

cat >"$KANBI_CONFIG" <<YAML
db_path: "$KANBI_DB"
multiplexer:
  default: herdr
prompt_ready_timeout: 3s
harnesses:
  pi:
    start: ["$FAKE_PI"]
    resume: ["$FAKE_PI", "--session", "{session_ref}"]
    prompt_mode: "arg"
    session_ref: "SESSION_REF="
    prompt_ready: "PROMPT_READY"
  codex:
    start: ["$FAKE_CODEX", "--no-alt-screen"]
    resume: ["$FAKE_CODEX", "resume", "--no-alt-screen", "{session_ref}"]
    prompt_mode: "arg"
    session_ref: "SESSION_REF="
  claude:
    start: ["$FAKE_CLAUDE"]
    resume: ["$FAKE_CLAUDE", "--resume", "{session_ref}"]
    prompt_mode: "arg"
    session_ref: "SESSION_REF="
YAML

if [[ -n "${KANBI_SMOKE_BIN:-}" ]]; then
  [[ -x "$KANBI_SMOKE_BIN" ]] || { echo "KANBI_SMOKE_BIN is not executable: $KANBI_SMOKE_BIN" >&2; exit 1; }
  cp "$KANBI_SMOKE_BIN" "$BIN"
  chmod +x "$BIN"
else
  go build -buildvcs=false -o "$BIN" ./cmd/kanbi
fi

"$BIN" doctor
"$BIN" add "Smoke test ticket" --body "Verify smoke path" --harness pi
LIST_OUTPUT="$("$BIN" list)"
grep -q "T-001" <<<"$LIST_OUTPUT"
"$BIN" open T-001 --send-prompt
"$BIN" add "Codex smoke ticket" --body "Verify codex prompt arg" --harness codex
LIST_OUTPUT="$("$BIN" list)"
grep -q "T-002" <<<"$LIST_OUTPUT"
"$BIN" open T-002 --send-prompt
"$BIN" add "Claude smoke ticket" --body "Verify claude prompt arg" --harness claude
LIST_OUTPUT="$("$BIN" list)"
grep -q "T-003" <<<"$LIST_OUTPUT"
"$BIN" open T-003 --send-prompt

[[ "$(sqlite3 "$KANBI_DB" "select count(*) from sessions where is_active=1 and multiplexer='herdr' and mux_container_id is not null;")" == 3 ]]
[[ "$(sqlite3 "$KANBI_DB" "select count(*) from sessions where tmux_window_id is not null;")" == 0 ]]
for number in 1 2 3; do
  pane="$(sqlite3 "$KANBI_DB" "select json_extract(mux_metadata, '$.pane_id') from sessions where ticket_id=$number and is_active=1;")"
  herdr pane wait-output "$pane" --match "# T-00$number:" --source recent --timeout 5000 >/dev/null
  out="$(herdr pane read "$pane" --source recent --format text)"
  case "$number" in
    1) grep -q 'Verify smoke path' <<<"$out" ;;
    2) grep -q 'Verify codex prompt arg' <<<"$out" ;;
    3) grep -q 'Verify claude prompt arg' <<<"$out" ;;
  esac
done
[[ "$(sqlite3 "$KANBI_DB" "select count(*) from sessions where is_active=1 and harness_session_ref is not null;")" == 3 ]]
# Opening a live ticket focuses its recorded container without a second attempt.
"$BIN" open T-001
[[ "$(sqlite3 "$KANBI_DB" "select count(*) from sessions where ticket_id=1;")" == 1 ]]
# Exit a raw-command pane and actually resume its stored ref into a new attempt.
old_id="$(sqlite3 "$KANBI_DB" "select id from sessions where ticket_id=1 and is_active=1;")"
old_ref="$(sqlite3 "$KANBI_DB" "select harness_session_ref from sessions where id=$old_id;")"
pane="$(sqlite3 "$KANBI_DB" "select json_extract(mux_metadata, '$.pane_id') from sessions where id=$old_id;")"
herdr pane close "$pane" >/dev/null
"$BIN" open T-001
[[ "$(sqlite3 "$KANBI_DB" "select count(*) from sessions where ticket_id=1;")" == 2 ]]
[[ "$(sqlite3 "$KANBI_DB" "select sum(is_active) from sessions where ticket_id=1;")" == 1 ]]
[[ "$(sqlite3 "$KANBI_DB" "select harness_session_ref from sessions where ticket_id=1 and is_active=1;")" == "$old_ref" ]]
pane="$(sqlite3 "$KANBI_DB" "select json_extract(mux_metadata, '$.pane_id') from sessions where ticket_id=1 and is_active=1;")"
herdr pane wait-output "$pane" --match "RESUMED $old_ref" --source recent --timeout 5000 >/dev/null
echo "smoke ok (Herdr containers, prompts, refs, focus, actual resume, preserved attempts)"
