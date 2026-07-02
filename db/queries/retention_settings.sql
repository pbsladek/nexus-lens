-- name: GetRetentionSettings :one
SELECT id, interval_seconds, scan_run_days, cleanup_execution_days, verification_finding_days, trend_snapshot_days, updated_by, updated_at
FROM app_retention_settings
WHERE id = true;

-- name: UpsertRetentionSettings :one
INSERT INTO app_retention_settings (
  id,
  interval_seconds,
  scan_run_days,
  cleanup_execution_days,
  verification_finding_days,
  trend_snapshot_days,
  updated_by,
  updated_at
)
VALUES (true, $1, $2, $3, $4, $5, $6, now())
ON CONFLICT (id) DO UPDATE
SET interval_seconds = EXCLUDED.interval_seconds,
    scan_run_days = EXCLUDED.scan_run_days,
    cleanup_execution_days = EXCLUDED.cleanup_execution_days,
    verification_finding_days = EXCLUDED.verification_finding_days,
    trend_snapshot_days = EXCLUDED.trend_snapshot_days,
    updated_by = EXCLUDED.updated_by,
    updated_at = now()
RETURNING id, interval_seconds, scan_run_days, cleanup_execution_days, verification_finding_days, trend_snapshot_days, updated_by, updated_at;
