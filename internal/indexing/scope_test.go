package indexing

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/pbsladek/nexus-lens/internal/nexus"
)

func TestNormalizeRunScopeDefaultsBoundedWindow(t *testing.T) {
	now := time.Date(2026, 6, 27, 15, 30, 0, 0, time.UTC)

	scope := normalizeRunScope(ScanTypeBoundedReindex, RunScope{
		Repositories: []string{" docker-hosted-b ", "docker-hosted-a", "docker-hosted-a", ""},
	}, now)

	if scope.WindowDays != 7 {
		t.Fatalf("WindowDays = %d, want 7", scope.WindowDays)
	}
	if scope.Since != "2026-06-20T00:00:00Z" {
		t.Fatalf("Since = %q", scope.Since)
	}
	wantRepos := []string{"docker-hosted-a", "docker-hosted-b"}
	if len(scope.Repositories) != len(wantRepos) {
		t.Fatalf("Repositories = %#v", scope.Repositories)
	}
	for i := range wantRepos {
		if scope.Repositories[i] != wantRepos[i] {
			t.Fatalf("Repositories = %#v, want %#v", scope.Repositories, wantRepos)
		}
	}
}

func TestDecodeTaskScopeAndRepositoryAllowed(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"scope": map[string]any{
			"repositories": []string{"docker-hosted-a", "docker-hosted-b"},
			"window_days":  14,
			"since":        "2026-06-20T00:00:00Z",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	scope := decodeTaskScope(payload)
	if scope.WindowDays != 14 || scope.Since != "2026-06-20T00:00:00Z" || len(scope.Repositories) != 2 {
		t.Fatalf("scope = %+v", scope)
	}
	if !repositoryAllowed("docker-hosted-a", scope) || repositoryAllowed("missing", scope) {
		t.Fatalf("repositoryAllowed returned unexpected values")
	}
	if !repositoryAllowed("anything", taskScope{}) {
		t.Fatalf("empty repository scope should allow all repositories")
	}
	if decoded := decodeTaskScope([]byte("{")); len(decoded.Repositories) != 0 || decoded.Since != "" {
		t.Fatalf("invalid payload decoded as %+v", decoded)
	}
}

func TestScopeSinceParsesValidRFC3339Only(t *testing.T) {
	since, ok := scopeSince(taskScope{Since: "2026-06-20T00:00:00Z"})
	if !ok || since.Format(time.RFC3339) != "2026-06-20T00:00:00Z" {
		t.Fatalf("scopeSince = %s, %v", since, ok)
	}
	if _, ok := scopeSince(taskScope{}); ok {
		t.Fatalf("empty scope should not have since")
	}
	if _, ok := scopeSince(taskScope{Since: "bad"}); ok {
		t.Fatalf("invalid since should not parse")
	}
}

func TestAssetChangedSince(t *testing.T) {
	since := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	inWindow, hasTimestamp := assetChangedSince(testAsset("2026-06-21T12:00:00Z"), since)
	if !hasTimestamp || !inWindow {
		t.Fatalf("assetChangedSince in-window = (%v, %v), want (true, true)", inWindow, hasTimestamp)
	}

	inWindow, hasTimestamp = assetChangedSince(testAsset("2026-06-19T23:59:59Z"), since)
	if !hasTimestamp || inWindow {
		t.Fatalf("assetChangedSince outside-window = (%v, %v), want (false, true)", inWindow, hasTimestamp)
	}
}

func testAsset(lastModified string) nexus.Asset {
	return nexus.Asset{LastModified: lastModified}
}
