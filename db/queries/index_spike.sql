-- name: UpsertNexusInstance :one
INSERT INTO nexus_instances (name, base_url, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (name) DO UPDATE
SET base_url = EXCLUDED.base_url,
    updated_at = now()
RETURNING id, name, base_url, created_at, updated_at;

-- name: UpsertRepository :one
INSERT INTO repositories (nexus_instance_id, name, format, type, url, updated_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (nexus_instance_id, name) DO UPDATE
SET format = EXCLUDED.format,
    type = EXCLUDED.type,
    url = EXCLUDED.url,
    updated_at = now()
RETURNING id, nexus_instance_id, name, format, type, url, created_at, updated_at;

-- name: UpsertContainerImage :one
INSERT INTO container_images (repository_id, name, last_seen_at)
VALUES ($1, $2, now())
ON CONFLICT (repository_id, name) DO UPDATE
SET last_seen_at = now()
RETURNING id, repository_id, name, first_seen_at, last_seen_at;

-- name: UpsertContainerTag :one
INSERT INTO container_tags (image_id, tag, digest, media_type, size_bytes, last_seen_at, deleted_at)
VALUES ($1, $2, $3, $4, $5, now(), NULL)
ON CONFLICT (image_id, tag) DO UPDATE
SET digest = EXCLUDED.digest,
    media_type = EXCLUDED.media_type,
    size_bytes = EXCLUDED.size_bytes,
    last_seen_at = now(),
    deleted_at = NULL,
    missing_in_full_reconcile_run_id = NULL,
    deleted_confirmed_run_id = NULL
RETURNING id, image_id, tag, digest, media_type, size_bytes, first_seen_at, last_seen_at, deleted_at;

-- name: MarkContainerTagDeleted :one
UPDATE container_tags
SET deleted_at = now(),
    last_seen_at = now()
WHERE image_id = $1
  AND tag = $2
  AND deleted_at IS NULL
RETURNING id, image_id, tag, digest, media_type, size_bytes, first_seen_at, last_seen_at, deleted_at;

-- name: MarkMissingContainerTagsDeletedForRepository :many
UPDATE container_tags ct
SET deleted_at = now(),
    missing_in_full_reconcile_run_id = $3,
    deleted_confirmed_run_id = $3
FROM container_images ci
WHERE ci.id = ct.image_id
  AND ci.repository_id = $1
  AND ct.deleted_at IS NULL
  AND ct.last_seen_at < $2
RETURNING ct.id, ct.image_id, ct.tag, ct.digest, ct.media_type, ct.size_bytes, ct.first_seen_at, ct.last_seen_at, ct.deleted_at;

-- name: ListActiveTagsForImage :many
SELECT ct.id, ct.tag, ct.digest, ct.media_type, ct.size_bytes, ct.first_seen_at, ct.last_seen_at
FROM container_tags ct
JOIN container_images ci ON ci.id = ct.image_id
JOIN repositories r ON r.id = ci.repository_id
JOIN nexus_instances ni ON ni.id = r.nexus_instance_id
WHERE ni.name = sqlc.arg(nexus_name)
  AND r.name = sqlc.arg(repository_name)
  AND ci.name = sqlc.arg(image_name)
  AND ct.deleted_at IS NULL
ORDER BY ct.tag;
