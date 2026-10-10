#!/usr/bin/env bash
# Automated upgrade + rollback drill between two Kanbi release artifacts
# (or local build-release snapshots). Uses a temporary data/config tree
# so it never touches the operator's real Kanbi state.
#
# Usage:
#   ./scripts/upgrade-rollback-drill.sh
#   OLD_ARCHIVE=/path/to/kanbi_A_linux_amd64.tar.gz \
#   NEW_ARCHIVE=/path/to/kanbi_B_linux_amd64.tar.gz \
#   ./scripts/upgrade-rollback-drill.sh
#
# When archives are omitted, the script builds two local snapshots via
# scripts/build-release.sh (old=drill-old, new=drill-new) from the current
# tree; that still validates packaging + backup/restore integrity even when
# both binaries share the same schema.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OS="$(go env GOOS)"
ARCH="$(go env GOARCH)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/kanbi-upgrade-drill.XXXXXX")"
RESULT_DIR="${KANBI_DRILL_RESULT_DIR:-$ROOT/dist/verification}"
RUN_STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
RESULT_FILE="${RESULT_DIR}/upgrade-rollback-drill-${RUN_STAMP}.txt"
LATEST_RESULT_FILE="${RESULT_DIR}/upgrade-rollback-drill-latest.txt"
trap 'rm -rf "$WORK"' EXIT

log() { printf '%s\n' "$*" | tee -a "$WORK/drill.log"; }
die() { log "FAIL: $*"; exit 1; }
sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

mkdir -p "$WORK/old" "$WORK/new" "$WORK/data" "$WORK/state" "$WORK/cfg" "$RESULT_DIR"
if [[ -n "${OLD_ARCHIVE:-}" && -z "${NEW_ARCHIVE:-}" ]] || [[ -z "${OLD_ARCHIVE:-}" && -n "${NEW_ARCHIVE:-}" ]]; then
  die "set both OLD_ARCHIVE and NEW_ARCHIVE, or neither"
fi

build_snapshot() {
  local version="$1"
  local out_dir="$2"
  (cd "$ROOT" && KANBI_RELEASE_DIR="$out_dir" ./scripts/build-release.sh "$version" >/dev/null)
  local archive="$out_dir/kanbi_${version}_${OS}_${ARCH}.tar.gz"
  [[ -f "$archive" ]] || die "missing snapshot archive $archive"
  printf '%s\n' "$archive"
}

extract_bin() {
  local archive="$1"
  local dest="$2"
  tar -xzf "$archive" -C "$dest"
  [[ -x "$dest/kanbi" ]] || die "archive $archive missing kanbi binary"
}

if [[ -n "${OLD_ARCHIVE:-}" ]]; then
  OLD_TGZ="$OLD_ARCHIVE"
else
  log "Building OLD snapshot from current tree (drill-old)…"
  OLD_TGZ="$(build_snapshot drill-old "$WORK/dist-old")"
fi
if [[ -n "${NEW_ARCHIVE:-}" ]]; then
  NEW_TGZ="$NEW_ARCHIVE"
else
  log "Building NEW snapshot from current tree (drill-new)…"
  NEW_TGZ="$(build_snapshot drill-new "$WORK/dist-new")"
fi

log "OLD_ARCHIVE=$(basename "$OLD_TGZ")"
log "NEW_ARCHIVE=$(basename "$NEW_TGZ")"
extract_bin "$OLD_TGZ" "$WORK/old"
extract_bin "$NEW_TGZ" "$WORK/new"

export KANBI_CONFIG="$WORK/cfg/config.yaml"
export KANBI_DATA_DIR="$WORK/data"
export KANBI_STATE_DIR="$WORK/state"
export KANBI_DB="$WORK/data/kanbi.db"
export KANBI_TMUX_SESSION="kanbi-upgrade-drill-$$"
# Avoid kicking interactive doctors that require Herdr servers when possible.
cat >"$KANBI_CONFIG" <<'YAML'
default_harness: pi
multiplexer:
  default: herdr
diagnostics:
  level: off
YAML

OLD_BIN="$WORK/old/kanbi"
NEW_BIN="$WORK/new/kanbi"
OLD_VERSION_TEXT="$($OLD_BIN version)"
NEW_VERSION_TEXT="$($NEW_BIN version)"
OLD_COMMIT="$(printf '%s\n' "$OLD_VERSION_TEXT" | sed -n 's/^commit: //p' | head -n1)"
NEW_COMMIT="$(printf '%s\n' "$NEW_VERSION_TEXT" | sed -n 's/^commit: //p' | head -n1)"
OLD_SCHEMA="$(printf '%s\n' "$OLD_VERSION_TEXT" | sed -n 's/^database schema: //p' | head -n1)"
NEW_SCHEMA="$(printf '%s\n' "$NEW_VERSION_TEXT" | sed -n 's/^database schema: //p' | head -n1)"
if [[ -n "${OLD_ARCHIVE:-}" && -n "${NEW_ARCHIVE:-}" && "$OLD_COMMIT" == "$NEW_COMMIT" ]]; then
  die "qualification archives resolve to the same commit $OLD_COMMIT"
fi

log "=== OLD version ==="
"$OLD_BIN" version | tee -a "$WORK/drill.log"
log "=== Seed data with OLD binary ==="
"$OLD_BIN" boards add "Drill Board" --cwd "$WORK" | tee -a "$WORK/drill.log"
"$OLD_BIN" add "Keep this ticket" --body "body-for-rollback" --board "Drill Board" | tee -a "$WORK/drill.log"
"$OLD_BIN" notes add T-001 --body "note-for-rollback" --board "Drill Board" | tee -a "$WORK/drill.log"

ATTACH_DIR="$WORK/data/attachments"
TICKET_ID="$("$OLD_BIN" show T-001 --board "Drill Board" --json | python3 -E -c 'import sys,json; print(json.load(sys.stdin)["ticket"]["id"])' 2>/dev/null || true)"
[[ -n "${TICKET_ID:-}" ]] || die "could not resolve ticket id for required attachment/session fixtures"
mkdir -p "$ATTACH_DIR/$TICKET_ID"
printf 'attachment-bytes\n' >"$ATTACH_DIR/$TICKET_ID/drill.txt"
log "Seeded required attachment for ticket id $TICKET_ID"

python3 -E - "$KANBI_DB" "$TICKET_ID" <<'PY' | tee -a "$WORK/drill.log" || die "could not seed session-history fixtures"
import datetime, sqlite3, sys
path, ticket_id = sys.argv[1], int(sys.argv[2])
now = datetime.datetime.now(datetime.timezone.utc).isoformat()
con = sqlite3.connect(path)
rows = [
    (ticket_id, "pi", "drill-closed-ref", "drill-runtime", "@closed", "b1-T-001-drill-closed", "closed", 0, now, now, now, now),
    (ticket_id, "pi", "drill-active-ref", "drill-runtime", "@active", "b1-T-001-drill-active", "running", 1, now, None, now, now),
]
con.executemany("""
insert into sessions(
  ticket_id,harness,harness_session_ref,tmux_session_name,tmux_window_id,tmux_window_name,
  status,is_active,started_at,closed_at,created_at,updated_at
) values(?,?,?,?,?,?,?,?,?,?,?,?)
""", rows)
con.commit()
assert con.execute("select count(*) from sessions where ticket_id=?", (ticket_id,)).fetchone()[0] == 2
assert con.execute("select count(*) from sessions where ticket_id=? and is_active=1", (ticket_id,)).fetchone()[0] == 1
print("seeded active and inactive session history")
PY

PRE_UPGRADE_BACKUP="$WORK/pre-upgrade.kanbi"
"$OLD_BIN" backup "$PRE_UPGRADE_BACKUP" | tee -a "$WORK/drill.log"
[[ -f "$PRE_UPGRADE_BACKUP" ]] || die "backup missing"
PRE_UPGRADE_BACKUP_SHA256="$(sha256_file "$PRE_UPGRADE_BACKUP")"
log "Pre-upgrade backup sha256=$PRE_UPGRADE_BACKUP_SHA256"

log "=== Upgrade: open DB with NEW binary ==="
"$NEW_BIN" version | tee -a "$WORK/drill.log"
"$NEW_BIN" doctor --json >/dev/null || log "WARN: doctor returned non-zero (may be missing Herdr in environment)"
"$NEW_BIN" list --board "Drill Board" --json | tee "$WORK/after-upgrade-list.json" | tee -a "$WORK/drill.log"
"$NEW_BIN" show T-001 --board "Drill Board" --json | tee "$WORK/after-upgrade-show.json" | tee -a "$WORK/drill.log"

python3 -E - <<'PY' "$WORK/after-upgrade-show.json" | tee -a "$WORK/drill.log" || die "upgrade lost ticket fields"
import json,sys
doc=json.load(open(sys.argv[1]))
t=doc["ticket"]
assert t.get("title")=="Keep this ticket" or t.get("Title")=="Keep this ticket" or "Keep this ticket" in json.dumps(t), t
body=t.get("body") or t.get("Body") or ""
assert "body-for-rollback" in body or "body-for-rollback" in json.dumps(doc), doc
print("upgrade ticket fields ok")
PY

python3 -E - "$KANBI_DB" "$TICKET_ID" "$ATTACH_DIR/$TICKET_ID/drill.txt" <<'PY' | tee -a "$WORK/drill.log" || die "post-upgrade durable-state integrity failed"
import pathlib, sqlite3, sys
path, ticket_id, attachment = sys.argv[1], int(sys.argv[2]), pathlib.Path(sys.argv[3])
con = sqlite3.connect(path)
con.execute("pragma foreign_keys=on")
assert con.execute("pragma foreign_key_check").fetchall() == []
assert con.execute("pragma integrity_check").fetchone()[0] == "ok"
rows = con.execute("select harness_session_ref,status,is_active from sessions where ticket_id=? order by id", (ticket_id,)).fetchall()
assert rows == [("drill-closed-ref", "closed", 0), ("drill-active-ref", "running", 1)], rows
assert con.execute("select body from ticket_notes where ticket_id=?", (ticket_id,)).fetchall() == [("note-for-rollback",)]
assert attachment.read_text() == "attachment-bytes\n"
print("upgrade preserved note, attachment, and complete session history")
PY

DOWNGRADE_RESULT="not_run"
if [[ "$OLD_SCHEMA" =~ ^[0-9]+$ && "$NEW_SCHEMA" =~ ^[0-9]+$ && "$OLD_SCHEMA" -lt "$NEW_SCHEMA" ]]; then
  log "=== Attempted in-place downgrade must be rejected ==="
  set +e
  DOWNGRADE_OUTPUT="$($OLD_BIN list --board "Drill Board" --json 2>&1)"
  DOWNGRADE_STATUS=$?
  set -e
  [[ $DOWNGRADE_STATUS -ne 0 ]] || die "old artifact unexpectedly opened schema $NEW_SCHEMA database"
  printf '%s\n' "$DOWNGRADE_OUTPUT" | grep -qi 'newer than supported' || die "downgrade rejection lacked newer-schema explanation: $DOWNGRADE_OUTPUT"
  log "Old artifact rejected newer schema as expected: $DOWNGRADE_OUTPUT"
  DOWNGRADE_RESULT="passed"
elif [[ -n "${OLD_ARCHIVE:-}" && -n "${NEW_ARCHIVE:-}" ]]; then
  die "qualification drill requires an older schema (old=$OLD_SCHEMA new=$NEW_SCHEMA)"
else
  DOWNGRADE_RESULT="skipped_same_schema_local_snapshots"
  log "SKIP: downgrade rejection requires explicit artifacts with old schema < new schema"
fi

log "=== Simulate post-upgrade changes then rollback via restore ==="
"$NEW_BIN" add "Should disappear on rollback" --body "ephemeral" --board "Drill Board" | tee -a "$WORK/drill.log"
printf 'post-upgrade-attachment-bytes\n' >"$ATTACH_DIR/$TICKET_ID/drill.txt"
log "Mutated attachment after backup; restore must recover original bytes"

# Restore must refuse while WAL/SHM present sometimes; stop= nothing holding DB.
# checkpoint via reopen is fine; remove sidecars if present after close.
# No long-lived process holds the DB here.

log "=== Rollback: restore pre-upgrade backup with OLD binary ==="
"$OLD_BIN" restore "$PRE_UPGRADE_BACKUP" --force | tee -a "$WORK/drill.log"
"$OLD_BIN" list --board "Drill Board" --json | tee "$WORK/after-rollback-list.json" | tee -a "$WORK/drill.log"
"$OLD_BIN" show T-001 --board "Drill Board" --json | tee "$WORK/after-rollback-show.json" | tee -a "$WORK/drill.log"

python3 -E - <<'PY' "$WORK/after-rollback-list.json" "$WORK/after-rollback-show.json" "$KANBI_DB" "$TICKET_ID" "$ATTACH_DIR/$TICKET_ID/drill.txt" | tee -a "$WORK/drill.log" || die "rollback restore integrity failed"
import json,pathlib,sqlite3,sys
listing=json.load(open(sys.argv[1]))
tickets=listing.get("tickets") or []
titles=[]
for t in tickets:
    titles.append(t.get("title") or t.get("Title") or "")
assert "Keep this ticket" in titles, titles
assert "Should disappear on rollback" not in titles, titles
doc=json.load(open(sys.argv[2]))
assert "body-for-rollback" in json.dumps(doc)
notes=doc.get("notes") or doc.get("ticket",{}).get("notes") or []
# notes may be nested
blob=json.dumps(doc)
assert "note-for-rollback" in blob, doc
db_path, ticket_id, attachment = sys.argv[3], int(sys.argv[4]), pathlib.Path(sys.argv[5])
con=sqlite3.connect(db_path)
con.execute("pragma foreign_keys=on")
assert con.execute("pragma foreign_key_check").fetchall() == []
assert con.execute("pragma integrity_check").fetchone()[0] == "ok"
sessions=con.execute("select harness_session_ref,status,is_active from sessions where ticket_id=? order by id", (ticket_id,)).fetchall()
assert sessions == [("drill-closed-ref", "closed", 0), ("drill-active-ref", "running", 1)], sessions
assert attachment.read_text() == "attachment-bytes\n"
print("rollback ticket, note, attachment, session history, and foreign-key consistency ok")
PY
log "Required attachment and complete session history intact after rollback"

log "=== PASS ==="
{
  echo "upgrade-rollback-drill: PASS"
  echo "date_utc: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "host: $(uname -s) $(uname -m)"
  echo "old_archive: $(basename "$OLD_TGZ")"
  echo "new_archive: $(basename "$NEW_TGZ")"
  echo "old_version: $("$OLD_BIN" version | tr '\n' ' ' | sed 's/[[:space:]]*$//')"
  echo "new_version: $("$NEW_BIN" version | tr '\n' ' ' | sed 's/[[:space:]]*$//')"
  echo "old_commit: $OLD_COMMIT"
  echo "new_commit: $NEW_COMMIT"
  echo "old_schema: $OLD_SCHEMA"
  echo "new_schema: $NEW_SCHEMA"
  echo "backup_sha256: $PRE_UPGRADE_BACKUP_SHA256"
  echo "downgrade_rejection: $DOWNGRADE_RESULT"
  echo "result: migration, rollback, tickets, notes, required attachment restoration, session history, SQLite integrity, and foreign-key consistency passed; downgrade=$DOWNGRADE_RESULT"
  echo "log: $RESULT_FILE"
  echo
  cat "$WORK/drill.log"
} >"$RESULT_FILE"
cp "$RESULT_FILE" "$LATEST_RESULT_FILE"

printf 'wrote %s\n' "$RESULT_FILE"
printf 'updated %s\n' "$LATEST_RESULT_FILE"
tail -n 30 "$RESULT_FILE"
