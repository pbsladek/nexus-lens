package nexus

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

func TestSearchDockerComponentsUsesNexusSearchParameters(t *testing.T) {
	var seenRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenRequests++
		if r.URL.Path != "/service/rest/v1/search" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		username, password, ok := r.BasicAuth()
		if !ok || username != "admin" || password != "secret" {
			t.Fatalf("missing basic auth")
		}
		query := r.URL.Query()
		if query.Get("repository") != "docker-hosted-a" ||
			query.Get("format") != "docker" ||
			query.Get("docker.imageName") != "nexus-lens/app" ||
			query.Get("docker.imageTag") != "pr-123" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(ComponentsPage{
			Items: []Component{{
				ID:         "component-1",
				Repository: "docker-hosted-a",
				Format:     "docker",
				Name:       "nexus-lens/app",
				Version:    "pr-123",
			}},
		})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "secret")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	components, err := client.SearchDockerComponents(context.Background(), "docker-hosted-a", "nexus-lens/app", "pr-123")
	if err != nil {
		t.Fatalf("search components: %v", err)
	}
	if seenRequests != 1 {
		t.Fatalf("expected one request, got %d", seenRequests)
	}
	if len(components) != 1 || components[0].ID != "component-1" {
		t.Fatalf("unexpected components: %#v", components)
	}
}

func TestSearchDockerComponentsFollowsContinuationToken(t *testing.T) {
	var seenTokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("continuationToken")
		seenTokens = append(seenTokens, token)
		page := ComponentsPage{
			Items: []Component{{ID: "component-" + token}},
		}
		if token == "" {
			page.ContinuationToken = "next-page"
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "", "")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	components, err := client.SearchDockerComponents(context.Background(), "repo", "image", "tag")
	if err != nil {
		t.Fatalf("search components: %v", err)
	}
	if len(seenTokens) != 2 || seenTokens[0] != "" || seenTokens[1] != "next-page" {
		t.Fatalf("unexpected continuation tokens: %#v", seenTokens)
	}
	if len(components) != 2 {
		t.Fatalf("expected two components, got %d", len(components))
	}
}

func TestDeleteComponentRequiresNoContent(t *testing.T) {
	var deletedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deletedPath = r.URL.Path
		if r.Method != http.MethodDelete {
			t.Fatalf("unexpected method %q", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "", "")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if err := client.DeleteComponent(context.Background(), "component/id"); err != nil {
		t.Fatalf("delete component: %v", err)
	}
	if deletedPath != "/service/rest/v1/components/component%2Fid" {
		t.Fatalf("unexpected delete path %q", deletedPath)
	}
}

func TestListAssetsUsesRepositoryContinuationAndAuth(t *testing.T) {
	var seenToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/service/rest/v1/assets" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "secret" {
			t.Fatalf("missing basic auth")
		}
		if got := r.URL.Query().Get("repository"); got != "npm-hosted" {
			t.Fatalf("repository = %q", got)
		}
		seenToken = r.URL.Query().Get("continuationToken")
		_ = json.NewEncoder(w).Encode(AssetsPage{
			ContinuationToken: "next",
			Items: []Asset{{
				ID:       "asset-1",
				Path:     "left-pad/-/left-pad-1.3.0.tgz",
				FileSize: 123,
			}},
		})
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.ListAssets(context.Background(), "npm-hosted", "token-1")
	if err != nil {
		t.Fatal(err)
	}
	if seenToken != "token-1" || page.ContinuationToken != "next" || len(page.Items) != 1 {
		t.Fatalf("seenToken=%q page=%+v", seenToken, page)
	}
}

func TestNexusClientHTTPAndDecodeErrors(t *testing.T) {
	t.Run("repository http error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "5")
			http.Error(w, "rate limited", http.StatusTooManyRequests)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, "", "")
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.ListRepositories(context.Background())
		httpErr := &upstream.HTTPError{}
		if !errorAsHTTP(err, &httpErr) || httpErr.StatusCode != http.StatusTooManyRequests || httpErr.RetryAfter == 0 {
			t.Fatalf("err = %#v from %v", httpErr, err)
		}
	})

	t.Run("assets bad json", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("{"))
		}))
		defer server.Close()

		client, err := NewClient(server.URL, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.ListAssets(context.Background(), "repo", ""); err == nil ||
			!strings.Contains(err.Error(), "decode nexus assets") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("delete status error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusForbidden)
		}))
		defer server.Close()

		client, err := NewClient(server.URL, "", "")
		if err != nil {
			t.Fatal(err)
		}
		httpErr := &upstream.HTTPError{}
		if err := client.DeleteComponent(context.Background(), "component-1"); !errorAsHTTP(err, &httpErr) || httpErr.StatusCode != http.StatusForbidden {
			t.Fatalf("err = %#v from %v", httpErr, err)
		}
	})
}

func errorAsHTTP(err error, target **upstream.HTTPError) bool {
	return errors.As(err, target)
}
