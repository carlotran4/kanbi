#!/usr/bin/env bash
# Parses the only release version formats accepted by CI/CD and prints
# source-safe shell assignments for the normalized version and channel.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 VERSION" >&2
  exit 2
fi
raw="$1"
pattern='^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(beta|rc)\.(0|[1-9][0-9]*))?$'
if [[ ! "$raw" =~ $pattern ]]; then
  echo "version must be MAJOR.MINOR.PATCH, MAJOR.MINOR.PATCH-beta.N, or MAJOR.MINOR.PATCH-rc.N without numeric leading zeros (optional leading v)" >&2
  exit 2
fi

version="${raw#v}"
major="${BASH_REMATCH[1]}"
prerelease=false
channel=stable
if [[ "$major" == "0" || -n "${BASH_REMATCH[4]}" ]]; then
  prerelease=true
  channel=beta
fi

printf 'RELEASE_VERSION_NORMALIZED=%q\n' "$version"
printf 'RELEASE_MAJOR=%q\n' "$major"
printf 'RELEASE_CHANNEL=%q\n' "$channel"
printf 'RELEASE_IS_PRERELEASE=%q\n' "$prerelease"
