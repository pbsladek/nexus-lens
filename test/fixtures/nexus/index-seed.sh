#!/usr/bin/env bash
set -euo pipefail

api_url="${NEXUS_LENS_FIXTURE_API_URL:-http://localhost:5601}"
api_user="${NEXUS_LENS_FIXTURE_APP_USER:-admin}"
api_password="${NEXUS_LENS_FIXTURE_APP_PASSWORD:-admin}"
nexus_name="${NEXUS_LENS_FIXTURE_NEXUS_NAME:-local}"
nexus_base_url="${NEXUS_LENS_FIXTURE_NEXUS_BASE_URL:-http://nexus:8081}"
timeout_seconds="${NEXUS_LENS_FIXTURE_INDEX_TIMEOUT_SECONDS:-300}"
cookie_jar="$(mktemp)"
work_dir="$(mktemp -d)"

cleanup() {
  rm -f "${cookie_jar}"
  rm -rf "${work_dir}"
}
trap cleanup EXIT

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "required command not found: $1" >&2
    exit 2
  fi
}

api_json() {
  local method="$1"
  local path="$2"
  local body="${3:-}"

  if [[ -n "${body}" ]]; then
    curl -fsS -X "${method}" \
      -H "Content-Type: application/json" \
      -b "${cookie_jar}" \
      -c "${cookie_jar}" \
      --data-binary @"${body}" \
      "${api_url}${path}"
  else
    curl -fsS -X "${method}" \
      -b "${cookie_jar}" \
      -c "${cookie_jar}" \
      "${api_url}${path}"
  fi
}

wait_for_api() {
  local deadline=$((SECONDS + timeout_seconds))
  until curl -fsS "${api_url}/api/v1/ready" >/dev/null 2>&1; do
    if (( SECONDS >= deadline )); then
      echo "timed out waiting for API readiness at ${api_url}" >&2
      exit 1
    fi
    sleep 2
  done
}

wait_for_run() {
  local run_id="$1"
  local deadline=$((SECONDS + timeout_seconds))
  local status=""
  local body="${work_dir}/runs-${run_id}.json"

  while (( SECONDS < deadline )); do
    api_json GET "/api/v1/index/runs?limit=200" > "${body}"
    status="$(jq -r --argjson id "${run_id}" '.runs[] | select(.id == $id) | .status' "${body}" | head -1)"
    case "${status}" in
      succeeded)
        echo "fixture index run ${run_id} succeeded"
        return
        ;;
      failed|canceled|skipped_blocked)
        echo "fixture index run ${run_id} ended with status ${status}" >&2
        jq --argjson id "${run_id}" '.runs[] | select(.id == $id)' "${body}" >&2
        exit 1
        ;;
    esac
    sleep 3
  done

  echo "timed out waiting for fixture index run ${run_id}; last status: ${status:-missing}" >&2
  exit 1
}

for command in curl jq; do
  require_command "${command}"
done

wait_for_api

login_body="${work_dir}/login.json"
jq -n --arg username "${api_user}" --arg password "${api_password}" \
  '{username: $username, password: $password}' > "${login_body}"
api_json POST "/api/v1/session" "${login_body}" >/dev/null

run_body="${work_dir}/full-reconcile.json"
jq -n \
  --arg nexus_name "${nexus_name}" \
  --arg base_url "${nexus_base_url}" \
  '{
    nexus_name: $nexus_name,
    base_url: $base_url,
    scan_type: "full_reconcile",
    scope: {repositories: ["docker-hosted-a", "docker-hosted-b"]}
  }' > "${run_body}"

run_id="$(api_json POST "/api/v1/index/runs" "${run_body}" | jq -r '.id')"
echo "started fixture index run ${run_id}"
wait_for_run "${run_id}"
