-- name: CountAppUsers :one
SELECT count(*)::bigint FROM app_users;

-- name: CreateAppUser :one
INSERT INTO app_users (username, password_hash, role, updated_at)
VALUES ($1, $2, $3, now())
RETURNING id, username, password_hash, role, created_at, updated_at, disabled_at;

-- name: GetActiveAppUserByUsername :one
SELECT
  u.id,
  u.username,
  u.password_hash,
  u.role,
  r.capabilities,
  u.created_at,
  u.updated_at,
  u.disabled_at
FROM app_users u
JOIN app_roles r ON r.name = u.role
WHERE u.username = $1
  AND u.disabled_at IS NULL;

-- name: ListAppRoles :many
SELECT name, display_name, capabilities, built_in, created_at, updated_at
FROM app_roles
ORDER BY built_in DESC, name;

-- name: UpsertAppRole :one
INSERT INTO app_roles (name, display_name, capabilities, built_in, updated_at)
VALUES ($1, $2, $3, false, now())
ON CONFLICT (name) DO UPDATE
SET display_name = EXCLUDED.display_name,
    capabilities = EXCLUDED.capabilities,
    updated_at = now()
WHERE app_roles.built_in = false
RETURNING name, display_name, capabilities, built_in, created_at, updated_at;
