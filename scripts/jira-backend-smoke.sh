#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<'USAGE'
Usage: ./scripts/jira-backend-smoke.sh

Opt-in real Atlassian/Jira backend smoke test. Required env:
  AGENT_KANBAN_JIRA_SITE_URL       e.g. https://example.atlassian.net
  AGENT_KANBAN_JIRA_PROJECT_KEY    e.g. KAN
  AGENT_KANBAN_JIRA_EMAIL          required with AGENT_KANBAN_JIRA_API_TOKEN
  AGENT_KANBAN_JIRA_API_TOKEN      Atlassian API token (or use bearer token)
  AGENT_KANBAN_JIRA_BEARER_TOKEN   optional alternative to email/api token

Optional env:
  AGENT_KANBAN_JIRA_ISSUE_TYPE     default: Task
USAGE
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi
if [[ $# -ne 0 ]]; then
  echo "unknown argument: $1" >&2
  usage >&2
  exit 2
fi

require_env() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "missing required env: $name" >&2
    usage >&2
    exit 2
  fi
}

require_env AGENT_KANBAN_JIRA_SITE_URL
require_env AGENT_KANBAN_JIRA_PROJECT_KEY
if [[ -n "${AGENT_KANBAN_JIRA_BEARER_TOKEN:-}" ]]; then
  :
else
  require_env AGENT_KANBAN_JIRA_EMAIL
  require_env AGENT_KANBAN_JIRA_API_TOKEN
fi

TMP="$(mktemp -d)"
BIN="$TMP/agent-kanban"
MARKER="ak-smoke-$(date +%Y%m%d%H%M%S)-$$"
LABEL="ak-smoke-$$"
BOARD="Jira Smoke $MARKER"
PULL_SUMMARY="$MARKER pull remote issue"
PULL_UPDATED_SUMMARY="$MARKER locally updated pulled issue"
REMOTE_COMMENT_BODY="$MARKER remote comment pull sync"
PUSH_SUMMARY="$MARKER push local issue"
PUSH_BODY="Created by Agent Kanban Jira real-backend smoke test $MARKER"
NOTE_BODY="$MARKER local note/comment sync"
CREATED_KEYS="$TMP/created-keys.txt"
: >"$CREATED_KEYS"

cleanup() {
  set +e
  if [[ -s "$CREATED_KEYS" ]]; then
    while IFS= read -r key; do
      [[ -n "$key" ]] || continue
      python3 "$TMP/jira_api.py" cleanup "$key" >/dev/null 2>&1 || true
    done <"$CREATED_KEYS"
  fi
  rm -rf "$TMP"
}
trap cleanup EXIT

export GOCACHE="${GOCACHE:-/tmp/agent-kanban-go-build}"
export AGENT_KANBAN_CONFIG="$TMP/config.yaml"
export AGENT_KANBAN_DB="$TMP/agent-kanban.db"
export AGENT_KANBAN_STATE_DIR="$TMP/state"
export AGENT_KANBAN_DATA_DIR="$TMP/data"

cat >"$AGENT_KANBAN_CONFIG" <<YAML
db_path: "$AGENT_KANBAN_DB"
YAML

cat >"$TMP/jira_api.py" <<'PY'
#!/usr/bin/env python3
import base64, json, os, sys, urllib.error, urllib.parse, urllib.request

site = os.environ["AGENT_KANBAN_JIRA_SITE_URL"].rstrip("/")
project = os.environ["AGENT_KANBAN_JIRA_PROJECT_KEY"]
issue_type = os.environ.get("AGENT_KANBAN_JIRA_ISSUE_TYPE", "Task")
email = os.environ.get("AGENT_KANBAN_JIRA_EMAIL", "")
api_token = os.environ.get("AGENT_KANBAN_JIRA_API_TOKEN", "")
bearer = os.environ.get("AGENT_KANBAN_JIRA_BEARER_TOKEN", "")

def adf(text):
    return {"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":text}]}]}

def request(method, path, body=None, ok=(200,201,204)):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(site + path, data=data, method=method)
    req.add_header("Accept", "application/json")
    if body is not None:
        req.add_header("Content-Type", "application/json")
    if bearer:
        req.add_header("Authorization", "Bearer " + bearer)
    else:
        token = base64.b64encode((email + ":" + api_token).encode()).decode()
        req.add_header("Authorization", "Basic " + token)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read()
            if resp.status not in ok:
                raise SystemExit(f"Jira {method} {path} returned HTTP {resp.status}: {raw.decode(errors='replace')}")
            if not raw:
                return {}
            return json.loads(raw.decode())
    except urllib.error.HTTPError as e:
        raw = e.read().decode(errors='replace')
        raise SystemExit(f"Jira {method} {path} returned HTTP {e.code}: {raw}")

def create(summary, body, label):
    payload = {"fields": {"project": {"key": project}, "issuetype": {"name": issue_type}, "summary": summary, "description": adf(body), "labels": [label]}}
    out = request("POST", "/rest/api/3/issue", payload)
    print(out["key"])

def search(jql):
    qs = urllib.parse.urlencode({"jql": jql, "fields": "summary,status,updated", "maxResults": "50"})
    out = request("GET", "/rest/api/3/search/jql?" + qs)
    print(json.dumps(out.get("issues", [])))

def add_comment(key, body):
    out = request("POST", "/rest/api/3/issue/" + urllib.parse.quote(key) + "/comment", {"body": adf(body)})
    print(out["id"])

def comments(key, needle):
    out = request("GET", "/rest/api/3/issue/" + urllib.parse.quote(key) + "/comment")
    text = json.dumps(out.get("comments", []))
    if needle not in text:
        raise SystemExit(f"comment marker not found on {key}: {needle}")
    print("comment ok")

def cleanup(key):
    quoted = urllib.parse.quote(key)
    try:
        request("DELETE", "/rest/api/3/issue/" + quoted, ok=(204,))
        print(f"deleted {key}")
        return
    except SystemExit as delete_err:
        last = str(delete_err)
    try:
        trans = request("GET", "/rest/api/3/issue/" + quoted + "/transitions").get("transitions", [])
        for wanted in ("done", "closed", "complete", "resolved"):
            for t in trans:
                if t.get("name", "").strip().lower() == wanted:
                    request("POST", "/rest/api/3/issue/" + quoted + "/transitions", {"transition": {"id": t["id"]}}, ok=(204,))
                    print(f"transitioned {key} via {t['name']}")
                    return
        print(f"cleanup left {key}: delete failed and no Done/Closed transition found; {last}", file=sys.stderr)
    except SystemExit as transition_err:
        print(f"cleanup left {key}: delete failed; transition failed: {transition_err}", file=sys.stderr)

cmd = sys.argv[1]
if cmd == "create":
    create(sys.argv[2], sys.argv[3], sys.argv[4])
elif cmd == "search":
    search(sys.argv[2])
elif cmd == "add-comment":
    add_comment(sys.argv[2], sys.argv[3])
elif cmd == "comments":
    comments(sys.argv[2], sys.argv[3])
elif cmd == "cleanup":
    cleanup(sys.argv[2])
else:
    raise SystemExit("unknown jira_api command: " + cmd)
PY
chmod +x "$TMP/jira_api.py"

cd "$ROOT"
go build -buildvcs=false -o "$BIN" ./cmd/agent-kanban

echo "creating temporary Jira pull issue ($MARKER)"
PULL_KEY="$(python3 "$TMP/jira_api.py" create "$PULL_SUMMARY" "Created by Agent Kanban Jira smoke pull path $MARKER" "$LABEL")"
echo "$PULL_KEY" >>"$CREATED_KEYS"
python3 "$TMP/jira_api.py" add-comment "$PULL_KEY" "$REMOTE_COMMENT_BODY" >/dev/null

CONFIG_JSON="{\"site_url\":\"${AGENT_KANBAN_JIRA_SITE_URL}\",\"project_key\":\"${AGENT_KANBAN_JIRA_PROJECT_KEY}\",\"email\":\"${AGENT_KANBAN_JIRA_EMAIL:-}\",\"issue_type\":\"${AGENT_KANBAN_JIRA_ISSUE_TYPE:-Task}\"}"
JQL="project = ${AGENT_KANBAN_JIRA_PROJECT_KEY} AND labels = \"${LABEL}\" ORDER BY updated DESC"

"$BIN" boards add "$BOARD" --backend atlassian --config "$CONFIG_JSON" --query "$JQL" >/dev/null
SYNC_OUT="$("$BIN" sync --board "$BOARD")"
echo "$SYNC_OUT"
printf '%s\n' "$SYNC_OUT" | grep -Eq 'pulled=[1-9]'
LIST_OUT="$("$BIN" list --board "$BOARD")"
printf '%s\n' "$LIST_OUT" | grep -F "$PULL_KEY"
printf '%s\n' "$LIST_OUT" | grep -F "$PULL_SUMMARY"
python3 - "$AGENT_KANBAN_DB" "$PULL_SUMMARY" "$REMOTE_COMMENT_BODY" <<'PY'
import sqlite3, sys
path, title, body = sys.argv[1:4]
conn = sqlite3.connect(path)
row = conn.execute("select t.id from tickets t join ticket_notes n on n.ticket_id=t.id where t.title=? and n.body=?", (title, body)).fetchone()
if not row:
    raise SystemExit("remote Jira comment was not pulled into local notes")
PY

echo "updating pulled ticket locally, then pushing update to Jira"
python3 - "$AGENT_KANBAN_DB" "$PULL_SUMMARY" "$PULL_UPDATED_SUMMARY" <<'PY'
import sqlite3, sys, time
path, old_title, new_title = sys.argv[1:4]
conn = sqlite3.connect(path)
future = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 120))
cur = conn.execute("update tickets set title=?, body=?, updated_at=? where title=?", (new_title, "Updated locally by Jira smoke", future, old_title))
if cur.rowcount != 1:
    raise SystemExit("could not update pulled local ticket")
conn.commit()
PY
SYNC_OUT="$("$BIN" sync --board "$BOARD")"
echo "$SYNC_OUT"
printf '%s\n' "$SYNC_OUT" | grep -Eq 'pushed=[1-9]'
python3 "$TMP/jira_api.py" search "project = ${AGENT_KANBAN_JIRA_PROJECT_KEY} AND key = ${PULL_KEY}" | grep -F "$PULL_UPDATED_SUMMARY" >/dev/null


echo "adding local ticket and note, then pushing to Jira"
ADD_OUT="$("$BIN" add "$PUSH_SUMMARY" --body "$PUSH_BODY" --board "$BOARD")"
echo "$ADD_OUT"
python3 - "$AGENT_KANBAN_DB" "$PUSH_SUMMARY" "$NOTE_BODY" <<'PY'
import sqlite3, sys, time
path, title, body = sys.argv[1:4]
conn = sqlite3.connect(path)
row = conn.execute("select id from tickets where title=?", (title,)).fetchone()
if not row:
    raise SystemExit("could not find local pushed ticket in DB")
now = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
conn.execute("insert into ticket_notes(ticket_id, body, created_at, updated_at) values(?,?,?,?)", (row[0], body, now, now))
conn.commit()
PY
SYNC_OUT="$("$BIN" sync --board "$BOARD")"
echo "$SYNC_OUT"
printf '%s\n' "$SYNC_OUT" | grep -Eq 'pushed=[1-9]'

PUSH_KEY="$(python3 - "$AGENT_KANBAN_DB" "$PUSH_SUMMARY" <<'PY'
import sqlite3, sys
conn = sqlite3.connect(sys.argv[1])
row = conn.execute("select display_id from tickets where title=? and external_id is not null", (sys.argv[2],)).fetchone()
if not row:
    raise SystemExit("pushed ticket has no Jira key/external id")
print(row[0])
PY
)"
echo "$PUSH_KEY" >>"$CREATED_KEYS"
python3 "$TMP/jira_api.py" search "project = ${AGENT_KANBAN_JIRA_PROJECT_KEY} AND key = ${PUSH_KEY}" | grep -F "$PUSH_SUMMARY" >/dev/null
python3 "$TMP/jira_api.py" comments "$PUSH_KEY" "$NOTE_BODY" >/dev/null

echo "jira backend smoke ok: pulled $PULL_KEY, pushed $PUSH_KEY (cleanup best effort)"
