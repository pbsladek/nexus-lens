package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/pbsladek/nexus-lens/internal/api"
	"github.com/pbsladek/nexus-lens/internal/auth"
	"github.com/pbsladek/nexus-lens/internal/db"
	"github.com/pbsladek/nexus-lens/internal/indexing"
	"github.com/pbsladek/nexus-lens/internal/postgres"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	addr := envOrDefault("NEXUS_LENS_ADDR", ":5601")

	var opts []api.Option
	if corsOrigin := os.Getenv("NEXUS_LENS_CORS_ORIGIN"); corsOrigin != "" {
		opts = append(opts, api.WithCORSOrigin(corsOrigin))
	}
	opts = append(opts, api.WithSecureSessionCookie(envBool("NEXUS_LENS_SESSION_COOKIE_SECURE", false)))
	pgCfg := postgres.FromEnv()
	if pgCfg.Enabled() {
		logger.Info("postgres configured", "postgres", pgCfg.Redacted())
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		database, err := postgres.Open(ctx, pgCfg, logger)
		cancel()
		if err != nil {
			logger.Error("postgres startup failed", "error", err)
			os.Exit(1)
		}
		defer database.Close()
		opts = append(opts, api.WithReadyCheck(database.Ping))

		queries := db.New(database.Pool)
		authService := auth.NewService(queries, logger)
		indexingService := indexing.NewService(queries, logger)
		bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := authService.BootstrapInitialAdmin(
			bootstrapCtx,
			os.Getenv("NEXUS_LENS_INITIAL_ADMIN_USERNAME"),
			os.Getenv("NEXUS_LENS_INITIAL_ADMIN_PASSWORD"),
		); err != nil {
			bootstrapCancel()
			logger.Error("initial admin bootstrap failed", "error", err)
			os.Exit(1)
		}
		bootstrapCancel()
		authAdapter := apiAuthAdapter{service: authService}
		opts = append(opts,
			api.WithAuthenticator(authAdapter.Authenticate),
			api.WithSessionManager(authAdapter),
			api.WithIndexingService(apiIndexingAdapter{
				service:                    indexingService,
				destructiveCleanupEnabled:  envBool("NEXUS_LENS_ENABLE_DESTRUCTIVE_CLEANUP", false),
				nexusUsername:              os.Getenv("NEXUS_LENS_NEXUS_USERNAME"),
				nexusPassword:              envOrFile("NEXUS_LENS_NEXUS_PASSWORD", "NEXUS_LENS_NEXUS_PASSWORD_FILE"),
				cleanupDeleteRPS:           envFloat("NEXUS_LENS_CLEANUP_DELETE_RPS", 0.5),
				cleanupMaintenanceStartUTC: os.Getenv("NEXUS_LENS_CLEANUP_MAINTENANCE_START_UTC"),
				cleanupMaintenanceEndUTC:   os.Getenv("NEXUS_LENS_CLEANUP_MAINTENANCE_END_UTC"),
				retentionDefaults: indexing.RetentionSettings{
					IntervalSeconds:         envInt("NEXUS_LENS_RETENTION_INTERVAL_SECONDS", 3600),
					ScanRunDays:             envInt("NEXUS_LENS_RETENTION_SCAN_RUN_DAYS", 30),
					CleanupExecutionDays:    envInt("NEXUS_LENS_RETENTION_CLEANUP_EXECUTION_DAYS", 365),
					VerificationFindingDays: envInt("NEXUS_LENS_RETENTION_VERIFICATION_FINDING_DAYS", 180),
					TrendSnapshotDays:       envInt("NEXUS_LENS_RETENTION_TREND_SNAPSHOT_DAYS", 365),
				},
			}),
		)
	} else {
		logger.Warn("postgres not configured; readiness will not check database")
	}

	server := api.NewServer(logger, opts...)

	logger.Info("starting api", "addr", addr)
	if err := http.ListenAndServe(addr, server); err != nil {
		logger.Error("api stopped", "error", err)
		os.Exit(1)
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envFloat(key string, fallback float64) float64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envOrFile(envKey, fileKey string) string {
	if value := os.Getenv(envKey); value != "" {
		return value
	}
	path := os.Getenv(fileKey)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

type apiAuthAdapter struct {
	service *auth.Service
}

func (a apiAuthAdapter) Authenticate(ctx context.Context, username, password string) (api.AuthenticatedUser, error) {
	user, err := a.service.Authenticate(ctx, username, password)
	if err != nil {
		return api.AuthenticatedUser{}, err
	}
	return apiUser(user), nil
}

func (a apiAuthAdapter) CreateSession(ctx context.Context, username, password string) (api.Session, error) {
	session, err := a.service.CreateSession(ctx, username, password)
	if err != nil {
		return api.Session{}, err
	}
	return api.Session{
		Token:     session.Token,
		ExpiresAt: session.ExpiresAt,
		User:      apiUser(session.User),
	}, nil
}

func (a apiAuthAdapter) AuthenticateSession(ctx context.Context, token string) (api.AuthenticatedUser, error) {
	user, err := a.service.AuthenticateSession(ctx, token)
	if err != nil {
		return api.AuthenticatedUser{}, err
	}
	return apiUser(user), nil
}

func (a apiAuthAdapter) RevokeSession(ctx context.Context, token string) error {
	return a.service.RevokeSession(ctx, token)
}

func apiUser(user auth.User) api.AuthenticatedUser {
	return api.AuthenticatedUser{Username: user.Username, Role: user.Role, Capabilities: user.Capabilities}
}

type apiIndexingAdapter struct {
	service                    *indexing.Service
	destructiveCleanupEnabled  bool
	nexusUsername              string
	nexusPassword              string
	cleanupDeleteRPS           float64
	cleanupMaintenanceStartUTC string
	cleanupMaintenanceEndUTC   string
	retentionDefaults          indexing.RetentionSettings
}

func (a apiIndexingAdapter) StartIndexRun(ctx context.Context, req api.StartIndexRunRequest) (api.IndexRun, error) {
	run, err := a.service.EnqueueRepositoryDiscoveryRun(ctx, indexing.EnqueueRepositoryDiscoveryRunRequest{
		NexusName: req.NexusName,
		BaseURL:   req.BaseURL,
		ScanType:  req.ScanType,
		TaskKey:   req.TaskKey,
		Scope: indexing.RunScope{
			Repositories: req.Scope.Repositories,
			WindowDays:   req.Scope.WindowDays,
		},
	})
	if err != nil {
		return api.IndexRun{}, err
	}
	return apiIndexRun(run), nil
}

func (a apiIndexingAdapter) ListIndexRuns(ctx context.Context, limit int32) ([]api.IndexRun, error) {
	runs, err := a.service.ListRecentRuns(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]api.IndexRun, 0, len(runs))
	for _, run := range runs {
		out = append(out, apiIndexRun(run))
	}
	return out, nil
}

func (a apiIndexingAdapter) ListIndexTasks(ctx context.Context, scanRunID int64, limit int32) ([]api.IndexTask, error) {
	tasks, err := a.service.ListRecentTasks(ctx, scanRunID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]api.IndexTask, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, apiIndexTask(task))
	}
	return out, nil
}

func (a apiIndexingAdapter) RetryIndexTask(ctx context.Context, taskID int64) (api.IndexTask, error) {
	task, err := a.service.RetryTask(ctx, taskID)
	if err != nil {
		return api.IndexTask{}, err
	}
	return apiIndexTask(task), nil
}

func (a apiIndexingAdapter) CancelIndexRun(ctx context.Context, scanRunID int64) (api.IndexRun, error) {
	run, err := a.service.CancelRun(ctx, scanRunID)
	if err != nil {
		return api.IndexRun{}, err
	}
	return apiIndexRun(run), nil
}

func (a apiIndexingAdapter) ListVerificationFindings(ctx context.Context, nexusInstanceID int64, status string, limit int32) ([]api.VerificationFinding, error) {
	findings, err := a.service.ListVerificationFindings(ctx, nexusInstanceID, status, limit)
	if err != nil {
		return nil, err
	}
	out := make([]api.VerificationFinding, 0, len(findings))
	for _, finding := range findings {
		out = append(out, apiVerificationFinding(finding))
	}
	return out, nil
}

func (a apiIndexingAdapter) DismissVerificationFinding(ctx context.Context, findingID int64, dismissedBy string) (api.VerificationFinding, error) {
	finding, err := a.service.DismissVerificationFinding(ctx, findingID, dismissedBy)
	if err != nil {
		return api.VerificationFinding{}, err
	}
	return apiVerificationFinding(finding), nil
}

func (a apiIndexingAdapter) PrometheusMetrics(ctx context.Context) (string, error) {
	return a.service.PrometheusMetrics(ctx)
}

func (a apiIndexingAdapter) ListNexusInstances(ctx context.Context, limit int32) ([]api.NexusInstance, error) {
	instances, err := a.service.ListNexusInstances(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]api.NexusInstance, 0, len(instances))
	for _, instance := range instances {
		out = append(out, apiNexusInstance(instance))
	}
	return out, nil
}

func (a apiIndexingAdapter) ListIndexingSchedules(ctx context.Context, nexusInstanceID int64, limit int32) ([]api.IndexingSchedule, error) {
	schedules, err := a.service.ListSchedules(ctx, nexusInstanceID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]api.IndexingSchedule, 0, len(schedules))
	for _, schedule := range schedules {
		out = append(out, apiIndexingSchedule(schedule))
	}
	return out, nil
}

func (a apiIndexingAdapter) UpsertIndexingSchedule(ctx context.Context, req api.UpsertIndexingScheduleRequest) (api.IndexingSchedule, error) {
	schedule, err := a.service.UpsertSchedule(ctx, indexing.ScheduleRequest{
		NexusName:       req.NexusName,
		BaseURL:         req.BaseURL,
		Name:            req.Name,
		ScanType:        req.ScanType,
		IntervalSeconds: req.IntervalSeconds,
		Enabled:         req.Enabled,
		DueNow:          req.DueNow,
		Scope: indexing.RunScope{
			Repositories: req.Scope.Repositories,
			WindowDays:   req.Scope.WindowDays,
		},
	})
	if err != nil {
		return api.IndexingSchedule{}, err
	}
	schedules, err := a.service.ListSchedules(ctx, schedule.NexusInstanceID, 200)
	if err != nil {
		return api.IndexingSchedule{}, err
	}
	for _, row := range schedules {
		if row.ID == schedule.ID {
			return apiIndexingSchedule(row), nil
		}
	}
	return api.IndexingSchedule{
		ID:              schedule.ID,
		NexusInstanceID: schedule.NexusInstanceID,
		ScanType:        schedule.ScanType,
		Name:            schedule.Name,
		Enabled:         schedule.Enabled,
		IntervalSeconds: schedule.IntervalSeconds,
		ScheduleExpr:    schedule.ScheduleExpr,
		Timezone:        schedule.Timezone,
		Scope:           decodeRunScope(schedule.Scope),
	}, nil
}

func (a apiIndexingAdapter) UpdateNexusRateLimits(ctx context.Context, nexusInstanceID int64, req api.RateLimitSettings) (api.IndexingControl, error) {
	control, err := a.service.UpdateRateLimits(ctx, nexusInstanceID, indexing.RateLimitSettings{
		AssetListRPS:        req.AssetListRPS,
		RegistryManifestRPS: req.RegistryManifestRPS,
		DefaultRPS:          req.DefaultRPS,
	})
	if err != nil {
		return api.IndexingControl{}, err
	}
	return apiIndexingControl(control), nil
}

func (a apiIndexingAdapter) GetRetentionSettings(ctx context.Context) (api.RetentionSettings, error) {
	settings, err := a.service.GetRetentionSettings(ctx, a.retentionDefaults)
	if err != nil {
		return api.RetentionSettings{}, err
	}
	return apiRetentionSettings(settings), nil
}

func (a apiIndexingAdapter) UpdateRetentionSettings(ctx context.Context, req api.RetentionSettings) (api.RetentionSettings, error) {
	settings, err := a.service.UpsertRetentionSettings(ctx, indexing.RetentionSettings{
		IntervalSeconds:         int(req.IntervalSeconds),
		ScanRunDays:             int(req.ScanRunDays),
		CleanupExecutionDays:    int(req.CleanupExecutionDays),
		VerificationFindingDays: int(req.VerificationFindingDays),
		TrendSnapshotDays:       int(req.TrendSnapshotDays),
		UpdatedBy:               req.UpdatedBy,
	})
	if err != nil {
		return api.RetentionSettings{}, err
	}
	return apiRetentionSettings(settings), nil
}

func (a apiIndexingAdapter) GetAnalyticsOverview(ctx context.Context, nexusInstanceID int64) (api.AnalyticsOverview, error) {
	row, err := a.service.GetAnalyticsOverview(ctx, nexusInstanceID)
	if err != nil {
		return api.AnalyticsOverview{}, err
	}
	return apiAnalyticsOverview(row), nil
}

func (a apiIndexingAdapter) ListRepositoryAnalytics(ctx context.Context, req api.AnalyticsListRequest) ([]api.RepositoryAnalytics, error) {
	rows, err := a.service.ListRepositoryAnalytics(ctx, indexing.AnalyticsListRequest{
		NexusInstanceID: req.NexusInstanceID,
		Search:          req.Search,
		Limit:           req.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]api.RepositoryAnalytics, 0, len(rows))
	for _, row := range rows {
		out = append(out, apiRepositoryAnalytics(row))
	}
	return out, nil
}

func (a apiIndexingAdapter) ListImageAnalytics(ctx context.Context, req api.AnalyticsListRequest) ([]api.ImageAnalytics, error) {
	rows, err := a.service.ListImageAnalytics(ctx, indexing.AnalyticsListRequest{
		NexusInstanceID: req.NexusInstanceID,
		RepositoryName:  req.RepositoryName,
		Search:          req.Search,
		Limit:           req.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]api.ImageAnalytics, 0, len(rows))
	for _, row := range rows {
		out = append(out, apiImageAnalytics(row))
	}
	return out, nil
}

func (a apiIndexingAdapter) ListTagAnalytics(ctx context.Context, req api.AnalyticsListRequest) ([]api.TagAnalytics, error) {
	rows, err := a.service.ListTagAnalytics(ctx, indexing.AnalyticsListRequest{
		NexusInstanceID: req.NexusInstanceID,
		RepositoryName:  req.RepositoryName,
		Search:          req.Search,
		OlderThanDays:   req.OlderThanDays,
		NeverDownloaded: req.NeverDownloaded,
		Limit:           req.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]api.TagAnalytics, 0, len(rows))
	for _, row := range rows {
		out = append(out, apiTagAnalytics(row))
	}
	return out, nil
}

func (a apiIndexingAdapter) ListPackageFormatRepositories(ctx context.Context, req api.AnalyticsListRequest) ([]api.PackageFormatRepository, error) {
	rows, err := a.service.ListPackageFormatRepositories(ctx, indexing.AnalyticsListRequest{
		NexusInstanceID: req.NexusInstanceID,
		Search:          req.Search,
		Limit:           req.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]api.PackageFormatRepository, 0, len(rows))
	for _, row := range rows {
		out = append(out, apiPackageFormatRepository(row))
	}
	return out, nil
}

func (a apiIndexingAdapter) ListTrendSeries(ctx context.Context, req api.TrendSeriesRequest) ([]api.TrendPoint, error) {
	rows, err := a.service.ListTrendSeries(ctx, indexing.TrendSeriesRequest{
		NexusInstanceID: req.NexusInstanceID,
		RepositoryID:    req.RepositoryID,
		ImageID:         req.ImageID,
		MetricName:      req.MetricName,
		PatternName:     req.PatternName,
		StartDate:       req.StartDate,
		EndDate:         req.EndDate,
	})
	if err != nil {
		return nil, err
	}
	out := make([]api.TrendPoint, 0, len(rows))
	for _, row := range rows {
		out = append(out, apiTrendPoint(row))
	}
	return out, nil
}

func (a apiIndexingAdapter) ListTrendTopMovers(ctx context.Context, req api.TrendTopMoversRequest) ([]api.TrendTopMover, error) {
	rows, err := a.service.ListTrendTopMovers(ctx, indexing.TrendTopMoversRequest{
		NexusInstanceID: req.NexusInstanceID,
		RepositoryID:    req.RepositoryID,
		ImageID:         req.ImageID,
		MetricName:      req.MetricName,
		PatternName:     req.PatternName,
		StartDate:       req.StartDate,
		EndDate:         req.EndDate,
		Limit:           req.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]api.TrendTopMover, 0, len(rows))
	for _, row := range rows {
		out = append(out, apiTrendTopMover(row))
	}
	return out, nil
}

func (a apiIndexingAdapter) SimulateCleanupPolicy(ctx context.Context, req api.CleanupSimulationRequest) (api.CleanupSimulationResult, error) {
	result, err := a.service.SimulateCleanupPolicy(ctx, indexing.CleanupSimulationRequest{
		NexusInstanceID: req.NexusInstanceID,
		RepositoryName:  req.RepositoryName,
		PolicyYAML:      req.PolicyYAML,
		Limit:           req.Limit,
	})
	if err != nil {
		return api.CleanupSimulationResult{}, err
	}
	return apiCleanupSimulationResult(result), nil
}

func (a apiIndexingAdapter) ListCleanupExecutions(ctx context.Context, nexusInstanceID int64, limit int32) ([]api.CleanupExecution, error) {
	rows, err := a.service.ListCleanupExecutions(ctx, nexusInstanceID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]api.CleanupExecution, 0, len(rows))
	for _, row := range rows {
		out = append(out, apiCleanupExecution(row))
	}
	return out, nil
}

func (a apiIndexingAdapter) RequestCleanupExecution(ctx context.Context, req api.CleanupExecutionRequest) (api.CleanupExecution, error) {
	row, err := a.service.RequestCleanupExecution(ctx, indexing.CleanupExecutionRequest{
		NexusInstanceID:        req.NexusInstanceID,
		RepositoryName:         req.RepositoryName,
		PolicyYAML:             req.PolicyYAML,
		ApprovalText:           req.ApprovalText,
		RequestedBy:            req.RequestedBy,
		DestructiveEnabled:     a.destructiveCleanupEnabled,
		NexusUsername:          a.nexusUsername,
		NexusPassword:          a.nexusPassword,
		DeleteRPS:              a.cleanupDeleteRPS,
		MaintenanceWindowStart: a.cleanupMaintenanceStartUTC,
		MaintenanceWindowEnd:   a.cleanupMaintenanceEndUTC,
		Limit:                  req.Limit,
	})
	if err != nil {
		return api.CleanupExecution{}, err
	}
	return apiCleanupExecution(row), nil
}

func (a apiIndexingAdapter) CancelCleanupExecution(ctx context.Context, cleanupExecutionID int64, canceledBy string) (api.CleanupExecution, error) {
	row, err := a.service.CancelCleanupExecution(ctx, cleanupExecutionID, canceledBy)
	if err != nil {
		return api.CleanupExecution{}, err
	}
	return apiCleanupExecution(row), nil
}

func (a apiIndexingAdapter) ListCleanupExecutionEvents(ctx context.Context, cleanupExecutionID int64, limit int32) ([]api.CleanupExecutionEvent, error) {
	rows, err := a.service.ListCleanupExecutionEvents(ctx, cleanupExecutionID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]api.CleanupExecutionEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, apiCleanupExecutionEvent(row))
	}
	return out, nil
}

func (a apiIndexingAdapter) ListTagPatterns(ctx context.Context, limit int32) ([]api.TagPattern, error) {
	rows, err := a.service.ListTagPatterns(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]api.TagPattern, 0, len(rows))
	for _, row := range rows {
		out = append(out, apiTagPattern(row))
	}
	return out, nil
}

func (a apiIndexingAdapter) UpsertTagPattern(ctx context.Context, req api.UpsertTagPatternRequest) (api.TagPattern, error) {
	row, err := a.service.UpsertTagPattern(ctx, indexing.TagPatternRequest{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		TagRegex:    req.TagRegex,
		Priority:    req.Priority,
		Enabled:     req.Enabled,
	})
	if err != nil {
		return api.TagPattern{}, err
	}
	return apiTagPattern(row), nil
}

func (a apiIndexingAdapter) StartTagReclassification(ctx context.Context, req api.TagReclassificationRequest) (api.IndexRun, error) {
	run, err := a.service.EnqueueTagReclassificationRun(ctx, indexing.TagReclassificationRequest{
		NexusInstanceID: req.NexusInstanceID,
		RepositoryName:  req.RepositoryName,
	})
	if err != nil {
		return api.IndexRun{}, err
	}
	return apiIndexRun(run), nil
}

func (a apiIndexingAdapter) ListTagPatternAnalytics(ctx context.Context, req api.TagPatternListRequest) ([]api.TagPatternAnalytics, error) {
	rows, err := a.service.ListTagPatternAnalytics(ctx, indexing.TagPatternListRequest{
		NexusInstanceID:  req.NexusInstanceID,
		RepositoryName:   req.RepositoryName,
		PatternName:      req.PatternName,
		OlderThanDays:    req.OlderThanDays,
		GrowthWindowDays: req.GrowthWindowDays,
		NeverDownloaded:  req.NeverDownloaded,
		Limit:            req.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]api.TagPatternAnalytics, 0, len(rows))
	for _, row := range rows {
		out = append(out, apiTagPatternAnalytics(row))
	}
	return out, nil
}

func (a apiIndexingAdapter) ListTagPatternGrowth(ctx context.Context, req api.TagPatternListRequest) ([]api.TagPatternGrowth, error) {
	rows, err := a.service.ListTagPatternGrowth(ctx, indexing.TagPatternListRequest{
		NexusInstanceID:  req.NexusInstanceID,
		RepositoryName:   req.RepositoryName,
		PatternName:      req.PatternName,
		GrowthWindowDays: req.GrowthWindowDays,
		NeverDownloaded:  req.NeverDownloaded,
		Limit:            req.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]api.TagPatternGrowth, 0, len(rows))
	for _, row := range rows {
		out = append(out, apiTagPatternGrowth(row))
	}
	return out, nil
}

func (a apiIndexingAdapter) ListTagPatternExamples(ctx context.Context, req api.TagPatternListRequest) ([]api.TagPatternExample, error) {
	rows, err := a.service.ListTagPatternExamples(ctx, indexing.TagPatternListRequest{
		NexusInstanceID: req.NexusInstanceID,
		RepositoryName:  req.RepositoryName,
		PatternName:     req.PatternName,
		Limit:           req.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]api.TagPatternExample, 0, len(rows))
	for _, row := range rows {
		out = append(out, apiTagPatternExample(row))
	}
	return out, nil
}

func (a apiIndexingAdapter) ListTagPatternSuggestions(ctx context.Context, req api.TagPatternListRequest) ([]api.TagPatternSuggestion, error) {
	rows, err := a.service.ListTagPatternSuggestions(ctx, indexing.TagPatternListRequest{
		NexusInstanceID: req.NexusInstanceID,
		RepositoryName:  req.RepositoryName,
		Limit:           req.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]api.TagPatternSuggestion, 0, len(rows))
	for _, row := range rows {
		out = append(out, apiTagPatternSuggestion(row))
	}
	return out, nil
}

func (a apiIndexingAdapter) PauseNexusIndexing(ctx context.Context, nexusInstanceID int64, reason, pausedBy string) (api.IndexingControl, error) {
	control, err := a.service.PauseNexusIndexing(ctx, nexusInstanceID, reason, pausedBy)
	if err != nil {
		return api.IndexingControl{}, err
	}
	return apiIndexingControl(control), nil
}

func (a apiIndexingAdapter) ResumeNexusIndexing(ctx context.Context, nexusInstanceID int64) (api.IndexingControl, error) {
	control, err := a.service.ResumeNexusIndexing(ctx, nexusInstanceID)
	if err != nil {
		return api.IndexingControl{}, err
	}
	return apiIndexingControl(control), nil
}

func apiIndexRun(run db.ScanRun) api.IndexRun {
	out := api.IndexRun{
		ID:              run.ID,
		NexusInstanceID: run.NexusInstanceID,
		ScanType:        run.ScanType,
		Status:          run.Status,
		Phase:           run.Phase.String,
		CompletedTasks:  run.CompletedTasks,
		FailedTasks:     run.FailedTasks,
	}
	if run.StartedAt.Valid {
		out.StartedAt = run.StartedAt.Time
	}
	if run.FinishedAt.Valid {
		finishedAt := run.FinishedAt.Time
		out.FinishedAt = &finishedAt
	}
	return out
}

func apiIndexTask(task db.ScanTask) api.IndexTask {
	out := api.IndexTask{
		ID:              task.ID,
		ScanRunID:       task.ScanRunID,
		NexusInstanceID: task.NexusInstanceID,
		TaskType:        task.TaskType,
		TaskKey:         task.TaskKey,
		RepositoryName:  task.RepositoryName.String,
		Subject:         task.Subject.String,
		CostClass:       task.CostClass,
		Status:          task.Status,
		Attempt:         task.Attempt,
		MaxAttempts:     task.MaxAttempts,
		LockedBy:        task.LockedBy.String,
		ErrorCode:       task.ErrorCode.String,
		ErrorClass:      task.ErrorClass.String,
		ErrorMessage:    task.ErrorMessage.String,
	}
	if task.AvailableAt.Valid {
		out.AvailableAt = task.AvailableAt.Time
	}
	if task.StartedAt.Valid {
		startedAt := task.StartedAt.Time
		out.StartedAt = &startedAt
	}
	if task.FinishedAt.Valid {
		finishedAt := task.FinishedAt.Time
		out.FinishedAt = &finishedAt
	}
	if len(task.ErrorDetails) > 0 {
		var details any
		if err := json.Unmarshal(task.ErrorDetails, &details); err == nil {
			out.ErrorDetails = details
		}
	}
	return out
}

func apiNexusInstance(instance db.ListNexusInstancesRow) api.NexusInstance {
	out := api.NexusInstance{
		ID:      instance.ID,
		Name:    instance.Name,
		BaseURL: instance.BaseUrl,
	}
	if instance.CreatedAt.Valid {
		out.CreatedAt = instance.CreatedAt.Time
	}
	if instance.UpdatedAt.Valid {
		out.UpdatedAt = instance.UpdatedAt.Time
	}
	return out
}

func apiIndexingSchedule(schedule db.ListIndexingSchedulesRow) api.IndexingSchedule {
	out := api.IndexingSchedule{
		ID:                   schedule.ID,
		NexusInstanceID:      schedule.NexusInstanceID,
		NexusName:            schedule.NexusName,
		NexusBaseURL:         schedule.NexusBaseUrl,
		ScanType:             schedule.ScanType,
		Name:                 schedule.Name,
		Enabled:              schedule.Enabled,
		ScheduleExpr:         schedule.ScheduleExpr,
		IntervalSeconds:      schedule.IntervalSeconds,
		Timezone:             schedule.Timezone,
		Scope:                decodeRunScope(schedule.Scope),
		LockConflictBehavior: schedule.LockConflictBehavior,
	}
	if schedule.NextRunAt.Valid {
		nextRunAt := schedule.NextRunAt.Time
		out.NextRunAt = &nextRunAt
	}
	if schedule.LastRunAt.Valid {
		lastRunAt := schedule.LastRunAt.Time
		out.LastRunAt = &lastRunAt
	}
	if schedule.CreatedAt.Valid {
		out.CreatedAt = schedule.CreatedAt.Time
	}
	if schedule.UpdatedAt.Valid {
		out.UpdatedAt = schedule.UpdatedAt.Time
	}
	return out
}

func decodeRunScope(data []byte) api.RunScope {
	var scope api.RunScope
	if len(data) > 0 {
		_ = json.Unmarshal(data, &scope)
	}
	return scope
}

func apiAnalyticsOverview(row db.GetAnalyticsOverviewRow) api.AnalyticsOverview {
	return api.AnalyticsOverview{
		RepositoryCount:          row.RepositoryCount,
		ImageCount:               row.ImageCount,
		ActiveTagCount:           row.ActiveTagCount,
		TagsMissingDigestCount:   row.TagsMissingDigestCount,
		TagsMissingManifestCount: row.TagsMissingManifestCount,
		NexusAssetBytes:          row.NexusAssetBytes,
		DockerLogicalBytes:       row.DockerLogicalBytes,
		DockerUniqueLayerBytes:   row.DockerUniqueLayerBytes,
		LastSuccessfulRunAt:      timePtrFromAny(row.LastSuccessfulRunAt),
	}
}

func apiRepositoryAnalytics(row db.ListRepositoryAnalyticsRow) api.RepositoryAnalytics {
	return api.RepositoryAnalytics{
		RepositoryID:           row.RepositoryID,
		NexusInstanceID:        row.NexusInstanceID,
		NexusName:              row.NexusName,
		RepositoryName:         row.RepositoryName,
		ImageCount:             row.ImageCount,
		ActiveTagCount:         row.ActiveTagCount,
		NexusAssetBytes:        row.NexusAssetBytes,
		DockerLogicalBytes:     row.DockerLogicalBytes,
		DockerUniqueLayerBytes: row.DockerUniqueLayerBytes,
		LastSeenAt:             timePtrFromAny(row.LastSeenAt),
	}
}

func apiImageAnalytics(row db.ListImageAnalyticsRow) api.ImageAnalytics {
	return api.ImageAnalytics{
		ImageID:                row.ImageID,
		RepositoryID:           row.RepositoryID,
		NexusInstanceID:        row.NexusInstanceID,
		NexusName:              row.NexusName,
		RepositoryName:         row.RepositoryName,
		ImageName:              row.ImageName,
		ActiveTagCount:         row.ActiveTagCount,
		DockerLogicalBytes:     row.DockerLogicalBytes,
		DockerUniqueLayerBytes: row.DockerUniqueLayerBytes,
		OldestTagFirstSeenAt:   timePtrFromAny(row.OldestTagFirstSeenAt),
		NewestTagLastSeenAt:    timePtrFromAny(row.NewestTagLastSeenAt),
	}
}

func apiTagAnalytics(row db.ListTagAnalyticsRow) api.TagAnalytics {
	out := api.TagAnalytics{
		TagID:                     row.TagID,
		ImageID:                   row.ImageID,
		RepositoryID:              row.RepositoryID,
		NexusInstanceID:           row.NexusInstanceID,
		NexusName:                 row.NexusName,
		RepositoryName:            row.RepositoryName,
		ImageName:                 row.ImageName,
		Tag:                       row.Tag,
		Digest:                    row.Digest,
		MediaType:                 row.MediaType,
		NexusTagSizeBytes:         row.NexusTagSizeBytes,
		DockerLogicalBytes:        row.DockerLogicalBytes,
		DockerLayerBytes:          row.DockerLayerBytes,
		EstimatedReclaimableBytes: row.EstimatedReclaimableBytes,
		SharedLayerBytes:          row.SharedLayerBytes,
	}
	if row.FirstSeenAt.Valid {
		out.FirstSeenAt = row.FirstSeenAt.Time
	}
	if row.LastSeenAt.Valid {
		out.LastSeenAt = row.LastSeenAt.Time
	}
	out.LastDownloadedAt = timePtrFromAny(row.LastDownloadedAt)
	return out
}

func apiPackageFormatRepository(row db.ListPackageFormatRepositoriesRow) api.PackageFormatRepository {
	out := api.PackageFormatRepository{
		RepositoryID:    row.RepositoryID,
		NexusInstanceID: row.NexusInstanceID,
		NexusName:       row.NexusName,
		RepositoryName:  row.RepositoryName,
		Format:          row.Format,
		Type:            row.Type,
		SupportStatus:   row.SupportStatus,
		SupportReason:   row.SupportReason,
		AssetCount:      row.AssetCount,
		AssetBytes:      row.AssetBytes,
	}
	if row.LastSeenAt.Valid {
		out.LastSeenAt = &row.LastSeenAt.Time
	}
	out.LastAssetSeenAt = timePtrFromAny(row.LastAssetSeenAt)
	return out
}

func apiTrendPoint(row db.ListTrendSeriesRow) api.TrendPoint {
	out := api.TrendPoint{Missing: row.Missing}
	if row.SnapshotDate.Valid {
		out.SnapshotDate = row.SnapshotDate.Time.Format("2006-01-02")
	}
	if row.CapturedAt.Valid {
		out.CapturedAt = &row.CapturedAt.Time
	}
	if row.MetricValue.Valid {
		if value, err := row.MetricValue.Float64Value(); err == nil && value.Valid {
			out.MetricValue = &value.Float64
		}
	}
	return out
}

func apiTrendTopMover(row db.ListTrendTopMoversRow) api.TrendTopMover {
	out := api.TrendTopMover{
		RepositoryID:   row.RepositoryID.Int64,
		RepositoryName: row.RepositoryName.String,
		StartValue:     numericFloat(row.StartValue),
		EndValue:       numericFloat(row.EndValue),
		DeltaValue:     numericFloat(row.DeltaValue),
		DeltaPercent:   numericFloatPtr(row.DeltaPercent),
	}
	if row.ImageID.Valid {
		imageID := row.ImageID.Int64
		out.ImageID = &imageID
	}
	if row.ImageName.Valid {
		out.ImageName = row.ImageName.String
	}
	return out
}

func apiCleanupSimulationResult(result indexing.CleanupSimulationResult) api.CleanupSimulationResult {
	out := api.CleanupSimulationResult{
		PolicyName:                result.PolicyName,
		EvaluatedTagCount:         result.EvaluatedTagCount,
		CandidateTagCount:         result.CandidateTagCount,
		ProtectedTagCount:         result.ProtectedTagCount,
		WarningCount:              result.WarningCount,
		EstimatedReclaimableBytes: result.EstimatedReclaimableBytes,
		Candidates:                make([]api.CleanupSimulationTag, 0, len(result.Candidates)),
		Protected:                 make([]api.CleanupSimulationTag, 0, len(result.Protected)),
		Warnings:                  make([]api.CleanupSimulationWarning, 0, len(result.Warnings)),
	}
	for _, tag := range result.Candidates {
		out.Candidates = append(out.Candidates, apiCleanupSimulationTag(tag))
	}
	for _, tag := range result.Protected {
		out.Protected = append(out.Protected, apiCleanupSimulationTag(tag))
	}
	for _, warning := range result.Warnings {
		out.Warnings = append(out.Warnings, api.CleanupSimulationWarning{
			Tag:  apiCleanupSimulationTag(warning.Tag),
			Code: warning.Code,
		})
	}
	return out
}

func apiCleanupSimulationTag(tag indexing.CleanupSimulationTag) api.CleanupSimulationTag {
	return api.CleanupSimulationTag{
		TagID:                     tag.TagID,
		ImageID:                   tag.ImageID,
		RepositoryID:              tag.RepositoryID,
		NexusInstanceID:           tag.NexusInstanceID,
		RepositoryName:            tag.RepositoryName,
		ImageName:                 tag.ImageName,
		Tag:                       tag.Tag,
		Digest:                    tag.Digest,
		PatternName:               tag.PatternName,
		RuleName:                  tag.RuleName,
		Reason:                    tag.Reason,
		DockerLogicalBytes:        tag.DockerLogicalBytes,
		EstimatedReclaimableBytes: tag.EstimatedReclaimableBytes,
		FirstSeenAt:               tag.FirstSeenAt,
		LastSeenAt:                tag.LastSeenAt,
		LastDownloadedAt:          tag.LastDownloadedAt,
	}
}

func apiCleanupExecution(row db.CleanupExecution) api.CleanupExecution {
	out := api.CleanupExecution{
		ID:                     row.ID,
		NexusInstanceID:        row.NexusInstanceID,
		PolicyName:             row.PolicyName,
		Status:                 row.Status,
		RequestedBy:            row.RequestedBy,
		DestructiveEnabled:     row.DestructiveEnabled,
		SimulationSummary:      jsonValue(row.SimulationSummary),
		AuditLog:               jsonValue(row.AuditLog),
		ApprovedCandidateCount: row.ApprovedCandidateCount,
	}
	if row.RepositoryName.Valid {
		out.RepositoryName = row.RepositoryName.String
	}
	if row.ApprovedBy.Valid {
		out.ApprovedBy = row.ApprovedBy.String
	}
	if row.PostDeleteVerificationRunID.Valid {
		id := row.PostDeleteVerificationRunID.Int64
		out.PostDeleteVerificationRunID = &id
	}
	if row.PostDeleteReconcileRunID.Valid {
		id := row.PostDeleteReconcileRunID.Int64
		out.PostDeleteReconcileRunID = &id
	}
	if row.CreatedAt.Valid {
		out.CreatedAt = row.CreatedAt.Time
	}
	if row.ApprovedAt.Valid {
		out.ApprovedAt = &row.ApprovedAt.Time
	}
	if row.StartedAt.Valid {
		out.StartedAt = &row.StartedAt.Time
	}
	if row.FinishedAt.Valid {
		out.FinishedAt = &row.FinishedAt.Time
	}
	if row.CancelRequestedAt.Valid {
		out.CancelRequestedAt = &row.CancelRequestedAt.Time
	}
	if row.CanceledBy.Valid {
		out.CanceledBy = row.CanceledBy.String
	}
	if row.LockedBy.Valid {
		out.LockedBy = row.LockedBy.String
	}
	if row.LeaseExpiresAt.Valid {
		out.LeaseExpiresAt = &row.LeaseExpiresAt.Time
	}
	return out
}

func apiCleanupExecutionEvent(row db.CleanupExecutionEvent) api.CleanupExecutionEvent {
	out := api.CleanupExecutionEvent{
		ID:                 row.ID,
		CleanupExecutionID: row.CleanupExecutionID,
		EventType:          row.EventType,
		Details:            jsonValue(row.Details),
	}
	if row.Actor.Valid {
		out.Actor = row.Actor.String
	}
	if row.RepositoryName.Valid {
		out.RepositoryName = row.RepositoryName.String
	}
	if row.ImageName.Valid {
		out.ImageName = row.ImageName.String
	}
	if row.Tag.Valid {
		out.Tag = row.Tag.String
	}
	if row.TagID.Valid {
		out.TagID = row.TagID.Int64
	}
	if row.RuleName.Valid {
		out.RuleName = row.RuleName.String
	}
	if row.ComponentID.Valid {
		out.ComponentID = row.ComponentID.String
	}
	if row.Status.Valid {
		out.Status = row.Status.String
	}
	if row.Message.Valid {
		out.Message = row.Message.String
	}
	if row.ErrorClass.Valid {
		out.ErrorClass = row.ErrorClass.String
	}
	if row.ErrorCode.Valid {
		out.ErrorCode = row.ErrorCode.String
	}
	if row.ErrorMessage.Valid {
		out.ErrorMessage = row.ErrorMessage.String
	}
	if row.CreatedAt.Valid {
		out.CreatedAt = row.CreatedAt.Time
	}
	return out
}

func apiTagPattern(row db.TagPattern) api.TagPattern {
	out := api.TagPattern{
		ID:          row.ID,
		Name:        row.Name,
		DisplayName: row.DisplayName,
		Description: row.Description,
		TagRegex:    row.TagRegex,
		Priority:    row.Priority,
		Enabled:     row.Enabled,
		BuiltIn:     row.BuiltIn,
	}
	if row.CreatedAt.Valid {
		out.CreatedAt = row.CreatedAt.Time
	}
	if row.UpdatedAt.Valid {
		out.UpdatedAt = row.UpdatedAt.Time
	}
	return out
}

func apiTagPatternAnalytics(row db.ListTagPatternAnalyticsRow) api.TagPatternAnalytics {
	return api.TagPatternAnalytics{
		PatternName:          row.PatternName,
		ActiveTagCount:       row.ActiveTagCount,
		DockerLogicalBytes:   row.DockerLogicalBytes,
		OldestTagFirstSeenAt: timePtrFromAny(row.OldestTagFirstSeenAt),
		NeverDownloadedCount: row.NeverDownloadedCount,
	}
}

func apiTagPatternGrowth(row db.ListTagPatternGrowthRow) api.TagPatternGrowth {
	return api.TagPatternGrowth{
		RepositoryName:       row.RepositoryName,
		ImageNamespace:       row.ImageNamespace,
		PatternName:          row.PatternName,
		ActiveTagCount:       row.ActiveTagCount,
		DockerLogicalBytes:   row.DockerLogicalBytes,
		GrowthTagCount:       row.GrowthTagCount,
		GrowthLogicalBytes:   row.GrowthLogicalBytes,
		NeverDownloadedCount: row.NeverDownloadedCount,
		OldestTagFirstSeenAt: timePtrFromAny(row.OldestTagFirstSeenAt),
	}
}

func apiTagPatternExample(row db.ListTagPatternExamplesRow) api.TagPatternExample {
	out := api.TagPatternExample{
		PatternName:    row.PatternName,
		RepositoryName: row.RepositoryName,
		ImageName:      row.ImageName,
		Tag:            row.Tag,
	}
	if row.FirstSeenAt.Valid {
		out.FirstSeenAt = row.FirstSeenAt.Time
	}
	if row.LastSeenAt.Valid {
		out.LastSeenAt = row.LastSeenAt.Time
	}
	return out
}

func apiTagPatternSuggestion(row db.ListTagPatternSuggestionsRow) api.TagPatternSuggestion {
	return api.TagPatternSuggestion{
		TagShape:       row.TagShape,
		TagCount:       row.TagCount,
		ExampleTags:    row.ExampleTags,
		SuggestedRegex: stringFromAny(row.SuggestedRegex),
	}
}

func apiVerificationFinding(finding db.VerificationFinding) api.VerificationFinding {
	out := api.VerificationFinding{
		ID:             finding.ID,
		Severity:       finding.Severity,
		FindingType:    finding.FindingType,
		RepositoryName: finding.RepositoryName.String,
		Subject:        finding.Subject.String,
		Message:        finding.Message.String,
		Remediation:    finding.Remediation.String,
		Status:         finding.Status,
	}
	if finding.NexusInstanceID.Valid {
		out.NexusInstanceID = finding.NexusInstanceID.Int64
	}
	if finding.ScanRunID.Valid {
		out.ScanRunID = finding.ScanRunID.Int64
	}
	if finding.CreatedAt.Valid {
		out.CreatedAt = finding.CreatedAt.Time
	}
	if finding.LastSeenAt.Valid {
		out.LastSeenAt = finding.LastSeenAt.Time
	}
	if finding.ResolvedAt.Valid {
		resolvedAt := finding.ResolvedAt.Time
		out.ResolvedAt = &resolvedAt
	}
	if finding.DismissedAt.Valid {
		dismissedAt := finding.DismissedAt.Time
		out.DismissedAt = &dismissedAt
	}
	out.Expected = decodeJSONValue(finding.Expected)
	out.Actual = decodeJSONValue(finding.Actual)
	out.Metadata = decodeJSONValue(finding.Metadata)
	return out
}

func apiIndexingControl(control db.NexusIndexingControl) api.IndexingControl {
	out := api.IndexingControl{
		NexusInstanceID:     control.NexusInstanceID,
		Paused:              control.Paused,
		CircuitFailureCount: control.CircuitFailureCount,
		PausedReason:        control.PausedReason.String,
		PausedBy:            control.PausedBy.String,
		LastFailureCode:     control.LastFailureCode.String,
		LastFailureClass:    control.LastFailureClass.String,
	}
	if control.PausedAt.Valid {
		pausedAt := control.PausedAt.Time
		out.PausedAt = &pausedAt
	}
	if control.CircuitOpenUntil.Valid {
		openUntil := control.CircuitOpenUntil.Time
		out.CircuitOpenUntil = &openUntil
	}
	out.AssetListRPS = numericFloatPtr(control.AssetListRps)
	out.RegistryManifestRPS = numericFloatPtr(control.RegistryManifestRps)
	out.DefaultRPS = numericFloatPtr(control.DefaultRps)
	return out
}

func apiRetentionSettings(settings indexing.RetentionSettings) api.RetentionSettings {
	return api.RetentionSettings{
		IntervalSeconds:         int32(settings.IntervalSeconds),
		ScanRunDays:             int32(settings.ScanRunDays),
		CleanupExecutionDays:    int32(settings.CleanupExecutionDays),
		VerificationFindingDays: int32(settings.VerificationFindingDays),
		TrendSnapshotDays:       int32(settings.TrendSnapshotDays),
		UpdatedBy:               settings.UpdatedBy,
		UpdatedAt:               settings.UpdatedAt,
		Configured:              settings.Configured,
	}
}

func numericFloat(value pgtype.Numeric) float64 {
	if converted := numericFloatPtr(value); converted != nil {
		return *converted
	}
	return 0
}

func jsonValue(data []byte) any {
	if len(data) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil
	}
	return value
}

func numericFloatPtr(value pgtype.Numeric) *float64 {
	if !value.Valid {
		return nil
	}
	converted, err := value.Float64Value()
	if err != nil || !converted.Valid {
		return nil
	}
	return &converted.Float64
}

func timePtrFromAny(value any) *time.Time {
	switch typed := value.(type) {
	case nil:
		return nil
	case time.Time:
		return &typed
	case pgtype.Timestamptz:
		if typed.Valid {
			return &typed.Time
		}
	}
	return nil
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return ""
	}
}

func decodeJSONValue(data []byte) any {
	if len(data) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil
	}
	return value
}
