#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
TEMPLATE_FILE="${ROOT_DIR}/docs/release-template.md"
OUTPUT_FILE="${1:-${ROOT_DIR}/dist/RELEASE_NOTES.md}"
APP_NAME="${APP_NAME:-obs-agent-connector}"
VERSION="${VERSION:-dev}"

mkdir -p "$(dirname "${OUTPUT_FILE}")"

sed \
  -e "s/{{APP_NAME}}/${APP_NAME}/g" \
  -e "s/{{VERSION}}/${VERSION}/g" \
  "${TEMPLATE_FILE}" \
  > "${OUTPUT_FILE}"
