#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/env.sh"
require_nexus_password

echo "Nexus status:"
curl -fsS -u "${NEXUS_USER}:${NEXUS_PASSWORD}" "${NEXUS_URL}/service/rest/v1/status"
echo

echo "Repositories:"
curl -fsS -u "${NEXUS_USER}:${NEXUS_PASSWORD}" "${NEXUS_URL}/service/rest/v1/repositories" | sed 's/},{/},\n{/g'
echo

echo "Assets page for ${NEXUS_REPO_A}:"
curl -fsS -u "${NEXUS_USER}:${NEXUS_PASSWORD}" "${NEXUS_URL}/service/rest/v1/assets?repository=${NEXUS_REPO_A}" | sed 's/},{/},\n{/g'
echo

echo "Registry tags for ${NEXUS_REPO_A}:"
curl -fsS -u "${NEXUS_USER}:${NEXUS_PASSWORD}" "http://${NEXUS_REGISTRY_A}/v2/nexus-lens/app/tags/list"
echo

