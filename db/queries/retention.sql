-- name: DeleteTerminalScanRunsBefore :execrows
DELETE FROM scan_runs
WHERE status IN ('succeeded', 'failed', 'canceled', 'skipped_blocked')
  AND finished_at IS NOT NULL
  AND finished_at < sqlc.arg(cutoff);

-- name: DeleteCleanupExecutionsBefore :execrows
DELETE FROM cleanup_executions
WHERE status NOT IN ('queued', 'running', 'cancel_requested')
  AND finished_at IS NOT NULL
  AND finished_at < sqlc.arg(cutoff);

-- name: DeleteClosedVerificationFindingsBefore :execrows
DELETE FROM verification_findings
WHERE status <> 'open'
  AND COALESCE(resolved_at, dismissed_at, last_seen_at, created_at) < sqlc.arg(cutoff);
