-- name: CreateAppSession :one
INSERT INTO app_sessions (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING id, user_id, token_hash, created_at, expires_at, revoked_at;

-- name: GetActiveAppSessionByTokenHash :one
SELECT
  s.id,
  s.user_id,
  s.token_hash,
  s.created_at,
  s.expires_at,
  s.revoked_at,
  u.username,
  u.role,
  r.capabilities
FROM app_sessions s
JOIN app_users u ON u.id = s.user_id
JOIN app_roles r ON r.name = u.role
WHERE s.token_hash = $1
  AND s.revoked_at IS NULL
  AND s.expires_at > now()
  AND u.disabled_at IS NULL;

-- name: RevokeAppSessionByTokenHash :exec
UPDATE app_sessions
SET revoked_at = now()
WHERE token_hash = $1
  AND revoked_at IS NULL;

-- name: DeleteExpiredAppSessions :exec
DELETE FROM app_sessions
WHERE expires_at < now() - interval '7 days'
   OR revoked_at < now() - interval '7 days';
