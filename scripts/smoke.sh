#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d)"
SESSION="agent-kanban-smoke-$$"
BIN="$TMP/agent-kanban"
FAKE_PI="$ROOT/scripts/fake-harnesses/pi"
FAKE_CODEX="$ROOT/scripts/fake-harnesses/codex"

cleanup() {
  tmux kill-session -t "$SESSION" 2>/dev/null || true
  rm -rf "$TMP"
}
trap cleanup EXIT

export GOCACHE="${GOCACHE:-/tmp/agent-kanban-go-build}"
export AGENT_KANBAN_CONFIG="$TMP/config.yaml"
export AGENT_KANBAN_DB="$TMP/agent-kanban.db"
export AGENT_KANBAN_STATE_DIR="$TMP/state"
export AGENT_KANBAN_DATA_DIR="$TMP/data"
export AGENT_KANBAN_TMUX_SESSION="$SESSION"

cat >"$AGENT_KANBAN_CONFIG" <<YAML
db_path: "$AGENT_KANBAN_DB"
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
YAML

cd "$ROOT"
go fmt ./...
go test ./...
go vet ./...
go build -buildvcs=false -o "$BIN" ./cmd/agent-kanban

"$BIN" doctor
"$BIN" add "Smoke test ticket" --body "Verify smoke path" --harness pi
"$BIN" list | grep -q "T-001"
"$BIN" open T-001 --send-prompt
"$BIN" add "Codex smoke ticket" --body "Verify codex prompt arg" --harness codex
"$BIN" list | grep -q "T-002"
"$BIN" open T-002 --send-prompt

tmux has-session -t "$SESSION"
tmux list-windows -t "$SESSION" -F '#{window_name}' | grep -q '^board$'
tmux list-windows -t "$SESSION" -F '#{window_name}' | grep -q '^T-001-smoke-test-ticket$'
tmux list-windows -t "$SESSION" -F '#{window_name}' | grep -q '^T-002-codex-smoke-ticket$'
sqlite3 "$AGENT_KANBAN_DB" "select tmux_window_id from sessions where is_active=1" | grep -q '^@'

sleep 0.5
OUT="$(tmux capture-pane -p -t "$SESSION:T-001-smoke-test-ticket")"
printf '%s\n' "$OUT" | grep -q '# T-001: Smoke test ticket'
printf '%s\n' "$OUT" | grep -q 'Verify smoke path'
CODEX_OUT="$(tmux capture-pane -p -t "$SESSION:T-002-codex-smoke-ticket")"
printf '%s\n' "$CODEX_OUT" | grep -q '# T-002: Codex smoke ticket'
printf '%s\n' "$CODEX_OUT" | grep -q 'Verify codex prompt arg'

echo "smoke ok"
