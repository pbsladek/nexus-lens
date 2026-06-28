package nexus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
