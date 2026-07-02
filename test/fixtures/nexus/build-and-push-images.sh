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

push_tags() {
  local image="$1"
  shift

  local tag
  for tag in "$@"; do
    docker push "${image}:${tag}"
  done
}

seed_scale_images() {
  local scale="${NEXUS_LENS_FIXTURE_SCALE:-small}"
  local image_count=0
  local tag_count=0

  case "${scale}" in
    small)
      return
      ;;
    medium)
      image_count="${NEXUS_LENS_FIXTURE_IMAGE_COUNT:-20}"
      tag_count="${NEXUS_LENS_FIXTURE_TAGS_PER_IMAGE:-25}"
      ;;
    large)
      image_count="${NEXUS_LENS_FIXTURE_IMAGE_COUNT:-100}"
      tag_count="${NEXUS_LENS_FIXTURE_TAGS_PER_IMAGE:-100}"
      ;;
    *)
      echo "unsupported NEXUS_LENS_FIXTURE_SCALE=${scale}; expected small, medium, or large" >&2
      exit 2
      ;;
  esac

  local i j image base_tag tag
  for i in $(seq 1 "${image_count}"); do
    if (( i % 2 == 0 )); then
      image="${NEXUS_REGISTRY_B}/nexus-lens/scale-${i}"
    else
      image="${NEXUS_REGISTRY_A}/nexus-lens/scale-${i}"
    fi
    base_tag="v0.${i}.0"
    build_image "scale-${i}" "scale image ${i}" "${image}:${base_tag}"
    docker tag "${image}:${base_tag}" "${image}:latest"
    docker tag "${image}:${base_tag}" "${image}:stable"
    docker tag "${image}:${base_tag}" "${image}:pr-${i}"
    docker tag "${image}:${base_tag}" "${image}:feat-scale-${i}"
    push_tags "${image}" "${base_tag}" latest stable "pr-${i}" "feat-scale-${i}"

    for j in $(seq 1 "${tag_count}"); do
      tag="build-${i}-${j}"
      docker tag "${image}:${base_tag}" "${image}:${tag}"
      docker push "${image}:${tag}"
    done
  done

  echo "seeded ${scale} scale fixture: ${image_count} images with ${tag_count} build tags each"
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

push_tags "${image_a}" v1.2.3 1.2.3 latest stable pr-123 feat-dvo-1313-hsdfjw "${sha_short}" "${sha_long}"

build_image "app-delete-me" "app tag deleted by mutation fixture" "${image_a}:pr-delete-me"
docker push "${image_a}:pr-delete-me"

docker tag "${image_a}:v1.2.3" "${image_b}:v1.2.3"
docker tag "${image_a}:v1.2.3" "${image_b}:latest"
docker tag "${image_a}:v1.2.3" "${image_b}:stable"
docker tag "${image_a}:v1.2.3" "${image_b}:pr-123"

push_tags "${image_b}" v1.2.3 latest stable pr-123

seed_scale_images

echo "seeded fixture images:"
echo "  ${image_a}"
echo "  ${image_b}"
