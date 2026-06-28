-- name: ListVerificationFindings :many
SELECT * FROM verification_findings
WHERE (sqlc.narg(nexus_instance_id)::bigint IS NULL OR nexus_instance_id = sqlc.narg(nexus_instance_id)::bigint)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
ORDER BY last_seen_at DESC, id DESC
LIMIT sqlc.arg(limit_count);

-- name: DismissVerificationFinding :one
UPDATE verification_findings
SET status = 'dismissed',
    dismissed_at = now(),
    resolved_at = NULL,
    last_seen_at = now(),
    metadata = metadata || jsonb_build_object('dismissed_by', sqlc.arg(dismissed_by)::text)
WHERE id = sqlc.arg(id)
  AND status = 'open'
RETURNING *;

-- name: UpsertOpenVerificationFinding :one
WITH updated AS (
  UPDATE verification_findings
  SET severity = sqlc.arg(severity),
      expected = sqlc.arg(expected),
      actual = sqlc.arg(actual),
      message = sqlc.arg(message),
      remediation = sqlc.arg(remediation),
      scan_run_id = sqlc.arg(vf_scan_run_id),
      last_seen_at = now(),
      metadata = sqlc.arg(metadata)
  WHERE verification_findings.status = 'open'
    AND verification_findings.nexus_instance_id IS NOT DISTINCT FROM sqlc.arg(vf_nexus_instance_id)
    AND verification_findings.finding_type = sqlc.arg(vf_finding_type)
    AND verification_findings.repository_name IS NOT DISTINCT FROM sqlc.arg(vf_repository_name)
    AND verification_findings.subject IS NOT DISTINCT FROM sqlc.arg(vf_subject)
  RETURNING *
),
inserted AS (
  INSERT INTO verification_findings (
    nexus_instance_id,
    scan_run_id,
    severity,
    finding_type,
    repository_name,
    subject,
    expected,
    actual,
    message,
    remediation,
    metadata
  )
  SELECT
    sqlc.arg(vf_nexus_instance_id),
    sqlc.arg(vf_scan_run_id),
    sqlc.arg(severity),
    sqlc.arg(vf_finding_type),
    sqlc.arg(vf_repository_name),
    sqlc.arg(vf_subject),
    sqlc.arg(expected),
    sqlc.arg(actual),
    sqlc.arg(message),
    sqlc.arg(remediation),
    sqlc.arg(metadata)
  WHERE NOT EXISTS (SELECT 1 FROM updated)
  RETURNING *
)
SELECT * FROM updated
UNION ALL
SELECT * FROM inserted
LIMIT 1;

-- name: ListMissingDigestVerificationCandidates :many
SELECT
  ni.id AS nexus_instance_id,
  r.name AS repository_name,
  ci.name AS image_name,
  ct.tag,
  ct.id AS tag_id
FROM container_tags ct
JOIN container_images ci ON ci.id = ct.image_id
JOIN repositories r ON r.id = ci.repository_id
JOIN nexus_instances ni ON ni.id = r.nexus_instance_id
WHERE (sqlc.narg(nexus_instance_id)::bigint IS NULL OR ni.id = sqlc.narg(nexus_instance_id)::bigint)
  AND (sqlc.narg(repository_name)::text IS NULL OR r.name = sqlc.narg(repository_name)::text)
  AND r.format = 'docker'
  AND r.type = 'hosted'
  AND ct.deleted_at IS NULL
  AND COALESCE(ct.digest, '') = ''
ORDER BY r.name, ci.name, ct.tag
LIMIT sqlc.arg(limit_count);

-- name: ListMissingManifestVerificationCandidates :many
SELECT
  ni.id AS nexus_instance_id,
  r.name AS repository_name,
  ci.name AS image_name,
  ct.tag,
  ct.id AS tag_id,
  ct.digest
FROM container_tags ct
JOIN container_images ci ON ci.id = ct.image_id
JOIN repositories r ON r.id = ci.repository_id
JOIN nexus_instances ni ON ni.id = r.nexus_instance_id
LEFT JOIN docker_manifests dm ON dm.repository_id = r.id AND dm.digest = ct.digest
WHERE (sqlc.narg(nexus_instance_id)::bigint IS NULL OR ni.id = sqlc.narg(nexus_instance_id)::bigint)
  AND (sqlc.narg(repository_name)::text IS NULL OR r.name = sqlc.narg(repository_name)::text)
  AND r.format = 'docker'
  AND r.type = 'hosted'
  AND ct.deleted_at IS NULL
  AND COALESCE(ct.digest, '') <> ''
  AND dm.id IS NULL
ORDER BY r.name, ci.name, ct.tag
LIMIT sqlc.arg(limit_count);
