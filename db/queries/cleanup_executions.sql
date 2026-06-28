-- name: CreateCleanupExecution :one
INSERT INTO cleanup_executions (
  nexus_instance_id,
  repository_name,
  policy_name,
  policy_yaml,
  status,
  requested_by,
  approved_by,
  approval_text,
  destructive_enabled,
  simulation_summary,
  audit_log,
  post_delete_verification_run_id,
  approved_at,
  finished_at
) VALUES (
  sqlc.arg(nexus_instance_id),
  sqlc.narg(repository_name),
  sqlc.arg(policy_name),
  sqlc.arg(policy_yaml),
  sqlc.arg(status),
  sqlc.arg(requested_by),
  sqlc.narg(approved_by),
  sqlc.arg(approval_text),
  sqlc.arg(destructive_enabled),
  sqlc.arg(simulation_summary),
  sqlc.arg(audit_log),
  sqlc.narg(post_delete_verification_run_id),
  sqlc.narg(approved_at),
  sqlc.narg(finished_at)
)
RETURNING *;

-- name: ListCleanupExecutions :many
SELECT *
FROM cleanup_executions
WHERE (sqlc.narg(nexus_instance_id)::bigint IS NULL OR nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
ORDER BY created_at DESC
LIMIT sqlc.arg(limit_count);
