-- +goose Up

CREATE INDEX IF NOT EXISTS nexus_assets_repo_checksum_downloaded_idx
  ON nexus_assets (repository_id, checksum_sha256, last_downloaded_at DESC)
  WHERE checksum_sha256 IS NOT NULL
    AND checksum_sha256 <> '';

CREATE INDEX IF NOT EXISTS container_tags_active_image_first_seen_idx
  ON container_tags (image_id, first_seen_at, id)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS container_tags_active_image_last_seen_idx
  ON container_tags (image_id, last_seen_at DESC, id)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS repositories_format_type_nexus_name_idx
  ON repositories (format, type, nexus_instance_id, name, id);

-- +goose Down

DROP INDEX IF EXISTS repositories_format_type_nexus_name_idx;
DROP INDEX IF EXISTS container_tags_active_image_last_seen_idx;
DROP INDEX IF EXISTS container_tags_active_image_first_seen_idx;
DROP INDEX IF EXISTS nexus_assets_repo_checksum_downloaded_idx;
