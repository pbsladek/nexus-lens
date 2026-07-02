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
