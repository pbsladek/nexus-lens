package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/pbsladek/nexus-lens/internal/db"
)

const schedulerAdvisoryLockID int64 = 56010002

type Service struct {
	queries *db.Queries
	logger  *slog.Logger
}

func NewService(queries *db.Queries, logger *slog.Logger) *Service {
	return &Service{queries: queries, logger: logger}
}

func (s *Service) EnqueueRepositoryDiscoveryRun(ctx context.Context, req EnqueueRepositoryDiscoveryRunRequest) (db.ScanRun, error) {
	if req.NexusName == "" {
		req.NexusName = "local"
	}
	if req.BaseURL == "" {
		req.BaseURL = "http://localhost:8081"
	}
	if req.ScanType == "" {
		req.ScanType = ScanTypeRecentSync
	}
	if req.TaskKey == "" {
		if req.ScanType == ScanTypeTrendSnapshot {
			req.TaskKey = "trend-snapshot/" + time.Now().UTC().Format("2006-01-02")
		} else {
			req.TaskKey = "repository-discovery"
		}
	}
	if req.TriggerSource == "" {
		req.TriggerSource = TriggerSourceManual
	}
	if req.ScanType != ScanTypeRecentSync && req.ScanType != ScanTypeBoundedReindex && req.ScanType != ScanTypeFullReconcile && req.ScanType != ScanTypeVerification && req.ScanType != ScanTypeTrendSnapshot {
		return db.ScanRun{}, fmt.Errorf("unsupported scan type %q", req.ScanType)
	}
	scope := normalizeRunScope(req.ScanType, req.Scope, time.Now())
	nexus, err := s.queries.UpsertNexusInstanceForIndexing(ctx, db.UpsertNexusInstanceForIndexingParams{
		Name:    req.NexusName,
		BaseUrl: req.BaseURL,
	})
	if err != nil {
		return db.ScanRun{}, fmt.Errorf("upsert nexus instance: %w", err)
	}
	if conflict, err := s.operationLockConflict(ctx, nexus.ID); err != nil {
		return db.ScanRun{}, err
	} else if conflict != nil {
		return db.ScanRun{}, conflict
	}
	if conflict, err := s.cleanupLockConflict(ctx, nexus.ID); err != nil {
		return db.ScanRun{}, err
	} else if conflict != nil {
		return db.ScanRun{}, conflict
	}

	run, err := s.queries.CreateScanRun(ctx, db.CreateScanRunParams{
		NexusInstanceID:     nexus.ID,
		ScanType:            req.ScanType,
		RepositoryName:      text(""),
		TriggerSource:       req.TriggerSource,
		TriggerJobName:      text(req.TriggerJobName),
		CursorBefore:        jsonBytes(map[string]any{"scope": scope}),
		EstimatedTotalTasks: int8(1, true),
		Stats:               emptyJSON,
	})
	if err != nil {
		return db.ScanRun{}, fmt.Errorf("create scan run: %w", err)
	}
	if scanTypeRequiresOperationLock(req.ScanType) {
		if _, err := s.queries.AcquireOperationLock(ctx, db.AcquireOperationLockParams{
			NexusInstanceID: nexus.ID,
			ScanRunID:       run.ID,
			OperationType:   req.ScanType,
			Reason:          text("manual run"),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				_, _ = s.queries.MarkScanRunFailed(ctx, db.MarkScanRunFailedParams{
					ID:           run.ID,
					ErrorCode:    textRequired("operation_lock_conflict"),
					ErrorClass:   textRequired("operation_lock"),
					ErrorMessage: textRequired("operation lock conflict"),
					ErrorDetails: emptyJSON,
				})
				if conflict, lockErr := s.operationLockConflict(ctx, nexus.ID); lockErr == nil && conflict != nil {
					return db.ScanRun{}, conflict
				}
				return db.ScanRun{}, &OperationLockConflict{NexusInstanceID: nexus.ID}
			}
			return db.ScanRun{}, fmt.Errorf("acquire operation lock: %w", err)
		}
	}

	taskType := TaskTypeRepositoryDiscovery
	taskKey := req.TaskKey
	costClass := "asset_list"
	if req.ScanType == ScanTypeVerification {
		taskType = TaskTypeVerificationChecks
		costClass = "verification"
	} else if req.ScanType == ScanTypeTrendSnapshot {
		taskType = TaskTypeTrendSnapshot
		costClass = "snapshot"
	}
	if _, err := s.queries.EnqueueScanTask(ctx, db.EnqueueScanTaskParams{
		ScanRunID:       run.ID,
		NexusInstanceID: nexus.ID,
		TaskType:        taskType,
		TaskKey:         taskKey,
		RepositoryName:  text(""),
		Subject:         text(""),
		CostClass:       costClass,
		Priority:        0,
		MaxAttempts:     3,
		Payload: jsonBytes(map[string]any{
			"base_url": req.BaseURL,
			"scope":    scope,
		}),
	}); err != nil {
		return db.ScanRun{}, fmt.Errorf("enqueue repository discovery task: %w", err)
	}

	s.logger.Info("scan run enqueued", "scan_run_id", run.ID, "nexus_instance_id", nexus.ID, "scan_type", run.ScanType)
	return run, nil
}

func (s *Service) UpsertSchedule(ctx context.Context, req ScheduleRequest) (db.IndexingSchedule, error) {
	if req.NexusName == "" {
		req.NexusName = "local"
	}
	if req.BaseURL == "" {
		req.BaseURL = "http://localhost:8081"
	}
	if req.Name == "" {
		req.Name = "recent-sync"
	}
	if req.ScanType == "" {
		req.ScanType = ScanTypeRecentSync
	}
	if req.IntervalSeconds <= 0 {
		req.IntervalSeconds = 1800
	}
	scope := normalizeRunScope(req.ScanType, req.Scope, time.Now())
	nexus, err := s.queries.UpsertNexusInstanceForIndexing(ctx, db.UpsertNexusInstanceForIndexingParams{
		Name:    req.NexusName,
		BaseUrl: req.BaseURL,
	})
	if err != nil {
		return db.IndexingSchedule{}, fmt.Errorf("upsert nexus instance: %w", err)
	}

	nextRunAt := time.Now().UTC().Add(time.Duration(req.IntervalSeconds) * time.Second)
	if req.DueNow {
		nextRunAt = time.Now().UTC()
	}
	schedule, err := s.queries.UpsertIndexingSchedule(ctx, db.UpsertIndexingScheduleParams{
		NexusInstanceID: nexus.ID,
		ScanType:        req.ScanType,
		Name:            req.Name,
		Enabled:         req.Enabled,
		ScheduleExpr:    fmt.Sprintf("every %ds", req.IntervalSeconds),
		IntervalSeconds: req.IntervalSeconds,
		Scope:           jsonBytes(scope),
		NextRunAt:       timestamptz(nextRunAt),
	})
	if err != nil {
		return db.IndexingSchedule{}, fmt.Errorf("upsert indexing schedule: %w", err)
	}
	return schedule, nil
}

func (s *Service) ListNexusInstances(ctx context.Context, limit int32) ([]db.ListNexusInstancesRow, error) {
	instances, err := s.queries.ListNexusInstances(ctx, boundedLimit(limit, 100))
	if err != nil {
		return nil, fmt.Errorf("list nexus instances: %w", err)
	}
	return instances, nil
}

func (s *Service) ListSchedules(ctx context.Context, nexusInstanceID int64, limit int32) ([]db.ListIndexingSchedulesRow, error) {
	var filter pgtype.Int8
	if nexusInstanceID > 0 {
		filter = int8(nexusInstanceID, true)
	}
	schedules, err := s.queries.ListIndexingSchedules(ctx, db.ListIndexingSchedulesParams{
		NexusInstanceID: filter,
		LimitCount:      boundedLimit(limit, 200),
	})
	if err != nil {
		return nil, fmt.Errorf("list indexing schedules: %w", err)
	}
	return schedules, nil
}

func (s *Service) UpdateRateLimits(ctx context.Context, nexusInstanceID int64, settings RateLimitSettings) (db.NexusIndexingControl, error) {
	control, err := s.queries.UpsertNexusRateLimits(ctx, db.UpsertNexusRateLimitsParams{
		NexusInstanceID:     nexusInstanceID,
		AssetListRps:        numericFromPtr(settings.AssetListRPS),
		RegistryManifestRps: numericFromPtr(settings.RegistryManifestRPS),
		DefaultRps:          numericFromPtr(settings.DefaultRPS),
	})
	if err != nil {
		return db.NexusIndexingControl{}, fmt.Errorf("upsert nexus rate limits: %w", err)
	}
	return control, nil
}

func (s *Service) GetRetentionSettings(ctx context.Context, defaults RetentionSettings) (RetentionSettings, error) {
	settings := normalizeRetentionSettings(defaults)
	row, err := s.queries.GetRetentionSettings(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return settings, nil
		}
		return RetentionSettings{}, fmt.Errorf("get retention settings: %w", err)
	}
	return mergeRetentionSettings(settings, row), nil
}

func (s *Service) UpsertRetentionSettings(ctx context.Context, settings RetentionSettings) (RetentionSettings, error) {
	settings = normalizeRetentionSettings(settings)
	row, err := s.queries.UpsertRetentionSettings(ctx, db.UpsertRetentionSettingsParams{
		IntervalSeconds:         optionalInt4(int32(settings.IntervalSeconds)),
		ScanRunDays:             optionalInt4(int32(settings.ScanRunDays)),
		CleanupExecutionDays:    optionalInt4(int32(settings.CleanupExecutionDays)),
		VerificationFindingDays: optionalInt4(int32(settings.VerificationFindingDays)),
		TrendSnapshotDays:       optionalInt4(int32(settings.TrendSnapshotDays)),
		UpdatedBy:               text(settings.UpdatedBy),
	})
	if err != nil {
		return RetentionSettings{}, fmt.Errorf("upsert retention settings: %w", err)
	}
	return mergeRetentionSettings(RetentionSettings{}, row), nil
}

func normalizeRetentionSettings(settings RetentionSettings) RetentionSettings {
	if settings.IntervalSeconds < 60 {
		settings.IntervalSeconds = 3600
	}
	if settings.ScanRunDays <= 0 {
		settings.ScanRunDays = 30
	}
	if settings.CleanupExecutionDays <= 0 {
		settings.CleanupExecutionDays = 365
	}
	if settings.VerificationFindingDays <= 0 {
		settings.VerificationFindingDays = 180
	}
	if settings.TrendSnapshotDays <= 0 {
		settings.TrendSnapshotDays = 365
	}
	return settings
}

func mergeRetentionSettings(defaults RetentionSettings, row db.AppRetentionSetting) RetentionSettings {
	settings := normalizeRetentionSettings(defaults)
	if row.IntervalSeconds.Valid {
		settings.IntervalSeconds = int(row.IntervalSeconds.Int32)
	}
	if row.ScanRunDays.Valid {
		settings.ScanRunDays = int(row.ScanRunDays.Int32)
	}
	if row.CleanupExecutionDays.Valid {
		settings.CleanupExecutionDays = int(row.CleanupExecutionDays.Int32)
	}
	if row.VerificationFindingDays.Valid {
		settings.VerificationFindingDays = int(row.VerificationFindingDays.Int32)
	}
	if row.TrendSnapshotDays.Valid {
		settings.TrendSnapshotDays = int(row.TrendSnapshotDays.Int32)
	}
	if row.UpdatedBy.Valid {
		settings.UpdatedBy = row.UpdatedBy.String
	}
	if row.UpdatedAt.Valid {
		updatedAt := row.UpdatedAt.Time
		settings.UpdatedAt = &updatedAt
	}
	settings.Configured = true
	return normalizeRetentionSettings(settings)
}

func (s *Service) GetAnalyticsOverview(ctx context.Context, nexusInstanceID int64) (db.GetAnalyticsOverviewRow, error) {
	row, err := s.queries.GetAnalyticsOverview(ctx, optionalInt8(nexusInstanceID))
	if err != nil {
		return db.GetAnalyticsOverviewRow{}, fmt.Errorf("get analytics overview: %w", err)
	}
	return row, nil
}

func (s *Service) ListRepositoryAnalytics(ctx context.Context, req AnalyticsListRequest) ([]db.ListRepositoryAnalyticsRow, error) {
	rows, err := s.queries.ListRepositoryAnalytics(ctx, db.ListRepositoryAnalyticsParams{
		NexusInstanceID: optionalInt8(req.NexusInstanceID),
		Search:          text(req.Search),
		LimitCount:      analyticsLimit(req.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list repository analytics: %w", err)
	}
	return rows, nil
}

func (s *Service) ListImageAnalytics(ctx context.Context, req AnalyticsListRequest) ([]db.ListImageAnalyticsRow, error) {
	rows, err := s.queries.ListImageAnalytics(ctx, db.ListImageAnalyticsParams{
		NexusInstanceID: optionalInt8(req.NexusInstanceID),
		RepositoryName:  text(req.RepositoryName),
		Search:          text(req.Search),
		LimitCount:      analyticsLimit(req.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list image analytics: %w", err)
	}
	return rows, nil
}

func (s *Service) ListTagAnalytics(ctx context.Context, req AnalyticsListRequest) ([]db.ListTagAnalyticsRow, error) {
	var olderThan pgtype.Timestamptz
	if req.OlderThanDays > 0 {
		olderThan = timestamptz(time.Now().UTC().Add(-time.Duration(req.OlderThanDays) * 24 * time.Hour))
	}
	rows, err := s.queries.ListTagAnalytics(ctx, db.ListTagAnalyticsParams{
		NexusInstanceID: optionalInt8(req.NexusInstanceID),
		RepositoryName:  text(req.RepositoryName),
		Search:          text(req.Search),
		OlderThan:       olderThan,
		NeverDownloaded: pgtype.Bool{Bool: req.NeverDownloaded, Valid: req.NeverDownloaded},
		LimitCount:      analyticsLimit(req.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list tag analytics: %w", err)
	}
	return rows, nil
}

func (s *Service) ListPackageFormatRepositories(ctx context.Context, req AnalyticsListRequest) ([]db.ListPackageFormatRepositoriesRow, error) {
	rows, err := s.queries.ListPackageFormatRepositories(ctx, db.ListPackageFormatRepositoriesParams{
		NexusInstanceID: optionalInt8(req.NexusInstanceID),
		Search:          text(req.Search),
		LimitCount:      analyticsLimit(req.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list package format repositories: %w", err)
	}
	return rows, nil
}

func (s *Service) ListTrendSeries(ctx context.Context, req TrendSeriesRequest) ([]db.ListTrendSeriesRow, error) {
	if req.MetricName == "" {
		req.MetricName = "docker_logical_bytes"
	}
	endDate, err := trendDateOrDefault(req.EndDate, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("parse trend end date: %w", err)
	}
	startDate, err := trendDateOrDefault(req.StartDate, endDate.AddDate(0, 0, -30))
	if err != nil {
		return nil, fmt.Errorf("parse trend start date: %w", err)
	}
	dimensions := emptyJSON
	if req.PatternName != "" {
		dimensions = jsonBytes(map[string]string{"pattern": req.PatternName})
	}
	rows, err := s.queries.ListTrendSeries(ctx, db.ListTrendSeriesParams{
		NexusInstanceID: req.NexusInstanceID,
		RepositoryID:    optionalInt8(req.RepositoryID),
		ImageID:         optionalInt8(req.ImageID),
		MetricName:      req.MetricName,
		Dimensions:      dimensions,
		StartDate:       pgDate(startDate),
		EndDate:         pgDate(endDate),
	})
	if err != nil {
		return nil, fmt.Errorf("list trend series: %w", err)
	}
	return rows, nil
}

func (s *Service) ListTrendTopMovers(ctx context.Context, req TrendTopMoversRequest) ([]db.ListTrendTopMoversRow, error) {
	if req.MetricName == "" {
		req.MetricName = "docker_logical_bytes"
	}
	endDate, err := trendDateOrDefault(req.EndDate, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("parse trend top movers end date: %w", err)
	}
	startDate, err := trendDateOrDefault(req.StartDate, endDate.AddDate(0, 0, -30))
	if err != nil {
		return nil, fmt.Errorf("parse trend top movers start date: %w", err)
	}
	dimensions := emptyJSON
	if req.PatternName != "" {
		dimensions = jsonBytes(map[string]string{"pattern": req.PatternName})
	}
	rows, err := s.queries.ListTrendTopMovers(ctx, db.ListTrendTopMoversParams{
		NexusInstanceID: req.NexusInstanceID,
		RepositoryID:    optionalInt8(req.RepositoryID),
		ImageID:         optionalInt8(req.ImageID),
		MetricName:      req.MetricName,
		Dimensions:      dimensions,
		StartDate:       pgDate(startDate),
		EndDate:         pgDate(endDate),
		LimitCount:      analyticsLimit(req.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list trend top movers: %w", err)
	}
	return rows, nil
}

func analyticsLimit(limit int32) int32 {
	if limit <= 0 || limit > 200 {
		return 50
	}
	return limit
}

func boundedLimit(limit, defaultLimit int32) int32 {
	if limit <= 0 || limit > defaultLimit {
		return defaultLimit
	}
	return limit
}

func trendDateOrDefault(value string, fallback time.Time) (time.Time, error) {
	if value == "" {
		return fallback, nil
	}
	return time.Parse("2006-01-02", value)
}

func (s *Service) ListTagPatterns(ctx context.Context, limit int32) ([]db.TagPattern, error) {
	patterns, err := s.queries.ListTagPatterns(ctx, boundedLimit(limit, 500))
	if err != nil {
		return nil, fmt.Errorf("list tag patterns: %w", err)
	}
	return patterns, nil
}

func (s *Service) UpsertTagPattern(ctx context.Context, req TagPatternRequest) (db.TagPattern, error) {
	if req.Name == "" {
		return db.TagPattern{}, fmt.Errorf("tag pattern name is required")
	}
	if req.DisplayName == "" {
		req.DisplayName = req.Name
	}
	if req.Priority == 0 {
		req.Priority = 500
	}
	pattern, err := s.queries.UpsertTagPattern(ctx, db.UpsertTagPatternParams{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		TagRegex:    req.TagRegex,
		Priority:    req.Priority,
		Enabled:     req.Enabled,
		BuiltIn:     false,
	})
	if err != nil {
		return db.TagPattern{}, fmt.Errorf("upsert tag pattern: %w", err)
	}
	return pattern, nil
}

func (s *Service) EnqueueTagReclassificationRun(ctx context.Context, req TagReclassificationRequest) (db.ScanRun, error) {
	if req.NexusInstanceID <= 0 {
		return db.ScanRun{}, fmt.Errorf("nexus_instance_id is required")
	}
	run, err := s.queries.CreateScanRun(ctx, db.CreateScanRunParams{
		NexusInstanceID:     req.NexusInstanceID,
		ScanType:            ScanTypeTagReclassification,
		RepositoryName:      text(req.RepositoryName),
		TriggerSource:       TriggerSourceManual,
		TriggerJobName:      text("tag-reclassification"),
		CursorBefore:        jsonBytes(map[string]any{"repository_name": req.RepositoryName}),
		EstimatedTotalTasks: int8(1, true),
		Stats:               emptyJSON,
	})
	if err != nil {
		return db.ScanRun{}, fmt.Errorf("create tag reclassification run: %w", err)
	}
	if _, err := s.queries.EnqueueScanTask(ctx, db.EnqueueScanTaskParams{
		ScanRunID:       run.ID,
		NexusInstanceID: req.NexusInstanceID,
		TaskType:        TaskTypeTagReclassification,
		TaskKey:         fmt.Sprintf("tag-reclassification/%d/%s", req.NexusInstanceID, req.RepositoryName),
		RepositoryName:  text(req.RepositoryName),
		Subject:         text(req.RepositoryName),
		CostClass:       "tag_classification",
		Priority:        -20,
		MaxAttempts:     3,
		Payload: jsonBytes(map[string]any{
			"nexus_instance_id": req.NexusInstanceID,
			"repository_name":   req.RepositoryName,
		}),
	}); err != nil {
		return db.ScanRun{}, fmt.Errorf("enqueue tag reclassification task: %w", err)
	}
	return run, nil
}

func (s *Service) ListTagPatternAnalytics(ctx context.Context, req TagPatternListRequest) ([]db.ListTagPatternAnalyticsRow, error) {
	var olderThan pgtype.Timestamptz
	if req.OlderThanDays > 0 {
		olderThan = timestamptz(time.Now().UTC().Add(-time.Duration(req.OlderThanDays) * 24 * time.Hour))
	}
	rows, err := s.queries.ListTagPatternAnalytics(ctx, db.ListTagPatternAnalyticsParams{
		NexusInstanceID: optionalInt8(req.NexusInstanceID),
		RepositoryName:  text(req.RepositoryName),
		OlderThan:       olderThan,
		NeverDownloaded: pgtype.Bool{Bool: req.NeverDownloaded, Valid: req.NeverDownloaded},
		LimitCount:      analyticsLimit(req.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list tag pattern analytics: %w", err)
	}
	return rows, nil
}

func (s *Service) ListTagPatternGrowth(ctx context.Context, req TagPatternListRequest) ([]db.ListTagPatternGrowthRow, error) {
	growthWindowDays := req.GrowthWindowDays
	if growthWindowDays <= 0 {
		growthWindowDays = 30
	}
	rows, err := s.queries.ListTagPatternGrowth(ctx, db.ListTagPatternGrowthParams{
		GrowthSince:     timestamptz(time.Now().UTC().Add(-time.Duration(growthWindowDays) * 24 * time.Hour)),
		LimitCount:      analyticsLimit(req.Limit),
		NexusInstanceID: optionalInt8(req.NexusInstanceID),
		RepositoryName:  text(req.RepositoryName),
		PatternName:     text(req.PatternName),
		NeverDownloaded: pgtype.Bool{Bool: req.NeverDownloaded, Valid: req.NeverDownloaded},
	})
	if err != nil {
		return nil, fmt.Errorf("list tag pattern growth: %w", err)
	}
	return rows, nil
}

func (s *Service) ListTagPatternExamples(ctx context.Context, req TagPatternListRequest) ([]db.ListTagPatternExamplesRow, error) {
	rows, err := s.queries.ListTagPatternExamples(ctx, db.ListTagPatternExamplesParams{
		NexusInstanceID: optionalInt8(req.NexusInstanceID),
		RepositoryName:  text(req.RepositoryName),
		PatternName:     text(req.PatternName),
		LimitCount:      analyticsLimit(req.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list tag pattern examples: %w", err)
	}
	return rows, nil
}

func (s *Service) ListTagPatternSuggestions(ctx context.Context, req TagPatternListRequest) ([]db.ListTagPatternSuggestionsRow, error) {
	rows, err := s.queries.ListTagPatternSuggestions(ctx, db.ListTagPatternSuggestionsParams{
		NexusInstanceID: optionalInt8(req.NexusInstanceID),
		RepositoryName:  text(req.RepositoryName),
		LimitCount:      analyticsLimit(req.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list tag pattern suggestions: %w", err)
	}
	return rows, nil
}

func (s *Service) ProcessDueSchedules(ctx context.Context, limit int32) (SchedulerResult, error) {
	if limit <= 0 {
		limit = 20
	}
	locked, err := s.queries.TryAcquireSchedulerLock(ctx, schedulerAdvisoryLockID)
	if err != nil {
		return SchedulerResult{}, fmt.Errorf("acquire scheduler advisory lock: %w", err)
	}
	if !locked {
		return SchedulerResult{}, nil
	}
	defer func() {
		_ = s.queries.ReleaseSchedulerLock(context.Background(), schedulerAdvisoryLockID)
	}()

	schedules, err := s.queries.ListDueIndexingSchedules(ctx, limit)
	if err != nil {
		return SchedulerResult{}, fmt.Errorf("list due indexing schedules: %w", err)
	}
	var result SchedulerResult
	for _, schedule := range schedules {
		result.Processed++
		if conflict, err := s.operationLockConflict(ctx, schedule.NexusInstanceID); err != nil {
			return result, err
		} else if conflict != nil {
			if _, err := s.queries.CreateSkippedScanRun(ctx, db.CreateSkippedScanRunParams{
				NexusInstanceID: schedule.NexusInstanceID,
				ScanType:        schedule.ScanType,
				RepositoryName:  text(""),
				TriggerSource:   TriggerSourceScheduler,
				TriggerJobName:  textRequired(schedule.Name),
				ErrorCode:       textRequired("operation_lock_conflict"),
				ErrorClass:      textRequired("operation_lock"),
				ErrorMessage:    textRequired(conflict.Error()),
				ErrorDetails: jsonBytes(map[string]any{
					"blocking_scan_run_id": conflict.BlockingScanRunID,
					"blocking_scan_type":   conflict.BlockingScanType,
				}),
				Stats: emptyJSON,
			}); err != nil {
				return result, fmt.Errorf("create skipped blocked scan run: %w", err)
			}
			if _, err := s.queries.MarkIndexingScheduleTicked(ctx, schedule.ID); err != nil {
				return result, fmt.Errorf("mark indexing schedule ticked: %w", err)
			}
			s.logger.Info("schedule skipped blocked", "schedule_id", schedule.ID, "schedule_name", schedule.Name, "blocking_scan_run_id", conflict.BlockingScanRunID)
			result.Skipped++
			continue
		}

		var scope RunScope
		if len(schedule.Scope) > 0 {
			_ = json.Unmarshal(schedule.Scope, &scope)
		}
		_, err := s.EnqueueRepositoryDiscoveryRun(ctx, EnqueueRepositoryDiscoveryRunRequest{
			NexusName:      schedule.NexusName,
			BaseURL:        schedule.NexusBaseUrl,
			ScanType:       schedule.ScanType,
			TaskKey:        fmt.Sprintf("schedule/%s/%d", schedule.Name, time.Now().Unix()),
			TriggerSource:  TriggerSourceScheduler,
			TriggerJobName: schedule.Name,
			Scope:          scope,
		})
		if err != nil {
			return result, err
		}
		if _, err := s.queries.MarkIndexingScheduleTicked(ctx, schedule.ID); err != nil {
			return result, fmt.Errorf("mark indexing schedule ticked: %w", err)
		}
		result.Enqueued++
	}
	return result, nil
}

func (s *Service) operationLockConflict(ctx context.Context, nexusInstanceID int64) (*OperationLockConflict, error) {
	lock, err := s.queries.GetOperationLock(ctx, nexusInstanceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get operation lock: %w", err)
	}
	return &OperationLockConflict{
		NexusInstanceID:   lock.NexusInstanceID,
		BlockingScanRunID: lock.ScanRunID,
		BlockingScanType:  lock.ScanType,
		BlockingStatus:    lock.ScanRunStatus,
	}, nil
}

func (s *Service) cleanupLockConflict(ctx context.Context, nexusInstanceID int64) (*OperationLockConflict, error) {
	lock, err := s.queries.GetCleanupLock(ctx, nexusInstanceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get cleanup lock: %w", err)
	}
	return &OperationLockConflict{
		NexusInstanceID:   lock.NexusInstanceID,
		BlockingScanRunID: lock.CleanupExecutionID,
		BlockingScanType:  "cleanup_execution",
		BlockingStatus:    "running",
	}, nil
}

func scanTypeRequiresOperationLock(scanType string) bool {
	return scanType == ScanTypeFullReconcile || scanType == ScanTypeBoundedReindex
}

func (s *Service) ListRecentRuns(ctx context.Context, limit int32) ([]db.ScanRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	runs, err := s.queries.ListRecentScanRuns(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent scan runs: %w", err)
	}
	return runs, nil
}

func (s *Service) ListRecentTasks(ctx context.Context, scanRunID int64, limit int32) ([]db.ScanTask, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	params := db.ListRecentScanTasksParams{
		LimitCount: limit,
	}
	if scanRunID > 0 {
		params.ScanRunID = int8(scanRunID, true)
	}
	tasks, err := s.queries.ListRecentScanTasks(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list recent scan tasks: %w", err)
	}
	return tasks, nil
}

func (s *Service) RetryTask(ctx context.Context, taskID int64) (db.ScanTask, error) {
	task, err := s.queries.RetryScanTaskByID(ctx, taskID)
	if err != nil {
		return db.ScanTask{}, fmt.Errorf("retry scan task: %w", err)
	}
	return retryScanTaskByIDRowToScanTask(task), nil
}

func (s *Service) CancelRun(ctx context.Context, scanRunID int64) (db.ScanRun, error) {
	run, err := s.queries.CancelScanRun(ctx, scanRunID)
	if err != nil {
		return db.ScanRun{}, fmt.Errorf("cancel scan run: %w", err)
	}
	if err := s.queries.ReleaseOperationLockByRun(ctx, scanRunID); err != nil {
		return db.ScanRun{}, fmt.Errorf("release operation lock: %w", err)
	}
	return run, nil
}

func (s *Service) ListVerificationFindings(ctx context.Context, nexusInstanceID int64, status string, limit int32) ([]db.VerificationFinding, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if status == "" {
		status = "open"
	}
	params := db.ListVerificationFindingsParams{
		Status:     text(status),
		LimitCount: limit,
	}
	if nexusInstanceID > 0 {
		params.NexusInstanceID = int8(nexusInstanceID, true)
	}
	findings, err := s.queries.ListVerificationFindings(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list verification findings: %w", err)
	}
	return findings, nil
}

func (s *Service) DismissVerificationFinding(ctx context.Context, findingID int64, dismissedBy string) (db.VerificationFinding, error) {
	finding, err := s.queries.DismissVerificationFinding(ctx, db.DismissVerificationFindingParams{
		ID:          findingID,
		DismissedBy: dismissedBy,
	})
	if err != nil {
		return db.VerificationFinding{}, fmt.Errorf("dismiss verification finding: %w", err)
	}
	return finding, nil
}

func (s *Service) ApplyRetention(ctx context.Context, settings RetentionSettings, now time.Time) (RetentionResult, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	settings = normalizeRetentionSettings(settings)
	var result RetentionResult
	var err error
	result.DeletedScanRuns, err = s.queries.DeleteTerminalScanRunsBefore(ctx, timestamptz(now.AddDate(0, 0, -settings.ScanRunDays)))
	if err != nil {
		return result, fmt.Errorf("delete old scan runs: %w", err)
	}
	result.DeletedCleanupExecutions, err = s.queries.DeleteCleanupExecutionsBefore(ctx, timestamptz(now.AddDate(0, 0, -settings.CleanupExecutionDays)))
	if err != nil {
		return result, fmt.Errorf("delete old cleanup executions: %w", err)
	}
	result.DeletedVerificationFindings, err = s.queries.DeleteClosedVerificationFindingsBefore(ctx, timestamptz(now.AddDate(0, 0, -settings.VerificationFindingDays)))
	if err != nil {
		return result, fmt.Errorf("delete old verification findings: %w", err)
	}
	result.DeletedTrendSnapshots, err = s.queries.DeleteTrendSnapshotsBefore(ctx, pgDate(now.AddDate(0, 0, -settings.TrendSnapshotDays)))
	if err != nil {
		return result, fmt.Errorf("delete old trend snapshots: %w", err)
	}
	return result, nil
}

func (s *Service) PrometheusMetrics(ctx context.Context) (string, error) {
	taskCounts, err := s.queries.CountScanTasksByStatus(ctx)
	if err != nil {
		return "", fmt.Errorf("count scan tasks by status: %w", err)
	}
	runCounts, err := s.queries.CountScanRunsByStatus24h(ctx)
	if err != nil {
		return "", fmt.Errorf("count scan runs by status: %w", err)
	}
	findingCounts, err := s.queries.CountOpenVerificationFindingsBySeverity(ctx)
	if err != nil {
		return "", fmt.Errorf("count open verification findings by severity: %w", err)
	}
	oldestPendingAge, err := s.queries.GetOldestPendingTaskAgeSeconds(ctx)
	if err != nil {
		return "", fmt.Errorf("get oldest pending task age: %w", err)
	}
	taskCountsByNexus, err := s.queries.CountScanTasksByNexusStatus(ctx)
	if err != nil {
		return "", fmt.Errorf("count scan tasks by nexus status: %w", err)
	}
	oldestPendingAgeByNexus, err := s.queries.ListOldestPendingTaskAgeByNexus(ctx)
	if err != nil {
		return "", fmt.Errorf("get oldest pending task age by nexus: %w", err)
	}
	staleSchedules, err := s.queries.ListStaleIndexingSchedules(ctx)
	if err != nil {
		return "", fmt.Errorf("list stale indexing schedules: %w", err)
	}
	cleanupCounts, err := s.queries.CountCleanupExecutionsByStatus(ctx)
	if err != nil {
		return "", fmt.Errorf("count cleanup executions by status: %w", err)
	}
	cleanupEventCounts, err := s.queries.CountCleanupExecutionEvents24h(ctx)
	if err != nil {
		return "", fmt.Errorf("count cleanup execution events: %w", err)
	}
	oldestCleanupAge, err := s.queries.GetOldestRunningCleanupAgeSeconds(ctx)
	if err != nil {
		return "", fmt.Errorf("get oldest running cleanup age: %w", err)
	}

	var out strings.Builder
	out.WriteString("# HELP nexus_lens_build_info Static build info for Nexus Lens.\n")
	out.WriteString("# TYPE nexus_lens_build_info gauge\n")
	out.WriteString("nexus_lens_build_info 1\n")
	out.WriteString("# HELP nexus_lens_scan_tasks Current scan task count by status.\n")
	out.WriteString("# TYPE nexus_lens_scan_tasks gauge\n")
	for _, row := range taskCounts {
		fmt.Fprintf(&out, "nexus_lens_scan_tasks{status=%q} %d\n", row.Status, row.Count)
	}
	out.WriteString("# HELP nexus_lens_scan_runs_24h Scan run count by status for runs started in the last 24 hours.\n")
	out.WriteString("# TYPE nexus_lens_scan_runs_24h gauge\n")
	for _, row := range runCounts {
		fmt.Fprintf(&out, "nexus_lens_scan_runs_24h{status=%q} %d\n", row.Status, row.Count)
	}
	out.WriteString("# HELP nexus_lens_verification_findings Open verification finding count by severity.\n")
	out.WriteString("# TYPE nexus_lens_verification_findings gauge\n")
	for _, row := range findingCounts {
		fmt.Fprintf(&out, "nexus_lens_verification_findings{status=%q,severity=%q} %d\n", "open", row.Severity, row.Count)
	}
	out.WriteString("# HELP nexus_lens_queue_oldest_pending_age_seconds Age in seconds of the oldest pending or retryable task.\n")
	out.WriteString("# TYPE nexus_lens_queue_oldest_pending_age_seconds gauge\n")
	fmt.Fprintf(&out, "nexus_lens_queue_oldest_pending_age_seconds %d\n", oldestPendingAge)
	out.WriteString("# HELP nexus_lens_scan_tasks_by_nexus Current scan task count by Nexus and status.\n")
	out.WriteString("# TYPE nexus_lens_scan_tasks_by_nexus gauge\n")
	for _, row := range taskCountsByNexus {
		fmt.Fprintf(&out, "nexus_lens_scan_tasks_by_nexus{nexus_instance_id=%q,nexus_name=%q,status=%q} %d\n", strconv.FormatInt(row.NexusInstanceID, 10), row.NexusName, row.Status, row.Count)
	}
	out.WriteString("# HELP nexus_lens_queue_oldest_pending_age_seconds_by_nexus Age in seconds of the oldest pending or retryable task by Nexus.\n")
	out.WriteString("# TYPE nexus_lens_queue_oldest_pending_age_seconds_by_nexus gauge\n")
	for _, row := range oldestPendingAgeByNexus {
		fmt.Fprintf(&out, "nexus_lens_queue_oldest_pending_age_seconds_by_nexus{nexus_instance_id=%q,nexus_name=%q} %d\n", strconv.FormatInt(row.NexusInstanceID, 10), row.NexusName, row.AgeSeconds)
	}
	out.WriteString("# HELP nexus_lens_indexing_schedule_stale_age_seconds Age in seconds for enabled schedules whose next run is overdue.\n")
	out.WriteString("# TYPE nexus_lens_indexing_schedule_stale_age_seconds gauge\n")
	for _, row := range staleSchedules {
		fmt.Fprintf(&out, "nexus_lens_indexing_schedule_stale_age_seconds{nexus_instance_id=%q,nexus_name=%q,schedule_name=%q,scan_type=%q} %d\n", strconv.FormatInt(row.NexusInstanceID, 10), row.NexusName, row.ScheduleName, row.ScanType, row.AgeSeconds)
	}
	out.WriteString("# HELP nexus_lens_cleanup_executions Current cleanup execution count by status.\n")
	out.WriteString("# TYPE nexus_lens_cleanup_executions gauge\n")
	for _, row := range cleanupCounts {
		fmt.Fprintf(&out, "nexus_lens_cleanup_executions{status=%q} %d\n", row.Status, row.Count)
	}
	out.WriteString("# HELP nexus_lens_cleanup_execution_events_24h Cleanup execution event count by type and status in the last 24 hours.\n")
	out.WriteString("# TYPE nexus_lens_cleanup_execution_events_24h gauge\n")
	for _, row := range cleanupEventCounts {
		fmt.Fprintf(&out, "nexus_lens_cleanup_execution_events_24h{event_type=%q,status=%q} %d\n", row.EventType, textValue(row.Status), row.Count)
	}
	out.WriteString("# HELP nexus_lens_cleanup_oldest_running_age_seconds Age in seconds of the oldest running cleanup execution.\n")
	out.WriteString("# TYPE nexus_lens_cleanup_oldest_running_age_seconds gauge\n")
	fmt.Fprintf(&out, "nexus_lens_cleanup_oldest_running_age_seconds %d\n", oldestCleanupAge)
	return out.String(), nil
}

func (s *Service) PauseNexusIndexing(ctx context.Context, nexusInstanceID int64, reason, pausedBy string) (db.NexusIndexingControl, error) {
	control, err := s.queries.PauseNexusIndexing(ctx, db.PauseNexusIndexingParams{
		NexusInstanceID: nexusInstanceID,
		PausedReason:    text(reason),
		PausedBy:        text(pausedBy),
	})
	if err != nil {
		return db.NexusIndexingControl{}, fmt.Errorf("pause nexus indexing: %w", err)
	}
	return control, nil
}

func (s *Service) ResumeNexusIndexing(ctx context.Context, nexusInstanceID int64) (db.NexusIndexingControl, error) {
	control, err := s.queries.ResumeNexusIndexing(ctx, nexusInstanceID)
	if err != nil {
		return db.NexusIndexingControl{}, fmt.Errorf("resume nexus indexing: %w", err)
	}
	return control, nil
}

func retryScanTaskByIDRowToScanTask(row db.RetryScanTaskByIDRow) db.ScanTask {
	return db.ScanTask{
		ID:                row.ID,
		ScanRunID:         row.ScanRunID,
		NexusInstanceID:   row.NexusInstanceID,
		TaskType:          row.TaskType,
		TaskKey:           row.TaskKey,
		RepositoryName:    row.RepositoryName,
		Subject:           row.Subject,
		CostClass:         row.CostClass,
		EstimatedRequests: row.EstimatedRequests,
		Status:            row.Status,
		Priority:          row.Priority,
		Attempt:           row.Attempt,
		MaxAttempts:       row.MaxAttempts,
		AvailableAt:       row.AvailableAt,
		LeaseExpiresAt:    row.LeaseExpiresAt,
		LockToken:         row.LockToken,
		LockedBy:          row.LockedBy,
		LockedAt:          row.LockedAt,
		StartedAt:         row.StartedAt,
		FinishedAt:        row.FinishedAt,
		LastHeartbeatAt:   row.LastHeartbeatAt,
		ProgressCurrent:   row.ProgressCurrent,
		ProgressTotal:     row.ProgressTotal,
		ProgressLabel:     row.ProgressLabel,
		Payload:           row.Payload,
		Checkpoint:        row.Checkpoint,
		Stats:             row.Stats,
		ErrorCode:         row.ErrorCode,
		ErrorClass:        row.ErrorClass,
		ErrorMessage:      row.ErrorMessage,
		ErrorDetails:      row.ErrorDetails,
		CreatedAt:         row.CreatedAt,
	}
}
