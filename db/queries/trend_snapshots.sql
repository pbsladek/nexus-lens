-- name: CaptureDailyTrendSnapshots :many
WITH docker_repositories AS (
  SELECT r.id, r.nexus_instance_id
  FROM repositories r
  WHERE r.format = 'docker'
    AND r.type = 'hosted'
    AND (sqlc.narg(nexus_instance_id)::bigint IS NULL OR r.nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
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
),
active_tags AS (
  SELECT
    ct.id AS tag_id,
    ct.digest,
    ct.first_seen_at,
    ct.last_seen_at,
    ci.id AS image_id,
    ci.repository_id,
    dr.nexus_instance_id,
    COALESCE(ctc.pattern_name, 'unclassified') AS pattern_name,
    COALESCE(dm.logical_size_bytes, 0)::numeric AS logical_size_bytes,
    asset_downloads.last_downloaded_at
  FROM container_tags ct
  JOIN container_images ci ON ci.id = ct.image_id
  JOIN docker_repositories dr ON dr.id = ci.repository_id
  LEFT JOIN docker_manifests dm ON dm.repository_id = ci.repository_id AND dm.digest = ct.digest
  LEFT JOIN container_tag_classifications ctc ON ctc.tag_id = ct.id
  LEFT JOIN asset_downloads ON asset_downloads.repository_id = ci.repository_id AND asset_downloads.checksum_sha256 = replace(ct.digest, 'sha256:', '')
  WHERE ct.deleted_at IS NULL
),
tag_manifest_targets AS (
  SELECT
    active_tags.tag_id,
    active_tags.repository_id,
    dm.id AS manifest_id,
    dm.metadata
  FROM active_tags
  JOIN docker_manifests dm ON dm.repository_id = active_tags.repository_id AND dm.digest = active_tags.digest
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
    COALESCE(sum(atl.size_bytes) FILTER (WHERE lrc.ref_count = 1), 0)::numeric AS estimated_reclaimable_bytes
  FROM active_tag_layers atl
  JOIN layer_ref_counts lrc ON lrc.layer_id = atl.layer_id
  GROUP BY atl.tag_id
),
tag_metrics AS (
  SELECT
    active_tags.*,
    COALESCE(tag_layer_rollup.estimated_reclaimable_bytes, 0)::numeric AS estimated_reclaimable_bytes
  FROM active_tags
  LEFT JOIN tag_layer_rollup ON tag_layer_rollup.tag_id = active_tags.tag_id
),
repository_asset_totals AS (
  SELECT dr.nexus_instance_id, na.repository_id, COALESCE(sum(na.size_bytes), 0)::numeric AS metric_value
  FROM docker_repositories dr
  JOIN nexus_assets na ON na.repository_id = dr.id
  GROUP BY dr.nexus_instance_id, na.repository_id
),
repository_layer_totals AS (
  SELECT dr.nexus_instance_id, dl.repository_id, COALESCE(sum(dl.size_bytes), 0)::numeric AS metric_value
  FROM docker_repositories dr
  JOIN docker_layers dl ON dl.repository_id = dr.id
  GROUP BY dr.nexus_instance_id, dl.repository_id
),
nexus_metrics AS (
  SELECT nexus_instance_id, NULL::bigint AS repository_id, NULL::bigint AS image_id, 'repository_count'::text AS metric_name, count(id)::numeric AS metric_value, '{}'::jsonb AS dimensions
  FROM docker_repositories
  GROUP BY nexus_instance_id
  UNION ALL
  SELECT dr.nexus_instance_id, NULL::bigint, NULL::bigint, 'image_count', count(ci.id)::numeric, '{}'::jsonb
  FROM docker_repositories dr
  JOIN container_images ci ON ci.repository_id = dr.id
  GROUP BY dr.nexus_instance_id
  UNION ALL
  SELECT nexus_instance_id, NULL::bigint, NULL::bigint, 'active_tag_count', count(tag_id)::numeric, '{}'::jsonb
  FROM tag_metrics
  GROUP BY nexus_instance_id
  UNION ALL
  SELECT nexus_instance_id, NULL::bigint, NULL::bigint, 'docker_logical_bytes', COALESCE(sum(logical_size_bytes), 0)::numeric, '{}'::jsonb
  FROM tag_metrics
  GROUP BY nexus_instance_id
  UNION ALL
  SELECT nexus_instance_id, NULL::bigint, NULL::bigint, 'estimated_reclaimable_bytes', COALESCE(sum(estimated_reclaimable_bytes), 0)::numeric, '{}'::jsonb
  FROM tag_metrics
  GROUP BY nexus_instance_id
  UNION ALL
  SELECT nexus_instance_id, NULL::bigint, NULL::bigint, 'new_tag_count_7d', count(tag_id) FILTER (WHERE first_seen_at >= (sqlc.arg(snapshot_date)::date - interval '7 days'))::numeric, '{}'::jsonb
  FROM tag_metrics
  GROUP BY nexus_instance_id
  UNION ALL
  SELECT nexus_instance_id, NULL::bigint, NULL::bigint, 'old_tag_count_30d', count(tag_id) FILTER (WHERE first_seen_at < (sqlc.arg(snapshot_date)::date - interval '30 days'))::numeric, '{}'::jsonb
  FROM tag_metrics
  GROUP BY nexus_instance_id
  UNION ALL
  SELECT nexus_instance_id, NULL::bigint, NULL::bigint, 'never_downloaded_count', count(tag_id) FILTER (WHERE last_downloaded_at IS NULL)::numeric, '{}'::jsonb
  FROM tag_metrics
  GROUP BY nexus_instance_id
  UNION ALL
  SELECT nexus_instance_id, NULL::bigint, NULL::bigint, 'nexus_asset_bytes', COALESCE(sum(metric_value), 0)::numeric, '{}'::jsonb
  FROM repository_asset_totals
  GROUP BY nexus_instance_id
  UNION ALL
  SELECT nexus_instance_id, NULL::bigint, NULL::bigint, 'docker_unique_layer_bytes', COALESCE(sum(metric_value), 0)::numeric, '{}'::jsonb
  FROM repository_layer_totals
  GROUP BY nexus_instance_id
),
repository_metrics AS (
  SELECT dr.nexus_instance_id, dr.id AS repository_id, NULL::bigint AS image_id, 'image_count'::text AS metric_name, count(ci.id)::numeric AS metric_value, '{}'::jsonb AS dimensions
  FROM docker_repositories dr
  LEFT JOIN container_images ci ON ci.repository_id = dr.id
  GROUP BY dr.nexus_instance_id, dr.id
  UNION ALL
  SELECT nexus_instance_id, repository_id, NULL::bigint, 'active_tag_count', count(tag_id)::numeric, '{}'::jsonb
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id
  UNION ALL
  SELECT nexus_instance_id, repository_id, NULL::bigint, 'docker_logical_bytes', COALESCE(sum(logical_size_bytes), 0)::numeric, '{}'::jsonb
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id
  UNION ALL
  SELECT nexus_instance_id, repository_id, NULL::bigint, 'estimated_reclaimable_bytes', COALESCE(sum(estimated_reclaimable_bytes), 0)::numeric, '{}'::jsonb
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id
  UNION ALL
  SELECT nexus_instance_id, repository_id, NULL::bigint, 'new_tag_count_7d', count(tag_id) FILTER (WHERE first_seen_at >= (sqlc.arg(snapshot_date)::date - interval '7 days'))::numeric, '{}'::jsonb
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id
  UNION ALL
  SELECT nexus_instance_id, repository_id, NULL::bigint, 'old_tag_count_30d', count(tag_id) FILTER (WHERE first_seen_at < (sqlc.arg(snapshot_date)::date - interval '30 days'))::numeric, '{}'::jsonb
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id
  UNION ALL
  SELECT nexus_instance_id, repository_id, NULL::bigint, 'never_downloaded_count', count(tag_id) FILTER (WHERE last_downloaded_at IS NULL)::numeric, '{}'::jsonb
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id
  UNION ALL
  SELECT nexus_instance_id, repository_id, NULL::bigint, 'nexus_asset_bytes', metric_value, '{}'::jsonb
  FROM repository_asset_totals
  UNION ALL
  SELECT nexus_instance_id, repository_id, NULL::bigint, 'docker_unique_layer_bytes', metric_value, '{}'::jsonb
  FROM repository_layer_totals
),
image_metrics AS (
  SELECT nexus_instance_id, repository_id, image_id, 'active_tag_count'::text AS metric_name, count(tag_id)::numeric AS metric_value, '{}'::jsonb AS dimensions
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id, image_id
  UNION ALL
  SELECT nexus_instance_id, repository_id, image_id, 'docker_logical_bytes', COALESCE(sum(logical_size_bytes), 0)::numeric, '{}'::jsonb
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id, image_id
  UNION ALL
  SELECT nexus_instance_id, repository_id, image_id, 'estimated_reclaimable_bytes', COALESCE(sum(estimated_reclaimable_bytes), 0)::numeric, '{}'::jsonb
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id, image_id
  UNION ALL
  SELECT nexus_instance_id, repository_id, image_id, 'new_tag_count_7d', count(tag_id) FILTER (WHERE first_seen_at >= (sqlc.arg(snapshot_date)::date - interval '7 days'))::numeric, '{}'::jsonb
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id, image_id
  UNION ALL
  SELECT nexus_instance_id, repository_id, image_id, 'old_tag_count_30d', count(tag_id) FILTER (WHERE first_seen_at < (sqlc.arg(snapshot_date)::date - interval '30 days'))::numeric, '{}'::jsonb
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id, image_id
  UNION ALL
  SELECT nexus_instance_id, repository_id, image_id, 'never_downloaded_count', count(tag_id) FILTER (WHERE last_downloaded_at IS NULL)::numeric, '{}'::jsonb
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id, image_id
),
pattern_metrics AS (
  SELECT
    nexus_instance_id,
    repository_id,
    NULL::bigint AS image_id,
    'tag_pattern_count'::text AS metric_name,
    count(tag_id)::numeric AS metric_value,
    jsonb_build_object('pattern', pattern_name) AS dimensions
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id, pattern_name
  UNION ALL
  SELECT
    nexus_instance_id,
    repository_id,
    NULL::bigint,
    'tag_pattern_logical_bytes',
    COALESCE(sum(logical_size_bytes), 0)::numeric,
    jsonb_build_object('pattern', pattern_name)
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id, pattern_name
  UNION ALL
  SELECT
    nexus_instance_id,
    repository_id,
    NULL::bigint,
    'tag_pattern_reclaimable_bytes',
    COALESCE(sum(estimated_reclaimable_bytes), 0)::numeric,
    jsonb_build_object('pattern', pattern_name)
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id, pattern_name
  UNION ALL
  SELECT
    nexus_instance_id,
    repository_id,
    image_id,
    'tag_pattern_count',
    count(tag_id)::numeric,
    jsonb_build_object('pattern', pattern_name)
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id, image_id, pattern_name
  UNION ALL
  SELECT
    nexus_instance_id,
    repository_id,
    image_id,
    'tag_pattern_logical_bytes',
    COALESCE(sum(logical_size_bytes), 0)::numeric,
    jsonb_build_object('pattern', pattern_name)
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id, image_id, pattern_name
  UNION ALL
  SELECT
    nexus_instance_id,
    repository_id,
    image_id,
    'tag_pattern_reclaimable_bytes',
    COALESCE(sum(estimated_reclaimable_bytes), 0)::numeric,
    jsonb_build_object('pattern', pattern_name)
  FROM tag_metrics
  GROUP BY nexus_instance_id, repository_id, image_id, pattern_name
),
all_metrics AS (
  SELECT * FROM nexus_metrics
  UNION ALL
  SELECT * FROM repository_metrics
  UNION ALL
  SELECT * FROM image_metrics
  UNION ALL
  SELECT * FROM pattern_metrics
)
INSERT INTO trend_snapshots (
  snapshot_date,
  captured_at,
  nexus_instance_id,
  repository_id,
  image_id,
  metric_name,
  metric_value,
  dimensions
)
SELECT
  sqlc.arg(snapshot_date)::date,
  now(),
  nexus_instance_id,
  repository_id,
  image_id,
  metric_name,
  metric_value,
  dimensions
FROM all_metrics
ON CONFLICT (snapshot_date, nexus_instance_id, repository_id, image_id, metric_name, dimensions)
DO UPDATE SET
  captured_at = EXCLUDED.captured_at,
  metric_value = EXCLUDED.metric_value
RETURNING *;

-- name: ListTrendSeries :many
WITH days AS (
  SELECT generate_series(sqlc.arg(start_date)::date, sqlc.arg(end_date)::date, interval '1 day')::date AS snapshot_date
)
SELECT
  days.snapshot_date,
  ts.captured_at,
  ts.metric_value,
  (ts.id IS NULL)::boolean AS missing
FROM days
LEFT JOIN trend_snapshots ts
  ON ts.snapshot_date = days.snapshot_date
  AND ts.nexus_instance_id = sqlc.arg(nexus_instance_id)::bigint
  AND ts.repository_id IS NOT DISTINCT FROM sqlc.narg(repository_id)::bigint
  AND ts.image_id IS NOT DISTINCT FROM sqlc.narg(image_id)::bigint
  AND ts.metric_name = sqlc.arg(metric_name)::text
  AND ts.dimensions = COALESCE(sqlc.narg(dimensions)::jsonb, '{}'::jsonb)
ORDER BY days.snapshot_date;

-- name: ListTrendTopMovers :many
WITH end_points AS (
  SELECT *
  FROM trend_snapshots
  WHERE snapshot_date = sqlc.arg(end_date)::date
    AND nexus_instance_id = sqlc.arg(nexus_instance_id)::bigint
    AND metric_name = sqlc.arg(metric_name)::text
    AND dimensions = COALESCE(sqlc.narg(dimensions)::jsonb, '{}'::jsonb)
    AND (sqlc.narg(repository_id)::bigint IS NULL OR repository_id = sqlc.narg(repository_id)::bigint)
    AND (sqlc.narg(image_id)::bigint IS NULL OR image_id = sqlc.narg(image_id)::bigint)
    AND repository_id IS NOT NULL
),
start_points AS (
  SELECT *
  FROM trend_snapshots
  WHERE snapshot_date = sqlc.arg(start_date)::date
    AND nexus_instance_id = sqlc.arg(nexus_instance_id)::bigint
    AND metric_name = sqlc.arg(metric_name)::text
    AND dimensions = COALESCE(sqlc.narg(dimensions)::jsonb, '{}'::jsonb)
)
SELECT
  ep.repository_id,
  r.name AS repository_name,
  ep.image_id,
  ci.name AS image_name,
  COALESCE(sp.metric_value, 0)::numeric AS start_value,
  ep.metric_value AS end_value,
  (ep.metric_value - COALESCE(sp.metric_value, 0))::numeric AS delta_value,
  CASE
    WHEN COALESCE(sp.metric_value, 0) = 0 THEN NULL::numeric
    ELSE ((ep.metric_value - sp.metric_value) / NULLIF(sp.metric_value, 0)) * 100
  END::numeric AS delta_percent
FROM end_points ep
LEFT JOIN start_points sp
  ON sp.repository_id IS NOT DISTINCT FROM ep.repository_id
  AND sp.image_id IS NOT DISTINCT FROM ep.image_id
LEFT JOIN repositories r ON r.id = ep.repository_id
LEFT JOIN container_images ci ON ci.id = ep.image_id
ORDER BY abs(ep.metric_value - COALESCE(sp.metric_value, 0)) DESC, ep.repository_id, ep.image_id
LIMIT sqlc.arg(limit_count);

-- name: ListTrendMetricNames :many
SELECT DISTINCT metric_name
FROM trend_snapshots
WHERE (sqlc.narg(nexus_instance_id)::bigint IS NULL OR nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
ORDER BY metric_name;

-- name: DeleteTrendSnapshotsBefore :execrows
DELETE FROM trend_snapshots
WHERE snapshot_date < sqlc.arg(cutoff_date)::date;
