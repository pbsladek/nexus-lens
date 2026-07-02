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
  approved_candidates,
  approved_candidate_count,
  post_delete_verification_run_id,
  post_delete_reconcile_run_id,
  approved_at,
  started_at,
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
  sqlc.arg(approved_candidates),
  sqlc.arg(approved_candidate_count),
  sqlc.narg(post_delete_verification_run_id),
  sqlc.narg(post_delete_reconcile_run_id),
  sqlc.narg(approved_at),
  sqlc.narg(started_at),
  sqlc.narg(finished_at)
)
RETURNING *;

-- name: ListCleanupExecutions :many
SELECT *
FROM cleanup_executions
WHERE (sqlc.narg(nexus_instance_id)::bigint IS NULL OR nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
ORDER BY created_at DESC
LIMIT sqlc.arg(limit_count);

-- name: GetCleanupExecution :one
SELECT *
FROM cleanup_executions
WHERE id = $1;

-- name: ClaimQueuedCleanupExecution :one
WITH next_execution AS (
  SELECT ce.id
  FROM cleanup_executions ce
  LEFT JOIN nexus_cleanup_locks cl ON cl.nexus_instance_id = ce.nexus_instance_id
  LEFT JOIN nexus_operation_locks ol ON ol.nexus_instance_id = ce.nexus_instance_id
  WHERE ce.status = 'queued'
    AND ce.destructive_enabled
    AND cl.nexus_instance_id IS NULL
    AND ol.nexus_instance_id IS NULL
  ORDER BY ce.created_at, ce.id
  LIMIT 1
  FOR UPDATE OF ce SKIP LOCKED
)
UPDATE cleanup_executions ce
SET status = 'running',
    started_at = COALESCE(ce.started_at, now()),
    locked_by = sqlc.arg(worker_id),
    lease_expires_at = now() + make_interval(secs => sqlc.arg(lease_seconds)::int)
WHERE ce.id = (SELECT id FROM next_execution)
RETURNING *;

-- name: ReclaimExpiredCleanupExecutions :many
UPDATE cleanup_executions
SET status = 'queued',
    locked_by = NULL,
    lease_expires_at = NULL
WHERE status = 'running'
  AND lease_expires_at IS NOT NULL
  AND lease_expires_at < now()
RETURNING *;

-- name: RequestCleanupExecutionCancel :one
UPDATE cleanup_executions
SET status = CASE WHEN status = 'queued' THEN 'canceled' ELSE 'cancel_requested' END,
    cancel_requested_at = now(),
    canceled_by = sqlc.arg(canceled_by),
    finished_at = CASE WHEN status = 'queued' THEN now() ELSE finished_at END,
    locked_by = CASE WHEN status = 'queued' THEN NULL ELSE locked_by END,
    lease_expires_at = CASE WHEN status = 'queued' THEN NULL ELSE lease_expires_at END
WHERE id = sqlc.arg(id)
  AND status IN ('queued', 'running')
RETURNING *;

-- name: FinishCleanupExecution :one
UPDATE cleanup_executions
SET status = sqlc.arg(status),
    simulation_summary = sqlc.arg(simulation_summary),
    audit_log = sqlc.arg(audit_log),
    post_delete_verification_run_id = sqlc.narg(post_delete_verification_run_id),
    post_delete_reconcile_run_id = sqlc.narg(post_delete_reconcile_run_id),
    finished_at = now(),
    locked_by = NULL,
    lease_expires_at = NULL
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: TouchCleanupExecutionLease :one
UPDATE cleanup_executions
SET lease_expires_at = now() + make_interval(secs => sqlc.arg(lease_seconds)::int)
WHERE id = sqlc.arg(id)
  AND status IN ('running', 'cancel_requested')
RETURNING *;

-- name: CreateCleanupExecutionEvent :one
INSERT INTO cleanup_execution_events (
  cleanup_execution_id,
  event_type,
  actor,
  repository_name,
  image_name,
  tag,
  tag_id,
  rule_name,
  component_id,
  status,
  message,
  error_class,
  error_code,
  error_message,
  details
) VALUES (
  sqlc.arg(cleanup_execution_id),
  sqlc.arg(event_type),
  sqlc.narg(actor),
  sqlc.narg(repository_name),
  sqlc.narg(image_name),
  sqlc.narg(tag),
  sqlc.narg(tag_id),
  sqlc.narg(rule_name),
  sqlc.narg(component_id),
  sqlc.narg(status),
  sqlc.narg(message),
  sqlc.narg(error_class),
  sqlc.narg(error_code),
  sqlc.narg(error_message),
  sqlc.arg(details)
)
RETURNING *;

-- name: ListCleanupExecutionEvents :many
SELECT *
FROM cleanup_execution_events
WHERE cleanup_execution_id = sqlc.arg(cleanup_execution_id)
ORDER BY created_at, id
LIMIT sqlc.arg(limit_count);

-- name: AcquireCleanupLock :one
INSERT INTO nexus_cleanup_locks (
  nexus_instance_id,
  cleanup_execution_id,
  lock_level,
  reason
)
VALUES (
  sqlc.arg(nexus_instance_id),
  sqlc.arg(cleanup_execution_id),
  'exclusive',
  sqlc.narg(reason)
)
ON CONFLICT (nexus_instance_id) DO NOTHING
RETURNING *;

-- name: ReleaseCleanupLockByExecution :exec
DELETE FROM nexus_cleanup_locks
WHERE cleanup_execution_id = $1;

-- name: GetCleanupLock :one
SELECT *
FROM nexus_cleanup_locks
WHERE nexus_instance_id = $1;
