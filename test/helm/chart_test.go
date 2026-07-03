package helm_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type manifest map[string]any

func TestHelmLintDefaultValues(t *testing.T) {
	runHelm(t, "lint", chartPath(t))
}

func TestDefaultRenderUsesSeparateImagesAndReleaseScopedSelectors(t *testing.T) {
	manifests := renderChart(t)

	service := requireManifest(t, manifests, "Service", "nexus-lens-nexus-lens")
	serviceAccount := requireManifest(t, manifests, "ServiceAccount", "nexus-lens-nexus-lens")
	apiDeployment := requireManifest(t, manifests, "Deployment", "nexus-lens-nexus-lens-api")
	workerDeployment := requireManifest(t, manifests, "Deployment", "nexus-lens-nexus-lens-worker")
	requireNoManifest(t, manifests, "ServiceMonitor", "nexus-lens-nexus-lens")
	requireNoManifest(t, manifests, "PrometheusRule", "nexus-lens-nexus-lens")
	requireNoManifest(t, manifests, "Ingress", "nexus-lens-nexus-lens")
	requireNoManifest(t, manifests, "NetworkPolicy", "nexus-lens-nexus-lens")

	assertString(t, mapAt(t, serviceAccount, "metadata"), "name", "nexus-lens-nexus-lens")

	serviceSelector := mapAt(t, service, "spec", "selector")
	assertString(t, serviceSelector, "app.kubernetes.io/name", "nexus-lens")
	assertString(t, serviceSelector, "app.kubernetes.io/instance", "nexus-lens")
	assertString(t, serviceSelector, "app.kubernetes.io/component", "api")

	apiLabels := mapAt(t, apiDeployment, "spec", "template", "metadata", "labels")
	apiSelector := mapAt(t, apiDeployment, "spec", "selector", "matchLabels")
	apiPodSpec := mapAt(t, apiDeployment, "spec", "template", "spec")
	assertString(t, apiLabels, "app.kubernetes.io/instance", "nexus-lens")
	assertString(t, apiSelector, "app.kubernetes.io/instance", "nexus-lens")
	assertString(t, apiPodSpec, "serviceAccountName", "nexus-lens-nexus-lens")
	assertBool(t, mapAt(t, apiPodSpec, "securityContext"), "runAsNonRoot", true)

	apiContainer := requireContainer(t, apiDeployment, "api")
	assertString(t, apiContainer, "image", "ghcr.io/example/nexus-lens-api:dev")
	assertString(t, apiContainer, "imagePullPolicy", "IfNotPresent")
	assertBool(t, mapAt(t, apiContainer, "securityContext"), "readOnlyRootFilesystem", true)
	assertString(t, mapAt(t, apiContainer, "resources", "requests"), "memory", "128Mi")
	apiEnv := envByName(t, apiContainer)
	assertEnvValue(t, apiEnv, "NEXUS_LENS_ADDR", ":5601")
	assertEnvValue(t, apiEnv, "NEXUS_LENS_SESSION_COOKIE_SECURE", "true")
	assertEnvSecretRef(t, apiEnv, "PGHOST", "nexus-lens-postgres", "host")
	assertEnvSecretRef(t, apiEnv, "PGPORT", "nexus-lens-postgres", "port")
	assertEnvSecretRef(t, apiEnv, "PGDATABASE", "nexus-lens-postgres", "dbname")
	assertEnvSecretRef(t, apiEnv, "PGUSER", "nexus-lens-postgres", "user")
	assertEnvSecretRef(t, apiEnv, "PGPASSWORD", "nexus-lens-postgres", "password")
	assertEnvSecretRef(t, apiEnv, "NEXUS_LENS_INITIAL_ADMIN_USERNAME", "nexus-lens-initial-admin", "username")
	assertEnvSecretRef(t, apiEnv, "NEXUS_LENS_INITIAL_ADMIN_PASSWORD", "nexus-lens-initial-admin", "password")
	assertNoEnv(t, apiEnv, "NEXUS_LENS_NEXUS_USERNAME")
	assertNoEnv(t, apiEnv, "NEXUS_LENS_NEXUS_PASSWORD")

	workerLabels := mapAt(t, workerDeployment, "spec", "template", "metadata", "labels")
	workerSelector := mapAt(t, workerDeployment, "spec", "selector", "matchLabels")
	workerPodSpec := mapAt(t, workerDeployment, "spec", "template", "spec")
	assertString(t, workerLabels, "app.kubernetes.io/instance", "nexus-lens")
	assertString(t, workerSelector, "app.kubernetes.io/instance", "nexus-lens")
	assertString(t, workerPodSpec, "serviceAccountName", "nexus-lens-nexus-lens")
	assertBool(t, mapAt(t, workerPodSpec, "securityContext"), "runAsNonRoot", true)

	workerContainer := requireContainer(t, workerDeployment, "worker")
	assertString(t, workerContainer, "image", "ghcr.io/example/nexus-lens-worker:dev")
	assertString(t, workerContainer, "imagePullPolicy", "IfNotPresent")
	assertBool(t, mapAt(t, workerContainer, "securityContext"), "readOnlyRootFilesystem", true)
	assertString(t, mapAt(t, workerContainer, "resources", "requests"), "memory", "256Mi")
	workerEnv := envByName(t, workerContainer)
	assertEnvSecretRef(t, workerEnv, "PGHOST", "nexus-lens-postgres", "host")
	assertEnvValue(t, workerEnv, "NEXUS_LENS_RETENTION_INTERVAL_SECONDS", "3600")
	assertEnvValue(t, workerEnv, "NEXUS_LENS_ENABLE_DESTRUCTIVE_CLEANUP", "false")
	assertNoEnv(t, workerEnv, "NEXUS_LENS_INITIAL_ADMIN_USERNAME")
}

func TestCustomRenderWiresSecretsMetricsAndComponentImages(t *testing.T) {
	values := writeValues(t, `
image:
  tag: shared-tag
  pullPolicy: Always
imagePullSecrets:
  - name: private-registry
serviceAccount:
  create: true
  name: lens-runtime
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/nexus-lens
podAnnotations:
  checksum/config: abc123
api:
  replicas: 2
  port: 8080
  image:
    repository: registry.example.test/nexus-lens/api
    tag: api-tag
    pullPolicy: Never
worker:
  replicas: 3
  image:
    repository: registry.example.test/nexus-lens/worker
    tag: worker-tag
    pullPolicy: IfNotPresent
  retention:
    intervalSeconds: 120
    scanRunDays: 10
    cleanupExecutionDays: 20
    verificationFindingDays: 30
    trendSnapshotDays: 40
nexus:
  credentials:
    existingSecret: nexus-api-credentials
    usernameKey: nexus-user
    passwordKey: nexus-password
postgres:
  existingSecret: app-postgres
  hostKey: hostname
  portKey: service-port
  databaseKey: database-name
  usernameKey: database-user
  passwordKey: database-password
  sslMode: verify-full
auth:
  session:
    cookieSecure: false
  initialAdmin:
    existingSecret: first-admin
    usernameKey: admin-user
    passwordKey: admin-password
cleanup:
  destructiveExecution:
    enabled: true
    deleteRPS: 1.25
    maintenanceWindowUTC:
      start: "02:30"
      end: "03:45"
metrics:
  serviceMonitor:
    enabled: true
    interval: 15s
    labels:
      team: platform
  prometheusRule:
    enabled: true
    labels:
      team: platform
    queueAgeSeconds: 99
    staleScheduleAgeSeconds: 456
    failedRuns24h: 2
    highVerificationFindings: 3
    cleanupRunningAgeSeconds: 123
    cleanupFailedExecutions: 4
ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt
  hosts:
    - host: lens.example.test
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: lens-tls
      hosts:
        - lens.example.test
networkPolicy:
  enabled: true
  ingress:
    enabled: true
  egress:
    enabled: true
`)
	manifests := renderChart(t, "-f", values)

	serviceAccount := requireManifest(t, manifests, "ServiceAccount", "lens-runtime")
	assertString(t, mapAt(t, serviceAccount, "metadata", "annotations"), "eks.amazonaws.com/role-arn", "arn:aws:iam::123456789012:role/nexus-lens")

	apiDeployment := requireManifest(t, manifests, "Deployment", "nexus-lens-nexus-lens-api")
	assertNumber(t, mapAt(t, apiDeployment, "spec"), "replicas", 2)
	apiPodSpec := mapAt(t, apiDeployment, "spec", "template", "spec")
	assertString(t, apiPodSpec, "serviceAccountName", "lens-runtime")
	assertString(t, requireMap(t, sliceAt(t, apiPodSpec, "imagePullSecrets")[0]), "name", "private-registry")
	assertString(t, mapAt(t, apiDeployment, "spec", "template", "metadata", "annotations"), "checksum/config", "abc123")
	apiContainer := requireContainer(t, apiDeployment, "api")
	assertString(t, apiContainer, "image", "registry.example.test/nexus-lens/api:api-tag")
	assertString(t, apiContainer, "imagePullPolicy", "Never")
	apiEnv := envByName(t, apiContainer)
	assertEnvValue(t, apiEnv, "NEXUS_LENS_ADDR", ":8080")
	assertEnvValue(t, apiEnv, "NEXUS_LENS_SESSION_COOKIE_SECURE", "false")
	assertEnvValue(t, apiEnv, "NEXUS_LENS_ENABLE_DESTRUCTIVE_CLEANUP", "true")
	assertEnvValue(t, apiEnv, "NEXUS_LENS_CLEANUP_DELETE_RPS", "1.25")
	assertEnvValue(t, apiEnv, "NEXUS_LENS_CLEANUP_MAINTENANCE_START_UTC", "02:30")
	assertEnvValue(t, apiEnv, "NEXUS_LENS_CLEANUP_MAINTENANCE_END_UTC", "03:45")
	assertEnvValue(t, apiEnv, "PGSSLMODE", "verify-full")
	assertEnvSecretRef(t, apiEnv, "NEXUS_LENS_NEXUS_USERNAME", "nexus-api-credentials", "nexus-user")
	assertEnvSecretRef(t, apiEnv, "NEXUS_LENS_NEXUS_PASSWORD", "nexus-api-credentials", "nexus-password")
	assertEnvSecretRef(t, apiEnv, "PGHOST", "app-postgres", "hostname")
	assertEnvSecretRef(t, apiEnv, "PGPORT", "app-postgres", "service-port")
	assertEnvSecretRef(t, apiEnv, "PGDATABASE", "app-postgres", "database-name")
	assertEnvSecretRef(t, apiEnv, "PGUSER", "app-postgres", "database-user")
	assertEnvSecretRef(t, apiEnv, "PGPASSWORD", "app-postgres", "database-password")
	assertEnvSecretRef(t, apiEnv, "NEXUS_LENS_INITIAL_ADMIN_USERNAME", "first-admin", "admin-user")
	assertEnvSecretRef(t, apiEnv, "NEXUS_LENS_INITIAL_ADMIN_PASSWORD", "first-admin", "admin-password")

	workerDeployment := requireManifest(t, manifests, "Deployment", "nexus-lens-nexus-lens-worker")
	assertNumber(t, mapAt(t, workerDeployment, "spec"), "replicas", 3)
	workerPodSpec := mapAt(t, workerDeployment, "spec", "template", "spec")
	assertString(t, workerPodSpec, "serviceAccountName", "lens-runtime")
	workerContainer := requireContainer(t, workerDeployment, "worker")
	assertString(t, workerContainer, "image", "registry.example.test/nexus-lens/worker:worker-tag")
	assertString(t, workerContainer, "imagePullPolicy", "IfNotPresent")
	workerEnv := envByName(t, workerContainer)
	assertEnvValue(t, workerEnv, "NEXUS_LENS_RETENTION_INTERVAL_SECONDS", "120")
	assertEnvValue(t, workerEnv, "NEXUS_LENS_RETENTION_SCAN_RUN_DAYS", "10")
	assertEnvValue(t, workerEnv, "NEXUS_LENS_RETENTION_CLEANUP_EXECUTION_DAYS", "20")
	assertEnvValue(t, workerEnv, "NEXUS_LENS_RETENTION_VERIFICATION_FINDING_DAYS", "30")
	assertEnvValue(t, workerEnv, "NEXUS_LENS_RETENTION_TREND_SNAPSHOT_DAYS", "40")
	assertEnvSecretRef(t, workerEnv, "NEXUS_LENS_NEXUS_USERNAME", "nexus-api-credentials", "nexus-user")
	assertEnvSecretRef(t, workerEnv, "NEXUS_LENS_NEXUS_PASSWORD", "nexus-api-credentials", "nexus-password")

	service := requireManifest(t, manifests, "Service", "nexus-lens-nexus-lens")
	assertNumber(t, requireMap(t, sliceAt(t, mapAt(t, service, "spec"), "ports")[0]), "port", 8080)

	serviceMonitor := requireManifest(t, manifests, "ServiceMonitor", "nexus-lens-nexus-lens")
	assertString(t, mapAt(t, serviceMonitor, "metadata", "labels"), "team", "platform")
	endpoint := requireMap(t, sliceAt(t, mapAt(t, serviceMonitor, "spec"), "endpoints")[0])
	assertString(t, endpoint, "interval", "15s")
	assertString(t, endpoint, "path", "/metrics")
	monitorSelector := mapAt(t, serviceMonitor, "spec", "selector", "matchLabels")
	assertString(t, monitorSelector, "app.kubernetes.io/instance", "nexus-lens")

	prometheusRule := requireManifest(t, manifests, "PrometheusRule", "nexus-lens-nexus-lens")
	assertString(t, mapAt(t, prometheusRule, "metadata", "labels"), "team", "platform")
	rules := sliceAt(t, requireMap(t, sliceAt(t, mapAt(t, prometheusRule, "spec"), "groups")[0]), "rules")
	assertRuleExpr(t, rules, "NexusLensQueueBacklogOld", "nexus_lens_queue_oldest_pending_age_seconds_by_nexus > 99")
	assertRuleExpr(t, rules, "NexusLensIndexingScheduleStale", "nexus_lens_indexing_schedule_stale_age_seconds > 456")
	assertRuleExpr(t, rules, "NexusLensCleanupExecutionStuck", "nexus_lens_cleanup_oldest_running_age_seconds > 123")

	ingress := requireManifest(t, manifests, "Ingress", "nexus-lens-nexus-lens")
	assertString(t, mapAt(t, ingress, "metadata", "annotations"), "cert-manager.io/cluster-issuer", "letsencrypt")
	assertString(t, mapAt(t, ingress, "spec"), "ingressClassName", "nginx")
	assertString(t, requireMap(t, sliceAt(t, mapAt(t, ingress, "spec"), "rules")[0]), "host", "lens.example.test")

	networkPolicy := requireManifest(t, manifests, "NetworkPolicy", "nexus-lens-nexus-lens")
	policyTypes := sliceAt(t, mapAt(t, networkPolicy, "spec"), "policyTypes")
	if len(policyTypes) != 2 {
		t.Fatalf("policyTypes length = %d, want 2", len(policyTypes))
	}
}

func TestHelmSchemaRejectsInvalidValues(t *testing.T) {
	values := writeValues(t, `
api:
  port: 70000
  image:
    repository: registry.example.test/nexus-lens/api
    pullPolicy: Sometimes
postgres:
  existingSecret: ""
cleanup:
  destructiveExecution:
    maintenanceWindowUTC:
      start: "25:99"
`)
	output, err := runHelmAllowError(t, "lint", chartPath(t), "-f", values)
	if err == nil {
		t.Fatalf("helm lint succeeded with invalid values:\n%s", output)
	}
	for _, want := range []string{"/api/port", "/postgres/existingSecret", "/cleanup/destructiveExecution/maintenanceWindowUTC/start"} {
		if !strings.Contains(output, want) {
			t.Fatalf("helm lint output missing %q:\n%s", want, output)
		}
	}
}

func renderChart(t *testing.T, extraArgs ...string) []manifest {
	t.Helper()
	args := []string{"template", "nexus-lens", chartPath(t), "--namespace", "nexus-lens"}
	args = append(args, extraArgs...)
	output := runHelm(t, args...)
	decoder := yaml.NewDecoder(strings.NewReader(output))
	var manifests []manifest
	for {
		var doc manifest
		err := decoder.Decode(&doc)
		if err != nil {
			if strings.Contains(err.Error(), "EOF") {
				break
			}
			t.Fatalf("decode rendered manifest: %v\n%s", err, output)
		}
		if len(doc) > 0 {
			manifests = append(manifests, doc)
		}
	}
	return manifests
}

func runHelm(t *testing.T, args ...string) string {
	t.Helper()
	output, err := runHelmAllowError(t, args...)
	if err != nil {
		t.Fatalf("helm %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return output
}

func runHelmAllowError(t *testing.T, args ...string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm is required for chart tests")
	}
	cmd := exec.Command("helm", args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return output.String(), err
}

func chartPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "charts", "nexus-lens")
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolve chart path: %v", err)
	}
	return abs
}

func writeValues(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		t.Fatalf("write values fixture: %v", err)
	}
	return path
}

func requireManifest(t *testing.T, manifests []manifest, kind, name string) manifest {
	t.Helper()
	for _, m := range manifests {
		if valueAsString(m["kind"]) == kind && stringAt(t, mapAt(t, m, "metadata"), "name") == name {
			return m
		}
	}
	t.Fatalf("rendered manifest %s/%s not found; rendered keys: %v", kind, name, manifestKeys(manifests))
	return nil
}

func requireNoManifest(t *testing.T, manifests []manifest, kind, name string) {
	t.Helper()
	for _, m := range manifests {
		if valueAsString(m["kind"]) == kind && stringAt(t, mapAt(t, m, "metadata"), "name") == name {
			t.Fatalf("did not expect rendered manifest %s/%s", kind, name)
		}
	}
}

func manifestKeys(manifests []manifest) []string {
	keys := make([]string, 0, len(manifests))
	for _, m := range manifests {
		metadata, _ := asMap(m["metadata"])
		keys = append(keys, valueAsString(m["kind"])+"/"+valueAsString(metadata["name"]))
	}
	return keys
}

func requireContainer(t *testing.T, deployment manifest, name string) map[string]any {
	t.Helper()
	containers := sliceAt(t, mapAt(t, deployment, "spec", "template", "spec"), "containers")
	for _, raw := range containers {
		container, ok := asMap(raw)
		if !ok {
			t.Fatalf("container has type %T, want map[string]any", raw)
		}
		if stringAt(t, container, "name") == name {
			return container
		}
	}
	t.Fatalf("container %q not found", name)
	return nil
}

func envByName(t *testing.T, container map[string]any) map[string]map[string]any {
	t.Helper()
	env := make(map[string]map[string]any)
	for _, raw := range sliceAt(t, container, "env") {
		entry, ok := asMap(raw)
		if !ok {
			t.Fatalf("env entry has type %T, want map[string]any", raw)
		}
		env[stringAt(t, entry, "name")] = entry
	}
	return env
}

func assertEnvValue(t *testing.T, env map[string]map[string]any, name, want string) {
	t.Helper()
	entry, ok := env[name]
	if !ok {
		t.Fatalf("env %s not found", name)
	}
	assertString(t, entry, "value", want)
}

func assertEnvSecretRef(t *testing.T, env map[string]map[string]any, name, secretName, secretKey string) {
	t.Helper()
	entry, ok := env[name]
	if !ok {
		t.Fatalf("env %s not found", name)
	}
	secretRef := mapAt(t, entry, "valueFrom", "secretKeyRef")
	assertString(t, secretRef, "name", secretName)
	assertString(t, secretRef, "key", secretKey)
}

func assertNoEnv(t *testing.T, env map[string]map[string]any, name string) {
	t.Helper()
	if _, ok := env[name]; ok {
		t.Fatalf("env %s should not be rendered", name)
	}
}

func assertRuleExpr(t *testing.T, rules []any, alert, expr string) {
	t.Helper()
	for _, raw := range rules {
		rule, ok := asMap(raw)
		if !ok {
			t.Fatalf("rule has type %T, want map[string]any", raw)
		}
		if stringAt(t, rule, "alert") == alert {
			assertString(t, rule, "expr", expr)
			return
		}
	}
	t.Fatalf("alert %q not found", alert)
}

func mapAt(t *testing.T, root any, path ...string) map[string]any {
	t.Helper()
	current := root
	for _, key := range path {
		m, ok := asMap(current)
		if !ok {
			t.Fatalf("path %s: value has type %T, want map[string]any", strings.Join(path, "."), current)
		}
		current = m[key]
	}
	m, ok := asMap(current)
	if !ok {
		t.Fatalf("path %s: value has type %T, want map[string]any", strings.Join(path, "."), current)
	}
	return m
}

func asMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case manifest:
		return map[string]any(typed), true
	default:
		return nil, false
	}
}

func requireMap(t *testing.T, value any) map[string]any {
	t.Helper()
	m, ok := asMap(value)
	if !ok {
		t.Fatalf("value has type %T, want map[string]any", value)
	}
	return m
}

func sliceAt(t *testing.T, root map[string]any, key string) []any {
	t.Helper()
	value, ok := root[key]
	if !ok {
		t.Fatalf("key %s not found", key)
	}
	slice, ok := value.([]any)
	if !ok {
		t.Fatalf("key %s has type %T, want []any", key, value)
	}
	return slice
}

func assertString(t *testing.T, root map[string]any, key, want string) {
	t.Helper()
	got := stringAt(t, root, key)
	if got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func stringAt(t *testing.T, root map[string]any, key string) string {
	t.Helper()
	value, ok := root[key]
	if !ok {
		t.Fatalf("key %s not found", key)
	}
	got := valueAsString(value)
	if got == "" && value != "" {
		t.Fatalf("key %s has type %T, want string-like value", key, value)
	}
	return got
}

func assertNumber(t *testing.T, root map[string]any, key string, want int) {
	t.Helper()
	value, ok := root[key]
	if !ok {
		t.Fatalf("key %s not found", key)
	}
	got, ok := value.(int)
	if !ok {
		t.Fatalf("key %s has type %T, want int", key, value)
	}
	if got != want {
		t.Fatalf("%s = %d, want %d", key, got, want)
	}
}

func assertBool(t *testing.T, root map[string]any, key string, want bool) {
	t.Helper()
	value, ok := root[key]
	if !ok {
		t.Fatalf("key %s not found", key)
	}
	got, ok := value.(bool)
	if !ok {
		t.Fatalf("key %s has type %T, want bool", key, value)
	}
	if got != want {
		t.Fatalf("%s = %t, want %t", key, got, want)
	}
}

func valueAsString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return ""
	}
}
