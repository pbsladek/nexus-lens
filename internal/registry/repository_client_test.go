package registry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pbsladek/nexus-lens/internal/upstream"
)

func TestRepositoryClientFetchManifestParsesDescriptors(t *testing.T) {
	var accept string
	var authUser string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repository/docker-hosted/v2/demo/app/manifests/latest" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		accept = r.Header.Get("Accept")
		authUser, _, _ = r.BasicAuth()
		w.Header().Set("Docker-Content-Digest", "sha256:manifest")
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schemaVersion": 2,
			"mediaType":     "application/vnd.oci.image.manifest.v1+json",
			"config": map[string]any{
				"mediaType": "application/vnd.oci.image.config.v1+json",
				"digest":    "sha256:config",
				"size":      321,
			},
			"layers": []map[string]any{{
				"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
				"digest":    "sha256:layer",
				"size":      1234,
			}},
		})
	}))
	defer server.Close()

	client, err := NewRepositoryClient(server.URL+"/repository/docker-hosted", "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := client.FetchManifest(context.Background(), "demo/app", "latest")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(accept, "application/vnd.oci.image.manifest.v1+json") || authUser != "admin" {
		t.Fatalf("accept=%q authUser=%q", accept, authUser)
	}
	if doc.Digest != "sha256:manifest" || doc.Config == nil || doc.Config.Digest != "sha256:config" ||
		len(doc.Layers) != 1 || doc.Layers[0].Size != 1234 {
		t.Fatalf("manifest doc = %+v", doc)
	}
}

func TestRegistryClientRequiresHost(t *testing.T) {
	if _, err := NewClient(Config{}); err == nil || !strings.Contains(err.Error(), "registry host is required") {
		t.Fatalf("NewClient err = %v", err)
	}
	client, err := NewClient(Config{RegistryHost: "registry.local", PlainHTTP: true, RequestsPerSecond: 2, MaxConcurrent: 3})
	if err != nil {
		t.Fatal(err)
	}
	if client == nil {
		t.Fatalf("client is nil")
	}
}

func TestRepositoryClientFetchManifestParsesIndexChildren(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", "sha256:index")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"mediaType": "application/vnd.oci.image.index.v1+json",
			"manifests": []map[string]any{{
				"mediaType": "application/vnd.oci.image.manifest.v1+json",
				"digest":    "sha256:amd64",
				"size":      555,
			}},
		})
	}))
	defer server.Close()

	client, err := NewRepositoryClient(server.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := client.FetchManifest(context.Background(), "demo/app", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Digest != "sha256:index" || len(doc.Children) != 1 || doc.Children[0].Digest != "sha256:amd64" {
		t.Fatalf("manifest doc = %+v", doc)
	}
}

func TestRepositoryClientFetchManifestErrors(t *testing.T) {
	t.Run("missing input", func(t *testing.T) {
		client, err := NewRepositoryClient("http://example.test", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.FetchManifest(context.Background(), "", "latest"); err == nil {
			t.Fatalf("expected missing input error")
		}
	})

	t.Run("http status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "7")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
		}))
		defer server.Close()
		client, err := NewRepositoryClient(server.URL, "", "")
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.FetchManifest(context.Background(), "demo/app", "latest")
		var httpErr *upstream.HTTPError
		if err == nil || !strings.Contains(err.Error(), "fetch manifest") || !strings.Contains(err.Error(), "429") {
			t.Fatalf("err = %v", err)
		}
		if !errors.As(err, &httpErr) || httpErr.RetryAfter == 0 {
			t.Fatalf("http err = %#v from %v", httpErr, err)
		}
	})

	t.Run("bad json", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("{"))
		}))
		defer server.Close()
		client, err := NewRepositoryClient(server.URL, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.FetchManifest(context.Background(), "demo/app", "latest"); err == nil ||
			!strings.Contains(err.Error(), "decode manifest") {
			t.Fatalf("err = %v", err)
		}
	})
}
