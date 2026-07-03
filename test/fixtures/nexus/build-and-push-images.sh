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

seed_real_images() {
  if [[ "${NEXUS_LENS_FIXTURE_REAL_IMAGES:-false}" != "true" ]]; then
    return
  fi

  local max_bytes="${NEXUS_LENS_FIXTURE_REAL_MAX_BYTES:-5368709120}"
  local cache_tar="${NEXUS_LENS_FIXTURE_REAL_CACHE_TAR:-}"
  local pull_images="${NEXUS_LENS_FIXTURE_REAL_PULL:-true}"
  if [[ -n "${cache_tar}" && -f "${cache_tar}" ]]; then
    echo "loading real image cache ${cache_tar}"
    docker load -i "${cache_tar}"
  fi

  local total_bytes=0
  local pushed_image_count=0
  local pushed_tag_count=0
  local index=0
  local source target_path target_tags notes size target_registry
  while IFS=$'\t' read -r source target_path target_tags notes; do
    if [[ "${pull_images}" == "true" ]]; then
      if ! docker pull "${source}"; then
        echo "could not pull real image ${source}; skipping" >&2
        continue
      fi
    elif ! docker image inspect "${source}" >/dev/null 2>&1; then
      echo "real image ${source} is not local and pulling is disabled; skipping" >&2
      continue
    fi
    size="$(docker image inspect "${source}" --format '{{.Size}}')"
    if ! [[ "${size}" =~ ^[0-9]+$ ]]; then
      echo "could not determine Docker size for ${source}; skipping" >&2
      continue
    fi
    if (( total_bytes + size > max_bytes )); then
      echo "skipping real image ${source}; size ${size} would exceed cap ${max_bytes}" >&2
      continue
    fi

    if [[ -z "${target_path}" ]]; then
      target_path="$(real_image_target_path "${source}")"
    fi

    target_registry="${NEXUS_REGISTRY_A}"
    if (( index % 2 == 1 )); then
      target_registry="${NEXUS_REGISTRY_B}"
    fi

    local source_tag target_tag target seen_tags extra_tags tag
    source_tag="$(real_image_tag "${source}")"
    seen_tags="|"
    for target_tag in "${source_tag}"; do
      target="${target_registry}/${target_path}:${target_tag}"
      docker tag "${source}" "${target}"
      docker push "${target}"
      seen_tags="${seen_tags}${target_tag}|"
      pushed_tag_count=$((pushed_tag_count + 1))
    done

    IFS=',' read -r -a extra_tags <<< "${target_tags:-}"
    for tag in "${extra_tags[@]}"; do
      target_tag="${tag//[[:space:]]/}"
      [[ -z "${target_tag}" ]] && continue
      if [[ "${seen_tags}" == *"|${target_tag}|"* ]]; then
        continue
      fi
      target="${target_registry}/${target_path}:${target_tag}"
      docker tag "${source}" "${target}"
      docker push "${target}"
      seen_tags="${seen_tags}${target_tag}|"
      pushed_tag_count=$((pushed_tag_count + 1))
    done

    total_bytes=$((total_bytes + size))
    pushed_image_count=$((pushed_image_count + 1))
    index=$((index + 1))
  done < <(each_real_image_catalog_entry)

  echo "seeded ${pushed_image_count} real source images as ${pushed_tag_count} Nexus tags; Docker-reported size total ${total_bytes} bytes; cap ${max_bytes} bytes"
}

real_image_tag() {
  local ref="${1%@*}"
  local leaf="${ref##*/}"
  if [[ "${leaf}" == *:* ]]; then
    printf "%s" "${leaf##*:}"
    return
  fi
  printf "latest"
}

real_image_target_path() {
  local ref="${1%@*}"
  local leaf="${ref##*/}"
  local path="${ref}"
  if [[ "${leaf}" == *:* ]]; then
    path="${ref%:*}"
  fi

  case "${path}" in
    docker.io/library/*)
      path="${path#docker.io/}"
      ;;
    docker.io/*)
      path="${path#docker.io/}"
      ;;
    */*)
      ;;
    *)
      path="library/${path}"
      ;;
  esac

  printf "%s" "${path}"
}

each_real_image_catalog_entry() {
  local image_refs="${NEXUS_LENS_FIXTURE_REAL_IMAGE_REFS:-}"
  if [[ -n "${image_refs}" ]]; then
    local source
    for source in ${image_refs}; do
      printf "%s\t%s\t\tcustom override\n" "${source}" "$(real_image_target_path "${source}")"
    done
    return
  fi

  local catalog_path="${NEXUS_LENS_FIXTURE_REAL_CATALOG:-${script_dir}/real-image-catalog.yaml}"
  if [[ ! -f "${catalog_path}" ]]; then
    echo "real image catalog not found: ${catalog_path}" >&2
    exit 1
  fi

  parse_real_image_catalog_yaml "${catalog_path}"
}

parse_real_image_catalog_yaml() {
  awk '
    function trim(value) {
      sub(/^[[:space:]]+/, "", value);
      sub(/[[:space:]]+$/, "", value);
      return value;
    }
    function unquote(value) {
      value = trim(value);
      if (value ~ /^".*"$/) {
        value = substr(value, 2, length(value) - 2);
      }
      return value;
    }
    function yaml_value(line) {
      sub(/^[[:space:]]*[^:]+:[[:space:]]*/, "", line);
      return unquote(line);
    }
    function tag_list(line) {
      line = yaml_value(line);
      gsub(/^\[/, "", line);
      gsub(/\]$/, "", line);
      gsub(/"/, "", line);
      gsub(/[[:space:]]*,[[:space:]]*/, ",", line);
      return trim(line);
    }
    function emit() {
      if (source != "") {
        printf "%s\t%s\t%s\t%s\n", source, target_path, target_tags, notes;
      }
    }

    /^[[:space:]]*-[[:space:]]source:/ {
      emit();
      source = $0;
      sub(/^[[:space:]]*-[[:space:]]source:[[:space:]]*/, "", source);
      source = unquote(source);
      target_path = "";
      target_tags = "";
      notes = "";
      next;
    }
    /^[[:space:]]*targetPath:/ {
      target_path = yaml_value($0);
      next;
    }
    /^[[:space:]]*targetTags:/ {
      target_tags = tag_list($0);
      next;
    }
    /^[[:space:]]*notes:/ {
      notes = yaml_value($0);
      next;
    }
    END {
      emit();
    }
  ' "$1"
}

seed_common_images() {
  if [[ "${NEXUS_LENS_FIXTURE_COMMON_IMAGES:-false}" != "true" ]]; then
    return
  fi

  seed_common_image "${NEXUS_REGISTRY_A}/library/alpine" "common alpine base" \
    3.23 3.24 edge latest
  seed_common_image "${NEXUS_REGISTRY_A}/library/busybox" "common busybox base" \
    1.37 1.38 stable latest
  seed_common_image "${NEXUS_REGISTRY_A}/library/debian" "common debian base" \
    12.14 12.14-slim bookworm bookworm-slim stable latest
  seed_common_image "${NEXUS_REGISTRY_A}/library/ubuntu" "common ubuntu base" \
    24.04 26.04 noble resolute latest
  seed_common_image "${NEXUS_REGISTRY_A}/library/postgres" "common postgres base" \
    17 17-alpine 18 18-alpine latest
  seed_common_image "${NEXUS_REGISTRY_B}/library/nginx" "common nginx runtime" \
    1.28 1.28-alpine 1.29 1.29-alpine stable latest
  seed_common_image "${NEXUS_REGISTRY_B}/library/redis" "common redis runtime" \
    8 8-alpine 8.8 8.8-alpine latest
  seed_common_image "${NEXUS_REGISTRY_B}/library/node" "common node runtime" \
    24 24-alpine 24-slim 26 26-alpine 26-slim latest
  seed_common_image "${NEXUS_REGISTRY_B}/library/python" "common python runtime" \
    3.13 3.13-slim 3.14 3.14-slim latest

  echo "seeded lightweight common image catalog"
}

seed_common_image() {
  local image="$1"
  local app_text="$2"
  shift 2

  local first_tag="$1"
  local safe_name="${image#*/}"
  safe_name="${safe_name//\//-}"
  build_image "common-${safe_name}" "${app_text}" "${image}:${first_tag}"

  local tag
  for tag in "$@"; do
    if [[ "${tag}" != "${first_tag}" ]]; then
      docker tag "${image}:${first_tag}" "${image}:${tag}"
    fi
  done
  push_tags "${image}" "$@"
}

seed_complex_tag_images() {
  if [[ "${NEXUS_LENS_FIXTURE_COMPLEX_TAGS:-false}" != "true" ]]; then
    return
  fi

  local tag_count="${NEXUS_LENS_FIXTURE_COMPLEX_TAG_COUNT:-96}"
  if ! [[ "${tag_count}" =~ ^[0-9]+$ ]]; then
    echo "unsupported NEXUS_LENS_FIXTURE_COMPLEX_TAG_COUNT=${tag_count}; expected a number" >&2
    exit 2
  fi

  local image_a="${NEXUS_REGISTRY_A}/nexus-lens/tag-patterns/api"
  local image_b="${NEXUS_REGISTRY_B}/nexus-lens/tag-patterns/worker"
  build_image "complex-tags-api" "complex tag pattern fixture api" "${image_a}:v2.8.0"
  build_image "complex-tags-worker" "complex tag pattern fixture worker" "${image_b}:v2.8.0"

  local baseline_tags=(
    v2.8.0
    2.8.0
    2.8.0-rc.1
    2.8.0-rc.2
    2.8.0-build.1842
    2026.07.03
    20260703.1842
    latest
    stable
    main-a1b2c3d
    main-a1b2c3d-SNAPSHOT
    release-2026.07-v2.8.0
    release-2026.07-v2.8.0-a1b2c3d
    branch-name-variations-a1b2c3d-SNAPSHOT
    branch-name-variations-f8e7d6c-SNAPSHOT
    feature-checkout-redesign-v2.8.0-a1b2c3d
    feature-checkout-redesign-a1b2c3d-SNAPSHOT
    fix-login-timeout-v2.8.1-b2c3d4e
    hotfix-prod-cache-v2.8.2-c3d4e5f
    pr-1842-a1b2c3d
    pr-1842-merge-b2c3d4e
    dependabot-npm-react-19-c3d4e5f
  )

  local tag
  for tag in "${baseline_tags[@]}"; do
    docker tag "${image_a}:v2.8.0" "${image_a}:${tag}"
    docker tag "${image_b}:v2.8.0" "${image_b}:${tag}"
  done
  push_tags "${image_a}" "${baseline_tags[@]}"
  push_tags "${image_b}" "${baseline_tags[@]}"

  local branches=(
    feature-auth-session-renewal
    feature-checkout-redesign
    feature-nexus-assets-api
    feature-tag-pattern-insights
    feature-ui-high-cardinality
    fix-login-timeout
    fix-postgres-index-plan
    bugfix-layer-size-rollup
    hotfix-prod-cache
    chore-renovate-dockerfiles
    dependabot-npm-vite-7
    experiment-branch-classifier
    release-2026.07
    users-pbsladek-nexus-lens
  )
  local hashes=(
    a1b2c3d
    b2c3d4e
    c3d4e5f
    d4e5f60
    e5f6071
    f607182
    0abc123
    1bcd234
    2cde345
    3def456
    4ef5678
    5f67890
  )
  local versions=(
    2.8.0
    2.8.1
    2.9.0
    3.0.0
    3.0.0-rc.1
    2026.07.03
  )

  local i branch hash version generated_tag
  for i in $(seq 1 "${tag_count}"); do
    branch="${branches[$(((i - 1) % ${#branches[@]}))]}"
    hash="${hashes[$(((i - 1) % ${#hashes[@]}))]}"
    version="${versions[$(((i - 1) % ${#versions[@]}))]}"

    case $((i % 8)) in
      0)
        generated_tag="${branch}-${hash}-SNAPSHOT"
        ;;
      1)
        generated_tag="${branch}-v${version}-${hash}"
        ;;
      2)
        generated_tag="${branch}-${version}-build.${i}-${hash}"
        ;;
      3)
        generated_tag="refs-heads-${branch}-${hash}"
        ;;
      4)
        generated_tag="pr-${i}-${branch}-${hash}"
        ;;
      5)
        generated_tag="${branch}-${hash}-dirty"
        ;;
      6)
        generated_tag="${branch}-git-${hash}"
        ;;
      *)
        generated_tag="${branch}-${version}-${hash}-SNAPSHOT"
        ;;
    esac

    docker tag "${image_a}:v2.8.0" "${image_a}:${generated_tag}"
    docker push "${image_a}:${generated_tag}"
    docker tag "${image_b}:v2.8.0" "${image_b}:${generated_tag}"
    docker push "${image_b}:${generated_tag}"
  done

  echo "seeded complex tag fixture: 2 images with $((tag_count + ${#baseline_tags[@]})) tags each"
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

seed_common_images
seed_complex_tag_images
seed_real_images
seed_scale_images

echo "seeded fixture images:"
echo "  ${image_a}"
echo "  ${image_b}"
