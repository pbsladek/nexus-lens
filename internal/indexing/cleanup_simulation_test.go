package indexing

import (
	"testing"
	"time"

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
