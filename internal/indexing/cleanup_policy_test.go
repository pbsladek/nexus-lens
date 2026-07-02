package indexing

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/pbsladek/nexus-lens/internal/db"
)

func TestParseCleanupPolicyAndCandidateMatching(t *testing.T) {
	policy, err := parseCleanupPolicy(`
name: ci-cleanup
candidate_rules:
  - name: old feature branches
    patterns: [feature_branch]
    tag_prefixes: ["feature-"]
    image_prefixes: ["demo/"]
    older_than_days: 30
    never_downloaded: true
    min_reclaimable_bytes: 100
`)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Name != "ci-cleanup" || len(policy.CandidateRules) != 1 {
		t.Fatalf("policy = %+v", policy)
	}

	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	row := cleanupPolicyTestRow(now.AddDate(0, 0, -45))
	row.LastDownloadedAt = nil
	rule, ok := matchCleanupCandidate(policy.CandidateRules, row, now)
	if !ok || rule.Name != "old feature branches" {
		t.Fatalf("match = %+v, %v", rule, ok)
	}
	if got := cleanupCandidateReason(rule); got != "pattern, older than 30d, never downloaded" {
		t.Fatalf("reason = %q", got)
	}

	row.LastDownloadedAt = now
	if _, ok := matchCleanupCandidate(policy.CandidateRules, row, now); ok {
		t.Fatalf("downloaded row should not match never_downloaded rule")
	}
}

func TestCleanupPolicyValidationAndHelpers(t *testing.T) {
	for _, raw := range []string{"", "name: empty\ncandidate_rules: []", "candidate_rules:\n- tags: [x]"} {
		if _, err := parseCleanupPolicy(raw); err == nil {
			t.Fatalf("expected validation error for %q", raw)
		}
	}
	if simulationLimit(0) != 1000 || simulationLimit(6000) != 1000 || simulationLimit(25) != 25 {
		t.Fatalf("simulationLimit returned unexpected values")
	}
	if !stringIn("a", []string{"b", "a"}) || !hasAnyPrefix("feature-x", []string{"bug", "feature-"}) {
		t.Fatalf("string matching helpers failed")
	}
	now := time.Now()
	if timeFromAny(now) == nil || timeFromAny(nil) != nil || timeFromAny("bad") != nil {
		t.Fatalf("timeFromAny returned unexpected values")
	}
	if stringFromMap(map[string]any{"key": "value"}, "key") != "value" ||
		int64FromMap(map[string]any{"int": 1, "int64": int64(2), "float": float64(3)}, "float") != 3 {
		t.Fatalf("map helpers returned unexpected values")
	}
	cleaned := mapWithoutKeys(map[string]any{"keep": 1, "drop": 2}, "drop")
	if len(cleaned) != 1 || cleaned["keep"] != 1 {
		t.Fatalf("mapWithoutKeys = %+v", cleaned)
	}
}

func TestCleanupSimulationTagMapping(t *testing.T) {
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	row := cleanupPolicyTestRow(now)
	row.LastDownloadedAt = now
	tag := cleanupSimulationTag(row)
	if tag.TagID != row.TagID || tag.ImageName != row.ImageName || tag.FirstSeenAt.IsZero() || tag.LastDownloadedAt == nil {
		t.Fatalf("tag = %+v", tag)
	}
}

func cleanupPolicyTestRow(firstSeen time.Time) db.ListCleanupSimulationTagsRow {
	return db.ListCleanupSimulationTagsRow{
		TagID:                     1,
		ImageID:                   2,
		RepositoryID:              3,
		NexusInstanceID:           4,
		RepositoryName:            "docker-hosted-a",
		ImageName:                 "demo/app",
		Tag:                       "feature-x",
		Digest:                    "sha256:tag",
		PatternName:               "feature_branch",
		DockerLogicalBytes:        1000,
		EstimatedReclaimableBytes: 500,
		FirstSeenAt:               pgtype.Timestamptz{Time: firstSeen, Valid: true},
		LastSeenAt:                pgtype.Timestamptz{Time: firstSeen.Add(time.Hour), Valid: true},
		LastDownloadedAt:          firstSeen,
	}
}
