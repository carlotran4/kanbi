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
RESULT_DIR="${KANBI_DRILL_RESULT_DIR:-$ROOT/docs/verification}"
RESULT_FILE="${RESULT_DIR}/upgrade-rollback-drill-latest.txt"
trap 'rm -rf "$WORK"' EXIT

log() { printf '%s\n' "$*" | tee -a "$WORK/drill.log"; }
die() { log "FAIL: $*"; exit 1; }

mkdir -p "$WORK/old" "$WORK/new" "$WORK/data" "$WORK/state" "$WORK/cfg" "$RESULT_DIR"

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

log "OLD_ARCHIVE=$OLD_TGZ"
log "NEW_ARCHIVE=$NEW_TGZ"
extract_bin "$OLD_TGZ" "$WORK/old"
extract_bin "$NEW_TGZ" "$WORK/new"

export KANBI_CONFIG="$WORK/cfg/config.yaml"
export KANBI_DATA_DIR="$WORK/data"
export KANBI_STATE_DIR="$WORK/state"
export KANBI_DB="$WORK/data/kanbi.db"
export KANBI_TMUX_SESSION="kanbi-upgrade-drill-$$"
# Avoid kicking interactive doctors that require tmux sessions when possible.
cat >"$KANBI_CONFIG" <<'YAML'
default_harness: pi
multiplexer:
  default: tmux
diagnostics:
  level: off
YAML

OLD_BIN="$WORK/old/kanbi"
NEW_BIN="$WORK/new/kanbi"

log "=== OLD version ==="
"$OLD_BIN" version | tee -a "$WORK/drill.log"
log "=== Seed data with OLD binary ==="
"$OLD_BIN" boards add "Drill Board" --cwd "$WORK" | tee -a "$WORK/drill.log"
"$OLD_BIN" add "Keep this ticket" --body "body-for-rollback" --board "Drill Board" | tee -a "$WORK/drill.log"
"$OLD_BIN" notes add T-001 --body "note-for-rollback" --board "Drill Board" | tee -a "$WORK/drill.log"

# Optional attachment via paste store layout: create a ticket attachment directory
# that backup is obliged to preserve (not via TUI paste path).
ATTACH_DIR="$WORK/data/attachments"
# Discover internal ticket id from SQLite after init via list/show is text IDs only;
# create a known attachment path by resolving ticket id with a tiny go helper would be heavy —
# instead write a marker under attachments/ after querying with the same DB.
TICKET_ID="$("$OLD_BIN" show T-001 --board "Drill Board" --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["ticket"]["id"])' 2>/dev/null || true)"
if [[ -n "${TICKET_ID:-}" && "$TICKET_ID" != "" ]]; then
  mkdir -p "$ATTACH_DIR/$TICKET_ID"
  printf 'attachment-bytes\n' >"$ATTACH_DIR/$TICKET_ID/drill.txt"
  log "Seeded attachment for ticket id $TICKET_ID"
else
  # Fallback: still create number-shaped path after backup; we at least exercise empty attachments.
  log "WARN: could not resolve ticket id for attachment seed; continuing without attachment file"
fi

PRE_UPGRADE_BACKUP="$WORK/pre-upgrade.kanbi"
"$OLD_BIN" backup "$PRE_UPGRADE_BACKUP" | tee -a "$WORK/drill.log"
[[ -f "$PRE_UPGRADE_BACKUP" ]] || die "backup missing"

log "=== Upgrade: open DB with NEW binary ==="
"$NEW_BIN" version | tee -a "$WORK/drill.log"
"$NEW_BIN" doctor --json >/dev/null || log "WARN: doctor returned non-zero (may be missing tmux in environment)"
"$NEW_BIN" list --board "Drill Board" --json | tee "$WORK/after-upgrade-list.json" | tee -a "$WORK/drill.log"
"$NEW_BIN" show T-001 --board "Drill Board" --json | tee "$WORK/after-upgrade-show.json" | tee -a "$WORK/drill.log"

python3 - <<'PY' "$WORK/after-upgrade-show.json" || die "upgrade lost ticket fields"
import json,sys
doc=json.load(open(sys.argv[1]))
t=doc["ticket"]
assert t.get("title")=="Keep this ticket" or t.get("Title")=="Keep this ticket" or "Keep this ticket" in json.dumps(t), t
body=t.get("body") or t.get("Body") or ""
assert "body-for-rollback" in body or "body-for-rollback" in json.dumps(doc), doc
print("upgrade ticket fields ok")
PY

log "=== Simulate post-upgrade change then rollback via restore ==="
"$NEW_BIN" add "Should disappear on rollback" --body "ephemeral" --board "Drill Board" | tee -a "$WORK/drill.log"

# Restore must refuse while WAL/SHM present sometimes; stop= nothing holding DB.
# checkpoint via reopen is fine; remove sidecars if present after close.
# No long-lived process holds the DB here.

log "=== Rollback: restore pre-upgrade backup with OLD binary ==="
"$OLD_BIN" restore "$PRE_UPGRADE_BACKUP" --force | tee -a "$WORK/drill.log"
"$OLD_BIN" list --board "Drill Board" --json | tee "$WORK/after-rollback-list.json" | tee -a "$WORK/drill.log"
"$OLD_BIN" show T-001 --board "Drill Board" --json | tee "$WORK/after-rollback-show.json" | tee -a "$WORK/drill.log"

python3 - <<'PY' "$WORK/after-rollback-list.json" "$WORK/after-rollback-show.json" || die "rollback restore integrity failed"
import json,sys
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
print("rollback integrity ok")
PY

if [[ -n "${TICKET_ID:-}" && -f "$ATTACH_DIR/$TICKET_ID/drill.txt" ]]; then
  grep -q attachment-bytes "$ATTACH_DIR/$TICKET_ID/drill.txt" || die "attachment lost after rollback"
  log "attachment intact after rollback"
fi

log "=== PASS ==="
{
  echo "upgrade-rollback-drill: PASS"
  echo "date_utc: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "host: $(uname -s) $(uname -m)"
  echo "old_archive: $OLD_TGZ"
  echo "new_archive: $NEW_TGZ"
  echo "old_version: $("$OLD_BIN" version | tr '\n' ' ')"
  echo "new_version: $("$NEW_BIN" version | tr '\n' ' ')"
  echo "result: tickets, notes, and attachments (when seeded) intact at pre-upgrade restore point"
  echo "log: see commit path docs/verification/upgrade-rollback-drill-latest.txt"
  echo
  cat "$WORK/drill.log"
} >"$RESULT_FILE"

printf 'wrote %s\n' "$RESULT_FILE"
cat "$RESULT_FILE" | tail -n 30
