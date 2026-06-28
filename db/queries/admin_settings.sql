-- name: ListNexusInstances :many
SELECT id, name, base_url, created_at, updated_at
FROM nexus_instances
ORDER BY name, id;

-- name: ListIndexingSchedules :many
SELECT
  s.id,
  s.nexus_instance_id,
  n.name AS nexus_name,
  n.base_url AS nexus_base_url,
  s.scan_type,
  s.name,
  s.enabled,
  s.schedule_expr,
  s.interval_seconds,
  s.timezone,
  s.scope,
  s.lock_conflict_behavior,
  s.next_run_at,
  s.last_run_at,
  s.created_at,
  s.updated_at
FROM indexing_schedules s
JOIN nexus_instances n ON n.id = s.nexus_instance_id
WHERE (sqlc.narg(nexus_instance_id)::bigint IS NULL OR s.nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
ORDER BY n.name, s.name, s.id;

-- name: UpsertNexusRateLimits :one
INSERT INTO nexus_indexing_controls (
  nexus_instance_id,
  asset_list_rps,
  registry_manifest_rps,
  default_rps,
  updated_at
)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (nexus_instance_id) DO UPDATE
SET asset_list_rps = EXCLUDED.asset_list_rps,
    registry_manifest_rps = EXCLUDED.registry_manifest_rps,
    default_rps = EXCLUDED.default_rps,
    updated_at = now()
RETURNING *;
