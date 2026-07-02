package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opencontainers/go-digest"
	regdescriptor "github.com/regclient/regclient/types/descriptor"
	"github.com/regclient/regclient/types/platform"
)

func TestDescriptorSummaryIncludesDigestSizeAndPlatform(t *testing.T) {
	dgst := digest.FromString("manifest")
	summary := descriptorSummary(regdescriptor.Descriptor{
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Digest:    dgst,
		Size:      123,
		Platform:  &platform.Platform{OS: "linux", Architecture: "amd64"},
	})
	if summary.MediaType != "application/vnd.oci.image.manifest.v1+json" ||
		summary.Digest != dgst.String() ||
		summary.Size != 123 ||
		summary.Platform != "linux/amd64" {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestDescriptorsPreserveOrder(t *testing.T) {
	first := digest.FromString("first")
	second := digest.FromString("second")
	out := descriptors([]regdescriptor.Descriptor{
		{Digest: first, Size: 1},
		{Digest: second, Size: 2},
	})
	if len(out) != 2 || out[0].Digest != first.String() || out[1].Digest != second.String() {
		t.Fatalf("descriptors = %+v", out)
	}
}

func TestClientListTagsAndManifestSummaryAgainstHTTPRegistry(t *testing.T) {
	manifestDigest := digest.FromString("manifest")
	configDigest := digest.FromString("config")
	layerDigest := digest.FromString("layer")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/":
			w.WriteHeader(http.StatusOK)
		case "/v2/demo/app/tags/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name": "demo/app",
				"tags": []string{"z", "latest", "a"},
			})
		case "/v2/demo/app/manifests/latest":
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schemaVersion": 2,
				"mediaType":     "application/vnd.oci.image.manifest.v1+json",
				"config": map[string]any{
					"mediaType": "application/vnd.oci.image.config.v1+json",
					"digest":    configDigest.String(),
					"size":      321,
				},
				"layers": []map[string]any{{
					"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
					"digest":    layerDigest.String(),
					"size":      1234,
				}},
			})
		default:
			t.Fatalf("unexpected registry path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{RegistryHost: server.Listener.Addr().String(), PlainHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	image := server.Listener.Addr().String() + "/demo/app"
	tags, err := client.ListTags(context.Background(), image)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 3 || tags[0] != "a" || tags[2] != "z" {
		t.Fatalf("tags = %+v", tags)
	}

	summary, err := client.ManifestSummary(context.Background(), image, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Digest == "" || summary.Digest == manifestDigest.String() || summary.Config == nil || summary.Config.Digest != configDigest.String() ||
		len(summary.Layers) != 1 || summary.Layers[0].Digest != layerDigest.String() || summary.Layers[0].Size != 1234 {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestClientReturnsParseErrorsForInvalidRefs(t *testing.T) {
	client, err := NewClient(Config{RegistryHost: "registry.local", PlainHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTags(context.Background(), "::bad-ref"); err == nil {
		t.Fatalf("expected ListTags parse error")
	}
	if _, err := client.ManifestSummary(context.Background(), "::bad-ref", "latest"); err == nil {
		t.Fatalf("expected ManifestSummary parse error")
	}
}
