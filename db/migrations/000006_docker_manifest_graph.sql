-- +goose Up

CREATE TABLE IF NOT EXISTS docker_manifests (
  id BIGSERIAL PRIMARY KEY,
  repository_id BIGINT NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
  digest TEXT NOT NULL,
  media_type TEXT,
  config_digest TEXT,
  logical_size_bytes BIGINT,
  first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL DEFAULT '{}',
  UNIQUE (repository_id, digest)
);

CREATE TABLE IF NOT EXISTS docker_layers (
  id BIGSERIAL PRIMARY KEY,
  repository_id BIGINT NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
  digest TEXT NOT NULL,
  media_type TEXT,
  size_bytes BIGINT,
  first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (repository_id, digest)
);

CREATE TABLE IF NOT EXISTS docker_manifest_layers (
  manifest_id BIGINT NOT NULL REFERENCES docker_manifests (id) ON DELETE CASCADE,
  layer_id BIGINT NOT NULL REFERENCES docker_layers (id) ON DELETE CASCADE,
  ordinal INT NOT NULL,
  PRIMARY KEY (manifest_id, layer_id, ordinal)
);

CREATE INDEX IF NOT EXISTS docker_manifests_repo_digest_idx
  ON docker_manifests (repository_id, digest);

CREATE INDEX IF NOT EXISTS docker_manifests_digest_idx
  ON docker_manifests (digest);

CREATE INDEX IF NOT EXISTS docker_layers_repo_digest_idx
  ON docker_layers (repository_id, digest);

CREATE INDEX IF NOT EXISTS docker_layers_digest_idx
  ON docker_layers (digest);

CREATE INDEX IF NOT EXISTS docker_manifest_layers_layer_idx
  ON docker_manifest_layers (layer_id, manifest_id);

-- +goose Down

DROP TABLE IF EXISTS docker_manifest_layers;
DROP TABLE IF EXISTS docker_layers;
DROP TABLE IF EXISTS docker_manifests;
