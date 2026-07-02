# Helm Chart Improvement Plan

This plan covers the next chart improvements after the initial API/worker
Deployments, Service, external PostgreSQL Secret refs, optional metrics objects,
schema validation, and Helm render tests.

The chart should stay focused on Nexus Lens. It should not deploy PostgreSQL,
CloudNativePG, Redis, Valkey, Nexus, or any operator. It should make common
Kubernetes installation choices easy while keeping external services explicit.

## Current State

The chart currently supports:

- API and worker Deployments with separate images.
- A Service for the API/UI on port `5601`.
- Existing PostgreSQL Secret references with configurable key names.
- Existing initial admin Secret references.
- Optional Nexus basic auth Secret references.
- Retention and destructive cleanup environment settings.
- Optional `ServiceMonitor` and `PrometheusRule`.
- `values.schema.json` validation for the existing values surface.
- `make helm-check`, which runs `helm lint`, `helm template`, and Go-based chart tests.

The main missing areas are production hardening, access configuration, scheduling
configuration, operational ergonomics, and representative values fixtures.

## Phase 1: Production Basics

These should land first because they are expected in most Kubernetes installs
and are low-risk chart-only changes.

### Pod Security Defaults

Add shared and component-specific security settings:

```yaml
podSecurityContext:
  enabled: true
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
  fsGroup: 65532
  seccompProfile:
    type: RuntimeDefault

containerSecurityContext:
  enabled: true
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  capabilities:
    drop: ["ALL"]
```

Support overrides under `api.securityContext` and `worker.securityContext`.
Keep the default compatible with distroless images.

Tests:

- Default render includes restricted pod and container security contexts.
- Component override changes only the selected component.
- Schema rejects invalid seccomp profile values and non-boolean toggles.

### Resources

Add common resource controls:

```yaml
api:
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits: {}

worker:
  resources:
    requests:
      cpu: 250m
      memory: 512Mi
    limits: {}
```

Do not set aggressive default limits. Requests are useful for scheduling;
limits are often environment-specific and can cause avoidable throttling.

Tests:

- Default render includes requests.
- Custom values render limits and requests exactly.
- Schema rejects invalid resource object shapes.

### Pod Scheduling Controls

Add the standard scheduling knobs for both components:

```yaml
nodeSelector: {}
tolerations: []
affinity: {}
topologySpreadConstraints: []

api:
  nodeSelector: {}
  tolerations: []
  affinity: {}
  topologySpreadConstraints: []

worker:
  nodeSelector: {}
  tolerations: []
  affinity: {}
  topologySpreadConstraints: []
```

Global values apply to both components. Component values merge or replace based
on the simplest behavior we can document clearly. Prefer replacement for lists
and shallow merge for maps.

Tests:

- Global scheduling values apply to both Deployments.
- API-only values do not affect the worker.
- Worker-only values do not affect the API.

### ServiceAccount And RBAC

Add a ServiceAccount even if the app does not need Kubernetes API access today:

```yaml
serviceAccount:
  create: true
  name: ""
  annotations: {}
  automountServiceAccountToken: false

rbac:
  create: false
```

Default `automountServiceAccountToken: false` because Nexus Lens talks to Nexus
and PostgreSQL, not the Kubernetes API. Keep RBAC disabled unless a future
feature needs Kubernetes API reads.

Tests:

- Default render creates a ServiceAccount and uses it in both Deployments.
- Default ServiceAccount token automount is false.
- `serviceAccount.create=false` uses the provided name and does not render a ServiceAccount.

## Phase 2: Access And Runtime Configuration

These are common install requirements and should come after the baseline
hardening is in place.

### Ingress

Add Ingress support:

```yaml
ingress:
  enabled: false
  className: ""
  annotations: {}
  hosts:
    - host: nexus-lens.example.com
      paths:
        - path: /
          pathType: Prefix
  tls: []
```

Keep Gateway API out of v1 unless there is a real deployment need. Ingress is
still the most common baseline.

Tests:

- Disabled by default.
- Enabled render includes host, path, class name, annotations, and TLS.
- Schema rejects missing host when ingress is enabled if we can express that
  cleanly without making the schema hard to maintain.

### Extra Environment And Volumes

Add escape hatches that do not require chart forks:

```yaml
api:
  extraEnv: []
  extraEnvFrom: []
  extraVolumes: []
  extraVolumeMounts: []

worker:
  extraEnv: []
  extraEnvFrom: []
  extraVolumes: []
  extraVolumeMounts: []
```

Use these for deployment-specific config such as proxy settings, custom CA
bundles, or observability agents.

Tests:

- Extra env and envFrom render under the expected container.
- Extra volumes and mounts render only for the selected component.

### Image Pull Secrets

Add registry authentication support:

```yaml
imagePullSecrets: []
```

Tests:

- Default render omits imagePullSecrets.
- Custom render includes all configured pull secrets.

### Logging Configuration

Add values for runtime logging once the binaries support these environment
variables:

```yaml
logging:
  level: info
  format: json
```

If the code does not yet read these variables, chart work should either wait or
include the matching Go change. Avoid chart values that do nothing.

Tests:

- Logging env vars render only after the app supports them.
- Schema limits level and format to known values.

## Phase 3: Nexus And Indexing Configuration

These values matter for the product shape, but they should be aligned with the
application config model before the chart owns them.

### Nexus Instance Bootstrap

The app supports multiple Nexus instances, but the chart currently only exposes
one optional global basic-auth Secret. Add a bootstrap ConfigMap or Secret-ref
driven config only after the app has a clear import path.

Candidate values:

```yaml
nexus:
  instances: []
```

Each instance should support:

- `name`
- `baseURL`
- `credentials.existingSecret`
- `credentials.usernameKey`
- `credentials.passwordKey`
- repository include/exclude patterns
- per-Nexus rate limits
- registry base URL/routing mode for Docker

Important constraint: credentials should stay in Secrets. The chart should not
render usernames or passwords into ConfigMaps.

Tests:

- Multiple Nexus instances render deterministic config without secret material.
- Per-instance Secret refs are represented.
- Schema rejects duplicate instance names if practical. If schema cannot do
  this cleanly, add a Helm template `fail` helper or chart test.

### Schedule Bootstrap

Do not create Kubernetes CronJobs for indexing until the product decides whether
schedules are app-managed in PostgreSQL or cluster-managed. Current design leans
toward app-managed schedules.

Recommended chart path:

```yaml
indexing:
  seedSchedules:
    enabled: false
    recentSync:
      enabled: true
      intervalSeconds: 1800
    boundedReindex:
      enabled: false
      intervalSeconds: 604800
      windowDays: 7
    fullReconcile:
      enabled: false
```

Use environment variables or a bootstrap config consumed by the worker/API only
if the app supports idempotent import.

Tests:

- Disabled by default.
- Enabled seed config renders deterministic env/config.
- Seeded schedule settings are bounded by schema minimums.

## Phase 4: Network And Availability

These features help production operators but need careful defaults.

### NetworkPolicy

Add optional NetworkPolicy:

```yaml
networkPolicy:
  enabled: false
  ingress:
    allowNamespaceLabels: {}
    allowPodLabels: {}
  egress:
    postgres:
      enabled: false
    nexus:
      enabled: false
    dns:
      enabled: true
```

Keep it disabled by default. Correct egress policies require environment-specific
labels, namespaces, CIDRs, or FQDN-capable CNI behavior. A wrong default can
silently break indexing.

Tests:

- Disabled by default.
- Enabled render includes API ingress and worker/API egress scaffolding.
- DNS egress can be toggled.

### PodDisruptionBudget

Add PDBs:

```yaml
api:
  podDisruptionBudget:
    enabled: false
    minAvailable: 1

worker:
  podDisruptionBudget:
    enabled: false
    minAvailable: 1
```

Default disabled because single-replica installs cannot satisfy a useful PDB
during voluntary disruptions.

Tests:

- Disabled by default.
- Enabled render targets the correct component selectors.

### Autoscaling

Add optional HPA later:

```yaml
api:
  autoscaling:
    enabled: false

worker:
  autoscaling:
    enabled: false
```

API autoscaling is straightforward. Worker autoscaling needs more care because
queue concurrency, Nexus rate limits, and database locks must remain the real
control plane.

Tests:

- API HPA render with min/max replicas and CPU target.
- Worker HPA render requires explicit opt-in and documented concurrency caveats.

## Phase 5: Packaging Profiles And Validation

### Representative Values Files

Add chart fixtures under `charts/nexus-lens/ci-values/`:

- `minimal.yaml`: external PostgreSQL and initial admin Secrets only.
- `production-small.yaml`: security contexts, resources, ingress, metrics, and one Nexus Secret.
- `production-large.yaml`: multiple workers, larger resources, metrics, and explicit rate-limit config once supported.
- `cnpg-secret.yaml`: uses common CNPG application Secret key names.

Tests:

- `make helm-check` renders every fixture.
- Chart tests assert important behavior from at least `production-small.yaml`.

### Kubernetes Schema Validation

Add a schema validation step when the tool choice is settled:

- Prefer `kubeconform` if adding a tool install step in CI is acceptable.
- Keep CRDs optional or ignored for `ServiceMonitor` and `PrometheusRule`, unless
  CI installs the Prometheus Operator schemas.

Make target:

```make
helm-validate:
	helm template nexus-lens ./charts/nexus-lens | kubeconform -strict -summary
```

Do not make this required locally until the tool is easy to install or wrapped
in CI.

### Chart README

Add `charts/nexus-lens/README.md` with:

- Install command.
- Required external Secrets.
- Common CNPG Secret mapping example.
- Production values example.
- Upgrade notes around migrations.
- Metrics enablement.
- Troubleshooting for failed migrations, missing Secrets, and readiness failures.

## Suggested Implementation Order

1. Security contexts, resources, ServiceAccount, imagePullSecrets.
2. Ingress and extra env/volumes.
3. Representative values fixtures and expanded chart tests.
4. NetworkPolicy and PDB.
5. Nexus instance bootstrap once the app config import path is clear.
6. Schedule bootstrap once app-managed schedule seeding is finalized.
7. HPA and kubeconform validation.
8. Chart README and production install guide polish.

## Non-Goals For V1

- Deploying PostgreSQL or CNPG.
- Deploying Nexus.
- Deploying Redis, Valkey, or another task queue.
- Cross-namespace Secret lookup.
- Kubernetes API scanning.
- Blob-store disk or object-store access.
- Webhook receiver resources unless the app ships webhook support later.
