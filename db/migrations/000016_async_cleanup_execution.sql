-- +goose Up

ALTER TABLE cleanup_executions
  ADD COLUMN IF NOT EXISTS approved_candidates JSONB NOT NULL DEFAULT '[]',
  ADD COLUMN IF NOT EXISTS approved_candidate_count BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS locked_by TEXT,
  ADD COLUMN IF NOT EXISTS cancel_requested_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS canceled_by TEXT,
  ADD COLUMN IF NOT EXISTS post_delete_reconcile_run_id BIGINT REFERENCES scan_runs (id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS cleanup_execution_events (
  id BIGSERIAL PRIMARY KEY,
  cleanup_execution_id BIGINT NOT NULL REFERENCES cleanup_executions (id) ON DELETE CASCADE,
  event_type TEXT NOT NULL,
  actor TEXT,
  repository_name TEXT,
  image_name TEXT,
  tag TEXT,
  tag_id BIGINT,
  rule_name TEXT,
  component_id TEXT,
  status TEXT,
  message TEXT,
  error_class TEXT,
  error_code TEXT,
  error_message TEXT,
  details JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS cleanup_execution_events_execution_created_idx
  ON cleanup_execution_events (cleanup_execution_id, created_at, id);

CREATE INDEX IF NOT EXISTS cleanup_execution_events_type_created_idx
  ON cleanup_execution_events (event_type, created_at DESC);

CREATE TABLE IF NOT EXISTS nexus_cleanup_locks (
  nexus_instance_id BIGINT PRIMARY KEY REFERENCES nexus_instances (id) ON DELETE CASCADE,
  cleanup_execution_id BIGINT NOT NULL REFERENCES cleanup_executions (id) ON DELETE CASCADE,
  lock_level TEXT NOT NULL DEFAULT 'exclusive',
  acquired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ,
  reason TEXT
);

CREATE INDEX IF NOT EXISTS cleanup_executions_queue_idx
  ON cleanup_executions (status, created_at)
  WHERE status IN ('queued', 'running', 'cancel_requested');

-- +goose Down

DROP TABLE IF EXISTS nexus_cleanup_locks;
DROP TABLE IF EXISTS cleanup_execution_events;

DROP INDEX IF EXISTS cleanup_executions_queue_idx;

ALTER TABLE cleanup_executions
  DROP COLUMN IF EXISTS post_delete_reconcile_run_id,
  DROP COLUMN IF EXISTS canceled_by,
  DROP COLUMN IF EXISTS cancel_requested_at,
  DROP COLUMN IF EXISTS locked_by,
  DROP COLUMN IF EXISTS lease_expires_at,
  DROP COLUMN IF EXISTS started_at,
  DROP COLUMN IF EXISTS approved_candidate_count,
  DROP COLUMN IF EXISTS approved_candidates;
