# Nexus Lens: Implementation Design

## Summary

Nexus Lens is a Kubernetes-native, read-only analytics and indexing platform for Sonatype Nexus Repository Manager 3.88.0 and newer. It builds a queryable index of hosted Docker/OCI artifacts and Nexus asset metadata into PostgreSQL. The backend is written in Go, the web UI is written in TypeScript, and the primary production deployment target is Kubernetes.

The web UI and API use the local index for fast exploration, dashboards, trend analysis, index operations, and later cleanup policy simulation. Kubernetes is the deployment platform, not an artifact inventory source: Nexus Lens should run naturally in-cluster and use Kubernetes primitives for config, secrets, scheduling, networking, and operations, while interacting only with Nexus for repository data.

The initial goal is not to replace Nexus cleanup policies or mutate Nexus. The first release should safely answer questions that are painful or slow in the Nexus UI:

- Which repositories/images/tags are consuming the most storage?
- Which tags are old, never pulled, or unused?
- What is the API-derived estimated reclaimable space after layer sharing?
- What has grown over time?
- Which index runs are fresh, stale, throttled, failed, or waiting on verification?

## Goals

### Primary goals

- Build a safe, read-only, scheduled index of Nexus hosted Docker/OCI repositories.
- Store the index in PostgreSQL for fast querying and dashboarding.
- Use the Go regclient library to interact with Nexus as an OCI/Docker registry, with `regctl` documented for diagnostics.
- Use Nexus REST APIs for repository, asset, and task metadata. Component APIs are optional targeted lookups, not a core Docker indexing source.
- Run as a Kubernetes-native application using Deployments, Jobs/CronJobs, Secrets, ConfigMaps, RBAC, Services, and standard PostgreSQL connectivity.
- Use Go for the API server, Nexus indexers, later policy simulator, and CLI tooling.
- Use TypeScript for the web UI and generated API client/types.
- Provide a web UI for visual exploration and analytics.
- Provide API endpoints for index status, artifacts, trends, operations, and reports.
- Support multiple Nexus instances with per-instance repository selection, credentials, schedules, and tuning.

### Non-goals for v1

- No deletion of artifacts by default. Controlled cleanup execution is optional, disabled by default, and requires explicit operator approval plus guardrails.
- No direct blob-store mutation.
- No blob-store filesystem or object-store scanning in v1.
- No proxy or group repository indexing in v1.
- No replacement for Nexus Repository Manager.
- No dependency on direct Nexus database access.
- No packaged PostgreSQL or database operator.
- No OIDC in the first auth implementation.
- No automatic cleanup execution. Nexus Lens can queue approved cleanup execution requests, but it must not infer or run destructive cleanup without an explicit admin request.
- No custom role editor in v1. The app starts with `admin` and `read_only`, while API responses expose named capabilities so roles can split later without changing endpoint contracts.

## Design principles

1. **Nexus remains source of truth.** Nexus APIs and registry APIs are authoritative for logical artifacts.
2. **The index is disposable.** PostgreSQL can be rebuilt from Nexus APIs and registry APIs.
3. **Read-only by default.** Indexing, analytics, verification, and simulation are non-mutating. Destructive cleanup is an opt-in execution mode with separate controls.
4. **Do not make Nexus the query engine.** Expensive analysis runs against the local index.
5. **Calculate real reclaimable bytes.** Logical image size is not enough because Docker layers are shared.
6. **Design for GitOps/platform workflows.** Reports, policy definitions, and cleanup plans should be exportable.
7. **Execution comes last.** Delete/cleanup actions require dry-run, approval, audit, and guardrails.
8. **Kubernetes first, portable second.** Production operation should use Kubernetes primitives; local development can use Docker Compose or a single binary.
9. **Typed contracts across the stack.** Go owns domain logic and API schemas; TypeScript consumes generated types rather than duplicating contracts by hand.
10. **API-only Nexus access.** Nexus Lens should work against remote Nexus and Nexus cloud-style installs where blob-store disks are not mounted.
11. **Nexus-only inventory.** Kubernetes is only the runtime/deployment environment; v1 does not inspect Kubernetes workloads or deployed image usage.

## V1 simplification boundary

The first implementation should bias toward fewer moving parts that are easy to debug:

- Polling/reconcile is the baseline. Webhooks are a later acceleration path, not required for v1 correctness.
- Defer webhook receiver/storage/API until polling, reconcile, and scheduled syncs are proven. Do not ship webhook state tables in the v1 schema.
- Full reconcile, bounded reindex, and recent sync are enough. Rebuild local index is a manual maintenance feature for later unless corruption recovery proves it is needed early.
- Docker/OCI is the only fully modeled artifact graph in v1. Hosted PyPI, npm, and Go module repositories can be asset-indexed through the generic artifact table without format-specific dependency or cleanup semantics.
- Do not model Nexus components deeply for Docker. Use Assets API for inventory and Registry API for tag/digest/layer truth; store component data only as optional raw diagnostic metadata if observed.
- Keep deletion safety with completed reconcile scopes, but avoid complex generation/swap table designs.
- Keep cleanup execution asynchronous. Store the approved dry-run candidate set, process it from the worker, lock the Nexus instance while it runs, allow cancellation, and write normalized execution events.
- Store current Docker state in hot tables and append lightweight observations for history. Do not build a full event-sourcing system in v1.
- Keep one current-state `nexus_assets` table. Do not add asset-history tables in v1.
- Defer generic source observation/event tables until a concrete debugging gap appears.
- Use existing manifest/layer tables as the digest cache first. Add a separate cross-repository `registry_digest_cache` only if profiling shows repeated digest fetches are a bottleneck.
- Use `verification_findings` for both drift and operator-facing index warnings in v1. Split `index_warnings` into its own table only if the lifecycle diverges.
- Use app-managed schedules in PostgreSQL, but keep missed-run behavior simple: skip blocked/missed runs and record the skip.
- Use rollup tables and daily snapshots before materialized views. Add materialized views only when query profiling shows they are worth the operational complexity.
- Keep full reconcile deletion confirmation repository-scoped in v1. Image/tag scopes are allowed for bounded reindex and verification, but not for broad deletion authority.

Debuggability rules:

- Every indexed row that can be questioned should point back to the run or observation that last changed it.
- Prefer append-only observations for tricky history over mutable state machines with many transitions.
- Keep the number of run types small and make every run visible in `scan_runs`.
- A failed or partial task should leave enough checkpoint/error state for an operator to answer: what was being scanned, what cursor/page/tag/digest failed, what will retry, and what data is still trustworthy.
- When in doubt, show a scoped warning instead of inferring global freshness or deletion state.

## External systems

### Nexus Repository Manager

Nexus provides:

- REST API for repositories, components, assets, tasks, and cleanup policies.
- Docker Registry HTTP API behavior for Docker repositories.
- Optional repository webhooks that can push repository events to Nexus Lens when administrators configure a Nexus webhook capability.
- Hosted repositories across formats; Docker/OCI hosted repositories are the v1 focus.
- Blob stores on disk or object storage, accessed only through Nexus APIs in v1.
- Cleanup policies and scheduled tasks.
- Prometheus metrics, mostly useful for runtime/JVM/service health rather than deep artifact analytics.

Supported Nexus scope:

- Official support starts at Nexus Repository Manager 3.88.0.
- Older Nexus 3 versions may work opportunistically but are not a compatibility target.
- Multiple Nexus instances are supported, and each instance can have different repository formats, credentials, schedules, and rate limits.
- v1 indexes hosted repositories only. Proxy/group repositories are intentionally excluded at first to avoid indexing remote upstream content and double-counting cached/grouped artifacts.
- v1 fully indexes hosted Docker/OCI repositories. Hosted PyPI, npm, and Go module repositories may be discovered and shown as unsupported/skipped until their format-specific indexers are implemented.
- Nexus may run inside or outside Kubernetes.
- Blob stores may be filesystem-backed, S3-backed, or mixed, but v1 analysis is API-driven and does not require blob-store mounts.

References:

- Nexus Automation overview: https://help.sonatype.com/en/automation.html
- Nexus REST API reference: https://help.sonatype.com/en/api-reference.html
- Nexus REST API pagination: https://help.sonatype.com/en/pagination.html
- Nexus Repositories API: https://help.sonatype.com/en/repositories-api.html
- Nexus Assets API: https://help.sonatype.com/en/assets-api.html
- Nexus Components API: https://help.sonatype.com/en/components-api.html
- Nexus Search API: https://help.sonatype.com/en/search-api.html
- Nexus Status API: https://help.sonatype.com/en/status-api.html
- Nexus Tasks API: https://help.sonatype.com/en/tasks-api.html
- Nexus Webhooks: https://help.sonatype.com/en/webhooks.html
- Nexus Repository Webhook capability: https://help.sonatype.com/en/enabling-a-repository-webhook-capability.html
- Nexus Cleanup Policies: https://help.sonatype.com/en/cleanup-policies.html
- Nexus Docker image deletion options: https://support.sonatype.com/hc/en-us/articles/360009696054-Options-to-Delete-Docker-Images-From-Nexus-Repository-3

### Sonatype API capability review

The published Sonatype help pages describe the common automation surface, while the running Nexus instance remains the authoritative contract through its built-in API reference/Swagger document. Nexus Lens should probe and record per-instance capabilities during connection setup because endpoint fields and supported formats can vary across Nexus releases.

Relevant API surfaces:

| Surface | Useful endpoints | What it gives Nexus Lens | Important limits |
| --- | --- | --- | --- |
| API reference / Swagger | Nexus UI Settings -> System -> API, published latest Swagger | Version-specific endpoint and schema discovery; useful for client generation tests and capability checks | Published latest docs may not match every configured Nexus instance |
| Pagination | `continuationToken` on paged APIs | Intra-run page checkpoints for repository walks | Tokens are next-page cursors, not a durable long-term cursor contract. If a token fails or a run is old, restart that repository walk and do not draw deletion conclusions from the partial pass. Page size is server-defined; docs have varied between versions, so do not hard-code progress totals from page size |
| Repositories API | `GET /service/rest/v1/repositories`, `GET /service/rest/v1/repositories/{repositoryName}` | Repository name, format, type, URL, approximate size, storage attributes, cleanup policy links | Includes proxy/group repos that v1 must explicitly skip |
| Assets API | `GET /service/rest/v1/assets?repository=...`, `GET /service/rest/v1/assets/{id}` | Repository asset inventory, asset IDs, paths, download URLs, checksums, file size, content type, and capability-probed optional fields such as `blobCreated`, `blobUpdated`, `lastModified`, `lastDownloaded`, `lastVerified`, `blobRef`, and `blobStoreName` | Public examples and per-version Swagger differ. Store raw asset JSON, probe fields per Nexus instance, and treat optional fields as nullable. List endpoint is a paginated repository walk, not a date-filtered changed-since feed; the OpenAPI exposes only `repository` and `continuationToken` query parameters, so do not assume server-side sorting, page-size control, or date filtering |
| Components API | `GET /service/rest/v1/components?repository=...`, `GET /service/rest/v1/components/{id}` | Logical component grouping and component-owned assets for formats where components are meaningful | Less useful than registry APIs for Docker tag/digest truth; mutating upload/delete endpoints are out of v1 scope |
| Search API | `GET /service/rest/v1/search`, `GET /service/rest/v1/search/assets`, `GET /service/rest/v1/search/assets/download` | Targeted component/asset lookup by repository, group/name/version, format-specific fields, and checksums | Search examples do not expose created/updated date filters; not a complete change feed. Do not assume arbitrary sorting such as by `lastModified` or Docker-aware fields |
| Status API | `GET /service/rest/v1/status`, `/status/writable`, `/status/check` | Health/readiness and writable/read-only state for annotating index runs safely | Gate reads on health/readability. Writable false should annotate maintenance/read-only state and may reduce expensive enrichment, but it does not automatically mean read-only indexing must stop |
| Tasks API | `GET /service/rest/v1/tasks`, `GET /service/rest/v1/tasks/{id}` | Bounded diagnostic visibility into Nexus maintenance tasks that may explain slow scans, read-only windows, cleanup activity, or blob compaction | Public docs do not show paginated task inventory or sorting. Treat as a diagnostic snapshot with optional type filters. Run/stop/create/update/delete task endpoints are mutating and should not be used in read-only v1 |
| Cleanup Policies API | cleanup policy list/detail endpoints | Later cleanup simulation can compare Nexus Lens findings with configured Nexus policy intent | v1 does not execute cleanup or mutate policies |
| Blob Store API | blob store list/detail endpoints | Blob store names, type, quotas, and grouping metadata for reporting | No blob store filesystem/object scanning in v1 |
| Repository Webhooks | Repository webhook capability posts selected repository events to a Nexus Lens URL | Optional fast-path candidate discovery; when payloads include repository, asset/component IDs, paths, or action fields, Nexus Lens can refetch via REST/registry APIs | Administrator-configured and delivery is best-effort from Nexus Lens' perspective; payload shape should be treated as version-specific and reconcile remains authoritative |
| Docker Registry API | `/v2/`, `/v2/_catalog`, `/v2/<name>/tags/list`, manifest `HEAD`/`GET`, blob `HEAD` | Docker image names, tags, manifest digests, manifest/layer sizes, immutable digest graph | Nexus Docker routing can be path-based, subdomain-based, reverse-proxied, or connector-based. Do not derive registry URLs from REST repository URLs alone. No general tags-changed-since endpoint; tag deletion/move detection still requires listing/checking tags |

Documented REST field baseline:

- Repository list/detail: model `name`, `format`, `type`, `url`, `online` when present, `storage`/attributes as raw JSON, and approximate `size` when present. Format/type-specific repository config should remain raw/capability-probed rather than hard-coded across all formats.
- Asset list: model `id`, `repository`, `format`, `path`, `downloadUrl`, and `checksum` as the stable broad inventory fields. Treat `fileSize`, `contentType`, timestamps, blob fields, and format-specific metadata as nullable because public examples and live Swagger can differ.
- Asset detail: model `id`, `repository`, `format`, `path`, `downloadUrl`, `checksum`, `contentType`, `lastModified`, `lastDownloaded`, `blobCreated`, and format-specific metadata when returned. Store the full raw JSON for fields such as `blobUpdated`, `lastVerified`, `blobRef`, and `blobStoreName` when observed.
- Component list/detail: model `id`, `repository`, `format`, `group`, `name`, `version`, `assets`, and `tags` when returned. Component-owned assets can include richer asset fields such as `contentType`, `lastModified`, `lastDownloaded`, `uploader`, `uploaderIp`, `fileSize`, `blobCreated`, `blobStoreName`, and format-specific metadata, but Docker tag/digest truth still comes from the registry API.
- Search/Search assets: model it as targeted lookup output, not a complete inventory feed. Store returned components/assets using the same nullable asset/component field handling as above.
- Tasks list/detail: model `id`, `name`, `type`, `message`, `currentState`, `lastRunResult`, `nextRun`, and `lastRun` when returned. Use task data for diagnostics and UI annotations only, not as an indexing source of truth.
- Status endpoints: model readable/writable/health state as capability and run annotations. Do not let status fields override completed reconcile evidence.

Design conclusions:

- Full reconcile should combine Repositories API, Assets API, and Docker Registry API. Components API is an optional targeted diagnostic lookup, not a core Docker indexing source.
- Assets API is the strongest Nexus REST inventory source for broad scans and timestamps. Scanning all hosted-repository assets through the Assets API is an acceptable baseline for v1 because it is paginated, checkpointable, and does not require blob-store access.
- Docker Registry API calls are the expensive enrichment path. Nexus Lens needs registry tag, manifest, config, and layer reads to calculate Docker tag-to-digest truth, logical image size, layer sharing, and API-derived estimated reclaimable bytes.
- Search API is useful for targeted repair, verification, and operator-selected scopes. It should not be the primary broad inventory mechanism.
- Webhooks should be supported as an optional candidate feed after the polling indexer exists. A webhook event should enqueue focused verification/refetch work; it should not directly mutate authoritative artifact state without follow-up reads from Nexus/registry APIs.
- Nexus status and task APIs should gate or annotate indexing. For example, pause expensive reads when Nexus health checks fail, annotate read-only/writable state, reduce expensive enrichment during maintenance windows when configured, and show Nexus cleanup/compaction tasks as possible causes of index drift or slow runs.
- The client should persist the observed Nexus version, API base URL, and capability flags per instance so UI warnings and scheduled jobs reflect what that Nexus can actually provide.

Other Nexus API surfaces seen in the published Swagger should be treated deliberately:

- Security management APIs for users, roles, privileges, realms, anonymous access, user tokens, API keys, LDAP/SAML, Crowd, certificates, SSRF protection, and secret encryption are not part of v1. Nexus Lens manages its own app users in PostgreSQL and uses Kubernetes Secret refs for Nexus credentials.
- Content selectors and routing rules are not required for v1 indexing. They may become useful later for explaining repository access policies or operator-defined scopes.
- Capabilities APIs are relevant because repository webhooks are configured as Nexus capabilities, but v1 should not create or mutate Nexus capabilities automatically. Read-only capability inspection can be added later if useful.
- Repository Browse, Components, and Nexus Tags APIs may help future explorer features, but Docker/OCI truth should come from Assets plus the Docker Registry API.
- Script APIs should not be used by Nexus Lens. They are too privileged for a read-only inventory product.
- System Nodes, Support, Recovery Mode, Data Store, Monthly Metrics, Usage History, and licensing APIs are operational surfaces. They can be shown later in diagnostics but are not needed for artifact indexing.
- Lifecycle, Firewall, Reconcile Plan, Staging, EULA, and product-specific APIs are out of v1 indexing scope unless a user explicitly wants those integrations later.

### regctl / regclient

`regctl` is the CLI from the regclient project. It can inspect tags, manifests, blobs, digests, and registry content using Docker/OCI registry APIs. Because Nexus Lens is a Go service, the preferred implementation path is to embed the Go regclient library for normal indexing and keep `regctl` as a diagnostic/spike tool.

References:

- regclient GitHub: https://github.com/regclient/regclient
- regctl usage: https://regclient.org/usage/regctl/
- regctl CLI reference: https://regclient.org/cli/regctl/

### Kubernetes runtime

Kubernetes provides:

- Native deployment primitives for Nexus Lens itself.
- Secrets and service accounts for Nexus and registry credentials.
- Deployments, Jobs, CronJobs, Services, Ingress/Gateway API, NetworkPolicies, RBAC, and ServiceMonitors.
- PostgreSQL connectivity through a Secret, including credentials from CloudNativePG-managed or externally managed databases.

Nexus Lens does not need to watch Kubernetes workload resources in v1.

## High-level architecture

```text
+-------------------+       +----------------------+       +----------------+
| Nexus REST API    | ----> | Go Indexer Workers   | ----> |                |
| Docker Registry   | ----> | Go Scheduler/Queue   | ----> | PostgreSQL     |
| Nexus APIs only   | ----> |                      |       +-------+--------+
+-------------------+       +----------+-----------+               |
                                      ^                           v
                                      |                  +----------------+
+-------------------+       +---------+------------+     | Query Layer    |
| TypeScript Web UI | <---- | Go API Server        | <-- | Rollup Tables  |
| Dashboards        |       | OpenAPI/JSON REST    |     | and Snapshots  |
| Admin/Indexing    |       | Metrics/Auth/Admin   |     +----------------+
+-------------------+       +----------------------+
```

See `nexus-lens-flow-design.md` for flow diagrams covering the simplified v1 architecture, indexing lifecycle, queue state machine, data model, freshness, and error handling. See `nexus-lens-indexing-queue-design.md` for the detailed indexing execution and PostgreSQL queue design.

## Architecture decisions

### Runtime and language split

- **Go backend:** API server, indexer workers, registry client, Nexus client, migration runner, later policy simulator, and optional CLI.
- **TypeScript frontend:** small SPA using a well-established framework, generated API client, typed form schemas where needed, and dashboard visualizations.
- **PostgreSQL datastore:** authoritative local index, run history, application configuration, rollups, and trend snapshots. It must be provided outside the Nexus Lens chart, either by CloudNativePG or any compatible managed/external PostgreSQL service.
- **OpenAPI contract:** the Go API should publish an OpenAPI spec and generate TypeScript client/types for the UI.

Recommended implementation choices:

- PostgreSQL client: `pgxpool` from `pgx`.
- SQL access pattern: `sqlc` generated Go query methods for core schema queries; hand-written query builders only for dynamic analytics filters.
- Migrations: `goose` SQL migrations embedded into the Go binary and run automatically on API startup by default, with optional manual CLI or Kubernetes Job execution.
- API router/spec: OpenAPI-first REST with generated Go server types and generated TypeScript client.
- Background work: PostgreSQL-backed work queue with `FOR UPDATE SKIP LOCKED`, advisory locks for singleton maintenance, and Kubernetes CronJobs as schedule triggers.
- Frontend: React + Vite + TypeScript by default, with generated API client, Playwright tests, and chart components fed by API response models rather than ad hoc transformation.

Frontend dependency strategy:

- Prefer a small, well-established frontend stack over a large application framework.
- Default recommendation for discussion: React + Vite + TypeScript + generated OpenAPI client + Playwright.
- Use browser-native platform features where practical: CSS modules or plain CSS, native form controls, URLSearchParams, Fetch through the generated client, and CSS variables for theming.
- Avoid adopting a heavy component framework until the UI patterns prove repetitive enough to justify it.
- If a UI primitive library is needed, prefer a headless or copy-owned approach so Nexus Lens is not tightly coupled to a large styling runtime.
- Charting should start with a small, maintained charting library only for dashboards that need it; tables and status views should use normal HTML/CSS first.
- State management should start with React state, URL state, and a small API-query helper. Do not add Redux-style global state unless the UI becomes hard to reason about.
- Keep accessibility and keyboard behavior explicit in tests, especially for modals, forms, tables, and run controls.

Repository shape:

```text
cmd/
  nexus-lens-api/
  nexus-lens-worker/
  nexus-lens/
internal/
  api/
  auth/
  config/
  db/
  indexer/
  nexus/
  registry/
  scheduler/
  queue/
  observability/
db/
  migrations/
  queries/
web/
  src/
api/
  openapi.yaml
charts/
  nexus-lens/
deploy/
  compose/
test/
  e2e/
  fixtures/
docs/
```

Monorepo rules:

- Keep Go, TypeScript, Helm, migrations, OpenAPI, and E2E fixtures in one repository.
- Generated OpenAPI clients and `sqlc` code should be checked in or verified in CI so drift is visible in review.
- Keep local development and E2E Docker Compose files under `deploy/compose`.
- Keep deterministic Nexus seed scripts and expected fixture metadata under `test/fixtures`.

### Kubernetes-native operating model

Kubernetes should be the primary production model:

- Helm chart first for installation and upgrades.
- `Deployment` for the API server.
- `Deployment` or `Job` workers for manually triggered runs.
- `CronJob` for scheduled repository, bounded reindex, trend, and verification scans.
- `Secret` references for Nexus credentials, registry auth, UI session secret, and database credentials.
- `ConfigMap` and database-backed config for Nexus instance config, repository filters, schedules, and tuning.
- External PostgreSQL configuration using an existing Secret. The database can be managed by CNPG, a cloud provider, or another Postgres operator outside this chart.
- `Service`, `Ingress`/Gateway API, and optional NetworkPolicy for UI/API access.
- `ServiceMonitor`/`PodMonitor` for Prometheus.
- `PodDisruptionBudget`, resource requests/limits, and leader election for HA-safe workers.

Nexus Lens should not require or ship a custom Kubernetes operator. Declarative configuration can be handled through Helm values, ConfigMaps, Secrets, and database-backed application config. GitOps users can manage those resources directly.

### API compatibility

- All public endpoints live under `/api/v1`.
- During alpha, breaking changes are allowed only behind explicit release notes and regenerated clients.
- Prefer additive response fields, additive enum values, and new endpoints over mutating existing contracts.
- Use cursor pagination for large collections.
- Use stable filter names and sort keys so the UI and saved links remain compatible.
- Return structured errors using a consistent problem response shape with `code`, `message`, `details`, `retryable`, and `request_id`.
- Expose the OpenAPI document at `/api/v1/openapi.json`.
- Generate and commit the TypeScript API client so UI contract drift is visible in review.
- Add API contract tests that compare generated server routes and generated client types against the committed OpenAPI spec.

## Components

### 1. API server

Responsibilities:

- Serve web UI API.
- Manage index runs.
- Expose repository, image, tag, blob, and trend queries.
- Expose Prometheus metrics generated from the index.
- Manage configuration for Nexus connections, schedules, repositories, and credentials.

Possible implementation:

- Go HTTP API.
- REST first using generated OpenAPI; GraphQL optional later.
- Built-in basic login/session auth first.
- Roles: admin and read-only.
- Kubernetes-aware config loading from Secrets and ConfigMaps for deployment settings.
- Server-side pagination, filtering, and sorting for all large tables.

### 2. Indexer workers

Responsibilities:

- Crawl Nexus REST APIs.
- Use Go regclient to inspect Docker/OCI registry content.
- Normalize metadata into PostgreSQL.
- Maintain cursors and scan state.
- Reconcile Nexus REST API and registry API views.

Indexer types:

- Repository indexer.
- Nexus asset indexer.
- Docker tag indexer.
- Docker manifest/layer indexer.
- Trend snapshotter.

Implementation notes:

- Write workers in Go.
- Prefer embedded regclient over shelling out during normal operation.
- Use a durable run queue in PostgreSQL for manual runs and retries.
- Use Kubernetes Jobs/CronJobs for coarse scheduling and the PostgreSQL queue for all scalable indexing work distribution.
- Use idempotent upserts and run-scoped tombstone confirmation before marking records deleted.

### 3. PostgreSQL

PostgreSQL is the primary datastore.

Database ownership:

- Nexus Lens does not run its own database operator.
- Production installs must point at an existing PostgreSQL service managed outside the Nexus Lens chart.
- CloudNativePG is supported as one external database provider, but Nexus Lens does not install or manage CNPG resources.
- The Helm chart should accept an existing database Secret and service host/port.
- Application migrations run from the Go service or a Kubernetes Job, not from a database operator.

Connection and auth:

- Use `pgxpool` with bounded pool sizes per process.
- Configure pool limits separately for API and workers.
- Read host, port, database, username, password, SSL mode, and optional CA cert from Kubernetes Secret references.
- Support password rotation by periodically rebuilding the pool or restarting pods through checksum annotations on Secret changes.
- Use separate database roles where practical:
  - `nexus_lens_app`: API read/write access to application tables.
  - `nexus_lens_worker`: index write access and rollup refresh access.
  - `nexus_lens_migrator`: schema migration ownership.
- For smaller installs, one application role is acceptable if the Secret is controlled by CNPG.

Schema and migrations:

- Use `goose` for versioned SQL migrations.
- Store migrations under `db/migrations` and embed them into the Go binary.
- Run migrations automatically on API startup by default.
- Also provide `nexus-lens migrate up` for local/manual use and an optional Kubernetes migration Job for deployments that prefer explicit migration control.
- Use a PostgreSQL advisory lock around migrations.
- API pods must wait on the advisory migration lock rather than racing schema changes.
- Every schema change must include forward migration, rollback notes, and query updates.
- Treat migrations as part of the API compatibility surface once `/api/v1` is used by external clients.
- Use normal tables for ingest state and explicit rollup tables for dashboard summaries. Materialized views are deferred until profiling shows a clear need.

Why Postgres over OpenSearch for v1:

- Artifact metadata is relational.
- Docker layer sharing requires joins and graph-like queries.
- Cleanup simulation requires deterministic relational logic.
- Trends can be modeled with time-series snapshots.
- JSONB and GIN indexes handle flexible metadata.
- Rollup tables and snapshots can power dashboards without materialized-view refresh complexity.

Optional extensions:

- `pg_trgm` for fuzzy image/tag search.
- `btree_gin` for mixed filtering.
- TimescaleDB optional for long-term trend data.

Extensions must be optional. The base product must run on stock PostgreSQL without requiring superuser privileges or extension installation. When extensions are unavailable, Nexus Lens should fall back to normal btree indexes and application-level search behavior.

Query design:

- Use `sqlc` for stable CRUD and reconciliation queries.
- Use explicit SQL files for analytics rollups and reclaimable-byte calculations.
- Use cursor pagination based on stable keys, not offset pagination, for large tables.
- Use `jsonb` for raw source payloads and provider-specific metadata, but promote frequently filtered fields into typed columns.
- Partition high-volume history tables by time only if profiling shows it is necessary.
- Refresh rollup tables asynchronously after index runs; the API should expose both raw freshness and rollup freshness.

Join and analytics performance:

- Treat the Docker manifest/layer graph as a high-cardinality join area. Do not run full graph joins directly on every dashboard request.
- Precompute common rollups after index runs:
  - bytes by repository.
  - bytes by image.
  - tag counts by image and pattern.
  - unique/shared layer byte estimates by repository/image.
  - freshness and verification summaries.
- Prefer explicit rollup tables for dashboard cards and charts. Materialized views are a later optimization when full refresh cost and staleness semantics are understood.
- For unique reclaimable-byte analysis, scope queries tightly by candidate set first, then join to manifests/layers. Avoid starting from all layers.
- Keep join keys typed and indexed: `repository_id`, `image_id`, `manifest_id`, `layer_id`, and digest columns.
- Avoid joining on `jsonb` fields in hot paths. Extract frequently filtered metadata into typed columns.
- Avoid unbounded `count(*)` over large joined tables in interactive endpoints. Use rollups or bounded estimates where exact counts are not required.
- Use keyset pagination for explorer tables. Common cursors should include stable IDs plus the selected sort key.
- Add `EXPLAIN (ANALYZE, BUFFERS)` review for heavy analytics queries before release.
- Add query budget tests for the large seed profile so regressions are visible in CI/nightly runs.

Postgres operational tuning:

- Keep API and worker connection pools small and explicit; worker replicas should not multiply into more connections than Postgres can handle.
- Prefer PgBouncer only if the deployment already provides it; do not require it.
- Set statement timeouts for API requests so bad analytics queries fail fast.
- Use longer but bounded timeouts for worker maintenance queries.
- Monitor slow queries, lock waits, dead tuples, index bloat, autovacuum activity, and rollup refresh duration.
- Large write batches should be chunked to avoid long transactions and excessive WAL spikes.
- Upserts should update only changed columns where practical to reduce bloat.

OpenSearch can be added later for free-text search across metadata, annotations, build labels, SBOMs, logs, or vulnerability data.

### 5. Web UI

Implementation:

- TypeScript SPA.
- Generated API client from the Go OpenAPI spec.
- Schema-driven settings forms backed by generated API models. Policy forms come in a later cleanup-simulation phase.
- Dense operational UI optimized for tables, filters, drilldowns, and repeat use.
- Simple Kibana-like layout: persistent left navigation, compact top header, global search/filter where useful, content pages built around tables, charts, and detail flyouts.
- Avoid marketing-style screens. The first screen after login should be the operational overview.

Default ports:

- API/UI HTTP: `5601`, matching the familiar Kibana-style operational UI convention.
- Metrics: same server at `/metrics` by default; optionally expose a separate metrics port `9090` only if operators need it.
- Container listens on `0.0.0.0:5601`.
- Helm Service port defaults to `5601`.

Management features:

- Configure Nexus instances, credential Secret references, repository include/exclude filters, and rate limits.
- Configure schedules for recent sync, bounded reindex, full reconciliation, trend snapshots, and verification.
- Start, pause, resume, cancel, and retry index runs.
- Start bounded reindex runs for common whole-day windows such as last 7/30/90 days or a custom day count.
- Inspect queue depth, active tasks, failed tasks, and stale locks.
- View per-repository freshness, last success, last error, and verification findings.
- Manage indexing settings and later cleanup policy simulations.
- Export reports and filtered tables as CSV/JSON.
- Show read-only safety status clearly: no deletion controls in v1.

Initial pages:

- Overview dashboard.
- Repository explorer.
- Docker image/tag explorer.
- Tag pattern analytics.
- Storage and growth trends.
- Reclaimable storage dashboard.
- Index run status and queue monitor.
- Admin/settings view.
- Verification findings view.

Navigation:

```text
Overview
Repositories
Images & Tags
Tag Patterns
Storage
Trends
Indexing
  Runs
  Running Tasks
  Queue
  Verification
Admin
  Nexus Instances
  API Capabilities
  Schedules
  Rate Limits
  Users
  Retention
  Settings
```

Page design:

- **Overview:** storage totals, tag totals, freshness, failed runs, verification warnings, top growth, top reclaimable candidates.
- **Repositories:** repository table with format/type/status, Nexus asset bytes, Docker logical bytes, image count, tag count, freshness badges, last error.
- **Images & Tags:** main explorer for image/tag rows with filters, saved URL state, and tag detail flyout.
- **Tag Patterns:** top patterns, unclassified explorer, suggested pattern candidates, editable patterns, reclassification action.
- **Storage:** Nexus asset bytes, Docker logical bytes, unique/shared estimates, largest repositories/images/tags, estimated reclaimable candidates.
- **Trends:** daily snapshot charts with repository/image comparisons and missing-snapshot gaps.
- **Indexing/Runs:** run list, active progress, phase, elapsed time, estimated remaining when available, controls.
- **Indexing/Running Tasks:** live worker/task view showing the "10 jobs running" style status: task type, subject, worker, attempt, elapsed, heartbeat, progress label.
- **Indexing/Queue:** queue depth by status/type, oldest pending, retryable/failed tasks, stale leases, bulk retry controls.
- **Indexing/Verification:** findings, severity, source, expected/actual, status, run link, verify/retry actions.
- **Admin/Nexus Instances:** Nexus URLs, hosted repo filters, credential status, basic auth Secret refs.
- **Admin/API Capabilities:** detected Nexus version, available REST surfaces, timestamp support, pagination behavior, last capability probe, and warnings for unsupported assumptions.
- **Admin/Schedules:** recent sync, bounded reindex, full reconcile, verification, trend snapshot schedules.
- **Admin/Rate Limits:** per-Nexus request limits, concurrency, circuit breaker state.
- **Admin/Users:** Postgres-backed users, roles, disabled state, password reset.
- **Admin/Retention:** configurable retention values and next retention run.

Freshness contract:

- **Asset inventory freshness:** last completed Assets API walk or scoped asset refresh.
- **Tag-list freshness:** last completed registry tag listing for the image/repository scope.
- **Manifest/layer enrichment freshness:** last completed manifest/config/layer enrichment for current tag digests.
- **Verification freshness:** last source-system comparison for sampled or scoped records.
- **Rollup freshness:** last rollup or trend snapshot refresh.

UI freshness states:

- `fresh`: within configured SLO.
- `stale`: outside configured SLO but previous data is available.
- `partial`: only part of the selected scope completed.
- `unknown`: no successful baseline yet.
- `blocked`: blocked by lock, rate limit, auth, health, or repeated failures.
- `needs_full_reconcile`: candidate scans cannot prove broad freshness or deletion state.

Dashboard labels:

- Use "Nexus asset bytes" for REST asset `fileSize` totals.
- Use "Docker logical bytes" for manifest-derived config/layer descriptor totals.
- Use "Estimated reclaimable bytes" only when Docker enrichment is fresh enough for the selected scope.
- Show stale or partial badges near storage and reclaimable charts when enrichment or rollups lag behind asset inventory.

Indexing progress UI:

- The top of the Indexing page should show active runs with compact progress bars.
- Progress bars can be indeterminate when total work is unknown.
- Always show concrete counters even when percent is unknown: tasks completed, running, pending, retryable, failed.
- Show elapsed time prominently; show ETA only when `progress_confidence` is `measured` or good `estimated`.
- Running task rows should read like operational jobs: worker ID, Kubernetes pod name when available, task type, repository/image subject, attempt, elapsed time, heartbeat age, and current progress label.
- Failed rows should show the error summary, attempt count, next retry time, and actions: retry task, retry failed tasks in run, cancel run, restart run.
- Restart support should distinguish "retry failed" from "restart run with same scope".

Indexing action confirmations:

- Full reconcile must require a confirmation modal.
- The modal should explain that the operation can take a long time, may increase Nexus API load, blocks other indexing operations for that Nexus instance, and may make progress estimates uncertain.
- The modal should show the selected Nexus instance, repository scope, current rate limits, active schedules that will be skipped/blocked, and estimated task count when available.
- Full reconcile can use a normal confirmation if scope is narrow, but should require typing the Nexus instance name when all repositories are selected.
- Bounded reindex should show a lighter confirmation explaining that it is partial-confidence, scoped to a time window, and does not confirm deletions outside the scope.
- Scheduled bounded reindex settings should explain that Nexus may not expose every change by timestamp; the schedule refreshes likely recent candidates and does not replace full reconcile.
- Disabled actions should show a clear reason, such as "Blocked by full reconcile run #123 for nexus-main."
- The UI should prefer "Schedule" and "Run now" as separate actions so operators understand whether they are changing recurring behavior or starting immediate work.

Useful visualizations:

- Repo storage treemap for current size distribution.
- Growth over time by repo/image/team.
- Tag age histogram.
- Last-pulled histogram.
- Tag pattern histogram.
- Tag pattern growth chart.
- Unique vs shared bytes chart.
- API-derived logical storage and shared-layer estimates.
- Index freshness heatmap by repository.
- Index run timeline with task phases and error markers.
- Nexus throttle/backoff chart.
- Verification finding trend.
- Top movers table showing images with largest growth over selected period.

## Data model draft

The SQL snippets define the logical schema. Migration files should order table creation so foreign keys reference existing tables, or add some constraints after all base tables exist.

### nexus_instances

```sql
create table nexus_instances (
  id bigserial primary key,
  name text not null unique,
  base_url text not null,
  auth_secret_ref text,
  nexus_version text,
  api_base_path text,
  api_capabilities jsonb not null default '{}',
  last_capability_probe_at timestamptz,
  metadata jsonb not null default '{}',
  first_seen_at timestamptz not null default now(),
  last_seen_at timestamptz not null default now()
);
```

### repositories

```sql
create table repositories (
  id bigserial primary key,
  nexus_instance_id bigint not null references nexus_instances(id),
  name text not null,
  format text not null,
  type text not null,
  url text,
  registry_base_url text,
  registry_routing_mode text,
  registry_validated_at timestamptz,
  blob_store_name text,
  online boolean,
  metadata jsonb not null default '{}',
  first_seen_at timestamptz not null default now(),
  last_seen_at timestamptz not null default now(),
  unique (nexus_instance_id, name)
);
```

### docker_images

```sql
create table docker_images (
  id bigserial primary key,
  repository_id bigint not null references repositories(id),
  name text not null,
  first_seen_at timestamptz not null default now(),
  last_seen_at timestamptz not null default now(),
  unique (repository_id, name)
);
```

### docker_tags

```sql
create table docker_tags (
  id bigserial primary key,
  image_id bigint not null references docker_images(id),
  tag text not null,
  manifest_digest text,
  pushed_at timestamptz,
  last_downloaded_at timestamptz,
  last_downloaded_confidence text not null default 'unknown',
  first_seen_at timestamptz not null default now(),
  last_seen_at timestamptz not null default now(),
  last_seen_full_reconcile_run_id bigint,
  missing_in_full_reconcile_run_id bigint,
  deleted_confirmed_run_id bigint,
  delete_source text,
  deleted_at timestamptz,
  metadata jsonb not null default '{}',
  unique (image_id, tag)
);
```

### docker_tag_observations

```sql
create table docker_tag_observations (
  id bigserial primary key,
  tag_id bigint not null references docker_tags(id),
  scan_run_id bigint references scan_runs(id),
  manifest_digest text,
  observed_at timestamptz not null default now(),
  observation_type text not null,
  metadata jsonb not null default '{}',
  unique (tag_id, scan_run_id, observation_type)
);
```

Tag observation rules:

- `docker_tags.manifest_digest` is a denormalized pointer to the current observed digest.
- Append observations for `seen`, `digest_changed`, `missing`, `deleted_confirmed`, and `recreated`.
- This is simpler than maintaining current/closed version intervals in v1, while still preserving enough history to debug tag moves and deletes.
- If later analytics need exact interval semantics, derive or migrate to a `docker_tag_versions` table.

### docker_tag_patterns

```sql
create table docker_tag_patterns (
  id bigserial primary key,
  name text not null unique,
  description text,
  regex text not null,
  category text not null,
  priority int not null default 100,
  builtin boolean not null default false,
  enabled boolean not null default true,
  metadata jsonb not null default '{}',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
```

### docker_tag_pattern_matches

```sql
create table docker_tag_pattern_matches (
  tag_id bigint not null references docker_tags(id),
  pattern_id bigint not null references docker_tag_patterns(id),
  matched_text text not null,
  captures jsonb not null default '{}',
  matched_at timestamptz not null default now(),
  primary key (tag_id, pattern_id)
);
```

Built-in pattern examples:

```text
semver:              ^v?\d+\.\d+\.\d+([-.+][0-9A-Za-z.-]+)?$
major_minor:         ^v?\d+\.\d+$
sha_short:           ^[a-f0-9]{7,12}$
sha_long:            ^[a-f0-9]{40}$
pr_number:           ^pr[-_]\d+([-.][0-9A-Za-z.-]+)?$
feature_branch:      ^(feat|feature)[-_][A-Za-z0-9._-]+$
bugfix_branch:       ^(fix|bugfix|hotfix)[-_][A-Za-z0-9._-]+$
ticket_branch:       ^[A-Z]+-\d+[-_][A-Za-z0-9._-]+$
environment:         ^(dev|qa|test|stage|staging|prod|production)$
channel:             ^(latest|stable|edge|nightly|canary)$
date:                ^\d{4}[.-]?\d{2}[.-]?\d{2}$
build_number:        ^build[-_]?\d+$
opaque_generated:    ^[A-Za-z0-9._-]{16,}$
```

Tags may match multiple patterns. Analytics should report both highest-priority category and all matched patterns so users can see overlaps like `feat-dvo-1313-hsdfjw` matching both `feature_branch` and `ticket_branch`-style conventions if configured.

### docker_manifests

```sql
create table docker_manifests (
  id bigserial primary key,
  repository_id bigint not null references repositories(id),
  digest text not null,
  media_type text,
  config_digest text,
  logical_size_bytes bigint,
  first_seen_at timestamptz not null default now(),
  last_seen_at timestamptz not null default now(),
  metadata jsonb not null default '{}',
  unique (repository_id, digest)
);
```

### docker_layers

```sql
create table docker_layers (
  id bigserial primary key,
  repository_id bigint not null references repositories(id),
  digest text not null,
  media_type text,
  size_bytes bigint,
  first_seen_at timestamptz not null default now(),
  last_seen_at timestamptz not null default now(),
  unique (repository_id, digest)
);
```

### docker_manifest_layers

```sql
create table docker_manifest_layers (
  manifest_id bigint not null references docker_manifests(id),
  layer_id bigint not null references docker_layers(id),
  ordinal int not null,
  primary key (manifest_id, layer_id, ordinal)
);
```

### registry digest caching

Use `docker_manifests` and `docker_layers` as the v1 digest cache. A separate `registry_digest_cache` table is deferred until profiling shows repeated cross-repository digest fetches are materially expensive.

Digest cache rules:

- Before fetching a manifest or layer descriptor, check for an existing repository-scoped row with matching digest and media type.
- Keep a small in-process negative cache for short-lived 404/manifest-unknown results during a worker run.
- Add a durable cross-repository digest cache later only if repeated digest fetches are a measured bottleneck.

### nexus_assets

```sql
create table nexus_assets (
  id bigserial primary key,
  repository_id bigint not null references repositories(id),
  nexus_asset_id text,
  path text,
  download_url text,
  content_type text,
  size_bytes bigint,
  blob_ref text,
  blob_store_name text,
  checksum_sha1 text,
  checksum_sha256 text,
  last_downloaded_at timestamptz,
  blob_created_at timestamptz,
  blob_updated_at timestamptz,
  last_modified_at timestamptz,
  last_verified_at timestamptz,
  first_seen_at timestamptz not null default now(),
  last_seen_at timestamptz not null default now(),
  last_seen_full_reconcile_run_id bigint,
  missing_in_full_reconcile_run_id bigint,
  deleted_confirmed_run_id bigint,
  metadata jsonb not null default '{}',
  unique (repository_id, nexus_asset_id)
);
```

### Deferred generic storage object model

Defer this generic abstraction until non-Docker formats or cleanup simulation need it. For Docker-first v1, use `nexus_assets`, `docker_manifests`, `docker_layers`, and `docker_manifest_layers` directly. This keeps the first schema easier to query and debug.

### app_users

```sql
create table app_users (
  id bigserial primary key,
  username text not null unique,
  password_hash text not null,
  role text not null,
  disabled boolean not null default false,
  force_password_change boolean not null default false,
  last_login_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
```

### app_sessions

```sql
create table app_sessions (
  id uuid primary key,
  user_id bigint not null references app_users(id),
  created_at timestamptz not null default now(),
  expires_at timestamptz not null,
  revoked_at timestamptz,
  last_seen_at timestamptz,
  user_agent text,
  ip_hash text
);
```

### audit_log

```sql
create table audit_log (
  id bigserial primary key,
  actor_user_id bigint references app_users(id),
  action text not null,
  subject_type text not null,
  subject_id text,
  created_at timestamptz not null default now(),
  metadata jsonb not null default '{}'
);
```

Initial admin bootstrap:

- App users are stored in PostgreSQL.
- PostgreSQL and Nexus credentials come only from Kubernetes Secret references configured through Helm values.
- On first startup, if no `admin` user exists, the API creates one from a configured bootstrap Secret.
- The bootstrap Secret should contain `username` and either `password` or `passwordHash`.
- After successful bootstrap, production values should require the admin to rotate the password unless a hash-only bootstrap mode is used. This should be configurable for local development and CI.
- If users already exist, bootstrap config is ignored and logged without exposing values.
- Password hashes should use Argon2id by default with configurable memory, iteration, parallelism, salt length, and key length parameters. Go's standard library does not include a password hashing primitive; use `golang.org/x/crypto/argon2` or a small vetted wrapper around it.
- Store password hashes in a PHC-style encoded string so future parameter upgrades and algorithm migration are possible per user.
- v1 roles are global only: `admin` and `readonly`. Per-Nexus or per-repository authorization can be added later if needed.
- Admin actions such as user changes, schedule changes, credential-alias changes, and run control actions should write audit log entries.

### indexing_schedules

```sql
create table indexing_schedules (
  id bigserial primary key,
  nexus_instance_id bigint not null references nexus_instances(id),
  scan_type text not null,
  name text not null,
  enabled boolean not null default true,
  schedule_expr text not null,
  timezone text not null default 'UTC',
  scope jsonb not null default '{}',
  max_runtime interval,
  lock_conflict_behavior text not null default 'skip_blocked',
  next_run_at timestamptz,
  last_run_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (nexus_instance_id, name)
);
```

### scan_runs

```sql
create table scan_runs (
  id bigserial primary key,
  nexus_instance_id bigint not null references nexus_instances(id),
  scan_type text not null,
  repository_name text,
  trigger_source text not null default 'manual',
  trigger_job_name text,
  status text not null,
  phase text,
  started_at timestamptz not null default now(),
  finished_at timestamptz,
  last_heartbeat_at timestamptz,
  estimated_total_tasks bigint,
  completed_tasks bigint not null default 0,
  failed_tasks bigint not null default 0,
  progress_percent numeric,
  progress_confidence text not null default 'unknown',
  cursor_before jsonb,
  cursor_after jsonb,
  stats jsonb not null default '{}',
  error_code text,
  error_class text,
  error_message text,
  error_details jsonb not null default '{}'
);
```

### scan_run_scopes

```sql
create table scan_run_scopes (
  id bigserial primary key,
  scan_run_id bigint not null references scan_runs(id),
  nexus_instance_id bigint not null references nexus_instances(id),
  repository_id bigint references repositories(id),
  image_id bigint references docker_images(id),
  scope_type text not null,
  scope_key text not null,
  status text not null default 'pending',
  asset_walk_completed boolean not null default false,
  tag_walk_completed boolean not null default false,
  enrichment_completed boolean not null default false,
  cursor_exhausted boolean not null default false,
  invalid_cursor_seen boolean not null default false,
  started_at timestamptz,
  finished_at timestamptz,
  stats jsonb not null default '{}',
  unique (scan_run_id, scope_type, scope_key)
);
```

Scope completion rules:

- V1 should use repository-level scopes for full reconcile. Image-level scopes are reserved for bounded reindex, verification, or explicit operator actions.
- Delete confirmation is allowed only for a scope with `status = 'succeeded'`, `cursor_exhausted = true`, and the required walk phases completed for that scan type.
- A partial, canceled, timed-out, invalid-cursor, or budget-paused scope must not mark missing tags/assets deleted.
- `last_seen_full_reconcile_run_id`, `missing_in_full_reconcile_run_id`, and `deleted_confirmed_run_id` on indexed rows point back to completed scopes/runs so deletion decisions are auditable.
- Full reconcile can succeed for some scopes and fail for others. UI and verification must show scope-level status instead of collapsing the whole Nexus into one binary result.

### scan_tasks

```sql
create table scan_tasks (
  id bigserial primary key,
  scan_run_id bigint not null references scan_runs(id),
  nexus_instance_id bigint not null references nexus_instances(id),
  task_type text not null,
  task_key text not null,
  repository_name text,
  subject text,
  cost_class text not null default 'default',
  estimated_requests int,
  status text not null default 'pending',
  priority int not null default 0,
  attempt int not null default 0,
  max_attempts int not null default 5,
  available_at timestamptz not null default now(),
  lease_expires_at timestamptz,
  lock_token uuid,
  locked_by text,
  locked_at timestamptz,
  started_at timestamptz,
  finished_at timestamptz,
  last_heartbeat_at timestamptz,
  progress_current bigint,
  progress_total bigint,
  progress_label text,
  payload jsonb not null default '{}',
  checkpoint jsonb not null default '{}',
  stats jsonb not null default '{}',
  error_code text,
  error_class text,
  error_message text,
  error_details jsonb not null default '{}',
  created_at timestamptz not null default now(),
  unique (scan_run_id, task_type, task_key)
);
```

### scan_task_attempts

```sql
create table scan_task_attempts (
  id bigserial primary key,
  scan_task_id bigint not null references scan_tasks(id),
  attempt int not null,
  worker_id text,
  worker_pod_name text,
  lock_token uuid,
  started_at timestamptz not null default now(),
  finished_at timestamptz,
  status text not null,
  error_code text,
  error_class text,
  error_message text,
  error_details jsonb not null default '{}',
  stats jsonb not null default '{}'
);
```

### nexus_operation_locks

```sql
create table nexus_operation_locks (
  nexus_instance_id bigint primary key references nexus_instances(id),
  scan_run_id bigint not null references scan_runs(id),
  operation_type text not null,
  lock_level text not null,
  acquired_at timestamptz not null default now(),
  expires_at timestamptz,
  reason text
);
```

Operation lock rules:

- `full_reconcile` and `bounded_reindex` acquire an exclusive per-Nexus operation lock.
- Recent sync, bounded reindex, and full reconcile must not start when an exclusive lock exists for the same Nexus.
- Verification and trend snapshots can either skip or run against the last completed state; they should not compete with an active exclusive operation by default.
- Locks are released when the owning run reaches a terminal state.
- Stale locks are recoverable if the owning run is stale and no running tasks still hold leases.
- UI and API should display the blocking run when an action is unavailable.

### index_checkpoints

```sql
create table index_checkpoints (
  id bigserial primary key,
  nexus_instance_id bigint not null references nexus_instances(id),
  repository_name text,
  checkpoint_type text not null,
  subject text not null default '',
  cursor jsonb not null default '{}',
  high_watermark timestamptz,
  last_successful_scan_run_id bigint references scan_runs(id),
  updated_at timestamptz not null default now(),
  unique (nexus_instance_id, repository_name, checkpoint_type, subject)
);
```

### Deferred source observation log

Do not create a generic `source_observations` table in v1. The current-state tables, `docker_tag_observations`, `scan_task_attempts`, raw asset JSON, and structured task errors provide enough debugging surface for the first implementation. Add a generic source observation/event table only if operators cannot answer a concrete support question from those records.

### Deferred webhook storage

Do not ship webhook receiver/storage/API in v1. Polling, full reconcile, bounded reindex, verification, and app-managed schedules are the authoritative indexing path.

When webhook-assisted sync is added later, it should:

- Provide a per-instance webhook URL and shared-secret validation for the `X-Nexus-Webhook-Signature` header when configured.
- Store `X-Nexus-Webhook-Delivery` as the preferred idempotency key when present and fall back to payload hash.
- Treat payloads as hints that enqueue focused candidate tasks, not as authoritative artifact updates.
- Retain payloads for a short configurable window for audit/debugging, then delete them by retention.
- Surface repeated processing failures as `verification_findings` instead of blocking all indexing.

### verification_findings

```sql
create table verification_findings (
  id bigserial primary key,
  nexus_instance_id bigint references nexus_instances(id),
  scan_run_id bigint references scan_runs(id),
  severity text not null,
  finding_type text not null,
  repository_name text,
  subject text,
  expected jsonb,
  actual jsonb,
  message text,
  remediation text,
  status text not null default 'open',
  created_at timestamptz not null default now(),
  last_seen_at timestamptz not null default now(),
  resolved_at timestamptz,
  dismissed_at timestamptz,
  metadata jsonb not null default '{}'
);
```

`verification_findings` covers both source-system drift and operator-facing index warnings in v1. Split warnings into a separate table only if they need a materially different lifecycle later.

Finding taxonomy:

- `partial_bounded_reindex`
- `stale_asset_inventory`
- `stale_docker_enrichment`
- `stale_rollups`
- `missing_last_downloaded_metadata`
- `unsupported_repository_format`
- `capability_missing`
- `blocked_schedule`
- `nexus_throttling`
- `verification_drift`
- `cursor_restarted`
- `delete_confirmation_blocked`
- `full_reconcile_budget_paused`

### policies

```sql
create table policies (
  id bigserial primary key,
  name text not null unique,
  format text not null default 'docker',
  enabled boolean not null default true,
  definition_yaml text not null,
  created_by text,
  updated_by text,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
```

### trend_snapshots

```sql
create table trend_snapshots (
  id bigserial primary key,
  snapshot_date date not null,
  captured_at timestamptz not null default now(),
  nexus_instance_id bigint not null references nexus_instances(id),
  repository_id bigint references repositories(id),
  image_id bigint references docker_images(id),
  metric_name text not null,
  metric_value numeric not null,
  dimensions jsonb not null default '{}',
  unique nulls not distinct (snapshot_date, nexus_instance_id, repository_id, image_id, metric_name, dimensions)
);
```

`UNIQUE NULLS NOT DISTINCT` requires PostgreSQL 15+. If Nexus Lens supports older PostgreSQL versions later, use generated sentinel columns for nullable dimensions instead of relying on ordinary nullable unique constraints.

### Index and constraint guidance

Minimum useful indexes:

```sql
create index nexus_instances_capability_probe_idx on nexus_instances (last_capability_probe_at);
create index app_sessions_user_idx on app_sessions (user_id, expires_at) where revoked_at is null;
create index audit_log_created_idx on audit_log (created_at desc);
create index indexing_schedules_due_idx on indexing_schedules (enabled, next_run_at) where enabled;
create index repositories_nexus_format_idx on repositories (nexus_instance_id, format);
create index repositories_nexus_name_idx on repositories (nexus_instance_id, name);
create index docker_images_repo_name_idx on docker_images (repository_id, name);
create index docker_images_name_idx on docker_images (name);
create index docker_tags_image_tag_idx on docker_tags (image_id, tag);
create index docker_tags_image_active_tag_idx on docker_tags (image_id, deleted_at, tag);
create index docker_tags_image_active_seen_idx on docker_tags (image_id, deleted_at, last_seen_at desc, id);
create index docker_tags_image_last_seen_idx on docker_tags (image_id, last_seen_at);
create index docker_tags_manifest_digest_idx on docker_tags (manifest_digest) where deleted_at is null;
create index docker_tags_last_seen_idx on docker_tags (last_seen_at);
create index docker_tags_deleted_idx on docker_tags (deleted_at) where deleted_at is not null;
create index docker_tags_tag_idx on docker_tags (tag);
create index docker_tag_observations_tag_seen_idx on docker_tag_observations (tag_id, observed_at desc);
create index docker_tag_observations_digest_idx on docker_tag_observations (manifest_digest);
create index docker_tag_patterns_enabled_idx on docker_tag_patterns (enabled, priority);
create index docker_tag_pattern_matches_pattern_idx on docker_tag_pattern_matches (pattern_id);
create index docker_tag_pattern_matches_tag_idx on docker_tag_pattern_matches (tag_id);
create index docker_manifests_repo_digest_idx on docker_manifests (repository_id, digest);
create index docker_manifests_digest_idx on docker_manifests (digest);
create index docker_layers_repo_digest_idx on docker_layers (repository_id, digest);
create index docker_layers_digest_idx on docker_layers (digest);
create index docker_manifest_layers_layer_idx on docker_manifest_layers (layer_id, manifest_id);
create index nexus_assets_blob_ref_idx on nexus_assets (blob_ref);
create index nexus_assets_repo_path_idx on nexus_assets (repository_id, path);
create index nexus_assets_repo_last_modified_idx on nexus_assets (repository_id, last_modified_at desc);
create index nexus_assets_repo_blob_created_idx on nexus_assets (repository_id, blob_created_at desc);
create index nexus_assets_repo_blob_updated_idx on nexus_assets (repository_id, blob_updated_at desc);
create index nexus_assets_repo_last_downloaded_idx on nexus_assets (repository_id, last_downloaded_at desc);
create index nexus_assets_blob_store_idx on nexus_assets (blob_store_name);
create index scan_runs_instance_status_started_idx on scan_runs (nexus_instance_id, status, started_at desc);
create index scan_runs_active_idx on scan_runs (status, started_at desc) where status in ('pending', 'running', 'pausing', 'canceling');
create index scan_runs_error_class_idx on scan_runs (nexus_instance_id, error_class, started_at desc) where error_class is not null;
create index scan_run_scopes_run_status_idx on scan_run_scopes (scan_run_id, status, scope_type);
create index scan_run_scopes_repo_idx on scan_run_scopes (nexus_instance_id, repository_id, status);
create index scan_tasks_claim_idx on scan_tasks (nexus_instance_id, cost_class, priority desc, available_at, id) where status in ('pending', 'retryable');
create index scan_tasks_lease_idx on scan_tasks (lease_expires_at, id) where status = 'running';
create index scan_tasks_run_status_idx on scan_tasks (scan_run_id, status, task_type);
create index scan_tasks_run_running_idx on scan_tasks (scan_run_id, locked_by, started_at) where status = 'running';
create index scan_tasks_status_created_idx on scan_tasks (status, created_at);
create index scan_tasks_error_class_idx on scan_tasks (nexus_instance_id, error_class, status, created_at desc) where error_class is not null;
create index scan_task_attempts_task_idx on scan_task_attempts (scan_task_id, attempt desc);
create index scan_task_attempts_error_class_idx on scan_task_attempts (error_class, started_at desc) where error_class is not null;
create index nexus_operation_locks_run_idx on nexus_operation_locks (scan_run_id);
create index index_checkpoints_lookup_idx on index_checkpoints (nexus_instance_id, repository_name, checkpoint_type, subject);
create index verification_findings_open_idx on verification_findings (nexus_instance_id, status, severity, last_seen_at desc);
create index trend_snapshots_lookup_idx on trend_snapshots (metric_name, snapshot_date, nexus_instance_id, repository_id);
```

Optional extension-backed indexes can be added when the database allows them, such as a `pg_trgm` GIN index for fuzzy image search. The base migrations must not require extensions.

## Registry interaction

### Nexus Docker routing

Nexus Docker registry endpoints must be configured and probed per repository. Nexus may expose Docker repositories through path-based routing, subdomain routing, reverse proxy rules, or connector ports. Nexus Lens must not assume the REST repository `url` is directly usable as a Docker registry reference.

Repository configuration should store:

- `registry_base_url`: the base Docker registry URL to use with regclient.
- `registry_routing_mode`: `path`, `subdomain`, `connector`, `reverse_proxy`, or `custom`.
- `registry_validated_at`: last successful probe time.

Validation steps:

1. Probe `/v2/` on the configured registry base URL.
2. Attempt tag listing for a known or discovered image when available.
3. Attempt manifest `HEAD`/`GET` for a sampled tag.
4. Store failures as capability warnings with the exact route that failed.

The UI should let operators configure or override the registry base URL per repository and show the last validation result.

### Go regclient usage in MVP

The MVP should embed the Go regclient library for production indexing. This keeps authentication, concurrency, retries, structured errors, and testing inside the Go process. `regctl` remains useful for local spikes and troubleshooting.

`regctl` should not be a production execution fallback in v1. Nexus Lens should use the Go library path for production so auth, retries, logging, rate limits, and error classification stay inside one process. CLI parity is still useful for docs and operator troubleshooting.

Examples:

```bash
regctl repo ls nexus.example.com
regctl tag ls nexus.example.com/repository/docker-hosted/app
regctl manifest get nexus.example.com/repository/docker-hosted/app:tag --format raw-body
regctl manifest head nexus.example.com/repository/docker-hosted/app:tag
regctl blob head nexus.example.com/repository/docker-hosted/app@sha256:<digest>
```

Wrapper requirements:

- Use request-scoped timeouts.
- Support retries with bounded exponential backoff.
- Support registry credentials from Kubernetes Secret, file, or environment.
- Never log credentials.
- Parse JSON/raw manifests into typed structs.
- Cache immutable digest lookups.

Benefits:

- Better concurrency control.
- Better typed access to manifests and blobs.
- Easier authentication plumbing.
- Less shell quoting risk.
- Better performance at scale.

## Indexing flow

### Nexus API capability assumptions

Nexus exposes repository, search, component, asset, and task APIs. The Assets API is useful for inventory because it can list assets in a repository with a continuation token and fetch asset detail by ID, including metadata such as checksums, size, blob creation time, last modified time, and last downloaded time where Nexus provides those fields.

Nexus Lens should still not assume there is a reliable hosted-Docker "changed since" feed that returns every changed image/tag without walking relevant repository content. The Assets API gives useful dates on asset records, but the repository asset list is still a paginated repository walk rather than a guaranteed timestamp-filtered change stream. The Docker Registry API also does not provide a general "tags changed since timestamp" endpoint.

Design implications:

- Each Nexus instance should run a capability probe during setup and periodically afterward. Store the Nexus version, available API surfaces, and observed field support in `nexus_instances.api_capabilities`.
- Asset listing is the backbone of full reconcile and can also feed recent candidate discovery when the configured scope is small enough to page cheaply.
- Asset detail timestamps are stored as source metadata and used to prioritize candidate refreshes after Nexus Lens has observed them.
- Recent sync and bounded reindex are candidate-based refresh strategies, not authoritative change streams.
- They should avoid whole-repository walks by using a mix of Nexus Assets/Search/API results, locally indexed timestamps, known recently active repositories/images, previous failures, verification findings, and explicit operator scope.
- They may miss old deletes or changes if Nexus does not expose updated timestamps for those records.
- They must not mark missing tags deleted unless the affected repository/scope has completed a full reconcile.
- Full reconcile remains the authoritative operation for deletion confirmation and broad drift detection.

### Asset scan vs registry enrichment

The v1 indexer should treat Nexus REST asset scanning and Docker registry enrichment as separate phases with different cost profiles.

Asset scan phase:

- Walk `GET /service/rest/v1/assets?repository=...` for every selected hosted repository during full reconcile.
- Store asset IDs, repository, format, path, download URL, checksums, file size, blob metadata, and available timestamps.
- Treat asset `fileSize` as the size of that Nexus asset/blob record, not as Docker image size. For Docker, a 30 GB image is represented by multiple assets such as manifests, configs, and layer blobs; individual manifest/config assets may be small while layer blob assets carry most bytes.
- Use asset `fileSize` for asset/blob inventory, rough storage attribution, and cross-checks, but not as the authoritative image/tag size.
- Fetch `GET /service/rest/v1/assets/{id}` only when the list response does not include fields needed for timestamps or validation.
- Use continuation tokens as intra-run checkpoints so a large repository walk can resume within the same valid cursor chain. If the token is invalid or stale, restart that repository walk and block deletion confirmation until a clean pass completes.
- Do not assume server-side sorting, page-size control, or date filtering. The documented and OpenAPI-visible list parameters are `repository` and `continuationToken`.
- Treat asset ordering as stable enough to follow a cursor, but not meaningful for freshness, path ordering, or progress estimates.
- Parallelize asset scanning across repositories and Nexus instances, not by opening multiple independent cursors into the same repository unless Nexus later exposes a supported partition key.
- Bound each queue task by `maxPagesPerTask` and `checkpointEveryItems` so long repositories yield periodically, leases stay fresh, and worker restarts do not replay huge scans.
- Rate-limit asset list/detail calls separately from registry manifest calls because registry enrichment is usually the more expensive path.
- Prefer this phase for broad inventory because it is API-only, paginated, and independent of blob-store backend.

Registry enrichment phase:

- Use Docker Registry API/regclient to list tags, resolve tag manifest digests, fetch unknown manifests, and extract config/layer digests and sizes.
- Calculate Docker logical image size from the manifest's referenced config and layer descriptor sizes, deduplicated by digest as needed for image/repository/reclaimable views.
- For manifest lists/OCI indexes, fetch child manifests or report per-platform and all-platform sizes separately. Do not collapse multi-arch images into a single-platform size without labeling the assumption.
- Descriptor sizes are API-derived content sizes. They are suitable for logical and estimated reclaimable analysis but do not guarantee immediate physical blob-store reclamation.
- Manifest and layer digest analysis is sufficient for v1 Docker storage analytics. Do not require direct correlation between Nexus `blobRef` values and Docker layer digests for v1 correctness.
- Nexus `blobRef` and blob-store metadata can be stored when available for diagnostics, duplicate/path-move warnings, and later physical storage analysis, but Docker reclaimable estimates should come from registry manifests/layers.
- Treat this as the slow path because it fans out by image/tag/digest and may require many manifest/config/blob requests.
- Cache immutable digest results aggressively. Once a manifest or layer digest has been fetched and verified, future runs should reuse it unless verification finds drift.
- Prioritize enrichment for new tags, changed tag digests, assets with recent timestamps, previous failures, verification findings, and operator-selected scopes.
- Allow dashboards to show partial state: asset inventory can be fresh while Docker size/layer enrichment is still catching up.
- Surface enrichment lag separately from asset inventory lag so operators understand whether Nexus Lens is missing artifacts or only waiting on size/layer analysis.

Docker download recency:

- Nexus `lastDownloaded` is asset-level metadata.
- For Docker, a pull may touch manifest, config, and shared layer assets; shared layers make tag-level "never pulled" an inference.
- Store tag-level download recency with a confidence flag, such as `exact_manifest`, `asset_inferred`, `layer_inferred`, or `unknown`.
- Prefer manifest/config asset timestamps for tag recency. Use layer timestamps only as weak evidence because layers can be shared across many tags.

### Initial sync

```text
1. Create a scan_run with trigger_source = manual, api, or cron.
2. Probe or refresh Nexus API capabilities if stale.
3. Check Nexus status endpoints and record health/read-only state.
4. Discover repositories from Nexus REST API.
5. Filter enabled Docker/OCI repositories.
6. Enqueue per-repository discovery tasks.
7. For each repo, walk Nexus Assets API pages to capture asset IDs, paths, checksums, sizes, and available source timestamps.
8. Derive candidate Docker image paths from asset paths and repository metadata where possible.
9. For each repo, discover remaining image names in bounded registry pages when needed.
10. Enqueue per-image tag listing tasks.
11. For each image, list tags using regclient/registry API.
12. Resolve tag manifest digests.
13. Classify tag names against enabled tag-pattern definitions.
14. Fetch manifests only for unknown digests or verification-selected existing digests.
15. Extract config and layer digests/sizes.
16. Upsert images, tags, manifests, layers, relationships, assets, and tag-pattern matches.
17. Reconcile Nexus REST API and registry API views.
18. Run verification checks.
19. Refresh dashboard rollup tables.
20. Create trend snapshot.
21. Mark scan_run successful only after all required task groups complete.
```

Initial sync rules:

- Work must be split into small tasks so a pod restart loses at most one task lease.
- Every task writes an updated checkpoint after each page or configured item count.
- Asset inventory freshness and Docker enrichment freshness should be tracked separately.
- Manifest and layer records are immutable by digest; repeated scans reuse them.
- Tag-to-digest relationships are mutable and must keep first seen, last seen, and deleted markers.
- Tag pattern classification is deterministic and rerunnable. Pattern definition changes enqueue a lightweight reclassification task for existing tags.
- Missing tags are not marked deleted during short recent syncs. Mark a tag deleted only after absence in a completed authoritative full reconcile scope.
- Full reconcile is in-place. Nexus Lens does not build a second generation or swap table sets in v1.
- Only one exclusive operation may run per Nexus instance at a time.
- Full reconcile and bounded reindex are mutually exclusive per Nexus instance.
- Recent sync, verification, and trend snapshot runs should not start for a Nexus instance while an exclusive operation is running unless explicitly configured as low-impact read-only verification.
- Strong verification findings are the safety mechanism for in-place reconcile drift.
- Large repositories should be processed breadth-first enough to provide early partial UI value, then deepened.

### Periodic sync

```text
1. Build a candidate set from Nexus Assets/Search/API results when useful, local index timestamps, previous failures, verification findings, and configured scopes.
2. Rescan candidate repositories/images.
3. Refresh tag lists for candidate images.
4. Fetch manifests only for new or changed digests.
5. Reuse existing immutable manifest/layer data.
6. Do not mark out-of-scope missing tags deleted.
7. Refresh rollup tables.
8. Write trend snapshots.
```

Periodic sync modes:

- **Recent sync:** low-concurrency candidate refresh every 15-60 minutes for repositories/images observed through asset timestamps, local activity, verification findings, or recent failures.
- **Bounded reindex:** admin/API-triggered or scheduled windowed scan, such as last 7/30/90 days, scoped by Nexus instance, repository, image prefix, or tag pattern. V1 bounded reindex windows are expressed in whole days.
- **Full reconcile:** configured interval by scale, walks all selected container repositories and confirms deletions for completed scopes.
- **Trend snapshot:** daily, writes compact aggregate metrics.
- **Verification scan:** samples indexed records and compares them back to Nexus/registry responses.

Suggested freshness schedules:

- Recent sync: every 30 minutes by default; configurable within a 15-60 minute range for scheduled runs.
- Bounded reindex: default weekly over the previous 7 whole days; scheduled bounded reindex should not run more than once per day per Nexus by default.
- Full reconcile: weekly, monthly, or manual depending on Nexus scale and confidence needs; scheduled full reconcile should not run more than once per day per Nexus by default.
- Trend snapshot: daily.
- Verification: daily or after selected runs, while avoiding active exclusive operations.

Scheduled runs should respect per-Nexus operation locks. If a scheduled run cannot start because another exclusive indexing operation is active for the same Nexus, mark it `skipped_blocked` with the blocking run ID rather than queueing unbounded duplicate work.

### Canonical index actions

| UI label | `scan_type` | Scope | Cost | Confirms deletions | Resets local rows | Default schedule | Confirmation |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Recent sync | `recent_sync` | Candidate repos/images from local timestamps, failures, verification findings, and recent activity | Low to medium | No | No | Every 15-60 minutes | None |
| Bounded reindex | `bounded_reindex` | Explicit Nexus/repo/image/tag-pattern/whole-day time window | Medium, proportional to candidate scope | No, except focused verified delete subjects | No | Optional weekly | Warning modal |
| Full reconcile | `full_reconcile` | Selected hosted repositories, usually all Docker repos for one Nexus | High, full asset walk plus registry diff/enrichment | Yes, only for completed scopes | No | Weekly/monthly/manual by scale | Strong confirmation |

Terms:

- Full reconcile is in-place and verifies Nexus state against the local index.
- Rebuild local index means discard/recreate selected local index rows from Nexus APIs. It is not a v1 index action; keep it as a later maintenance/admin escape hatch until corruption recovery proves it needs first-class product support.
- Avoid using "full reindex", "deep rebuild", and "full tag reconciliation" interchangeably in UI copy.

### Indexing cost model

Full asset API scans are acceptable, but they are not free. Cost should be estimated and shown before broad manual runs.

Approximate cost:

```text
asset_pages ~= observed_asset_count / observed_assets_per_page
asset_detail_calls ~= assets_missing_required_fields
tag_list_calls ~= candidate_images
manifest_head_calls ~= candidate_tags
manifest_fetch_calls ~= new_or_changed_or_uncached_digests
blob_head_calls ~= uncached_layer_or_config_digests when manifest descriptors are insufficient
```

Cost rules:

- The observed Assets API page size is learned from responses; Nexus Lens does not configure it.
- A full asset walk is O(total selected assets). On very large Nexus instances it may span many queue tasks and, if budgeted, several scheduler invocations.
- Registry enrichment is usually more expensive than the REST asset walk because it fans out by image, tag, manifest, config, and layer digest.
- `maxRunDuration` should budget work, not necessarily fail it. A budget-paused run records progress and resumes from the next schedulable window unless the user cancels it.
- Cost previews should show selected repositories, last observed asset count, estimated asset pages, candidate images, tags to list, manifest fetches expected, existing manifest/layer reuse, active rate limits, and estimated REST/registry request counts.

### Diffing after baseline

After one successful full reconcile has established a baseline, most runs should diff against local state instead of treating every observation as new.

Asset diff rules:

- Upsert by `(repository_id, nexus_asset_id)` when asset ID is present.
- Also maintain current path/checksum/size fields so path moves or ID changes can be detected as warnings rather than silent duplicates.
- For each full asset walk scope, stamp observed assets with the current `scan_run_id`.
- Assets not seen in a cursor-exhausted completed full-reconcile scope become missing candidates, not deleted immediately outside the completed scope.
- Optional asset timestamp fields such as `blob_created_at`, `blob_updated_at`, `last_modified_at`, and `last_downloaded_at` prioritize candidates. They are not completeness guarantees.

Docker tag diff rules:

- Compare the current registry tag digest with `docker_tags.manifest_digest`.
- If the digest changed, update the current pointer on `docker_tags` and append a `digest_changed` observation.
- If a tag disappears during a completed full reconcile scope, append a `deleted_confirmed` observation and set deletion fields on `docker_tags`.
- If a previously deleted tag reappears, append a `recreated` observation and clear current deletion fields.
- Existing manifest/layer rows determine whether expensive manifest fetches can be skipped.

Candidate source tracking:

- Every candidate should record why it was selected: `asset_timestamp`, `local_recent`, `previous_failure`, `verification_finding`, `tag_pattern_change`, `operator_scope`, or `full_reconcile`.
- Candidate confidence should be explicit: `hint`, `scoped`, `verified`, or `authoritative_scope`.
- Timestamp high-watermarks are priority hints. They must not be used as the only proof that no other changes exist.

### Deletion confirmation

Deletion confirmation requires a completed authoritative scope, not just a run with status `succeeded`.

Rules:

- A repository full-reconcile scope is authoritative only when all required cursor walks are exhausted, all required task groups finish, no invalid cursor was seen, and the scope status is `succeeded`.
- Invalid continuation tokens force a restart of that repository walk from the beginning and block deletion confirmation for that scope.
- Budget-paused, canceled, partial, or failed scopes must not mark rows deleted.
- Recent sync and bounded reindex may verify deletion for an explicit subject, but they must not infer broad missing-tag deletion.
- Deletion decisions should link to the confirming `scan_run_id`/scope and include `delete_source`.

### Schedule ownership

v1 uses app-managed schedules stored in PostgreSQL.

Rules:

- Helm values provide initial schedule defaults.
- On first startup, the API imports schedule defaults if no schedules exist for that Nexus instance.
- After import, PostgreSQL is the source of truth for schedules so the UI can edit schedules without changing Kubernetes CronJobs.
- Kubernetes runs a lightweight scheduler tick CronJob, or an API/worker scheduler loop, that wakes frequently and asks the database which schedules are due.
- Scheduler ticks enqueue scan runs and exit quickly; they do not perform indexing work inline.
- GitOps users can disable UI schedule editing and manage schedule defaults through Helm, but then the UI should clearly show schedules as read-only.

Schedule records should include:

- Nexus instance.
- Scan type.
- Scope.
- Cron expression or interval.
- Time zone.
- Enabled/suspended state.
- `next_run_at` and `last_run_at`.
- Maximum runtime/budget.
- Lock conflict behavior: `skip_blocked` by default.

Scheduled runs that cannot start due to a per-Nexus operation lock should be recorded as `skipped_blocked` with the blocking run ID. Missed scheduler ticks are skipped in v1 rather than caught up, which avoids surprise backlogs after downtime.

### Later: webhook-assisted sync

Repository webhooks are deferred until after the polling indexer is stable. They can later reduce the latency between a Nexus change and Nexus Lens discovering it. They should be treated as hints that identify where to look next, not as authoritative inventory updates.

Flow:

```text
1. Nexus administrator configures a Repository webhook capability that posts to Nexus Lens.
2. Nexus Lens receives the payload, validates the shared-secret HMAC signature when configured, and stores the raw event.
3. The event processor extracts repository name, event/action, asset ID, component ID, and path-like subjects when present.
4. The worker enqueues focused tasks:
   - refetch asset by ID when an asset ID is present;
   - refetch component by ID when a component ID is present;
   - refresh the inferred Docker image tag list when the path maps to a Docker image;
   - verify deletion when the event indicates removed content.
5. The focused tasks read Nexus REST/registry APIs and update the normal index tables.
6. The webhook event is marked processed after the follow-up task is durable.
```

Rules:

- Webhook setup is out of v1 scope. When added, Nexus Lens can show the URL/secret, but it should not mutate Nexus capabilities automatically.
- Store `X-Nexus-Webhook-Delivery` as the preferred idempotency key when present; fall back to payload hash.
- Validate `X-Nexus-Webhook-Signature` with Sonatype's HMAC behavior when a shared secret is configured.
- Webhook events should be idempotent and can be reprocessed safely.
- Webhook payload parsing should be schema-tolerant because available fields may differ by Nexus version and event type.
- A webhook-created candidate does not permit global deletion marking. Deletion still requires either focused verification for that subject or a successful full reconcile for the repository/scope.
- If webhook delivery stops, scheduled syncs and full reconcile still keep the index eventually consistent.

### Bounded reindex

Bounded reindex refreshes a time window without paying the cost of a full repository walk. It is useful for large Nexus instances when operators want to repair or refresh recent data.

Inputs:

```yaml
type: bounded_reindex
nexusInstance: main
scope:
  repositories: ["docker-hosted"]
  imagePrefixes: ["team-a/"]
  tagPatterns: ["feature_branch"]
window:
  days: 30
  anchor: now
```

Behavior:

- Use locally indexed asset timestamps, known paths/checksums, previous failures, verification findings, and operator-selected scope to narrow candidates.
- Use Nexus Assets/Search/API results only for targeted lookup or for a small-scope full walk. Do not assume Nexus can server-side filter a repository by arbitrary `created`, `updated`, or `lastModified` windows.
- Use source timestamps from indexed assets, such as `blob_created_at`, `blob_updated_at`, `last_modified_at`, and `last_downloaded_at`, plus local timestamps such as `first_seen_at` and `last_seen_at`, to prioritize known records in the window.
- If the requested scope is narrow enough, optionally page the Nexus Assets API for those repositories to discover assets not yet in the local index.
- Include repositories/images with previous failures, verification findings, stale checkpoints, or recent tag pattern changes.
- Refresh tag lists for affected images.
- Fetch new or changed manifests.
- Re-run tag pattern classification for affected tags.
- Refresh rollups, verification samples, and daily snapshots affected by the refreshed records.

Limits:

- Bounded reindex is partial-confidence by design.
- It can add and update records in scope.
- It should not globally mark missing tags deleted.
- Missing-tag deletion still requires one successful full reconciliation for the repository/scope.
- If the candidate set cannot be narrowed safely, the UI should warn that a full reconcile is required for authoritative freshness rather than silently scanning the whole repository.
- A broad whole-day window such as "last 30 days" is only as complete as the locally indexed asset timestamp coverage unless the run is allowed to page the relevant Nexus Assets API scopes.
- The UI should label bounded reindex results as partial and show the selected time window/scope.

### Work queue and resume

Workers claim tasks with `FOR UPDATE SKIP LOCKED` and update `locked_by`, `locked_at`, `lease_expires_at`, `lock_token`, and `attempt`. A stale-lock reaper returns tasks to `pending` or `retryable` when the owning pod disappears or the lease expires.

### Index progress tracking

Index progress must be useful even when the total amount of work is unknown. Nexus APIs and registry scans may not expose a reliable total upfront, so the UI should distinguish measured progress from estimates.

Progress model:

- `scan_runs.phase` describes the current high-level phase, such as `discover_repositories`, `list_images`, `list_tags`, `fetch_manifests`, `ingest_assets`, `verify`, `refresh_rollups`, or `snapshot`.
- `scan_runs.completed_tasks`, `failed_tasks`, and `estimated_total_tasks` power the run-level progress bar.
- `progress_confidence` should be one of `unknown`, `estimated`, or `measured`.
- If total work is unknown, show an indeterminate progress bar plus concrete counters.
- Once enough tasks are enqueued to estimate total work, switch to estimated progress.
- For bounded reindex and full reconcile scopes with known task counts, use measured progress.
- `scan_tasks.progress_current`, `progress_total`, and `progress_label` provide per-task progress, such as `tag page 14`, `manifest 120/800`, or `repository 3/10`.
- Use separate checkpoint types for `asset_inventory` and `docker_enrichment` so freshness and lag can be reported independently.
- Heartbeats update `last_heartbeat_at` on active runs/tasks so stuck work can be detected.

UI status should show:

- Run type: recent sync, bounded reindex, full reconcile, verification, trend snapshot.
- Scope: Nexus instance, repository, image prefix, tag pattern, and time window when applicable.
- Status: pending, running, paused, canceling, succeeded, failed, partial, canceled.
- Current phase.
- Asset inventory freshness and Docker enrichment freshness.
- Elapsed time.
- Estimated remaining time when confidence is good enough, otherwise "estimating".
- Running workers/tasks with worker ID, task type, subject, attempt, current duration, and heartbeat age.
- Counts by task status: pending, running, retryable, failed, succeeded, canceled.
- Nexus throttling/backoff state.
- Last error per failed task and retry availability.

Restart/retry behavior:

- Restarting a run should not create duplicate tasks if the same idempotency key and scope are used.
- Retrying a failed run should enqueue only failed/retryable tasks when possible.
- A full restart option can cancel active tasks for the run and enqueue a new run with the same scope.
- Pausing a run stops new task claims but lets running tasks finish or hit lease timeout.
- Canceling a run marks pending/retryable tasks canceled and asks running workers to stop after the current safe checkpoint.
- Stale running tasks are recovered by lease reaping and shown as retried/stale in the UI.

PostgreSQL queue design:

- Use Postgres as the durable source of truth for work. CronJobs and API actions should enqueue tasks, not execute large scans inline.
- Use `FOR UPDATE SKIP LOCKED` so many workers can claim tasks concurrently without waiting behind rows another worker already locked.
- Claim tasks in small batches, usually 1-50 tasks depending on task cost.
- Keep task transactions short: claim, commit, do network work outside the transaction, then write results/checkpoints in bounded transactions.
- Use `task_key` plus `unique (scan_run_id, task_type, task_key)` for idempotent enqueue.
- Denormalize `nexus_instance_id` and `cost_class` onto `scan_tasks` so workers can claim work that fits available per-Nexus and per-API-surface capacity.
- Claiming should respect per-Nexus admission control. A throttled or circuit-open Nexus should not let workers claim a large batch of tasks that will only sleep or fail.
- Cost classes should include at least `asset_list`, `asset_detail`, `registry_tag_list`, `registry_manifest`, `registry_blob`, `db_rollup`, and `verification`.
- Use `lease_expires_at` and `lock_token` so stale workers can be recovered safely and late writes from old workers can be rejected.
- Use `LISTEN/NOTIFY` only as a wake-up optimization. Workers must still poll because notifications are not durable queue state.
- Separate active task state from attempt history. `scan_tasks` is the hot table; `scan_task_attempts` records history without bloating the hot claim path.
- Keep completed task retention bounded. Archive or delete old successful tasks after the UI/reporting retention period.
- Do not let one giant run enqueue millions of tiny tasks if larger page-sized tasks would be enough. Task granularity should balance resume precision against queue overhead.

Task status lifecycle:

```text
pending -> running -> succeeded
pending -> running -> retryable -> running -> succeeded
pending -> running -> retryable -> failed
pending/running/retryable -> canceled
```

Use `retryable` when a task has failed but should wait until `available_at` before being claimed again. Use `failed` only after `attempt >= max_attempts` or when the error is non-retryable.

### Error handling and structured failure records

Errors should be classified once in Go and then used consistently by the worker, API, UI, metrics, and logs. Avoid scattering retry decisions across call sites.

Error classes:

- `auth`: missing, invalid, or forbidden Nexus/PostgreSQL/application credentials. Usually non-retryable until configuration changes.
- `rate_limited`: Nexus returned `429` or an equivalent throttling signal. Retryable with backoff and per-Nexus admission reduction.
- `transient_network`: timeout, connection reset, DNS failure, temporary TLS failure, or upstream `502`/`503`/`504`. Retryable with bounded backoff.
- `nexus_unavailable`: health probe or required Nexus API surface unavailable. Retryable, but may open the per-Nexus circuit breaker.
- `not_found`: repository, asset, manifest, blob, or tag missing. Retryability depends on operation type; deletion decisions still require the reconcile rules.
- `unsupported_capability`: Nexus version, repository format, route, or response shape is unsupported. Usually non-retryable for that scope until config or version changes.
- `invalid_cursor`: Nexus continuation token failed or produced inconsistent pagination. Retry by restarting the affected walk from the last safe checkpoint; block deletion confirmation for that scope.
- `data_invariant`: local database or source data violates an expected invariant. Non-retryable until repaired, and should create a verification finding when indexed state may be affected.
- `postgres`: database connection, serialization, constraint, migration, or pool exhaustion error. Retry only when the specific SQLSTATE is safe to retry.
- `canceled`: user- or system-requested cancellation. Non-error terminal state for the task/run, but still logged at info level.
- `bug`: unexpected panic, nil/zero invariant, parse failure for a documented API shape, or unknown error class. Non-retryable by default after recording enough detail for debugging.

Every stored task/run error should include:

- Stable `error_code` such as `nexus_unauthorized`, `registry_manifest_unknown`, `assets_cursor_invalid`, `postgres_serialization_failure`, or `task_lease_lost`.
- Broad `error_class` from the taxonomy above.
- Human-readable `error_message` safe for UI display.
- Structured `error_details` with redacted endpoint, method, HTTP status, Nexus request ID or response headers when available, SQLSTATE when available, retry decision, next retry time, cursor/checkpoint identifiers, repository, subject, worker ID, pod name, and attempt number.
- A short response body excerpt only when it is redacted, size-limited, and useful for diagnosing Nexus errors.

Retry rules:

- Retry decisions should be produced by a central classifier that receives the operation type, HTTP status or SQLSTATE, Nexus capability state, and attempt count.
- All retries use bounded exponential backoff with jitter and respect per-Nexus rate limits and circuit breakers.
- Non-retryable task errors become `failed` immediately and should include a remediation hint when possible.
- Retryable failures write a `scan_task_attempts` row for every attempt and update the hot `scan_tasks` row with only the latest compact error summary.
- Lost leases and late worker writes should be classified separately from Nexus failures so they are not mistaken for source-system errors.
- A run can finish `partial` when some scopes fail; the UI should show which data is still trustworthy and which scopes require retry or full reconcile.
- Errors that imply indexed data may be wrong should create or refresh a `verification_findings` row instead of remaining only in logs.

API error responses:

```json
{
  "code": "operation_locked",
  "message": "Indexing is already running for nexus-main.",
  "retryable": true,
  "request_id": "req_01J...",
  "details": {
    "nexus_instance": "nexus-main",
    "blocking_run_id": 123,
    "blocking_scan_type": "full_reconcile"
  }
}
```

API handlers should map internal errors to stable public codes. Internal stack traces, SQL text with values, credentials, raw authorization headers, and unredacted upstream URLs must not be returned to clients.

Example claim query:

```sql
with claimed as (
  select id
  from scan_tasks
  where status in ('pending', 'retryable')
    and available_at <= now()
    and nexus_instance_id = any($1)
    and cost_class = any($2)
  order by priority desc, available_at, id
  for update skip locked
  limit $3
)
update scan_tasks t
set status = 'running',
    locked_by = $4,
    locked_at = now(),
    lease_expires_at = now() + $5::interval,
    lock_token = $6,
    attempt = attempt + 1,
    started_at = coalesce(started_at, now())
from claimed
where t.id = claimed.id
returning t.*;
```

Task completion should verify the current lock token:

```sql
update scan_tasks
set status = 'succeeded',
    finished_at = now(),
    lease_expires_at = null,
    locked_by = null,
    locked_at = null,
    lock_token = null,
    stats = $3
where id = $1
  and lock_token = $2;
```

A zero-row update means the lease was lost or reclaimed; the worker must not assume completion was recorded.

Resume behavior:

- Each scan type stores cursor state in `index_checkpoints`.
- Page tokens from Nexus REST APIs are committed only after the page has been fully processed. Treat continuation tokens as intra-run page cursors, not durable long-term cursors.
- If Nexus rejects a continuation token or the cursor is considered stale, restart that repository walk from the beginning, record `cursor_restarted`, set `invalid_cursor_seen` on the scope, and block deletion confirmation for that scope until a clean cursor-exhausted pass completes.
- Registry tag list progress is checkpointed by image name and tag page/cursor where supported.
- On restart, workers resume from the last committed checkpoint and idempotently upsert already-seen records.
- Failed tasks retry with bounded exponential backoff and then become `failed` with a visible finding.
- Manual retry from the UI should enqueue failed tasks without requiring a rebuild local index operation.

Concrete task types:

- `asset_walk_slice`: walk up to `maxPagesPerTask` Assets API pages for one repository and enqueue the next slice transactionally when another continuation token remains.
- `asset_detail_batch`: fetch details for up to N asset IDs missing required fields.
- `image_tag_list`: list tags for one image/repository through the Docker Registry API.
- `manifest_fetch_batch`: fetch or verify up to N uncached manifest digests.
- `blob_head_batch`: fetch blob/config/layer descriptor details only when manifest descriptors are insufficient.
- `tag_classification_batch`: classify up to N tags against enabled tag patterns.
- `rollup_refresh`: refresh rollup tables for a completed scope.

Task chaining rule:

- A slice task commits processed rows and checkpoints first, then enqueues the next slice in the same transaction where practical.
- If the next slice enqueue fails, the checkpoint must not advance past work that cannot be resumed.

Queue table performance:

- `scan_tasks_claim_idx` must be a partial index over claimable rows, ordered by priority and availability. Do not force workers to scan completed tasks.
- Keep claim predicates simple and backed by partial indexes.
- Avoid frequent updates to large JSONB columns in `scan_tasks`; put verbose logs/history in `scan_task_attempts`.
- Set a lower autovacuum scale factor for hot queue tables because status updates produce dead tuples quickly.
- Consider `fillfactor` around 70-80 for `scan_tasks` to reduce page splits and update churn.
- Monitor dead tuples, table bloat, queue claim latency, and oldest pending task age.
- Batch enqueue operations with `insert ... on conflict do nothing`.
- Use advisory locks for singleton maintenance tasks such as stale lease reaping, rollup refresh orchestration, and trend snapshot generation.

Suggested queue table storage tuning:

```sql
alter table scan_tasks set (
  fillfactor = 75,
  autovacuum_vacuum_scale_factor = 0.02,
  autovacuum_analyze_scale_factor = 0.02
);
```

### Nexus load management

Protecting Nexus is a core requirement. Defaults should be balanced and tunable per Nexus instance. Repository-level filters are required, but rate limits should start per Nexus instance rather than per repository.

Controls:

- Global Nexus requests-per-second token bucket.
- Per-instance request limit and burst.
- Per-instance worker and request concurrency limits.
- Separate concurrency limits for REST list calls, registry tag calls, registry manifest calls, and asset detail calls.
- Retry budget with exponential backoff and jitter.
- Honor HTTP `429`, `503`, timeout, and connection errors by backing off aggressively.
- Circuit breaker per Nexus instance when errors exceed a configured threshold.
- Pause/resume indexing per Nexus instance from the UI.
- Maintenance windows for full reconcile runs.
- Maximum runtime/budget per scheduled run so busy clusters do not pile up work; budget exhaustion pauses resumable work instead of marking the scope authoritative.
- Configurable maximum pages/items per task and checkpoint interval for Nexus REST walks.
- Configurable manifest fetch cache TTL for negative/failed lookups.

Suggested safe defaults:

```yaml
nexus.rateLimits.requestsPerSecond: 5
nexus.rateLimits.burst: 10
nexus.rateLimits.maxConcurrentRepos: 2
nexus.rateLimits.maxConcurrentRestRequests: 4
nexus.rateLimits.maxConcurrentManifestFetches: 8
indexing.maxPagesPerTask: 20
indexing.checkpointEveryItems: 500
indexing.maxRunDuration: 6h
```

The UI should show when Nexus Lens is intentionally throttling itself so slow indexing is understandable rather than mistaken for a failure.

For large installations that need more speed, operators can raise per-Nexus concurrency and add worker replicas. The PostgreSQL queue should absorb the extra parallelism while token buckets and circuit breakers keep Nexus from being overloaded.

### Index correctness verification

Correctness cannot rely only on successful task completion. Nexus Lens should continuously compare indexed state to source systems.

Verification checks:

- Repository count and names from Nexus REST API match indexed selected repositories.
- Sampled image tag lists from registry API match indexed tags.
- Manifest digest and media type for sampled tags match registry responses.
- Layer count and layer byte totals for sampled manifests match stored graph rows.
- Nexus asset counts reconcile to indexed asset rows within known API limitations.
- Deleted/missing tags require absence in one completed authoritative full reconcile scope before being marked deleted.
- Full-walk mechanics are verified: cursor exhaustion, cursor restarts, skipped scopes, chained task completion, and budget-paused scopes.
- Reclaimable-byte estimates are checked against small deterministic fixtures in tests.

Verification outputs:

- Store mismatches in `verification_findings`.
- Show open findings on the index status page.
- Include verification status in `scan_runs.stats`.
- Emit findings such as `repo_full_walk_incomplete`, `cursor_restarted`, `delete_confirmation_blocked`, `asset_inventory_fresh_enrichment_stale`, and `full_reconcile_budget_paused`.
- Expose `/api/v1/index/verification` for UI and automation.
- Allow manual "verify repository" and "verify sample" actions from the UI.

### Suggested schedule

```text
Recent repo sync: every 30 minutes by default; scheduled runs configurable from 15-60 minutes
Bounded reindex: default weekly over previous 7 whole days; scheduled runs at most once per day per Nexus by default
Full reconcile: weekly, monthly, or manual by scale; scheduled runs at most once per day per Nexus by default
Trend snapshot: daily
Verification: daily or after selected runs, while avoiding active exclusive operations
```

### Retention

Retention is configurable and deletion-only in v1. Nexus Lens should not downsample old data in v1.

Default retention:

```yaml
worker:
  retention:
    intervalSeconds: 3600
    scanRunDays: 30
    cleanupExecutionDays: 365
    verificationFindingDays: 180
    trendSnapshotDays: 365
```

Rules:

- Delete terminal scan runs older than the configured scan-run retention.
- Delete terminal cleanup executions older than the configured cleanup-execution retention. Event rows cascade with their execution.
- Keep open verification findings regardless of age; delete only closed findings after their retention window.
- Keep trend snapshots as daily rows and delete rows older than the configured snapshot retention.
- Expose retention defaults in Helm and allow Admin settings UI overrides stored in PostgreSQL.
- Add downsampling later only if daily snapshot volume or chart performance makes it necessary.

## Analytics

### Core metrics

- Total repositories.
- Total images.
- Total tags.
- Total manifests.
- Total layers.
- Logical bytes by repo/image/tag.
- Nexus asset bytes by repository and Docker logical bytes by repository/image/tag.
- Unique reclaimable bytes.
- Shared bytes.
- Old tags by age bucket.
- Never-pulled tags.
- Tag pattern counts by repository/image/team/time.
- Unknown/unclassified tag count.
- Fastest-growing tag patterns.
- Index freshness.
- Queue depth and failed task count.
- Verification finding count by severity.
- Nexus throttling/backoff time.

### Unique reclaimable size

For a candidate delete set, calculate only layers, manifests, configs, and API-visible storage objects that are no longer referenced by any remaining protected tag/manifest.

Conceptually:

```text
candidate_tags -> candidate_manifests -> candidate_layers
reclaimable_layers = candidate_layers - layers_referenced_by_non_candidate_manifests
estimated_reclaimable_bytes = sum(reclaimable_layers.size_bytes) + unique manifest/config bytes
```

This API-derived estimate should be the main differentiator versus Nexus UI cleanup views. It is not a guarantee of immediate disk/S3 reclamation because Nexus deletion behavior, soft-deleted blobs, cleanup tasks, compaction, and blob-store backend behavior control when bytes are physically reclaimed.

### Example dashboard questions

- Top 20 images by unique bytes.
- Top 20 images by growth this month.
- Largest never-pulled tags.
- PR tags older than 30 days.
- Images with more than N tags.
- Tags missing last downloaded metadata.
- What tag patterns are developers actually pushing?
- Which teams/images use branch-style tags like `feat-dvo-1313-hsdfjw`?
- Which images use semantic version tags like `1.2.3` or `v1.2.3`?
- Which repositories have high growth from PR, feature, or opaque generated tags?
- Which tag patterns are good cleanup-policy candidates?
- Which repositories are stale because indexing is throttled, failing, or paused?
- Which teams or image namespaces are driving container growth?
- Which recent window needs a bounded reindex because verification found drift?

### Trend model

Trend data should be stored as compact daily aggregate snapshots rather than recomputing historical state from raw artifact rows on every request.

A snapshot is a set of metric rows captured for one calendar day after indexing/rollups have refreshed. It is not a copy of every tag, image, or manifest. It is a compact fact table for charting questions like "how many bytes did this repository have on this day?" or "how many feature-branch tags existed on this day?"

Example rows for one day:

```text
snapshot_date | nexus | repository | image | metric_name       | metric_value | dimensions
2026-06-27    | main  | docker-apps | null  | logical_bytes     | 928374923847 | {}
2026-06-27    | main  | docker-apps | api   | tag_count         | 412           | {}
2026-06-27    | main  | docker-apps | api   | tag_pattern_count | 87            | {"pattern":"feature_branch"}
2026-06-27    | main  | docker-apps | api   | unique_bytes      | 9823749123    | {}
```

Rules:

- Capture at most one canonical snapshot per Nexus/repository/image/metric/dimensions per day.
- Store `snapshot_date` as the reporting date and `captured_at` as the actual time the row was written.
- Daily snapshots are enough for v1. Do not downsample in v1.
- Retain daily snapshots for 365 days by default.
- If a snapshot job is missed or fails, show the missing day explicitly in charts rather than smoothing it away.
- Add downsampling or longer-term monthly summaries later only if data volume or chart performance requires it.

Example trend query:

```sql
select snapshot_date, metric_value
from trend_snapshots
where nexus_instance_id = $1
  and repository_id = $2
  and image_id is null
  and metric_name = 'logical_bytes'
  and dimensions = '{}'::jsonb
  and snapshot_date between $3 and $4
order by snapshot_date;
```

Snapshot dimensions:

- Nexus instance.
- Repository.
- Image or artifact namespace.
- Format, starting with `docker`.
- Team/environment labels when available.

Snapshot metrics:

- Logical bytes.
- API-reported bytes.
- Unique bytes.
- Tag count.
- Manifest count.
- Layer count.
- Never-pulled tag count.
- Old tag count.
- Cleanup candidate count.
- Estimated reclaimable bytes.
- Tag pattern counts.
- Unclassified tag count.

Visualization requirements:

- Time range selector: 7d, 30d, 90d, 365d, custom.
- Compare repositories or images over time.
- Show absolute size and delta over the selected range.
- Make missing data explicit when snapshots were skipped or failed.
- Link from a trend point to the underlying repository/image filtered view.
- Bound chart payloads by querying daily rows over the requested range. Downsampling is not part of v1.

### Tag pattern analytics

Tag pattern analysis helps understand how developers publish container tags before cleanup policy simulation exists. The goal is to answer "what conventions are in use?" and "which conventions produce long-lived storage growth?"

Classifier behavior:

- Use ordered regex patterns stored in `docker_tag_patterns`.
- Ship built-in patterns for SemVer, branch names, PR tags, ticket IDs, commit SHAs, dates, environment/channel tags, and opaque generated tags.
- Allow admins to edit built-in patterns, disable them, and add custom patterns through the UI.
- Generate suggested patterns from frequent unclassified tags so users do not need to guess conventions upfront.
- Store all matches plus a highest-priority primary category for aggregate reporting.
- Keep unmatched tags visible as `unclassified` rather than hiding them.
- Reclassify existing tags when pattern definitions change.

Analytics views:

- Top tag patterns by tag count.
- Top tag patterns by logical bytes, unique bytes, and growth.
- Pattern breakdown by Nexus instance, repository, image namespace/prefix, age, and last downloaded status.
- Example tags for each pattern, including recent examples and largest examples.
- Pattern trend over time, showing whether branch/PR/opaque tags are growing faster than release tags.
- Unclassified tag explorer to help admins create new custom patterns.
- Suggested pattern candidates derived from tokenized unclassified tags and repeated separators/prefixes.

Cleanup-policy assist:

- Recommend candidate cleanup policy building blocks such as "PR tags older than N days", "feature tags never downloaded", or "opaque generated tags with no recent pulls".
- Do not execute cleanup in v1.
- Keep recommendations explainable by linking every recommendation to matching tags and examples.

### Future package formats

The core v1 domain is Docker/OCI. PyPI, npm, and Go module repositories use a generic artifact layer first, but they should not weaken the container-specific graph model.

Extension approach:

- Keep container tables optimized for image/tag/manifest/layer analysis.
- Use `generic_artifacts` for initial non-container asset indexing from the Nexus Assets API.
- Use `repositories.format` to route indexers and UI views.
- Reuse Nexus REST asset ingestion across formats where useful, but add format-specific models only when a second format is implemented.
- Add deeper format-specific indexers for PyPI packages, npm packages, and Go modules later.
- Keep cleanup simulation policy format-aware; do not force layer-sharing concepts onto package types where they do not apply.

Future format examples:

- **PyPI:** package name, version, file type, Python tags, upload timestamp, download metadata.
- **npm:** package name, version, dist tarball, scope, publish timestamp, download metadata.
- **Go modules:** module path, semantic version, pseudo-version, zip/mod/info assets, proxy cache metadata.

## Cleanup policy simulation

Policy examples:

```yaml
name: docker-pr-cleanup
scope:
  repositories:
    - docker-hosted
keep:
  tags:
    - latest
    - stable
    - /^v\d+\.\d+\.\d+$/
  last_n_per_image: 20
delete:
  tag_patterns:
    - /^pr-/
  older_than: 30d
  never_downloaded_older_than: 14d
safety:
  require_last_backup: false
  max_delete_per_run: 10000
```

Simulation output:

```text
Would delete: 41,204 tags
Candidate manifests: 39,881
Logical size: 19.4 TB
Estimated reclaimable: 6.8 TB
Protected by pattern: 813 tags
Missing metadata warnings: 441 tags
Required Nexus follow-up tasks:
  - Docker - Delete unused manifests and images
  - Nexus cleanup/compaction tasks if available in the target deployment
```

## Cleanup execution: later phase

Execution should be explicitly out of v1 but designed for.

Required controls:

- Dry-run first.
- Approval workflow.
- Immutable audit log.
- Protected tag rules.
- Maximum delete count/bytes per run.
- Rate limiting.
- Maintenance windows.
- Post-delete verification.
- Clear statement that compaction may be required to reclaim disk.

Possible execution methods:

- Nexus REST API delete component/asset.
- Docker Registry API delete manifest where supported/configured.
- Nexus cleanup policy/task integration.
- Export-only mode that produces a script or JSON plan.

## Kubernetes deployment integration

Nexus Lens uses Kubernetes as the runtime platform only. It does not scan Kubernetes workloads or collect deployed image usage in v1.

RBAC:

- ServiceAccount permissions should be limited to what the application needs for its own runtime resources.
- Nexus Lens does not need read access to Pods, Deployments, StatefulSets, DaemonSets, Jobs, CronJobs, or custom workload owners.
- Secret access should be limited to configured Nexus, Postgres, session, and bootstrap-admin Secret references in the `nexus-lens` namespace.
- Worker pod names are self-reported through the Downward API, such as `POD_NAME`; Nexus Lens does not need to list Pods to show worker identity.

Credential reference mode:

- v1 default is GitOps-managed credential aliases. Helm values define named aliases that map to Kubernetes Secret refs.
- The UI can select, display status for, and validate configured aliases, but it should not create arbitrary Secret refs by default.
- This keeps ServiceAccount RBAC narrow: `get` only on explicitly configured Secrets.
- A later dynamic mode can allow broad namespace Secret lookup, but it must be opt-in, audited, and clearly warned in Helm values.

Configuration model:

- Helm values define installation-time wiring: Nexus endpoints, credential aliases, database Secret name, initial schedule defaults, and RBAC settings.
- ConfigMaps can provide repository filters and default indexing settings.
- PostgreSQL stores mutable application config, imported schedules, run history, task state, checkpoints, verification findings, operator warnings, and trend snapshots. Operator warnings use `verification_findings` in v1.
- GitOps users can manage Helm values, ConfigMaps, Secrets, and CNPG `Cluster` resources directly.
- No Nexus Lens CRDs or custom operator are required.

## Web UI draft

### Overview

Cards:

- Nexus asset bytes.
- Docker logical bytes.
- Estimated reclaimable bytes.
- Old tags.
- Never-pulled tags.
- Last successful index run.

Charts:

- Storage growth by repository.
- Reclaimable bytes by repository.
- Tag age distribution.
- Top repositories by growth.

### Repository explorer

Filters:

- Repository.
- Image name.
- Tag pattern.
- Age.
- Last downloaded.
- Unique bytes.

Table columns:

- Image.
- Tag.
- Digest.
- Pushed.
- Last downloaded.
- Logical size.
- Unique bytes.
- Shared bytes.
- Cleanup candidate.

### Policy simulator

Later phase.

Inputs:

- YAML editor.
- Form builder.
- Repository scope selector.
- Protected tag patterns.
- Time windows.

Outputs:

- Candidate table.
- Risk warnings.
- Estimated reclaimable bytes.
- Exportable plan.

## API draft

```text
GET    /api/v1/health
GET    /api/v1/ready
GET    /api/v1/openapi.json
POST   /api/v1/auth/login
POST   /api/v1/auth/logout
GET    /api/v1/auth/me
GET    /api/v1/config/nexus-instances
POST   /api/v1/config/nexus-instances
PATCH  /api/v1/config/nexus-instances/{id}
GET    /api/v1/config/nexus-instances/{id}/capabilities
POST   /api/v1/config/nexus-instances/{id}/probe-capabilities
GET    /api/v1/config/schedules
POST   /api/v1/config/schedules
PATCH  /api/v1/config/schedules/{id}
GET    /api/v1/index/status
GET    /api/v1/index/runs
POST   /api/v1/index/runs
GET    /api/v1/index/runs/{id}
GET    /api/v1/index/runs/{id}/progress
POST   /api/v1/index/runs/{id}/cancel
POST   /api/v1/index/runs/{id}/pause
POST   /api/v1/index/runs/{id}/resume
POST   /api/v1/index/runs/{id}/restart
POST   /api/v1/index/runs/{id}/retry-failed
POST   /api/v1/index/bounded-reindex
GET    /api/v1/index/tasks
GET    /api/v1/index/tasks/running
GET    /api/v1/index/checkpoints
GET    /api/v1/index/verification
POST   /api/v1/index/verification/runs
GET    /api/v1/index/verification/findings
POST   /api/v1/index/verification/findings/{id}/dismiss
POST   /api/v1/index/pause
POST   /api/v1/index/resume
GET    /api/v1/repositories
GET    /api/v1/repositories/{repo}/images
GET    /api/v1/images/{image_id}/tags
GET    /api/v1/tags/{tag_id}
GET    /api/v1/tag-patterns
POST   /api/v1/tag-patterns
PATCH  /api/v1/tag-patterns/{id}
POST   /api/v1/tag-patterns/reclassify
GET    /api/v1/analytics/overview
GET    /api/v1/analytics/storage-trends
GET    /api/v1/analytics/reclaimable
GET    /api/v1/analytics/tag-age
GET    /api/v1/analytics/tag-patterns
GET    /api/v1/analytics/tag-patterns/{id}/examples
GET    /api/v1/analytics/tag-pattern-trends
GET    /api/v1/analytics/growth
GET    /api/v1/analytics/index-freshness
GET    /api/v1/users
POST   /api/v1/users
PATCH  /api/v1/users/{id}
POST   /api/v1/users/{id}/reset-password
GET    /api/v1/audit-log
```

Later:

```text
GET  /api/v1/policies
POST /api/v1/policies
PATCH /api/v1/policies/{id}
POST /api/v1/policies/{id}/simulate
POST /api/v1/policies/simulate
POST /api/v1/cleanup/plans
POST /api/v1/cleanup/plans/{id}/approve
POST /api/v1/cleanup/plans/{id}/execute
POST /api/v1/nexus-webhooks/{nexus_instance_name}
GET  /api/v1/nexus-webhooks/events
POST /api/v1/nexus-webhooks/events/{id}/reprocess
```

API behavior:

- Every list endpoint supports cursor pagination, stable sorting, and filter parameters.
- Every mutating endpoint requires CSRF protection for browser sessions or bearer-token auth for API clients.
- Long-running operations return a run ID and are observable through index run/task endpoints.
- Manual run creation should accept idempotency keys so UI retries do not enqueue duplicate full scans or bounded reindex runs.
- Mutating index-run endpoints must enforce per-Nexus operation locks and return a structured conflict response when blocked.
- Scheduled runs blocked by an active per-Nexus operation lock should create or record a `skipped_blocked` run event with the blocking run ID.
- API responses include `request_id` for log correlation.
- Sensitive config endpoints return Secret references, never secret values.

## Deployment

### Kubernetes/Helm for production

Kubernetes is the primary production target.

Default namespace assumption:

- The Helm chart installs all Nexus Lens resources into one namespace, normally `nexus-lens`.
- Cross-namespace Secret references should not be required for the default install.
- Advanced split-namespace deployments can be considered later, but v1 should optimize for one namespace.

Pod design:

- **API pod:** serves the TypeScript UI, `/api/v1`, `/metrics`, readiness, and liveness. It should be horizontally scalable and stateless except for database access.
- **Worker pod:** consumes queued index tasks, runs reconciliation, refreshes rollup tables, and performs trend snapshotting. It should support multiple replicas with database locking and per-Nexus concurrency limits.
- **API startup migrations:** API pods run `goose up` on startup by default using a PostgreSQL advisory lock.
- **Scheduler trigger pod or scheduler loop:** wakes frequently, asks PostgreSQL which schedules are due, enqueues work, and exits quickly if implemented as a CronJob. It should not perform long indexing work directly.
- **Optional one-shot Job pods:** run manual local index rebuilds or validation checks with explicit resource limits.

Kubernetes resources:

- Deployment: Go API server.
- Deployment: Go worker process for queue consumers.
- CronJob: optional lightweight scheduler tick when an in-process scheduler loop is not used.
- Jobs: manually triggered index runs when isolation is useful.
- Existing external/managed PostgreSQL, including CNPG-managed databases.
- Secret: Nexus credentials, database credentials, UI session secret, initial admin bootstrap.
- ConfigMap: index schedules/repo config for v1.
- PVC: optional cache.
- Service and Ingress/Gateway API: UI/API access.
- ServiceAccount, Role/ClusterRole, RoleBinding/ClusterRoleBinding.
- ServiceMonitor: Prometheus metrics.
- NetworkPolicy: restrict egress to Nexus, Postgres, Kubernetes API, and required auth endpoints.

Nexus Lens does not require access to Nexus blob-store disks or object-storage buckets. All v1 indexing should work against remote Nexus deployments through Nexus REST and registry APIs.

Default network ports:

- API/UI container port: `5601`.
- Kubernetes Service port: `5601`.
- Local development URL: `http://localhost:5601`.
- Metrics path: `/metrics` on port `5601` by default.
- Optional separate metrics port: `9090`.

Helm chart shape:

```text
charts/nexus-lens/
  Chart.yaml
  values.yaml
  values.schema.json
  templates/deployment-api.yaml
  templates/deployment-worker.yaml
  templates/cronjob-scheduler.yaml
  templates/configmap.yaml
  templates/secret-refs.yaml
  templates/service.yaml
  templates/ingress.yaml
  templates/rbac.yaml
  templates/servicemonitor.yaml
  templates/networkpolicy.yaml
```

Important Helm values:

```yaml
image:
  repository: ghcr.io/example/nexus-lens
  tag: ""

logging:
  format: json
  level: info
  includeCaller: false
  includeStacktrace: error
  sampleDebugLogs: true
  redact:
    enabled: true

api:
  replicas: 2
  port: 5601
  resources: {}
  ingress:
    enabled: false
  metrics:
    path: /metrics
    separatePort:
      enabled: false
      port: 9090

worker:
  replicas: 2
  resources: {}
  maxConcurrentTasks: 8

scheduler:
  mode: cronjob
  tick: "*/1 * * * *"

postgres:
  existingSecret: nexus-lens-app
  hostKey: host
  portKey: port
  databaseKey: dbname
  usernameKey: user
  passwordKey: password
  sslMode: require
  migrations:
    runOnStartup: true
    optionalJob:
      enabled: false

auth:
  sessionSecretRef: nexus-lens-session
  forceInitialAdminPasswordRotation: true
  passwordHash:
    algorithm: argon2id
    memoryKiB: 19456
    iterations: 2
    parallelism: 1
    saltBytes: 16
    keyBytes: 32
  initialAdmin:
    secretRef: nexus-lens-initial-admin
    usernameKey: username
    passwordKey: password

nexus:
  instances:
    - name: main
      baseURL: https://nexus.example.com
      authSecretRef: nexus-main-readonly
      repositories:
        include: ["docker-*"]
        exclude: []
      registry:
        routingMode: path
        baseURL: https://nexus.example.com
      rateLimits:
        requestsPerSecond: 5
        burst: 10
        maxConcurrentRepos: 2
        maxConcurrentManifests: 8
    - name: python-packages
      baseURL: https://nexus-python.example.com
      authSecretRef: nexus-python-readonly
      repositories:
        include: ["pypi-hosted"]
        exclude: []
      formats:
        enabled: ["pypi"]
        mode: discover-only

indexing:
  scheduleOwnership: app-managed
  schedules:
    # Imported into PostgreSQL as defaults on first install.
    recent:
      enabled: true
      cron: "*/30 * * * *"
      timezone: UTC
      maxRuntime: 20m
    fullReconcile:
      enabled: true
      cron: "0 2 * * 0"
      timezone: UTC
      maxRuntime: 6h
    boundedReindex:
      enabled: true
      cron: "0 1 * * 0"
      window: 7d
      maxRuntime: 2h
    verification:
      enabled: true
      cron: "0 3 * * *"
    trendSnapshot:
      enabled: true
      cron: "0 4 * * *"
  maxPagesPerTask: 20
  checkpointEveryItems: 500
  retry:
    maxAttempts: 5
    baseDelay: 5s
    maxDelay: 5m

worker:
  retention:
    intervalSeconds: 3600
    scanRunDays: 30
    cleanupExecutionDays: 365
    verificationFindingDays: 180
    trendSnapshotDays: 365

```

PostgreSQL compatibility:

- The chart should accept an existing Secret for any PostgreSQL deployment, including CNPG-generated application Secrets.
- Nexus Lens does not create, own, or standardize the PostgreSQL Secret. Operators provide an existing Secret reference and map the key names through Helm values.
- The chart examples can default to common key names such as `host`, `port`, `dbname`, `user`, and `password`, but every key must be configurable.
- TLS mode should be configurable through `postgres.sslMode` and `postgres.sslRootCertSecretRef`.
- Nexus Lens should not require superuser privileges. The database user needs schema ownership or migration privileges plus normal DML privileges.

Helm validation:

- Provide `values.schema.json` for common misconfigurations.
- Render successfully with generic external Postgres and CNPG-managed Postgres profiles.
- Use checksum annotations so ConfigMap/Secret reference changes roll API and worker pods.
- Keep all container security contexts restricted by default: non-root, read-only root filesystem where practical, dropped capabilities.
- Include resource requests/limits examples for small, medium, and large Nexus instances.
- CI should run `helm template` and Kubernetes schema validation against representative values files.

### Docker Compose for local development

Services:

- API/server.
- Worker.
- Web UI.
- PostgreSQL.

Compose is useful for local iteration but should not define the production architecture.

## Security

### Credentials

- Store Nexus credentials in Kubernetes Secret, environment, or external secret provider.
- Use least-privilege Nexus user where possible.
- Do not log credentials.
- Redact URLs containing credentials.
- Support separate read-only and executor credentials later if operators need stricter separation. The first implementation uses the same Nexus basic credential for REST, registry, and optional cleanup execution.

Nexus authentication:

- Support Nexus basic username/password credentials from Kubernetes Secrets first.
- Token credentials can be added later if needed.
- Support per-instance credentials so multiple Nexus servers do not share auth.
- Use the same basic credential for Nexus REST and registry API in the first implementation. Separate registry/REST credentials can be added later if needed.
- Validate credentials with a cheap health/repository probe before starting a full scan.
- Store only Secret names/keys in PostgreSQL; never copy secret material into the database.
- Redact `Authorization`, cookies, query credentials, and embedded URL credentials in logs/errors.
- Expose credential status in the UI as connected, unauthorized, forbidden, rate-limited, or unknown.

PostgreSQL authentication:

- Read database credentials only from Kubernetes Secret or local development environment.
- Prefer CNPG-generated application users for in-cluster deployments.
- Support TLS-required connections and custom CA Secret references.
- Keep API and worker pool sizes bounded so database connections do not starve CNPG failover or maintenance.
- Run migrations with a migrator role when separate roles are configured.

User authentication:

- Start with built-in basic login/session auth.
- Store app users, password hashes, roles, disabled state, and login timestamps in PostgreSQL.
- Store app role capability lists in PostgreSQL so deployments can add narrower roles without code changes. Seed `admin` and `read_only` roles by migration.
- Bootstrap the first admin user from a Kubernetes Secret on first install.
- Initial roles are `admin` and `read-only`.
- Admins can configure Nexus instances, schedules, credentials references, rate limits, and indexing actions.
- Read-only users can view dashboards, repository data, index status, trends, and verification findings.
- Support local development auth bypass only when explicitly enabled.
- OIDC can be added later without changing the internal role model.
- All admin actions should be audited.

### Multi-user UI

Roles:

- Read-only: read dashboards, repository data, trends, index status, and verification findings.
- Admin: configure instances, schedules, credentials references, rate limits, and indexing actions.
- Custom roles: map a role name to explicit capabilities such as `manage_index`, `manage_settings`, `manage_tag_patterns`, `manage_findings`, and `execute_cleanup`.

## Observability

Prometheus metrics:

```text
nexus_lens_index_last_success_timestamp
nexus_lens_index_run_duration_seconds
nexus_lens_index_errors_total
nexus_lens_api_errors_total
nexus_lens_index_lag_seconds
nexus_lens_asset_inventory_lag_seconds
nexus_lens_docker_enrichment_lag_seconds
nexus_lens_index_queue_depth
nexus_lens_index_enrichment_backlog
nexus_lens_index_schedules_skipped_total
nexus_lens_index_queue_claim_duration_seconds
nexus_lens_index_queue_oldest_pending_seconds
nexus_lens_index_task_attempts_total
nexus_lens_index_task_failures_total
nexus_lens_index_task_leases_reaped_total
nexus_lens_index_run_progress_percent
nexus_lens_index_run_elapsed_seconds
nexus_lens_index_running_tasks
nexus_lens_index_task_heartbeat_age_seconds
nexus_lens_index_paused
nexus_lens_repositories_total
nexus_lens_docker_images_total
nexus_lens_docker_tags_total
nexus_lens_docker_tag_patterns_total
nexus_lens_docker_tag_pattern_matches_total
nexus_lens_docker_tags_unclassified_total
nexus_lens_nexus_asset_bytes
nexus_lens_logical_bytes
nexus_lens_estimated_reclaimable_bytes
nexus_lens_never_downloaded_tags_total
nexus_lens_old_tags_total
nexus_lens_nexus_requests_total
nexus_lens_nexus_request_duration_seconds
nexus_lens_nexus_backoff_seconds_total
nexus_lens_nexus_circuit_breaker_open
nexus_lens_nexus_capability_probe_timestamp
nexus_lens_verification_findings_total
nexus_lens_postgres_pool_acquired
nexus_lens_postgres_pool_idle
```

Metric labels:

- Allowed low-cardinality labels: `nexus_instance`, `scan_type`, `task_type`, `status`, `api_surface`, `cost_class`, `severity`, `error_class`, and `repository` only where repository count is known to be modest.
- Avoid labels for image name, tag, digest, asset path, asset ID, user name, or error message.
- High-cardinality detail belongs in PostgreSQL tables and logs, not Prometheus labels.

Logs:

- Structured JSON logs are required in Kubernetes. Pretty console logs are acceptable only for local development.
- Every API request log should include `timestamp`, `level`, `logger`, `message`, `request_id`, `method`, `path`, `status`, `duration_ms`, `remote_ip` or trusted proxy address, authenticated user ID when available, and stable error code/class when the request fails.
- Every worker log should include `worker_id`, Kubernetes pod name, `scan_run_id`, `scan_task_id`, `scan_type`, `task_type`, `attempt`, `nexus_instance`, repository name when known, `cost_class`, and current phase.
- Nexus client logs should include API surface (`rest` or `registry`), method, redacted URL or route template, status code, duration, retry count, backoff duration, circuit-breaker state, and upstream request ID/header values when Nexus provides them.
- PostgreSQL logs emitted by the app should include operation name, duration, row count when useful, SQLSTATE on error, and pool acquisition time. Do not log SQL parameter values unless they are explicitly safe and low volume.
- Error logs should include `error_code`, `error_class`, `retryable`, `attempt`, `max_attempts`, `next_retry_at`, and a redacted, size-limited `error_details` object.
- Panic recovery should log stack traces at error level with request/task context, then return a structured API error or mark the task failed with `error_class = 'bug'`.
- High-volume per-item success logs should be debug level and sampled. State transitions, retries, circuit-breaker changes, skipped schedules, run start/finish, and non-retryable failures should not be sampled.
- Avoid artifact credentials, auth headers, cookies, session IDs, raw password hashes, Secret values, bearer tokens, basic auth fragments, and unredacted URLs.
- Log field names should stay stable because operators will build queries and alerts around them.
- Logs should be correlation-friendly: `request_id` ties UI/API requests to server logs, and `scan_run_id`/`scan_task_id` tie worker logs to database records and UI run pages.

Index status UI should be backed by:

- Current and recent `scan_runs`.
- Queue depth by task type/status.
- Oldest pending task age.
- Active worker leases.
- Failed task count and retry state.
- Last successful checkpoint by repository.
- Last rollup refresh time.
- Last API capability probe by Nexus instance.
- Current Nexus throttle/backoff/circuit-breaker state.
- Open verification findings.

Alerts:

- No successful recent sync within configured freshness SLO.
- Asset inventory lag exceeds SLO.
- Docker enrichment lag exceeds SLO.
- Enrichment backlog grows for longer than threshold.
- Full reconciliation failed for N consecutive runs.
- Queue oldest pending task exceeds threshold.
- Scheduled run skipped as blocked for longer than threshold.
- One error class, such as `auth`, `rate_limited`, or `postgres`, dominates failures for longer than threshold.
- Nexus circuit breaker open longer than threshold.
- Verification findings with high severity.
- Postgres migration failed.
- Postgres connection pool exhaustion.

## Testing and validation

### GitHub Actions workflows

Use GitHub Actions for build, CI, and E2E.

Workflows:

```text
.github/workflows/ci.yml
.github/workflows/e2e.yml
.github/workflows/release.yml
```

CI workflow:

- Go format, vet, unit tests.
- TypeScript lint, typecheck, unit tests.
- OpenAPI generation check.
- TypeScript client generation check.
- `goose` migration validation against Postgres service container.
- `sqlc` generation check.
- Helm template and values schema validation.
- Docker image build smoke test.

E2E workflow:

- Start Postgres service container.
- Start Nexus test container.
- Seed hosted Docker repositories with deterministic data.
- Start Nexus Lens API/worker/UI.
- Run initial sync, bounded reindex, verification, and daily snapshot jobs.
- Run API assertions.
- Run Playwright browser tests.

Release workflow:

- Build multi-arch container images.
- Package Helm chart.
- Attach generated OpenAPI spec and checksums.
- Publish release artifacts only after CI and E2E pass.

### Unit tests

Go unit tests:

- Nexus REST client pagination, auth errors, retry decisions, and redaction.
- Registry client manifest parsing for OCI image, Docker schema 2, manifest lists, and missing tags.
- Error classifier coverage for retryable/non-retryable Nexus, registry, PostgreSQL, cancellation, lost-lease, invalid-cursor, and bug cases.
- Structured logging field stability and redaction for credentials, cookies, auth headers, embedded URL credentials, and Secret values.
- Docker tag pattern classifier for SemVer, branch tags, PR tags, ticket tags, SHA tags, dates, channels, generated tags, and unclassified tags.
- Reclaimable-byte calculation with shared layers and protected tags.
- Policy parser and simulator.
- Work queue locking, retry, stale lock recovery, and checkpoint updates.
- Postgres query methods generated by `sqlc` where practical with lightweight database tests.

TypeScript unit tests:

- Generated API client usage wrappers.
- Policy form validation.
- Chart data transformations.
- Table filtering/sorting state.
- Route/query-param persistence for saved views.

### Integration tests

Use Docker-based integration tests for local and CI validation:

- Sonatype Nexus Repository Manager container with seeded Docker repositories.
- PostgreSQL container using the same migrations as production.
- Optional local registry helper for generating deterministic image manifests before pushing to Nexus.
- Nexus Lens API and worker containers built from the current branch.

Seed data should include:

- Two hosted Docker repositories for the first E2E fixture, with room to add more repositories in nightly/stress profiles.
- Hundreds to thousands of images/tags for normal CI so runtime and storage stay reasonable.
- A larger nightly profile with tens of thousands of tags and shared layers.
- A stress/performance profile that can model millions of tags through generated fixtures or a dedicated non-CI environment.
- Manifest lists/multi-arch images.
- Tags with shared layers, unique layers, old timestamps, missing last download metadata, and deleted/recreated tags.
- Tags that move to a new digest and tags that are deleted then recreated.
- Tags covering common patterns: `feat-dvo-1313-hsdfjw`, `v1.2.3`, `1.2.3`, `pr-123`, `latest`, `stable`, short SHA, long SHA, date tags, and opaque generated tags.

Validation cases:

- Capability probe records Nexus version, available APIs, asset timestamp support, and pagination behavior without assuming a fixed server page size.
- Initial sync indexes all seeded repositories/images/tags/manifests/layers.
- Periodic sync picks up new tags, changed tag digests, removed tags, and new Nexus assets.
- Assets API ingestion stores `blobCreated`, `lastModified`, and `lastDownloaded` when Nexus returns them.
- Bounded reindex refreshes only the requested time window/scope and does not mark out-of-scope missing tags deleted.
- Bounded reindex uses candidate narrowing and does not silently fall back to a whole-repository scan.
- Per-Nexus operation locks block recent sync, bounded reindex, and full reconcile while an exclusive operation is active.
- Scheduled bounded reindex over the previous 7 days is skipped or marked blocked when another exclusive operation is active.
- Index progress reports unknown, estimated, and measured progress states correctly.
- Interrupted worker resumes from checkpoint without duplicating rows or skipping pages.
- Multiple workers claim tasks concurrently without duplicate execution under `FOR UPDATE SKIP LOCKED`.
- Expired leases are reclaimed and late worker completion updates are rejected by lock-token checks.
- Rate limits keep Nexus request concurrency under configured values.
- Verification findings are created for intentionally corrupted index rows.
- Rollup tables update after successful runs and daily trend snapshots are written by the snapshot schedule.
- Tag pattern analytics match seeded expected counts and example tags.
- Cleanup simulation expected reclaimable bytes match deterministic fixture calculations.
- Migrations apply cleanly from an empty database and from the previous schema version.
- Helm templates render valid manifests for CNPG and external Postgres profiles.
- Security contexts, RBAC, and NetworkPolicy render as expected for the one-namespace install.
- First-install admin bootstrap creates an admin only when no app users exist.
- Automatic startup migrations are safe when multiple API pods start concurrently.

### E2E tests

End-to-end tests should run the full stack with Docker Compose or a test harness:

- Start Nexus, Postgres, API, worker, and UI with Docker Compose.
- Seed Nexus with deterministic container data.
- Run initial sync through the API.
- Wait for scan completion through `/api/v1/index/runs`.
- Assert repository, tag, manifest, analytics, trend, and verification API responses.
- Kill a worker during a large run and assert resume behavior.
- Run a periodic sync after mutating seeded data and assert deltas.
- Run a bounded reindex for the last 30 days and assert only scoped recent records are refreshed.
- Attempt a second reindex for the same Nexus during an active reindex and assert the API returns a conflict with the blocking run ID.

### Playwright UI tests

Playwright should validate real browser behavior against the E2E stack:

- Login or development-auth flow.
- Overview dashboard renders non-empty metrics and charts.
- Repository explorer filters by repository, image, tag pattern, age, last downloaded status, and unique bytes.
- Index status page shows running, completed, failed, paused, and retried states.
- Queue monitor shows active and failed tasks.
- Running Tasks page shows worker pod names, elapsed time, heartbeat age, attempt count, progress labels, and restart/retry actions.
- Full reconcile confirmation modal shows blocking warning, active schedule impact, and requires typed Nexus name confirmation.
- Bounded reindex confirmation modal explains partial-confidence behavior and selected time window.
- Trend charts render expected daily series after seeded snapshots.
- Tag pattern analytics show top patterns, examples, unclassified tags, and trend charts.
- Admin settings can update rate limits/schedules without exposing Secret values.
- Responsive layout checks for desktop and a narrow viewport.

Playwright should include screenshot traces for failures and at least one accessibility scan for core pages.

### Performance validation

- Measure initial sync throughput against the normal and large seed profiles.
- Measure Nexus request rate and confirm throttling matches config.
- Measure API latency for main dashboard queries against large seed data.
- Explain/analyze reclaimable-byte and trend queries before release.
- Explain/analyze queue claim, queue status, repository explorer, tag pattern analytics, and reclaimable-byte queries against large seed data.
- Measure queue claim latency with multiple worker replicas and a large pending task backlog.
- Track Postgres table sizes and index bloat in nightly tests.

## Phased implementation plan

Each phase should be independently shippable to a development environment. Do not start a later phase by depending on unverified behavior from an earlier phase; keep the E2E harness green as functionality accumulates.

First usable release boundary:

- The first broadly useful release should include Phases 0-6.
- Cleanup simulation, package-format work, and destructive execution remain later.
- Phase 6 tag-pattern analytics is included because it is core to understanding developer behavior and designing cleanup policies later.

### Phase 0: Skeleton setup

Goal: create the implementation skeleton so all later phases have a consistent repo structure, build path, and CI baseline.

Build:

- Go module layout for API, worker, internal packages, migrations, and CLI entrypoints.
- TypeScript app skeleton with routing, left navigation shell, generated-client placeholder, and basic styling.
- OpenAPI source file and generation scripts.
- `goose` migration directory and first empty/schema-version migration.
- `sqlc` configuration.
- Dockerfiles for API/worker image and local development.
- Helm chart skeleton with `values.yaml`, `values.schema.json`, Service on port `5601`, API Deployment, worker Deployment, Secrets/ConfigMap references, RBAC, and NetworkPolicy.
- Docker Compose skeleton under `deploy/compose` for Nexus, PostgreSQL, API, worker, and web.
- `test/fixtures` directory for deterministic Nexus seed data and expected fixture metadata.
- GitHub Actions `ci.yml`, `e2e.yml`, and `release.yml` skeletons.
- Makefile or task runner targets for `format`, `test`, `typecheck`, `openapi`, `sqlc`, `migrate`, `helm-template`, and `e2e`.

Exit criteria:

- `go test ./...` runs, even if most packages are placeholders.
- TypeScript typecheck runs.
- Helm chart renders with default values.
- Docker image builds.
- GitHub Actions CI skeleton runs the basic checks.
- App starts locally on `http://localhost:5601` and serves a placeholder UI plus `/api/v1/health`.

### Phase 1: Nexus and E2E fixture spike

Goal: prove the external systems, Docker Compose E2E path, and deterministic Nexus fixture before committing to the full indexing core.

Build:

- Docker Compose stack for Nexus, PostgreSQL, API placeholder, worker placeholder, and web placeholder.
- Deterministic seed helper that creates two hosted Docker repositories in Nexus.
- Fixture image builder/pusher that produces shared layers across images and repositories.
- Seeded Docker tags covering SemVer, SHA, PR, feature branch, `latest`, and `stable`.
- Fixture mutations for moved, deleted, and recreated tags.
- Prove Go regclient can list tags and fetch manifests from hosted Nexus Docker repos.
- Keep equivalent `regctl` commands documented for troubleshooting.
- Prove Nexus REST API can list repositories/assets/components with useful timestamps on Nexus 3.88.0+.
- Build a thin Postgres schema with `goose`, `pgxpool`, and `sqlc`.
- Produce a minimal OpenAPI contract and generated TypeScript client.
- Prove Docker-based Nexus/Postgres E2E seeding works.

Exit criteria:

- `docker compose` starts Nexus and PostgreSQL locally with persistent named volumes or a documented reset command.
- A local command can create two hosted Docker repositories and push deterministic fixture images/tags.
- Fixture manifests prove at least one shared layer across images and at least one shared layer across repositories.
- Fixture mutation commands can move a tag to a new digest, delete a tag, and recreate a deleted tag.
- A local command can query Nexus Assets API and Docker Registry API for the seeded repositories.
- A local command can seed Nexus, index a small hosted Docker repo, and query indexed tags from Postgres.
- A generated TypeScript client can call a Go `/api/v1/health` endpoint.
- Known unknowns are documented before Phase 2 starts.

### Phase 2: Application foundation

Goal: establish the deployable skeleton and contracts that every later phase builds on.

Build:

- Go API skeleton with `/api/v1`, OpenAPI generation, structured errors, request IDs, health, and readiness.
- Structured JSON logging, redaction middleware, panic recovery, request logging, and stable API problem responses.
- PostgreSQL startup migrations, connection handling, advisory migration lock, and external Postgres Secret config.
- Basic UI session auth with Postgres-managed users and `admin`/`read-only` roles.
- Initial admin bootstrap from Kubernetes Secret when no app users exist.
- Helm chart with API, worker, Postgres config, Secrets, RBAC, Service, and optional Ingress in one `nexus-lens` namespace.
- TypeScript app shell using the generated API client.
- Docker Compose or test harness for Nexus, Postgres, API, worker, and UI.

Exit criteria:

- Helm templates render for one-namespace generic external Postgres and CNPG-managed external Postgres Secret profiles.
- Migrations run against an empty Postgres and are idempotent under concurrent API startup.
- API and UI start locally with basic auth.
- Initial admin bootstrap works once and is ignored after users exist.
- API errors include stable codes, retryability, request IDs, and no secrets.
- Request logs are structured JSON and can be correlated by request ID.
- Contract generation is part of CI or a local check.

### Phase 3: Durable indexing core

Goal: build the read-only hosted Docker/OCI indexer with resumable work and Nexus-safe throttling.

Build:

- Nexus instance config and basic credential validation.
- Hosted repository discovery with unsupported formats marked skipped.
- Docker image/tag scan.
- Manifest/layer graph.
- Asset metadata ingest.
- Durable scan runs, tasks, checkpoints, retries, and stale-lock recovery.
- Central error classifier, structured task attempt errors, retry/backoff decisions, and detailed worker logging.
- Bounded reindex by time window and optional repository/image/tag-pattern scope.
- Per-Nexus operation locks for mutually exclusive indexing operations.
- API-triggered and scheduler-triggered index runs.
- Balanced per-Nexus rate limiting, backoff, circuit breaker, pause/resume.
- One-full-reconcile deletion confirmation.

Exit criteria:

- Initial sync indexes seeded hosted Docker repos end to end.
- Bounded reindex refreshes a last-30-days scope without marking unrelated missing tags deleted.
- Concurrent bounded reindex/reconcile attempts for the same Nexus are blocked with a visible conflict.
- Worker interruption resumes without skipped pages or duplicate logical records.
- Full reconciliation marks intentionally removed tags deleted after one successful reconcile.
- Failed tasks record `error_code`, `error_class`, redacted details, retry decision, and attempt history.
- Request-rate tests prove Nexus concurrency stays within configured limits.

### Phase 4: Index operations UI and verification

Goal: make indexing understandable and operable through the UI.

Build:

- Index run status and queue monitor.
- Retry, cancel, pause, and resume controls.
- Bounded reindex controls for last 7/30/90 days and custom day-count windows.
- Confirmation modals and typed confirmation for full reconcile.
- Blocked-action explanations that identify the active blocking run.
- Admin settings for Nexus instances, schedules, and per-Nexus rate limits.
- Verification checks and `verification_findings`.
- Verification findings view.
- Prometheus metrics, structured log correlation, and alerts for run freshness, queue age, throttling, error classes, and verification failures.

Exit criteria:

- Admin can start, pause, resume, retry, and inspect index runs from the UI.
- Admin sees warnings before starting full reconcile and cannot start conflicting operations for the same Nexus.
- Admin can inspect failed task details, retryability, next retry time, and the related log/request/run identifiers.
- Read-only users can view status without changing settings.
- Intentional index corruption creates a visible verification finding.
- Playwright covers run status and queue monitor flows.

### Phase 5: Core artifact analytics

Goal: deliver the first useful analytics experience over the indexed Docker/OCI data.

Build:

- Overview dashboard.
- Repository/image/tag explorer.
- Tag age and last-pull views.
- Unique/shared/reclaimable byte calculations.
- Nexus asset byte and Docker logical byte views.

Exit criteria:

- Users can answer top storage, old tag, and never-pulled tag questions from the UI.
- Reclaimable-byte calculations match deterministic shared-layer fixtures.
- Explorer filters are covered by Playwright.

### Phase 6: Docker tag pattern analytics

Goal: understand how developers actually tag images so later cleanup policies are grounded in real usage. Phase 6 is part of the first usable release boundary.

Build:

- Built-in tag pattern definitions for SemVer, branch names, ticket branches, PR tags, SHAs, dates, channels, environments, build numbers, and opaque generated tags.
- Tag classification during indexing.
- Reclassification task when patterns change.
- Tag pattern analytics API and UI.
- Pattern examples, unclassified tag explorer, and pattern trends.
- Pattern metrics by Nexus instance, repository, image namespace/prefix, age, last downloaded status, bytes, and growth.
- Admin-editable tag pattern definitions.
- Discovery workflow that surfaces frequent unclassified shapes and suggests candidate regex patterns.

Exit criteria:

- Seed tags like `feat-dvo-1313-hsdfjw`, `v1.2.3`, `1.2.3`, `pr-123`, `latest`, short SHA, long SHA, and opaque tags classify as expected.
- UI shows top tag patterns, example tags, unclassified tags, and pattern growth.
- Admin can edit a pattern and trigger reclassification.
- UI surfaces discoverable candidate patterns from unclassified indexed tags.
- Pattern analytics can identify candidate cleanup-policy building blocks without executing cleanup.

### Phase 7: Trends and storage analytics

Goal: turn point-in-time index data into historical trend analysis.

Build:

- Trend snapshots.
- Growth, freshness, reclaimable, pattern, and top-mover visualizations.
- Daily time-series API responses.
- Links from trend points back to filtered repository/image/tag views.

Exit criteria:

- Seeded daily snapshots render expected trend series in API and UI.
- Missing snapshot windows are visible rather than silently smoothed over.
- Dashboard queries meet latency targets against the large seed profile.

### Phase 8: Cleanup policy simulation

Goal: use indexed data and tag-pattern analytics to design safe cleanup policies without deleting anything.

Build:

- Policy YAML format.
- Simulation engine.
- Protected tag rules.
- Tag-pattern-based candidate rules.
- Exportable cleanup plan.
- Deterministic fixture validation for reclaimable bytes.

Exit criteria:

- Simulation output explains candidate, protected, and warning categories.
- Pattern-based cleanup candidates link back to matching tags and pattern analytics.
- Playwright covers the simulator YAML editor, validation errors, simulation run, and candidate/protected result tables.
- No delete API or execution path exists in this phase.

### Phase 9: Package formats

Goal: prepare for non-container Nexus repositories without disrupting the Docker/OCI core.

Build:

- Generic artifact model preparation for PyPI, npm, and Go module formats.
- Hosted PyPI/npm/Go discover-only views before full format indexers.

Exit criteria:

- Unsupported hosted formats are visible as skipped/discover-only, not errors.
- Docker/OCI analytics remain unchanged.

### Phase 10: Controlled execution

Goal: add destructive cleanup only after simulation, audit, approval, and guardrails are proven.

Build:

- Approval workflow.
- Nexus cleanup/delete integration.
- Audit log.
- Post-delete verification.
- Compaction/task recommendations where supported by the target Nexus deployment.

Exit criteria:

- Every destructive action requires a prior simulation and explicit approval.
- Execution has rate limits, maintenance windows, audit records, and post-delete verification.
- Read-only mode remains available and default.

## Open questions

- How reliable is `lastDownloaded` metadata for hosted Docker repositories on Nexus 3.88.0+?
- Are hosted Docker repositories always reachable through the Docker Registry API with the same basic auth credentials as Nexus REST?
- Should cleanup simulation output Nexus-native cleanup policy configs, executable delete plans, or both?

## Possible names

- Nexus Lens
- BlobLens
- RepoLens
- Artifact Lens
- Nexus Cartographer
- Registry Intel

Working name: **Nexus Lens**.
