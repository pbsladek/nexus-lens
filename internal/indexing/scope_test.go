package indexing

import (
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
