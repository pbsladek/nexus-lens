-- name: CountScanTasksByStatus :many
SELECT status, count(*)::bigint AS count
FROM scan_tasks
GROUP BY status
ORDER BY status;

-- name: CountScanRunsByStatus24h :many
SELECT status, count(*)::bigint AS count
FROM scan_runs
WHERE started_at >= now() - interval '24 hours'
GROUP BY status
ORDER BY status;

-- name: CountOpenVerificationFindingsBySeverity :many
SELECT severity, count(*)::bigint AS count
FROM verification_findings
WHERE status = 'open'
GROUP BY severity
ORDER BY severity;

-- name: GetOldestPendingTaskAgeSeconds :one
SELECT COALESCE(EXTRACT(EPOCH FROM now() - MIN(available_at)), 0)::bigint AS age_seconds
FROM scan_tasks
WHERE status IN ('pending', 'retryable');

-- name: CountScanTasksByNexusStatus :many
SELECT
  t.nexus_instance_id,
  ni.name AS nexus_name,
  t.status,
  count(*)::bigint AS count
FROM scan_tasks t
JOIN nexus_instances ni ON ni.id = t.nexus_instance_id
GROUP BY t.nexus_instance_id, ni.name, t.status
ORDER BY ni.name, t.status;

-- name: ListOldestPendingTaskAgeByNexus :many
SELECT
  t.nexus_instance_id,
  ni.name AS nexus_name,
  COALESCE(EXTRACT(EPOCH FROM now() - MIN(t.available_at)), 0)::bigint AS age_seconds
FROM scan_tasks t
JOIN nexus_instances ni ON ni.id = t.nexus_instance_id
WHERE t.status IN ('pending', 'retryable')
GROUP BY t.nexus_instance_id, ni.name
ORDER BY age_seconds DESC;

-- name: ListStaleIndexingSchedules :many
SELECT
  s.nexus_instance_id,
  ni.name AS nexus_name,
  s.name AS schedule_name,
  s.scan_type,
  COALESCE(EXTRACT(EPOCH FROM now() - s.next_run_at), 0)::bigint AS age_seconds
FROM indexing_schedules s
JOIN nexus_instances ni ON ni.id = s.nexus_instance_id
WHERE s.enabled
  AND s.next_run_at IS NOT NULL
  AND s.next_run_at < now()
ORDER BY age_seconds DESC, ni.name, s.name
LIMIT 100;

-- name: CountCleanupExecutionsByStatus :many
SELECT status, count(*)::bigint AS count
FROM cleanup_executions
GROUP BY status
ORDER BY status;

-- name: CountCleanupExecutionEvents24h :many
SELECT event_type, status, count(*)::bigint AS count
FROM cleanup_execution_events
WHERE created_at >= now() - interval '24 hours'
GROUP BY event_type, status
ORDER BY event_type, status;

-- name: GetOldestRunningCleanupAgeSeconds :one
SELECT COALESCE(EXTRACT(EPOCH FROM now() - MIN(started_at)), 0)::bigint AS age_seconds
FROM cleanup_executions
WHERE status IN ('running', 'cancel_requested')
  AND started_at IS NOT NULL;
