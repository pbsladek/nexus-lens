#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/env.sh"
require_nexus_password

work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT

printf "%s\n" "${NEXUS_PASSWORD}" | docker login "${NEXUS_REGISTRY_A}" -u "${NEXUS_USER}" --password-stdin
printf "%s\n" "${NEXUS_PASSWORD}" | docker login "${NEXUS_REGISTRY_B}" -u "${NEXUS_USER}" --password-stdin

build_image() {
  local name="$1"
  local app_text="$2"
  local tag="$3"

  local dir="${work_dir}/${name}"
  mkdir -p "${dir}"
  printf "shared-layer-v1\n" > "${dir}/common.txt"
  printf "%s\n" "${app_text}" > "${dir}/app.txt"
  cat > "${dir}/Dockerfile" <<'DOCKERFILE'
FROM scratch
COPY common.txt /common.txt
COPY app.txt /app.txt
DOCKERFILE

  docker build -t "${tag}" "${dir}"
}

image_a="${NEXUS_REGISTRY_A}/nexus-lens/app"
image_b="${NEXUS_REGISTRY_B}/nexus-lens/app"
sha_short="a1b2c3d"
sha_long="0123456789abcdef0123456789abcdef01234567"

build_image "app-shared" "app shared v1" "${image_a}:v1.2.3"
docker tag "${image_a}:v1.2.3" "${image_a}:1.2.3"
docker tag "${image_a}:v1.2.3" "${image_a}:latest"
docker tag "${image_a}:v1.2.3" "${image_a}:stable"
docker tag "${image_a}:v1.2.3" "${image_a}:pr-123"
docker tag "${image_a}:v1.2.3" "${image_a}:feat-dvo-1313-hsdfjw"
docker tag "${image_a}:v1.2.3" "${image_a}:${sha_short}"
docker tag "${image_a}:v1.2.3" "${image_a}:${sha_long}"

for tag in v1.2.3 1.2.3 latest stable pr-123 feat-dvo-1313-hsdfjw "${sha_short}" "${sha_long}"; do
  docker push "${image_a}:${tag}"
done

build_image "app-delete-me" "app tag deleted by mutation fixture" "${image_a}:pr-delete-me"
docker push "${image_a}:pr-delete-me"

docker tag "${image_a}:v1.2.3" "${image_b}:v1.2.3"
docker tag "${image_a}:v1.2.3" "${image_b}:latest"
docker tag "${image_a}:v1.2.3" "${image_b}:stable"
docker tag "${image_a}:v1.2.3" "${image_b}:pr-123"

for tag in v1.2.3 latest stable pr-123; do
  docker push "${image_b}:${tag}"
done

echo "seeded fixture images:"
echo "  ${image_a}"
echo "  ${image_b}"
