package db

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeDB struct {
	rowFunc  func(query string, args []any) fakeRow
	rowsFunc func(query string, args []any) (*fakeRows, error)
	execFunc func(query string, args []any) (pgconn.CommandTag, error)
}

func (f *fakeDB) Exec(_ context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	if f.execFunc != nil {
		return f.execFunc(query, args)
	}
	return pgconn.NewCommandTag("DELETE 1"), nil
}

func (f *fakeDB) Query(_ context.Context, query string, args ...interface{}) (pgx.Rows, error) {
	if f.rowsFunc == nil {
		return nil, fmt.Errorf("unexpected query: %s", dbQueryName(query))
	}
	return f.rowsFunc(query, args)
}

func (f *fakeDB) QueryRow(_ context.Context, query string, args ...interface{}) pgx.Row {
	if f.rowFunc == nil {
		return fakeRow{err: fmt.Errorf("unexpected query row: %s", dbQueryName(query))}
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
		if err := assignDBScanValue(dest[i], r.values[i]); err != nil {
			return fmt.Errorf("scan value %d: %w", i, err)
		}
	}
	return nil
}

type fakeRows struct {
	rows  [][]any
	index int
	err   error
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return r.err }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.NewCommandTag("SELECT 1") }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeRows) Next() bool {
	if r.index >= len(r.rows) {
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

func assignDBScanValue(dest any, value any) error {
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

func dbQueryName(query string) string {
	line, _, _ := strings.Cut(query, "\n")
	return strings.TrimPrefix(line, "-- name: ")
}

func TestIndexingQueueQueryWrappers(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	q := New(&fakeDB{
		rowFunc: func(query string, args []any) fakeRow {
			switch {
			case strings.Contains(query, "CreateScanRun"),
				strings.Contains(query, "CancelScanRun"):
				return fakeRow{values: dbScanRunValues(42, 7, "recent_sync", "running", now)}
			case strings.Contains(query, "EnqueueScanTask"),
				strings.Contains(query, "RetryScanTaskByID"):
				return fakeRow{values: dbScanTaskValues(99, 42, 7, "repository_discovery", now)}
			case strings.Contains(query, "GetOperationLock"):
				return fakeRow{values: []any{int64(7), int64(42), "full_reconcile", "exclusive", dbTime(now), dbTime(now.Add(time.Hour)), dbText("running"), "full_reconcile", "running"}}
			case strings.Contains(query, "UpsertIndexingSchedule"):
				return fakeRow{values: []any{int64(3), int64(7), "recent_sync", "recent-sync", true, "every 1800s", int32(1800), "UTC", []byte(`{}`), "skip", dbTime(now), pgtype.Timestamptz{}, dbTime(now), dbTime(now)}}
			case strings.Contains(query, "UpsertNexusInstanceForIndexing"):
				return fakeRow{values: []any{int64(7), "local", "http://nexus:8081", dbTime(now), dbTime(now)}}
			default:
				return fakeRow{err: fmt.Errorf("unexpected row query: %s", dbQueryName(query))}
			}
		},
		rowsFunc: func(query string, args []any) (*fakeRows, error) {
			switch {
			case strings.Contains(query, "ListRecentScanRuns"):
				return &fakeRows{rows: [][]any{dbScanRunValues(42, 7, "recent_sync", "running", now)}}, nil
			case strings.Contains(query, "ListRecentScanTasks"):
				return &fakeRows{rows: [][]any{dbScanTaskValues(99, 42, 7, "repository_discovery", now)}}, nil
			default:
				return nil, fmt.Errorf("unexpected query: %s", dbQueryName(query))
			}
		},
	})

	ctx := context.Background()
	if run, err := q.CreateScanRun(ctx, CreateScanRunParams{NexusInstanceID: 7, ScanType: "recent_sync", Stats: []byte(`{}`)}); err != nil || run.ID != 42 {
		t.Fatalf("CreateScanRun run=%+v err=%v", run, err)
	}
	if task, err := q.EnqueueScanTask(ctx, EnqueueScanTaskParams{ScanRunID: 42, NexusInstanceID: 7, TaskType: "repository_discovery", TaskKey: "repository-discovery", MaxAttempts: 3}); err != nil || task.ID != 99 {
		t.Fatalf("EnqueueScanTask task=%+v err=%v", task, err)
	}
	if lock, err := q.GetOperationLock(ctx, 7); err != nil || lock.ScanRunID != 42 {
		t.Fatalf("GetOperationLock lock=%+v err=%v", lock, err)
	}
	if rows, err := q.ListRecentScanRuns(ctx, 50); err != nil || len(rows) != 1 {
		t.Fatalf("ListRecentScanRuns rows=%+v err=%v", rows, err)
	}
	if rows, err := q.ListRecentScanTasks(ctx, ListRecentScanTasksParams{LimitCount: 50}); err != nil || len(rows) != 1 {
		t.Fatalf("ListRecentScanTasks rows=%+v err=%v", rows, err)
	}
	if task, err := q.RetryScanTaskByID(ctx, 99); err != nil || task.ID != 99 {
		t.Fatalf("RetryScanTaskByID task=%+v err=%v", task, err)
	}
	if schedule, err := q.UpsertIndexingSchedule(ctx, UpsertIndexingScheduleParams{NexusInstanceID: 7, ScanType: "recent_sync", Name: "recent-sync"}); err != nil || schedule.ID != 3 {
		t.Fatalf("UpsertIndexingSchedule schedule=%+v err=%v", schedule, err)
	}
	if instance, err := q.UpsertNexusInstanceForIndexing(ctx, UpsertNexusInstanceForIndexingParams{Name: "local", BaseUrl: "http://nexus:8081"}); err != nil || instance.ID != 7 {
		t.Fatalf("UpsertNexusInstanceForIndexing instance=%+v err=%v", instance, err)
	}
}

func TestAnalyticsTagPatternCleanupAndMetricsQueryWrappers(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	q := New(&fakeDB{
		rowFunc: func(query string, args []any) fakeRow {
			switch {
			case strings.Contains(query, "GetAnalyticsOverview"):
				return fakeRow{values: []any{int64(1), int64(2), int64(3), int64(4), int64(5), int64(6), int64(7), int64(8), now}}
			case strings.Contains(query, "ClassifyActiveTags"):
				return fakeRow{values: []any{int64(4)}}
			case strings.Contains(query, "UpsertTagPattern"):
				return fakeRow{values: dbTagPatternValues(1, now)}
			case strings.Contains(query, "CreateCleanupExecutionEvent"):
				return fakeRow{values: dbCleanupEventValues(21, 11, "approval_recorded", now)}
			case strings.Contains(query, "CreateCleanupExecution"),
				strings.Contains(query, "RequestCleanupExecutionCancel"):
				return fakeRow{values: dbCleanupExecutionValues(11, 7, "blocked_read_only", now)}
			case strings.Contains(query, "GetOldestPendingTaskAgeSeconds"),
				strings.Contains(query, "GetOldestRunningCleanupAgeSeconds"):
				return fakeRow{values: []any{int64(120)}}
			default:
				return fakeRow{err: fmt.Errorf("unexpected row query: %s", dbQueryName(query))}
			}
		},
		rowsFunc: func(query string, args []any) (*fakeRows, error) {
			switch {
			case strings.Contains(query, "ListCleanupSimulationTags"):
				return &fakeRows{rows: [][]any{dbCleanupSimulationTagValues(now)}}, nil
			case strings.Contains(query, "ListCleanupExecutionEvents"):
				return &fakeRows{rows: [][]any{dbCleanupEventValues(21, 11, "approval_recorded", now)}}, nil
			case strings.Contains(query, "ListCleanupExecutions"):
				return &fakeRows{rows: [][]any{dbCleanupExecutionValues(11, 7, "blocked_read_only", now)}}, nil
			case strings.Contains(query, "ListTagPatterns"):
				return &fakeRows{rows: [][]any{dbTagPatternValues(1, now)}}, nil
			case strings.Contains(query, "CountScanTasksByStatus"),
				strings.Contains(query, "CountScanRunsByStatus24h"),
				strings.Contains(query, "CountCleanupExecutionsByStatus"):
				return &fakeRows{rows: [][]any{{"running", int64(1)}}}, nil
			case strings.Contains(query, "CountOpenVerificationFindingsBySeverity"):
				return &fakeRows{rows: [][]any{{"high", int64(1)}}}, nil
			case strings.Contains(query, "CountCleanupExecutionEvents24h"):
				return &fakeRows{rows: [][]any{{"cleanup_started", dbText("running"), int64(1)}}}, nil
			default:
				return &fakeRows{}, nil
			}
		},
		execFunc: func(query string, args []any) (pgconn.CommandTag, error) {
			return pgconn.NewCommandTag("DELETE 2"), nil
		},
	})

	ctx := context.Background()
	if overview, err := q.GetAnalyticsOverview(ctx, pgtype.Int8{}); err != nil || overview.RepositoryCount != 1 {
		t.Fatalf("GetAnalyticsOverview overview=%+v err=%v", overview, err)
	}
	if count, err := q.ClassifyActiveTags(ctx, ClassifyActiveTagsParams{}); err != nil || count != 4 {
		t.Fatalf("ClassifyActiveTags count=%d err=%v", count, err)
	}
	if pattern, err := q.UpsertTagPattern(ctx, UpsertTagPatternParams{Name: "semver", DisplayName: "SemVer", TagRegex: "^v"}); err != nil || pattern.ID != 1 {
		t.Fatalf("UpsertTagPattern pattern=%+v err=%v", pattern, err)
	}
	if patterns, err := q.ListTagPatterns(ctx, 50); err != nil || len(patterns) != 1 {
		t.Fatalf("ListTagPatterns patterns=%+v err=%v", patterns, err)
	}
	if tags, err := q.ListCleanupSimulationTags(ctx, ListCleanupSimulationTagsParams{LimitCount: 10}); err != nil || len(tags) != 1 {
		t.Fatalf("ListCleanupSimulationTags tags=%+v err=%v", tags, err)
	}
	if execution, err := q.CreateCleanupExecution(ctx, CreateCleanupExecutionParams{NexusInstanceID: 7, PolicyName: "ci-cleanup"}); err != nil || execution.ID != 11 {
		t.Fatalf("CreateCleanupExecution execution=%+v err=%v", execution, err)
	}
	if event, err := q.CreateCleanupExecutionEvent(ctx, CreateCleanupExecutionEventParams{CleanupExecutionID: 11, EventType: "approval_recorded"}); err != nil || event.ID != 21 {
		t.Fatalf("CreateCleanupExecutionEvent event=%+v err=%v", event, err)
	}
	if rows, err := q.ListCleanupExecutions(ctx, ListCleanupExecutionsParams{LimitCount: 10}); err != nil || len(rows) != 1 {
		t.Fatalf("ListCleanupExecutions rows=%+v err=%v", rows, err)
	}
	if rows, err := q.ListCleanupExecutionEvents(ctx, ListCleanupExecutionEventsParams{CleanupExecutionID: 11, LimitCount: 10}); err != nil || len(rows) != 1 {
		t.Fatalf("ListCleanupExecutionEvents rows=%+v err=%v", rows, err)
	}
	if row, err := q.RequestCleanupExecutionCancel(ctx, RequestCleanupExecutionCancelParams{ID: 11}); err != nil || row.ID != 11 {
		t.Fatalf("RequestCleanupExecutionCancel row=%+v err=%v", row, err)
	}
	if rows, err := q.CountScanTasksByStatus(ctx); err != nil || len(rows) != 1 {
		t.Fatalf("CountScanTasksByStatus rows=%+v err=%v", rows, err)
	}
	if age, err := q.GetOldestPendingTaskAgeSeconds(ctx); err != nil || age != 120 {
		t.Fatalf("GetOldestPendingTaskAgeSeconds age=%d err=%v", age, err)
	}
	if deleted, err := q.DeleteTerminalScanRunsBefore(ctx, dbTime(now)); err != nil || deleted != 2 {
		t.Fatalf("DeleteTerminalScanRunsBefore deleted=%d err=%v", deleted, err)
	}
}

func TestManifestVerificationAndAdminQueryWrappers(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	q := New(&fakeDB{
		rowFunc: func(query string, args []any) fakeRow {
			switch {
			case strings.Contains(query, "UpsertDockerManifest"):
				return fakeRow{values: dbDockerManifestValues(101, 1, "sha256:manifest", now)}
			case strings.Contains(query, "UpsertDockerLayer"):
				return fakeRow{values: dbDockerLayerValues(201, 1, "sha256:layer", now)}
			case strings.Contains(query, "UpsertNexusRateLimits"),
				strings.Contains(query, "PauseNexusIndexing"),
				strings.Contains(query, "ResumeNexusIndexing"):
				return fakeRow{values: dbIndexingControlValues(7, now)}
			default:
				return fakeRow{err: fmt.Errorf("unexpected row query: %s", dbQueryName(query))}
			}
		},
		rowsFunc: func(query string, args []any) (*fakeRows, error) {
			switch {
			case strings.Contains(query, "ListNexusInstances"):
				return &fakeRows{rows: [][]any{{int64(7), "local", "http://nexus:8081", dbTime(now), dbTime(now)}}}, nil
			case strings.Contains(query, "ListMissingDigestVerificationCandidates"):
				return &fakeRows{rows: [][]any{{int64(7), "docker-hosted", "demo/app", "latest", int64(101)}}}, nil
			case strings.Contains(query, "ListMissingManifestVerificationCandidates"):
				return &fakeRows{rows: [][]any{{int64(7), "docker-hosted", "demo/app", "latest", int64(101), "sha256:abc"}}}, nil
			default:
				return &fakeRows{}, nil
			}
		},
	})

	ctx := context.Background()
	if manifest, err := q.UpsertDockerManifest(ctx, UpsertDockerManifestParams{RepositoryID: 1, Digest: "sha256:manifest"}); err != nil || manifest.ID != 101 {
		t.Fatalf("UpsertDockerManifest manifest=%+v err=%v", manifest, err)
	}
	if layer, err := q.UpsertDockerLayer(ctx, UpsertDockerLayerParams{RepositoryID: 1, Digest: "sha256:layer"}); err != nil || layer.ID != 201 {
		t.Fatalf("UpsertDockerLayer layer=%+v err=%v", layer, err)
	}
	if err := q.LinkDockerManifestLayer(ctx, LinkDockerManifestLayerParams{ManifestID: 101, LayerID: 201}); err != nil {
		t.Fatal(err)
	}
	if rows, err := q.ListMissingDigestVerificationCandidates(ctx, ListMissingDigestVerificationCandidatesParams{LimitCount: 10}); err != nil || len(rows) != 1 {
		t.Fatalf("ListMissingDigestVerificationCandidates rows=%+v err=%v", rows, err)
	}
	if rows, err := q.ListMissingManifestVerificationCandidates(ctx, ListMissingManifestVerificationCandidatesParams{LimitCount: 10}); err != nil || len(rows) != 1 {
		t.Fatalf("ListMissingManifestVerificationCandidates rows=%+v err=%v", rows, err)
	}
	if rows, err := q.ListNexusInstances(ctx, 10); err != nil || len(rows) != 1 {
		t.Fatalf("ListNexusInstances rows=%+v err=%v", rows, err)
	}
	if control, err := q.UpsertNexusRateLimits(ctx, UpsertNexusRateLimitsParams{NexusInstanceID: 7}); err != nil || control.NexusInstanceID != 7 {
		t.Fatalf("UpsertNexusRateLimits control=%+v err=%v", control, err)
	}
	if control, err := q.PauseNexusIndexing(ctx, PauseNexusIndexingParams{NexusInstanceID: 7}); err != nil || !control.Paused {
		t.Fatalf("PauseNexusIndexing control=%+v err=%v", control, err)
	}
	if control, err := q.ResumeNexusIndexing(ctx, 7); err != nil || control.NexusInstanceID != 7 {
		t.Fatalf("ResumeNexusIndexing control=%+v err=%v", control, err)
	}
}

func TestAdditionalGeneratedQueryWrappers(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	q := New(&fakeDB{
		rowFunc: func(query string, args []any) fakeRow {
			switch {
			case strings.Contains(query, "CountAppUsers"):
				return fakeRow{values: []any{int64(1)}}
			case strings.Contains(query, "CreateAppUser"):
				return fakeRow{values: []any{int64(1), "admin", "hash", "admin", dbTime(now), dbTime(now), pgtype.Timestamptz{}}}
			case strings.Contains(query, "GetActiveAppUserByUsername"):
				return fakeRow{values: []any{int64(1), "admin", "hash", "admin", []string{"read"}, dbTime(now), dbTime(now), pgtype.Timestamptz{}}}
			case strings.Contains(query, "CreateAppSession"):
				return fakeRow{values: []any{int64(10), int64(1), "hash", dbTime(now), dbTime(now.Add(time.Hour)), pgtype.Timestamptz{}}}
			case strings.Contains(query, "GetActiveAppSessionByTokenHash"):
				return fakeRow{values: []any{int64(10), int64(1), "hash", dbTime(now), dbTime(now.Add(time.Hour)), pgtype.Timestamptz{}, "admin", "admin", []string{"read"}}}
			case strings.Contains(query, "UpsertAppRole"):
				return fakeRow{values: []any{"custom", "Custom", []string{"read"}, false, dbTime(now), dbTime(now)}}
			case strings.Contains(query, "GetNexusInstanceByID"):
				return fakeRow{values: []any{int64(7), "local", "http://nexus:8081", dbTime(now), dbTime(now)}}
			case strings.Contains(query, "GetOldestRunningCleanupAgeSeconds"):
				return fakeRow{values: []any{int64(120)}}
			case strings.Contains(query, "RecordNexusIndexingSuccess"):
				return fakeRow{values: dbIndexingControlValues(7, now)}
			case strings.Contains(query, "GetRetentionSettings"),
				strings.Contains(query, "UpsertRetentionSettings"):
				return fakeRow{values: []any{true, pgtype.Int4{Int32: 3600, Valid: true}, pgtype.Int4{Int32: 30, Valid: true}, pgtype.Int4{Int32: 365, Valid: true}, pgtype.Int4{Int32: 180, Valid: true}, pgtype.Int4{Int32: 365, Valid: true}, dbText("admin"), dbTime(now)}}
			case strings.Contains(query, "AcquireCleanupLock"):
				return fakeRow{values: []any{int64(7), int64(11), "exclusive", dbTime(now), dbTime(now.Add(time.Hour)), dbText("cleanup")}}
			case strings.Contains(query, "ClaimQueuedCleanupExecution"),
				strings.Contains(query, "FinishCleanupExecution"),
				strings.Contains(query, "GetCleanupExecution"),
				strings.Contains(query, "TouchCleanupExecutionLease"):
				return fakeRow{values: dbCleanupExecutionValues(11, 7, "running", now)}
			case strings.Contains(query, "GetCleanupLock"):
				return fakeRow{values: []any{int64(7), int64(11), "exclusive", dbTime(now), dbTime(now.Add(time.Hour)), dbText("cleanup")}}
			case strings.Contains(query, "UpsertContainerImage"):
				return fakeRow{values: []any{int64(201), int64(301), "demo/app", dbTime(now), dbTime(now)}}
			case strings.Contains(query, "UpsertContainerTag"):
				return fakeRow{values: []any{int64(101), int64(201), "latest", "sha256:abc", "media", int64(10), dbTime(now), dbTime(now), pgtype.Timestamptz{}}}
			case strings.Contains(query, "MarkContainerTagDeleted"):
				return fakeRow{values: []any{int64(101), int64(201), "latest", "sha256:abc", "media", int64(10), dbTime(now), dbTime(now), pgtype.Timestamptz{}}}
			case strings.Contains(query, "UpsertGenericArtifact"):
				return fakeRow{values: []any{int64(501), int64(301), "pypi", dbText(""), "demo", dbText("1.0.0"), dbText("demo-1.0.0.whl"), pgtype.Int8{Int64: 10, Valid: true}, dbText("abc"), dbTime(now), dbTime(now), []byte(`{}`)}}
			case strings.Contains(query, "UpsertNexusAsset"):
				return fakeRow{values: []any{int64(601), int64(301), dbText("asset"), dbText("path"), dbText("url"), dbText("content"), pgtype.Int8{Int64: 10, Valid: true}, dbText("default"), dbText("sha1"), dbText("sha256"), pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgtype.Timestamptz{}, dbTime(now), dbTime(now), []byte(`{}`)}}
			default:
				return fakeRow{err: fmt.Errorf("unexpected row query: %s", dbQueryName(query))}
			}
		},
		rowsFunc: func(query string, args []any) (*fakeRows, error) {
			switch {
			case strings.Contains(query, "ListAppRoles"):
				return &fakeRows{rows: [][]any{{"admin", "Admin", []string{"read"}, true, dbTime(now), dbTime(now)}}}, nil
			case strings.Contains(query, "ListIndexingSchedules"):
				return &fakeRows{rows: [][]any{{int64(3), int64(7), "local", "http://nexus:8081", "recent_sync", "recent-sync", true, "every 1800s", int32(1800), "UTC", []byte(`{}`), "skip", dbTime(now), pgtype.Timestamptz{}, dbTime(now), dbTime(now)}}}, nil
			case strings.Contains(query, "ListRepositoryAnalytics"),
				strings.Contains(query, "ListImageAnalytics"),
				strings.Contains(query, "ListTagAnalytics"),
				strings.Contains(query, "ListPackageFormatRepositories"),
				strings.Contains(query, "ListTagPatternAnalytics"),
				strings.Contains(query, "ListTagPatternGrowth"),
				strings.Contains(query, "ListTagPatternExamples"),
				strings.Contains(query, "ListTagPatternSuggestions"),
				strings.Contains(query, "ListTrendSeries"),
				strings.Contains(query, "ListTrendTopMovers"),
				strings.Contains(query, "ListTrendMetricNames"),
				strings.Contains(query, "ListActiveTagsForRepository"),
				strings.Contains(query, "ListActiveTagsForRepositorySeenSince"),
				strings.Contains(query, "ListActiveTagsForImage"),
				strings.Contains(query, "ReclaimExpiredCleanupExecutions"),
				strings.Contains(query, "ReclaimExpiredScanTasks"),
				strings.Contains(query, "MarkMissingContainerTagsDeletedForRepository"),
				strings.Contains(query, "CaptureDailyTrendSnapshots"),
				strings.Contains(query, "ListVerificationFindings"):
				return &fakeRows{}, nil
			case strings.Contains(query, "CountCleanupExecutionsByStatus"),
				strings.Contains(query, "CountScanRunsByStatus24h"):
				return &fakeRows{rows: [][]any{{"running", int64(1)}}}, nil
			case strings.Contains(query, "CountCleanupExecutionEvents24h"):
				return &fakeRows{rows: [][]any{{"cleanup_started", dbText("running"), int64(1)}}}, nil
			case strings.Contains(query, "CountOpenVerificationFindingsBySeverity"):
				return &fakeRows{rows: [][]any{{"high", int64(1)}}}, nil
			default:
				return &fakeRows{}, nil
			}
		},
		execFunc: func(query string, args []any) (pgconn.CommandTag, error) {
			switch {
			case strings.Contains(query, "TryAcquireSchedulerLock"):
				return pgconn.NewCommandTag("SELECT 1"), nil
			default:
				return pgconn.NewCommandTag("DELETE 2"), nil
			}
		},
	})

	ctx := context.Background()
	if count, err := q.CountAppUsers(ctx); err != nil || count != 1 {
		t.Fatalf("CountAppUsers count=%d err=%v", count, err)
	}
	if user, err := q.CreateAppUser(ctx, CreateAppUserParams{Username: "admin"}); err != nil || user.ID != 1 {
		t.Fatalf("CreateAppUser user=%+v err=%v", user, err)
	}
	if user, err := q.GetActiveAppUserByUsername(ctx, "admin"); err != nil || user.Username != "admin" {
		t.Fatalf("GetActiveAppUserByUsername user=%+v err=%v", user, err)
	}
	if roles, err := q.ListAppRoles(ctx); err != nil || len(roles) != 1 {
		t.Fatalf("ListAppRoles roles=%+v err=%v", roles, err)
	}
	if role, err := q.UpsertAppRole(ctx, UpsertAppRoleParams{Name: "custom"}); err != nil || role.Name != "custom" {
		t.Fatalf("UpsertAppRole role=%+v err=%v", role, err)
	}
	if session, err := q.CreateAppSession(ctx, CreateAppSessionParams{UserID: 1}); err != nil || session.ID != 10 {
		t.Fatalf("CreateAppSession session=%+v err=%v", session, err)
	}
	if session, err := q.GetActiveAppSessionByTokenHash(ctx, "hash"); err != nil || session.Username != "admin" {
		t.Fatalf("GetActiveAppSessionByTokenHash session=%+v err=%v", session, err)
	}
	if err := q.RevokeAppSessionByTokenHash(ctx, "hash"); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteExpiredAppSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if instance, err := q.GetNexusInstanceByID(ctx, 7); err != nil || instance.ID != 7 {
		t.Fatalf("GetNexusInstanceByID instance=%+v err=%v", instance, err)
	}
	if rows, err := q.ListIndexingSchedules(ctx, ListIndexingSchedulesParams{LimitCount: 10}); err != nil || len(rows) != 1 {
		t.Fatalf("ListIndexingSchedules rows=%+v err=%v", rows, err)
	}
	if _, err := q.GetRetentionSettings(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpsertRetentionSettings(ctx, UpsertRetentionSettingsParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.AcquireCleanupLock(ctx, AcquireCleanupLockParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ClaimQueuedCleanupExecution(ctx, ClaimQueuedCleanupExecutionParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.FinishCleanupExecution(ctx, FinishCleanupExecutionParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetCleanupExecution(ctx, 11); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetCleanupLock(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := q.TouchCleanupExecutionLease(ctx, TouchCleanupExecutionLeaseParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ReclaimExpiredCleanupExecutions(ctx); err != nil {
		t.Fatal(err)
	}
	if err := q.ReleaseCleanupLockByExecution(ctx, 11); err != nil {
		t.Fatal(err)
	}
	if err := q.ReleaseOperationLockByRun(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if err := q.ReleaseSchedulerLock(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := q.RecordNexusIndexingSuccess(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if rows, err := q.ListRepositoryAnalytics(ctx, ListRepositoryAnalyticsParams{LimitCount: 10}); err != nil || len(rows) != 0 {
		t.Fatalf("ListRepositoryAnalytics rows=%+v err=%v", rows, err)
	}
	if rows, err := q.ListTrendMetricNames(ctx, pgtype.Int8{}); err != nil || len(rows) != 0 {
		t.Fatalf("ListTrendMetricNames rows=%+v err=%v", rows, err)
	}
	if _, err := q.UpsertContainerImage(ctx, UpsertContainerImageParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpsertContainerTag(ctx, UpsertContainerTagParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.MarkContainerTagDeleted(ctx, MarkContainerTagDeletedParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpsertGenericArtifact(ctx, UpsertGenericArtifactParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpsertNexusAsset(ctx, UpsertNexusAssetParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CaptureDailyTrendSnapshots(ctx, CaptureDailyTrendSnapshotsParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CountCleanupExecutionsByStatus(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetOldestRunningCleanupAgeSeconds(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestQueueVerificationRetentionAndListQueryWrappers(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	q := New(&fakeDB{
		rowFunc: func(query string, args []any) fakeRow {
			switch {
			case strings.Contains(query, "AcquireOperationLock"):
				return fakeRow{values: []any{int64(7), int64(42), "full_reconcile", "exclusive", dbTime(now), dbTime(now.Add(time.Hour)), dbText("manual")}}
			case strings.Contains(query, "CancelScanRun"),
				strings.Contains(query, "CreateSkippedScanRun"),
				strings.Contains(query, "GetScanRun"),
				strings.Contains(query, "MarkScanRunFailed"),
				strings.Contains(query, "RefreshScanRunTaskCounters"):
				return fakeRow{values: dbScanRunValues(42, 7, "full_reconcile", "running", now)}
			case strings.Contains(query, "CompleteScanTask"),
				strings.Contains(query, "FailScanTask"),
				strings.Contains(query, "RetryScanTask"):
				return fakeRow{values: dbScanTaskValues(99, 42, 7, "docker_tag_scan", now)}
			case strings.Contains(query, "CreateScanTaskAttempt"):
				return fakeRow{values: dbScanTaskAttemptValues(1001, 99, now)}
			case strings.Contains(query, "GetNexusIndexingControl"),
				strings.Contains(query, "RecordNexusIndexingFailure"):
				return fakeRow{values: dbIndexingControlValues(7, now)}
			case strings.Contains(query, "MarkIndexingScheduleTicked"):
				return fakeRow{values: dbIndexingScheduleValues(3, 7, now)}
			case strings.Contains(query, "TryAcquireSchedulerLock"):
				return fakeRow{values: []any{true}}
			case strings.Contains(query, "UpsertNexusInstance"):
				return fakeRow{values: []any{int64(7), "local", "http://nexus:8081", dbTime(now), dbTime(now)}}
			case strings.Contains(query, "UpsertRepositoryFromDiscovery"):
				return fakeRow{values: dbRepositoryValues(301, 7, "docker-hosted", now)}
			case strings.Contains(query, "UpsertRepository"):
				return fakeRow{values: []any{int64(301), int64(7), "docker-hosted", "docker", "hosted", dbText("http://nexus/repository/docker-hosted"), dbTime(now), dbTime(now)}}
			case strings.Contains(query, "DismissVerificationFinding"):
				return fakeRow{values: dbVerificationFindingValues(55, now)}
			case strings.Contains(query, "UpsertOpenVerificationFinding"):
				return fakeRow{values: dbVerificationFindingValues(55, now)}
			default:
				return fakeRow{err: fmt.Errorf("unexpected row query: %s", dbQueryName(query))}
			}
		},
		rowsFunc: func(query string, args []any) (*fakeRows, error) {
			switch {
			case strings.Contains(query, "ClaimScanTasks"),
				strings.Contains(query, "ReclaimExpiredScanTasks"):
				return &fakeRows{rows: [][]any{dbScanTaskValues(99, 42, 7, "docker_tag_scan", now)}}, nil
			case strings.Contains(query, "ListDueIndexingSchedules"):
				return &fakeRows{rows: [][]any{append(dbIndexingScheduleValues(3, 7, now), "local", "http://nexus:8081")}}, nil
			case strings.Contains(query, "ListVerificationFindings"):
				return &fakeRows{rows: [][]any{dbVerificationFindingValues(55, now)}}, nil
			case strings.Contains(query, "MarkMissingContainerTagsDeletedForRepository"):
				return &fakeRows{rows: [][]any{{int64(101), int64(201), "latest", "sha256:abc", "application/vnd.oci.image.manifest.v1+json", int64(1000), dbTime(now), dbTime(now), dbTime(now)}}}, nil
			case strings.Contains(query, "ListImageAnalytics"):
				return &fakeRows{rows: [][]any{dbImageAnalyticsValues(now)}}, nil
			case strings.Contains(query, "ListTagAnalytics"):
				return &fakeRows{rows: [][]any{dbTagAnalyticsValues(now)}}, nil
			case strings.Contains(query, "ListPackageFormatRepositories"):
				return &fakeRows{rows: [][]any{dbPackageFormatRepositoryValues(now)}}, nil
			case strings.Contains(query, "ListTagPatternAnalytics"):
				return &fakeRows{rows: [][]any{{"semver", int64(2), int64(100), dbTime(now), int64(1)}}}, nil
			case strings.Contains(query, "ListTagPatternGrowth"):
				return &fakeRows{rows: [][]any{{"docker-hosted", "demo", "feature_branch", int64(3), int64(200), int64(1), int64(50), int64(1), dbTime(now)}}}, nil
			case strings.Contains(query, "ListTagPatternExamples"):
				return &fakeRows{rows: [][]any{{"semver", "docker-hosted", "demo/app", "v1.2.3", dbTime(now), dbTime(now)}}}, nil
			case strings.Contains(query, "ListTagPatternSuggestions"):
				return &fakeRows{rows: [][]any{{"A-0", int64(4), []string{"pr-1"}, []byte("^[A-Za-z]+-[0-9]+$")}}}, nil
			case strings.Contains(query, "ListTrendSeries"):
				return &fakeRows{rows: [][]any{{pgtype.Date{Time: now, Valid: true}, dbTime(now), pgtype.Numeric{}, false}}}, nil
			case strings.Contains(query, "ListTrendTopMovers"):
				return &fakeRows{rows: [][]any{{pgtype.Int8{Int64: 301, Valid: true}, dbText("docker-hosted"), pgtype.Int8{Int64: 201, Valid: true}, dbText("demo/app"), pgtype.Numeric{}, pgtype.Numeric{}, pgtype.Numeric{}, pgtype.Numeric{}}}}, nil
			case strings.Contains(query, "ListActiveTagsForRepository"),
				strings.Contains(query, "ListActiveTagsForRepositorySeenSince"):
				return &fakeRows{rows: [][]any{{int64(301), dbText("http://nexus/repository/docker-hosted"), "demo/app", "latest", "sha256:abc", "application/vnd.oci.image.manifest.v1+json"}}}, nil
			case strings.Contains(query, "ListActiveTagsForImage"):
				return &fakeRows{rows: [][]any{{int64(101), "latest", "sha256:abc", "application/vnd.oci.image.manifest.v1+json", int64(1000), dbTime(now), dbTime(now)}}}, nil
			default:
				return &fakeRows{}, nil
			}
		},
		execFunc: func(query string, args []any) (pgconn.CommandTag, error) {
			return pgconn.NewCommandTag("DELETE 2"), nil
		},
	})

	ctx := context.Background()
	if lock, err := q.AcquireOperationLock(ctx, AcquireOperationLockParams{NexusInstanceID: 7, ScanRunID: 42, OperationType: "full_reconcile"}); err != nil || lock.ScanRunID != 42 {
		t.Fatalf("AcquireOperationLock lock=%+v err=%v", lock, err)
	}
	if run, err := q.CancelScanRun(ctx, 42); err != nil || run.ID != 42 {
		t.Fatalf("CancelScanRun run=%+v err=%v", run, err)
	}
	if tasks, err := q.ClaimScanTasks(ctx, ClaimScanTasksParams{LimitCount: 10}); err != nil || len(tasks) != 1 {
		t.Fatalf("ClaimScanTasks tasks=%+v err=%v", tasks, err)
	}
	if task, err := q.CompleteScanTask(ctx, CompleteScanTaskParams{ID: 99, Stats: []byte(`{}`)}); err != nil || task.ID != 99 {
		t.Fatalf("CompleteScanTask task=%+v err=%v", task, err)
	}
	if attempt, err := q.CreateScanTaskAttempt(ctx, CreateScanTaskAttemptParams{ScanTaskID: 99, Attempt: 1}); err != nil || attempt.ScanTaskID != 99 {
		t.Fatalf("CreateScanTaskAttempt attempt=%+v err=%v", attempt, err)
	}
	if run, err := q.CreateSkippedScanRun(ctx, CreateSkippedScanRunParams{NexusInstanceID: 7, ScanType: "recent_sync", ErrorDetails: []byte(`{}`), Stats: []byte(`{}`)}); err != nil || run.ID != 42 {
		t.Fatalf("CreateSkippedScanRun run=%+v err=%v", run, err)
	}
	if task, err := q.FailScanTask(ctx, FailScanTaskParams{ID: 99, ErrorDetails: []byte(`{}`)}); err != nil || task.ID != 99 {
		t.Fatalf("FailScanTask task=%+v err=%v", task, err)
	}
	if err := q.FinishScanTaskAttempt(ctx, FinishScanTaskAttemptParams{ID: 1001, Status: "succeeded", Stats: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if control, err := q.GetNexusIndexingControl(ctx, 7); err != nil || control.NexusInstanceID != 7 {
		t.Fatalf("GetNexusIndexingControl control=%+v err=%v", control, err)
	}
	if run, err := q.GetScanRun(ctx, 42); err != nil || run.ID != 42 {
		t.Fatalf("GetScanRun run=%+v err=%v", run, err)
	}
	if schedules, err := q.ListDueIndexingSchedules(ctx, 10); err != nil || len(schedules) != 1 {
		t.Fatalf("ListDueIndexingSchedules schedules=%+v err=%v", schedules, err)
	}
	if schedule, err := q.MarkIndexingScheduleTicked(ctx, 3); err != nil || schedule.ID != 3 {
		t.Fatalf("MarkIndexingScheduleTicked schedule=%+v err=%v", schedule, err)
	}
	if run, err := q.MarkScanRunFailed(ctx, MarkScanRunFailedParams{ID: 42, ErrorDetails: []byte(`{}`)}); err != nil || run.ID != 42 {
		t.Fatalf("MarkScanRunFailed run=%+v err=%v", run, err)
	}
	if tasks, err := q.ReclaimExpiredScanTasks(ctx); err != nil || len(tasks) != 1 {
		t.Fatalf("ReclaimExpiredScanTasks tasks=%+v err=%v", tasks, err)
	}
	if control, err := q.RecordNexusIndexingFailure(ctx, RecordNexusIndexingFailureParams{NexusInstanceID: 7, Threshold: 3}); err != nil || control.NexusInstanceID != 7 {
		t.Fatalf("RecordNexusIndexingFailure control=%+v err=%v", control, err)
	}
	if run, err := q.RefreshScanRunTaskCounters(ctx, 42); err != nil || run.ID != 42 {
		t.Fatalf("RefreshScanRunTaskCounters run=%+v err=%v", run, err)
	}
	if task, err := q.RetryScanTask(ctx, RetryScanTaskParams{ID: 99, ErrorDetails: []byte(`{}`)}); err != nil || task.ID != 99 {
		t.Fatalf("RetryScanTask task=%+v err=%v", task, err)
	}
	if ok, err := q.TryAcquireSchedulerLock(ctx, 1234); err != nil || !ok {
		t.Fatalf("TryAcquireSchedulerLock ok=%v err=%v", ok, err)
	}
	if instance, err := q.UpsertNexusInstance(ctx, UpsertNexusInstanceParams{Name: "local", BaseUrl: "http://nexus:8081"}); err != nil || instance.ID != 7 {
		t.Fatalf("UpsertNexusInstance instance=%+v err=%v", instance, err)
	}
	if repository, err := q.UpsertRepository(ctx, UpsertRepositoryParams{NexusInstanceID: 7, Name: "docker-hosted", Format: "docker", Type: "hosted"}); err != nil || repository.ID != 301 {
		t.Fatalf("UpsertRepository repository=%+v err=%v", repository, err)
	}
	if repository, err := q.UpsertRepositoryFromDiscovery(ctx, UpsertRepositoryFromDiscoveryParams{NexusInstanceID: 7, Name: "docker-hosted", Format: "docker", Type: "hosted", Metadata: []byte(`{}`)}); err != nil || repository.ID != 301 {
		t.Fatalf("UpsertRepositoryFromDiscovery repository=%+v err=%v", repository, err)
	}
	if finding, err := q.DismissVerificationFinding(ctx, DismissVerificationFindingParams{DismissedBy: "admin", ID: 55}); err != nil || finding.ID != 55 {
		t.Fatalf("DismissVerificationFinding finding=%+v err=%v", finding, err)
	}
	if findings, err := q.ListVerificationFindings(ctx, ListVerificationFindingsParams{LimitCount: 10}); err != nil || len(findings) != 1 {
		t.Fatalf("ListVerificationFindings findings=%+v err=%v", findings, err)
	}
	if finding, err := q.UpsertOpenVerificationFinding(ctx, UpsertOpenVerificationFindingParams{Severity: "warning", Expected: []byte(`{}`), Actual: []byte(`{}`), VfFindingType: "missing_digest", Metadata: []byte(`{}`)}); err != nil || finding.ID != 55 {
		t.Fatalf("UpsertOpenVerificationFinding finding=%+v err=%v", finding, err)
	}
	if tags, err := q.MarkMissingContainerTagsDeletedForRepository(ctx, MarkMissingContainerTagsDeletedForRepositoryParams{RepositoryID: 301, LastSeenAt: dbTime(now)}); err != nil || len(tags) != 1 {
		t.Fatalf("MarkMissingContainerTagsDeletedForRepository tags=%+v err=%v", tags, err)
	}
	if images, err := q.ListImageAnalytics(ctx, ListImageAnalyticsParams{LimitCount: 10}); err != nil || len(images) != 1 {
		t.Fatalf("ListImageAnalytics images=%+v err=%v", images, err)
	}
	if tags, err := q.ListTagAnalytics(ctx, ListTagAnalyticsParams{LimitCount: 10}); err != nil || len(tags) != 1 {
		t.Fatalf("ListTagAnalytics tags=%+v err=%v", tags, err)
	}
	if repositories, err := q.ListPackageFormatRepositories(ctx, ListPackageFormatRepositoriesParams{LimitCount: 10}); err != nil || len(repositories) != 1 {
		t.Fatalf("ListPackageFormatRepositories repositories=%+v err=%v", repositories, err)
	}
	if analytics, err := q.ListTagPatternAnalytics(ctx, ListTagPatternAnalyticsParams{LimitCount: 10}); err != nil || len(analytics) != 1 {
		t.Fatalf("ListTagPatternAnalytics analytics=%+v err=%v", analytics, err)
	}
	if growth, err := q.ListTagPatternGrowth(ctx, ListTagPatternGrowthParams{LimitCount: 10}); err != nil || len(growth) != 1 {
		t.Fatalf("ListTagPatternGrowth growth=%+v err=%v", growth, err)
	}
	if examples, err := q.ListTagPatternExamples(ctx, ListTagPatternExamplesParams{LimitCount: 10}); err != nil || len(examples) != 1 {
		t.Fatalf("ListTagPatternExamples examples=%+v err=%v", examples, err)
	}
	if suggestions, err := q.ListTagPatternSuggestions(ctx, ListTagPatternSuggestionsParams{LimitCount: 10}); err != nil || len(suggestions) != 1 {
		t.Fatalf("ListTagPatternSuggestions suggestions=%+v err=%v", suggestions, err)
	}
	if points, err := q.ListTrendSeries(ctx, ListTrendSeriesParams{NexusInstanceID: 7, MetricName: "docker_logical_bytes"}); err != nil || len(points) != 1 {
		t.Fatalf("ListTrendSeries points=%+v err=%v", points, err)
	}
	if movers, err := q.ListTrendTopMovers(ctx, ListTrendTopMoversParams{LimitCount: 10, NexusInstanceID: 7, MetricName: "docker_logical_bytes"}); err != nil || len(movers) != 1 {
		t.Fatalf("ListTrendTopMovers movers=%+v err=%v", movers, err)
	}
	if tags, err := q.ListActiveTagsForRepository(ctx, 301); err != nil || len(tags) != 1 {
		t.Fatalf("ListActiveTagsForRepository tags=%+v err=%v", tags, err)
	}
	if tags, err := q.ListActiveTagsForRepositorySeenSince(ctx, ListActiveTagsForRepositorySeenSinceParams{ID: 301, LastSeenAt: dbTime(now)}); err != nil || len(tags) != 1 {
		t.Fatalf("ListActiveTagsForRepositorySeenSince tags=%+v err=%v", tags, err)
	}
	if tags, err := q.ListActiveTagsForImage(ctx, ListActiveTagsForImageParams{NexusName: "local", RepositoryName: "docker-hosted", ImageName: "demo/app"}); err != nil || len(tags) != 1 {
		t.Fatalf("ListActiveTagsForImage tags=%+v err=%v", tags, err)
	}
	if deleted, err := q.DeleteCleanupExecutionsBefore(ctx, dbTime(now)); err != nil || deleted != 2 {
		t.Fatalf("DeleteCleanupExecutionsBefore deleted=%d err=%v", deleted, err)
	}
	if deleted, err := q.DeleteClosedVerificationFindingsBefore(ctx, dbTime(now)); err != nil || deleted != 2 {
		t.Fatalf("DeleteClosedVerificationFindingsBefore deleted=%d err=%v", deleted, err)
	}
	if deleted, err := q.DeleteTrendSnapshotsBefore(ctx, pgtype.Date{Time: now, Valid: true}); err != nil || deleted != 2 {
		t.Fatalf("DeleteTrendSnapshotsBefore deleted=%d err=%v", deleted, err)
	}
}

func dbText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: true}
}

func dbTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func dbScanRunValues(id, nexusID int64, scanType, status string, now time.Time) []any {
	return []any{id, nexusID, scanType, pgtype.Text{}, "manual", pgtype.Text{}, status, dbText("queued"), dbTime(now), pgtype.Timestamptz{}, dbTime(now), pgtype.Int8{Int64: 1, Valid: true}, int64(0), int64(0), pgtype.Numeric{}, "estimated", []byte(`{}`), []byte(`{}`), []byte(`{}`), pgtype.Text{}, pgtype.Text{}, pgtype.Text{}, []byte(`{}`)}
}

func dbScanTaskValues(id, scanRunID, nexusID int64, taskType string, now time.Time) []any {
	return []any{id, scanRunID, nexusID, taskType, "task", pgtype.Text{}, pgtype.Text{}, "asset_list", pgtype.Int4{}, "pending", int32(0), int32(0), int32(3), dbTime(now), pgtype.Timestamptz{}, pgtype.UUID{}, pgtype.Text{}, pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgtype.Timestamptz{}, dbTime(now), pgtype.Int8{}, pgtype.Int8{}, pgtype.Text{}, []byte(`{}`), []byte(`{}`), []byte(`{}`), pgtype.Text{}, pgtype.Text{}, pgtype.Text{}, []byte(`{}`), dbTime(now)}
}

func dbTagPatternValues(id int64, now time.Time) []any {
	return []any{id, "semver", "SemVer", "semantic versions", "^v?[0-9]+", int32(500), true, false, dbTime(now), dbTime(now)}
}

func dbCleanupSimulationTagValues(firstSeen time.Time) []any {
	return []any{int64(101), int64(201), int64(301), int64(7), "local", "docker-hosted", "demo/app", "feature-x", "sha256:abc", "feature_branch", int64(1000), int64(800), int64(500), int64(300), dbTime(firstSeen), dbTime(firstSeen.Add(time.Hour)), nil, false, false}
}

func dbCleanupExecutionValues(id, nexusID int64, status string, now time.Time) []any {
	return []any{id, nexusID, dbText("docker-hosted"), "ci-cleanup", "name: ci-cleanup", status, "admin", dbText("admin"), "ci-cleanup", false, []byte(`{}`), []byte(`[]`), pgtype.Int8{}, dbTime(now), dbTime(now), pgtype.Timestamptz{}, []byte(`[]`), int64(1), pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgtype.Text{}, pgtype.Timestamptz{}, pgtype.Text{}, pgtype.Int8{}}
}

func dbCleanupEventValues(id, executionID int64, eventType string, now time.Time) []any {
	return []any{id, executionID, eventType, dbText("admin"), dbText("docker-hosted"), dbText("demo/app"), dbText("feature-x"), pgtype.Int8{Int64: 101, Valid: true}, dbText("old features"), dbText("component-1"), dbText("done"), dbText("message"), pgtype.Text{}, pgtype.Text{}, pgtype.Text{}, []byte(`{}`), dbTime(now)}
}

func dbDockerManifestValues(id, repositoryID int64, digest string, now time.Time) []any {
	return []any{id, repositoryID, digest, dbText("application/vnd.oci.image.manifest.v1+json"), dbText("sha256:config"), pgtype.Int8{Int64: 60, Valid: true}, dbTime(now), dbTime(now), []byte(`{}`)}
}

func dbDockerLayerValues(id, repositoryID int64, digest string, now time.Time) []any {
	return []any{id, repositoryID, digest, dbText("application/vnd.oci.image.layer.v1.tar+gzip"), pgtype.Int8{Int64: 20, Valid: true}, dbTime(now), dbTime(now)}
}

func dbIndexingControlValues(nexusID int64, now time.Time) []any {
	return []any{nexusID, true, dbText("maintenance"), dbTime(now), dbText("admin"), int32(1), dbTime(now.Add(time.Minute)), dbTime(now), dbText("rate_limited"), dbText("nexus"), dbTime(now), pgtype.Numeric{}, pgtype.Numeric{}, pgtype.Numeric{}}
}

func dbScanTaskAttemptValues(id, taskID int64, now time.Time) []any {
	return []any{id, taskID, int32(1), dbText("worker-1"), dbText("pod-1"), pgtype.UUID{}, dbTime(now), pgtype.Timestamptz{}, "running", pgtype.Text{}, pgtype.Text{}, pgtype.Text{}, []byte(`{}`), []byte(`{}`)}
}

func dbIndexingScheduleValues(id, nexusID int64, now time.Time) []any {
	return []any{id, nexusID, "recent_sync", "recent-sync", true, "every 1800s", int32(1800), "UTC", []byte(`{}`), "skip", dbTime(now.Add(time.Hour)), dbTime(now), dbTime(now), dbTime(now)}
}

func dbRepositoryValues(id, nexusID int64, name string, now time.Time) []any {
	return []any{id, nexusID, name, "docker", "hosted", dbText("http://nexus/repository/" + name), dbTime(now), dbTime(now), dbText("http://registry"), dbText("direct"), pgtype.Timestamptz{}, dbText("default"), pgtype.Bool{Bool: true, Valid: true}, []byte(`{}`), dbTime(now), dbTime(now)}
}

func dbImageAnalyticsValues(now time.Time) []any {
	return []any{int64(201), int64(301), int64(7), "local", "docker-hosted", "demo/app", int64(2), int64(200), int64(100), dbTime(now), dbTime(now)}
}

func dbTagAnalyticsValues(now time.Time) []any {
	return []any{int64(101), int64(201), int64(301), int64(7), "local", "docker-hosted", "demo/app", "latest", "sha256:abc", "application/vnd.oci.image.manifest.v1+json", int64(100), int64(200), int64(150), int64(80), int64(20), dbTime(now), dbTime(now), pgtype.Timestamptz{}, dbTime(now)}
}

func dbPackageFormatRepositoryValues(now time.Time) []any {
	return []any{int64(301), int64(7), "local", "pypi-hosted", "pypi", "hosted", "indexed", "hosted package repository discovered", int64(3), int64(1000), dbTime(now), dbTime(now)}
}

func dbVerificationFindingValues(id int64, now time.Time) []any {
	return []any{id, pgtype.Int8{Int64: 7, Valid: true}, pgtype.Int8{Int64: 42, Valid: true}, "warning", "missing_digest", dbText("docker-hosted"), dbText("demo/app:latest"), []byte(`{}`), []byte(`{}`), dbText("missing digest"), dbText("rescan tag"), "open", dbTime(now), dbTime(now), pgtype.Timestamptz{}, pgtype.Timestamptz{}, []byte(`{}`)}
}
