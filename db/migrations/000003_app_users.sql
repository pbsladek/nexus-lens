-- +goose Up

CREATE TABLE IF NOT EXISTS app_users (
  id BIGSERIAL PRIMARY KEY,
  username TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('admin', 'read_only')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  disabled_at TIMESTAMPTZ,
  UNIQUE (username)
);

CREATE INDEX IF NOT EXISTS app_users_active_role_idx
  ON app_users (role, username)
  WHERE disabled_at IS NULL;

-- +goose Down

DROP TABLE IF EXISTS app_users;

