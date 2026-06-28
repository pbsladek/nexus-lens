-- +goose Up

CREATE TABLE IF NOT EXISTS indexing_schedules (
  id BIGSERIAL PRIMARY KEY,
  nexus_instance_id BIGINT NOT NULL REFERENCES nexus_instances (id) ON DELETE CASCADE,
  scan_type TEXT NOT NULL,
  name TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  schedule_expr TEXT NOT NULL,
  interval_seconds INT NOT NULL,
  timezone TEXT NOT NULL DEFAULT 'UTC',
  scope JSONB NOT NULL DEFAULT '{}',
  lock_conflict_behavior TEXT NOT NULL DEFAULT 'skip_blocked',
  next_run_at TIMESTAMPTZ,
  last_run_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (nexus_instance_id, name)
);

CREATE INDEX IF NOT EXISTS indexing_schedules_due_idx
  ON indexing_schedules (enabled, next_run_at)
  WHERE enabled;

CREATE INDEX IF NOT EXISTS indexing_schedules_nexus_idx
  ON indexing_schedules (nexus_instance_id, enabled, name);

-- +goose Down

DROP TABLE IF EXISTS indexing_schedules;
