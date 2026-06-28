-- name: UpsertNexusInstanceForIndexing :one
INSERT INTO nexus_instances (name, base_url, updated_at, last_seen_at)
VALUES ($1, $2, now(), now())
ON CONFLICT (name) DO UPDATE
SET base_url = EXCLUDED.base_url,
    updated_at = now(),
    last_seen_at = now()
RETURNING id, name, base_url, created_at, updated_at;

-- name: CreateScanRun :one
INSERT INTO scan_runs (
  nexus_instance_id,
  scan_type,
  repository_name,
  trigger_source,
  trigger_job_name,
  status,
  phase,
  cursor_before,
  estimated_total_tasks,
  stats
)
VALUES ($1, $2, $3, $4, $5, 'running', 'queued', $6, $7, $8)
RETURNING *;

-- name: CreateSkippedScanRun :one
INSERT INTO scan_runs (
  nexus_instance_id,
  scan_type,
  repository_name,
  trigger_source,
  trigger_job_name,
  status,
  phase,
  finished_at,
  error_code,
  error_class,
  error_message,
  error_details,
  stats
)
VALUES ($1, $2, $3, $4, $5, 'skipped_blocked', 'finished', now(), $6, $7, $8, $9, $10)
RETURNING *;

-- name: MarkScanRunFailed :one
UPDATE scan_runs
SET status = 'failed',
    phase = 'finished',
    finished_at = now(),
    error_code = $2,
    error_class = $3,
    error_message = $4,
    error_details = $5
WHERE id = $1
RETURNING *;

-- name: GetScanRun :one
SELECT * FROM scan_runs
WHERE id = $1;

-- name: ListRecentScanRuns :many
SELECT * FROM scan_runs
ORDER BY started_at DESC, id DESC
LIMIT $1;

-- name: ListRecentScanTasks :many
SELECT * FROM scan_tasks
WHERE (sqlc.narg(scan_run_id)::bigint IS NULL OR scan_run_id = sqlc.narg(scan_run_id)::bigint)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(limit_count);

-- name: RetryScanTaskByID :one
WITH retried_task AS (
  UPDATE scan_tasks
  SET status = 'retryable',
      available_at = now(),
      max_attempts = GREATEST(max_attempts, attempt + 3),
      lease_expires_at = NULL,
      lock_token = NULL,
      locked_by = NULL,
      locked_at = NULL,
      error_code = NULL,
      error_class = NULL,
      error_message = NULL,
      error_details = '{}'
  WHERE scan_tasks.id = $1
    AND scan_tasks.status IN ('failed', 'retryable')
  RETURNING *
),
revived_run AS (
  UPDATE scan_runs r
  SET status = 'running',
      phase = 'queued',
      finished_at = NULL,
      failed_tasks = GREATEST(failed_tasks - 1, 0),
      error_code = NULL,
      error_class = NULL,
      error_message = NULL,
      error_details = '{}',
      last_heartbeat_at = now()
  WHERE r.id = (SELECT scan_run_id FROM retried_task)
    AND r.status IN ('failed', 'canceled')
  RETURNING id
)
SELECT * FROM retried_task;

-- name: CancelScanRun :one
WITH canceled_tasks AS (
  UPDATE scan_tasks
  SET status = 'canceled',
      finished_at = COALESCE(finished_at, now()),
      lease_expires_at = NULL,
      lock_token = NULL,
      locked_by = NULL,
      locked_at = NULL
  WHERE scan_run_id = $1
    AND status IN ('pending', 'retryable', 'running')
  RETURNING id
)
UPDATE scan_runs
SET status = 'canceled',
    phase = 'finished',
    finished_at = COALESCE(finished_at, now()),
    error_code = 'run_canceled',
    error_class = 'operator',
    error_message = 'index run canceled',
    error_details = jsonb_build_object('canceled_tasks', (SELECT count(*) FROM canceled_tasks))
WHERE scan_runs.id = $1
  AND scan_runs.status IN ('running', 'pending')
RETURNING *;

-- name: TryAcquireSchedulerLock :one
SELECT pg_try_advisory_lock($1)::boolean;

-- name: ReleaseSchedulerLock :exec
SELECT pg_advisory_unlock($1);

-- name: UpsertIndexingSchedule :one
INSERT INTO indexing_schedules (
  nexus_instance_id,
  scan_type,
  name,
  enabled,
  schedule_expr,
  interval_seconds,
  scope,
  next_run_at,
  updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
ON CONFLICT (nexus_instance_id, name) DO UPDATE
SET scan_type = EXCLUDED.scan_type,
    enabled = EXCLUDED.enabled,
    schedule_expr = EXCLUDED.schedule_expr,
    interval_seconds = EXCLUDED.interval_seconds,
    scope = EXCLUDED.scope,
    next_run_at = COALESCE(indexing_schedules.next_run_at, EXCLUDED.next_run_at),
    updated_at = now()
RETURNING *;

-- name: ListDueIndexingSchedules :many
SELECT
  s.id,
  s.nexus_instance_id,
  s.scan_type,
  s.name,
  s.enabled,
  s.schedule_expr,
  s.interval_seconds,
  s.timezone,
  s.scope,
  s.lock_conflict_behavior,
  s.next_run_at,
  s.last_run_at,
  s.created_at,
  s.updated_at,
  n.name AS nexus_name,
  n.base_url AS nexus_base_url
FROM indexing_schedules s
JOIN nexus_instances n ON n.id = s.nexus_instance_id
LEFT JOIN nexus_indexing_controls c ON c.nexus_instance_id = s.nexus_instance_id
WHERE s.enabled
  AND s.next_run_at IS NOT NULL
  AND s.next_run_at <= now()
  AND COALESCE(c.paused, false) = false
  AND (c.circuit_open_until IS NULL OR c.circuit_open_until <= now())
ORDER BY s.next_run_at, s.id
LIMIT $1;

-- name: MarkIndexingScheduleTicked :one
UPDATE indexing_schedules
SET last_run_at = now(),
    next_run_at = now() + make_interval(secs => interval_seconds),
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetOperationLock :one
SELECT
  l.nexus_instance_id,
  l.scan_run_id,
  l.operation_type,
  l.lock_level,
  l.acquired_at,
  l.expires_at,
  l.reason,
  r.scan_type,
  r.status AS scan_run_status
FROM nexus_operation_locks l
JOIN scan_runs r ON r.id = l.scan_run_id
WHERE l.nexus_instance_id = $1;

-- name: AcquireOperationLock :one
INSERT INTO nexus_operation_locks (
  nexus_instance_id,
  scan_run_id,
  operation_type,
  lock_level,
  reason
)
VALUES ($1, $2, $3, 'exclusive', $4)
ON CONFLICT (nexus_instance_id) DO NOTHING
RETURNING *;

-- name: ReleaseOperationLockByRun :exec
DELETE FROM nexus_operation_locks
WHERE scan_run_id = $1;

-- name: EnqueueScanTask :one
INSERT INTO scan_tasks (
  scan_run_id,
  nexus_instance_id,
  task_type,
  task_key,
  repository_name,
  subject,
  cost_class,
  priority,
  max_attempts,
  payload
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (scan_run_id, task_type, task_key) DO UPDATE
SET priority = GREATEST(scan_tasks.priority, EXCLUDED.priority)
RETURNING *;

-- name: UpsertRepositoryFromDiscovery :one
INSERT INTO repositories (
  nexus_instance_id,
  name,
  format,
  type,
  url,
  online,
  metadata,
  updated_at,
  last_seen_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now())
ON CONFLICT (nexus_instance_id, name) DO UPDATE
SET format = EXCLUDED.format,
    type = EXCLUDED.type,
    url = EXCLUDED.url,
    online = EXCLUDED.online,
    metadata = EXCLUDED.metadata,
    updated_at = now(),
    last_seen_at = now()
RETURNING *;

-- name: ClaimScanTasks :many
WITH candidates AS (
  SELECT t.id
  FROM scan_tasks t
  LEFT JOIN nexus_indexing_controls c ON c.nexus_instance_id = t.nexus_instance_id
  WHERE t.status IN ('pending', 'retryable')
    AND t.available_at <= now()
    AND t.attempt < t.max_attempts
    AND COALESCE(c.paused, false) = false
    AND (c.circuit_open_until IS NULL OR c.circuit_open_until <= now())
  ORDER BY t.priority DESC, t.available_at, t.id
  LIMIT sqlc.arg(limit_count)
  FOR UPDATE OF t SKIP LOCKED
)
UPDATE scan_tasks t
SET status = 'running',
    attempt = t.attempt + 1,
    lock_token = sqlc.arg(lock_token),
    locked_by = sqlc.arg(worker_id),
    locked_at = now(),
    started_at = COALESCE(t.started_at, now()),
    lease_expires_at = now() + make_interval(secs => sqlc.arg(lease_seconds)::int),
    last_heartbeat_at = now(),
    error_code = NULL,
    error_class = NULL,
    error_message = NULL,
    error_details = '{}'
FROM candidates
WHERE t.id = candidates.id
RETURNING t.*;

-- name: PauseNexusIndexing :one
INSERT INTO nexus_indexing_controls (
  nexus_instance_id,
  paused,
  paused_reason,
  paused_at,
  paused_by,
  updated_at
)
VALUES ($1, true, $2, now(), $3, now())
ON CONFLICT (nexus_instance_id) DO UPDATE
SET paused = true,
    paused_reason = EXCLUDED.paused_reason,
    paused_at = now(),
    paused_by = EXCLUDED.paused_by,
    updated_at = now()
RETURNING *;

-- name: ResumeNexusIndexing :one
INSERT INTO nexus_indexing_controls (
  nexus_instance_id,
  paused,
  circuit_failure_count,
  circuit_open_until,
  updated_at
)
VALUES ($1, false, 0, NULL, now())
ON CONFLICT (nexus_instance_id) DO UPDATE
SET paused = false,
    paused_reason = NULL,
    paused_at = NULL,
    paused_by = NULL,
    circuit_failure_count = 0,
    circuit_open_until = NULL,
    updated_at = now()
RETURNING *;

-- name: RecordNexusIndexingSuccess :one
INSERT INTO nexus_indexing_controls (
  nexus_instance_id,
  paused,
  circuit_failure_count,
  circuit_open_until,
  updated_at
)
VALUES ($1, false, 0, NULL, now())
ON CONFLICT (nexus_instance_id) DO UPDATE
SET circuit_failure_count = 0,
    circuit_open_until = NULL,
    updated_at = now()
RETURNING *;

-- name: RecordNexusIndexingFailure :one
INSERT INTO nexus_indexing_controls (
  nexus_instance_id,
  paused,
  circuit_failure_count,
  circuit_open_until,
  last_failure_at,
  last_failure_code,
  last_failure_class,
  updated_at
)
VALUES (
  $1,
  false,
  1,
  CASE WHEN sqlc.arg(threshold)::int <= 1 THEN sqlc.arg(open_until)::timestamptz ELSE NULL END,
  now(),
  $2,
  $3,
  now()
)
ON CONFLICT (nexus_instance_id) DO UPDATE
SET circuit_failure_count = nexus_indexing_controls.circuit_failure_count + 1,
    circuit_open_until = CASE
      WHEN nexus_indexing_controls.circuit_failure_count + 1 >= sqlc.arg(threshold)::int THEN sqlc.arg(open_until)::timestamptz
      ELSE nexus_indexing_controls.circuit_open_until
    END,
    last_failure_at = now(),
    last_failure_code = EXCLUDED.last_failure_code,
    last_failure_class = EXCLUDED.last_failure_class,
    updated_at = now()
RETURNING *;

-- name: GetNexusIndexingControl :one
SELECT * FROM nexus_indexing_controls
WHERE nexus_instance_id = $1;

-- name: CreateScanTaskAttempt :one
INSERT INTO scan_task_attempts (
  scan_task_id,
  attempt,
  worker_id,
  worker_pod_name,
  lock_token,
  status
)
VALUES ($1, $2, $3, $4, $5, 'running')
RETURNING *;

-- name: FinishScanTaskAttempt :exec
UPDATE scan_task_attempts
SET status = $2,
    finished_at = now(),
    error_code = $3,
    error_class = $4,
    error_message = $5,
    error_details = $6,
    stats = $7
WHERE id = $1;

-- name: CompleteScanTask :one
UPDATE scan_tasks
SET status = 'succeeded',
    finished_at = now(),
    lease_expires_at = NULL,
    lock_token = NULL,
    locked_by = NULL,
    locked_at = NULL,
    last_heartbeat_at = now(),
    stats = $3,
    error_code = NULL,
    error_class = NULL,
    error_message = NULL,
    error_details = '{}'
WHERE id = $1
  AND lock_token = $2
RETURNING *;

-- name: RetryScanTask :one
UPDATE scan_tasks
SET status = 'retryable',
    available_at = $3,
    lease_expires_at = NULL,
    lock_token = NULL,
    locked_by = NULL,
    locked_at = NULL,
    last_heartbeat_at = now(),
    error_code = $4,
    error_class = $5,
    error_message = $6,
    error_details = $7
WHERE id = $1
  AND lock_token = $2
RETURNING *;

-- name: FailScanTask :one
UPDATE scan_tasks
SET status = 'failed',
    finished_at = now(),
    lease_expires_at = NULL,
    lock_token = NULL,
    locked_by = NULL,
    locked_at = NULL,
    last_heartbeat_at = now(),
    error_code = $3,
    error_class = $4,
    error_message = $5,
    error_details = $6
WHERE id = $1
  AND lock_token = $2
RETURNING *;

-- name: ReclaimExpiredScanTasks :many
UPDATE scan_tasks
SET status = CASE WHEN attempt >= max_attempts THEN 'failed' ELSE 'retryable' END,
    available_at = now(),
    lease_expires_at = NULL,
    lock_token = NULL,
    locked_by = NULL,
    locked_at = NULL,
    error_code = 'task_lease_expired',
    error_class = 'worker',
    error_message = 'task lease expired',
    error_details = '{}'
WHERE status = 'running'
  AND lease_expires_at < now()
RETURNING *;

-- name: RefreshScanRunTaskCounters :one
WITH task_counts AS (
  SELECT
    count(*) FILTER (WHERE status = 'succeeded')::bigint AS completed_tasks,
    count(*) FILTER (WHERE status = 'failed')::bigint AS failed_tasks,
    count(*) FILTER (WHERE status IN ('pending', 'retryable', 'running'))::bigint AS active_tasks
  FROM scan_tasks
  WHERE scan_run_id = $1
)
UPDATE scan_runs r
SET completed_tasks = task_counts.completed_tasks,
    failed_tasks = task_counts.failed_tasks,
    status = CASE
      WHEN task_counts.active_tasks = 0 AND task_counts.failed_tasks > 0 THEN 'failed'
      WHEN task_counts.active_tasks = 0 THEN 'succeeded'
      ELSE r.status
    END,
    phase = CASE
      WHEN task_counts.active_tasks = 0 THEN 'finished'
      ELSE r.phase
    END,
    finished_at = CASE
      WHEN task_counts.active_tasks = 0 THEN COALESCE(r.finished_at, now())
      ELSE r.finished_at
    END,
    last_heartbeat_at = now()
FROM task_counts
WHERE r.id = $1
RETURNING r.*;
