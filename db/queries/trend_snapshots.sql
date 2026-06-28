-- name: CaptureDailyTrendSnapshots :many
WITH docker_repositories AS (
  SELECT r.id, r.nexus_instance_id
  FROM repositories r
  WHERE r.format = 'docker'
    AND r.type = 'hosted'
    AND (sqlc.narg(nexus_instance_id)::bigint IS NULL OR r.nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
),
active_tags AS (
  SELECT
    ct.id AS tag_id,
    ci.id AS image_id,
    ci.repository_id,
    dr.nexus_instance_id,
    COALESCE(ctc.pattern_name, 'unclassified') AS pattern_name,
    COALESCE(dm.logical_size_bytes, 0)::numeric AS logical_size_bytes
  FROM container_tags ct
  JOIN container_images ci ON ci.id = ct.image_id
  JOIN docker_repositories dr ON dr.id = ci.repository_id
  LEFT JOIN docker_manifests dm ON dm.repository_id = ci.repository_id AND dm.digest = ct.digest
  LEFT JOIN container_tag_classifications ctc ON ctc.tag_id = ct.id
  WHERE ct.deleted_at IS NULL
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
  FROM active_tags
  GROUP BY nexus_instance_id
  UNION ALL
  SELECT nexus_instance_id, NULL::bigint, NULL::bigint, 'docker_logical_bytes', COALESCE(sum(logical_size_bytes), 0)::numeric, '{}'::jsonb
  FROM active_tags
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
  FROM active_tags
  GROUP BY nexus_instance_id, repository_id
  UNION ALL
  SELECT nexus_instance_id, repository_id, NULL::bigint, 'docker_logical_bytes', COALESCE(sum(logical_size_bytes), 0)::numeric, '{}'::jsonb
  FROM active_tags
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
  FROM active_tags
  GROUP BY nexus_instance_id, repository_id, image_id
  UNION ALL
  SELECT nexus_instance_id, repository_id, image_id, 'docker_logical_bytes', COALESCE(sum(logical_size_bytes), 0)::numeric, '{}'::jsonb
  FROM active_tags
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
  FROM active_tags
  GROUP BY nexus_instance_id, repository_id, pattern_name
  UNION ALL
  SELECT
    nexus_instance_id,
    repository_id,
    NULL::bigint,
    'tag_pattern_logical_bytes',
    COALESCE(sum(logical_size_bytes), 0)::numeric,
    jsonb_build_object('pattern', pattern_name)
  FROM active_tags
  GROUP BY nexus_instance_id, repository_id, pattern_name
  UNION ALL
  SELECT
    nexus_instance_id,
    repository_id,
    image_id,
    'tag_pattern_count',
    count(tag_id)::numeric,
    jsonb_build_object('pattern', pattern_name)
  FROM active_tags
  GROUP BY nexus_instance_id, repository_id, image_id, pattern_name
  UNION ALL
  SELECT
    nexus_instance_id,
    repository_id,
    image_id,
    'tag_pattern_logical_bytes',
    COALESCE(sum(logical_size_bytes), 0)::numeric,
    jsonb_build_object('pattern', pattern_name)
  FROM active_tags
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

-- name: ListTrendMetricNames :many
SELECT DISTINCT metric_name
FROM trend_snapshots
WHERE (sqlc.narg(nexus_instance_id)::bigint IS NULL OR nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
ORDER BY metric_name;

-- name: DeleteTrendSnapshotsBefore :execrows
DELETE FROM trend_snapshots
WHERE snapshot_date < sqlc.arg(cutoff_date)::date;
