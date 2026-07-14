#!/usr/bin/env sh
set -eu

APP_NAME="obs-agent-connector"
VERSION="${VERSION:-latest}"
INSTALL_DIR="${INSTALL_DIR:-}"
CONFIG_DIR="${CONFIG_DIR:-$HOME/.obs-agent-connector}"
DOWNLOAD_BASE_URL="${DOWNLOAD_BASE_URL:-${OBS_AGENT_CONNECTOR_OSS_ENDPOINT:-}}"
SCRIPT_PATH=""
PATH_RC_FILE=""
PATH_EXPORT_LINE=""

case "${0:-}" in
  ""|"-"|sh|*/sh) ;;
  /*)
    if [ -f "$0" ]; then
      SCRIPT_PATH="$0"
    fi
    ;;
  *)
    if [ -f "./$0" ]; then
      SCRIPT_PATH="./$0"
    elif [ -f "$0" ]; then
      SCRIPT_PATH="$0"
    fi
    ;;
esac

usage() {
  cat <<EOF
Usage:
  install.sh [--version <tag|latest>] [--install-dir <path>] [--config-dir <path>] [--download-base-url <url>]
EOF
}

shell_rc_file() {
  shell_name="$(basename "${SHELL:-}")"
  case "$shell_name" in
    zsh) printf '%s\n' "$HOME/.zshrc" ;;
    bash) printf '%s\n' "$HOME/.bashrc" ;;
    *) printf '%s\n' "$HOME/.profile" ;;
  esac
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) shift; VERSION="$1" ;;
    --install-dir) shift; INSTALL_DIR="$1" ;;
    --config-dir) shift; CONFIG_DIR="$1" ;;
    --download-base-url) shift; DOWNLOAD_BASE_URL="$1" ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

if [ -z "${INSTALL_DIR}" ]; then
  if [ -w /usr/local/bin ]; then
    INSTALL_DIR="/usr/local/bin"
  else
    INSTALL_DIR="$HOME/.local/bin"
  fi
fi

if [ -z "${DOWNLOAD_BASE_URL}" ]; then
  echo "download_base_url is required; pass --download-base-url <url> or set DOWNLOAD_BASE_URL / OBS_AGENT_CONNECTOR_OSS_ENDPOINT" >&2
  exit 2
fi

DOWNLOAD_BASE_URL="${DOWNLOAD_BASE_URL%/}"

latest_version() {
  curl -fsSL "${DOWNLOAD_BASE_URL}/latest.txt" | tr -d '\r' | head -n 1
}

current_version="$VERSION"
if [ "$current_version" = "latest" ]; then
  current_version="$(latest_version)"
  if [ -z "$current_version" ]; then
    echo "Failed to resolve latest version from ${DOWNLOAD_BASE_URL}/latest.txt" >&2
    exit 1
  fi
fi

os_name="$(uname -s)"
arch_name="$(uname -m)"

case "$os_name" in
  Darwin) goos="darwin" ;;
  Linux) goos="linux" ;;
  *) echo "Unsupported OS: $os_name" >&2; exit 1 ;;
esac

case "$arch_name" in
  arm64|aarch64) goarch="arm64" ;;
  x86_64|amd64) goarch="amd64" ;;
  *) echo "Unsupported architecture: $arch_name" >&2; exit 1 ;;
esac

asset_name="${APP_NAME}-${goos}-${goarch}.tar.gz"
binary_name="${APP_NAME}-${goos}-${goarch}"
download_url="${DOWNLOAD_BASE_URL}/${asset_name}"
config_path="${CONFIG_DIR}/config.json"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT INT TERM

mkdir -p "$INSTALL_DIR" "$CONFIG_DIR"

curl -fsSL -o "$tmp_dir/$asset_name" "$download_url"
tar -xzf "$tmp_dir/$asset_name" -C "$tmp_dir"
install -m 0755 "$tmp_dir/$binary_name" "$INSTALL_DIR/$APP_NAME"

cat > "$config_path" <<EOF
{
  "download_base_url": "${DOWNLOAD_BASE_URL}"
}
EOF

printf 'Installed %s %s to %s\n' "$APP_NAME" "$current_version" "$INSTALL_DIR/$APP_NAME"
printf 'Wrote config to %s\n' "$config_path"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    PATH_RC_FILE="$(shell_rc_file)"
    PATH_EXPORT_LINE="export PATH=\"$INSTALL_DIR:\$PATH\""
    if [ -n "$PATH_RC_FILE" ]; then
      if [ ! -f "$PATH_RC_FILE" ] || ! grep -Fqs "$PATH_EXPORT_LINE" "$PATH_RC_FILE"; then
        printf '\n%s\n' "$PATH_EXPORT_LINE" >> "$PATH_RC_FILE"
        printf 'Added %s to PATH in %s\n' "$INSTALL_DIR" "$PATH_RC_FILE"
      else
        printf 'PATH already configured in %s\n' "$PATH_RC_FILE"
      fi
      printf 'Open a new shell or run: export PATH="%s:$PATH"\n' "$INSTALL_DIR"
    else
      printf 'Add %s to PATH if needed.\n' "$INSTALL_DIR"
    fi
    ;;
esac

if [ -n "$SCRIPT_PATH" ] && [ -f "$SCRIPT_PATH" ]; then
  rm -f "$SCRIPT_PATH"
fi
