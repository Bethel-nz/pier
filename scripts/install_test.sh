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

PIER_NO_MODIFY_PATH=1 \
PIER_DOWNLOAD_BASE_URL="file://$test_root/release" \
PIER_INSTALL_DIR="$test_root/bin" \
  bash "$root/scripts/install.sh"

result="$($test_root/bin/pier)"
if [[ "$result" != "pier-install-test" ]]; then
  echo "installed binary returned: $result" >&2
  exit 1
fi

# A named version is reported, and an older pier already there is named too.
printf '#!/bin/sh\necho "pier v0.1.0"\n' > "$test_root/bin/pier"
chmod +x "$test_root/bin/pier"
printf '#!/bin/sh\n[ "$1" = --version ] && echo "pier v0.9.0" && exit 0\necho pier-install-test\n' > "$test_root/payload/pier"
tar -czf "$test_root/release/$asset" -C "$test_root/payload" pier
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$test_root/release" && sha256sum "$asset" > checksums.txt)
else
  (cd "$test_root/release" && shasum -a 256 "$asset" > checksums.txt)
fi
output="$(
  PIER_VERSION=0.9.0 \
  PIER_NO_MODIFY_PATH=1 \
  PIER_DOWNLOAD_BASE_URL="file://$test_root/release" \
  PIER_INSTALL_DIR="$test_root/bin" \
    bash "$root/scripts/install.sh"
)"
if [[ "$output" != *"Pier updated from v0.1.0 to v0.9.0"* ]]; then
  echo "update output: $output" >&2
  exit 1
fi

# Another pier earlier on PATH is pointed out.
mkdir -p "$test_root/shadow"
printf '#!/bin/sh\necho shadow\n' > "$test_root/shadow/pier"
chmod +x "$test_root/shadow/pier"
output="$(
  PATH="$test_root/shadow:$test_root/bin:$PATH" \
  PIER_VERSION=0.9.0 \
  PIER_DOWNLOAD_BASE_URL="file://$test_root/release" \
  PIER_INSTALL_DIR="$test_root/bin" \
    bash "$root/scripts/install.sh"
)"
if [[ "$output" != *"$test_root/shadow/pier comes earlier on your PATH"* ]]; then
  echo "shadow output: $output" >&2
  exit 1
fi

# A release that does not exist says so, and installs nothing.
if output="$(
  PIER_VERSION=0.0.1 \
  PIER_NO_MODIFY_PATH=1 \
  PIER_DOWNLOAD_BASE_URL="file://$test_root/missing" \
  PIER_INSTALL_DIR="$test_root/none" \
    bash "$root/scripts/install.sh" 2>&1
)"; then
  echo "a missing release installed: $output" >&2
  exit 1
fi
if [[ "$output" != *"Pier could not download $asset for v0.0.1"* || -e "$test_root/none" ]]; then
  echo "missing release output: $output" >&2
  exit 1
fi
echo "install tests passed"
