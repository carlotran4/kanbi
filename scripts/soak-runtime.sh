#!/usr/bin/env bash
# soak-runtime.sh runs a deterministic local stress test of sync ownership,
# scheduling storms, and lease/cancellation handling without external providers.
#
# Usage:
#   ./scripts/soak-runtime.sh
#   KANBI_SOAK_SECONDS=60 ./scripts/soak-runtime.sh
#
# Defaults:
#   duration 60s (override with KANBI_SOAK_SECONDS)
#   race detector enabled
#   no real providers / no network
# Cleanup: temp data is confined to Go's testing temp dirs; no SQLite files under $HOME.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
export KANBI_SOAK=1
export KANBI_SOAK_SECONDS="${KANBI_SOAK_SECONDS:-60}"
echo "kanbi soak-runtime: ${KANBI_SOAK_SECONDS}s concurrent fake provider schedule/stop cycles"
go test -count=1 -race -timeout "$((KANBI_SOAK_SECONDS + 120))s" ./internal/ticketbackend -run 'TestRuntimeSoakDeterministic|TestManagerStopDrains|TestManagerLease'
echo "kanbi soak-runtime: pass"
