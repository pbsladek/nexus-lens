package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/pbsladek/nexus-lens/internal/api"
	"github.com/pbsladek/nexus-lens/internal/auth"
	"github.com/pbsladek/nexus-lens/internal/db"
	"github.com/pbsladek/nexus-lens/internal/indexing"
)

func TestAPIEnvHelpers(t *testing.T) {
	t.Setenv("BOOL_TRUE", "true")
	t.Setenv("BOOL_BAD", "not-bool")
	t.Setenv("FLOAT_VALUE", "2.5")
	t.Setenv("FLOAT_BAD", "bad")
	t.Setenv("INT_VALUE", "42")
	t.Setenv("INT_BAD", "bad")

	if !envBool("BOOL_TRUE", false) || !envBool("BOOL_BAD", true) || envBool("MISSING_BOOL", false) {
		t.Fatalf("envBool returned unexpected values")
	}
	if envFloat("FLOAT_VALUE", 1) != 2.5 || envFloat("FLOAT_BAD", 1) != 1 || envFloat("MISSING_FLOAT", 3) != 3 {
		t.Fatalf("envFloat returned unexpected values")
	}
	if envInt("INT_VALUE", 1) != 42 || envInt("INT_BAD", 1) != 1 || envInt("MISSING_INT", 3) != 3 {
		t.Fatalf("envInt returned unexpected values")
	}
	if envOrDefault("MISSING_DEFAULT", "fallback") != "fallback" {
		t.Fatalf("envOrDefault did not return fallback")
	}
}

func TestAPIEnvOrFilePrefersEnvAndTrimsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SECRET_FILE", path)

	if got := envOrFile("SECRET_ENV", "SECRET_FILE"); got != "from-file" {
		t.Fatalf("file secret = %q", got)
	}
	t.Setenv("SECRET_ENV", "from-env")
	if got := envOrFile("SECRET_ENV", "SECRET_FILE"); got != "from-env" {
		t.Fatalf("env secret = %q", got)
	}
}

func TestAPIUserAdapterPreservesCapabilities(t *testing.T) {
	user := apiUser(auth.User{
		Username:     "admin",
		Role:         auth.RoleAdmin,
		Capabilities: []string{"read", "manage_index"},
	})
	if user.Username != "admin" || user.Role != auth.RoleAdmin || len(user.Capabilities) != 2 || user.Capabilities[1] != "manage_index" {
		t.Fatalf("api user = %+v", user)
	}
}

func TestAPIIndexMappingPreservesNullableFields(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	run := apiIndexRun(db.ScanRun{
		ID:              1,
		NexusInstanceID: 2,
		ScanType:        "full_reconcile",
		Status:          "succeeded",
		Phase:           pgText("done"),
		StartedAt:       pgTime(now),
		FinishedAt:      pgTime(now.Add(time.Hour)),
		CompletedTasks:  3,
		FailedTasks:     4,
	})
	if run.ID != 1 || run.Phase != "done" || run.FinishedAt == nil || !run.FinishedAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("run = %+v", run)
	}

	task := apiIndexTask(db.ScanTask{
		ID:              10,
		ScanRunID:       1,
		NexusInstanceID: 2,
		TaskType:        "docker_tag_scan",
		TaskKey:         "task",
		RepositoryName:  pgText("docker-hosted"),
		Subject:         pgText("library/app"),
		CostClass:       "asset_list",
		Status:          "failed",
		Attempt:         2,
		MaxAttempts:     3,
		LockedBy:        pgText("worker"),
		ErrorCode:       pgText("boom"),
		ErrorClass:      pgText("nexus"),
		ErrorMessage:    pgText("failed"),
		AvailableAt:     pgTime(now),
		StartedAt:       pgTime(now.Add(time.Minute)),
		FinishedAt:      pgTime(now.Add(2 * time.Minute)),
		ErrorDetails:    mustJSON(t, map[string]any{"retryable": true}),
	})
	if task.RepositoryName != "docker-hosted" || task.StartedAt == nil || task.ErrorDetails == nil {
		t.Fatalf("task = %+v", task)
	}
}

func TestAPIOperationMappingPreservesScopeAndControls(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	schedule := apiIndexingSchedule(db.ListIndexingSchedulesRow{
		ID:                   1,
		NexusInstanceID:      2,
		NexusName:            "nexus-a",
		NexusBaseUrl:         "https://nexus.example",
		ScanType:             "bounded_reindex",
		Name:                 "weekly",
		Enabled:              true,
		ScheduleExpr:         "@weekly",
		IntervalSeconds:      604800,
		Timezone:             "UTC",
		Scope:                mustJSON(t, map[string]any{"repositories": []string{"docker-hosted"}}),
		LockConflictBehavior: "skip",
		NextRunAt:            pgTime(now),
		LastRunAt:            pgTime(now.Add(-time.Hour)),
		CreatedAt:            pgTime(now.Add(-24 * time.Hour)),
		UpdatedAt:            pgTime(now),
	})
	if schedule.Scope.Repositories[0] != "docker-hosted" || schedule.NextRunAt == nil || schedule.LastRunAt == nil {
		t.Fatalf("schedule = %+v", schedule)
	}

	control := apiIndexingControl(db.NexusIndexingControl{
		NexusInstanceID:     2,
		Paused:              true,
		PausedReason:        pgText("maintenance"),
		PausedBy:            pgText("admin"),
		PausedAt:            pgTime(now),
		CircuitFailureCount: 3,
		CircuitOpenUntil:    pgTime(now.Add(time.Minute)),
		LastFailureCode:     pgText("rate_limited"),
		LastFailureClass:    pgText("nexus"),
		AssetListRps:        pgNumeric(t, "2.5"),
		DefaultRps:          pgNumeric(t, "1.25"),
	})
	if !control.Paused || control.PausedAt == nil || control.AssetListRPS == nil || *control.AssetListRPS != 2.5 {
		t.Fatalf("control = %+v", control)
	}
}

func TestAPIAnalyticsAndTrendMapping(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	overview := apiAnalyticsOverview(db.GetAnalyticsOverviewRow{
		RepositoryCount:          1,
		ImageCount:               2,
		ActiveTagCount:           3,
		TagsMissingDigestCount:   4,
		TagsMissingManifestCount: 5,
		NexusAssetBytes:          6,
		DockerLogicalBytes:       7,
		DockerUniqueLayerBytes:   8,
		LastSuccessfulRunAt:      now,
	})
	if overview.LastSuccessfulRunAt == nil || overview.RepositoryCount != 1 {
		t.Fatalf("overview = %+v", overview)
	}

	tag := apiTagAnalytics(db.ListTagAnalyticsRow{
		TagID:                     1,
		ImageID:                   2,
		RepositoryID:              3,
		NexusInstanceID:           4,
		NexusName:                 "nexus-a",
		RepositoryName:            "docker-hosted",
		ImageName:                 "demo/app",
		Tag:                       "feature-x",
		Digest:                    "sha256:abc",
		MediaType:                 "application/vnd.oci.image.manifest.v1+json",
		NexusTagSizeBytes:         10,
		DockerLogicalBytes:        20,
		DockerLayerBytes:          30,
		EstimatedReclaimableBytes: 40,
		SharedLayerBytes:          50,
		FirstSeenAt:               pgTime(now),
		LastSeenAt:                pgTime(now.Add(time.Hour)),
		LastDownloadedAt:          pgtype.Timestamptz{Time: now.Add(2 * time.Hour), Valid: true},
	})
	if tag.FirstSeenAt.IsZero() || tag.LastDownloadedAt == nil || tag.EstimatedReclaimableBytes != 40 {
		t.Fatalf("tag = %+v", tag)
	}

	point := apiTrendPoint(db.ListTrendSeriesRow{
		SnapshotDate: pgtype.Date{Time: now, Valid: true},
		CapturedAt:   pgTime(now.Add(time.Hour)),
		MetricValue:  pgNumeric(t, "42.5"),
		Missing:      false,
	})
	if point.SnapshotDate != "2026-07-01" || point.MetricValue == nil || *point.MetricValue != 42.5 {
		t.Fatalf("point = %+v", point)
	}

	mover := apiTrendTopMover(db.ListTrendTopMoversRow{
		RepositoryID:   pgtype.Int8{Int64: 3, Valid: true},
		RepositoryName: pgText("docker-hosted"),
		ImageID:        pgtype.Int8{Int64: 4, Valid: true},
		ImageName:      pgText("demo/app"),
		StartValue:     pgNumeric(t, "10"),
		EndValue:       pgNumeric(t, "25"),
		DeltaValue:     pgNumeric(t, "15"),
		DeltaPercent:   pgNumeric(t, "150"),
	})
	if mover.ImageID == nil || mover.DeltaPercent == nil || *mover.DeltaPercent != 150 {
		t.Fatalf("mover = %+v", mover)
	}
}

func TestAPIAnalyticsRowMappings(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	repo := apiRepositoryAnalytics(db.ListRepositoryAnalyticsRow{
		RepositoryID:           1,
		NexusInstanceID:        2,
		NexusName:              "nexus-a",
		RepositoryName:         "docker-hosted",
		ImageCount:             3,
		ActiveTagCount:         4,
		NexusAssetBytes:        5,
		DockerLogicalBytes:     6,
		DockerUniqueLayerBytes: 7,
		LastSeenAt:             pgTime(now),
	})
	if repo.RepositoryName != "docker-hosted" || repo.LastSeenAt == nil || repo.DockerUniqueLayerBytes != 7 {
		t.Fatalf("repo = %+v", repo)
	}

	image := apiImageAnalytics(db.ListImageAnalyticsRow{
		ImageID:                10,
		RepositoryID:           1,
		NexusInstanceID:        2,
		NexusName:              "nexus-a",
		RepositoryName:         "docker-hosted",
		ImageName:              "demo/app",
		ActiveTagCount:         4,
		DockerLogicalBytes:     6,
		DockerUniqueLayerBytes: 7,
		OldestTagFirstSeenAt:   now.Add(-time.Hour),
		NewestTagLastSeenAt:    now,
	})
	if image.ImageName != "demo/app" || image.OldestTagFirstSeenAt == nil || image.NewestTagLastSeenAt == nil {
		t.Fatalf("image = %+v", image)
	}

	pkg := apiPackageFormatRepository(db.ListPackageFormatRepositoriesRow{
		RepositoryID:    1,
		NexusInstanceID: 2,
		NexusName:       "nexus-a",
		RepositoryName:  "pypi-hosted",
		Format:          "pypi",
		Type:            "hosted",
		SupportStatus:   "indexed",
		SupportReason:   "supported",
		AssetCount:      11,
		AssetBytes:      12,
		LastSeenAt:      pgTime(now),
		LastAssetSeenAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true},
	})
	if pkg.RepositoryName != "pypi-hosted" || pkg.LastSeenAt == nil || pkg.LastAssetSeenAt == nil {
		t.Fatalf("package repo = %+v", pkg)
	}
}

func TestAPITagPatternAndVerificationMappings(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	analytics := apiTagPatternAnalytics(db.ListTagPatternAnalyticsRow{
		PatternName:          "feature_branch",
		ActiveTagCount:       2,
		DockerLogicalBytes:   100,
		OldestTagFirstSeenAt: now,
		NeverDownloadedCount: 1,
	})
	if analytics.PatternName != "feature_branch" || analytics.OldestTagFirstSeenAt == nil {
		t.Fatalf("analytics = %+v", analytics)
	}
	growth := apiTagPatternGrowth(db.ListTagPatternGrowthRow{
		RepositoryName:       "docker-hosted",
		ImageNamespace:       "demo",
		PatternName:          "feature_branch",
		ActiveTagCount:       3,
		DockerLogicalBytes:   200,
		GrowthTagCount:       1,
		GrowthLogicalBytes:   50,
		NeverDownloadedCount: 1,
		OldestTagFirstSeenAt: now,
	})
	if growth.ImageNamespace != "demo" || growth.OldestTagFirstSeenAt == nil {
		t.Fatalf("growth = %+v", growth)
	}
	example := apiTagPatternExample(db.ListTagPatternExamplesRow{
		PatternName:    "semver",
		RepositoryName: "docker-hosted",
		ImageName:      "demo/app",
		Tag:            "v1.2.3",
		FirstSeenAt:    pgTime(now),
		LastSeenAt:     pgTime(now.Add(time.Hour)),
	})
	if example.Tag != "v1.2.3" || example.FirstSeenAt.IsZero() || example.LastSeenAt.IsZero() {
		t.Fatalf("example = %+v", example)
	}
	suggestion := apiTagPatternSuggestion(db.ListTagPatternSuggestionsRow{
		TagShape:       "A-0",
		TagCount:       4,
		ExampleTags:    []string{"pr-1"},
		SuggestedRegex: []byte("^[A-Za-z]+-[0-9]+$"),
	})
	if suggestion.SuggestedRegex == "" || suggestion.ExampleTags[0] != "pr-1" {
		t.Fatalf("suggestion = %+v", suggestion)
	}

	finding := apiVerificationFinding(db.VerificationFinding{
		ID:              99,
		NexusInstanceID: pgtype.Int8{Int64: 2, Valid: true},
		ScanRunID:       pgtype.Int8{Int64: 3, Valid: true},
		Severity:        "high",
		FindingType:     "docker_tag_digest_missing",
		RepositoryName:  pgText("docker-hosted"),
		Subject:         pgText("demo/app:latest"),
		Expected:        mustJSON(t, map[string]any{"digest": "present"}),
		Actual:          mustJSON(t, map[string]any{"digest": ""}),
		Message:         pgText("missing digest"),
		Remediation:     pgText("reindex"),
		Status:          "open",
		CreatedAt:       pgTime(now),
		LastSeenAt:      pgTime(now.Add(time.Hour)),
		ResolvedAt:      pgTime(now.Add(2 * time.Hour)),
		DismissedAt:     pgTime(now.Add(3 * time.Hour)),
		Metadata:        mustJSON(t, map[string]any{"task": 1}),
	})
	if finding.NexusInstanceID != 2 || finding.ResolvedAt == nil || finding.DismissedAt == nil || finding.Expected == nil {
		t.Fatalf("finding = %+v", finding)
	}
}

func TestAPICleanupAndTagPatternMapping(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	downloadedAt := now.Add(-time.Hour)
	result := apiCleanupSimulationResult(indexing.CleanupSimulationResult{
		PolicyName:                "ci-cleanup",
		EvaluatedTagCount:         3,
		CandidateTagCount:         1,
		ProtectedTagCount:         1,
		WarningCount:              1,
		EstimatedReclaimableBytes: 100,
		Candidates: []indexing.CleanupSimulationTag{{
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
			DockerLogicalBytes:        200,
			EstimatedReclaimableBytes: 100,
			FirstSeenAt:               now.Add(-45 * 24 * time.Hour),
			LastSeenAt:                now,
			LastDownloadedAt:          &downloadedAt,
		}},
		Warnings: []indexing.CleanupSimulationWarning{{
			Code: "missing_manifest",
			Tag:  indexing.CleanupSimulationTag{TagID: 1, ImageName: "demo/app", Tag: "feature-x"},
		}},
	})
	if len(result.Candidates) != 1 || len(result.Warnings) != 1 || result.Candidates[0].LastDownloadedAt == nil {
		t.Fatalf("result = %+v", result)
	}

	execution := apiCleanupExecution(db.CleanupExecution{
		ID:                          5,
		NexusInstanceID:             4,
		RepositoryName:              pgText("docker-hosted"),
		PolicyName:                  "ci-cleanup",
		Status:                      "queued",
		RequestedBy:                 "admin",
		ApprovedBy:                  pgText("admin"),
		DestructiveEnabled:          true,
		SimulationSummary:           mustJSON(t, map[string]any{"candidate_tag_count": 1}),
		AuditLog:                    mustJSON(t, []map[string]any{{"event": "approval_recorded"}}),
		PostDeleteVerificationRunID: pgtype.Int8{Int64: 7, Valid: true},
		PostDeleteReconcileRunID:    pgtype.Int8{Int64: 8, Valid: true},
		ApprovedCandidateCount:      1,
		CreatedAt:                   pgTime(now),
		ApprovedAt:                  pgTime(now),
		StartedAt:                   pgTime(now.Add(time.Minute)),
		FinishedAt:                  pgTime(now.Add(2 * time.Minute)),
		CancelRequestedAt:           pgTime(now.Add(3 * time.Minute)),
		CanceledBy:                  pgText("admin"),
		LockedBy:                    pgText("worker"),
		LeaseExpiresAt:              pgTime(now.Add(4 * time.Minute)),
	})
	if execution.RepositoryName != "docker-hosted" || execution.PostDeleteReconcileRunID == nil || execution.AuditLog == nil {
		t.Fatalf("execution = %+v", execution)
	}

	event := apiCleanupExecutionEvent(db.CleanupExecutionEvent{
		ID:                 9,
		CleanupExecutionID: 5,
		EventType:          "candidate_deleted",
		Actor:              pgText("worker"),
		RepositoryName:     pgText("docker-hosted"),
		ImageName:          pgText("demo/app"),
		Tag:                pgText("feature-x"),
		TagID:              pgtype.Int8{Int64: 1, Valid: true},
		RuleName:           pgText("old feature"),
		ComponentID:        pgText("component"),
		Status:             pgText("deleted"),
		Message:            pgText("candidate component deleted"),
		ErrorClass:         pgText(""),
		ErrorCode:          pgText(""),
		ErrorMessage:       pgText(""),
		Details:            mustJSON(t, map[string]any{"component_id": "component"}),
		CreatedAt:          pgTime(now),
	})
	if event.Actor != "worker" || event.TagID != 1 || event.Details == nil {
		t.Fatalf("event = %+v", event)
	}

	pattern := apiTagPattern(db.TagPattern{
		ID:          10,
		Name:        "semver",
		DisplayName: "SemVer",
		Description: "semantic versions",
		TagRegex:    "^v?[0-9]+",
		Priority:    1,
		Enabled:     true,
		BuiltIn:     true,
		CreatedAt:   pgTime(now),
		UpdatedAt:   pgTime(now),
	})
	if pattern.Name != "semver" || pattern.CreatedAt.IsZero() {
		t.Fatalf("pattern = %+v", pattern)
	}
}

func TestAPIPrimitiveConversionHelpers(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if numericFloat(pgNumeric(t, "7.25")) != 7.25 || numericFloat(pgtype.Numeric{}) != 0 {
		t.Fatalf("numericFloat returned unexpected value")
	}
	if numericFloatPtr(pgtype.Numeric{}) != nil || *numericFloatPtr(pgNumeric(t, "2")) != 2 {
		t.Fatalf("numericFloatPtr returned unexpected value")
	}
	if timePtrFromAny(nil) != nil || timePtrFromAny("bad") != nil || !timePtrFromAny(now).Equal(now) {
		t.Fatalf("timePtrFromAny returned unexpected value")
	}
	if got := timePtrFromAny(pgtype.Timestamptz{Time: now, Valid: true}); got == nil || !got.Equal(now) {
		t.Fatalf("timePtrFromAny pgtype = %v", got)
	}
	if stringFromAny("x") != "x" || stringFromAny([]byte("y")) != "y" || stringFromAny(1) != "" {
		t.Fatalf("stringFromAny returned unexpected value")
	}
	if jsonValue(nil) != nil || jsonValue([]byte("{bad")) != nil || decodeJSONValue([]byte("{bad")) != nil {
		t.Fatalf("json helpers should ignore invalid values")
	}
	if decoded := jsonValue(mustJSON(t, map[string]any{"ok": true})); decoded == nil {
		t.Fatalf("jsonValue did not decode object")
	}
}

func TestAPIIndexingAdapterDelegatesToService(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	fake := &apiAdapterFakeDB{}
	fake.execFunc = func(query string, args []any) (pgconn.CommandTag, error) {
		return pgconn.NewCommandTag("DELETE 2"), nil
	}
	fake.rowFunc = func(query string, args []any) apiAdapterFakeRow {
		switch {
		case strings.Contains(query, "UpsertNexusInstanceForIndexing"):
			return apiAdapterFakeRow{values: []any{int64(7), "local", "http://nexus:8081", pgTime(now), pgTime(now)}}
		case strings.Contains(query, "GetOperationLock"), strings.Contains(query, "GetCleanupLock"):
			return apiAdapterFakeRow{err: pgx.ErrNoRows}
		case strings.Contains(query, "CreateScanRun"),
			strings.Contains(query, "CancelScanRun"):
			return apiAdapterFakeRow{values: apiAdapterScanRunValues(42, 7, indexing.ScanTypeRecentSync, "running", now)}
		case strings.Contains(query, "EnqueueScanTask"),
			strings.Contains(query, "RetryScanTaskByID"):
			return apiAdapterFakeRow{values: apiAdapterScanTaskValues(99, 42, 7, indexing.TaskTypeRepositoryDiscovery, now)}
		case strings.Contains(query, "UpsertIndexingSchedule"):
			return apiAdapterFakeRow{values: apiAdapterScheduleValues(3, 7, now)}
		case strings.Contains(query, "UpsertNexusRateLimits"),
			strings.Contains(query, "PauseNexusIndexing"),
			strings.Contains(query, "ResumeNexusIndexing"):
			return apiAdapterFakeRow{values: apiAdapterControlValues(7, now)}
		case strings.Contains(query, "GetRetentionSettings"),
			strings.Contains(query, "UpsertRetentionSettings"):
			return apiAdapterFakeRow{values: []any{true, pgtype.Int4{Int32: 3600, Valid: true}, pgtype.Int4{Int32: 30, Valid: true}, pgtype.Int4{Int32: 365, Valid: true}, pgtype.Int4{Int32: 180, Valid: true}, pgtype.Int4{Int32: 365, Valid: true}, pgText("admin"), pgTime(now)}}
		case strings.Contains(query, "GetAnalyticsOverview"):
			return apiAdapterFakeRow{values: []any{int64(1), int64(2), int64(3), int64(4), int64(5), int64(6), int64(7), int64(8), now}}
		case strings.Contains(query, "GetOldestPendingTaskAgeSeconds"),
			strings.Contains(query, "GetOldestRunningCleanupAgeSeconds"):
			return apiAdapterFakeRow{values: []any{int64(120)}}
		case strings.Contains(query, "UpsertTagPattern"):
			return apiAdapterFakeRow{values: []any{int64(5), "semver", "SemVer", "semantic versions", "^v", int32(100), true, false, pgTime(now), pgTime(now)}}
		case strings.Contains(query, "DismissVerificationFinding"):
			return apiAdapterFakeRow{values: apiAdapterFindingValues(17, now)}
		default:
			return apiAdapterFakeRow{err: fmt.Errorf("unexpected query row: %s", apiAdapterQueryName(query))}
		}
	}
	fake.rowsFunc = func(query string, args []any) (*apiAdapterFakeRows, error) {
		switch {
		case strings.Contains(query, "ListRecentScanRuns"):
			return &apiAdapterFakeRows{rows: [][]any{apiAdapterScanRunValues(42, 7, indexing.ScanTypeRecentSync, "running", now)}}, nil
		case strings.Contains(query, "ListRecentScanTasks"):
			return &apiAdapterFakeRows{rows: [][]any{apiAdapterScanTaskValues(99, 42, 7, indexing.TaskTypeRepositoryDiscovery, now)}}, nil
		case strings.Contains(query, "ListVerificationFindings"):
			return &apiAdapterFakeRows{rows: [][]any{apiAdapterFindingValues(17, now)}}, nil
		case strings.Contains(query, "ListNexusInstances"):
			return &apiAdapterFakeRows{rows: [][]any{{int64(7), "local", "http://nexus:8081", pgTime(now), pgTime(now)}}}, nil
		case strings.Contains(query, "ListIndexingSchedules"):
			return &apiAdapterFakeRows{rows: [][]any{apiAdapterScheduleListValues(3, 7, now)}}, nil
		case strings.Contains(query, "ListTagPatterns"):
			return &apiAdapterFakeRows{rows: [][]any{{int64(5), "semver", "SemVer", "semantic versions", "^v", int32(100), true, false, pgTime(now), pgTime(now)}}}, nil
		case strings.Contains(query, "ListRepositoryAnalytics"),
			strings.Contains(query, "ListImageAnalytics"),
			strings.Contains(query, "ListTagAnalytics"),
			strings.Contains(query, "ListPackageFormatRepositories"),
			strings.Contains(query, "ListTrendSeries"),
			strings.Contains(query, "ListTrendTopMovers"),
			strings.Contains(query, "ListTagPatternAnalytics"),
			strings.Contains(query, "ListTagPatternGrowth"),
			strings.Contains(query, "ListTagPatternExamples"),
			strings.Contains(query, "ListTagPatternSuggestions"):
			return &apiAdapterFakeRows{}, nil
		case strings.Contains(query, "CountScanTasksByStatus"),
			strings.Contains(query, "CountScanRunsByStatus24h"),
			strings.Contains(query, "CountScanTasksByNexusStatus"),
			strings.Contains(query, "ListOldestPendingTaskAgeByNexus"),
			strings.Contains(query, "ListStaleIndexingSchedules"),
			strings.Contains(query, "CountOpenVerificationFindingsBySeverity"),
			strings.Contains(query, "CountCleanupExecutionsByStatus"),
			strings.Contains(query, "CountCleanupExecutionEvents24h"):
			return &apiAdapterFakeRows{}, nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", apiAdapterQueryName(query))
		}
	}
	service := indexing.NewService(db.New(fake), slog.New(slog.NewTextHandler(io.Discard, nil)))
	adapter := apiIndexingAdapter{service: service, retentionDefaults: indexing.RetentionSettings{IntervalSeconds: 3600, ScanRunDays: 30, CleanupExecutionDays: 365, VerificationFindingDays: 180, TrendSnapshotDays: 365}}
	ctx := context.Background()

	if run, err := adapter.StartIndexRun(ctx, api.StartIndexRunRequest{NexusName: "local", BaseURL: "http://nexus:8081", ScanType: indexing.ScanTypeRecentSync}); err != nil || run.ID != 42 {
		t.Fatalf("StartIndexRun run=%+v err=%v", run, err)
	}
	if runs, err := adapter.ListIndexRuns(ctx, 10); err != nil || len(runs) != 1 {
		t.Fatalf("ListIndexRuns runs=%+v err=%v", runs, err)
	}
	if tasks, err := adapter.ListIndexTasks(ctx, 42, 10); err != nil || len(tasks) != 1 {
		t.Fatalf("ListIndexTasks tasks=%+v err=%v", tasks, err)
	}
	if task, err := adapter.RetryIndexTask(ctx, 99); err != nil || task.ID != 99 {
		t.Fatalf("RetryIndexTask task=%+v err=%v", task, err)
	}
	if run, err := adapter.CancelIndexRun(ctx, 42); err != nil || run.ID != 42 {
		t.Fatalf("CancelIndexRun run=%+v err=%v", run, err)
	}
	if findings, err := adapter.ListVerificationFindings(ctx, 7, "open", 10); err != nil || len(findings) != 1 {
		t.Fatalf("ListVerificationFindings findings=%+v err=%v", findings, err)
	}
	if finding, err := adapter.DismissVerificationFinding(ctx, 17, "admin"); err != nil || finding.ID != 17 {
		t.Fatalf("DismissVerificationFinding finding=%+v err=%v", finding, err)
	}
	if metrics, err := adapter.PrometheusMetrics(ctx); err != nil || !strings.Contains(metrics, "nexus_lens") {
		t.Fatalf("PrometheusMetrics metrics=%q err=%v", metrics, err)
	}
	if instances, err := adapter.ListNexusInstances(ctx, 10); err != nil || len(instances) != 1 {
		t.Fatalf("ListNexusInstances instances=%+v err=%v", instances, err)
	}
	if schedules, err := adapter.ListIndexingSchedules(ctx, 7, 10); err != nil || len(schedules) != 1 {
		t.Fatalf("ListIndexingSchedules schedules=%+v err=%v", schedules, err)
	}
	if schedule, err := adapter.UpsertIndexingSchedule(ctx, api.UpsertIndexingScheduleRequest{Name: "recent-sync", ScanType: indexing.ScanTypeRecentSync, IntervalSeconds: 1800, Enabled: true}); err != nil || schedule.ID != 3 {
		t.Fatalf("UpsertIndexingSchedule schedule=%+v err=%v", schedule, err)
	}
	if control, err := adapter.UpdateNexusRateLimits(ctx, 7, api.RateLimitSettings{}); err != nil || control.NexusInstanceID != 7 {
		t.Fatalf("UpdateNexusRateLimits control=%+v err=%v", control, err)
	}
	if settings, err := adapter.GetRetentionSettings(ctx); err != nil || settings.ScanRunDays != 30 {
		t.Fatalf("GetRetentionSettings settings=%+v err=%v", settings, err)
	}
	if settings, err := adapter.UpdateRetentionSettings(ctx, api.RetentionSettings{IntervalSeconds: 3600, ScanRunDays: 30, CleanupExecutionDays: 365, VerificationFindingDays: 180, TrendSnapshotDays: 365}); err != nil || !settings.Configured {
		t.Fatalf("UpdateRetentionSettings settings=%+v err=%v", settings, err)
	}
	if overview, err := adapter.GetAnalyticsOverview(ctx, 7); err != nil || overview.RepositoryCount != 1 {
		t.Fatalf("GetAnalyticsOverview overview=%+v err=%v", overview, err)
	}
	if _, err := adapter.ListRepositoryAnalytics(ctx, api.AnalyticsListRequest{Limit: 10}); err != nil {
		t.Fatalf("ListRepositoryAnalytics err=%v", err)
	}
	if _, err := adapter.ListImageAnalytics(ctx, api.AnalyticsListRequest{Limit: 10}); err != nil {
		t.Fatalf("ListImageAnalytics err=%v", err)
	}
	if _, err := adapter.ListTagAnalytics(ctx, api.AnalyticsListRequest{Limit: 10}); err != nil {
		t.Fatalf("ListTagAnalytics err=%v", err)
	}
	if _, err := adapter.ListPackageFormatRepositories(ctx, api.AnalyticsListRequest{Limit: 10}); err != nil {
		t.Fatalf("ListPackageFormatRepositories err=%v", err)
	}
	if _, err := adapter.ListTrendSeries(ctx, api.TrendSeriesRequest{NexusInstanceID: 7, MetricName: "docker_logical_bytes"}); err != nil {
		t.Fatalf("ListTrendSeries err=%v", err)
	}
	if _, err := adapter.ListTrendTopMovers(ctx, api.TrendTopMoversRequest{NexusInstanceID: 7, MetricName: "docker_logical_bytes"}); err != nil {
		t.Fatalf("ListTrendTopMovers err=%v", err)
	}
	if patterns, err := adapter.ListTagPatterns(ctx, 10); err != nil || len(patterns) != 1 {
		t.Fatalf("ListTagPatterns patterns=%+v err=%v", patterns, err)
	}
	if pattern, err := adapter.UpsertTagPattern(ctx, api.UpsertTagPatternRequest{Name: "semver", TagRegex: "^v"}); err != nil || pattern.ID != 5 {
		t.Fatalf("UpsertTagPattern pattern=%+v err=%v", pattern, err)
	}
	if run, err := adapter.StartTagReclassification(ctx, api.TagReclassificationRequest{NexusInstanceID: 7}); err != nil || run.ID != 42 {
		t.Fatalf("StartTagReclassification run=%+v err=%v", run, err)
	}
	if _, err := adapter.ListTagPatternAnalytics(ctx, api.TagPatternListRequest{Limit: 10}); err != nil {
		t.Fatalf("ListTagPatternAnalytics err=%v", err)
	}
	if _, err := adapter.ListTagPatternGrowth(ctx, api.TagPatternListRequest{Limit: 10}); err != nil {
		t.Fatalf("ListTagPatternGrowth err=%v", err)
	}
	if _, err := adapter.ListTagPatternExamples(ctx, api.TagPatternListRequest{Limit: 10}); err != nil {
		t.Fatalf("ListTagPatternExamples err=%v", err)
	}
	if _, err := adapter.ListTagPatternSuggestions(ctx, api.TagPatternListRequest{Limit: 10}); err != nil {
		t.Fatalf("ListTagPatternSuggestions err=%v", err)
	}
	if control, err := adapter.PauseNexusIndexing(ctx, 7, "maintenance", "admin"); err != nil || !control.Paused {
		t.Fatalf("PauseNexusIndexing control=%+v err=%v", control, err)
	}
	if control, err := adapter.ResumeNexusIndexing(ctx, 7); err != nil || control.NexusInstanceID != 7 {
		t.Fatalf("ResumeNexusIndexing control=%+v err=%v", control, err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func pgText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: true}
}

func pgTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func pgNumeric(t *testing.T, value string) pgtype.Numeric {
	t.Helper()
	var numeric pgtype.Numeric
	if err := numeric.Scan(value); err != nil {
		t.Fatal(err)
	}
	return numeric
}

type apiAdapterFakeDB struct {
	rowFunc  func(query string, args []any) apiAdapterFakeRow
	rowsFunc func(query string, args []any) (*apiAdapterFakeRows, error)
	execFunc func(query string, args []any) (pgconn.CommandTag, error)
}

func (f *apiAdapterFakeDB) Exec(_ context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	if f.execFunc != nil {
		return f.execFunc(query, args)
	}
	return pgconn.NewCommandTag("DELETE 0"), nil
}

func (f *apiAdapterFakeDB) Query(_ context.Context, query string, args ...interface{}) (pgx.Rows, error) {
	if f.rowsFunc == nil {
		return nil, fmt.Errorf("unexpected query: %s", apiAdapterQueryName(query))
	}
	return f.rowsFunc(query, args)
}

func (f *apiAdapterFakeDB) QueryRow(_ context.Context, query string, args ...interface{}) pgx.Row {
	if f.rowFunc == nil {
		return apiAdapterFakeRow{err: fmt.Errorf("unexpected query row: %s", apiAdapterQueryName(query))}
	}
	return f.rowFunc(query, args)
}

type apiAdapterFakeRow struct {
	values []any
	err    error
}

func (r apiAdapterFakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan destination count %d does not match values %d", len(dest), len(r.values))
	}
	for i := range dest {
		if err := apiAdapterAssignScanValue(dest[i], r.values[i]); err != nil {
			return fmt.Errorf("scan value %d: %w", i, err)
		}
	}
	return nil
}

type apiAdapterFakeRows struct {
	rows  [][]any
	index int
	err   error
}

func (r *apiAdapterFakeRows) Close()                                       {}
func (r *apiAdapterFakeRows) Err() error                                   { return r.err }
func (r *apiAdapterFakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *apiAdapterFakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *apiAdapterFakeRows) RawValues() [][]byte                          { return nil }
func (r *apiAdapterFakeRows) Conn() *pgx.Conn                              { return nil }
func (r *apiAdapterFakeRows) Next() bool {
	if r.index >= len(r.rows) {
		return false
	}
	r.index++
	return true
}
func (r *apiAdapterFakeRows) Scan(dest ...any) error {
	if r.index == 0 || r.index > len(r.rows) {
		return fmt.Errorf("scan called without current row")
	}
	return apiAdapterFakeRow{values: r.rows[r.index-1]}.Scan(dest...)
}
func (r *apiAdapterFakeRows) Values() ([]any, error) {
	if r.index == 0 || r.index > len(r.rows) {
		return nil, fmt.Errorf("values called without current row")
	}
	return r.rows[r.index-1], nil
}

func apiAdapterAssignScanValue(dest any, value any) error {
	target := reflect.ValueOf(dest)
	if target.Kind() != reflect.Pointer || target.IsNil() {
		return fmt.Errorf("destination is not a nonnil pointer")
	}
	elem := target.Elem()
	if value == nil {
		elem.SetZero()
		return nil
	}
	source := reflect.ValueOf(value)
	if source.Type().AssignableTo(elem.Type()) {
		elem.Set(source)
		return nil
	}
	if source.Type().ConvertibleTo(elem.Type()) {
		elem.Set(source.Convert(elem.Type()))
		return nil
	}
	if elem.Kind() == reflect.Interface {
		elem.Set(source)
		return nil
	}
	return fmt.Errorf("cannot assign %T to %s", value, elem.Type())
}

func apiAdapterQueryName(query string) string {
	line, _, _ := strings.Cut(query, "\n")
	return strings.TrimPrefix(line, "-- name: ")
}

func apiAdapterScanRunValues(id, nexusID int64, scanType, status string, now time.Time) []any {
	return []any{id, nexusID, scanType, pgtype.Text{}, "manual", pgtype.Text{}, status, pgText("queued"), pgTime(now), pgtype.Timestamptz{}, pgTime(now), pgtype.Int8{Int64: 1, Valid: true}, int64(0), int64(0), pgtype.Numeric{}, "estimated", []byte(`{}`), []byte(`{}`), []byte(`{}`), pgtype.Text{}, pgtype.Text{}, pgtype.Text{}, []byte(`{}`)}
}

func apiAdapterScanTaskValues(id, scanRunID, nexusID int64, taskType string, now time.Time) []any {
	return []any{id, scanRunID, nexusID, taskType, "task", pgtype.Text{}, pgtype.Text{}, "asset_list", pgtype.Int4{}, "pending", int32(0), int32(0), int32(3), pgTime(now), pgtype.Timestamptz{}, pgtype.UUID{}, pgtype.Text{}, pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgTime(now), pgtype.Int8{}, pgtype.Int8{}, pgtype.Text{}, []byte(`{}`), []byte(`{}`), []byte(`{}`), pgtype.Text{}, pgtype.Text{}, pgtype.Text{}, []byte(`{}`), pgTime(now)}
}

func apiAdapterScheduleValues(id, nexusID int64, now time.Time) []any {
	return []any{id, nexusID, indexing.ScanTypeRecentSync, "recent-sync", true, "every 1800s", int32(1800), "UTC", []byte(`{}`), "skip", pgTime(now), pgtype.Timestamptz{}, pgTime(now), pgTime(now)}
}

func apiAdapterScheduleListValues(id, nexusID int64, now time.Time) []any {
	return []any{id, nexusID, "local", "http://nexus:8081", indexing.ScanTypeRecentSync, "recent-sync", true, "every 1800s", int32(1800), "UTC", []byte(`{}`), "skip", pgTime(now), pgtype.Timestamptz{}, pgTime(now), pgTime(now)}
}

func apiAdapterControlValues(nexusID int64, now time.Time) []any {
	return []any{nexusID, true, pgText("maintenance"), pgTime(now), pgText("admin"), int32(1), pgTime(now.Add(time.Minute)), pgTime(now), pgText("rate_limited"), pgText("nexus"), pgTime(now), pgtype.Numeric{}, pgtype.Numeric{}, pgtype.Numeric{}}
}

func apiAdapterFindingValues(id int64, now time.Time) []any {
	return []any{id, pgtype.Int8{Int64: 7, Valid: true}, pgtype.Int8{Int64: 42, Valid: true}, "warning", "missing_digest", pgText("docker-hosted"), pgText("demo/app:latest"), []byte(`{}`), []byte(`{}`), pgText("missing digest"), pgText("rescan tag"), "open", pgTime(now), pgTime(now), pgtype.Timestamptz{}, pgtype.Timestamptz{}, []byte(`{}`)}
}
