# Nexus Lens Flow Design

This document explains the simplified v1 architecture and indexing flows for Nexus Lens. It is intended to be read beside `nexus-lens-design.md` and `nexus-lens-indexing-queue-design.md`.

## V1 Architecture

V1 keeps the runtime small: API, worker, scheduler, PostgreSQL, and Nexus. PostgreSQL is the source of truth for application state, indexed artifact state, queue state, checkpoints, rollups, verification findings, and users.

```mermaid
flowchart LR
  UI[TypeScript UI]
  API[Go API pod]
  Worker[Go worker pod(s)]
  Scheduler[Scheduler loop or CronJob tick]
  PG[(PostgreSQL)]
  Nexus[Nexus Repository 3.88+]
  Registry[Nexus Docker Registry API]
  Prom[Prometheus]

  UI -->|/api/v1| API
  API -->|read/write app state| PG
  API -->|manual run requests| PG
  Scheduler -->|due schedules| PG
  Worker -->|claim scan_tasks| PG
  Worker -->|REST assets/repos/status/tasks| Nexus
  Worker -->|tags/manifests/layers| Registry
  Worker -->|index results/checkpoints/errors| PG
  Prom -->|scrape metrics| API
  Prom -->|scrape metrics| Worker
```

Out of v1:

- No custom Kubernetes operator.
- No packaged PostgreSQL.
- No Redis, Valkey, or external task queue.
- No webhook receiver/storage/API.
- No blob-store disk or object-store scanning.
- No cleanup execution.
- No deep Nexus component model for Docker.

## Simplified V1 Index Model

The v1 index is Docker/OCI-first. Nexus assets provide repository inventory and timestamps. Registry manifests provide tag-to-digest truth, logical image size, layer sharing, and reclaimable-byte estimates.

```mermaid
erDiagram
  NEXUS_INSTANCES ||--o{ REPOSITORIES : owns
  REPOSITORIES ||--o{ NEXUS_ASSETS : contains
  REPOSITORIES ||--o{ DOCKER_IMAGES : contains
  DOCKER_IMAGES ||--o{ DOCKER_TAGS : has
  DOCKER_TAGS ||--o{ DOCKER_TAG_OBSERVATIONS : records
  DOCKER_TAGS }o--o| DOCKER_MANIFESTS : current_digest
  REPOSITORIES ||--o{ DOCKER_MANIFESTS : caches
  REPOSITORIES ||--o{ DOCKER_LAYERS : caches
  DOCKER_MANIFESTS ||--o{ DOCKER_MANIFEST_LAYERS : references
  DOCKER_LAYERS ||--o{ DOCKER_MANIFEST_LAYERS : appears_in
  DOCKER_TAG_PATTERNS ||--o{ DOCKER_TAG_PATTERN_MATCHES : matches
  DOCKER_TAGS ||--o{ DOCKER_TAG_PATTERN_MATCHES : classified_by
  SCAN_RUNS ||--o{ SCAN_RUN_SCOPES : has
  SCAN_RUNS ||--o{ SCAN_TASKS : enqueues
  SCAN_TASKS ||--o{ SCAN_TASK_ATTEMPTS : records
  NEXUS_INSTANCES ||--o{ VERIFICATION_FINDINGS : reports
  NEXUS_INSTANCES ||--o{ TREND_SNAPSHOTS : snapshots
```

Deferred models:

- Generic `storage_objects`.
- Generic `source_observations`.
- `docker_tag_versions` interval history.
- Separate `registry_digest_cache`.
- Separate `index_warnings`.
- Webhook event storage.
- Generic artifact/package model for non-Docker formats.

`DOCKER_TAGS.current_digest` is a logical relationship to `DOCKER_MANIFESTS.digest`; it does not need to be a strict foreign key in v1 because manifests may be enriched asynchronously.

## Full Reconcile Flow

Full reconcile is the authoritative operation for broad drift detection and deletion confirmation. V1 deletion authority is repository-scoped.

```mermaid
sequenceDiagram
  participant Admin as Admin/UI or Schedule
  participant API as Go API
  participant PG as PostgreSQL
  participant W as Worker(s)
  participant NX as Nexus REST API
  participant REG as Docker Registry API

  Admin->>API: start full_reconcile for Nexus/repositories
  API->>PG: create scan_run and repository scan_run_scopes
  API->>PG: acquire per-Nexus operation lock
  API->>PG: enqueue asset_walk_slice tasks
  W->>PG: claim task with FOR UPDATE SKIP LOCKED
  W->>NX: list /service/rest/v1/assets?repository=...
  W->>PG: upsert nexus_assets and checkpoint continuationToken
  W->>PG: enqueue next asset_walk_slice if token exists
  W->>PG: derive candidate Docker image names from assets/local state
  W->>REG: list images/tags and resolve manifest digests
  W->>REG: fetch unknown manifests/layers
  W->>PG: upsert images, tags, manifests, layers, observations
  W->>PG: mark repository scope complete after required task groups finish
  W->>PG: confirm deletions only for succeeded repository scopes
  W->>PG: run verification checks for completed scope
  W->>PG: refresh rollup tables and trend snapshots
  W->>PG: finish scan_run and release operation lock
```

Deletion guardrails:

- Recent sync never marks broad deletions.
- Bounded reindex never marks broad deletions.
- Full reconcile can confirm deletions only after a repository scope is complete, cursor-exhausted, and error-free.
- Invalid cursors, cancellation, budget exhaustion, or partial scope failure block deletion confirmation for that scope.

## Recent Sync and Bounded Reindex

Recent sync and bounded reindex are candidate refreshes. They make the index fresher without pretending Nexus exposes a complete changed-since feed.

```mermaid
flowchart TD
  Start[Schedule or operator request]
  Scope[Build candidate scope]
  Sources[Candidate sources: local timestamps, previous failures, verification findings, tag pattern changes, operator scope]
  Narrow{Scope narrow enough to page Assets API?}
  PageAssets[Page selected repository Assets API]
  LocalOnly[Use local candidate rows only]
  Tags[Refresh registry tag lists]
  Manifest[Fetch only new or changed manifests]
  Upsert[Upsert current state and tag observations]
  DeleteGuard[No broad deletion confirmation]
  Rollups[Refresh affected rollups and snapshots]
  Finish[Finish run as succeeded or partial]

  Start --> Scope --> Sources --> Narrow
  Narrow -->|yes| PageAssets --> Tags
  Narrow -->|no| LocalOnly --> Tags
  Tags --> Manifest --> Upsert --> DeleteGuard --> Rollups --> Finish
```

Candidate refresh results should be labeled as partial unless the run completed a full authoritative repository scope.

## PostgreSQL Queue Flow

V1 uses PostgreSQL as the task queue. This keeps scan state, queue state, retries, checkpoints, and indexed data in one datastore.

```mermaid
stateDiagram-v2
  [*] --> pending
  pending --> running: worker claims row
  running --> succeeded: task complete with valid lock_token
  running --> retryable: retryable error
  retryable --> running: available_at reached
  retryable --> failed: attempts exhausted
  running --> failed: non-retryable error
  pending --> canceled: run canceled
  retryable --> canceled: run canceled
  running --> retryable: lease expired or reclaimed
  running --> canceled: worker observes cancel
  succeeded --> [*]
  failed --> [*]
  canceled --> [*]
```

Claiming rules:

- Workers claim with `FOR UPDATE SKIP LOCKED`.
- Claims are short transactions.
- Network work happens outside the claim transaction.
- `lease_expires_at` and `lock_token` prevent stale workers from writing late completion.
- Every failure writes a compact latest error on `scan_tasks` and detailed attempt history in `scan_task_attempts`.

## Schedule and Operation Lock Flow

Schedules are app-managed rows in PostgreSQL. The scheduler only decides what is due and enqueues work; it does not perform indexing inline.

```mermaid
flowchart TD
  Tick[Scheduler tick]
  Due[Read due indexing_schedules]
  Lock{Exclusive operation active for Nexus?}
  Skip[Record skipped_blocked with blocking run id]
  Run[Create scan_run]
  Exclusive{Run needs exclusive lock?}
  OpLock[Acquire per-Nexus operation lock for full_reconcile or bounded_reindex]
  Enqueue[Enqueue scan_tasks]
  Workers[Workers process tasks]
  Terminal[Run reaches terminal state]
  Release[Release operation lock if held]

  Tick --> Due --> Lock
  Lock -->|yes| Skip
  Lock -->|no| Run --> Exclusive
  Exclusive -->|yes| OpLock --> Enqueue
  Exclusive -->|no| Enqueue
  Enqueue --> Workers --> Terminal --> Release
```

Recent sync can be skipped when an exclusive operation is active. Full reconcile and bounded reindex must be mutually exclusive per Nexus instance.

## Data Freshness Flow

Freshness is tracked separately because each source has a different cost and reliability profile.

```mermaid
flowchart LR
  Assets[Asset inventory freshness]
  Tags[Tag-list freshness]
  Manifests[Manifest/layer enrichment freshness]
  Verify[Verification freshness]
  Rollup[Rollup freshness]
  UI[UI freshness badges]

  Assets --> UI
  Tags --> UI
  Manifests --> UI
  Verify --> UI
  Rollup --> UI
```

Storage views should distinguish:

- Nexus asset bytes from REST asset `fileSize`.
- Docker logical bytes from manifest/layer descriptors.
- Estimated reclaimable bytes from Docker layer sharing analysis.

## Error and Verification Flow

Errors are classified once in Go and stored in structured fields. Correctness-impacting errors become verification findings.

```mermaid
flowchart TD
  Err[Task/API error]
  Classify[Central error classifier]
  Retry{Retryable?}
  Attempt[Write scan_task_attempts]
  Retryable[Set scan_tasks retryable with next available_at]
  Failed[Set scan_tasks failed]
  Finding{May affect index correctness?}
  VF[Create or refresh verification_findings]
  UI[Show failed task/finding in UI]

  Err --> Classify --> Attempt --> Retry
  Retry -->|yes| Retryable --> UI
  Retry -->|no| Failed --> Finding
  Finding -->|yes| VF --> UI
  Finding -->|no| UI
```

Operators should be able to answer:

- Which Nexus/repository/image/tag failed.
- Which cursor, page, digest, or route failed.
- Whether the error is retryable.
- When the next retry will happen.
- Which scan run and task attempt contain the details.
- Which data remains trustworthy.

## Rollup and Snapshot Flow

V1 prefers explicit rollup tables and daily snapshots over materialized views.

```mermaid
flowchart TD
  IndexChange[Index writes current rows]
  ScopeDone[Scope or run completes]
  RollupTask[rollup_refresh task]
  RollupTables[Dashboard rollup tables]
  SnapshotTask[Daily trend snapshot task]
  TrendSnapshots[trend_snapshots]
  API[Analytics API]

  IndexChange --> ScopeDone --> RollupTask --> RollupTables --> API
  ScopeDone --> SnapshotTask --> TrendSnapshots --> API
```

Materialized views can be added later if profiling shows they are better than rollup tables for specific dashboards.

## Later Extension Points

These can be added after v1 without changing the core source-of-truth model:

- Webhook-assisted candidate discovery.
- Non-Docker format indexers.
- Generic artifact/package model.
- Cleanup policy simulation.
- Destructive cleanup execution.
- Cross-repository digest cache.
- Materialized views for expensive dashboards.
