#!/usr/bin/env bash
# Validates a built release binary without rebuilding it. The script uses only
# temporary application state and never touches the operator's Kanbi database.
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: ./scripts/release-artifact-smoke.sh BINARY [EXPECTED_VERSION] [EXPECTED_COMMIT] [BUILDINFO_JSON]

Checks release metadata, help, database initialization, normal CLI writes,
full backup/restore (including an attachment), and restored-state integrity.
EXPECTED_VERSION omits the leading v (for example 0.3.0-beta.1). When supplied,
BUILDINFO_JSON must exactly match the binary's provenance fields.
USAGE
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi
if [[ $# -lt 1 || $# -gt 4 ]]; then
  usage >&2
  exit 2
fi

BIN="$1"
EXPECTED_VERSION="${2:-}"
EXPECTED_COMMIT="${3:-}"
BUILDINFO_JSON="${4:-}"
[[ -x "$BIN" ]] || { echo "release binary is not executable: $BIN" >&2; exit 1; }
BIN="$(cd "$(dirname "$BIN")" && pwd)/$(basename "$BIN")"
if [[ -n "$BUILDINFO_JSON" ]]; then
  [[ -f "$BUILDINFO_JSON" ]] || { echo "BUILDINFO_JSON not found: $BUILDINFO_JSON" >&2; exit 1; }
  BUILDINFO_JSON="$(cd "$(dirname "$BUILDINFO_JSON")" && pwd)/$(basename "$BUILDINFO_JSON")"
fi

WORK="$(mktemp -d "${TMPDIR:-/tmp}/kanbi-artifact-smoke.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
export KANBI_CONFIG="$WORK/config.yaml"
export KANBI_DB="$WORK/data/kanbi.db"
export KANBI_DATA_DIR="$WORK/data"
export KANBI_STATE_DIR="$WORK/state"
export KANBI_CACHE_DIR="$WORK/cache"

cat >"$KANBI_CONFIG" <<YAML
multiplexer:
  default: tmux
diagnostics:
  level: off
YAML

"$BIN" --help >/dev/null
"$BIN" version >"$WORK/version.txt"
"$BIN" version --json >"$WORK/version.json"
python3 -E - "$WORK/version.json" "$EXPECTED_VERSION" "$EXPECTED_COMMIT" "$BUILDINFO_JSON" <<'PY'
import json, sys
path, expected_version, expected_commit, buildinfo_path = sys.argv[1:]
doc = json.load(open(path))
if doc.get("schema") != "kanbi.v1.version":
    raise SystemExit(f"unexpected version schema: {doc!r}")
if not isinstance(doc.get("schema_version"), int) or doc["schema_version"] < 1:
    raise SystemExit(f"invalid schema version: {doc!r}")
if expected_version and doc.get("version") != expected_version:
    raise SystemExit(f"version={doc.get('version')!r}, want {expected_version!r}")
if expected_commit and doc.get("commit") != expected_commit:
    raise SystemExit(f"commit={doc.get('commit')!r}, want {expected_commit!r}")
if buildinfo_path:
    build = json.load(open(buildinfo_path))
    field_map = {
        "version": "version",
        "commit": "commit",
        "build_date": "build_date",
        "go_version": "go_version",
        "os": "os",
        "arch": "arch",
        "schema_version": "schema_version",
    }
    for binary_field, build_field in field_map.items():
        if doc.get(binary_field) != build.get(build_field):
            raise SystemExit(
                f"binary {binary_field}={doc.get(binary_field)!r}, "
                f"BUILDINFO {build_field}={build.get(build_field)!r}"
            )
print(f"metadata ok: version={doc['version']} commit={doc['commit']} schema={doc['schema_version']} {doc['os']}/{doc['arch']}")
PY

"$BIN" boards add "Artifact Smoke" --cwd "$WORK" >/dev/null
"$BIN" add "Preserved ticket" --body "preserved body" --board "Artifact Smoke" >/dev/null
"$BIN" notes add T-001 --body "preserved note" --board "Artifact Smoke" >/dev/null
"$BIN" show T-001 --board "Artifact Smoke" --json >"$WORK/before.json"
TICKET_ID="$(python3 -E -c 'import json,sys; print(json.load(open(sys.argv[1]))["ticket"]["id"])' "$WORK/before.json")"
mkdir -p "$KANBI_DATA_DIR/attachments/$TICKET_ID"
printf 'artifact-smoke-attachment\n' >"$KANBI_DATA_DIR/attachments/$TICKET_ID/preserved.txt"

BACKUP="$WORK/pre-mutation.kanbi"
"$BIN" backup "$BACKUP" >/dev/null
[[ -s "$BACKUP" ]] || { echo "backup was not created" >&2; exit 1; }

"$BIN" add "Must disappear after restore" --body "post-backup" --board "Artifact Smoke" >/dev/null
printf 'mutated-after-backup\n' >"$KANBI_DATA_DIR/attachments/$TICKET_ID/preserved.txt"
"$BIN" restore "$BACKUP" --force >/dev/null
"$BIN" list --board "Artifact Smoke" --json >"$WORK/after-list.json"
"$BIN" show T-001 --board "Artifact Smoke" --json >"$WORK/after-show.json"

python3 -E - "$WORK/after-list.json" "$WORK/after-show.json" "$KANBI_DB" "$KANBI_DATA_DIR/attachments/$TICKET_ID/preserved.txt" <<'PY'
import json, pathlib, sqlite3, sys
listing = json.load(open(sys.argv[1]))
detail = json.load(open(sys.argv[2]))
titles = [ticket.get("title") for ticket in listing.get("tickets", [])]
if "Preserved ticket" not in titles or "Must disappear after restore" in titles:
    raise SystemExit(f"restored ticket set is incorrect: {titles!r}")
blob = json.dumps(detail)
for expected in ("preserved body", "preserved note"):
    if expected not in blob:
        raise SystemExit(f"restored ticket is missing {expected!r}")
attachment = pathlib.Path(sys.argv[4])
if attachment.read_text() != "artifact-smoke-attachment\n":
    raise SystemExit("attachment was not restored to its backup contents")
con = sqlite3.connect(sys.argv[3])
if con.execute("pragma integrity_check").fetchone()[0] != "ok":
    raise SystemExit("SQLite integrity_check failed")
if con.execute("pragma foreign_key_check").fetchall():
    raise SystemExit("SQLite foreign-key consistency check failed")
print("database initialization and backup/restore integrity ok")
PY

echo "release artifact smoke: PASS"
