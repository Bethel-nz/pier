#!/usr/bin/env bash
set -euo pipefail

repo="Bethel-nz/pier"
version="${PIER_VERSION:-latest}"
install_dir="${PIER_INSTALL_DIR:-${HOME}/.local/bin}"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "Pier supports macOS and Linux with this installer." >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "Pier does not have a release for architecture $(uname -m)." >&2; exit 1 ;;
esac

asset="pier_${os}_${arch}.tar.gz"
if [[ -n "${PIER_DOWNLOAD_BASE_URL:-}" ]]; then
  base_url="${PIER_DOWNLOAD_BASE_URL%/}"
elif [[ "$version" == "latest" ]]; then
  base_url="https://github.com/${repo}/releases/latest/download"
else
  [[ "$version" == v* ]] || version="v${version}"
  base_url="https://github.com/${repo}/releases/download/${version}"
fi

temporary="$(mktemp -d "${TMPDIR:-/tmp}/pier-install.XXXXXX")"
cleanup() {
  rm -rf "$temporary"
}
trap cleanup EXIT

download() {
  local url="$1"
  local output="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$output"
  elif command -v wget >/dev/null 2>&1; then
    wget -q "$url" -O "$output"
  else
    echo "Pier installation requires curl or wget." >&2
    exit 1
  fi
}

download "$base_url/$asset" "$temporary/$asset"
download "$base_url/checksums.txt" "$temporary/checksums.txt"

expected="$(awk -v file="$asset" '$2 == file || $2 == "*" file { print $1; exit }' "$temporary/checksums.txt")"
if [[ -z "$expected" ]]; then
  echo "Pier could not find $asset in checksums.txt." >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$temporary/$asset" | awk '{ print $1 }')"
else
  actual="$(shasum -a 256 "$temporary/$asset" | awk '{ print $1 }')"
fi
if [[ "$actual" != "$expected" ]]; then
  echo "Pier checksum verification failed for $asset." >&2
  exit 1
fi

tar -xzf "$temporary/$asset" -C "$temporary"
mkdir -p "$install_dir"
install -m 0755 "$temporary/pier" "$install_dir/pier"

echo "Pier installed to $install_dir/pier"
case ":${PATH}:" in
  *":${install_dir}:"*) ;;
  *) echo "Add $install_dir to your PATH to run pier." ;;
esac
