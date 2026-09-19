#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/pier-install-test.XXXXXX")"
cleanup() {
  rm -rf "$test_root"
}
trap cleanup EXIT

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "unsupported test OS" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "unsupported test architecture" >&2; exit 1 ;;
esac

asset="pier_${os}_${arch}.tar.gz"
mkdir -p "$test_root/payload" "$test_root/release" "$test_root/bin"
printf '#!/bin/sh\necho pier-install-test\n' > "$test_root/payload/pier"
chmod +x "$test_root/payload/pier"
tar -czf "$test_root/release/$asset" -C "$test_root/payload" pier

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$test_root/release" && sha256sum "$asset" > checksums.txt)
else
  (cd "$test_root/release" && shasum -a 256 "$asset" > checksums.txt)
fi

PIER_DOWNLOAD_BASE_URL="file://$test_root/release" \
PIER_INSTALL_DIR="$test_root/bin" \
  bash "$root/scripts/install.sh"

result="$($test_root/bin/pier)"
if [[ "$result" != "pier-install-test" ]]; then
  echo "installed binary returned: $result" >&2
  exit 1
fi
