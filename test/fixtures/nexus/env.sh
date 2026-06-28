#!/usr/bin/env bash
set -euo pipefail

export NEXUS_URL="${NEXUS_URL:-http://localhost:8081}"
export NEXUS_USER="${NEXUS_USER:-admin}"
export NEXUS_PASSWORD="${NEXUS_PASSWORD:-}"
export NEXUS_REPO_A="${NEXUS_REPO_A:-docker-hosted-a}"
export NEXUS_REPO_B="${NEXUS_REPO_B:-docker-hosted-b}"
export NEXUS_REPO_A_PORT="${NEXUS_REPO_A_PORT:-5001}"
export NEXUS_REPO_B_PORT="${NEXUS_REPO_B_PORT:-5002}"
export NEXUS_REGISTRY_A="${NEXUS_REGISTRY_A:-localhost:${NEXUS_REPO_A_PORT}}"
export NEXUS_REGISTRY_B="${NEXUS_REGISTRY_B:-localhost:${NEXUS_REPO_B_PORT}}"

if [[ -z "${NEXUS_PASSWORD}" ]]; then
  password_file="${NEXUS_PASSWORD_FILE:-deploy/compose/.nexus-admin-password}"
  if [[ -f "${password_file}" ]]; then
    NEXUS_PASSWORD="$(<"${password_file}")"
    export NEXUS_PASSWORD
  fi
fi

require_nexus_password() {
  if [[ -z "${NEXUS_PASSWORD}" ]]; then
    echo "NEXUS_PASSWORD is required. Set it or write it to deploy/compose/.nexus-admin-password." >&2
    exit 2
  fi
}

