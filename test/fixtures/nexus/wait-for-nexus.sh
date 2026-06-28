#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/env.sh"

timeout_seconds="${1:-300}"
deadline=$((SECONDS + timeout_seconds))

echo "waiting for Nexus at ${NEXUS_URL}"
while (( SECONDS < deadline )); do
  if curl -fsS "${NEXUS_URL}/service/rest/v1/status" >/dev/null 2>&1; then
    echo "Nexus is responding"
    exit 0
  fi
  sleep 5
done

echo "timed out waiting for Nexus after ${timeout_seconds}s" >&2
exit 1

