-- +goose Up

ALTER TABLE nexus_instances
  ADD COLUMN IF NOT EXISTS auth_secret_ref TEXT,
  ADD COLUMN IF NOT EXISTS nexus_version TEXT,
  ADD COLUMN IF NOT EXISTS api_capabilities JSONB NOT NULL DEFAULT '{}',
  ADD COLUMN IF NOT EXISTS last_capability_probe_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}',
  ADD COLUMN IF NOT EXISTS first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now();

ALTER TABLE repositories
  ADD COLUMN IF NOT EXISTS registry_base_url TEXT,
  ADD COLUMN IF NOT EXISTS registry_routing_mode TEXT,
  ADD COLUMN IF NOT EXISTS registry_validated_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS blob_store_name TEXT,
  ADD COLUMN IF NOT EXISTS online BOOLEAN,
  ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}',
  ADD COLUMN IF NOT EXISTS first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE TABLE IF NOT EXISTS scan_runs (
  id BIGSERIAL PRIMARY KEY,
  nexus_instance_id BIGINT NOT NULL REFERENCES nexus_instances (id) ON DELETE CASCADE,
  scan_type TEXT NOT NULL,
  repository_name TEXT,
  trigger_source TEXT NOT NULL DEFAULT 'manual',
  trigger_job_name TEXT,
  status TEXT NOT NULL,
  phase TEXT,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ,
  last_heartbeat_at TIMESTAMPTZ,
  estimated_total_tasks BIGINT,
  completed_tasks BIGINT NOT NULL DEFAULT 0,
  failed_tasks BIGINT NOT NULL DEFAULT 0,
  progress_percent NUMERIC,
  progress_confidence TEXT NOT NULL DEFAULT 'unknown',
  cursor_before JSONB,
  cursor_after JSONB,
  stats JSONB NOT NULL DEFAULT '{}',
  error_code TEXT,
  error_class TEXT,
  error_message TEXT,
  error_details JSONB NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS scan_run_scopes (
  id BIGSERIAL PRIMARY KEY,
  scan_run_id BIGINT NOT NULL REFERENCES scan_runs (id) ON DELETE CASCADE,
  nexus_instance_id BIGINT NOT NULL REFERENCES nexus_instances (id) ON DELETE CASCADE,
  repository_id BIGINT REFERENCES repositories (id) ON DELETE SET NULL,
  image_id BIGINT REFERENCES container_images (id) ON DELETE SET NULL,
  scope_type TEXT NOT NULL,
  scope_key TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  asset_walk_completed BOOLEAN NOT NULL DEFAULT false,
  tag_walk_completed BOOLEAN NOT NULL DEFAULT false,
  enrichment_completed BOOLEAN NOT NULL DEFAULT false,
  cursor_exhausted BOOLEAN NOT NULL DEFAULT false,
  invalid_cursor_seen BOOLEAN NOT NULL DEFAULT false,
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  stats JSONB NOT NULL DEFAULT '{}',
  UNIQUE (scan_run_id, scope_type, scope_key)
);

CREATE TABLE IF NOT EXISTS scan_tasks (
  id BIGSERIAL PRIMARY KEY,
  scan_run_id BIGINT NOT NULL REFERENCES scan_runs (id) ON DELETE CASCADE,
  nexus_instance_id BIGINT NOT NULL REFERENCES nexus_instances (id) ON DELETE CASCADE,
  task_type TEXT NOT NULL,
  task_key TEXT NOT NULL,
  repository_name TEXT,
  subject TEXT,
  cost_class TEXT NOT NULL DEFAULT 'default',
  estimated_requests INT,
  status TEXT NOT NULL DEFAULT 'pending',
  priority INT NOT NULL DEFAULT 0,
  attempt INT NOT NULL DEFAULT 0,
  max_attempts INT NOT NULL DEFAULT 5,
  available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  lease_expires_at TIMESTAMPTZ,
  lock_token UUID,
  locked_by TEXT,
  locked_at TIMESTAMPTZ,
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  last_heartbeat_at TIMESTAMPTZ,
  progress_current BIGINT,
  progress_total BIGINT,
  progress_label TEXT,
  payload JSONB NOT NULL DEFAULT '{}',
  checkpoint JSONB NOT NULL DEFAULT '{}',
  stats JSONB NOT NULL DEFAULT '{}',
  error_code TEXT,
  error_class TEXT,
  error_message TEXT,
  error_details JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (scan_run_id, task_type, task_key)
);

CREATE TABLE IF NOT EXISTS scan_task_attempts (
  id BIGSERIAL PRIMARY KEY,
  scan_task_id BIGINT NOT NULL REFERENCES scan_tasks (id) ON DELETE CASCADE,
  attempt INT NOT NULL,
  worker_id TEXT,
  worker_pod_name TEXT,
  lock_token UUID,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ,
  status TEXT NOT NULL,
  error_code TEXT,
  error_class TEXT,
  error_message TEXT,
  error_details JSONB NOT NULL DEFAULT '{}',
  stats JSONB NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS nexus_operation_locks (
  nexus_instance_id BIGINT PRIMARY KEY REFERENCES nexus_instances (id) ON DELETE CASCADE,
  scan_run_id BIGINT NOT NULL REFERENCES scan_runs (id) ON DELETE CASCADE,
  operation_type TEXT NOT NULL,
  lock_level TEXT NOT NULL,
  acquired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ,
  reason TEXT
);

CREATE TABLE IF NOT EXISTS index_checkpoints (
  id BIGSERIAL PRIMARY KEY,
  nexus_instance_id BIGINT NOT NULL REFERENCES nexus_instances (id) ON DELETE CASCADE,
  repository_name TEXT,
  checkpoint_type TEXT NOT NULL,
  subject TEXT NOT NULL DEFAULT '',
  cursor JSONB NOT NULL DEFAULT '{}',
  high_watermark TIMESTAMPTZ,
  last_successful_scan_run_id BIGINT REFERENCES scan_runs (id) ON DELETE SET NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (nexus_instance_id, repository_name, checkpoint_type, subject)
);

CREATE TABLE IF NOT EXISTS verification_findings (
  id BIGSERIAL PRIMARY KEY,
  nexus_instance_id BIGINT REFERENCES nexus_instances (id) ON DELETE CASCADE,
  scan_run_id BIGINT REFERENCES scan_runs (id) ON DELETE SET NULL,
  severity TEXT NOT NULL,
  finding_type TEXT NOT NULL,
  repository_name TEXT,
  subject TEXT,
  expected JSONB,
  actual JSONB,
  message TEXT,
  remediation TEXT,
  status TEXT NOT NULL DEFAULT 'open',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at TIMESTAMPTZ,
  dismissed_at TIMESTAMPTZ,
  metadata JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS indexing_nexus_capability_probe_idx
  ON nexus_instances (last_capability_probe_at);

CREATE INDEX IF NOT EXISTS scan_runs_instance_status_started_idx
  ON scan_runs (nexus_instance_id, status, started_at DESC);

CREATE INDEX IF NOT EXISTS scan_runs_active_idx
  ON scan_runs (status, started_at DESC)
  WHERE status IN ('pending', 'running', 'pausing', 'canceling');

CREATE INDEX IF NOT EXISTS scan_runs_error_class_idx
  ON scan_runs (nexus_instance_id, error_class, started_at DESC)
  WHERE error_class IS NOT NULL;

CREATE INDEX IF NOT EXISTS scan_run_scopes_run_status_idx
  ON scan_run_scopes (scan_run_id, status, scope_type);

CREATE INDEX IF NOT EXISTS scan_run_scopes_repo_idx
  ON scan_run_scopes (nexus_instance_id, repository_id, status);

CREATE INDEX IF NOT EXISTS scan_tasks_claim_idx
  ON scan_tasks (status, available_at, priority DESC, id)
  WHERE status IN ('pending', 'retryable');

CREATE INDEX IF NOT EXISTS scan_tasks_running_lease_idx
  ON scan_tasks (lease_expires_at, id)
  WHERE status = 'running';

CREATE INDEX IF NOT EXISTS scan_tasks_run_status_idx
  ON scan_tasks (scan_run_id, status, id);

CREATE INDEX IF NOT EXISTS scan_tasks_nexus_cost_status_idx
  ON scan_tasks (nexus_instance_id, cost_class, status, available_at);

CREATE INDEX IF NOT EXISTS scan_tasks_error_class_idx
  ON scan_tasks (nexus_instance_id, error_class, available_at)
  WHERE error_class IS NOT NULL;

CREATE INDEX IF NOT EXISTS scan_task_attempts_task_started_idx
  ON scan_task_attempts (scan_task_id, started_at DESC);

CREATE INDEX IF NOT EXISTS scan_task_attempts_error_class_idx
  ON scan_task_attempts (error_class, started_at DESC)
  WHERE error_class IS NOT NULL;

CREATE INDEX IF NOT EXISTS index_checkpoints_lookup_idx
  ON index_checkpoints (nexus_instance_id, repository_name, checkpoint_type, subject);

CREATE INDEX IF NOT EXISTS verification_findings_open_idx
  ON verification_findings (nexus_instance_id, severity, created_at DESC)
  WHERE status = 'open';

-- +goose Down

DROP TABLE IF EXISTS verification_findings;
DROP TABLE IF EXISTS index_checkpoints;
DROP TABLE IF EXISTS nexus_operation_locks;
DROP TABLE IF EXISTS scan_task_attempts;
DROP TABLE IF EXISTS scan_tasks;
DROP TABLE IF EXISTS scan_run_scopes;
DROP TABLE IF EXISTS scan_runs;
