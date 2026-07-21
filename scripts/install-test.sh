#!/usr/bin/env sh
set -eu

BASE_URL="https://static.guance.com/obs-agent-connector-test"
TMP_SCRIPT="$(mktemp "${TMPDIR:-/tmp}/obs-agent-connector-install.XXXXXX.sh")"

cleanup() {
  rm -f "${TMP_SCRIPT}"
}
trap cleanup EXIT INT TERM

usage() {
  cat <<EOF
Usage:
  install-test.sh --endpoint <url> --x-token <token> [additional install.sh args]

Example:
  ./scripts/install-test.sh \\
    --endpoint http://testing-openway.dataflux.cn \\
    --x-token agent_xxx
EOF
}

need_value() {
  option_name="$1"
  option_value="$2"
  if [ -z "${option_value}" ]; then
    echo "${option_name} is required" >&2
    exit 2
  fi
}

ENDPOINT_VALUE=""
X_TOKEN_VALUE=""

next_value=""
for arg in "$@"; do
  if [ -n "${next_value}" ]; then
    case "${next_value}" in
      endpoint) ENDPOINT_VALUE="${arg}" ;;
      x-token) X_TOKEN_VALUE="${arg}" ;;
    esac
    next_value=""
    continue
  fi

  case "${arg}" in
    --endpoint)
      next_value="endpoint"
      ;;
    --endpoint=*)
      ENDPOINT_VALUE="${arg#*=}"
      ;;
    --x-token)
      next_value="x-token"
      ;;
    --x-token=*)
      X_TOKEN_VALUE="${arg#*=}"
      ;;
    -h|--help)
      usage
      exit 0
      ;;
  esac
done

need_value "--endpoint" "${ENDPOINT_VALUE}"
need_value "--x-token" "${X_TOKEN_VALUE}"

curl -fsSL -o "${TMP_SCRIPT}" "${BASE_URL}/install.sh"
sh "${TMP_SCRIPT}" --download-base-url "${BASE_URL}" "$@"
