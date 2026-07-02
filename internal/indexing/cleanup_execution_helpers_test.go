package indexing

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/pbsladek/nexus-lens/internal/db"
	"github.com/pbsladek/nexus-lens/internal/nexus"
)

func TestCleanupExecutionCandidateTransforms(t *testing.T) {
	tags := []CleanupSimulationTag{{
		TagID:                     1,
		ImageID:                   2,
		RepositoryID:              3,
		NexusInstanceID:           4,
		RepositoryName:            "docker-hosted",
		ImageName:                 "demo/app",
		Tag:                       "feature-x",
		Digest:                    "sha256:abc",
		PatternName:               "feature_branch",
		RuleName:                  "old feature",
		Reason:                    "older than 30d",
		DockerLogicalBytes:        20,
		EstimatedReclaimableBytes: 10,
	}}

	candidates := cleanupApprovedCandidates(tags)
	if len(candidates) != 1 || candidates[0].Digest != "sha256:abc" || candidates[0].RuleName != "old feature" {
		t.Fatalf("candidates = %+v", candidates)
	}

	raw, err := json.Marshal(candidates)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := cleanupCandidatesFromExecution(db.CleanupExecution{ApprovedCandidates: raw})
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || decoded[0].ImageName != "demo/app" {
		t.Fatalf("decoded = %+v", decoded)
	}
	if _, err := cleanupCandidatesFromExecution(db.CleanupExecution{ApprovedCandidates: []byte("{")}); err == nil {
		t.Fatalf("expected invalid approved candidates to fail")
	}
}

func TestCleanupAuditAndEventTransforms(t *testing.T) {
	events := appendCleanupAudit(mustCleanupJSON(t, []map[string]any{{"event": "existing"}}),
		map[string]any{"event": "new", "actor": "admin"},
	)
	if len(events) != 2 || events[0]["event"] != "existing" || events[1]["actor"] != "admin" {
		t.Fatalf("events = %+v", events)
	}

	input := cleanupEventFromAudit(map[string]any{
		"event":        "candidate_deleted",
		"actor":        "worker",
		"message":      "deleted",
		"repository":   "docker-hosted",
		"image":        "demo/app",
		"tag":          "feature-x",
		"tag_id":       float64(123),
		"rule_name":    "old feature",
		"component_id": "component-1",
		"extra":        "kept",
	})
	if input.EventType != "candidate_deleted" || input.TagID != 123 || input.Details["extra"] != "kept" {
		t.Fatalf("event input = %+v", input)
	}

	candidate := cleanupExecutionCandidate{RepositoryName: "docker-hosted", ImageName: "demo/app", Tag: "feature-x", TagID: 1, RuleName: "old"}
	audit := cleanupExecutionCandidateAuditEvent("worker", "candidate_deleted", "deleted", candidate, map[string]any{"component_id": "component-1"})
	if audit["approved_digest"] != "" || audit["repository"] != "docker-hosted" || audit["component_id"] != "component-1" {
		t.Fatalf("audit = %+v", audit)
	}
	event := cleanupExecutionCandidateEvent("worker", "candidate_deleted", "deleted", "deleted", candidate, "component-1", nil)
	if event.ComponentID != "component-1" || event.TagID != 1 || event.Status != "deleted" {
		t.Fatalf("event = %+v", event)
	}
}

func TestCleanupStatusRepositoriesAndDigestMatching(t *testing.T) {
	if cleanupStatusFromStats(cleanupExecutionStats{FailedCount: 1}) != "failed" ||
		cleanupStatusFromStats(cleanupExecutionStats{SkippedCount: 1}) != "completed_with_warnings" ||
		cleanupStatusFromStats(cleanupExecutionStats{}) != "succeeded_noop" ||
		cleanupStatusFromStats(cleanupExecutionStats{DeletedCount: 1}) != "succeeded" {
		t.Fatalf("cleanupStatusFromStats returned unexpected status")
	}

	repositories := cleanupExecutionCandidateRepositories([]cleanupExecutionCandidate{
		{RepositoryName: "b"},
		{RepositoryName: "a"},
		{RepositoryName: "b"},
		{},
	})
	if len(repositories) != 2 || repositories[0] != "a" || repositories[1] != "b" {
		t.Fatalf("repositories = %+v", repositories)
	}

	candidate := cleanupExecutionCandidate{RepositoryName: "docker-hosted", ImageName: "demo/app", Tag: "latest"}
	component, ok := uniqueMatchingApprovedDockerComponent([]nexus.Component{{
		ID:         "component-1",
		Repository: "docker-hosted",
		Format:     "docker",
		Name:       "demo/app",
		Version:    "latest",
	}}, candidate)
	if !ok || component.ID != "component-1" {
		t.Fatalf("component = %+v ok=%v", component, ok)
	}
	if _, ok := uniqueMatchingApprovedDockerComponent([]nexus.Component{{ID: "", Repository: "docker-hosted", Format: "docker", Name: "demo/app", Version: "latest"}}, candidate); ok {
		t.Fatalf("empty component id should not match")
	}

	component = nexus.Component{Assets: []nexus.Asset{{Checksum: nexus.AssetChecksum{SHA256: "abc"}}}}
	if !componentMatchesApprovedDigest(component, "sha256:abc") ||
		!componentMatchesApprovedDigest(component, "") ||
		componentMatchesApprovedDigest(component, "sha256:def") {
		t.Fatalf("componentMatchesApprovedDigest returned unexpected values")
	}
}

func TestCleanupEventCreateParamsKeepNullableFields(t *testing.T) {
	event := cleanupExecutionEventInput{
		EventType:      "candidate_deleted",
		Actor:          "worker",
		RepositoryName: "docker-hosted",
		ImageName:      "demo/app",
		Tag:            "feature-x",
		TagID:          1,
		RuleName:       "old",
		ComponentID:    "component-1",
		Status:         "deleted",
		Message:        "deleted",
		ErrorClass:     "nexus",
		ErrorCode:      "boom",
		ErrorMessage:   "failed",
		Details:        map[string]any{"ok": true},
	}
	if !text(event.Actor).Valid || !optionalInt8(event.TagID).Valid || !json.Valid(jsonBytes(event.Details)) {
		t.Fatalf("event helper values invalid: %+v", event)
	}
	if optionalInt8(0) != (pgtype.Int8{}) {
		t.Fatalf("zero optional tag id should be invalid")
	}
}

func mustCleanupJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
