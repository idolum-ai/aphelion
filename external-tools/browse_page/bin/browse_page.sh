#!/usr/bin/env bash
set -euo pipefail

payload="$(cat)"
url="$(printf '%s' "${payload}" | sed -n 's/.*"url"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
if [[ -z "${url}" ]]; then
  echo "missing required url" >&2
  exit 1
fi

json_escape() {
  local value="${1//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//$'\n'/\\n}"
  printf '%s' "${value}"
}

summary="Deterministic browse_page pilot fixture. Replace this script with an agent-owned browser implementation outside Aphelion core."
printf '{"url":"%s","title":"Fixture Page","summary":"%s"}\n' "$(json_escape "${url}")" "$(json_escape "${summary}")"
