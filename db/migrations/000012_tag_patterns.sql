-- +goose Up

CREATE TABLE IF NOT EXISTS tag_patterns (
  id BIGSERIAL PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  tag_regex TEXT NOT NULL,
  priority INT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  built_in BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS container_tag_classifications (
  tag_id BIGINT PRIMARY KEY REFERENCES container_tags (id) ON DELETE CASCADE,
  pattern_id BIGINT REFERENCES tag_patterns (id) ON DELETE SET NULL,
  pattern_name TEXT NOT NULL DEFAULT 'unclassified',
  tag_value TEXT NOT NULL,
  classified_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS tag_patterns_enabled_priority_idx
  ON tag_patterns (enabled, priority, id);

CREATE INDEX IF NOT EXISTS container_tag_classifications_pattern_idx
  ON container_tag_classifications (pattern_id, classified_at DESC);

CREATE INDEX IF NOT EXISTS container_tag_classifications_pattern_name_idx
  ON container_tag_classifications (pattern_name, classified_at DESC);

CREATE INDEX IF NOT EXISTS container_tag_classifications_tag_value_idx
  ON container_tag_classifications (tag_value);

INSERT INTO tag_patterns (name, display_name, description, tag_regex, priority, enabled, built_in)
VALUES
  ('semver', 'SemVer', 'Semantic version tags with optional v prefix and prerelease/build suffix.', '^v?[0-9]+[.][0-9]+[.][0-9]+([-+][0-9A-Za-z.-]+)?$', 10, true, true),
  ('channel', 'Channel', 'Mutable release channel tags such as latest, stable, edge, main, or develop.', '^(latest|stable|edge|canary|main|master|develop|dev)$', 20, true, true),
  ('pull_request', 'Pull request', 'Pull request tags such as pr-123 or pull-123.', '^(pr|pull)-[0-9]+$', 30, true, true),
  ('long_sha', 'Long SHA', 'Long hexadecimal commit or content identifiers.', '^[0-9a-f]{40,64}$', 40, true, true),
  ('short_sha', 'Short SHA', 'Short hexadecimal commit identifiers.', '^[0-9a-f]{7,12}$', 50, true, true),
  ('ticket_branch', 'Ticket branch', 'Ticket-prefixed branch tags such as ABC-123-feature.', '^[A-Za-z]+-[0-9]+[-/_A-Za-z0-9.]*$', 60, true, true),
  ('feature_branch', 'Feature branch', 'Branch tags with common prefixes such as feat, feature, fix, hotfix, chore, or release.', '^(feat|feature|fix|bugfix|hotfix|chore|release)[-/_][A-Za-z0-9._-]+$', 70, true, true),
  ('date', 'Date', 'Date-like tags such as 20260628, 2026-06-28, or 2026.06.28.', '^[0-9]{4}[-.]?[0-9]{2}[-.]?[0-9]{2}([-.][0-9]+)?$', 80, true, true),
  ('environment', 'Environment', 'Environment tags such as prod, staging, qa, test, dev, or sandbox.', '^(prod|production|stage|staging|qa|test|dev|development|sandbox)$', 90, true, true),
  ('build_number', 'Build number', 'Numeric build tags or build-prefixed numeric tags.', '^(build-)?[0-9]{4,}$', 100, true, true),
  ('opaque_generated', 'Opaque generated', 'Long generated-looking tags that are not otherwise classified.', '^[A-Za-z0-9._-]{12,}$', 1000, true, true)
ON CONFLICT (name) DO NOTHING;

-- +goose Down

DROP TABLE IF EXISTS container_tag_classifications;
DROP TABLE IF EXISTS tag_patterns;
