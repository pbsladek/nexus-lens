-- +goose Up

CREATE TABLE IF NOT EXISTS app_sessions (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL REFERENCES app_users (id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  revoked_at TIMESTAMPTZ,
  UNIQUE (token_hash)
);

CREATE INDEX IF NOT EXISTS app_sessions_user_active_idx
  ON app_sessions (user_id, expires_at)
  WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS app_sessions_active_token_idx
  ON app_sessions (token_hash)
  WHERE revoked_at IS NULL;

-- +goose Down

DROP TABLE IF EXISTS app_sessions;

