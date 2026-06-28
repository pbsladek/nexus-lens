package registry

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"
)

func TestLiveNexusRegistry(t *testing.T) {
	if os.Getenv("NEXUS_LENS_LIVE_REGISTRY") != "1" {
		t.Skip("set NEXUS_LENS_LIVE_REGISTRY=1 to run against a live Nexus registry")
	}

	password := os.Getenv("NEXUS_PASSWORD")
	if password == "" {
		t.Fatal("NEXUS_PASSWORD is required for live registry test")
	}

	registryHost := envOrDefault("NEXUS_REGISTRY_A", "localhost:5001")
	image := registryHost + "/nexus-lens/app"
	client, err := NewClient(Config{
		RegistryHost:      registryHost,
		Username:          envOrDefault("NEXUS_USER", "admin"),
		Password:          password,
		PlainHTTP:         true,
		RequestsPerSecond: 5,
		MaxConcurrent:     2,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tags, err := client.ListTags(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"v1.2.3", "latest", "stable", "pr-123"} {
		if !slices.Contains(tags, tag) {
			t.Fatalf("expected tag %q in %v", tag, tags)
		}
	}
	if slices.Contains(tags, "pr-delete-me") {
		t.Fatalf("expected pr-delete-me to be deleted, got tags %v", tags)
	}

	manifest, err := client.ManifestSummary(ctx, image, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Digest == "" || manifest.MediaType == "" || manifest.Size <= 0 {
		t.Fatalf("manifest summary missing descriptor fields: %+v", manifest)
	}
	if !manifest.IsList {
		t.Fatalf("expected Docker-built fixture to resolve to an OCI/Docker index, got %+v", manifest)
	}
	if len(manifest.Children) == 0 {
		t.Fatalf("expected child manifests in index: %+v", manifest)
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
