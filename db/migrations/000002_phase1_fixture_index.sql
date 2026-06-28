-- +goose Up

CREATE TABLE IF NOT EXISTS nexus_instances (
  id BIGSERIAL PRIMARY KEY,
  name TEXT NOT NULL,
  base_url TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (name)
);

CREATE TABLE IF NOT EXISTS repositories (
  id BIGSERIAL PRIMARY KEY,
  nexus_instance_id BIGINT NOT NULL REFERENCES nexus_instances (id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  format TEXT NOT NULL,
  type TEXT NOT NULL,
  url TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (nexus_instance_id, name)
);

CREATE TABLE IF NOT EXISTS container_images (
  id BIGSERIAL PRIMARY KEY,
  repository_id BIGINT NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (repository_id, name)
);

CREATE TABLE IF NOT EXISTS container_tags (
  id BIGSERIAL PRIMARY KEY,
  image_id BIGINT NOT NULL REFERENCES container_images (id) ON DELETE CASCADE,
  tag TEXT NOT NULL,
  digest TEXT NOT NULL,
  media_type TEXT NOT NULL,
  size_bytes BIGINT NOT NULL DEFAULT 0,
  first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at TIMESTAMPTZ,
  UNIQUE (image_id, tag)
);

CREATE INDEX IF NOT EXISTS repositories_nexus_format_type_idx
  ON repositories (nexus_instance_id, format, type);

CREATE INDEX IF NOT EXISTS container_images_repository_last_seen_idx
  ON container_images (repository_id, last_seen_at DESC, id);

CREATE INDEX IF NOT EXISTS container_tags_image_last_seen_idx
  ON container_tags (image_id, last_seen_at DESC, id);

CREATE INDEX IF NOT EXISTS container_tags_active_digest_idx
  ON container_tags (digest)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS container_tags_active_tag_pattern_idx
  ON container_tags (tag text_pattern_ops)
  WHERE deleted_at IS NULL;

-- +goose Down

DROP TABLE IF EXISTS container_tags;
DROP TABLE IF EXISTS container_images;
DROP TABLE IF EXISTS repositories;
DROP TABLE IF EXISTS nexus_instances;
