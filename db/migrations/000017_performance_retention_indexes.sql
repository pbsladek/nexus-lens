-- +goose Up

CREATE INDEX IF NOT EXISTS scan_runs_recent_idx
  ON scan_runs (started_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS scan_runs_terminal_finished_idx
  ON scan_runs (finished_at, id)
  WHERE status IN ('succeeded', 'failed', 'canceled', 'skipped_blocked')
    AND finished_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS scan_tasks_recent_idx
  ON scan_tasks (created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS scan_tasks_run_recent_idx
  ON scan_tasks (scan_run_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS cleanup_executions_terminal_finished_idx
  ON cleanup_executions (finished_at, id)
  WHERE status NOT IN ('queued', 'running', 'cancel_requested')
    AND finished_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS cleanup_executions_running_lease_idx
  ON cleanup_executions (lease_expires_at, id)
  WHERE status = 'running'
    AND lease_expires_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS verification_findings_closed_retention_idx
  ON verification_findings ((COALESCE(resolved_at, dismissed_at, last_seen_at, created_at)), id)
  WHERE status <> 'open';

-- +goose Down

DROP INDEX IF EXISTS verification_findings_closed_retention_idx;
DROP INDEX IF EXISTS cleanup_executions_running_lease_idx;
DROP INDEX IF EXISTS cleanup_executions_terminal_finished_idx;
DROP INDEX IF EXISTS scan_tasks_run_recent_idx;
DROP INDEX IF EXISTS scan_tasks_recent_idx;
DROP INDEX IF EXISTS scan_runs_terminal_finished_idx;
DROP INDEX IF EXISTS scan_runs_recent_idx;
