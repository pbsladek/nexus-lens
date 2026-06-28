#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/env.sh"
require_nexus_password

active_realms="$(curl -fsS -u "${NEXUS_USER}:${NEXUS_PASSWORD}" "${NEXUS_URL}/service/rest/v1/security/realms/active")"
if [[ "${active_realms}" == *'"DockerToken"'* ]]; then
  echo "Docker Bearer Token Realm already active"
  exit 0
fi

echo "enabling Docker Bearer Token Realm"
curl -fsS -u "${NEXUS_USER}:${NEXUS_PASSWORD}" \
  -H "Content-Type: application/json" \
  -X PUT "${NEXUS_URL}/service/rest/v1/security/realms/active" \
  --data '["NexusAuthenticatingRealm","DockerToken"]'
echo

