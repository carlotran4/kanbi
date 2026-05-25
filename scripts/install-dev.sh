#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${AGENT_KANBAN_BIN_DIR:-$HOME/.local/bin}"
CACHE_DIR="${AGENT_KANBAN_DEV_CACHE_DIR:-$ROOT/.bin}"
WRAPPER="$BIN_DIR/agent-kanban"
DEV_BIN="$CACHE_DIR/agent-kanban"

mkdir -p "$BIN_DIR" "$CACHE_DIR"

cat > "$WRAPPER" <<EOF
#!/usr/bin/env bash
set -euo pipefail

ROOT="$ROOT"
DEV_BIN="$DEV_BIN"
LOCK_FILE="\$DEV_BIN.lock"

needs_build() {
  [[ ! -x "\$DEV_BIN" ]] && return 0
  find "\$ROOT/cmd" "\$ROOT/internal" "\$ROOT/go.mod" "\$ROOT/go.sum" -newer "\$DEV_BIN" -print -quit | grep -q .
}

build() {
  mkdir -p "\$(dirname "\$DEV_BIN")"
  echo "agent-kanban: rebuilding development binary..." >&2
  (cd "\$ROOT" && go build -buildvcs=false -o "\$DEV_BIN" ./cmd/agent-kanban)
}

if needs_build; then
  if command -v flock >/dev/null 2>&1; then
    exec 9>"\$LOCK_FILE"
    flock 9
    needs_build && build
  else
    build
  fi
fi

exec "\$DEV_BIN" "\$@"
EOF

chmod +x "$WRAPPER"

if [[ ":$PATH:" != *":$BIN_DIR:"* ]]; then
  cat >&2 <<EOF
Installed $WRAPPER, but $BIN_DIR is not currently on PATH.
Add this to your shell profile:
  export PATH="$BIN_DIR:\$PATH"
EOF
else
  echo "Installed $WRAPPER"
fi

"$WRAPPER" doctor
