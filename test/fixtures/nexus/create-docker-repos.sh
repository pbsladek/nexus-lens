#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/env.sh"
require_nexus_password

create_repo() {
  local name="$1"
  local port="$2"

  if curl -fsS -u "${NEXUS_USER}:${NEXUS_PASSWORD}" "${NEXUS_URL}/service/rest/v1/repositories/${name}" >/dev/null 2>&1; then
    echo "repository ${name} already exists"
    return
  fi

  echo "creating Docker hosted repository ${name} on port ${port}"
  curl -fsS -u "${NEXUS_USER}:${NEXUS_PASSWORD}" \
    -H "Content-Type: application/json" \
    -X POST "${NEXUS_URL}/service/rest/v1/repositories/docker/hosted" \
    --data @- <<JSON
{
  "name": "${name}",
  "online": true,
  "storage": {
    "blobStoreName": "default",
    "strictContentTypeValidation": true,
    "writePolicy": "allow"
  },
  "docker": {
    "v1Enabled": false,
    "forceBasicAuth": true,
    "httpPort": ${port}
  },
  "component": {
    "proprietaryComponents": false
  }
}
JSON
  echo
}

create_repo "${NEXUS_REPO_A}" "${NEXUS_REPO_A_PORT}"
create_repo "${NEXUS_REPO_B}" "${NEXUS_REPO_B_PORT}"

