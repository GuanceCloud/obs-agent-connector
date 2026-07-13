#!/usr/bin/env sh
set -eu

APP_NAME="obs-agent-connector"
VERSION="${VERSION:-latest}"
BRAND="${BRAND:-guance}"
INSTALL_DIR="${INSTALL_DIR:-}"
CONFIG_DIR="${CONFIG_DIR:-}"
RELEASE_REPO="${RELEASE_REPO:-GuanceCloud/obs-agent-connector}"
RELEASE_API_URL="${RELEASE_API_URL:-https://api.github.com/repos/${RELEASE_REPO}/releases/latest}"
RELEASE_LATEST_URL="${RELEASE_LATEST_URL:-https://github.com/${RELEASE_REPO}/releases/latest}"
RELEASE_PAGE_BASE_URL="${RELEASE_PAGE_BASE_URL:-https://github.com/${RELEASE_REPO}/releases/tag}"
RELEASE_DOWNLOAD_BASE_URL="${RELEASE_DOWNLOAD_BASE_URL:-https://github.com/${RELEASE_REPO}/releases/download}"

usage() {
  cat <<EOF
Usage:
  install-obs-agent-connector.sh [--version <tag|latest>] [--brand <name>] [--install-dir <path>] [--config-dir <path>] [--release-repo <owner/repo>] [--release-api-url <url>] [--release-latest-url <url>] [--release-page-base-url <url>] [--release-download-base-url <url>]
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version) shift; VERSION="$1" ;;
    --brand) shift; BRAND="$1" ;;
    --install-dir) shift; INSTALL_DIR="$1" ;;
    --config-dir) shift; CONFIG_DIR="$1" ;;
    --release-repo) shift; RELEASE_REPO="$1" ;;
    --release-api-url) shift; RELEASE_API_URL="$1" ;;
    --release-latest-url) shift; RELEASE_LATEST_URL="$1" ;;
    --release-page-base-url) shift; RELEASE_PAGE_BASE_URL="$1" ;;
    --release-download-base-url) shift; RELEASE_DOWNLOAD_BASE_URL="$1" ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

if [ -z "${CONFIG_DIR}" ]; then
  CONFIG_DIR="$HOME/.obs-agent-connector/$BRAND"
fi

if [ -z "${INSTALL_DIR}" ]; then
  if [ -w /usr/local/bin ]; then
    INSTALL_DIR="/usr/local/bin"
  else
    INSTALL_DIR="$HOME/.local/bin"
  fi
fi

latest_version() {
  if command -v curl >/dev/null 2>&1; then
    value="$(curl -fsSL "$RELEASE_API_URL" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1 || true)"
    if [ -n "$value" ]; then
      printf '%s\n' "$value"
      return 0
    fi
  fi

  location="$(curl -fsSI "$RELEASE_LATEST_URL" | awk '/^location:/I {print $2}' | tr -d '\r' | tail -n 1 || true)"
  case "$location" in
    */tag/*) printf '%s\n' "${location##*/tag/}" ;;
    *) echo "Failed to resolve latest version" >&2; exit 1 ;;
  esac
}

current_version="$VERSION"
if [ "$current_version" = "latest" ]; then
  current_version="$(latest_version)"
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
download_url="${RELEASE_DOWNLOAD_BASE_URL%/}/${current_version}/${asset_name}"
real_bin_dir="${CONFIG_DIR}/bin"
real_bin_path="${real_bin_dir}/${APP_NAME}"
wrapper_path="${INSTALL_DIR}/${APP_NAME}"
config_path="${CONFIG_DIR}/config.json"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT INT TERM

mkdir -p "$INSTALL_DIR" "$CONFIG_DIR" "$real_bin_dir"

curl -fsSL -o "$tmp_dir/$asset_name" "$download_url"
tar -xzf "$tmp_dir/$asset_name" -C "$tmp_dir"
install -m 0755 "$tmp_dir/$binary_name" "$real_bin_path"

cat > "$config_path" <<EOF
{
  "brand": "${BRAND}",
  "release_repo": "${RELEASE_REPO}",
  "release_api_url": "${RELEASE_API_URL}",
  "release_latest_url": "${RELEASE_LATEST_URL}",
  "release_page_base_url": "${RELEASE_PAGE_BASE_URL}",
  "release_download_base_url": "${RELEASE_DOWNLOAD_BASE_URL}"
}
EOF

cat > "$wrapper_path" <<EOF
#!/usr/bin/env sh
export OBS_AGENT_CONNECTOR_CONFIG='${config_path}'
exec '${real_bin_path}' "\$@"
EOF
chmod 0755 "$wrapper_path"

printf 'Installed %s %s to %s\n' "$APP_NAME" "$current_version" "$wrapper_path"
printf 'Installed runtime binary to %s\n' "$real_bin_path"
printf 'Wrote config to %s\n' "$config_path"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) printf 'Add %s to PATH if needed.\n' "$INSTALL_DIR" ;;
esac
