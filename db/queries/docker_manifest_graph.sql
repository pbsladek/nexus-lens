-- name: ListActiveTagsForRepository :many
SELECT
  r.id AS repository_id,
  r.url AS repository_url,
  ci.name AS image_name,
  ct.tag,
  ct.digest,
  ct.media_type
FROM container_tags ct
JOIN container_images ci ON ci.id = ct.image_id
JOIN repositories r ON r.id = ci.repository_id
WHERE r.id = $1
  AND ct.deleted_at IS NULL
ORDER BY ci.name, ct.tag;

-- name: ListActiveTagsForRepositorySeenSince :many
SELECT
  r.id AS repository_id,
  r.url AS repository_url,
  ci.name AS image_name,
  ct.tag,
  ct.digest,
  ct.media_type
FROM container_tags ct
JOIN container_images ci ON ci.id = ct.image_id
JOIN repositories r ON r.id = ci.repository_id
WHERE r.id = $1
  AND ct.deleted_at IS NULL
  AND ct.last_seen_at >= $2
ORDER BY ci.name, ct.tag;

-- name: UpsertDockerManifest :one
INSERT INTO docker_manifests (
  repository_id,
  digest,
  media_type,
  config_digest,
  logical_size_bytes,
  metadata,
  last_seen_at
)
VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (repository_id, digest) DO UPDATE
SET media_type = EXCLUDED.media_type,
    config_digest = EXCLUDED.config_digest,
    logical_size_bytes = EXCLUDED.logical_size_bytes,
    metadata = EXCLUDED.metadata,
    last_seen_at = now()
RETURNING *;

-- name: UpsertDockerLayer :one
INSERT INTO docker_layers (
  repository_id,
  digest,
  media_type,
  size_bytes,
  last_seen_at
)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (repository_id, digest) DO UPDATE
SET media_type = EXCLUDED.media_type,
    size_bytes = EXCLUDED.size_bytes,
    last_seen_at = now()
RETURNING *;

-- name: LinkDockerManifestLayer :exec
INSERT INTO docker_manifest_layers (manifest_id, layer_id, ordinal)
VALUES ($1, $2, $3)
ON CONFLICT (manifest_id, layer_id, ordinal) DO NOTHING;
