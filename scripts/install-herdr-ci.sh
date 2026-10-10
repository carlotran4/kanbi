#!/usr/bin/env bash
# Pinned official release binaries and their published GitHub SHA-256 digests.
set -euo pipefail
case "$(uname -s)-$(uname -m)" in
  Linux-x86_64) asset=herdr-linux-x86_64; digest=18a8dc65f1c2fa485884344356dea1cfd911c6f06cf46fa78e193f4087f4dba7 ;;
  Linux-aarch64|Linux-arm64) asset=herdr-linux-aarch64; digest=4de7aa3e25678812e92960de64f7c2aaa1bca1f0f80a3c5e559837e231e1f5c0 ;;
  Darwin-x86_64) asset=herdr-macos-x86_64; digest=db62d548ff3e832b087a96b1894a08d26be3905f1830309cd556783f215d4054 ;;
  Darwin-arm64) asset=herdr-macos-aarch64; digest=5173a3e0ae42d5d1ab7ebfa5d5e6329f7c3d23f8e1a3677c7ce3231da2884157 ;;
  *) echo 'Unsupported Herdr CI platform' >&2; exit 1 ;;
esac
install_dir="${1:?usage: install-herdr-ci.sh DIRECTORY}"
mkdir -p "$install_dir"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
curl --fail --location --silent --show-error "https://github.com/herdrdev/herdr/releases/download/v0.9.3/$asset" -o "$work/herdr"
python3 - "$work/herdr" "$digest" <<'PY'
import hashlib,sys
if hashlib.sha256(open(sys.argv[1],'rb').read()).hexdigest() != sys.argv[2]:
    sys.exit('Herdr release checksum mismatch')
PY
install -m 755 "$work/herdr" "$install_dir/herdr"
"$install_dir/herdr" --version
