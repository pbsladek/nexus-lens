#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${repo_root}"

compose_file="deploy/compose/compose.yaml"
api_url="${NEXUS_LENS_E2E_API_URL:-http://localhost:5601}"
api_user="${NEXUS_LENS_E2E_APP_USER:-admin}"
api_password="${NEXUS_LENS_E2E_APP_PASSWORD:-admin}"
nexus_name="${NEXUS_LENS_E2E_NEXUS_NAME:-local}"
nexus_base_url="${NEXUS_LENS_E2E_NEXUS_BASE_URL:-http://nexus:8081}"
target_repository="${NEXUS_LENS_E2E_REPOSITORY:-docker-hosted-a}"
target_image="${NEXUS_LENS_E2E_IMAGE:-nexus-lens/app}"
target_tag="${NEXUS_LENS_E2E_TAG:-pr-123}"
run_timeout_seconds="${NEXUS_LENS_E2E_TIMEOUT_SECONDS:-300}"
cookie_jar="$(mktemp)"
work_dir="$(mktemp -d)"

cleanup() {
  rm -f "${cookie_jar}"
  rm -rf "${work_dir}"
  NEXUS_LENS_ENABLE_DESTRUCTIVE_CLEANUP=false docker compose -f "${compose_file}" up -d api worker >/dev/null 2>&1 || true
}
trap cleanup EXIT

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "required command not found: $1" >&2
    exit 2
  fi
}

log() {
  printf '[live-cleanup-e2e] %s\n' "$*"
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
  local deadline=$((SECONDS + run_timeout_seconds))
  until curl -fsS "${api_url}/api/v1/ready" >/dev/null 2>&1; do
    if (( SECONDS >= deadline )); then
      echo "timed out waiting for API readiness" >&2
      exit 1
    fi
    sleep 2
  done
}

wait_for_run() {
  local run_id="$1"
  local label="$2"
  local deadline=$((SECONDS + run_timeout_seconds))
  local status=""
  local body="${work_dir}/runs-${run_id}.json"

  while (( SECONDS < deadline )); do
    api_json GET "/api/v1/index/runs?limit=200" > "${body}"
    status="$(jq -r --argjson id "${run_id}" '.runs[] | select(.id == $id) | .status' "${body}" | head -1)"
    case "${status}" in
      succeeded)
        log "${label} run ${run_id} succeeded"
        return
        ;;
      failed|canceled|skipped_blocked)
        echo "${label} run ${run_id} ended with status ${status}" >&2
        jq --argjson id "${run_id}" '.runs[] | select(.id == $id)' "${body}" >&2
        exit 1
        ;;
    esac
    sleep 3
  done

  echo "timed out waiting for ${label} run ${run_id}; last status: ${status:-missing}" >&2
  exit 1
}

wait_for_cleanup_execution() {
  local execution_id="$1"
  local deadline=$((SECONDS + run_timeout_seconds))
  local status=""
  local body="${work_dir}/cleanup-executions-${execution_id}.json"

  while (( SECONDS < deadline )); do
    api_json GET "/api/v1/cleanup/executions?limit=200" > "${body}"
    status="$(jq -r --argjson id "${execution_id}" '.executions[] | select(.id == $id) | .status' "${body}" | head -1)"
    case "${status}" in
      succeeded|succeeded_noop)
        jq --argjson id "${execution_id}" '.executions[] | select(.id == $id)' "${body}" > "${work_dir}/cleanup-execution.json"
        log "cleanup execution ${execution_id} ${status}"
        return
        ;;
      failed|canceled|blocked_read_only|blocked_operation_lock|blocked_maintenance_window|blocked_missing_credentials)
        echo "cleanup execution ${execution_id} ended with status ${status}" >&2
        jq --argjson id "${execution_id}" '.executions[] | select(.id == $id)' "${body}" >&2
        exit 1
        ;;
    esac
    sleep 3
  done

  echo "timed out waiting for cleanup execution ${execution_id}; last status: ${status:-missing}" >&2
  exit 1
}

postgres_scalar() {
  local sql="$1"
  docker compose -f "${compose_file}" exec -T postgres \
    psql -U nexus_lens -d nexus_lens -Atc "${sql}"
}

for command in curl docker jq; do
  require_command "${command}"
done

log "starting disposable Compose services"
docker compose -f "${compose_file}" up -d postgres nexus web

log "preparing Nexus fixture"
test/fixtures/nexus/wait-for-nexus.sh
test/fixtures/nexus/capture-admin-password.sh
test/fixtures/nexus/accept-eula.sh
test/fixtures/nexus/enable-docker-realm.sh
test/fixtures/nexus/create-docker-repos.sh
test/fixtures/nexus/build-and-push-images.sh

log "starting API and worker with destructive cleanup enabled for this fixture run"
NEXUS_LENS_ENABLE_DESTRUCTIVE_CLEANUP=true docker compose -f "${compose_file}" up -d --build api worker
wait_for_api

login_body="${work_dir}/login.json"
jq -n --arg username "${api_user}" --arg password "${api_password}" \
  '{username: $username, password: $password}' > "${login_body}"
api_json POST "/api/v1/session" "${login_body}" >/dev/null

log "running scoped full reconcile for ${target_repository}"
run_body="${work_dir}/full-reconcile.json"
jq -n \
  --arg nexus_name "${nexus_name}" \
  --arg base_url "${nexus_base_url}" \
  --arg repository "${target_repository}" \
  '{
    nexus_name: $nexus_name,
    base_url: $base_url,
    scan_type: "full_reconcile",
    scope: {repositories: [$repository]}
  }' > "${run_body}"
full_reconcile_id="$(api_json POST "/api/v1/index/runs" "${run_body}" | jq -r '.id')"
wait_for_run "${full_reconcile_id}" "initial full reconcile"

nexus_instance_id="$(api_json GET "/api/v1/admin/nexus-instances?limit=50" | jq -r --arg name "${nexus_name}" '.instances[] | select(.name == $name) | .id' | head -1)"
if [[ -z "${nexus_instance_id}" || "${nexus_instance_id}" == "null" ]]; then
  echo "could not find Nexus instance ${nexus_name}" >&2
  exit 1
fi

policy_yaml="$(cat <<POLICY
name: live-cleanup-e2e
candidate_rules:
  - name: delete seeded pr tag
    tags: [${target_tag}]
    image_prefixes: [${target_image}]
POLICY
)"

simulation_body="${work_dir}/cleanup-simulation.json"
jq -n \
  --argjson nexus_instance_id "${nexus_instance_id}" \
  --arg repository "${target_repository}" \
  --arg policy_yaml "${policy_yaml}" \
  '{nexus_instance_id: $nexus_instance_id, repository_name: $repository, policy_yaml: $policy_yaml, limit: 100}' > "${simulation_body}"
api_json POST "/api/v1/cleanup/simulations" "${simulation_body}" > "${work_dir}/cleanup-simulation-result.json"
candidate_count="$(jq -r '.candidate_tag_count' "${work_dir}/cleanup-simulation-result.json")"
target_candidate_count="$(jq -r --arg repository "${target_repository}" --arg image "${target_image}" --arg tag "${target_tag}" '[.candidates[] | select(.repository_name == $repository and .image_name == $image and .tag == $tag)] | length' "${work_dir}/cleanup-simulation-result.json")"
if [[ "${candidate_count}" != "1" || "${target_candidate_count}" != "1" ]]; then
  echo "expected exactly one cleanup candidate for ${target_repository}/${target_image}:${target_tag}; got ${candidate_count}" >&2
  jq . "${work_dir}/cleanup-simulation-result.json" >&2
  exit 1
fi

log "requesting approved cleanup execution"
execution_body="${work_dir}/cleanup-execution-request.json"
jq -n \
  --argjson nexus_instance_id "${nexus_instance_id}" \
  --arg repository "${target_repository}" \
  --arg policy_yaml "${policy_yaml}" \
  '{
    nexus_instance_id: $nexus_instance_id,
    repository_name: $repository,
    policy_yaml: $policy_yaml,
    approval_text: "live-cleanup-e2e",
    limit: 100
  }' > "${execution_body}"
execution_id="$(api_json POST "/api/v1/cleanup/executions" "${execution_body}" | jq -r '.id')"
wait_for_cleanup_execution "${execution_id}"

post_delete_verification_run_id="$(jq -r '.post_delete_verification_run_id // empty' "${work_dir}/cleanup-execution.json")"
post_delete_reconcile_run_id="$(jq -r '.post_delete_reconcile_run_id // empty' "${work_dir}/cleanup-execution.json")"
if [[ -z "${post_delete_verification_run_id}" || -z "${post_delete_reconcile_run_id}" ]]; then
  echo "cleanup execution did not link post-delete verification and reconcile runs" >&2
  jq . "${work_dir}/cleanup-execution.json" >&2
  exit 1
fi
wait_for_run "${post_delete_verification_run_id}" "post-delete verification"
wait_for_run "${post_delete_reconcile_run_id}" "post-delete reconcile"

api_json GET "/api/v1/cleanup/executions/${execution_id}/events?limit=200" > "${work_dir}/cleanup-events.json"
deleted_event_count="$(jq -r --arg repository "${target_repository}" --arg image "${target_image}" --arg tag "${target_tag}" '[.events[] | select(.event_type == "candidate_deleted" and .repository_name == $repository and .image_name == $image and .tag == $tag)] | length' "${work_dir}/cleanup-events.json")"
if [[ "${deleted_event_count}" != "1" ]]; then
  echo "expected one candidate_deleted event for ${target_repository}/${target_image}:${target_tag}" >&2
  jq . "${work_dir}/cleanup-events.json" >&2
  exit 1
fi

source "test/fixtures/nexus/env.sh"
require_nexus_password
registry_tags="$(curl -fsS -u "${NEXUS_USER}:${NEXUS_PASSWORD}" "http://${NEXUS_REGISTRY_A}/v2/${target_image}/tags/list")"
if jq -e --arg tag "${target_tag}" '.tags // [] | index($tag)' >/dev/null <<<"${registry_tags}"; then
  echo "registry still lists deleted tag ${target_tag}" >&2
  printf '%s\n' "${registry_tags}" >&2
  exit 1
fi

deleted_at_present="$(postgres_scalar "
SELECT (ct.deleted_at IS NOT NULL)::text
FROM container_tags ct
JOIN container_images ci ON ci.id = ct.image_id
JOIN repositories r ON r.id = ci.repository_id
JOIN nexus_instances ni ON ni.id = r.nexus_instance_id
WHERE ni.name = '${nexus_name}'
  AND r.name = '${target_repository}'
  AND ci.name = '${target_image}'
  AND ct.tag = '${target_tag}'
ORDER BY ct.id DESC
LIMIT 1;
")"
if [[ "${deleted_at_present}" != "true" ]]; then
  echo "expected local index tag row to be marked deleted; got ${deleted_at_present:-no row}" >&2
  exit 1
fi

log "live cleanup E2E passed: deleted ${target_repository}/${target_image}:${target_tag}, verified Nexus registry state, DB state, events, and follow-up runs"
