#!/usr/bin/env bash
set -euo pipefail

: "${KANBI_GITHUB_OWNER:?set KANBI_GITHUB_OWNER}"
: "${KANBI_GITHUB_REPO:?set KANBI_GITHUB_REPO}"
: "${KANBI_GITHUB_TOKEN:=${GITHUB_TOKEN:-}}"
: "${KANBI_GITHUB_TOKEN:?set KANBI_GITHUB_TOKEN or GITHUB_TOKEN}"

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
BIN=${KANBI_BIN:-"$ROOT/.bin/kanbi"}
if [[ ! -x "$BIN" || "$ROOT/cmd" -nt "$BIN" || "$ROOT/internal" -nt "$BIN" ]]; then
  (cd "$ROOT" && go build -o .bin/kanbi ./cmd/kanbi)
fi

work=$(mktemp -d)
cleanup() {
  if [[ -n "${issue_number:-}" ]]; then
    curl -fsS -X PATCH \
      -H "Authorization: Bearer $KANBI_GITHUB_TOKEN" \
      -H "Accept: application/vnd.github+json" \
      -H "X-GitHub-Api-Version: 2022-11-28" \
      "https://api.github.com/repos/$KANBI_GITHUB_OWNER/$KANBI_GITHUB_REPO/issues/$issue_number" \
      -d '{"state":"closed","labels":[]}' >/dev/null || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT

export KANBI_CONFIG="$work/config.yaml"
export KANBI_DB="$work/kanbi.db"
export KANBI_DATA_DIR="$work/data"
export KANBI_STATE_DIR="$work/state"
export KANBI_CACHE_DIR="$work/cache"

label="kanbi-smoke-$(date +%s)-$RANDOM"
api="https://api.github.com/repos/$KANBI_GITHUB_OWNER/$KANBI_GITHUB_REPO"
headers=(-H "Authorization: Bearer $KANBI_GITHUB_TOKEN" -H "Accept: application/vnd.github+json" -H "X-GitHub-Api-Version: 2022-11-28")

issue_json=$(curl -fsS -X POST "${headers[@]}" "$api/issues" \
  -d "{\"title\":\"Kanbi smoke $label\",\"body\":\"remote body\",\"labels\":[\"$label\",\"needs-review\"]}")
issue_number=$(printf '%s' "$issue_json" | python3 -c 'import json,sys; print(json.load(sys.stdin)["number"])')

"$BIN" boards add "GitHub Smoke" --cwd "$work" --backend github \
  --config "{\"owner\":\"$KANBI_GITHUB_OWNER\",\"repo\":\"$KANBI_GITHUB_REPO\"}" \
  --query "state=open,closed&labels=$label"
"$BIN" sync --board "GitHub Smoke"
"$BIN" list --board "GitHub Smoke" | grep -q "GH-$issue_number"

curl -fsS -X PATCH "${headers[@]}" "$api/issues/$issue_number" -d '{"title":"Kanbi smoke remote edited","body":"remote edited body"}' >/dev/null
curl -fsS -X POST "${headers[@]}" "$api/issues/$issue_number/comments" -d '{"body":"remote smoke comment"}' >/dev/null
"$BIN" sync --board "GitHub Smoke"
"$BIN" list --board "GitHub Smoke" | grep -q "Kanbi smoke remote edited"

python3 - "$KANBI_DB" "$issue_number" <<'PY'
import sqlite3, sys
from datetime import datetime, timezone
path, number = sys.argv[1], sys.argv[2]
now = datetime.now(timezone.utc).isoformat()
con = sqlite3.connect(path)
ticket_id = con.execute("select id from tickets where external_id=?", (number,)).fetchone()[0]
con.execute("update tickets set title=?, body=?, updated_at=? where id=?", ("Kanbi smoke local pushed", "local pushed body", now, ticket_id))
con.execute("insert into ticket_notes(ticket_id, body, created_at, updated_at) values(?,?,?,?)", (ticket_id, "local pushed comment", now, now))
con.commit()
PY
"$BIN" sync --board "GitHub Smoke"
issue_after_push=$(curl -fsS "${headers[@]}" "$api/issues/$issue_number")
printf '%s' "$issue_after_push" | grep -q "Kanbi smoke local pushed"
comments_after_push=$(curl -fsS "${headers[@]}" "$api/issues/$issue_number/comments?per_page=100")
printf '%s' "$comments_after_push" | grep -q "local pushed comment"

curl -fsS -X PATCH "${headers[@]}" "$api/issues/$issue_number" -d '{"state":"closed"}' >/dev/null
"$BIN" sync --board "GitHub Smoke"
"$BIN" sync --board "GitHub Smoke"

echo "github backend smoke passed for GH-$issue_number label=$label"
