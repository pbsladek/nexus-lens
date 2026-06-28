-- +goose Up

CREATE TABLE IF NOT EXISTS nexus_indexing_controls (
  nexus_instance_id BIGINT PRIMARY KEY REFERENCES nexus_instances (id) ON DELETE CASCADE,
  paused BOOLEAN NOT NULL DEFAULT false,
  paused_reason TEXT,
  paused_at TIMESTAMPTZ,
  paused_by TEXT,
  circuit_failure_count INT NOT NULL DEFAULT 0,
  circuit_open_until TIMESTAMPTZ,
  last_failure_at TIMESTAMPTZ,
  last_failure_code TEXT,
  last_failure_class TEXT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS nexus_indexing_controls_blocking_idx
  ON nexus_indexing_controls (paused, circuit_open_until)
  WHERE paused OR circuit_open_until IS NOT NULL;

-- +goose Down

DROP TABLE IF EXISTS nexus_indexing_controls;
