-- +goose Up

CREATE TABLE IF NOT EXISTS trend_snapshots (
  id BIGSERIAL PRIMARY KEY,
  snapshot_date DATE NOT NULL,
  captured_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  nexus_instance_id BIGINT NOT NULL REFERENCES nexus_instances (id) ON DELETE CASCADE,
  repository_id BIGINT REFERENCES repositories (id) ON DELETE SET NULL,
  image_id BIGINT REFERENCES container_images (id) ON DELETE SET NULL,
  metric_name TEXT NOT NULL,
  metric_value NUMERIC NOT NULL,
  dimensions JSONB NOT NULL DEFAULT '{}',
  UNIQUE NULLS NOT DISTINCT (snapshot_date, nexus_instance_id, repository_id, image_id, metric_name, dimensions)
);

CREATE INDEX IF NOT EXISTS trend_snapshots_lookup_idx
  ON trend_snapshots (metric_name, snapshot_date, nexus_instance_id, repository_id);

CREATE INDEX IF NOT EXISTS trend_snapshots_repo_image_idx
  ON trend_snapshots (nexus_instance_id, repository_id, image_id, snapshot_date);

CREATE INDEX IF NOT EXISTS trend_snapshots_captured_idx
  ON trend_snapshots (captured_at DESC);

-- +goose Down

DROP TABLE IF EXISTS trend_snapshots;
