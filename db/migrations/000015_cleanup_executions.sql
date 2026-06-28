-- +goose Up

CREATE TABLE IF NOT EXISTS cleanup_executions (
  id BIGSERIAL PRIMARY KEY,
  nexus_instance_id BIGINT NOT NULL REFERENCES nexus_instances (id) ON DELETE CASCADE,
  repository_name TEXT,
  policy_name TEXT NOT NULL,
  policy_yaml TEXT NOT NULL,
  status TEXT NOT NULL,
  requested_by TEXT NOT NULL,
  approved_by TEXT,
  approval_text TEXT NOT NULL,
  destructive_enabled BOOLEAN NOT NULL DEFAULT false,
  simulation_summary JSONB NOT NULL DEFAULT '{}',
  audit_log JSONB NOT NULL DEFAULT '[]',
  post_delete_verification_run_id BIGINT REFERENCES scan_runs (id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  approved_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS cleanup_executions_nexus_created_idx
  ON cleanup_executions (nexus_instance_id, created_at DESC);

CREATE INDEX IF NOT EXISTS cleanup_executions_status_idx
  ON cleanup_executions (status, created_at DESC);

-- +goose Down

DROP TABLE IF EXISTS cleanup_executions;
