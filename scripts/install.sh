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
releases="https://github.com/${repo}/releases"

# latest_tag prints the newest release's tag, such as v0.4.1: the last part
# of where /releases/latest redirects.
latest_tag() {
  local url=""
  if command -v curl >/dev/null 2>&1; then
    url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "${releases}/latest" 2>/dev/null)" || true
  elif command -v wget >/dev/null 2>&1; then
    url="$(wget --max-redirect=0 --server-response -O /dev/null "${releases}/latest" 2>&1 | awk 'tolower($1) == "location:" { print $2 }' | tail -n 1 | tr -d '\r')" || true
  fi
  local tag="${url##*/}"
  [[ "$tag" == v* ]] && echo "$tag"
}

if [[ "$version" != "latest" && "$version" != v* ]]; then
  version="v${version}"
fi
if [[ -n "${PIER_DOWNLOAD_BASE_URL:-}" ]]; then
  base_url="${PIER_DOWNLOAD_BASE_URL%/}"
else
  if [[ "$version" == "latest" ]]; then
    if ! version="$(latest_tag)"; then
      echo "Pier could not find its latest release at ${releases}. Check your connection, or set PIER_VERSION." >&2
      exit 1
    fi
  fi
  base_url="${releases}/download/${version}"
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

if ! download "$base_url/$asset" "$temporary/$asset" || ! download "$base_url/checksums.txt" "$temporary/checksums.txt"; then
  echo "Pier could not download $asset for ${version}. See the versions at ${releases}." >&2
  exit 1
fi

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

# The version already here, when it is new enough to say.
previous=""
if [[ -x "$install_dir/pier" ]]; then
  previous="$("$install_dir/pier" --version 2>/dev/null | awk '{ print $2 }')" || previous=""
fi

tar -xzf "$temporary/$asset" -C "$temporary"
mkdir -p "$install_dir"
install -m 0755 "$temporary/pier" "$install_dir/pier"

if [[ "$version" == "latest" ]]; then
  echo "Pier installed to $install_dir/pier"
elif [[ -n "$previous" && "$previous" != "$version" ]]; then
  echo "Pier updated from $previous to $version in $install_dir/pier"
else
  echo "Pier $version installed to $install_dir/pier"
fi
case ":${PATH}:" in
  *":${install_dir}:"*)
    found="$(command -v pier 2>/dev/null || true)"
    if [[ -n "$found" && "$found" != "$install_dir/pier" ]]; then
      echo
      echo "$found comes earlier on your PATH, so pier still runs that one."
      echo "Remove it, or move $install_dir ahead of $(dirname "$found") in PATH."
    else
      echo "Run: pier --help"
    fi
    exit 0
    ;;
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
