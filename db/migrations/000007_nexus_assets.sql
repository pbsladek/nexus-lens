-- +goose Up

CREATE TABLE IF NOT EXISTS nexus_assets (
  id BIGSERIAL PRIMARY KEY,
  repository_id BIGINT NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
  nexus_asset_id TEXT,
  path TEXT,
  download_url TEXT,
  content_type TEXT,
  size_bytes BIGINT,
  blob_store_name TEXT,
  checksum_sha1 TEXT,
  checksum_sha256 TEXT,
  last_downloaded_at TIMESTAMPTZ,
  blob_created_at TIMESTAMPTZ,
  last_modified_at TIMESTAMPTZ,
  first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL DEFAULT '{}',
  UNIQUE (repository_id, nexus_asset_id)
);

CREATE INDEX IF NOT EXISTS nexus_assets_repo_path_idx
  ON nexus_assets (repository_id, path);

CREATE INDEX IF NOT EXISTS nexus_assets_repo_last_modified_idx
  ON nexus_assets (repository_id, last_modified_at DESC);

CREATE INDEX IF NOT EXISTS nexus_assets_repo_blob_created_idx
  ON nexus_assets (repository_id, blob_created_at DESC);

CREATE INDEX IF NOT EXISTS nexus_assets_repo_last_downloaded_idx
  ON nexus_assets (repository_id, last_downloaded_at DESC);

CREATE INDEX IF NOT EXISTS nexus_assets_blob_store_idx
  ON nexus_assets (blob_store_name);

CREATE INDEX IF NOT EXISTS nexus_assets_checksum_sha256_idx
  ON nexus_assets (checksum_sha256)
  WHERE checksum_sha256 IS NOT NULL;

-- +goose Down

DROP TABLE IF EXISTS nexus_assets;
