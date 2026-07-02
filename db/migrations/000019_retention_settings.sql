-- +goose Up

CREATE TABLE IF NOT EXISTS app_retention_settings (
  id BOOLEAN PRIMARY KEY DEFAULT true CHECK (id),
  interval_seconds INT,
  scan_run_days INT,
  cleanup_execution_days INT,
  verification_finding_days INT,
  trend_snapshot_days INT,
  updated_by TEXT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT app_retention_settings_interval_positive CHECK (interval_seconds IS NULL OR interval_seconds >= 60),
  CONSTRAINT app_retention_settings_scan_run_days_positive CHECK (scan_run_days IS NULL OR scan_run_days > 0),
  CONSTRAINT app_retention_settings_cleanup_execution_days_positive CHECK (cleanup_execution_days IS NULL OR cleanup_execution_days > 0),
  CONSTRAINT app_retention_settings_verification_finding_days_positive CHECK (verification_finding_days IS NULL OR verification_finding_days > 0),
  CONSTRAINT app_retention_settings_trend_snapshot_days_positive CHECK (trend_snapshot_days IS NULL OR trend_snapshot_days > 0)
);

-- +goose Down

DROP TABLE IF EXISTS app_retention_settings;
