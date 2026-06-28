-- name: UpsertNexusAsset :one
INSERT INTO nexus_assets (
  repository_id,
  nexus_asset_id,
  path,
  download_url,
  content_type,
  size_bytes,
  blob_store_name,
  checksum_sha1,
  checksum_sha256,
  last_downloaded_at,
  blob_created_at,
  last_modified_at,
  metadata,
  last_seen_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now())
ON CONFLICT (repository_id, nexus_asset_id) DO UPDATE
SET path = EXCLUDED.path,
    download_url = EXCLUDED.download_url,
    content_type = EXCLUDED.content_type,
    size_bytes = EXCLUDED.size_bytes,
    blob_store_name = EXCLUDED.blob_store_name,
    checksum_sha1 = EXCLUDED.checksum_sha1,
    checksum_sha256 = EXCLUDED.checksum_sha256,
    last_downloaded_at = EXCLUDED.last_downloaded_at,
    blob_created_at = EXCLUDED.blob_created_at,
    last_modified_at = EXCLUDED.last_modified_at,
    metadata = EXCLUDED.metadata,
    last_seen_at = now()
RETURNING *;
