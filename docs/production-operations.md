# Nexus Lens Production Operations

This document captures operational rules that should stay true as the implementation grows.

## PostgreSQL

Nexus Lens uses built-in PostgreSQL features only. The database does not require extensions, superuser privileges, a bundled operator, Redis, Valkey, or a separate task queue.

Production deployments should provide PostgreSQL through an external service or operator such as CloudNativePG. The Helm chart only references an existing Secret and maps its key names through values.

Backup scope:

- Back up PostgreSQL.
- Do not back up Nexus Lens pods; they are disposable.
- Do not treat the Nexus Lens index as the source of truth for artifacts. Nexus remains authoritative.
- Include app users, sessions, schedules, settings, queue history, cleanup audit rows, trend snapshots, and indexed artifact rows in the database backup.

Restore behavior:

- Restore PostgreSQL first.
- Start the API so migrations can run.
- Start workers after the database is reachable.
- Run a scoped verification or full reconcile after restore to confirm local index state against Nexus.
- If the index is suspect, run full reconcile for the affected Nexus repositories. The index can be rebuilt from Nexus APIs and registry APIs.

## Retention

Retention defaults are configured through Helm values and can be overridden at runtime from the Admin settings UI:

```yaml
worker:
  retention:
    intervalSeconds: 3600
    scanRunDays: 30
    cleanupExecutionDays: 365
    verificationFindingDays: 180
    trendSnapshotDays: 365
```

The runtime override is stored in PostgreSQL in `app_retention_settings`. If no override exists, the API and worker use the deployment defaults. The worker periodically deletes old terminal rows. Open verification findings and active cleanup executions are retained. Trend snapshots are daily rows and are not downsampled in v1.

## Query And Index Rules

Hot UI paths should stay on typed columns and indexed predicates:

- Filter by `nexus_instance_id` early.
- Filter by `repository_id` or `repository_name` before image/tag joins when possible.
- Keep tag explorer filters on current-state tables, not raw observations.
- Avoid JSONB predicates in hot paths unless the key has a deliberate expression index.
- Keep queue claim predicates narrow: status, availability, Nexus instance, cost class, priority, and ID.
- Avoid large unbounded list endpoints. Every list endpoint should have a limit and stable ordering.

The schema includes targeted indexes for recent run/task lists, queue claims, retention cutoffs, cleanup leases, closed verification findings, active tag scans, and Nexus asset checksum download lookups. Add new indexes only after identifying a hot query shape or retention path that cannot use the existing indexes.

Interactive analytics and cleanup queries should avoid global rollups. Scope repository/image/tag CTEs first, then aggregate assets, layers, and download timestamps only for the selected Nexus or repository set. Config endpoints should pass limits into SQL rather than fetching all rows and truncating in application code.

Useful review commands:

```sql
EXPLAIN (ANALYZE, BUFFERS)
SELECT ...;

SELECT relname, n_live_tup, n_dead_tup
FROM pg_stat_user_tables
ORDER BY n_dead_tup DESC;

SELECT schemaname, relname, indexrelname, idx_scan
FROM pg_stat_user_indexes
ORDER BY idx_scan ASC;
```

Performance review checklist:

- Analytics queries should not join millions of tag rows before applying Nexus/repository/search filters.
- Cleanup simulation should read bounded candidate sets and reuse current tag classification.
- Verification should create focused findings instead of broad expensive comparisons when a source run is partial.
- The PostgreSQL queue should claim small batches with `FOR UPDATE SKIP LOCKED` and do network work outside the claim transaction.
- Retention should not delete active queue rows, active cleanup executions, or open findings.

## Observability

Logs should be structured and include the IDs needed to debug one failed item:

- `request_id` for API calls.
- `worker_id`, `scan_run_id`, `task_id`, `task_type`, and `attempt` for indexing tasks.
- `cleanup_execution_id`, repository, image, tag, component ID, and event type for cleanup execution.
- Nexus route/status/request ID when available.
- Error code, class, message, retry decision, and relevant cursor or digest context.

Prometheus should cover:

- Queue status and oldest pending task age.
- Scan run status over the last 24 hours.
- Open verification finding count.
- Cleanup execution status, event rate, and oldest running cleanup age.

Alerts should stay operator-actionable. A good alert points to one of: a stuck queue, repeated upstream failure, a stale cleanup execution, or correctness findings that need review.

## RBAC

The first implementation keeps two built-in roles:

- `read_only`
- `admin`

API responses expose role-derived capabilities so handlers and the UI can use named permissions instead of broad role checks. Current capabilities are:

- `read`
- `manage_index`
- `manage_settings`
- `manage_tag_patterns`
- `manage_findings`
- `execute_cleanup`

For now, `admin` receives every capability and `read_only` receives only `read`. Future roles can split these capabilities without changing endpoint contracts.

## Test Coverage

Normal Go tests skip live PostgreSQL coverage unless `NEXUS_LENS_TEST_DATABASE_URL` is set. Use a disposable database URL and run:

```bash
NEXUS_LENS_TEST_DATABASE_URL="postgres://user:pass@localhost:5432/nexus_lens_test?sslmode=disable" make test-integration
```

The integration tests create a temporary schema, run embedded migrations through the application migration path, and cover retention settings, package-format query behavior, and Nexus Assets API package indexing.

The destructive cleanup fixture is intentionally opt-in:

```bash
make e2e-live-cleanup
```

It starts the Docker Compose Nexus/PostgreSQL stack, seeds Docker repositories, deletes a seeded tag through Nexus APIs, and verifies registry state, database state, cleanup events, and follow-up verification/reconcile runs.
