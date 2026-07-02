-- +goose Up

CREATE TABLE IF NOT EXISTS app_roles (
  name TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  capabilities TEXT[] NOT NULL DEFAULT '{}',
  built_in BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO app_roles (name, display_name, capabilities, built_in, updated_at)
VALUES
  (
    'admin',
    'Admin',
    ARRAY[
      'read',
      'manage_index',
      'manage_settings',
      'manage_tag_patterns',
      'manage_findings',
      'execute_cleanup'
    ]::TEXT[],
    true,
    now()
  ),
  (
    'read_only',
    'Read only',
    ARRAY['read']::TEXT[],
    true,
    now()
  )
ON CONFLICT (name) DO UPDATE
SET display_name = EXCLUDED.display_name,
    capabilities = EXCLUDED.capabilities,
    built_in = EXCLUDED.built_in,
    updated_at = now();

ALTER TABLE app_users
  DROP CONSTRAINT IF EXISTS app_users_role_check;

ALTER TABLE app_users
  ADD CONSTRAINT app_users_role_fkey
  FOREIGN KEY (role)
  REFERENCES app_roles (name)
  ON UPDATE CASCADE;

CREATE INDEX IF NOT EXISTS app_roles_capabilities_gin_idx
  ON app_roles USING GIN (capabilities);

-- +goose Down

ALTER TABLE app_users
  DROP CONSTRAINT IF EXISTS app_users_role_fkey;

ALTER TABLE app_users
  ADD CONSTRAINT app_users_role_check
  CHECK (role IN ('admin', 'read_only'));

DROP TABLE IF EXISTS app_roles;
