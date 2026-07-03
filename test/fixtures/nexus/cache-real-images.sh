#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cache_tar="${NEXUS_LENS_FIXTURE_REAL_CACHE_TAR:-deploy/compose/real-image-cache.tar}"
manifest_path="${NEXUS_LENS_FIXTURE_REAL_MANIFEST:-deploy/compose/real-image-cache.manifest.md}"
catalog_path="${NEXUS_LENS_FIXTURE_REAL_CATALOG:-${script_dir}/real-image-catalog.yaml}"
max_bytes="${NEXUS_LENS_FIXTURE_REAL_MAX_BYTES:-5368709120}"
image_refs="${NEXUS_LENS_FIXTURE_REAL_IMAGE_REFS:-}"

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "required command not found: $1" >&2
    exit 2
  fi
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

each_catalog_entry() {
  if [[ -n "${image_refs}" ]]; then
    local source
    for source in ${image_refs}; do
      printf "%s\t%s\t\tcustom override\n" "${source}" "$(real_image_target_path "${source}")"
    done
    return
  fi

  if [[ ! -f "${catalog_path}" ]]; then
    echo "real image catalog not found: ${catalog_path}" >&2
    exit 1
  fi

  parse_catalog_yaml "${catalog_path}"
}

parse_catalog_yaml() {
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

bytes_human() {
  local bytes="$1"
  awk -v bytes="${bytes}" 'BEGIN {
    split("B KiB MiB GiB TiB", units, " ");
    value = bytes;
    unit = 1;
    while (value >= 1024 && unit < 5) {
      value = value / 1024;
      unit++;
    }
    if (unit == 1) {
      printf "%d %s", value, units[unit];
    } else {
      printf "%.1f %s", value, units[unit];
    }
  }'
}

write_manifest_header() {
  mkdir -p "$(dirname "${manifest_path}")"
  {
    echo "# Nexus Lens Real Image Cache Manifest"
    echo
    echo "- Cache tar: \`${cache_tar}\`"
    echo "- Size cap: $(bytes_human "${max_bytes}") (${max_bytes} bytes)"
    echo "- Catalog: \`${catalog_path}\`"
    echo
    echo "| Status | Source | Size | Digest/ID | Nexus target | Extra target tags | Notes |"
    echo "| --- | --- | ---: | --- | --- | --- | --- |"
  } > "${manifest_path}"
}

append_manifest_row() {
  local status="$1"
  local source="$2"
  local size="$3"
  local digest="$4"
  local target_path="$5"
  local target_tags="$6"
  local notes="$7"
  printf "| %s | \`%s\` | %s | \`%s\` | \`%s:%s\` | %s | %s |\n" \
    "${status}" \
    "${source}" \
    "$(bytes_human "${size}")" \
    "${digest}" \
    "${target_path}" \
    "$(real_image_tag "${source}")" \
    "${target_tags:-}" \
    "${notes:-}" >> "${manifest_path}"
}

update_catalog_cache_summary() {
  if [[ -n "${image_refs}" || ! -f "${catalog_path}" ]]; then
    return
  fi

  local tmp_path
  tmp_path="$(mktemp "${catalog_path}.tmp.XXXXXX")"
  awk \
    -v image_count="${selected_count}" \
    -v total_bytes="${total_bytes}" \
    -v cap_bytes="${max_bytes}" \
    -v remaining_bytes="$((max_bytes - total_bytes))" \
    -v total_human="$(bytes_human "${total_bytes}")" \
    -v manifest="${manifest_path}" '
    function print_cache() {
      print "cache:";
      print "  imageCount: " image_count;
      print "  dockerReportedSizeBytes: " total_bytes;
      print "  capBytes: " cap_bytes;
      print "  remainingBytes: " remaining_bytes;
      print "  dockerReportedSize: \"" total_human "\"";
      print "  generatedManifest: \"" manifest "\"";
      printed_cache = 1;
    }

    /^cache:[[:space:]]*$/ {
      if (!printed_cache) {
        print_cache();
      }
      in_cache = 1;
      next;
    }
    /^images:[[:space:]]*$/ {
      if (!printed_cache) {
        print_cache();
      }
      in_cache = 0;
      print;
      next;
    }
    in_cache {
      next;
    }
    {
      print;
    }
    END {
      if (!printed_cache) {
        print_cache();
      }
    }
  ' "${catalog_path}" > "${tmp_path}"
  mv "${tmp_path}" "${catalog_path}"
}

require_command docker
require_command awk

mkdir -p "$(dirname "${cache_tar}")"
write_manifest_header

total_bytes=0
selected=()
selected_count=0
skipped_count=0

while IFS=$'\t' read -r source target_path target_tags notes; do
  if ! docker pull "${source}"; then
    echo "could not pull real image ${source}; skipping" >&2
    append_manifest_row "skipped-pull-error" "${source}" 0 "" "${target_path}" "${target_tags}" "${notes}"
    skipped_count=$((skipped_count + 1))
    continue
  fi
  size="$(docker image inspect "${source}" --format '{{.Size}}')"
  if ! [[ "${size}" =~ ^[0-9]+$ ]]; then
    echo "could not determine Docker size for ${source}; skipping" >&2
    append_manifest_row "skipped-no-size" "${source}" 0 "" "${target_path}" "${target_tags}" "${notes}"
    skipped_count=$((skipped_count + 1))
    continue
  fi

  digest="$(docker image inspect "${source}" --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{else}}{{.Id}}{{end}}')"
  if (( total_bytes + size > max_bytes )); then
    echo "skipping cache image ${source}; size ${size} would exceed cap ${max_bytes}" >&2
    append_manifest_row "skipped-cap" "${source}" "${size}" "${digest}" "${target_path}" "${target_tags}" "${notes}"
    skipped_count=$((skipped_count + 1))
    continue
  fi

  selected+=("${source}")
  total_bytes=$((total_bytes + size))
  selected_count=$((selected_count + 1))
  append_manifest_row "selected" "${source}" "${size}" "${digest}" "${target_path}" "${target_tags}" "${notes}"
done < <(each_catalog_entry)

if (( selected_count == 0 )); then
  echo "no real images selected for cache" >&2
  exit 1
fi

docker save -o "${cache_tar}" "${selected[@]}"
{
  echo
  echo "## Summary"
  echo
  echo "- Selected images: ${selected_count}"
  echo "- Skipped images: ${skipped_count}"
  echo "- Selected Docker-reported size: $(bytes_human "${total_bytes}") (${total_bytes} bytes)"
  echo "- Remaining cap: $(bytes_human "$((max_bytes - total_bytes))") ($((max_bytes - total_bytes)) bytes)"
} >> "${manifest_path}"

update_catalog_cache_summary

echo "wrote ${cache_tar}"
echo "wrote ${manifest_path}"
echo "updated ${catalog_path}"
echo "cached ${selected_count} real images; Docker-reported size total ${total_bytes} bytes; cap ${max_bytes} bytes"
