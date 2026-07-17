#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
POLICY="$ROOT/scripts/release-version-policy.sh"

assert_valid() {
  local input="$1" want_version="$2" want_channel="$3" want_prerelease="$4"
  unset RELEASE_VERSION_NORMALIZED RELEASE_MAJOR RELEASE_CHANNEL RELEASE_IS_PRERELEASE
  # The parser accepts only a constrained character set and emits %q-escaped values.
  source <("$POLICY" "$input")
  [[ "$RELEASE_VERSION_NORMALIZED" == "$want_version" ]]
  [[ "$RELEASE_CHANNEL" == "$want_channel" ]]
  [[ "$RELEASE_IS_PRERELEASE" == "$want_prerelease" ]]
}

assert_invalid() {
  if "$POLICY" "$1" >/dev/null 2>&1; then
    echo "unexpectedly accepted release version: $1" >&2
    exit 1
  fi
}

assert_valid v0.3.0-beta.1 0.3.0-beta.1 beta true
assert_valid 0.3.0-rc.2 0.3.0-rc.2 beta true
assert_valid v0.3.0 0.3.0 beta true
assert_valid v1.0.0 1.0.0 stable false
assert_valid v2.4.1-rc.3 2.4.1-rc.3 beta true

for invalid in \
  v01.0.0 v1.01.0 v1.0.01 v08.0.0 v1.0.0-beta.01 \
  v1.0 v1.0.0-alpha.1 v1.0.0-beta v1.0.0+build latest ''; do
  assert_invalid "$invalid"
done

echo "release version policy: PASS"
