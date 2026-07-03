# Nexus Lens Helm Chart

This chart deploys the Nexus Lens API and worker into an existing namespace. It
does not install PostgreSQL, CNPG, Nexus Repository, Redis, or any operator.

## Required Secrets

PostgreSQL connection values are read from an existing Secret. The key names are
configurable because CNPG and external PostgreSQL providers use different
conventions:

```yaml
postgres:
  existingSecret: nexus-lens-postgres
  hostKey: host
  portKey: port
  databaseKey: dbname
  usernameKey: user
  passwordKey: password
  sslMode: require
```

The first admin user is bootstrapped from an existing Secret on first install:

```yaml
auth:
  initialAdmin:
    existingSecret: nexus-lens-initial-admin
    usernameKey: username
    passwordKey: password
```

Nexus credentials are optional at render time and can also be configured in the
app. When provided through Kubernetes, they are read from an existing Secret:

```yaml
nexus:
  credentials:
    existingSecret: nexus-lens-nexus
    usernameKey: username
    passwordKey: password
```

## Production Values

The default values include conservative pod and container security contexts and
resource requests/limits. Production installs should usually set:

- `image.tag` to an immutable release tag.
- `imagePullSecrets` when pulling from a private registry.
- `ingress.enabled`, `ingress.hosts`, and `ingress.tls`.
- `networkPolicy.enabled` if the cluster enforces network policy.
- `metrics.serviceMonitor.enabled` and `metrics.prometheusRule.enabled` when
  the Prometheus Operator CRDs are installed.

Render the production fixture locally:

```sh
helm template nexus-lens ./charts/nexus-lens -f ./charts/nexus-lens/ci-values/production.yaml
```

## Checks

```sh
make helm-check
make helm-package
```

`make helm-check` lints the chart, renders default values, renders CI values
fixtures, and runs Go chart tests for selectors, images, Secret refs, security
contexts, resources, ingress, network policy, ServiceMonitor, and
PrometheusRule output.
