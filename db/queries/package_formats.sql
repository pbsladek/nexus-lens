-- name: ListPackageFormatRepositories :many
WITH filtered_repositories AS (
  SELECT
    r.id,
    r.nexus_instance_id,
    n.name AS nexus_name,
    r.name,
    r.format,
    r.type,
    r.last_seen_at
  FROM repositories r
  JOIN nexus_instances n ON n.id = r.nexus_instance_id
  WHERE (sqlc.narg(nexus_instance_id)::bigint IS NULL OR r.nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
    AND (sqlc.narg(search)::text IS NULL OR r.name ILIKE '%' || sqlc.narg(search)::text || '%' OR r.format ILIKE '%' || sqlc.narg(search)::text || '%')
),
asset_rollup AS (
  SELECT
    na.repository_id,
    count(*)::bigint AS asset_count,
    COALESCE(sum(na.size_bytes), 0)::bigint AS asset_bytes,
    max(na.last_seen_at) AS last_asset_seen_at
  FROM nexus_assets na
  JOIN filtered_repositories fr ON fr.id = na.repository_id
  GROUP BY na.repository_id
)
SELECT
  fr.id AS repository_id,
  fr.nexus_instance_id,
  fr.nexus_name,
  fr.name AS repository_name,
  fr.format,
  fr.type,
  CASE
    WHEN fr.type <> 'hosted' THEN 'skipped'
    WHEN fr.format = 'docker' THEN 'indexed'
    WHEN fr.format IN ('pypi', 'npm', 'go') THEN 'asset_indexed'
    ELSE 'unsupported'
  END AS support_status,
  CASE
    WHEN fr.type <> 'hosted' THEN 'only hosted repositories are indexed in v1'
    WHEN fr.format = 'docker' THEN 'docker/oci analytics are fully indexed'
    WHEN fr.format IN ('pypi', 'npm', 'go') THEN 'hosted package assets are indexed from the Nexus Assets API into generic artifact rows'
    ELSE 'repository format is not supported yet'
  END AS support_reason,
  COALESCE(asset_rollup.asset_count, 0)::bigint AS asset_count,
  COALESCE(asset_rollup.asset_bytes, 0)::bigint AS asset_bytes,
  fr.last_seen_at,
  asset_rollup.last_asset_seen_at
FROM filtered_repositories fr
LEFT JOIN asset_rollup ON asset_rollup.repository_id = fr.id
ORDER BY
  CASE
    WHEN fr.format = 'docker' AND fr.type = 'hosted' THEN 0
    WHEN fr.type = 'hosted' AND fr.format IN ('pypi', 'npm', 'go') THEN 1
    ELSE 2
  END,
  fr.format,
  fr.name
LIMIT sqlc.arg(limit_count);
