-- name: UpsertGenericArtifact :one
INSERT INTO generic_artifacts (
  repository_id,
  format,
  namespace,
  name,
  version,
  path,
  size_bytes,
  checksum_sha256,
  metadata,
  last_seen_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
ON CONFLICT (repository_id, format, namespace, name, version, path) DO UPDATE
SET size_bytes = EXCLUDED.size_bytes,
    checksum_sha256 = EXCLUDED.checksum_sha256,
    metadata = EXCLUDED.metadata,
    last_seen_at = now()
RETURNING *;
