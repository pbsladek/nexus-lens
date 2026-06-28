-- +goose Up

ALTER TABLE nexus_indexing_controls
  ADD COLUMN IF NOT EXISTS asset_list_rps NUMERIC,
  ADD COLUMN IF NOT EXISTS registry_manifest_rps NUMERIC,
  ADD COLUMN IF NOT EXISTS default_rps NUMERIC;

-- +goose Down

ALTER TABLE nexus_indexing_controls
  DROP COLUMN IF EXISTS default_rps,
  DROP COLUMN IF EXISTS registry_manifest_rps,
  DROP COLUMN IF EXISTS asset_list_rps;
