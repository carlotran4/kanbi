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

Runs the tmux-backed end-to-end smoke test with fake harnesses and a
fake-Herdr doctor probe. By default this also runs go fmt, go test, and
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
SESSION="kanbi-smoke-$$"
BIN="$TMP/kanbi"
FAKE_PI="$ROOT/scripts/fake-harnesses/pi"
FAKE_CODEX="$ROOT/scripts/fake-harnesses/codex"
FAKE_CLAUDE="$ROOT/scripts/fake-harnesses/claude"
FAKE_HERDR="$TMP/herdr"

cleanup() {
  tmux kill-session -t "$SESSION" 2>/dev/null || true
  rm -rf "$TMP"
}
trap cleanup EXIT

cd "$ROOT"
export GOCACHE="${GOCACHE:-/tmp/kanbi-go-build}"
if [[ "$RUN_CHECKS" == "1" ]]; then
  # Run the repository checks before exporting smoke-only Kanbi paths. Those
  # variables intentionally affect application behavior and would otherwise
  # leak into unit tests that exercise XDG and tmux configuration defaults.
  go fmt ./...
  go test ./...
  go vet ./...
fi

export KANBI_CONFIG="$TMP/config.yaml"
export KANBI_DB="$TMP/kanbi.db"
export KANBI_STATE_DIR="$TMP/state"
export KANBI_DATA_DIR="$TMP/data"
export KANBI_TMUX_SESSION="$SESSION"

cat >"$FAKE_HERDR" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
  status)
    echo '{"ok":true,"fake":true}'
    ;;
  --version)
    echo 'herdr fake-smoke'
    ;;
  *)
    echo "fake herdr: unsupported $*" >&2
    exit 2
    ;;
esac
SH
chmod +x "$FAKE_HERDR"

cat >"$KANBI_CONFIG" <<YAML
db_path: "$KANBI_DB"
tmux_session: "$SESSION"
prompt_ready_timeout: 3s
harnesses:
  pi:
    start: ["$FAKE_PI"]
    resume: ["$FAKE_PI", "--session", "{session_ref}"]
    prompt_mode: "arg"
    prompt_ready: "PROMPT_READY"
    session_ref: "SESSION_REF="
  codex:
    start: ["$FAKE_CODEX", "--no-alt-screen"]
    resume: ["$FAKE_CODEX", "resume", "--no-alt-screen", "{session_ref}"]
    prompt_mode: "arg"
  claude:
    start: ["$FAKE_CLAUDE"]
    resume: ["$FAKE_CLAUDE", "--resume", "{session_ref}"]
    prompt_mode: "arg"
YAML

if [[ -n "${KANBI_SMOKE_BIN:-}" ]]; then
  [[ -x "$KANBI_SMOKE_BIN" ]] || { echo "KANBI_SMOKE_BIN is not executable: $KANBI_SMOKE_BIN" >&2; exit 1; }
  cp "$KANBI_SMOKE_BIN" "$BIN"
  chmod +x "$BIN"
else
  go build -buildvcs=false -o "$BIN" ./cmd/kanbi
fi

# Ref capture must never inspect the operator's real harness histories.
export HOME="$TMP/home"
mkdir -p "$HOME"

"$BIN" doctor
HERDR_CONFIG="$TMP/herdr-config.yaml"
cat >"$HERDR_CONFIG" <<YAML
multiplexer:
  default: herdr
  herdr:
    binary: "$FAKE_HERDR"
    session: smoke
YAML
HERDR_DOCTOR_OUTPUT="$(KANBI_CONFIG="$HERDR_CONFIG" KANBI_DB="$TMP/herdr-doctor.db" "$BIN" doctor)"
grep -q 'ok herdr session smoke' <<<"$HERDR_DOCTOR_OUTPUT"
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

tmux has-session -t "$SESSION"
WINDOWS="$(tmux list-windows -t "$SESSION" -F '#{window_name}')"
grep -q '^board$' <<<"$WINDOWS"
grep -q '^b1-T-001-smoke-test-ticket$' <<<"$WINDOWS"
grep -q '^b1-T-002-codex-smoke-ticket$' <<<"$WINDOWS"
grep -q '^b1-T-003-claude-smoke-ticket$' <<<"$WINDOWS"
ACTIVE_WINDOW_IDS="$(sqlite3 "$KANBI_DB" "select tmux_window_id from sessions where is_active=1")"
grep -q '^@' <<<"$ACTIVE_WINDOW_IDS"

sleep 0.5
OUT="$(tmux capture-pane -p -t "$SESSION:b1-T-001-smoke-test-ticket")"
grep -q '# T-001: Smoke test ticket' <<<"$OUT"
grep -q 'Verify smoke path' <<<"$OUT"
CODEX_OUT="$(tmux capture-pane -p -t "$SESSION:b1-T-002-codex-smoke-ticket")"
grep -q '# T-002: Codex smoke ticket' <<<"$CODEX_OUT"
grep -q 'Verify codex prompt arg' <<<"$CODEX_OUT"
CLAUDE_OUT="$(tmux capture-pane -p -t "$SESSION:b1-T-003-claude-smoke-ticket")"
grep -q '# T-003: Claude smoke ticket' <<<"$CLAUDE_OUT"
grep -q 'Verify claude prompt arg' <<<"$CLAUDE_OUT"

echo "smoke ok"
