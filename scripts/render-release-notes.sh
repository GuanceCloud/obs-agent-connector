#!/usr/bin/env sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
TEMPLATE_FILE="${ROOT_DIR}/docs/release-template.md"
OUTPUT_FILE="${1:-${ROOT_DIR}/dist/RELEASE_NOTES.md}"
APP_NAME="${APP_NAME:-obs-agent-connector}"
VERSION="${VERSION:-dev}"

mkdir -p "$(dirname "${OUTPUT_FILE}")"

previous_tag() {
  git -C "${ROOT_DIR}" tag --sort=version:refname \
    | grep -Fxv "${VERSION}" \
    | tail -n 1
}

render_changes() {
  local previous
  previous="$(previous_tag || true)"

  if [ -n "${previous}" ]; then
    git -C "${ROOT_DIR}" log --format='- %s' "${previous}..${VERSION}"
    return
  fi

  git -C "${ROOT_DIR}" log --format='- %s' "${VERSION}" -n 20
}

CHANGES="$(render_changes)"
if [ -z "${CHANGES}" ]; then
  CHANGES="- Packaging update"
fi

escaped_changes="$(printf '%s\n' "${CHANGES}" | sed 's/[&/\]/\\&/g')"

sed \
  -e "s/{{APP_NAME}}/${APP_NAME}/g" \
  -e "s/{{VERSION}}/${VERSION}/g" \
  -e "/{{CHANGES}}/{
r /dev/stdin
d
}" \
  "${TEMPLATE_FILE}" \
  > "${OUTPUT_FILE}" <<EOF
${escaped_changes}
EOF
