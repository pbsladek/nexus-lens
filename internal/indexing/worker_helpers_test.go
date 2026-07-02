package indexing

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/pbsladek/nexus-lens/internal/db"
	"github.com/pbsladek/nexus-lens/internal/nexus"
	"github.com/pbsladek/nexus-lens/internal/registry"
)

func TestNewWorkerOptionsAndEnvDefaults(t *testing.T) {
	t.Setenv("NEXUS_LENS_WORKER_ID", "env-worker")
	t.Setenv("NEXUS_LENS_NEXUS_USERNAME", "nexus-user")
	t.Setenv("NEXUS_LENS_NEXUS_PASSWORD", "nexus-pass")
	t.Setenv("NEXUS_LENS_ENABLE_DESTRUCTIVE_CLEANUP", "true")
	t.Setenv("NEXUS_LENS_CLEANUP_DELETE_RPS", "3.5")
	t.Setenv("NEXUS_LENS_RETENTION_INTERVAL_SECONDS", "10")

	worker := NewWorker(nil, slog.Default(),
		WithWorkerID("override-worker"),
		WithWorkerPodName("pod-a"),
		WithBatchSize(25),
		WithLeaseDuration(2*time.Minute),
		WithPollInterval(500*time.Millisecond),
	)

	if worker.workerID != "override-worker" || worker.workerPod != "pod-a" || worker.batchSize != 25 {
		t.Fatalf("worker options not applied: %+v", worker)
	}
	if worker.nexusUser != "nexus-user" || worker.nexusPass != "nexus-pass" || !worker.destructiveCleanupEnabled {
		t.Fatalf("worker env not applied: %+v", worker)
	}
	if worker.cleanupDeleteRPS != 3.5 || worker.retentionSettings.IntervalSeconds != 10 {
		t.Fatalf("worker numeric env not applied: %+v", worker)
	}
	if worker.lease != 2*time.Minute || worker.pollInterval != 500*time.Millisecond {
		t.Fatalf("worker durations not applied: %+v", worker)
	}
}

func TestWorkerExecuteTaskValidationBranches(t *testing.T) {
	worker := NewWorker(nil, slog.Default())
	if err := worker.executeTask(context.Background(), db.ScanTask{TaskType: TaskTypeNoop}); err != nil {
		t.Fatalf("noop task returned error: %+v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := worker.executeTask(canceled, db.ScanTask{TaskType: TaskTypeNoop}); err == nil || err.Code != "worker_context_canceled" {
		t.Fatalf("canceled task error = %+v", err)
	}

	if err := worker.executeTask(context.Background(), db.ScanTask{TaskType: "missing"}); err == nil || err.Code != "unsupported_task_type" || err.Retryable {
		t.Fatalf("unsupported task error = %+v", err)
	}

	for _, tt := range []struct {
		name     string
		taskType string
		payload  []byte
		wantCode string
	}{
		{name: "trend invalid json", taskType: TaskTypeTrendSnapshot, payload: []byte("{"), wantCode: "invalid_task_payload"},
		{name: "trend invalid date", taskType: TaskTypeTrendSnapshot, payload: []byte(`{"snapshot_date":"07-01-2026"}`), wantCode: "invalid_task_payload"},
		{name: "package missing repo", taskType: TaskTypePackageAssetScan, payload: []byte(`{"repository_name":"pypi","format":"pypi"}`), wantCode: "invalid_task_payload"},
		{name: "docker missing repo", taskType: TaskTypeDockerTagScan, payload: []byte(`{"repository_name":"docker-hosted"}`), wantCode: "invalid_task_payload"},
		{name: "manifest missing url", taskType: TaskTypeManifestFetchBatch, payload: []byte(`{"repository_id":1}`), wantCode: "invalid_task_payload"},
		{name: "tag reclass invalid json", taskType: TaskTypeTagReclassification, payload: []byte("{"), wantCode: "invalid_task_payload"},
		{name: "verification invalid json", taskType: TaskTypeVerificationChecks, payload: []byte("{"), wantCode: "invalid_task_payload"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := worker.executeTask(context.Background(), db.ScanTask{TaskType: tt.taskType, Payload: tt.payload})
			if err == nil || err.Code != tt.wantCode || err.Retryable {
				t.Fatalf("task error = %+v", err)
			}
		})
	}
}

func TestDockerAssetHelpers(t *testing.T) {
	image, tag, ok := dockerTagFromAssetPath("/v2/team/app/manifests/feature-x")
	if !ok || image != "team/app" || tag != "feature-x" {
		t.Fatalf("docker tag path parsed as image=%q tag=%q ok=%v", image, tag, ok)
	}
	for _, path := range []string{
		"v2/team/app/manifests/latest",
		"/v2/-/manifests/latest",
		"/v2/team/app/manifests/sha256:abc",
		"/v2/team/app/blobs/sha256:abc",
	} {
		if _, _, ok := dockerTagFromAssetPath(path); ok {
			t.Fatalf("path %q should not parse as a tag manifest", path)
		}
	}

	if dockerDigest(nexus.Asset{}) != "" ||
		dockerDigest(nexus.Asset{Checksum: nexus.AssetChecksum{SHA256: "abc"}}) != "sha256:abc" ||
		dockerDigest(nexus.Asset{Checksum: nexus.AssetChecksum{SHA256: "sha256:def"}}) != "sha256:def" {
		t.Fatalf("dockerDigest returned unexpected values")
	}
}

func TestWorkerAssetChangedSincePrefersNexusTimestamps(t *testing.T) {
	since := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	inWindow, hasTimestamp := assetChangedSince(nexus.Asset{LastModified: since.Add(time.Minute).Format(time.RFC3339Nano)}, since)
	if !inWindow || !hasTimestamp {
		t.Fatalf("recent modified asset should be in window")
	}
	inWindow, hasTimestamp = assetChangedSince(nexus.Asset{BlobCreated: since.Add(-time.Minute).Format(time.RFC3339Nano)}, since)
	if inWindow || !hasTimestamp {
		t.Fatalf("old created asset should be outside window")
	}
	inWindow, hasTimestamp = assetChangedSince(nexus.Asset{LastModified: "bad"}, since)
	if inWindow || hasTimestamp {
		t.Fatalf("bad timestamp should be treated as missing")
	}
}

func TestManifestSizingAndChildDigestHelpers(t *testing.T) {
	doc := registry.ManifestDocument{
		Config: &registry.Descriptor{Digest: "sha256:config", Size: 10},
		Layers: []registry.Descriptor{
			{Digest: "sha256:layer-a", Size: 20},
			{Digest: "sha256:layer-b", Size: 30},
		},
	}
	if got := manifestLogicalSize(doc); got != 60 {
		t.Fatalf("manifest logical size = %d", got)
	}

	index := registry.ManifestDocument{
		Size: 100,
		Children: []registry.Descriptor{
			{Digest: "sha256:child-a", Size: 11},
			{Digest: "", Size: 12},
			{Digest: "sha256:child-b", Size: 13},
		},
	}
	if got := manifestLogicalSize(index); got != 36 {
		t.Fatalf("index logical size = %d", got)
	}
	if got := manifestLogicalSize(registry.ManifestDocument{Size: 99}); got != 99 {
		t.Fatalf("fallback logical size = %d", got)
	}
	digests := childManifestDigests(index.Children)
	if len(digests) != 2 || digests[0] != "sha256:child-a" || digests[1] != "sha256:child-b" {
		t.Fatalf("child digests = %+v", digests)
	}
}

func TestUnsupportedReasonAndRetryDelay(t *testing.T) {
	if unsupportedReason(nexus.Repository{Format: "docker", Type: "hosted"}) != "" {
		t.Fatalf("hosted docker should be supported")
	}
	if unsupportedReason(nexus.Repository{Format: "npm", Type: "hosted"}) != "" {
		t.Fatalf("hosted npm should be supported")
	}
	if got := unsupportedReason(nexus.Repository{Format: "docker", Type: "proxy"}); got != "repository is not hosted" {
		t.Fatalf("unsupported type reason = %q", got)
	}
	if got := unsupportedReason(nexus.Repository{Format: "maven2", Type: "hosted"}); got != "repository format maven2 is not supported yet" {
		t.Fatalf("unsupported format reason = %q", got)
	}

	if retryDelay(0) != time.Second || retryDelay(3) != 9*time.Second || retryDelay(99) != time.Minute {
		t.Fatalf("retryDelay returned unexpected values")
	}
}
