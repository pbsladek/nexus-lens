-- +goose Up

ALTER TABLE container_tags
  ADD COLUMN IF NOT EXISTS missing_in_full_reconcile_run_id BIGINT REFERENCES scan_runs (id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS deleted_confirmed_run_id BIGINT REFERENCES scan_runs (id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS container_tags_active_last_seen_idx
  ON container_tags (last_seen_at, id)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS container_tags_deleted_confirmed_run_idx
  ON container_tags (deleted_confirmed_run_id)
  WHERE deleted_confirmed_run_id IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS container_tags_deleted_confirmed_run_idx;
DROP INDEX IF EXISTS container_tags_active_last_seen_idx;

ALTER TABLE container_tags
  DROP COLUMN IF EXISTS deleted_confirmed_run_id,
  DROP COLUMN IF EXISTS missing_in_full_reconcile_run_id;
