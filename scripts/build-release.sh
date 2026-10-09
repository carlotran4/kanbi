#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${1:-0.0.0-dev}"
VERSION="${VERSION#v}"
OUT_DIR="${KANBI_RELEASE_DIR:-$ROOT/dist}"
OS="$(go env GOOS)"
ARCH="$(go env GOARCH)"
COMMIT="$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || printf unknown)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
GO_VERSION="$(go env GOVERSION 2>/dev/null || go version | awk '{print $3}')"
PACKAGE_DIR="$(mktemp -d)"
trap 'rm -rf "$PACKAGE_DIR"' EXIT

mkdir -p "$OUT_DIR" "$PACKAGE_DIR/package"
cd "$ROOT"
CGO_ENABLED=1 go build -trimpath \
  -ldflags "-s -w -X github.com/carlotran4/kanbi/internal/buildinfo.Version=$VERSION -X github.com/carlotran4/kanbi/internal/buildinfo.Commit=$COMMIT -X github.com/carlotran4/kanbi/internal/buildinfo.BuildDate=$BUILD_DATE" \
  -o "$PACKAGE_DIR/package/kanbi" ./cmd/kanbi
cp README.md LICENSE "$PACKAGE_DIR/package/"
cp internal/tui/textarea/LICENSE "$PACKAGE_DIR/package/LICENSE-bubbles-textarea"

# provenance: machine-readable build record packaged with the binary for support/release audit.
SCHEMA_VERSION="$("$PACKAGE_DIR/package/kanbi" version --json | sed -n 's/.*"schema_version"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' | head -n1)"
if [[ -z "$SCHEMA_VERSION" ]]; then
  SCHEMA_VERSION="$("$PACKAGE_DIR/package/kanbi" version | sed -n 's/^database schema: //p' | head -n1)"
fi
cat >"$PACKAGE_DIR/package/BUILDINFO.json" <<EOF
{
  "format": "kanbi-buildinfo",
  "format_version": 1,
  "version": "$VERSION",
  "commit": "$COMMIT",
  "build_date": "$BUILD_DATE",
  "go_version": "$GO_VERSION",
  "os": "$OS",
  "arch": "$ARCH",
  "cgo_enabled": true,
  "schema_version": ${SCHEMA_VERSION:-0},
  "module": "github.com/carlotran4/kanbi"
}
EOF

ARCHIVE="$OUT_DIR/kanbi_${VERSION}_${OS}_${ARCH}.tar.gz"
tar -czf "$ARCHIVE" -C "$PACKAGE_DIR/package" kanbi README.md LICENSE LICENSE-bubbles-textarea BUILDINFO.json
(
  cd "$OUT_DIR"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$(basename "$ARCHIVE")" > SHA256SUMS
  else
    shasum -a 256 "$(basename "$ARCHIVE")" > SHA256SUMS
  fi
)
"$PACKAGE_DIR/package/kanbi" version
printf 'created %s\n' "$ARCHIVE"
printf 'checksums %s/SHA256SUMS\n' "$OUT_DIR"
