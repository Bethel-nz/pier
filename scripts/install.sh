#!/usr/bin/env bash
set -euo pipefail

repo="Bethel-nz/pier"
version="${PIER_VERSION:-latest}"
install_dir="${PIER_INSTALL_DIR:-${HOME}/.local/bin}"

# ask, yes, or no: whether to add install_dir to the shell's startup file.
modify_path="ask"
[[ "${PIER_NO_MODIFY_PATH:-}" == "1" ]] && modify_path="no"
for arg in "$@"; do
  case "$arg" in
    --yes|-y) modify_path="yes" ;;
    --no-path) modify_path="no" ;;
    *) echo "Unknown option: $arg" >&2; exit 2 ;;
  esac
done

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

# A Rosetta shell on Apple silicon reports x86_64; install the native build.
if [[ "$os" == darwin && "$arch" == amd64 && "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" == 1 ]]; then
  arch=arm64
fi

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
  *":${install_dir}:"*) echo "Run: pier --help"; exit 0 ;;
esac

# install_dir isn't on PATH: find the shell's startup file and the line it needs.
line="export PATH=\"${install_dir}:\$PATH\""
case "$(basename "${SHELL:-sh}")" in
  zsh) rc="${ZDOTDIR:-$HOME}/.zshrc" ;;
  bash) if [[ "$os" == darwin ]]; then rc="$HOME/.bash_profile"; else rc="$HOME/.bashrc"; fi ;;
  fish) rc="$HOME/.config/fish/config.fish"; line="fish_add_path ${install_dir}" ;;
  *) rc="$HOME/.profile" ;;
esac

echo
echo "$install_dir is not on your PATH."

if [[ "$modify_path" == "ask" ]]; then
  # Piped into bash, stdin is this script, so ask on the terminal instead.
  if (: </dev/tty) 2>/dev/null; then
    printf 'Add it to %s? [y/N] ' "$rc" >/dev/tty
    read -r answer </dev/tty || answer=""
    case "$answer" in
      y|Y|yes|YES) modify_path="yes" ;;
      *) modify_path="no" ;;
    esac
  else
    modify_path="no"
  fi
fi

if [[ "$modify_path" == "yes" ]]; then
  mkdir -p "$(dirname "$rc")"
  if [[ -f "$rc" ]] && grep -Fqx "$line" "$rc"; then
    echo "$rc already adds it."
  else
    printf '\n# Added by the Pier installer\n%s\n' "$line" >> "$rc"
    echo "Added it to $rc."
  fi
  echo "Open a new terminal, or run: source $rc"
else
  echo "Add this line to $rc, then open a new terminal:"
  echo
  echo "  $line"
fi
