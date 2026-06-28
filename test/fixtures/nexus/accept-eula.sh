#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/env.sh"
require_nexus_password

status_json="$(curl -fsS -u "${NEXUS_USER}:${NEXUS_PASSWORD}" "${NEXUS_URL}/service/rest/v1/system/eula")"
if [[ "${status_json}" == *'"accepted" : true'* || "${status_json}" == *'"accepted":true'* ]]; then
  echo "Nexus Community Edition EULA already accepted"
  exit 0
fi

accepted_json="$(printf "%s" "${status_json}" | sed -e 's/"accepted"[[:space:]]*:[[:space:]]*false/"accepted" : true/')"

curl -fsS -u "${NEXUS_USER}:${NEXUS_PASSWORD}" \
  -H "Content-Type: application/json" \
  -X POST "${NEXUS_URL}/service/rest/v1/system/eula" \
  --data "${accepted_json}"

echo "accepted Nexus Community Edition EULA"
