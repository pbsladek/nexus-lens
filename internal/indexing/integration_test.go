package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pbsladek/nexus-lens/internal/db"
	"github.com/pbsladek/nexus-lens/internal/nexus"
	"github.com/pbsladek/nexus-lens/internal/postgres"
)

func TestIntegrationRetentionSettingsAndPackageFormatQueries(t *testing.T) {
	queries, pool := migratedIntegrationQueries(t)
	ctx := context.Background()
	service := NewService(queries, slog.Default())

	defaults := RetentionSettings{
		IntervalSeconds:         900,
		ScanRunDays:             14,
		CleanupExecutionDays:    90,
		VerificationFindingDays: 45,
		TrendSnapshotDays:       365,
	}
	settings, err := service.GetRetentionSettings(ctx, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Configured || settings.ScanRunDays != 14 {
		t.Fatalf("default retention settings = %+v", settings)
	}

	updated, err := service.UpsertRetentionSettings(ctx, RetentionSettings{
		IntervalSeconds:         7200,
		ScanRunDays:             30,
		CleanupExecutionDays:    400,
		VerificationFindingDays: 120,
		TrendSnapshotDays:       730,
		UpdatedBy:               "integration-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Configured || updated.UpdatedBy != "integration-admin" || updated.TrendSnapshotDays != 730 {
		t.Fatalf("updated retention settings = %+v", updated)
	}

	instance, err := queries.UpsertNexusInstanceForIndexing(ctx, db.UpsertNexusInstanceForIndexingParams{
		Name:    "local",
		BaseUrl: "http://nexus:8081",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, repo := range []struct {
		name   string
		format string
		typ    string
	}{
		{name: "docker-hosted", format: "docker", typ: "hosted"},
		{name: "pypi-hosted", format: "pypi", typ: "hosted"},
		{name: "maven-proxy", format: "maven2", typ: "proxy"},
	} {
		if _, err := queries.UpsertRepositoryFromDiscovery(ctx, db.UpsertRepositoryFromDiscoveryParams{
			NexusInstanceID: instance.ID,
			Name:            repo.name,
			Format:          repo.format,
			Type:            repo.typ,
			Metadata:        emptyJSON,
		}); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := service.ListPackageFormatRepositories(ctx, AnalyticsListRequest{NexusInstanceID: instance.ID, Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	statusByName := map[string]string{}
	for _, row := range rows {
		statusByName[row.RepositoryName] = row.SupportStatus
	}
	if statusByName["docker-hosted"] != "indexed" || statusByName["pypi-hosted"] != "asset_indexed" || statusByName["maven-proxy"] != "skipped" {
		t.Fatalf("support statuses = %+v", statusByName)
	}

	var versionCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM goose_db_version").Scan(&versionCount); err != nil {
		t.Fatal(err)
	}
	if versionCount == 0 {
		t.Fatalf("expected goose migration version rows")
	}
}

func TestIntegrationScanPackageAssetsPaginatesBoundsAndUpserts(t *testing.T) {
	queries, pool := migratedIntegrationQueries(t)
	ctx := context.Background()

	instance, err := queries.UpsertNexusInstanceForIndexing(ctx, db.UpsertNexusInstanceForIndexingParams{
		Name:    "local",
		BaseUrl: "http://nexus:8081",
	})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := queries.UpsertRepositoryFromDiscovery(ctx, db.UpsertRepositoryFromDiscoveryParams{
		NexusInstanceID: instance.ID,
		Name:            "npm-hosted",
		Format:          "npm",
		Type:            "hosted",
		Metadata:        emptyJSON,
	})
	if err != nil {
		t.Fatal(err)
	}

	var requestedTokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/service/rest/v1/assets" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("repository"); got != "npm-hosted" {
			t.Fatalf("repository = %q, want npm-hosted", got)
		}
		token := r.URL.Query().Get("continuationToken")
		requestedTokens = append(requestedTokens, token)
		switch token {
		case "":
			_ = json.NewEncoder(w).Encode(nexus.AssetsPage{
				ContinuationToken: "next-page",
				Items: []nexus.Asset{
					{
						ID:           "asset-recent",
						Repository:   "npm-hosted",
						Format:       "npm",
						Path:         "left-pad/-/left-pad-1.3.0.tgz",
						FileSize:     123,
						LastModified: "2026-06-20T00:00:00Z",
						Checksum:     nexus.AssetChecksum{SHA256: "sha256:recent"},
					},
					{
						ID:           "asset-old",
						Repository:   "npm-hosted",
						Format:       "npm",
						Path:         "old-pkg/-/old-pkg-0.1.0.tgz",
						FileSize:     456,
						LastModified: "2026-04-01T00:00:00Z",
						Checksum:     nexus.AssetChecksum{SHA256: "sha256:old"},
					},
				},
			})
		case "next-page":
			_ = json.NewEncoder(w).Encode(nexus.AssetsPage{
				Items: []nexus.Asset{{
					ID:         "asset-missing-time",
					Repository: "npm-hosted",
					Format:     "npm",
					Path:       "missing/-/missing-1.0.0.tgz",
					FileSize:   789,
					Checksum:   nexus.AssetChecksum{SHA256: "sha256:missing"},
				}},
			})
		default:
			t.Fatalf("unexpected continuation token %q", token)
		}
	}))
	defer server.Close()

	worker := NewWorker(queries, slog.Default())
	payload := jsonBytes(map[string]any{
		"base_url":        server.URL,
		"repository_id":   repository.ID,
		"repository_name": "npm-hosted",
		"format":          "npm",
		"scope":           taskScope{Since: "2026-06-01T00:00:00Z"},
	})
	taskErr := worker.scanPackageAssets(ctx, db.ScanTask{
		ScanRunID:       1,
		NexusInstanceID: instance.ID,
		TaskType:        TaskTypePackageAssetScan,
		CostClass:       "asset_list",
		Payload:         payload,
	})
	if taskErr != nil {
		t.Fatalf("scanPackageAssets returned %+v", taskErr)
	}

	if got := strings.Join(requestedTokens, ","); got != ",next-page" {
		t.Fatalf("requested continuation tokens = %q", got)
	}
	assertTableCount(t, pool, "nexus_assets", 3)
	assertTableCount(t, pool, "generic_artifacts", 1)

	var name, version string
	if err := pool.QueryRow(ctx, "SELECT name, version FROM generic_artifacts").Scan(&name, &version); err != nil {
		t.Fatal(err)
	}
	if name != "left-pad" || version != "1.3.0" {
		t.Fatalf("generic artifact = %s %s", name, version)
	}
}

func TestIntegrationCleanupExecutionClaimTouchAndCancel(t *testing.T) {
	queries, _ := migratedIntegrationQueries(t)
	ctx := context.Background()

	instance, err := queries.UpsertNexusInstanceForIndexing(ctx, db.UpsertNexusInstanceForIndexingParams{
		Name:    "local",
		BaseUrl: "http://nexus:8081",
	})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := queries.CreateCleanupExecution(ctx, db.CreateCleanupExecutionParams{
		NexusInstanceID:        instance.ID,
		RepositoryName:         text("docker-hosted-a"),
		PolicyName:             "ci-cleanup",
		PolicyYaml:             "name: ci-cleanup",
		Status:                 "queued",
		RequestedBy:            "admin",
		ApprovedBy:             text("admin"),
		ApprovalText:           "ci-cleanup",
		DestructiveEnabled:     true,
		SimulationSummary:      emptyJSON,
		AuditLog:               []byte("[]"),
		ApprovedCandidates:     []byte("[]"),
		ApprovedCandidateCount: 1,
		ApprovedAt:             timestamptz(time.Now().UTC()),
	})
	if err != nil {
		t.Fatal(err)
	}

	claimed, err := queries.ClaimQueuedCleanupExecution(ctx, db.ClaimQueuedCleanupExecutionParams{
		WorkerID:     textRequired("worker-a"),
		LeaseSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != execution.ID || claimed.Status != "running" || textValue(claimed.LockedBy) != "worker-a" || !claimed.LeaseExpiresAt.Valid {
		t.Fatalf("claimed cleanup execution = %+v", claimed)
	}

	touched, err := queries.TouchCleanupExecutionLease(ctx, db.TouchCleanupExecutionLeaseParams{
		ID:           claimed.ID,
		LeaseSeconds: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !touched.LeaseExpiresAt.Valid || !touched.LeaseExpiresAt.Time.After(claimed.LeaseExpiresAt.Time) {
		t.Fatalf("touched lease = %v, claimed lease = %v", touched.LeaseExpiresAt, claimed.LeaseExpiresAt)
	}

	canceled, err := queries.RequestCleanupExecutionCancel(ctx, db.RequestCleanupExecutionCancelParams{
		ID:         touched.ID,
		CanceledBy: textRequired("admin"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != "cancel_requested" || textValue(canceled.CanceledBy) != "admin" || !canceled.LeaseExpiresAt.Valid {
		t.Fatalf("canceled cleanup execution = %+v", canceled)
	}
}

func TestIntegrationServiceEnqueueScheduleLocksAndPause(t *testing.T) {
	queries, pool := migratedIntegrationQueries(t)
	ctx := context.Background()
	service := NewService(queries, slog.Default())

	boundedRun, err := service.EnqueueRepositoryDiscoveryRun(ctx, EnqueueRepositoryDiscoveryRunRequest{
		NexusName: "bounded",
		BaseURL:   "http://nexus:8081",
		ScanType:  ScanTypeBoundedReindex,
		Scope: RunScope{
			Repositories: []string{"docker-hosted-b", "docker-hosted-a", "docker-hosted-a"},
			WindowDays:   30,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var cursor struct {
		Scope taskScope `json:"scope"`
	}
	if err := json.Unmarshal(boundedRun.CursorBefore, &cursor); err != nil {
		t.Fatal(err)
	}
	if cursor.Scope.WindowDays != 30 || strings.Join(cursor.Scope.Repositories, ",") != "docker-hosted-a,docker-hosted-b" || cursor.Scope.Since == "" {
		t.Fatalf("bounded run scope = %+v", cursor.Scope)
	}

	_, err = service.EnqueueRepositoryDiscoveryRun(ctx, EnqueueRepositoryDiscoveryRunRequest{
		NexusName: "bounded",
		BaseURL:   "http://nexus:8081",
		ScanType:  ScanTypeFullReconcile,
	})
	var conflict *OperationLockConflict
	if !errors.As(err, &conflict) || conflict.BlockingScanRunID != boundedRun.ID {
		t.Fatalf("lock conflict = %#v from %v", conflict, err)
	}

	schedule, err := service.UpsertSchedule(ctx, ScheduleRequest{
		NexusName:       "scheduled",
		BaseURL:         "http://nexus:8081",
		Name:            "recent-sync",
		ScanType:        ScanTypeRecentSync,
		IntervalSeconds: 900,
		Enabled:         true,
		DueNow:          true,
		Scope:           RunScope{Repositories: []string{"docker-hosted-a"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ProcessDueSchedules(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Processed != 1 || result.Enqueued != 1 || result.Skipped != 0 {
		t.Fatalf("scheduler result = %+v", result)
	}
	var schedulerRunCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM scan_runs WHERE trigger_source = 'scheduler' AND trigger_job_name = $1", schedule.Name).Scan(&schedulerRunCount); err != nil {
		t.Fatal(err)
	}
	if schedulerRunCount != 1 {
		t.Fatalf("scheduler run count = %d, want 1", schedulerRunCount)
	}

	control, err := service.PauseNexusIndexing(ctx, schedule.NexusInstanceID, "maintenance", "operator")
	if err != nil {
		t.Fatal(err)
	}
	if !control.Paused || textValue(control.PausedReason) != "maintenance" || textValue(control.PausedBy) != "operator" {
		t.Fatalf("paused control = %+v", control)
	}
	control, err = service.ResumeNexusIndexing(ctx, schedule.NexusInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if control.Paused || control.PausedReason.Valid || control.PausedBy.Valid {
		t.Fatalf("resumed control = %+v", control)
	}
}

func migratedIntegrationQueries(t *testing.T) (*db.Queries, *pgxpool.Pool) {
	t.Helper()
	rawURL := os.Getenv("NEXUS_LENS_TEST_DATABASE_URL")
	if rawURL == "" {
		t.Skip("set NEXUS_LENS_TEST_DATABASE_URL to run Postgres integration tests")
	}

	ctx := context.Background()
	adminPool, err := pgxpool.New(ctx, rawURL)
	if err != nil {
		t.Fatalf("open admin test database pool: %v", err)
	}

	schema := "nexus_lens_test_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := adminPool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", pgx.Identifier{schema}.Sanitize())); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = adminPool.Exec(cleanupCtx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pgx.Identifier{schema}.Sanitize()))
		adminPool.Close()
	})

	schemaURL := databaseURLWithSearchPath(t, rawURL, schema)
	database, err := postgres.Open(ctx, postgres.Config{URL: schemaURL}, slog.Default())
	if err != nil {
		t.Fatalf("open migrated test database: %v", err)
	}
	t.Cleanup(database.Close)

	return db.New(database.Pool), database.Pool
}

func databaseURLWithSearchPath(t *testing.T, rawURL, schema string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	values := parsed.Query()
	values.Set("search_path", schema)
	parsed.RawQuery = values.Encode()
	return parsed.String()
}

func assertTableCount(t *testing.T, pool *pgxpool.Pool, table string, want int) {
	t.Helper()
	var got int
	ctx := context.Background()
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}
