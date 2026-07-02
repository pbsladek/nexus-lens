package indexing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/pbsladek/nexus-lens/internal/db"
	"github.com/pbsladek/nexus-lens/internal/nexus"
	"github.com/pbsladek/nexus-lens/internal/registry"
)

type fakeDBTX struct {
	t        *testing.T
	rowFunc  func(query string, args []any) fakeRow
	rowsFunc func(query string, args []any) (*fakeRows, error)
	execFunc func(query string, args []any) (pgconn.CommandTag, error)
	queries  []string
	args     [][]any
}

func (f *fakeDBTX) Exec(_ context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	f.queries = append(f.queries, query)
	f.args = append(f.args, args)
	if f.execFunc != nil {
		return f.execFunc(query, args)
	}
	return pgconn.NewCommandTag("DELETE 0"), nil
}

func (f *fakeDBTX) Query(_ context.Context, query string, args ...interface{}) (pgx.Rows, error) {
	f.queries = append(f.queries, query)
	f.args = append(f.args, args)
	if f.rowsFunc == nil {
		return nil, fmt.Errorf("unexpected query: %s", queryName(query))
	}
	return f.rowsFunc(query, args)
}

func (f *fakeDBTX) QueryRow(_ context.Context, query string, args ...interface{}) pgx.Row {
	f.queries = append(f.queries, query)
	f.args = append(f.args, args)
	if f.rowFunc == nil {
		return fakeRow{err: fmt.Errorf("unexpected query row: %s", queryName(query))}
	}
	return f.rowFunc(query, args)
}

type fakeRow struct {
	values []any
	err    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan destination count %d does not match values %d", len(dest), len(r.values))
	}
	for i := range dest {
		if err := assignScanValue(dest[i], r.values[i]); err != nil {
			return fmt.Errorf("scan value %d: %w", i, err)
		}
	}
	return nil
}

type fakeRows struct {
	rows   [][]any
	index  int
	closed bool
	err    error
}

func (r *fakeRows) Close()                                       { r.closed = true }
func (r *fakeRows) Err() error                                   { return r.err }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

func (r *fakeRows) Next() bool {
	if r.index >= len(r.rows) {
		r.closed = true
		return false
	}
	r.index++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.index == 0 || r.index > len(r.rows) {
		return fmt.Errorf("scan called without current row")
	}
	return fakeRow{values: r.rows[r.index-1]}.Scan(dest...)
}

func (r *fakeRows) Values() ([]any, error) {
	if r.index == 0 || r.index > len(r.rows) {
		return nil, fmt.Errorf("values called without current row")
	}
	return r.rows[r.index-1], nil
}

func assignScanValue(dest any, value any) error {
	if dest == nil {
		return nil
	}
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
	if elem.Kind() == reflect.Interface && source.Type().AssignableTo(elem.Type()) {
		elem.Set(source)
		return nil
	}
	return fmt.Errorf("cannot assign %T to %s", value, elem.Type())
}

func queryName(query string) string {
	line, _, _ := strings.Cut(query, "\n")
	return strings.TrimPrefix(line, "-- name: ")
}

func TestServiceEnqueueRepositoryDiscoveryRunWithFakeDB(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	fake := &fakeDBTX{}
	fake.rowFunc = func(query string, args []any) fakeRow {
		switch {
		case strings.Contains(query, "UpsertNexusInstanceForIndexing"):
			return fakeRow{values: []any{int64(7), "local", "http://nexus:8081", pgTimeIndex(now), pgTimeIndex(now)}}
		case strings.Contains(query, "GetOperationLock"), strings.Contains(query, "GetCleanupLock"):
			return fakeRow{err: pgx.ErrNoRows}
		case strings.Contains(query, "CreateScanRun"):
			return fakeRow{values: scanRunValues(42, 7, ScanTypeRecentSync, "running", "queued", now)}
		case strings.Contains(query, "EnqueueScanTask"):
			return fakeRow{values: scanTaskValues(99, 42, 7, TaskTypeRepositoryDiscovery, "repository-discovery", "pending", now)}
		default:
			return fakeRow{err: fmt.Errorf("unexpected query row: %s", queryName(query))}
		}
	}
	service := NewService(db.New(fake), slog.New(slog.NewTextHandler(io.Discard, nil)))

	run, err := service.EnqueueRepositoryDiscoveryRun(context.Background(), EnqueueRepositoryDiscoveryRunRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != 42 || run.NexusInstanceID != 7 || run.ScanType != ScanTypeRecentSync {
		t.Fatalf("run = %+v", run)
	}
	if !calledQuery(fake, "EnqueueScanTask") {
		t.Fatalf("expected EnqueueScanTask to be called; queries=%v", queryNames(fake.queries))
	}
}

func TestServiceEnqueueRepositoryDiscoveryRunDetectsOperationLock(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	fake := &fakeDBTX{}
	fake.rowFunc = func(query string, args []any) fakeRow {
		switch {
		case strings.Contains(query, "UpsertNexusInstanceForIndexing"):
			return fakeRow{values: []any{int64(7), "local", "http://nexus:8081", pgTimeIndex(now), pgTimeIndex(now)}}
		case strings.Contains(query, "GetOperationLock"):
			return fakeRow{values: []any{int64(7), int64(41), ScanTypeFullReconcile, "nexus", pgTimeIndex(now), pgTimeIndex(now.Add(time.Hour)), pgTextIndex("full reconcile"), ScanTypeFullReconcile, "running"}}
		default:
			return fakeRow{err: fmt.Errorf("unexpected query row: %s", queryName(query))}
		}
	}
	service := NewService(db.New(fake), slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := service.EnqueueRepositoryDiscoveryRun(context.Background(), EnqueueRepositoryDiscoveryRunRequest{ScanType: ScanTypeBoundedReindex})
	var conflict *OperationLockConflict
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(fmt.Sprintf("%T", conflict), "OperationLockConflict") {
		t.Fatalf("unexpected conflict type guard")
	}
}

func TestServiceUpsertScheduleWithFakeDB(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	fake := &fakeDBTX{}
	fake.rowFunc = func(query string, args []any) fakeRow {
		switch {
		case strings.Contains(query, "UpsertNexusInstanceForIndexing"):
			return fakeRow{values: []any{int64(7), "local", "http://nexus:8081", pgTimeIndex(now), pgTimeIndex(now)}}
		case strings.Contains(query, "UpsertIndexingSchedule"):
			if args[2] != "recent-sync" || args[5] != int32(1800) {
				t.Fatalf("schedule args = %+v", args)
			}
			return fakeRow{values: []any{int64(3), int64(7), ScanTypeRecentSync, "recent-sync", true, "every 1800s", int32(1800), "UTC", []byte(`{}`), "skip", pgTimeIndex(now), pgtype.Timestamptz{}, pgTimeIndex(now), pgTimeIndex(now)}}
		default:
			return fakeRow{err: fmt.Errorf("unexpected query row: %s", queryName(query))}
		}
	}
	service := NewService(db.New(fake), slog.New(slog.NewTextHandler(io.Discard, nil)))

	schedule, err := service.UpsertSchedule(context.Background(), ScheduleRequest{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if schedule.ID != 3 || schedule.Name != "recent-sync" || schedule.IntervalSeconds != 1800 {
		t.Fatalf("schedule = %+v", schedule)
	}
}

func TestServiceListRetryAndMetricsWithFakeDB(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	fake := &fakeDBTX{}
	fake.rowFunc = func(query string, args []any) fakeRow {
		switch {
		case strings.Contains(query, "RetryScanTaskByID"):
			return fakeRow{values: retryScanTaskRowValues(99, 42, 7, now)}
		case strings.Contains(query, "GetOldestPendingTaskAgeSeconds"):
			return fakeRow{values: []any{int64(120)}}
		case strings.Contains(query, "GetOldestRunningCleanupAgeSeconds"):
			return fakeRow{values: []any{int64(300)}}
		default:
			return fakeRow{err: fmt.Errorf("unexpected query row: %s", queryName(query))}
		}
	}
	fake.rowsFunc = func(query string, args []any) (*fakeRows, error) {
		switch {
		case strings.Contains(query, "ListRecentScanRuns"):
			return &fakeRows{rows: [][]any{scanRunValues(42, 7, ScanTypeRecentSync, "running", "queued", now)}}, nil
		case strings.Contains(query, "ListRecentScanTasks"):
			return &fakeRows{rows: [][]any{scanTaskValues(99, 42, 7, TaskTypeRepositoryDiscovery, "repository-discovery", "pending", now)}}, nil
		case strings.Contains(query, "CountScanTasksByStatus"):
			return &fakeRows{rows: [][]any{{"pending", int64(2)}}}, nil
		case strings.Contains(query, "CountScanRunsByStatus24h"):
			return &fakeRows{rows: [][]any{{"running", int64(1)}}}, nil
		case strings.Contains(query, "CountOpenVerificationFindingsBySeverity"):
			return &fakeRows{rows: [][]any{{"high", int64(3)}}}, nil
		case strings.Contains(query, "CountCleanupExecutionsByStatus"):
			return &fakeRows{rows: [][]any{{"queued", int64(4)}}}, nil
		case strings.Contains(query, "CountCleanupExecutionEvents24h"):
			return &fakeRows{rows: [][]any{{"cleanup_started", pgTextIndex("running"), int64(5)}}}, nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", queryName(query))
		}
	}
	service := NewService(db.New(fake), slog.New(slog.NewTextHandler(io.Discard, nil)))

	runs, err := service.ListRecentRuns(context.Background(), 0)
	if err != nil || len(runs) != 1 || runs[0].ID != 42 {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
	tasks, err := service.ListRecentTasks(context.Background(), 42, 0)
	if err != nil || len(tasks) != 1 || tasks[0].ID != 99 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	task, err := service.RetryTask(context.Background(), 99)
	if err != nil || task.ID != 99 || task.ScanRunID != 42 {
		t.Fatalf("retry task=%+v err=%v", task, err)
	}
	metrics, err := service.PrometheusMetrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`nexus_lens_scan_tasks{status="pending"} 2`,
		`nexus_lens_scan_runs_24h{status="running"} 1`,
		`nexus_lens_verification_findings{status="open",severity="high"} 3`,
		`nexus_lens_cleanup_executions{status="queued"} 4`,
		`nexus_lens_cleanup_oldest_running_age_seconds 300`,
	} {
		if !strings.Contains(metrics, want) {
			t.Fatalf("metrics missing %q:\n%s", want, metrics)
		}
	}
}

func TestServiceWrapperQueriesWithFakeDB(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	fake := &fakeDBTX{}
	fake.execFunc = func(query string, args []any) (pgconn.CommandTag, error) {
		return pgconn.NewCommandTag("DELETE 2"), nil
	}
	fake.rowFunc = func(query string, args []any) fakeRow {
		switch {
		case strings.Contains(query, "UpsertNexusRateLimits"),
			strings.Contains(query, "PauseNexusIndexing"),
			strings.Contains(query, "ResumeNexusIndexing"):
			return fakeRow{values: indexingControlValues(7, now)}
		case strings.Contains(query, "GetRetentionSettings"),
			strings.Contains(query, "UpsertRetentionSettings"):
			return fakeRow{values: retentionSettingValues(now)}
		case strings.Contains(query, "GetAnalyticsOverview"):
			return fakeRow{values: []any{int64(1), int64(2), int64(3), int64(4), int64(5), int64(6), int64(7), int64(8), now}}
		case strings.Contains(query, "UpsertTagPattern"):
			return fakeRow{values: tagPatternValues(1, now)}
		case strings.Contains(query, "CreateScanRun"):
			return fakeRow{values: scanRunValues(42, 7, ScanTypeTagReclassification, "running", "queued", now)}
		case strings.Contains(query, "EnqueueScanTask"):
			return fakeRow{values: scanTaskValues(99, 42, 7, TaskTypeTagReclassification, "tag-reclassification/7/docker-hosted", "pending", now)}
		case strings.Contains(query, "CancelScanRun"):
			return fakeRow{values: scanRunValues(42, 7, ScanTypeRecentSync, "canceled", "finished", now)}
		case strings.Contains(query, "DismissVerificationFinding"):
			return fakeRow{values: verificationFindingValues(17, now)}
		default:
			return fakeRow{err: fmt.Errorf("unexpected query row: %s", queryName(query))}
		}
	}
	fake.rowsFunc = func(query string, args []any) (*fakeRows, error) {
		switch {
		case strings.Contains(query, "ListNexusInstances"):
			return &fakeRows{rows: [][]any{{int64(7), "local", "http://nexus:8081", pgTimeIndex(now), pgTimeIndex(now)}}}, nil
		case strings.Contains(query, "ListIndexingSchedules"),
			strings.Contains(query, "ListRepositoryAnalytics"),
			strings.Contains(query, "ListImageAnalytics"),
			strings.Contains(query, "ListTagAnalytics"),
			strings.Contains(query, "ListPackageFormatRepositories"),
			strings.Contains(query, "ListTrendSeries"),
			strings.Contains(query, "ListTrendTopMovers"),
			strings.Contains(query, "ListTagPatternAnalytics"),
			strings.Contains(query, "ListTagPatternGrowth"),
			strings.Contains(query, "ListTagPatternExamples"),
			strings.Contains(query, "ListTagPatternSuggestions"),
			strings.Contains(query, "ListVerificationFindings"):
			return &fakeRows{}, nil
		case strings.Contains(query, "ListTagPatterns"):
			return &fakeRows{rows: [][]any{tagPatternValues(1, now)}}, nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", queryName(query))
		}
	}
	service := NewService(db.New(fake), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()

	if rows, err := service.ListNexusInstances(ctx, 500); err != nil || len(rows) != 1 || rows[0].ID != 7 {
		t.Fatalf("ListNexusInstances rows=%+v err=%v", rows, err)
	}
	if _, err := service.ListSchedules(ctx, 7, 500); err != nil {
		t.Fatalf("ListSchedules err=%v", err)
	}
	rps := 2.5
	if control, err := service.UpdateRateLimits(ctx, 7, RateLimitSettings{AssetListRPS: &rps}); err != nil || control.NexusInstanceID != 7 {
		t.Fatalf("UpdateRateLimits control=%+v err=%v", control, err)
	}
	if settings, err := service.GetRetentionSettings(ctx, RetentionSettings{}); err != nil || settings.ScanRunDays != 60 {
		t.Fatalf("GetRetentionSettings settings=%+v err=%v", settings, err)
	}
	if settings, err := service.UpsertRetentionSettings(ctx, RetentionSettings{ScanRunDays: 90}); err != nil || !settings.Configured {
		t.Fatalf("UpsertRetentionSettings settings=%+v err=%v", settings, err)
	}
	if overview, err := service.GetAnalyticsOverview(ctx, 7); err != nil || overview.RepositoryCount != 1 {
		t.Fatalf("GetAnalyticsOverview overview=%+v err=%v", overview, err)
	}
	if _, err := service.ListRepositoryAnalytics(ctx, AnalyticsListRequest{Limit: 999}); err != nil {
		t.Fatalf("ListRepositoryAnalytics err=%v", err)
	}
	if _, err := service.ListImageAnalytics(ctx, AnalyticsListRequest{RepositoryName: "docker-hosted"}); err != nil {
		t.Fatalf("ListImageAnalytics err=%v", err)
	}
	if _, err := service.ListTagAnalytics(ctx, AnalyticsListRequest{OlderThanDays: 30, NeverDownloaded: true}); err != nil {
		t.Fatalf("ListTagAnalytics err=%v", err)
	}
	if _, err := service.ListPackageFormatRepositories(ctx, AnalyticsListRequest{Search: "pypi"}); err != nil {
		t.Fatalf("ListPackageFormatRepositories err=%v", err)
	}
	if _, err := service.ListTrendSeries(ctx, TrendSeriesRequest{NexusInstanceID: 7, StartDate: "2026-06-01", EndDate: "2026-07-01", PatternName: "feature_branch"}); err != nil {
		t.Fatalf("ListTrendSeries err=%v", err)
	}
	if _, err := service.ListTrendTopMovers(ctx, TrendTopMoversRequest{NexusInstanceID: 7, StartDate: "2026-06-01", EndDate: "2026-07-01", PatternName: "feature_branch"}); err != nil {
		t.Fatalf("ListTrendTopMovers err=%v", err)
	}
	if patterns, err := service.ListTagPatterns(ctx, 0); err != nil || len(patterns) != 1 {
		t.Fatalf("ListTagPatterns patterns=%+v err=%v", patterns, err)
	}
	if pattern, err := service.UpsertTagPattern(ctx, TagPatternRequest{Name: "semver", TagRegex: "^v"}); err != nil || pattern.Name != "semver" {
		t.Fatalf("UpsertTagPattern pattern=%+v err=%v", pattern, err)
	}
	if run, err := service.EnqueueTagReclassificationRun(ctx, TagReclassificationRequest{NexusInstanceID: 7, RepositoryName: "docker-hosted"}); err != nil || run.ScanType != ScanTypeTagReclassification {
		t.Fatalf("EnqueueTagReclassificationRun run=%+v err=%v", run, err)
	}
	if _, err := service.ListTagPatternAnalytics(ctx, TagPatternListRequest{OlderThanDays: 30, NeverDownloaded: true}); err != nil {
		t.Fatalf("ListTagPatternAnalytics err=%v", err)
	}
	if _, err := service.ListTagPatternGrowth(ctx, TagPatternListRequest{GrowthWindowDays: 14, NeverDownloaded: true}); err != nil {
		t.Fatalf("ListTagPatternGrowth err=%v", err)
	}
	if _, err := service.ListTagPatternExamples(ctx, TagPatternListRequest{PatternName: "semver"}); err != nil {
		t.Fatalf("ListTagPatternExamples err=%v", err)
	}
	if _, err := service.ListTagPatternSuggestions(ctx, TagPatternListRequest{RepositoryName: "docker-hosted"}); err != nil {
		t.Fatalf("ListTagPatternSuggestions err=%v", err)
	}
	if run, err := service.CancelRun(ctx, 42); err != nil || run.Status != "canceled" {
		t.Fatalf("CancelRun run=%+v err=%v", run, err)
	}
	if _, err := service.ListVerificationFindings(ctx, 7, "", 999); err != nil {
		t.Fatalf("ListVerificationFindings err=%v", err)
	}
	if finding, err := service.DismissVerificationFinding(ctx, 17, "admin"); err != nil || finding.ID != 17 {
		t.Fatalf("DismissVerificationFinding finding=%+v err=%v", finding, err)
	}
	if result, err := service.ApplyRetention(ctx, RetentionSettings{}, now); err != nil || result.DeletedScanRuns != 2 || result.DeletedTrendSnapshots != 2 {
		t.Fatalf("ApplyRetention result=%+v err=%v", result, err)
	}
	if control, err := service.PauseNexusIndexing(ctx, 7, "maintenance", "admin"); err != nil || !control.Paused {
		t.Fatalf("PauseNexusIndexing control=%+v err=%v", control, err)
	}
	if control, err := service.ResumeNexusIndexing(ctx, 7); err != nil || control.NexusInstanceID != 7 {
		t.Fatalf("ResumeNexusIndexing control=%+v err=%v", control, err)
	}
}

func TestWorkerDatabaseBackedHelpersWithFakeDB(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	fake := &fakeDBTX{}
	fake.execFunc = func(query string, args []any) (pgconn.CommandTag, error) {
		switch {
		case strings.Contains(query, "DeleteTrendSnapshotsBefore"):
			return pgconn.NewCommandTag("DELETE 2"), nil
		case strings.Contains(query, "LinkDockerManifestLayer"):
			return pgconn.NewCommandTag("INSERT 0 1"), nil
		default:
			return pgconn.NewCommandTag("UPDATE 1"), nil
		}
	}
	fake.rowFunc = func(query string, args []any) fakeRow {
		switch {
		case strings.Contains(query, "GetNexusIndexingControl"):
			return fakeRow{values: indexingControlValues(7, now)}
		case strings.Contains(query, "ClassifyActiveTags"):
			return fakeRow{values: []any{int64(4)}}
		case strings.Contains(query, "UpsertDockerManifest"):
			return fakeRow{values: dockerManifestValues(101, 1, args[1].(string), now)}
		case strings.Contains(query, "UpsertDockerLayer"):
			return fakeRow{values: dockerLayerValues(201, 1, args[1].(string), now)}
		default:
			return fakeRow{err: fmt.Errorf("unexpected query row: %s", queryName(query))}
		}
	}
	fake.rowsFunc = func(query string, args []any) (*fakeRows, error) {
		switch {
		case strings.Contains(query, "CaptureDailyTrendSnapshots"),
			strings.Contains(query, "ListMissingDigestVerificationCandidates"),
			strings.Contains(query, "ListMissingManifestVerificationCandidates"):
			return &fakeRows{}, nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", queryName(query))
		}
	}
	worker := NewWorker(db.New(fake), slog.New(slog.NewTextHandler(io.Discard, nil)), WithWorkerID("worker-test"))
	ctx := context.Background()

	if err := worker.waitForRateLimit(ctx, db.ScanTask{NexusInstanceID: 7, CostClass: "asset_list"}); err != nil {
		t.Fatal(err)
	}
	if taskErr := worker.runTrendSnapshot(ctx, db.ScanTask{
		ID:              1,
		ScanRunID:       42,
		NexusInstanceID: 7,
		TaskType:        TaskTypeTrendSnapshot,
		Payload:         []byte(`{"snapshot_date":"2026-07-01","retention_days":30}`),
	}); taskErr != nil {
		t.Fatalf("runTrendSnapshot taskErr = %+v", taskErr)
	}
	if taskErr := worker.runVerificationChecks(ctx, db.ScanTask{
		ID:              2,
		ScanRunID:       42,
		NexusInstanceID: 7,
		TaskType:        TaskTypeVerificationChecks,
		CostClass:       "verification",
		Payload:         []byte(`{"scope":{"repositories":["docker-hosted"]}}`),
	}); taskErr != nil {
		t.Fatalf("runVerificationChecks taskErr = %+v", taskErr)
	}
	if classified, taskErr := worker.classifyActiveTags(ctx, 7, 1, "docker-hosted"); taskErr != nil || classified != 4 {
		t.Fatalf("classifyActiveTags classified=%d taskErr=%+v", classified, taskErr)
	}
	if taskErr := worker.runTagReclassification(ctx, db.ScanTask{
		ID:              3,
		ScanRunID:       42,
		NexusInstanceID: 7,
		TaskType:        TaskTypeTagReclassification,
		Payload:         []byte(`{"repository_name":"docker-hosted"}`),
	}); taskErr != nil {
		t.Fatalf("runTagReclassification taskErr = %+v", taskErr)
	}
	manifestCount, layerCount, taskErr := worker.storeManifestDocument(ctx, 1, registry.ManifestDocument{
		Digest:    "sha256:manifest",
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Config:    &registry.Descriptor{Digest: "sha256:config", Size: 10},
		Layers: []registry.Descriptor{
			{Digest: "sha256:layer-a", MediaType: "application/vnd.oci.image.layer.v1.tar+gzip", Size: 20},
			{Digest: "sha256:layer-b", MediaType: "application/vnd.oci.image.layer.v1.tar+gzip", Size: 30},
		},
	}, true)
	if taskErr != nil || manifestCount != 1 || layerCount != 2 {
		t.Fatalf("storeManifestDocument manifests=%d layers=%d taskErr=%+v", manifestCount, layerCount, taskErr)
	}
}

func TestWorkerCompleteFailAndRefreshTaskLifecycle(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	fake := &fakeDBTX{}
	fake.execFunc = func(query string, args []any) (pgconn.CommandTag, error) {
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	fake.rowFunc = func(query string, args []any) fakeRow {
		switch {
		case strings.Contains(query, "CreateScanTaskAttempt"):
			return fakeRow{values: scanTaskAttemptValues(1001, args[0].(int64), now)}
		case strings.Contains(query, "CompleteScanTask"):
			return fakeRow{values: scanTaskValues(args[0].(int64), 42, 7, TaskTypeNoop, "noop", TaskStatusSucceeded, now)}
		case strings.Contains(query, "RetryScanTask"):
			return fakeRow{values: scanTaskValues(args[0].(int64), 42, 7, TaskTypeNoop, "noop", TaskStatusRetryable, now)}
		case strings.Contains(query, "FailScanTask"):
			return fakeRow{values: scanTaskValues(args[0].(int64), 42, 7, TaskTypeNoop, "noop", TaskStatusFailed, now)}
		case strings.Contains(query, "RefreshScanRunTaskCounters"):
			return fakeRow{values: scanRunValues(args[0].(int64), 7, ScanTypeRecentSync, "succeeded", "finished", now)}
		case strings.Contains(query, "RecordNexusIndexingSuccess"),
			strings.Contains(query, "RecordNexusIndexingFailure"):
			return fakeRow{values: indexingControlValues(7, now)}
		default:
			return fakeRow{err: fmt.Errorf("unexpected query row: %s", queryName(query))}
		}
	}
	worker := NewWorker(db.New(fake), slog.New(slog.NewTextHandler(io.Discard, nil)), WithWorkerID("worker-test"))
	ctx := context.Background()
	baseTask := db.ScanTask{
		ID:              99,
		ScanRunID:       42,
		NexusInstanceID: 7,
		TaskType:        TaskTypeNoop,
		TaskKey:         "noop",
		Attempt:         1,
		MaxAttempts:     3,
		Payload:         emptyJSON,
	}

	if err := worker.completeTask(ctx, baseTask, 1001); err != nil {
		t.Fatal(err)
	}
	if err := worker.failTask(ctx, baseTask, 1002, &TaskError{Code: "nexus_unavailable", Class: "nexus_unavailable", Message: "upstream unavailable", Retryable: true}); err != nil {
		t.Fatal(err)
	}
	lastAttempt := baseTask
	lastAttempt.Attempt = 3
	if err := worker.failTask(ctx, lastAttempt, 1003, &TaskError{Code: "auth_failed", Class: "auth", Message: "denied", Retryable: false}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"CompleteScanTask",
		"RetryScanTask",
		"FailScanTask",
		"FinishScanTaskAttempt",
		"RefreshScanRunTaskCounters",
		"ReleaseOperationLockByRun",
		"RecordNexusIndexingSuccess",
		"RecordNexusIndexingFailure",
	} {
		if !calledQuery(fake, want) {
			t.Fatalf("expected %s to be called; queries=%v", want, queryNames(fake.queries))
		}
	}
}

func TestWorkerScanPackageAssetsUsesNexusAssetsAPIAndIndexesGenericArtifacts(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	var requestedTokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/service/rest/v1/assets" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("repository"); got != "npm-hosted" {
			t.Fatalf("repository = %q", got)
		}
		token := r.URL.Query().Get("continuationToken")
		requestedTokens = append(requestedTokens, token)
		switch token {
		case "":
			_ = json.NewEncoder(w).Encode(nexus.AssetsPage{
				ContinuationToken: "next",
				Items: []nexus.Asset{{
					ID:           "asset-recent",
					Repository:   "npm-hosted",
					Format:       "npm",
					Path:         "left-pad/-/left-pad-1.3.0.tgz",
					DownloadURL:  "http://nexus/repository/npm-hosted/left-pad/-/left-pad-1.3.0.tgz",
					ContentType:  "application/octet-stream",
					FileSize:     123,
					LastModified: "2026-06-20T00:00:00Z",
					Checksum:     nexus.AssetChecksum{SHA1: "sha1", SHA256: "sha256:recent"},
				}},
			})
		case "next":
			_ = json.NewEncoder(w).Encode(nexus.AssetsPage{
				Items: []nexus.Asset{{
					ID:           "asset-old",
					Repository:   "npm-hosted",
					Format:       "npm",
					Path:         "old-pkg/-/old-pkg-0.1.0.tgz",
					FileSize:     456,
					LastModified: "2026-04-01T00:00:00Z",
					Checksum:     nexus.AssetChecksum{SHA256: "sha256:old"},
				}},
			})
		default:
			t.Fatalf("unexpected continuation token %q", token)
		}
	}))
	defer server.Close()

	fake := &fakeDBTX{}
	var genericArtifactUpserts int
	var nexusAssetUpserts int
	fake.rowFunc = func(query string, args []any) fakeRow {
		switch {
		case strings.Contains(query, "GetNexusIndexingControl"):
			return fakeRow{values: indexingControlValues(7, now)}
		case strings.Contains(query, "UpsertNexusAsset"):
			nexusAssetUpserts++
			return fakeRow{values: nexusAssetValues(int64(600+nexusAssetUpserts), 301, now)}
		case strings.Contains(query, "UpsertGenericArtifact"):
			genericArtifactUpserts++
			if args[3] != "left-pad" || args[4] != pgTextIndex("1.3.0") {
				t.Fatalf("generic artifact args = %+v", args)
			}
			return fakeRow{values: genericArtifactValues(500, 301, "npm", "left-pad", "1.3.0", now)}
		default:
			return fakeRow{err: fmt.Errorf("unexpected query row: %s", queryName(query))}
		}
	}
	worker := NewWorker(db.New(fake), slog.New(slog.NewTextHandler(io.Discard, nil)), WithWorkerID("worker-test"))
	taskErr := worker.scanPackageAssets(context.Background(), db.ScanTask{
		ID:              1,
		ScanRunID:       42,
		NexusInstanceID: 7,
		TaskType:        TaskTypePackageAssetScan,
		CostClass:       "asset_list",
		Payload: jsonBytes(map[string]any{
			"base_url":        server.URL,
			"repository_id":   301,
			"repository_name": "npm-hosted",
			"format":          "npm",
			"scope":           taskScope{Since: "2026-06-01T00:00:00Z"},
		}),
	})
	if taskErr != nil {
		t.Fatalf("scanPackageAssets taskErr = %+v", taskErr)
	}
	if got := strings.Join(requestedTokens, ","); got != ",next" {
		t.Fatalf("requested tokens = %q", got)
	}
	if nexusAssetUpserts != 2 || genericArtifactUpserts != 1 {
		t.Fatalf("nexusAssetUpserts=%d genericArtifactUpserts=%d", nexusAssetUpserts, genericArtifactUpserts)
	}
}

func TestWorkerScanDockerTagsIndexesTagsDeletesMissingAndEnqueuesManifestFetch(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/service/rest/v1/assets" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("repository"); got != "docker-hosted" {
			t.Fatalf("repository = %q", got)
		}
		_ = json.NewEncoder(w).Encode(nexus.AssetsPage{
			Items: []nexus.Asset{
				{
					ID:           "asset-latest",
					Repository:   "docker-hosted",
					Format:       "docker",
					Path:         "/v2/demo/app/manifests/latest",
					ContentType:  "application/vnd.oci.image.manifest.v1+json",
					FileSize:     1000,
					LastModified: "2026-06-20T00:00:00Z",
					Checksum:     nexus.AssetChecksum{SHA256: "abc"},
				},
				{
					ID:           "asset-blob",
					Repository:   "docker-hosted",
					Format:       "docker",
					Path:         "/v2/demo/app/blobs/sha256:layer",
					FileSize:     2000,
					LastModified: "2026-06-20T00:00:00Z",
				},
			},
		})
	}))
	defer server.Close()

	fake := &fakeDBTX{}
	var tagUpserts int
	var taskEnqueues int
	fake.rowFunc = func(query string, args []any) fakeRow {
		switch {
		case strings.Contains(query, "GetNexusIndexingControl"):
			return fakeRow{values: indexingControlValues(7, now)}
		case strings.Contains(query, "UpsertNexusAsset"):
			return fakeRow{values: nexusAssetValues(601, 301, now)}
		case strings.Contains(query, "UpsertContainerImage"):
			return fakeRow{values: []any{int64(201), int64(301), "demo/app", pgTimeIndex(now), pgTimeIndex(now)}}
		case strings.Contains(query, "UpsertContainerTag"):
			tagUpserts++
			if args[1] != "latest" || args[2] != "sha256:abc" {
				t.Fatalf("tag args = %+v", args)
			}
			return fakeRow{values: []any{int64(101), int64(201), "latest", "sha256:abc", "application/vnd.oci.image.manifest.v1+json", int64(1000), pgTimeIndex(now), pgTimeIndex(now), pgtype.Timestamptz{}}}
		case strings.Contains(query, "GetScanRun"):
			return fakeRow{values: scanRunValues(42, 7, ScanTypeFullReconcile, "running", "queued", now)}
		case strings.Contains(query, "ClassifyActiveTags"):
			return fakeRow{values: []any{int64(1)}}
		case strings.Contains(query, "EnqueueScanTask"):
			taskEnqueues++
			if args[2] != TaskTypeManifestFetchBatch {
				t.Fatalf("enqueue args = %+v", args)
			}
			return fakeRow{values: scanTaskValues(99, 42, 7, TaskTypeManifestFetchBatch, "manifest-fetch/docker-hosted", "pending", now)}
		default:
			return fakeRow{err: fmt.Errorf("unexpected query row: %s", queryName(query))}
		}
	}
	fake.rowsFunc = func(query string, args []any) (*fakeRows, error) {
		switch {
		case strings.Contains(query, "MarkMissingContainerTagsDeletedForRepository"):
			return &fakeRows{rows: [][]any{{int64(102), int64(201), "old", "sha256:old", "media", int64(1), pgTimeIndex(now), pgTimeIndex(now), pgTimeIndex(now)}}}, nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", queryName(query))
		}
	}
	worker := NewWorker(db.New(fake), slog.New(slog.NewTextHandler(io.Discard, nil)), WithWorkerID("worker-test"))
	taskErr := worker.scanDockerTags(context.Background(), db.ScanTask{
		ID:              1,
		ScanRunID:       42,
		NexusInstanceID: 7,
		TaskType:        TaskTypeDockerTagScan,
		CostClass:       "asset_list",
		Payload: jsonBytes(map[string]any{
			"base_url":        server.URL,
			"repository_id":   301,
			"repository_name": "docker-hosted",
			"repository_url":  "http://registry/repository/docker-hosted",
		}),
	})
	if taskErr != nil {
		t.Fatalf("scanDockerTags taskErr = %+v", taskErr)
	}
	if tagUpserts != 1 || taskEnqueues != 1 {
		t.Fatalf("tagUpserts=%d taskEnqueues=%d", tagUpserts, taskEnqueues)
	}
}

func TestWorkerDiscoverRepositoriesFiltersHostedReposAndEnqueuesScans(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	online := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/service/rest/v1/repositories" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]nexus.Repository{
			{Name: "docker-hosted", Format: "docker", Type: "hosted", URL: "http://nexus/repository/docker-hosted", Online: &online},
			{Name: "npm-hosted", Format: "npm", Type: "hosted", URL: "http://nexus/repository/npm-hosted", Online: &online},
			{Name: "docker-proxy", Format: "docker", Type: "proxy", URL: "http://nexus/repository/docker-proxy", Online: &online},
		})
	}))
	defer server.Close()

	fake := &fakeDBTX{}
	var upsertedRepos []string
	var taskTypes []string
	fake.rowFunc = func(query string, args []any) fakeRow {
		switch {
		case strings.Contains(query, "GetNexusIndexingControl"):
			return fakeRow{values: indexingControlValues(7, now)}
		case strings.Contains(query, "UpsertRepositoryFromDiscovery"):
			name := args[1].(string)
			upsertedRepos = append(upsertedRepos, name)
			return fakeRow{values: repositoryValues(int64(300+len(upsertedRepos)), 7, name, args[2].(string), args[3].(string), now)}
		case strings.Contains(query, "EnqueueScanTask"):
			taskTypes = append(taskTypes, args[2].(string))
			return fakeRow{values: scanTaskValues(int64(90+len(taskTypes)), 42, 7, args[2].(string), args[3].(string), "pending", now)}
		default:
			return fakeRow{err: fmt.Errorf("unexpected query row: %s", queryName(query))}
		}
	}
	worker := NewWorker(db.New(fake), slog.New(slog.NewTextHandler(io.Discard, nil)), WithWorkerID("worker-test"))
	taskErr := worker.discoverRepositories(context.Background(), db.ScanTask{
		ID:              1,
		ScanRunID:       42,
		NexusInstanceID: 7,
		TaskType:        TaskTypeRepositoryDiscovery,
		CostClass:       "asset_list",
		Payload: jsonBytes(map[string]any{
			"base_url": server.URL,
			"scope":    taskScope{Repositories: []string{"docker-hosted", "npm-hosted", "docker-proxy"}},
		}),
	})
	if taskErr != nil {
		t.Fatalf("discoverRepositories taskErr = %+v", taskErr)
	}
	if strings.Join(upsertedRepos, ",") != "docker-hosted,npm-hosted" {
		t.Fatalf("upsertedRepos = %+v", upsertedRepos)
	}
	if strings.Join(taskTypes, ",") != TaskTypeDockerTagScan+","+TaskTypePackageAssetScan {
		t.Fatalf("taskTypes = %+v", taskTypes)
	}
}

func TestWorkerFetchManifestBatchStoresManifestsAndLayers(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repository/docker-hosted/v2/demo/app/manifests/latest" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Docker-Content-Digest", "sha256:manifest")
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schemaVersion": 2,
			"mediaType":     "application/vnd.oci.image.manifest.v1+json",
			"config": map[string]any{
				"mediaType": "application/vnd.oci.image.config.v1+json",
				"digest":    "sha256:config",
				"size":      321,
			},
			"layers": []map[string]any{{
				"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
				"digest":    "sha256:layer",
				"size":      1234,
			}},
		})
	}))
	defer server.Close()

	fake := &fakeDBTX{}
	var manifests int
	var layers int
	var links int
	fake.execFunc = func(query string, args []any) (pgconn.CommandTag, error) {
		if strings.Contains(query, "LinkDockerManifestLayer") {
			links++
		}
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	fake.rowFunc = func(query string, args []any) fakeRow {
		switch {
		case strings.Contains(query, "GetNexusIndexingControl"):
			return fakeRow{values: indexingControlValues(7, now)}
		case strings.Contains(query, "UpsertDockerManifest"):
			manifests++
			return fakeRow{values: dockerManifestValues(int64(100+manifests), 301, args[1].(string), now)}
		case strings.Contains(query, "UpsertDockerLayer"):
			layers++
			return fakeRow{values: dockerLayerValues(int64(200+layers), 301, args[1].(string), now)}
		default:
			return fakeRow{err: fmt.Errorf("unexpected query row: %s", queryName(query))}
		}
	}
	fake.rowsFunc = func(query string, args []any) (*fakeRows, error) {
		switch {
		case strings.Contains(query, "ListActiveTagsForRepository"):
			return &fakeRows{rows: [][]any{{int64(301), pgTextIndex(server.URL + "/repository/docker-hosted"), "demo/app", "latest", "sha256:manifest", "application/vnd.oci.image.manifest.v1+json"}}}, nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", queryName(query))
		}
	}
	worker := NewWorker(db.New(fake), slog.New(slog.NewTextHandler(io.Discard, nil)), WithWorkerID("worker-test"))
	taskErr := worker.fetchManifestBatch(context.Background(), db.ScanTask{
		ID:              1,
		ScanRunID:       42,
		NexusInstanceID: 7,
		TaskType:        TaskTypeManifestFetchBatch,
		CostClass:       "registry_manifest",
		Payload: jsonBytes(map[string]any{
			"repository_id":   301,
			"repository_name": "docker-hosted",
			"repository_url":  server.URL + "/repository/docker-hosted",
		}),
	})
	if taskErr != nil {
		t.Fatalf("fetchManifestBatch taskErr = %+v", taskErr)
	}
	if manifests != 1 || layers != 1 || links != 1 {
		t.Fatalf("manifests=%d layers=%d links=%d", manifests, layers, links)
	}
}

func TestServiceProcessDueSchedulesEnqueuesAndTicksSchedule(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	fake := &fakeDBTX{}
	fake.execFunc = func(query string, args []any) (pgconn.CommandTag, error) {
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	fake.rowFunc = func(query string, args []any) fakeRow {
		switch {
		case strings.Contains(query, "TryAcquireSchedulerLock"):
			return fakeRow{values: []any{true}}
		case strings.Contains(query, "GetOperationLock"),
			strings.Contains(query, "GetCleanupLock"):
			return fakeRow{err: pgx.ErrNoRows}
		case strings.Contains(query, "UpsertNexusInstanceForIndexing"):
			return fakeRow{values: []any{int64(7), "local", "http://nexus:8081", pgTimeIndex(now), pgTimeIndex(now)}}
		case strings.Contains(query, "CreateScanRun"):
			return fakeRow{values: scanRunValues(42, 7, ScanTypeRecentSync, "running", "queued", now)}
		case strings.Contains(query, "EnqueueScanTask"):
			return fakeRow{values: scanTaskValues(99, 42, 7, TaskTypeRepositoryDiscovery, "schedule/recent-sync/123", "pending", now)}
		case strings.Contains(query, "MarkIndexingScheduleTicked"):
			return fakeRow{values: indexingScheduleValues(3, 7, now)}
		default:
			return fakeRow{err: fmt.Errorf("unexpected query row: %s", queryName(query))}
		}
	}
	fake.rowsFunc = func(query string, args []any) (*fakeRows, error) {
		switch {
		case strings.Contains(query, "ListDueIndexingSchedules"):
			return &fakeRows{rows: [][]any{append(indexingScheduleValues(3, 7, now), "local", "http://nexus:8081")}}, nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", queryName(query))
		}
	}
	service := NewService(db.New(fake), slog.New(slog.NewTextHandler(io.Discard, nil)))

	result, err := service.ProcessDueSchedules(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Processed != 1 || result.Enqueued != 1 || result.Skipped != 0 {
		t.Fatalf("result = %+v", result)
	}
	for _, want := range []string{"TryAcquireSchedulerLock", "ListDueIndexingSchedules", "EnqueueScanTask", "MarkIndexingScheduleTicked", "ReleaseSchedulerLock"} {
		if !calledQuery(fake, want) {
			t.Fatalf("expected %s to be called; queries=%v", want, queryNames(fake.queries))
		}
	}
}

func TestCleanupServiceAPIDrivenFlowsWithFakeDB(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	policy := `
name: ci-cleanup
candidate_rules:
  - name: old features
    patterns: [feature_branch]
    tag_prefixes: ["feature-"]
    older_than_days: 30
    never_downloaded: true
`
	fake := &fakeDBTX{}
	fake.rowFunc = func(query string, args []any) fakeRow {
		switch {
		case strings.Contains(query, "CreateCleanupExecutionEvent"):
			return fakeRow{values: cleanupEventValues(21, 11, "approval_recorded", now)}
		case strings.Contains(query, "CreateCleanupExecution"):
			return fakeRow{values: cleanupExecutionValues(11, 7, "ci-cleanup", "blocked_read_only", false, now)}
		case strings.Contains(query, "RequestCleanupExecutionCancel"):
			return fakeRow{values: cleanupExecutionValues(11, 7, "ci-cleanup", "cancel_requested", false, now)}
		case strings.Contains(query, "FinishCleanupExecution"):
			return fakeRow{values: cleanupExecutionValues(11, 7, "ci-cleanup", args[0].(string), false, now)}
		default:
			return fakeRow{err: fmt.Errorf("unexpected query row: %s", queryName(query))}
		}
	}
	fake.rowsFunc = func(query string, args []any) (*fakeRows, error) {
		switch {
		case strings.Contains(query, "ListCleanupSimulationTags"):
			return &fakeRows{rows: [][]any{cleanupSimulationTagValues(now.AddDate(0, 0, -45), nil)}}, nil
		case strings.Contains(query, "ListCleanupExecutionEvents"):
			return &fakeRows{rows: [][]any{cleanupEventValues(21, 11, "approval_recorded", now)}}, nil
		case strings.Contains(query, "ListCleanupExecutions"):
			return &fakeRows{rows: [][]any{cleanupExecutionValues(11, 7, "ci-cleanup", "blocked_read_only", false, now)}}, nil
		default:
			return nil, fmt.Errorf("unexpected query: %s", queryName(query))
		}
	}
	service := NewService(db.New(fake), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()

	simulation, err := service.SimulateCleanupPolicy(ctx, CleanupSimulationRequest{
		NexusInstanceID: 7,
		RepositoryName:  "docker-hosted",
		PolicyYAML:      policy,
		Limit:           25,
	})
	if err != nil {
		t.Fatal(err)
	}
	if simulation.PolicyName != "ci-cleanup" || simulation.CandidateTagCount != 1 || simulation.WarningCount != 0 {
		t.Fatalf("simulation = %+v", simulation)
	}
	execution, err := service.RequestCleanupExecution(ctx, CleanupExecutionRequest{
		NexusInstanceID: 7,
		PolicyYAML:      policy,
		ApprovalText:    "ci-cleanup",
		RequestedBy:     "admin",
		Limit:           25,
	})
	if err != nil {
		t.Fatal(err)
	}
	if execution.Status != "blocked_read_only" || execution.PolicyName != "ci-cleanup" {
		t.Fatalf("execution = %+v", execution)
	}
	executions, err := service.ListCleanupExecutions(ctx, 7, 500)
	if err != nil || len(executions) != 1 {
		t.Fatalf("executions=%+v err=%v", executions, err)
	}
	events, err := service.ListCleanupExecutionEvents(ctx, 11, 5000)
	if err != nil || len(events) != 1 || events[0].EventType != "approval_recorded" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	canceled, err := service.CancelCleanupExecution(ctx, 11, "")
	if err != nil || canceled.Status != "cancel_requested" {
		t.Fatalf("canceled=%+v err=%v", canceled, err)
	}
	finished, err := service.finishCleanupExecution(ctx, 11, "succeeded_noop", nil, cleanupExecutionStats{}, pgtype.Int8{}, pgtype.Int8{})
	if err != nil || finished.Status != "succeeded_noop" {
		t.Fatalf("finished=%+v err=%v", finished, err)
	}
	if _, err := service.createCleanupEvent(ctx, 11, cleanupExecutionEventInput{EventType: "manual_note", Actor: "admin"}); err != nil {
		t.Fatal(err)
	}
}

func calledQuery(fake *fakeDBTX, name string) bool {
	for _, query := range fake.queries {
		if strings.Contains(query, name) {
			return true
		}
	}
	return false
}

func queryNames(queries []string) []string {
	out := make([]string, 0, len(queries))
	for _, query := range queries {
		out = append(out, queryName(query))
	}
	return out
}

func pgTimeIndex(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func pgTextIndex(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: true}
}

func scanRunValues(id, nexusID int64, scanType, status, phase string, now time.Time) []any {
	return []any{
		id,
		nexusID,
		scanType,
		pgtype.Text{},
		TriggerSourceManual,
		pgtype.Text{},
		status,
		pgTextIndex(phase),
		pgTimeIndex(now),
		pgtype.Timestamptz{},
		pgTimeIndex(now),
		pgtype.Int8{Int64: 1, Valid: true},
		int64(0),
		int64(0),
		pgtype.Numeric{},
		"estimated",
		[]byte(`{}`),
		[]byte(`{}`),
		[]byte(`{}`),
		pgtype.Text{},
		pgtype.Text{},
		pgtype.Text{},
		[]byte(`{}`),
	}
}

func scanTaskValues(id, scanRunID, nexusID int64, taskType, taskKey, status string, now time.Time) []any {
	return []any{
		id,
		scanRunID,
		nexusID,
		taskType,
		taskKey,
		pgtype.Text{},
		pgtype.Text{},
		"asset_list",
		pgtype.Int4{},
		status,
		int32(0),
		int32(0),
		int32(3),
		pgTimeIndex(now),
		pgtype.Timestamptz{},
		pgtype.UUID{},
		pgtype.Text{},
		pgtype.Timestamptz{},
		pgtype.Timestamptz{},
		pgtype.Timestamptz{},
		pgTimeIndex(now),
		pgtype.Int8{},
		pgtype.Int8{},
		pgtype.Text{},
		[]byte(`{}`),
		[]byte(`{}`),
		[]byte(`{}`),
		pgtype.Text{},
		pgtype.Text{},
		pgtype.Text{},
		[]byte(`{}`),
		pgTimeIndex(now),
	}
}

func retryScanTaskRowValues(id, scanRunID, nexusID int64, now time.Time) []any {
	return scanTaskValues(id, scanRunID, nexusID, TaskTypeRepositoryDiscovery, "repository-discovery", "pending", now)
}

func scanTaskAttemptValues(id, taskID int64, now time.Time) []any {
	return []any{
		id,
		taskID,
		int32(1),
		pgTextIndex("worker-test"),
		pgTextIndex("worker-pod"),
		pgtype.UUID{},
		pgTimeIndex(now),
		pgtype.Timestamptz{},
		"running",
		pgtype.Text{},
		pgtype.Text{},
		pgtype.Text{},
		[]byte(`{}`),
		[]byte(`{}`),
	}
}

func nexusAssetValues(id, repositoryID int64, now time.Time) []any {
	return []any{
		id,
		repositoryID,
		pgTextIndex(fmt.Sprintf("asset-%d", id)),
		pgTextIndex("path"),
		pgTextIndex("http://nexus/download"),
		pgTextIndex("application/octet-stream"),
		pgtype.Int8{Int64: 123, Valid: true},
		pgTextIndex("default"),
		pgTextIndex("sha1"),
		pgTextIndex("sha256"),
		pgtype.Timestamptz{},
		pgtype.Timestamptz{},
		pgTimeIndex(now),
		pgTimeIndex(now),
		pgTimeIndex(now),
		[]byte(`{}`),
	}
}

func genericArtifactValues(id, repositoryID int64, format, name, version string, now time.Time) []any {
	return []any{
		id,
		repositoryID,
		format,
		pgTextIndex(""),
		name,
		pgTextIndex(version),
		pgTextIndex(name + "-" + version + ".tgz"),
		pgtype.Int8{Int64: 123, Valid: true},
		pgTextIndex("sha256"),
		pgTimeIndex(now),
		pgTimeIndex(now),
		[]byte(`{}`),
	}
}

func repositoryValues(id, nexusID int64, name, format, repoType string, now time.Time) []any {
	return []any{
		id,
		nexusID,
		name,
		format,
		repoType,
		pgTextIndex("http://nexus/repository/" + name),
		pgTimeIndex(now),
		pgTimeIndex(now),
		pgTextIndex("http://registry/repository/" + name),
		pgTextIndex("direct"),
		pgtype.Timestamptz{},
		pgTextIndex("default"),
		pgtype.Bool{Bool: true, Valid: true},
		[]byte(`{}`),
		pgTimeIndex(now),
		pgTimeIndex(now),
	}
}

func indexingScheduleValues(id, nexusID int64, now time.Time) []any {
	return []any{
		id,
		nexusID,
		ScanTypeRecentSync,
		"recent-sync",
		true,
		"every 1800s",
		int32(1800),
		"UTC",
		[]byte(`{"window_days":1}`),
		"skip",
		pgTimeIndex(now),
		pgtype.Timestamptz{},
		pgTimeIndex(now),
		pgTimeIndex(now),
	}
}

func indexingControlValues(nexusID int64, now time.Time) []any {
	return []any{
		nexusID,
		true,
		pgTextIndex("maintenance"),
		pgTimeIndex(now),
		pgTextIndex("admin"),
		int32(1),
		pgTimeIndex(now.Add(time.Minute)),
		pgTimeIndex(now),
		pgTextIndex("rate_limited"),
		pgTextIndex("nexus"),
		pgTimeIndex(now),
		pgtype.Numeric{},
		pgtype.Numeric{},
		pgtype.Numeric{},
	}
}

func retentionSettingValues(now time.Time) []any {
	return []any{
		true,
		pgtype.Int4{Int32: 3600, Valid: true},
		pgtype.Int4{Int32: 60, Valid: true},
		pgtype.Int4{Int32: 365, Valid: true},
		pgtype.Int4{Int32: 180, Valid: true},
		pgtype.Int4{Int32: 730, Valid: true},
		pgTextIndex("admin"),
		pgTimeIndex(now),
	}
}

func tagPatternValues(id int64, now time.Time) []any {
	return []any{
		id,
		"semver",
		"SemVer",
		"semantic versions",
		"^v?[0-9]+",
		int32(500),
		true,
		false,
		pgTimeIndex(now),
		pgTimeIndex(now),
	}
}

func verificationFindingValues(id int64, now time.Time) []any {
	return []any{
		id,
		pgtype.Int8{Int64: 7, Valid: true},
		pgtype.Int8{Int64: 42, Valid: true},
		"high",
		"docker_tag_digest_missing",
		pgTextIndex("docker-hosted"),
		pgTextIndex("demo/app:latest"),
		[]byte(`{"digest":"present"}`),
		[]byte(`{"digest":""}`),
		pgTextIndex("missing digest"),
		pgTextIndex("reindex"),
		"dismissed",
		pgTimeIndex(now),
		pgTimeIndex(now),
		pgtype.Timestamptz{},
		pgTimeIndex(now),
		[]byte(`{"dismissed_by":"admin"}`),
	}
}

func dockerManifestValues(id, repositoryID int64, digest string, now time.Time) []any {
	return []any{
		id,
		repositoryID,
		digest,
		pgTextIndex("application/vnd.oci.image.manifest.v1+json"),
		pgTextIndex("sha256:config"),
		pgtype.Int8{Int64: 60, Valid: true},
		pgTimeIndex(now),
		pgTimeIndex(now),
		[]byte(`{}`),
	}
}

func dockerLayerValues(id, repositoryID int64, digest string, now time.Time) []any {
	return []any{
		id,
		repositoryID,
		digest,
		pgTextIndex("application/vnd.oci.image.layer.v1.tar+gzip"),
		pgtype.Int8{Int64: 20, Valid: true},
		pgTimeIndex(now),
		pgTimeIndex(now),
	}
}

func cleanupSimulationTagValues(firstSeen time.Time, lastDownloaded any) []any {
	return []any{
		int64(101),
		int64(201),
		int64(301),
		int64(7),
		"local",
		"docker-hosted",
		"demo/app",
		"feature-x",
		"sha256:abc",
		"feature_branch",
		int64(1000),
		int64(800),
		int64(500),
		int64(300),
		pgTimeIndex(firstSeen),
		pgTimeIndex(firstSeen.Add(time.Hour)),
		lastDownloaded,
		false,
		false,
	}
}

func cleanupExecutionValues(id, nexusID int64, policyName, status string, destructive bool, now time.Time) []any {
	return []any{
		id,
		nexusID,
		pgTextIndex("docker-hosted"),
		policyName,
		"name: ci-cleanup",
		status,
		"admin",
		pgTextIndex("admin"),
		policyName,
		destructive,
		[]byte(`{"candidate_tag_count":1}`),
		[]byte(`[{"event":"approval_recorded"}]`),
		pgtype.Int8{},
		pgTimeIndex(now),
		pgTimeIndex(now),
		pgtype.Timestamptz{},
		[]byte(`[{"tag_id":101}]`),
		int64(1),
		pgtype.Timestamptz{},
		pgtype.Timestamptz{},
		pgtype.Text{},
		pgtype.Timestamptz{},
		pgtype.Text{},
		pgtype.Int8{},
	}
}

func cleanupEventValues(id, executionID int64, eventType string, now time.Time) []any {
	return []any{
		id,
		executionID,
		eventType,
		pgTextIndex("admin"),
		pgTextIndex("docker-hosted"),
		pgTextIndex("demo/app"),
		pgTextIndex("feature-x"),
		pgtype.Int8{Int64: 101, Valid: true},
		pgTextIndex("old features"),
		pgTextIndex("component-1"),
		pgTextIndex("blocked_read_only"),
		pgTextIndex("approval recorded"),
		pgtype.Text{},
		pgtype.Text{},
		pgtype.Text{},
		[]byte(`{"ok":true}`),
		pgTimeIndex(now),
	}
}
