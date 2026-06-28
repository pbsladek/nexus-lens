-- +goose Up

CREATE TABLE IF NOT EXISTS generic_artifacts (
  id BIGSERIAL PRIMARY KEY,
  repository_id BIGINT NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
  format TEXT NOT NULL,
  namespace TEXT,
  name TEXT NOT NULL,
  version TEXT,
  path TEXT,
  size_bytes BIGINT,
  checksum_sha256 TEXT,
  first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL DEFAULT '{}',
  UNIQUE (repository_id, format, namespace, name, version, path)
);

CREATE INDEX IF NOT EXISTS generic_artifacts_repo_format_idx
  ON generic_artifacts (repository_id, format, name);

CREATE INDEX IF NOT EXISTS generic_artifacts_repo_last_seen_idx
  ON generic_artifacts (repository_id, last_seen_at DESC);

-- +goose Down

DROP TABLE IF EXISTS generic_artifacts;
