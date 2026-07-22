#!/usr/bin/env sh
set -eu

APP_NAME="obs-agent-connector"
VERSION="${VERSION:-latest}"
INSTALL_DIR="${INSTALL_DIR:-}"
CONFIG_DIR="${CONFIG_DIR:-$HOME/.obs-agent-connector}"
DOWNLOAD_BASE_URL="${DOWNLOAD_BASE_URL:-${OBS_AGENT_CONNECTOR_OSS_ENDPOINT:-}}"
PLUGIN_SOURCE="${PLUGIN_SOURCE:-}"
PLUGIN_BASE_URL="${PLUGIN_BASE_URL:-}"
ENDPOINT="${ENDPOINT:-}"
X_TOKEN="${X_TOKEN:-}"
BINARY_ONLY=0
SCRIPT_PATH=""
PATH_RC_FILE=""
PATH_EXPORT_LINE=""
PATH_RELOAD_CMD=""

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
  install.sh [--version <tag|latest>] [--install-dir <path>] [--config-dir <path>] [--download-base-url <url>] [--endpoint <url>] [--x-token <token>] [--plugin-source <oss|github>] [--plugin-base-url <url>] [--binary-only]
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

shell_reload_command() {
  shell_name="$(basename "${SHELL:-}")"
  case "$shell_name" in
    zsh) printf '%s\n' "source ~/.zshrc" ;;
    bash) printf '%s\n' "source ~/.bashrc" ;;
    *) printf '%s\n' ". ~/.profile" ;;
  esac
}

json_get() {
  key="$1"
  path="$2"
  if [ ! -f "$path" ]; then
    return 0
  fi
  sed -n "s/.*\"$key\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p" "$path" | head -n 1
}

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

download_base_from_endpoint() {
  endpoint_value="$1"
  endpoint_host="$(printf '%s\n' "$endpoint_value" | sed -n 's#^[A-Za-z][A-Za-z0-9+.-]*://\([^/@]*@\)\?\([^/:]*\).*#\2#p')"
  endpoint_host="${endpoint_host%.}"
  root_domain="$(printf '%s\n' "$endpoint_host" | awk -F. 'NF >= 2 { print $(NF-1) "." $NF }')"
  if [ -n "$root_domain" ]; then
    printf 'https://static.%s/obs-agent-connector\n' "$root_domain"
  fi
}

plugin_base_from_download_base() {
  value="$1"
  value="${value%/}"
  case "$value" in
    */*)
      printf '%s\n' "${value%/*}"
      ;;
    *)
      printf '%s\n' "$value"
      ;;
  esac
}

calculate_sha256() {
  checksum_target="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$checksum_target" | awk '{print tolower($1)}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$checksum_target" | awk '{print tolower($1)}'
    return
  fi
  if command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$checksum_target" | awk '{print tolower($NF)}'
    return
  fi
  echo "A SHA-256 tool is required: sha256sum, shasum, or openssl" >&2
  return 1
}

cache_busted_url() {
  cache_url="$1"
  cache_key="$2"
  case "$cache_url" in
    http://*|https://*) printf '%s?v=%s\n' "$cache_url" "$cache_key" ;;
    *) printf '%s\n' "$cache_url" ;;
  esac
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) shift; VERSION="$1" ;;
    --version=*) VERSION="${1#*=}" ;;
    --install-dir) shift; INSTALL_DIR="$1" ;;
    --install-dir=*) INSTALL_DIR="${1#*=}" ;;
    --config-dir) shift; CONFIG_DIR="$1" ;;
    --config-dir=*) CONFIG_DIR="${1#*=}" ;;
    --download-base-url) shift; DOWNLOAD_BASE_URL="$1" ;;
    --download-base-url=*) DOWNLOAD_BASE_URL="${1#*=}" ;;
    --plugin-source) shift; PLUGIN_SOURCE="$1" ;;
    --plugin-source=*) PLUGIN_SOURCE="${1#*=}" ;;
    --plugin-base-url) shift; PLUGIN_BASE_URL="$1" ;;
    --plugin-base-url=*) PLUGIN_BASE_URL="${1#*=}" ;;
    --endpoint) shift; ENDPOINT="$1" ;;
    --endpoint=*) ENDPOINT="${1#*=}" ;;
    --x-token) shift; X_TOKEN="$1" ;;
    --x-token=*) X_TOKEN="${1#*=}" ;;
    --binary-only) BINARY_ONLY=1 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

endpoint_was_provided="$ENDPOINT"

if [ -z "${INSTALL_DIR}" ]; then
  if [ -w /usr/local/bin ]; then
    INSTALL_DIR="/usr/local/bin"
  else
    INSTALL_DIR="$HOME/.local/bin"
  fi
fi

config_path="${CONFIG_DIR}/config.json"
existing_download_base_url="$(json_get download_base_url "$config_path")"
existing_plugin_source="$(json_get plugin_source "$config_path")"
existing_plugin_base_url="$(json_get plugin_base_url "$config_path")"
existing_endpoint="$(json_get endpoint "$config_path")"
existing_x_token="$(json_get x_token "$config_path")"

if [ -z "${ENDPOINT}" ] && [ -n "${existing_endpoint}" ]; then
  ENDPOINT="${existing_endpoint}"
fi
if [ -z "${X_TOKEN}" ] && [ -n "${existing_x_token}" ]; then
  X_TOKEN="${existing_x_token}"
fi
if [ -z "${DOWNLOAD_BASE_URL}" ]; then
  if [ -n "${endpoint_was_provided}" ]; then
    DOWNLOAD_BASE_URL="$(download_base_from_endpoint "${ENDPOINT}")"
  elif [ -n "${existing_download_base_url}" ]; then
    DOWNLOAD_BASE_URL="${existing_download_base_url}"
  else
    DOWNLOAD_BASE_URL="$(download_base_from_endpoint "${ENDPOINT}")"
  fi
fi
if [ -z "${PLUGIN_SOURCE}" ] && [ -n "${existing_plugin_source}" ]; then
  PLUGIN_SOURCE="${existing_plugin_source}"
fi
if [ -z "${PLUGIN_SOURCE}" ]; then
  PLUGIN_SOURCE="oss"
fi
if [ -z "${PLUGIN_BASE_URL}" ]; then
  if [ -n "${existing_plugin_base_url}" ]; then
    PLUGIN_BASE_URL="${existing_plugin_base_url}"
  elif [ "${PLUGIN_SOURCE}" = "oss" ]; then
    PLUGIN_BASE_URL="$(plugin_base_from_download_base "${DOWNLOAD_BASE_URL}")"
  fi
fi

if [ -z "${DOWNLOAD_BASE_URL}" ]; then
  echo "download_base_url is required; pass --download-base-url <url> or set DOWNLOAD_BASE_URL / OBS_AGENT_CONNECTOR_OSS_ENDPOINT" >&2
  exit 2
fi
if [ "${BINARY_ONLY}" -eq 0 ] && [ -z "${ENDPOINT}" ]; then
  echo "endpoint is required; pass --endpoint <url> on first install or keep it in config.json" >&2
  exit 2
fi
if [ "${BINARY_ONLY}" -eq 0 ] && [ -z "${X_TOKEN}" ]; then
  echo "x-token is required; pass --x-token <token> on first install or keep it in config.json" >&2
  exit 2
fi
if [ "${PLUGIN_SOURCE}" = "github" ] && [ -z "${PLUGIN_BASE_URL}" ]; then
  echo "plugin_base_url is required when plugin_source=github; pass --plugin-base-url <url> or update config.json" >&2
  exit 2
fi

DOWNLOAD_BASE_URL="${DOWNLOAD_BASE_URL%/}"
PLUGIN_BASE_URL="${PLUGIN_BASE_URL%/}"

latest_version() {
  latest_cache_key="$(date +%s)"
  curl -fsSL "$(cache_busted_url "${DOWNLOAD_BASE_URL}/latest.txt" "$latest_cache_key")" | tr -d '\r' | head -n 1
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
download_url="$(cache_busted_url "${DOWNLOAD_BASE_URL}/${asset_name}" "$current_version")"
checksums_url="$(cache_busted_url "${DOWNLOAD_BASE_URL}/SHA256SUMS" "$current_version")"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT INT TERM

mkdir -p "$INSTALL_DIR" "$CONFIG_DIR"

curl -fsSL -o "$tmp_dir/$asset_name" "$download_url"
curl -fsSL -o "$tmp_dir/SHA256SUMS" "$checksums_url"

expected_checksum="$(awk -v name="$asset_name" '{ file=$2; sub(/^\*/, "", file); if (file == name) { print tolower($1); exit } }' "$tmp_dir/SHA256SUMS")"
if [ -z "$expected_checksum" ]; then
  echo "Checksum entry not found for ${asset_name}" >&2
  exit 1
fi
actual_checksum="$(calculate_sha256 "$tmp_dir/$asset_name")"
if [ "$actual_checksum" != "$expected_checksum" ]; then
  echo "Checksum verification failed for ${asset_name}" >&2
  exit 1
fi
printf 'Verified SHA-256 for %s\n' "$asset_name"

tar -xzf "$tmp_dir/$asset_name" -C "$tmp_dir"
install -m 0755 "$tmp_dir/$binary_name" "$INSTALL_DIR/$APP_NAME"

if [ "${BINARY_ONLY}" -eq 0 ]; then
  cat > "$config_path" <<EOF
{
  "download_base_url": "$(json_escape "${DOWNLOAD_BASE_URL}")",
  "plugin_source": "$(json_escape "${PLUGIN_SOURCE}")",
  "plugin_base_url": "$(json_escape "${PLUGIN_BASE_URL}")",
  "endpoint": "$(json_escape "${ENDPOINT}")",
  "x_token": "$(json_escape "${X_TOKEN}")"
}
EOF
fi

printf 'Installed %s %s to %s\n' "$APP_NAME" "$current_version" "$INSTALL_DIR/$APP_NAME"
if [ "${BINARY_ONLY}" -eq 0 ]; then
  printf 'Wrote config to %s\n' "$config_path"
fi

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    PATH_RC_FILE="$(shell_rc_file)"
    PATH_EXPORT_LINE="export PATH=\"$INSTALL_DIR:\$PATH\""
    PATH_RELOAD_CMD="$(shell_reload_command)"
    if [ -n "$PATH_RC_FILE" ]; then
      if [ ! -f "$PATH_RC_FILE" ] || ! grep -Fqs "$PATH_EXPORT_LINE" "$PATH_RC_FILE"; then
        printf '\n%s\n' "$PATH_EXPORT_LINE" >> "$PATH_RC_FILE"
        printf 'Added %s to PATH in %s\n' "$INSTALL_DIR" "$PATH_RC_FILE"
      else
        printf 'PATH already configured in %s\n' "$PATH_RC_FILE"
      fi
      printf 'Run %s to use %s in the current shell\n' "$PATH_RELOAD_CMD" "$APP_NAME"
    else
      printf 'Add %s to PATH if needed.\n' "$INSTALL_DIR"
    fi
    ;;
esac

if [ -n "$SCRIPT_PATH" ] && [ -f "$SCRIPT_PATH" ]; then
  rm -f "$SCRIPT_PATH"
fi
