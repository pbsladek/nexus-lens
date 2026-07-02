-- name: ListTagPatterns :many
SELECT id, name, display_name, description, tag_regex, priority, enabled, built_in, created_at, updated_at
FROM tag_patterns
ORDER BY priority, id
LIMIT sqlc.arg(limit_count);

-- name: UpsertTagPattern :one
INSERT INTO tag_patterns (
  name,
  display_name,
  description,
  tag_regex,
  priority,
  enabled,
  built_in,
  updated_at
)
VALUES (
  sqlc.arg(name),
  sqlc.arg(display_name),
  sqlc.arg(description),
  sqlc.arg(tag_regex),
  sqlc.arg(priority),
  sqlc.arg(enabled),
  sqlc.arg(built_in),
  now()
)
ON CONFLICT (name) DO UPDATE
SET display_name = EXCLUDED.display_name,
    description = EXCLUDED.description,
    tag_regex = EXCLUDED.tag_regex,
    priority = EXCLUDED.priority,
    enabled = EXCLUDED.enabled,
    updated_at = now()
RETURNING id, name, display_name, description, tag_regex, priority, enabled, built_in, created_at, updated_at;

-- name: ClassifyActiveTags :one
WITH candidates AS (
  SELECT
    ct.id AS tag_id,
    ct.tag AS tag_value,
    matched.pattern_id,
    COALESCE(matched.pattern_name, 'unclassified') AS pattern_name
  FROM container_tags ct
  JOIN container_images ci ON ci.id = ct.image_id
  JOIN repositories r ON r.id = ci.repository_id
  LEFT JOIN LATERAL (
    SELECT
      tp.id AS pattern_id,
      tp.name AS pattern_name
    FROM tag_patterns tp
    WHERE tp.enabled
      AND ct.tag ~* tp.tag_regex
    ORDER BY tp.priority, tp.id
    LIMIT 1
  ) matched ON true
  WHERE ct.deleted_at IS NULL
    AND (sqlc.narg(nexus_instance_id)::bigint IS NULL OR r.nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
    AND (sqlc.narg(repository_id)::bigint IS NULL OR r.id = sqlc.narg(repository_id)::bigint)
    AND (sqlc.narg(repository_name)::text IS NULL OR r.name = sqlc.narg(repository_name)::text)
),
upserted AS (
  INSERT INTO container_tag_classifications (
    tag_id,
    pattern_id,
    pattern_name,
    tag_value,
    classified_at,
    metadata
  )
  SELECT
    tag_id,
    pattern_id,
    pattern_name,
    tag_value,
    now(),
    '{}'::jsonb
  FROM candidates
  ON CONFLICT (tag_id) DO UPDATE
  SET pattern_id = EXCLUDED.pattern_id,
      pattern_name = EXCLUDED.pattern_name,
      tag_value = EXCLUDED.tag_value,
      classified_at = now(),
      metadata = EXCLUDED.metadata
  RETURNING 1
)
SELECT count(*)::bigint AS classified_count
FROM upserted;

-- name: ListTagPatternAnalytics :many
WITH scoped_repositories AS (
  SELECT r.id, r.nexus_instance_id, r.name
  FROM repositories r
  WHERE (sqlc.narg(nexus_instance_id)::bigint IS NULL OR r.nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
    AND (sqlc.narg(repository_name)::text IS NULL OR r.name = sqlc.narg(repository_name)::text)
),
asset_downloads AS (
  SELECT
    na.repository_id,
    na.checksum_sha256,
    max(na.last_downloaded_at) AS last_downloaded_at
  FROM nexus_assets na
  JOIN scoped_repositories sr ON sr.id = na.repository_id
  WHERE na.checksum_sha256 IS NOT NULL
    AND na.checksum_sha256 <> ''
  GROUP BY na.repository_id, na.checksum_sha256
),
active_tags AS (
  SELECT
    ct.id AS tag_id,
    ci.repository_id,
    sr.nexus_instance_id,
    COALESCE(ctc.pattern_name, 'unclassified') AS pattern_name,
    dm.logical_size_bytes,
    asset_downloads.last_downloaded_at,
    ct.first_seen_at
  FROM container_tags ct
  JOIN container_images ci ON ci.id = ct.image_id
  JOIN scoped_repositories sr ON sr.id = ci.repository_id
  LEFT JOIN container_tag_classifications ctc ON ctc.tag_id = ct.id
  LEFT JOIN docker_manifests dm ON dm.repository_id = ci.repository_id AND dm.digest = ct.digest
  LEFT JOIN asset_downloads ON asset_downloads.repository_id = sr.id AND asset_downloads.checksum_sha256 = replace(ct.digest, 'sha256:', '')
  WHERE ct.deleted_at IS NULL
    AND (sqlc.narg(older_than)::timestamptz IS NULL OR ct.first_seen_at < sqlc.narg(older_than)::timestamptz)
    AND (sqlc.narg(never_downloaded)::boolean IS NULL OR sqlc.narg(never_downloaded)::boolean = false OR asset_downloads.last_downloaded_at IS NULL)
)
SELECT
  pattern_name,
  count(tag_id)::bigint AS active_tag_count,
  COALESCE(sum(logical_size_bytes), 0)::bigint AS docker_logical_bytes,
  min(first_seen_at) AS oldest_tag_first_seen_at,
  count(tag_id) FILTER (WHERE last_downloaded_at IS NULL)::bigint AS never_downloaded_count
FROM active_tags
GROUP BY pattern_name
ORDER BY active_tag_count DESC, docker_logical_bytes DESC, pattern_name
LIMIT sqlc.arg(limit_count);

-- name: ListTagPatternExamples :many
SELECT
  COALESCE(ctc.pattern_name, 'unclassified') AS pattern_name,
  r.name AS repository_name,
  ci.name AS image_name,
  ct.tag,
  ct.first_seen_at,
  ct.last_seen_at
FROM container_tags ct
JOIN container_images ci ON ci.id = ct.image_id
JOIN repositories r ON r.id = ci.repository_id
LEFT JOIN container_tag_classifications ctc ON ctc.tag_id = ct.id
WHERE ct.deleted_at IS NULL
  AND (sqlc.narg(nexus_instance_id)::bigint IS NULL OR r.nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
  AND (sqlc.narg(repository_name)::text IS NULL OR r.name = sqlc.narg(repository_name)::text)
  AND (sqlc.narg(pattern_name)::text IS NULL OR COALESCE(ctc.pattern_name, 'unclassified') = sqlc.narg(pattern_name)::text)
ORDER BY ct.last_seen_at DESC, ct.id
LIMIT sqlc.arg(limit_count);

-- name: ListTagPatternSuggestions :many
WITH unclassified AS (
  SELECT
    r.name AS repository_name,
    ci.name AS image_name,
    ct.tag,
    regexp_replace(
      regexp_replace(
        regexp_replace(ct.tag, '[A-Za-z]+', 'A', 'g'),
        '[0-9]+', '0', 'g'
      ),
      '[^A0._/-]+', 'x', 'g'
    ) AS tag_shape
  FROM container_tags ct
  JOIN container_images ci ON ci.id = ct.image_id
  JOIN repositories r ON r.id = ci.repository_id
  LEFT JOIN container_tag_classifications ctc ON ctc.tag_id = ct.id
  WHERE ct.deleted_at IS NULL
    AND COALESCE(ctc.pattern_name, 'unclassified') = 'unclassified'
    AND (sqlc.narg(nexus_instance_id)::bigint IS NULL OR r.nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
    AND (sqlc.narg(repository_name)::text IS NULL OR r.name = sqlc.narg(repository_name)::text)
),
shape_rollup AS (
  SELECT
    tag_shape,
    count(*)::bigint AS tag_count,
    array_agg(tag ORDER BY tag) AS example_tags
  FROM unclassified
  GROUP BY tag_shape
)
SELECT
  tag_shape,
  tag_count,
  example_tags[1:5]::text[] AS example_tags,
  '^' ||
    replace(
      replace(
        replace(
          replace(tag_shape, '/', '[/]'),
          '.', '[.]'
        ),
        'A', '[A-Za-z]+'
      ),
      '0', '[0-9]+'
    ) ||
  '$' AS suggested_regex
FROM shape_rollup
ORDER BY tag_count DESC, tag_shape
LIMIT sqlc.arg(limit_count);

-- name: ListTagPatternGrowth :many
WITH scoped_repositories AS (
  SELECT r.id, r.nexus_instance_id, r.name
  FROM repositories r
  WHERE (sqlc.narg(nexus_instance_id)::bigint IS NULL OR r.nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
    AND (sqlc.narg(repository_name)::text IS NULL OR r.name = sqlc.narg(repository_name)::text)
),
asset_downloads AS (
  SELECT
    na.repository_id,
    na.checksum_sha256,
    max(na.last_downloaded_at) AS last_downloaded_at
  FROM nexus_assets na
  JOIN scoped_repositories sr ON sr.id = na.repository_id
  WHERE na.checksum_sha256 IS NOT NULL
    AND na.checksum_sha256 <> ''
  GROUP BY na.repository_id, na.checksum_sha256
),
active_tags AS (
  SELECT
    sr.nexus_instance_id,
    sr.name AS repository_name,
    CASE
      WHEN position('/' in ci.name) > 0 THEN split_part(ci.name, '/', 1)
      ELSE '(root)'
    END AS image_namespace,
    COALESCE(ctc.pattern_name, 'unclassified') AS pattern_name,
    ct.id AS tag_id,
    ct.first_seen_at,
    COALESCE(dm.logical_size_bytes, 0)::bigint AS logical_size_bytes,
    asset_downloads.last_downloaded_at
  FROM container_tags ct
  JOIN container_images ci ON ci.id = ct.image_id
  JOIN scoped_repositories sr ON sr.id = ci.repository_id
  LEFT JOIN container_tag_classifications ctc ON ctc.tag_id = ct.id
  LEFT JOIN docker_manifests dm ON dm.repository_id = ci.repository_id AND dm.digest = ct.digest
  LEFT JOIN asset_downloads ON asset_downloads.repository_id = sr.id AND asset_downloads.checksum_sha256 = replace(ct.digest, 'sha256:', '')
  WHERE ct.deleted_at IS NULL
    AND (sqlc.narg(pattern_name)::text IS NULL OR COALESCE(ctc.pattern_name, 'unclassified') = sqlc.narg(pattern_name)::text)
    AND (sqlc.narg(never_downloaded)::boolean IS NULL OR sqlc.narg(never_downloaded)::boolean = false OR asset_downloads.last_downloaded_at IS NULL)
)
SELECT
  repository_name,
  image_namespace,
  pattern_name,
  count(tag_id)::bigint AS active_tag_count,
  COALESCE(sum(logical_size_bytes), 0)::bigint AS docker_logical_bytes,
  count(tag_id) FILTER (WHERE first_seen_at >= sqlc.arg(growth_since)::timestamptz)::bigint AS growth_tag_count,
  COALESCE(sum(logical_size_bytes) FILTER (WHERE first_seen_at >= sqlc.arg(growth_since)::timestamptz), 0)::bigint AS growth_logical_bytes,
  count(tag_id) FILTER (WHERE last_downloaded_at IS NULL)::bigint AS never_downloaded_count,
  min(first_seen_at) AS oldest_tag_first_seen_at
FROM active_tags
GROUP BY repository_name, image_namespace, pattern_name
ORDER BY growth_logical_bytes DESC, growth_tag_count DESC, docker_logical_bytes DESC, repository_name, image_namespace, pattern_name
LIMIT sqlc.arg(limit_count);
