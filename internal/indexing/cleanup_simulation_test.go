package indexing

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/pbsladek/nexus-lens/internal/db"
	"github.com/pbsladek/nexus-lens/internal/nexus"
)

func TestCleanupMaintenanceWindowOpen(t *testing.T) {
	tests := []struct {
		name       string
		now        time.Time
		start      string
		end        string
		wantOpen   bool
		wantReason string
	}{
		{
			name:     "unconfigured",
			now:      time.Date(2026, 6, 28, 1, 30, 0, 0, time.UTC),
			wantOpen: true,
		},
		{
			name:     "inside same day window",
			now:      time.Date(2026, 6, 28, 1, 30, 0, 0, time.UTC),
			start:    "01:00",
			end:      "03:00",
			wantOpen: true,
		},
		{
			name:       "outside same day window",
			now:        time.Date(2026, 6, 28, 4, 0, 0, 0, time.UTC),
			start:      "01:00",
			end:        "03:00",
			wantOpen:   false,
			wantReason: "cleanup execution is outside the configured UTC maintenance window",
		},
		{
			name:     "inside overnight window",
			now:      time.Date(2026, 6, 28, 23, 30, 0, 0, time.UTC),
			start:    "22:00",
			end:      "02:00",
			wantOpen: true,
		},
		{
			name:       "invalid start",
			now:        time.Date(2026, 6, 28, 1, 30, 0, 0, time.UTC),
			start:      "1am",
			end:        "03:00",
			wantOpen:   false,
			wantReason: "cleanup maintenance window start must use HH:MM UTC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			open, reason := cleanupMaintenanceWindowOpen(tt.now, tt.start, tt.end)
			if open != tt.wantOpen || reason != tt.wantReason {
				t.Fatalf("cleanupMaintenanceWindowOpen = (%v, %q), want (%v, %q)", open, reason, tt.wantOpen, tt.wantReason)
			}
		})
	}
}

func TestUniqueMatchingDockerComponentRequiresSingleExactMatch(t *testing.T) {
	candidate := CleanupSimulationTag{
		RepositoryName: "docker-hosted-a",
		ImageName:      "nexus-lens/app",
		Tag:            "pr-123",
	}

	component, ok := uniqueMatchingDockerComponent([]nexus.Component{{
		ID:         "component-1",
		Repository: "docker-hosted-a",
		Format:     "docker",
		Name:       "nexus-lens/app",
		Version:    "pr-123",
	}}, candidate)
	if !ok || component.ID != "component-1" {
		t.Fatalf("unique match = (%#v, %v)", component, ok)
	}

	_, ok = uniqueMatchingDockerComponent([]nexus.Component{
		{ID: "component-1", Repository: "docker-hosted-a", Format: "docker", Name: "nexus-lens/app", Version: "pr-123"},
		{ID: "component-2", Repository: "docker-hosted-a", Format: "docker", Name: "nexus-lens/app", Version: "pr-123"},
	}, candidate)
	if ok {
		t.Fatalf("expected duplicate component matches to be rejected")
	}

	_, ok = uniqueMatchingDockerComponent([]nexus.Component{{
		ID:         "component-1",
		Repository: "docker-hosted-a",
		Format:     "docker",
		Name:       "nexus-lens/app",
		Version:    "latest",
	}}, candidate)
	if ok {
		t.Fatalf("expected tag mismatch to be rejected")
	}
}

func TestExecuteCleanupDeletesSkipsAmbiguousComponent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/service/rest/v1/search" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("docker.imageTag"); got != "pr-123" {
			t.Fatalf("docker.imageTag = %q, want pr-123", got)
		}
		_ = json.NewEncoder(w).Encode(nexus.ComponentsPage{
			Items: []nexus.Component{
				{ID: "component-1", Repository: "docker-hosted-a", Format: "docker", Name: "demo/app", Version: "pr-123"},
				{ID: "component-2", Repository: "docker-hosted-a", Format: "docker", Name: "demo/app", Version: "pr-123"},
			},
		})
	}))
	defer server.Close()

	service := NewService(nil, slog.Default())
	status, stats, audit := service.executeCleanupDeletes(
		context.Background(),
		CleanupExecutionRequest{RequestedBy: "admin", NexusUsername: "admin", NexusPassword: "secret", DeleteRPS: 1000},
		db.GetNexusInstanceByIDRow{BaseUrl: server.URL},
		CleanupSimulationResult{Candidates: []CleanupSimulationTag{cleanupTestCandidate("pr-123")}},
		nil,
	)

	if status != "completed_with_warnings" || stats.SkippedCount != 1 || stats.DeletedCount != 0 || stats.FailedCount != 0 {
		t.Fatalf("status=%s stats=%+v", status, stats)
	}
	if !auditHasEvent(audit, "candidate_skipped_ambiguous_component") {
		t.Fatalf("audit missing ambiguous component event: %+v", audit)
	}
}

func TestExecuteCleanupDeletesStopsWhenContextCanceledBeforeNextDelete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(nexus.ComponentsPage{
			Items: []nexus.Component{
				{ID: "component-1", Repository: "docker-hosted-a", Format: "docker", Name: "demo/app", Version: "first"},
				{ID: "component-2", Repository: "docker-hosted-a", Format: "docker", Name: "demo/app", Version: "first"},
			},
		})
		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()
	}))
	defer server.Close()

	service := NewService(nil, slog.Default())
	status, stats, audit := service.executeCleanupDeletes(
		ctx,
		CleanupExecutionRequest{RequestedBy: "admin", NexusUsername: "admin", NexusPassword: "secret", DeleteRPS: 0.001},
		db.GetNexusInstanceByIDRow{BaseUrl: server.URL},
		CleanupSimulationResult{Candidates: []CleanupSimulationTag{
			cleanupTestCandidate("first"),
			cleanupTestCandidate("second"),
		}},
		nil,
	)

	if status != "failed" || stats.SkippedCount != 1 || stats.FailedCount != 1 {
		t.Fatalf("status=%s stats=%+v", status, stats)
	}
	if !auditHasEvent(audit, "cleanup_context_canceled") {
		t.Fatalf("audit missing cancellation event: %+v", audit)
	}
}

func cleanupTestCandidate(tag string) CleanupSimulationTag {
	return CleanupSimulationTag{
		TagID:           101,
		ImageID:         11,
		RepositoryID:    1,
		NexusInstanceID: 7,
		RepositoryName:  "docker-hosted-a",
		ImageName:       "demo/app",
		Tag:             tag,
		FirstSeenAt:     time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		LastSeenAt:      time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
}

func auditHasEvent(audit []map[string]any, event string) bool {
	return slices.ContainsFunc(audit, func(row map[string]any) bool {
		return row["event"] == event
	})
}
