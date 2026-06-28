#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/env.sh"
require_nexus_password

work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT

image_a="${NEXUS_REGISTRY_A}/nexus-lens/app"

printf "%s\n" "${NEXUS_PASSWORD}" | docker login "${NEXUS_REGISTRY_A}" -u "${NEXUS_USER}" --password-stdin

find_asset_id_by_path() {
  local target_path="$1"

  curl -fsS -u "${NEXUS_USER}:${NEXUS_PASSWORD}" \
    "${NEXUS_URL}/service/rest/v1/assets?repository=${NEXUS_REPO_A}" \
    | awk -v target_path="${target_path}" '
      /"path"[[:space:]]*:/ {
        path = $0
        sub(/^.*"path"[[:space:]]*:[[:space:]]*"/, "", path)
        sub(/".*$/, "", path)
        matched = (path == target_path)
      }
      matched && /"id"[[:space:]]*:/ {
        id = $0
        sub(/^.*"id"[[:space:]]*:[[:space:]]*"/, "", id)
        sub(/".*$/, "", id)
        print id
        exit
      }
    '
}

mkdir -p "${work_dir}/app-moved"
printf "shared-layer-v1\n" > "${work_dir}/app-moved/common.txt"
printf "app moved latest\n" > "${work_dir}/app-moved/app.txt"
cat > "${work_dir}/app-moved/Dockerfile" <<'DOCKERFILE'
FROM scratch
COPY common.txt /common.txt
COPY app.txt /app.txt
DOCKERFILE

docker build -t "${image_a}:latest" "${work_dir}/app-moved"
docker push "${image_a}:latest"
echo "moved ${image_a}:latest to a new digest"

delete_asset_path="/v2/nexus-lens/app/manifests/pr-delete-me"
delete_asset_id="$(find_asset_id_by_path "${delete_asset_path}" || true)"
if [[ -n "${delete_asset_id}" ]]; then
  curl -fsS -u "${NEXUS_USER}:${NEXUS_PASSWORD}" \
    -X DELETE "${NEXUS_URL}/service/rest/v1/assets/${delete_asset_id}"
  echo "deleted ${image_a}:pr-delete-me asset ${delete_asset_id}"
else
  echo "could not resolve asset id for ${image_a}:pr-delete-me; skipping delete attempt" >&2
fi

docker tag "${image_a}:latest" "${image_a}:pr-123"
docker push "${image_a}:pr-123"
echo "recreated ${image_a}:pr-123"
