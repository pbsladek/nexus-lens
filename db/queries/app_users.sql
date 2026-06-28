-- name: CountAppUsers :one
SELECT count(*)::bigint FROM app_users;

-- name: CreateAppUser :one
INSERT INTO app_users (username, password_hash, role, updated_at)
VALUES ($1, $2, $3, now())
RETURNING id, username, password_hash, role, created_at, updated_at, disabled_at;

-- name: GetActiveAppUserByUsername :one
SELECT id, username, password_hash, role, created_at, updated_at, disabled_at
FROM app_users
WHERE username = $1
  AND disabled_at IS NULL;

