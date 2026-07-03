#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/env.sh"

apply=false
local_docker=false
verbose=false

usage() {
  cat <<'USAGE'
Prune retired Nexus Lens fixture seed tags from the local Nexus fixture.

Default mode is a dry run. Nothing is deleted unless --apply is passed.

Usage:
  test/fixtures/nexus/prune-old-seed-data.sh [--apply] [--local-docker]

Options:
  --apply          Delete matching Nexus assets instead of printing a dry run.
  --local-docker   Also remove matching local Docker image tags. This is off by
                   default because those images may be useful outside this repo.
  --verbose        Print missing Nexus assets and Docker image tags individually.
  -h, --help       Show this help.

After applying Nexus deletes, run:
  make fixture-index

That lets Nexus Lens mark the deleted tags in Postgres.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --apply)
      apply=true
      ;;
    --local-docker)
      local_docker=true
      ;;
    --verbose)
      verbose=true
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "required command not found: $1" >&2
    exit 2
  fi
}

require_nexus_password
require_command curl
require_command python3

if [[ "${local_docker}" == "true" ]]; then
  require_command docker
fi

work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT

add_targets_for_repo() {
  local repo="$1"
  local image="$2"
  local reason="$3"
  shift 3

  local tag
  for tag in "$@"; do
    printf "%s\t%s\t%s\t%s\n" "${repo}" "${image}" "${tag}" "${reason}"
  done
}

add_targets_for_both_repos() {
  local image="$1"
  local reason="$2"
  shift 2

  add_targets_for_repo "${NEXUS_REPO_A}" "${image}" "${reason}" "$@"
  add_targets_for_repo "${NEXUS_REPO_B}" "${image}" "${reason}" "$@"
}

old_nexus_targets() {
  add_targets_for_both_repos "library/alpine" "retired Alpine seed versions" \
    3.19 3.20 3.21 3.19-stable 3.20-stable 3.21-stable
  add_targets_for_both_repos "library/busybox" "retired BusyBox seed versions" \
    1.35 1.36 1.36-glibc
  add_targets_for_both_repos "library/debian" "retired Debian seed versions" \
    bullseye bullseye-slim
  add_targets_for_both_repos "library/ubuntu" "retired Ubuntu seed versions" \
    22.04 jammy noble-20260610
  add_targets_for_both_repos "library/postgres" "retired Postgres seed versions" \
    15 15-alpine 16 16-alpine
  add_targets_for_both_repos "library/nginx" "retired Nginx seed versions" \
    1.25 1.25-alpine 1.26 1.26-alpine 1.27 1.27-alpine
  add_targets_for_both_repos "library/redis" "retired Redis seed versions" \
    7 7-alpine 7.2 7.2-alpine
  add_targets_for_both_repos "library/node" "retired Node seed versions" \
    20 20-alpine 20-slim 22 22-alpine 22-slim
  add_targets_for_both_repos "library/python" "retired Python seed versions" \
    3.11 3.11-alpine 3.11-slim 3.12 3.12-alpine 3.12-slim 3.13-rc
  add_targets_for_both_repos "library/golang" "retired Go seed versions" \
    1.22 1.22-alpine 1.22-stable 1.23 1.23-alpine 1.23-stable \
    1.24 1.24-alpine 1.24-stable 1.25-alpine
  add_targets_for_both_repos "library/mongo" "retired Mongo seed versions" \
    7
}

old_upstream_image_refs() {
  cat <<'REFS'
alpine:3.19
alpine:3.20
alpine:3.21
busybox:1.36
debian:bullseye-slim
ubuntu:22.04
nginx:1.26-alpine
nginx:1.27-alpine
redis:7-alpine
postgres:15-alpine
postgres:16-alpine
mongo:7
node:20-alpine
node:20-slim
node:22-alpine
node:22-slim
python:3.11-alpine
python:3.11-slim
python:3.12-alpine
python:3.12-slim
golang:1.22-alpine
golang:1.23-alpine
golang:1.24-alpine
golang:1.25-alpine
REFS
}

asset_map_path() {
  local repo="$1"
  printf "%s/%s.assets.tsv" "${work_dir}" "${repo//[^A-Za-z0-9_.-]/_}"
}

fetch_asset_map() {
  local repo="$1"
  local out_path
  out_path="$(asset_map_path "${repo}")"
  : > "${out_path}"

  local token=""
  local page parsed next_token
  while :; do
    if [[ -n "${token}" ]]; then
      page="$(curl -fsS -u "${NEXUS_USER}:${NEXUS_PASSWORD}" \
        -G \
        --data-urlencode "repository=${repo}" \
        --data-urlencode "continuationToken=${token}" \
        "${NEXUS_URL}/service/rest/v1/assets")"
    else
      page="$(curl -fsS -u "${NEXUS_USER}:${NEXUS_PASSWORD}" \
        -G \
        --data-urlencode "repository=${repo}" \
        "${NEXUS_URL}/service/rest/v1/assets")"
    fi

    parsed="$(printf "%s" "${page}" | python3 -c '
import json
import sys

data = json.load(sys.stdin)
for item in data.get("items", []):
    asset_id = item.get("id")
    path = item.get("path")
    if asset_id and path:
        print(f"ASSET\t{asset_id}\t{path}")
token = data.get("continuationToken")
if token:
    print(f"TOKEN\t{token}")
')"
    printf "%s\n" "${parsed}" | awk -F '\t' '$1 == "ASSET" {print $2 "\t" $3}' >> "${out_path}"
    next_token="$(printf "%s\n" "${parsed}" | awk -F '\t' '$1 == "TOKEN" {print $2; exit}')"
    if [[ -z "${next_token}" ]]; then
      break
    fi
    token="${next_token}"
  done
}

delete_nexus_targets() {
  local targets_path="${work_dir}/old-targets.tsv"
  old_nexus_targets > "${targets_path}"

  fetch_asset_map "${NEXUS_REPO_A}"
  fetch_asset_map "${NEXUS_REPO_B}"

  local deleted=0
  local missing=0
  local repo image tag reason path map_path ids asset_id
  while IFS=$'\t' read -r repo image tag reason; do
    path="/v2/${image}/manifests/${tag}"
    map_path="$(asset_map_path "${repo}")"
    ids="$(awk -F '\t' -v path="${path}" '$2 == path {print $1}' "${map_path}")"
    if [[ -z "${ids}" ]]; then
      if [[ "${verbose}" == "true" ]]; then
        printf "missing nexus asset: repo=%s path=%s (%s)\n" "${repo}" "${path}" "${reason}"
      fi
      missing=$((missing + 1))
      continue
    fi

    while IFS= read -r asset_id; do
      [[ -z "${asset_id}" ]] && continue
      if [[ "${apply}" == "true" ]]; then
        curl -fsS -u "${NEXUS_USER}:${NEXUS_PASSWORD}" \
          -X DELETE "${NEXUS_URL}/service/rest/v1/assets/${asset_id}"
        printf "deleted nexus asset: repo=%s id=%s path=%s (%s)\n" "${repo}" "${asset_id}" "${path}" "${reason}"
      else
        printf "would delete nexus asset: repo=%s id=%s path=%s (%s)\n" "${repo}" "${asset_id}" "${path}" "${reason}"
      fi
      deleted=$((deleted + 1))
    done <<< "${ids}"
  done < "${targets_path}"

  if [[ "${apply}" == "true" ]]; then
    printf "nexus prune complete: deleted=%s missing=%s\n" "${deleted}" "${missing}"
  else
    printf "nexus prune dry run complete: would_delete=%s missing=%s\n" "${deleted}" "${missing}"
  fi
}

prune_local_docker_refs() {
  if [[ "${local_docker}" != "true" ]]; then
    echo "local Docker image pruning skipped; pass --local-docker to include it"
    return
  fi

  local refs_path="${work_dir}/docker-refs.txt"
  {
    old_nexus_targets | awk -F '\t' -v registry="${NEXUS_REGISTRY_A}" '$1 != "" {print registry "/" $2 ":" $3}'
    old_nexus_targets | awk -F '\t' -v registry="${NEXUS_REGISTRY_B}" '$1 != "" {print registry "/" $2 ":" $3}'
    old_upstream_image_refs
  } | sort -u > "${refs_path}"

  local removed=0
  local missing=0
  local skipped_in_use=0
  local failed=0
  local ref
  while IFS= read -r ref; do
    [[ -z "${ref}" ]] && continue
    if ! docker image inspect "${ref}" >/dev/null 2>&1; then
      if [[ "${verbose}" == "true" ]]; then
        printf "missing local docker image tag: %s\n" "${ref}"
      fi
      missing=$((missing + 1))
      continue
    fi

    if docker ps -a --filter "ancestor=${ref}" --format '{{.ID}}' | grep -q .; then
      printf "skipping local docker image tag in use by a container: %s\n" "${ref}"
      skipped_in_use=$((skipped_in_use + 1))
      continue
    fi

    if [[ "${apply}" == "true" ]]; then
      if docker rmi "${ref}"; then
        printf "removed local docker image tag: %s\n" "${ref}"
      else
        printf "failed to remove local docker image tag: %s\n" "${ref}" >&2
        failed=$((failed + 1))
        continue
      fi
    else
      printf "would remove local docker image tag: %s\n" "${ref}"
    fi
    removed=$((removed + 1))
  done < "${refs_path}"

  if [[ "${apply}" == "true" ]]; then
    printf "local docker prune complete: removed=%s skipped_in_use=%s failed=%s missing=%s\n" "${removed}" "${skipped_in_use}" "${failed}" "${missing}"
  else
    printf "local docker prune dry run complete: would_remove=%s skipped_in_use=%s missing=%s\n" "${removed}" "${skipped_in_use}" "${missing}"
  fi
}

if [[ "${apply}" == "true" ]]; then
  echo "apply mode enabled: matching Nexus assets will be deleted"
else
  echo "dry-run mode: pass --apply to delete matching Nexus assets"
fi

delete_nexus_targets
prune_local_docker_refs

if [[ "${apply}" == "true" ]]; then
  echo "Run make fixture-index to update the local index after these deletes."
fi
