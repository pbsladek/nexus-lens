-- name: GetAnalyticsOverview :one
WITH docker_repositories AS (
  SELECT r.id
  FROM repositories r
  WHERE r.format = 'docker'
    AND r.type = 'hosted'
    AND (sqlc.narg(nexus_instance_id)::bigint IS NULL OR r.nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
),
active_tags AS (
  SELECT
    ct.id,
    ct.digest,
    ct.last_seen_at,
    ci.id AS image_id,
    ci.repository_id,
    dm.logical_size_bytes
  FROM container_tags ct
  JOIN container_images ci ON ci.id = ct.image_id
  JOIN docker_repositories dr ON dr.id = ci.repository_id
  LEFT JOIN docker_manifests dm ON dm.repository_id = ci.repository_id AND dm.digest = ct.digest
  WHERE ct.deleted_at IS NULL
),
asset_totals AS (
  SELECT COALESCE(sum(na.size_bytes), 0)::bigint AS nexus_asset_bytes
  FROM nexus_assets na
  JOIN docker_repositories dr ON dr.id = na.repository_id
),
layer_totals AS (
  SELECT COALESCE(sum(dl.size_bytes), 0)::bigint AS docker_unique_layer_bytes
  FROM docker_layers dl
  JOIN docker_repositories dr ON dr.id = dl.repository_id
),
latest_success AS (
  SELECT max(finished_at) AS last_successful_run_at
  FROM scan_runs
  WHERE status = 'succeeded'
    AND (sqlc.narg(nexus_instance_id)::bigint IS NULL OR nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
)
SELECT
  (SELECT count(*) FROM docker_repositories)::bigint AS repository_count,
  (SELECT count(*) FROM container_images ci JOIN docker_repositories dr ON dr.id = ci.repository_id)::bigint AS image_count,
  (SELECT count(*) FROM active_tags)::bigint AS active_tag_count,
  (SELECT count(*) FROM active_tags WHERE digest IS NULL OR digest = '')::bigint AS tags_missing_digest_count,
  (SELECT count(*) FROM active_tags WHERE logical_size_bytes IS NULL)::bigint AS tags_missing_manifest_count,
  (SELECT COALESCE(sum(logical_size_bytes), 0)::bigint FROM active_tags)::bigint AS docker_logical_bytes,
  asset_totals.nexus_asset_bytes,
  layer_totals.docker_unique_layer_bytes,
  latest_success.last_successful_run_at
FROM asset_totals, layer_totals, latest_success;

-- name: ListRepositoryAnalytics :many
WITH image_counts AS (
  SELECT
    ci.repository_id,
    count(ci.id)::bigint AS image_count
  FROM container_images ci
  GROUP BY ci.repository_id
),
tag_rollup AS (
  SELECT
    ci.repository_id,
    count(ct.id)::bigint AS active_tag_count,
    COALESCE(sum(dm.logical_size_bytes), 0)::bigint AS docker_logical_bytes
  FROM container_images ci
  LEFT JOIN container_tags ct ON ct.image_id = ci.id AND ct.deleted_at IS NULL
  LEFT JOIN docker_manifests dm ON dm.repository_id = ci.repository_id AND dm.digest = ct.digest
  GROUP BY ci.repository_id
),
asset_totals AS (
  SELECT repository_id, COALESCE(sum(size_bytes), 0)::bigint AS nexus_asset_bytes
  FROM nexus_assets
  GROUP BY repository_id
),
layer_totals AS (
  SELECT repository_id, COALESCE(sum(size_bytes), 0)::bigint AS docker_unique_layer_bytes
  FROM docker_layers
  GROUP BY repository_id
)
SELECT
  r.id AS repository_id,
  r.nexus_instance_id,
  n.name AS nexus_name,
  r.name AS repository_name,
  COALESCE(image_counts.image_count, 0)::bigint AS image_count,
  COALESCE(tag_rollup.active_tag_count, 0)::bigint AS active_tag_count,
  COALESCE(asset_totals.nexus_asset_bytes, 0)::bigint AS nexus_asset_bytes,
  COALESCE(tag_rollup.docker_logical_bytes, 0)::bigint AS docker_logical_bytes,
  COALESCE(layer_totals.docker_unique_layer_bytes, 0)::bigint AS docker_unique_layer_bytes,
  r.last_seen_at
FROM repositories r
JOIN nexus_instances n ON n.id = r.nexus_instance_id
LEFT JOIN image_counts ON image_counts.repository_id = r.id
LEFT JOIN tag_rollup ON tag_rollup.repository_id = r.id
LEFT JOIN asset_totals ON asset_totals.repository_id = r.id
LEFT JOIN layer_totals ON layer_totals.repository_id = r.id
WHERE r.format = 'docker'
  AND r.type = 'hosted'
  AND (sqlc.narg(nexus_instance_id)::bigint IS NULL OR r.nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
  AND (sqlc.narg(search)::text IS NULL OR r.name ILIKE '%' || sqlc.narg(search)::text || '%')
ORDER BY nexus_asset_bytes DESC, docker_logical_bytes DESC, r.id
LIMIT sqlc.arg(limit_count);

-- name: ListImageAnalytics :many
WITH tag_rollup AS (
  SELECT
    ci.id AS image_id,
    count(ct.id)::bigint AS active_tag_count,
    COALESCE(sum(dm.logical_size_bytes), 0)::bigint AS docker_logical_bytes,
    min(ct.first_seen_at) AS oldest_tag_first_seen_at,
    max(ct.last_seen_at) AS newest_tag_last_seen_at
  FROM container_images ci
  LEFT JOIN container_tags ct ON ct.image_id = ci.id AND ct.deleted_at IS NULL
  LEFT JOIN docker_manifests dm ON dm.repository_id = ci.repository_id AND dm.digest = ct.digest
  GROUP BY ci.id
),
tag_manifest_targets AS (
  SELECT
    ct.id AS tag_id,
    ci.id AS image_id,
    ci.repository_id,
    dm.id AS manifest_id,
    dm.metadata
  FROM container_tags ct
  JOIN container_images ci ON ci.id = ct.image_id
  JOIN docker_manifests dm ON dm.repository_id = ci.repository_id AND dm.digest = ct.digest
  WHERE ct.deleted_at IS NULL
),
effective_tag_manifests AS (
  SELECT tag_id, image_id, repository_id, manifest_id
  FROM tag_manifest_targets
  UNION
  SELECT
    tmt.tag_id,
    tmt.image_id,
    tmt.repository_id,
    child.id AS manifest_id
  FROM tag_manifest_targets tmt
  JOIN LATERAL jsonb_array_elements_text(COALESCE(tmt.metadata->'child_manifest_digests', '[]'::jsonb)) child_digest(digest) ON true
  JOIN docker_manifests child ON child.repository_id = tmt.repository_id AND child.digest = child_digest.digest
),
distinct_image_layers AS (
  SELECT
    etm.image_id,
    dl.id AS layer_id,
    dl.size_bytes
  FROM effective_tag_manifests etm
  JOIN docker_manifest_layers dml ON dml.manifest_id = etm.manifest_id
  JOIN docker_layers dl ON dl.id = dml.layer_id
  GROUP BY etm.image_id, dl.id, dl.size_bytes
),
layer_rollup AS (
  SELECT
    image_id,
    COALESCE(sum(size_bytes), 0)::bigint AS docker_unique_layer_bytes
  FROM distinct_image_layers
  GROUP BY image_id
)
SELECT
  ci.id AS image_id,
  r.id AS repository_id,
  r.nexus_instance_id,
  n.name AS nexus_name,
  r.name AS repository_name,
  ci.name AS image_name,
  COALESCE(tag_rollup.active_tag_count, 0)::bigint AS active_tag_count,
  COALESCE(tag_rollup.docker_logical_bytes, 0)::bigint AS docker_logical_bytes,
  COALESCE(layer_rollup.docker_unique_layer_bytes, 0)::bigint AS docker_unique_layer_bytes,
  tag_rollup.oldest_tag_first_seen_at,
  tag_rollup.newest_tag_last_seen_at
FROM container_images ci
JOIN repositories r ON r.id = ci.repository_id
JOIN nexus_instances n ON n.id = r.nexus_instance_id
LEFT JOIN tag_rollup ON tag_rollup.image_id = ci.id
LEFT JOIN layer_rollup ON layer_rollup.image_id = ci.id
WHERE r.format = 'docker'
  AND r.type = 'hosted'
  AND (sqlc.narg(nexus_instance_id)::bigint IS NULL OR r.nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
  AND (sqlc.narg(repository_name)::text IS NULL OR r.name = sqlc.narg(repository_name)::text)
  AND (sqlc.narg(search)::text IS NULL OR ci.name ILIKE '%' || sqlc.narg(search)::text || '%')
ORDER BY docker_logical_bytes DESC, active_tag_count DESC, ci.id
LIMIT sqlc.arg(limit_count);

-- name: ListTagAnalytics :many
WITH tag_manifest_targets AS (
  SELECT
    ct.id AS tag_id,
    ci.repository_id,
    dm.id AS manifest_id,
    dm.metadata
  FROM container_tags ct
  JOIN container_images ci ON ci.id = ct.image_id
  JOIN repositories r ON r.id = ci.repository_id
  JOIN docker_manifests dm ON dm.repository_id = r.id AND dm.digest = ct.digest
  WHERE ct.deleted_at IS NULL
    AND r.format = 'docker'
    AND r.type = 'hosted'
    AND (sqlc.narg(nexus_instance_id)::bigint IS NULL OR r.nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
),
effective_tag_manifests AS (
  SELECT tag_id, repository_id, manifest_id
  FROM tag_manifest_targets
  UNION
  SELECT
    tmt.tag_id,
    tmt.repository_id,
    child.id AS manifest_id
  FROM tag_manifest_targets tmt
  JOIN LATERAL jsonb_array_elements_text(COALESCE(tmt.metadata->'child_manifest_digests', '[]'::jsonb)) child_digest(digest) ON true
  JOIN docker_manifests child ON child.repository_id = tmt.repository_id AND child.digest = child_digest.digest
),
active_tag_layers AS (
  SELECT
    etm.tag_id,
    dl.id AS layer_id,
    dl.size_bytes
  FROM effective_tag_manifests etm
  JOIN docker_manifest_layers dml ON dml.manifest_id = etm.manifest_id
  JOIN docker_layers dl ON dl.id = dml.layer_id
  GROUP BY etm.tag_id, dl.id, dl.size_bytes
),
layer_ref_counts AS (
  SELECT layer_id, count(DISTINCT tag_id)::bigint AS ref_count
  FROM active_tag_layers
  GROUP BY layer_id
),
tag_layer_rollup AS (
  SELECT
    atl.tag_id,
    COALESCE(sum(atl.size_bytes), 0)::bigint AS docker_layer_bytes,
    COALESCE(sum(atl.size_bytes) FILTER (WHERE lrc.ref_count = 1), 0)::bigint AS estimated_reclaimable_bytes,
    COALESCE(sum(atl.size_bytes) FILTER (WHERE lrc.ref_count > 1), 0)::bigint AS shared_layer_bytes
  FROM active_tag_layers atl
  JOIN layer_ref_counts lrc ON lrc.layer_id = atl.layer_id
  GROUP BY atl.tag_id
),
asset_downloads AS (
  SELECT
    repository_id,
    checksum_sha256,
    max(last_downloaded_at) AS last_downloaded_at
  FROM nexus_assets
  WHERE checksum_sha256 IS NOT NULL
    AND checksum_sha256 <> ''
  GROUP BY repository_id, checksum_sha256
)
SELECT
  ct.id AS tag_id,
  ci.id AS image_id,
  r.id AS repository_id,
  r.nexus_instance_id,
  n.name AS nexus_name,
  r.name AS repository_name,
  ci.name AS image_name,
  ct.tag,
  ct.digest,
  ct.media_type,
  ct.size_bytes AS nexus_tag_size_bytes,
  COALESCE(dm.logical_size_bytes, 0)::bigint AS docker_logical_bytes,
  COALESCE(tag_layer_rollup.docker_layer_bytes, 0)::bigint AS docker_layer_bytes,
  COALESCE(tag_layer_rollup.estimated_reclaimable_bytes, 0)::bigint AS estimated_reclaimable_bytes,
  COALESCE(tag_layer_rollup.shared_layer_bytes, 0)::bigint AS shared_layer_bytes,
  ct.first_seen_at,
  ct.last_seen_at,
  ct.deleted_at,
  asset_downloads.last_downloaded_at
FROM container_tags ct
JOIN container_images ci ON ci.id = ct.image_id
JOIN repositories r ON r.id = ci.repository_id
JOIN nexus_instances n ON n.id = r.nexus_instance_id
LEFT JOIN docker_manifests dm ON dm.repository_id = r.id AND dm.digest = ct.digest
LEFT JOIN tag_layer_rollup ON tag_layer_rollup.tag_id = ct.id
LEFT JOIN asset_downloads ON asset_downloads.repository_id = r.id AND asset_downloads.checksum_sha256 = replace(ct.digest, 'sha256:', '')
WHERE r.format = 'docker'
  AND r.type = 'hosted'
  AND ct.deleted_at IS NULL
  AND (sqlc.narg(nexus_instance_id)::bigint IS NULL OR r.nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
  AND (sqlc.narg(repository_name)::text IS NULL OR r.name = sqlc.narg(repository_name)::text)
  AND (sqlc.narg(search)::text IS NULL OR ci.name ILIKE '%' || sqlc.narg(search)::text || '%' OR ct.tag ILIKE '%' || sqlc.narg(search)::text || '%')
  AND (sqlc.narg(older_than)::timestamptz IS NULL OR ct.first_seen_at < sqlc.narg(older_than)::timestamptz)
  AND (sqlc.narg(never_downloaded)::boolean IS NULL OR sqlc.narg(never_downloaded)::boolean = false OR asset_downloads.last_downloaded_at IS NULL)
ORDER BY estimated_reclaimable_bytes DESC, docker_logical_bytes DESC, ct.id
LIMIT sqlc.arg(limit_count);
