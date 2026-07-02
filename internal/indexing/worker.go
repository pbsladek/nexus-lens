package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/pbsladek/nexus-lens/internal/db"
	"github.com/pbsladek/nexus-lens/internal/nexus"
	"github.com/pbsladek/nexus-lens/internal/registry"
)

type Worker struct {
	queries                   *db.Queries
	logger                    *slog.Logger
	workerID                  string
	workerPod                 string
	nexusUser                 string
	nexusPass                 string
	destructiveCleanupEnabled bool
	cleanupDeleteRPS          float64
	cleanupMaintenanceStart   string
	cleanupMaintenanceEnd     string
	retentionSettings         RetentionSettings
	lastRetentionAt           time.Time
	batchSize                 int32
	lease                     time.Duration
	pollInterval              time.Duration
	limiter                   *rateLimiter
}

type WorkerOption func(*Worker)

func NewWorker(queries *db.Queries, logger *slog.Logger, opts ...WorkerOption) *Worker {
	w := &Worker{
		queries:                   queries,
		logger:                    logger,
		workerID:                  workerID(),
		nexusUser:                 os.Getenv("NEXUS_LENS_NEXUS_USERNAME"),
		nexusPass:                 envOrFile("NEXUS_LENS_NEXUS_PASSWORD", "NEXUS_LENS_NEXUS_PASSWORD_FILE"),
		destructiveCleanupEnabled: envBool("NEXUS_LENS_ENABLE_DESTRUCTIVE_CLEANUP", false),
		cleanupDeleteRPS:          envFloat("NEXUS_LENS_CLEANUP_DELETE_RPS", 0.5),
		cleanupMaintenanceStart:   os.Getenv("NEXUS_LENS_CLEANUP_MAINTENANCE_START_UTC"),
		cleanupMaintenanceEnd:     os.Getenv("NEXUS_LENS_CLEANUP_MAINTENANCE_END_UTC"),
		retentionSettings: RetentionSettings{
			IntervalSeconds:         envInt("NEXUS_LENS_RETENTION_INTERVAL_SECONDS", 3600),
			ScanRunDays:             envInt("NEXUS_LENS_RETENTION_SCAN_RUN_DAYS", 30),
			CleanupExecutionDays:    envInt("NEXUS_LENS_RETENTION_CLEANUP_EXECUTION_DAYS", 365),
			VerificationFindingDays: envInt("NEXUS_LENS_RETENTION_VERIFICATION_FINDING_DAYS", 180),
			TrendSnapshotDays:       envInt("NEXUS_LENS_RETENTION_TREND_SNAPSHOT_DAYS", 365),
		},
		batchSize:    DefaultWorkerBatchSize,
		lease:        DefaultWorkerLease,
		pollInterval: DefaultWorkerPollPeriod,
		limiter:      newRateLimiter(),
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

func WithWorkerID(id string) WorkerOption {
	return func(w *Worker) {
		if id != "" {
			w.workerID = id
		}
	}
}

func WithWorkerPodName(name string) WorkerOption {
	return func(w *Worker) {
		w.workerPod = name
	}
}

func WithBatchSize(size int32) WorkerOption {
	return func(w *Worker) {
		if size > 0 {
			w.batchSize = size
		}
	}
}

func WithLeaseDuration(duration time.Duration) WorkerOption {
	return func(w *Worker) {
		if duration > 0 {
			w.lease = duration
		}
	}
}

func WithPollInterval(duration time.Duration) WorkerOption {
	return func(w *Worker) {
		if duration > 0 {
			w.pollInterval = duration
		}
	}
}

func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		processed, err := w.RunOnce(ctx)
		if err != nil {
			w.logger.Error("worker iteration failed", "worker_id", w.workerID, "error", err)
		}
		if processed > 0 {
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	scheduler := NewService(w.queries, w.logger)
	scheduleResult, err := scheduler.ProcessDueSchedules(ctx, 20)
	if err != nil {
		return 0, fmt.Errorf("process due schedules: %w", err)
	}
	if scheduleResult.Processed > 0 {
		w.logger.Info("scheduler tick processed", "processed", scheduleResult.Processed, "enqueued", scheduleResult.Enqueued, "skipped", scheduleResult.Skipped)
	}
	retentionProcessed, err := w.applyRetentionIfDue(ctx, scheduler)
	if err != nil {
		return scheduleResult.Processed, err
	}

	cleanupProcessed := 0
	if w.destructiveCleanupEnabled {
		processedCleanup, err := scheduler.ProcessNextCleanupExecution(ctx, CleanupExecutionProcessRequest{
			NexusUsername:          w.nexusUser,
			NexusPassword:          w.nexusPass,
			DeleteRPS:              w.cleanupDeleteRPS,
			MaintenanceWindowStart: w.cleanupMaintenanceStart,
			MaintenanceWindowEnd:   w.cleanupMaintenanceEnd,
			LeaseSeconds:           int32(w.lease.Seconds()),
			WorkerID:               w.workerID,
		})
		if err != nil {
			return scheduleResult.Processed, fmt.Errorf("process cleanup execution: %w", err)
		}
		if processedCleanup {
			cleanupProcessed = 1
		}
	}

	reclaimed, err := w.queries.ReclaimExpiredScanTasks(ctx)
	if err != nil {
		return 0, fmt.Errorf("reclaim expired scan tasks: %w", err)
	}
	for _, task := range reclaimed {
		w.logger.Warn("reclaimed expired scan task", "task_id", task.ID, "scan_run_id", task.ScanRunID, "attempt", task.Attempt, "status", task.Status)
		_, _ = w.refreshRunAndReleaseLock(ctx, task.ScanRunID)
	}

	lockToken, err := newLockToken()
	if err != nil {
		return len(reclaimed), err
	}
	claimed, err := w.queries.ClaimScanTasks(ctx, db.ClaimScanTasksParams{
		LockToken:    lockToken,
		WorkerID:     textRequired(w.workerID),
		LeaseSeconds: int32(w.lease.Seconds()),
		LimitCount:   w.batchSize,
	})
	if err != nil {
		return len(reclaimed), fmt.Errorf("claim scan tasks: %w", err)
	}

	for _, task := range claimed {
		w.processClaimedTask(ctx, task)
	}
	return scheduleResult.Processed + retentionProcessed + cleanupProcessed + len(reclaimed) + len(claimed), nil
}

func (w *Worker) applyRetentionIfDue(ctx context.Context, scheduler *Service) (int, error) {
	settings, err := scheduler.GetRetentionSettings(ctx, w.retentionSettings)
	if err != nil {
		return 0, err
	}
	retentionInterval := time.Duration(settings.IntervalSeconds) * time.Second
	if retentionInterval <= 0 {
		return 0, nil
	}
	now := time.Now().UTC()
	if !w.lastRetentionAt.IsZero() && now.Sub(w.lastRetentionAt) < retentionInterval {
		return 0, nil
	}
	result, err := scheduler.ApplyRetention(ctx, settings, now)
	if err != nil {
		return 0, fmt.Errorf("apply retention: %w", err)
	}
	w.lastRetentionAt = now
	w.logger.Info("retention applied",
		"deleted_scan_runs", result.DeletedScanRuns,
		"deleted_cleanup_executions", result.DeletedCleanupExecutions,
		"deleted_verification_findings", result.DeletedVerificationFindings,
		"deleted_trend_snapshots", result.DeletedTrendSnapshots,
	)
	if result.DeletedScanRuns+result.DeletedCleanupExecutions+result.DeletedVerificationFindings+result.DeletedTrendSnapshots == 0 {
		return 0, nil
	}
	return 1, nil
}

func (w *Worker) processClaimedTask(ctx context.Context, task db.ScanTask) {
	logger := w.logger.With("worker_id", w.workerID, "task_id", task.ID, "scan_run_id", task.ScanRunID, "task_type", task.TaskType, "attempt", task.Attempt)
	attempt, err := w.queries.CreateScanTaskAttempt(ctx, db.CreateScanTaskAttemptParams{
		ScanTaskID:    task.ID,
		Attempt:       task.Attempt,
		WorkerID:      textRequired(w.workerID),
		WorkerPodName: text(w.workerPod),
		LockToken:     task.LockToken,
	})
	if err != nil {
		logger.Error("create task attempt failed", "error", err)
		return
	}

	logger.Info("task started")
	taskErr := w.executeTask(ctx, task)
	if taskErr == nil {
		if err := w.completeTask(ctx, task, attempt.ID); err != nil {
			logger.Error("complete task failed", "error", err)
			return
		}
		logger.Info("task completed")
		return
	}

	if err := w.failTask(ctx, task, attempt.ID, taskErr); err != nil {
		logger.Error("fail task failed", "error", err)
		return
	}
	logger.Warn("task failed", "error_code", taskErr.Code, "error_class", taskErr.Class, "retryable", taskErr.Retryable)
}

func (w *Worker) executeTask(ctx context.Context, task db.ScanTask) *TaskError {
	if err := ctx.Err(); err != nil {
		return &TaskError{Code: "worker_context_canceled", Class: "worker", Message: err.Error(), Retryable: true}
	}
	switch task.TaskType {
	case TaskTypeNoop:
		return nil
	case TaskTypeRepositoryDiscovery:
		return w.discoverRepositories(ctx, task)
	case TaskTypeDockerTagScan:
		return w.scanDockerTags(ctx, task)
	case TaskTypePackageAssetScan:
		return w.scanPackageAssets(ctx, task)
	case TaskTypeManifestFetchBatch:
		return w.fetchManifestBatch(ctx, task)
	case TaskTypeVerificationChecks:
		return w.runVerificationChecks(ctx, task)
	case TaskTypeTrendSnapshot:
		return w.runTrendSnapshot(ctx, task)
	case TaskTypeTagReclassification:
		return w.runTagReclassification(ctx, task)
	default:
		return &TaskError{
			Code:      "unsupported_task_type",
			Class:     "worker",
			Message:   ErrUnsupportedTaskType.Error(),
			Retryable: false,
		}
	}
}

func (w *Worker) waitForRateLimit(ctx context.Context, task db.ScanTask) error {
	control, err := w.queries.GetNexusIndexingControl(ctx, task.NexusInstanceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return w.limiter.Wait(ctx, task.CostClass)
		}
		return err
	}
	return w.limiter.WaitForNexus(ctx, task.NexusInstanceID, task.CostClass, nexusRateLimitControl{
		assetListRPS:        control.AssetListRps,
		registryManifestRPS: control.RegistryManifestRps,
		defaultRPS:          control.DefaultRps,
	})
}

func (w *Worker) runTrendSnapshot(ctx context.Context, task db.ScanTask) *TaskError {
	var payload struct {
		SnapshotDate  string `json:"snapshot_date"`
		RetentionDays int    `json:"retention_days"`
	}
	if len(task.Payload) > 0 {
		if err := json.Unmarshal(task.Payload, &payload); err != nil {
			return &TaskError{Code: "invalid_task_payload", Class: "worker", Message: err.Error(), Retryable: false}
		}
	}

	snapshotDate := time.Now().UTC()
	if payload.SnapshotDate != "" {
		parsed, err := time.Parse("2006-01-02", payload.SnapshotDate)
		if err != nil {
			return &TaskError{Code: "invalid_task_payload", Class: "worker", Message: "snapshot_date must use YYYY-MM-DD", Retryable: false}
		}
		snapshotDate = parsed
	}
	retentionDays := payload.RetentionDays
	if retentionDays <= 0 {
		retentionDays = 365
	}

	snapshots, err := w.queries.CaptureDailyTrendSnapshots(ctx, db.CaptureDailyTrendSnapshotsParams{
		SnapshotDate:    pgDate(snapshotDate),
		NexusInstanceID: int8(task.NexusInstanceID, true),
	})
	if err != nil {
		return &TaskError{Code: "postgres_trend_snapshot_failed", Class: "postgres", Message: err.Error(), Retryable: true}
	}
	deleted, err := w.queries.DeleteTrendSnapshotsBefore(ctx, pgDate(snapshotDate.AddDate(0, 0, -retentionDays)))
	if err != nil {
		return &TaskError{Code: "postgres_trend_retention_failed", Class: "postgres", Message: err.Error(), Retryable: true}
	}
	w.logger.Info("trend snapshot captured", "task_id", task.ID, "scan_run_id", task.ScanRunID, "nexus_instance_id", task.NexusInstanceID, "snapshot_date", snapshotDate.Format("2006-01-02"), "snapshot_rows", len(snapshots), "retention_deleted", deleted)
	return nil
}

func (w *Worker) runVerificationChecks(ctx context.Context, task db.ScanTask) *TaskError {
	var payload struct {
		Scope taskScope `json:"scope"`
	}
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return &TaskError{Code: "invalid_task_payload", Class: "worker", Message: err.Error(), Retryable: false}
	}
	if err := w.waitForRateLimit(ctx, task); err != nil {
		return rateLimitError(task.CostClass, err)
	}

	repositories := payload.Scope.Repositories
	if len(repositories) == 0 {
		repositories = []string{""}
	}
	findings := 0
	for _, repository := range repositories {
		params := db.ListMissingDigestVerificationCandidatesParams{
			NexusInstanceID: int8(task.NexusInstanceID, true),
			RepositoryName:  text(repository),
			LimitCount:      100,
		}
		missingDigests, err := w.queries.ListMissingDigestVerificationCandidates(ctx, params)
		if err != nil {
			return &TaskError{Code: "postgres_verification_query_failed", Class: "postgres", Message: err.Error(), Retryable: true}
		}
		for _, candidate := range missingDigests {
			subject := candidate.ImageName + ":" + candidate.Tag
			if _, err := w.queries.UpsertOpenVerificationFinding(ctx, db.UpsertOpenVerificationFindingParams{
				VfNexusInstanceID: int8(candidate.NexusInstanceID, true),
				VfScanRunID:       int8(task.ScanRunID, true),
				Severity:          "high",
				VfFindingType:     "docker_tag_digest_missing",
				VfRepositoryName:  textRequired(candidate.RepositoryName),
				VfSubject:         textRequired(subject),
				Expected:          jsonBytes(map[string]any{"digest": "non-empty registry digest"}),
				Actual:            jsonBytes(map[string]any{"tag_id": candidate.TagID, "digest": ""}),
				Message:           textRequired("active Docker tag is missing a digest in the local index"),
				Remediation:       textRequired("run a bounded reindex for the repository; if the finding remains, run a full reconcile"),
				Metadata:          jsonBytes(map[string]any{"scan_task_id": task.ID}),
			}); err != nil {
				return &TaskError{Code: "postgres_verification_finding_upsert_failed", Class: "postgres", Message: err.Error(), Retryable: true}
			}
			findings++
		}

		manifestParams := db.ListMissingManifestVerificationCandidatesParams{
			NexusInstanceID: int8(task.NexusInstanceID, true),
			RepositoryName:  text(repository),
			LimitCount:      100,
		}
		missingManifests, err := w.queries.ListMissingManifestVerificationCandidates(ctx, manifestParams)
		if err != nil {
			return &TaskError{Code: "postgres_verification_query_failed", Class: "postgres", Message: err.Error(), Retryable: true}
		}
		for _, candidate := range missingManifests {
			subject := candidate.ImageName + ":" + candidate.Tag
			if _, err := w.queries.UpsertOpenVerificationFinding(ctx, db.UpsertOpenVerificationFindingParams{
				VfNexusInstanceID: int8(candidate.NexusInstanceID, true),
				VfScanRunID:       int8(task.ScanRunID, true),
				Severity:          "medium",
				VfFindingType:     "docker_tag_manifest_missing",
				VfRepositoryName:  textRequired(candidate.RepositoryName),
				VfSubject:         textRequired(subject),
				Expected:          jsonBytes(map[string]any{"manifest_digest": candidate.Digest}),
				Actual:            jsonBytes(map[string]any{"tag_id": candidate.TagID, "manifest_indexed": false}),
				Message:           textRequired("active Docker tag points at a digest that has no manifest graph in the local index"),
				Remediation:       textRequired("retry failed manifest tasks or run a bounded reindex for the repository"),
				Metadata:          jsonBytes(map[string]any{"scan_task_id": task.ID}),
			}); err != nil {
				return &TaskError{Code: "postgres_verification_finding_upsert_failed", Class: "postgres", Message: err.Error(), Retryable: true}
			}
			findings++
		}
	}

	w.logger.Info("verification checks completed", "task_id", task.ID, "scan_run_id", task.ScanRunID, "findings", findings)
	return nil
}

func (w *Worker) discoverRepositories(ctx context.Context, task db.ScanTask) *TaskError {
	var payload struct {
		BaseURL string    `json:"base_url"`
		Scope   taskScope `json:"scope"`
	}
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return &TaskError{Code: "invalid_task_payload", Class: "worker", Message: err.Error(), Retryable: false}
	}
	client, err := nexus.NewClient(payload.BaseURL, w.nexusUser, w.nexusPass)
	if err != nil {
		return &TaskError{Code: "invalid_nexus_config", Class: "config", Message: err.Error(), Retryable: false}
	}
	if err := w.waitForRateLimit(ctx, task); err != nil {
		return rateLimitError(task.CostClass, err)
	}
	repos, err := client.ListRepositories(ctx)
	if err != nil {
		return classifyUpstreamError("list repositories", "nexus_repository_list", err)
	}
	hostedDocker := 0
	hostedPackage := 0
	unsupported := 0
	for _, repo := range repos {
		if !repositoryAllowed(repo.Name, payload.Scope) {
			continue
		}
		if repo.Format == "docker" && repo.Type == "hosted" {
			hostedDocker++
		} else if repo.Type == "hosted" && packageAssetIndexFormat(repo.Format) {
			hostedPackage++
		} else {
			unsupported++
		}
		online := pgtype.Bool{}
		if repo.Online != nil {
			online = pgtype.Bool{Bool: *repo.Online, Valid: true}
		}
		repositoryRow, err := w.queries.UpsertRepositoryFromDiscovery(ctx, db.UpsertRepositoryFromDiscoveryParams{
			NexusInstanceID: task.NexusInstanceID,
			Name:            repo.Name,
			Format:          repo.Format,
			Type:            repo.Type,
			Url:             text(repo.URL),
			Online:          online,
			Metadata: jsonBytes(map[string]any{
				"attributes":         repo.Attributes,
				"supported_in_v1":    repo.Type == "hosted" && (repo.Format == "docker" || packageAssetIndexFormat(repo.Format)),
				"unsupported_reason": unsupportedReason(repo),
			}),
		})
		if err != nil {
			return &TaskError{Code: "postgres_repository_upsert_failed", Class: "postgres", Message: err.Error(), Retryable: true}
		}
		if repo.Format == "docker" && repo.Type == "hosted" {
			if _, err := w.queries.EnqueueScanTask(ctx, db.EnqueueScanTaskParams{
				ScanRunID:       task.ScanRunID,
				NexusInstanceID: task.NexusInstanceID,
				TaskType:        TaskTypeDockerTagScan,
				TaskKey:         "docker-tag-scan/" + repo.Name,
				RepositoryName:  textRequired(repo.Name),
				Subject:         textRequired(repo.Name),
				CostClass:       "asset_list",
				Priority:        0,
				MaxAttempts:     3,
				Payload: jsonBytes(map[string]any{
					"base_url":        payload.BaseURL,
					"repository_id":   repositoryRow.ID,
					"repository_name": repo.Name,
					"repository_url":  repo.URL,
					"scope":           payload.Scope,
				}),
			}); err != nil {
				return &TaskError{Code: "postgres_task_enqueue_failed", Class: "postgres", Message: err.Error(), Retryable: true}
			}
		} else if repo.Type == "hosted" && packageAssetIndexFormat(repo.Format) {
			if _, err := w.queries.EnqueueScanTask(ctx, db.EnqueueScanTaskParams{
				ScanRunID:       task.ScanRunID,
				NexusInstanceID: task.NexusInstanceID,
				TaskType:        TaskTypePackageAssetScan,
				TaskKey:         "package-asset-scan/" + repo.Name,
				RepositoryName:  textRequired(repo.Name),
				Subject:         textRequired(repo.Name),
				CostClass:       "asset_list",
				Priority:        0,
				MaxAttempts:     3,
				Payload: jsonBytes(map[string]any{
					"base_url":        payload.BaseURL,
					"repository_id":   repositoryRow.ID,
					"repository_name": repo.Name,
					"format":          repo.Format,
					"scope":           payload.Scope,
				}),
			}); err != nil {
				return &TaskError{Code: "postgres_task_enqueue_failed", Class: "postgres", Message: err.Error(), Retryable: true}
			}
		}
	}
	w.logger.Info("repositories discovered", "task_id", task.ID, "scan_run_id", task.ScanRunID, "repositories", len(repos), "hosted_docker", hostedDocker, "hosted_package", hostedPackage, "unsupported", unsupported)
	return nil
}

func (w *Worker) scanPackageAssets(ctx context.Context, task db.ScanTask) *TaskError {
	var payload struct {
		BaseURL        string    `json:"base_url"`
		RepositoryID   int64     `json:"repository_id"`
		RepositoryName string    `json:"repository_name"`
		Format         string    `json:"format"`
		Scope          taskScope `json:"scope"`
	}
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return &TaskError{Code: "invalid_task_payload", Class: "worker", Message: err.Error(), Retryable: false}
	}
	if payload.RepositoryID == 0 || payload.RepositoryName == "" || !packageAssetIndexFormat(payload.Format) {
		return &TaskError{Code: "invalid_task_payload", Class: "worker", Message: "repository_id, repository_name, and supported package format are required", Retryable: false}
	}
	client, err := nexus.NewClient(payload.BaseURL, w.nexusUser, w.nexusPass)
	if err != nil {
		return &TaskError{Code: "invalid_nexus_config", Class: "config", Message: err.Error(), Retryable: false}
	}

	indexed := 0
	pages := 0
	skippedOutsideWindow := 0
	skippedMissingTimestamp := 0
	since, hasSince := scopeSince(payload.Scope)
	continuationToken := ""
	for {
		if err := w.waitForRateLimit(ctx, task); err != nil {
			return rateLimitError(task.CostClass, err)
		}
		page, err := client.ListAssets(ctx, payload.RepositoryName, continuationToken)
		if err != nil {
			return classifyUpstreamError("list assets", "nexus_assets_list", err)
		}
		pages++
		for _, asset := range page.Items {
			if taskErr := w.upsertNexusAsset(ctx, payload.RepositoryID, asset); taskErr != nil {
				return taskErr
			}
			if hasSince {
				inWindow, hasTimestamp := assetChangedSince(asset, since)
				if !hasTimestamp {
					skippedMissingTimestamp++
					continue
				}
				if !inWindow {
					skippedOutsideWindow++
					continue
				}
			}
			if taskErr := w.upsertGenericArtifact(ctx, payload.RepositoryID, payload.Format, asset); taskErr != nil {
				return taskErr
			}
			indexed++
		}
		if page.ContinuationToken == "" {
			break
		}
		continuationToken = page.ContinuationToken
	}
	w.logger.Info("package assets indexed", "task_id", task.ID, "scan_run_id", task.ScanRunID, "repository", payload.RepositoryName, "format", payload.Format, "pages", pages, "assets", indexed, "skipped_outside_window", skippedOutsideWindow, "skipped_missing_timestamp", skippedMissingTimestamp)
	return nil
}

func (w *Worker) scanDockerTags(ctx context.Context, task db.ScanTask) *TaskError {
	var payload struct {
		BaseURL        string    `json:"base_url"`
		RepositoryID   int64     `json:"repository_id"`
		RepositoryName string    `json:"repository_name"`
		RepositoryURL  string    `json:"repository_url"`
		Scope          taskScope `json:"scope"`
	}
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return &TaskError{Code: "invalid_task_payload", Class: "worker", Message: err.Error(), Retryable: false}
	}
	if payload.RepositoryID == 0 || payload.RepositoryName == "" {
		return &TaskError{Code: "invalid_task_payload", Class: "worker", Message: "repository_id and repository_name are required", Retryable: false}
	}
	client, err := nexus.NewClient(payload.BaseURL, w.nexusUser, w.nexusPass)
	if err != nil {
		return &TaskError{Code: "invalid_nexus_config", Class: "config", Message: err.Error(), Retryable: false}
	}

	images := map[string]int64{}
	tags := 0
	skippedOutsideWindow := 0
	skippedMissingTimestamp := 0
	pages := 0
	since, hasSince := scopeSince(payload.Scope)
	continuationToken := ""
	for {
		if err := w.waitForRateLimit(ctx, task); err != nil {
			return rateLimitError(task.CostClass, err)
		}
		page, err := client.ListAssets(ctx, payload.RepositoryName, continuationToken)
		if err != nil {
			return classifyUpstreamError("list assets", "nexus_assets_list", err)
		}
		pages++
		for _, asset := range page.Items {
			if taskErr := w.upsertNexusAsset(ctx, payload.RepositoryID, asset); taskErr != nil {
				return taskErr
			}
			imageName, tag, ok := dockerTagFromAssetPath(asset.Path)
			if !ok {
				continue
			}
			if hasSince {
				inWindow, hasTimestamp := assetChangedSince(asset, since)
				if !hasTimestamp {
					skippedMissingTimestamp++
					continue
				}
				if !inWindow {
					skippedOutsideWindow++
					continue
				}
			}
			imageID, ok := images[imageName]
			if !ok {
				image, err := w.queries.UpsertContainerImage(ctx, db.UpsertContainerImageParams{
					RepositoryID: payload.RepositoryID,
					Name:         imageName,
				})
				if err != nil {
					return &TaskError{Code: "postgres_image_upsert_failed", Class: "postgres", Message: err.Error(), Retryable: true}
				}
				imageID = image.ID
				images[imageName] = imageID
			}
			if _, err := w.queries.UpsertContainerTag(ctx, db.UpsertContainerTagParams{
				ImageID:   imageID,
				Tag:       tag,
				Digest:    dockerDigest(asset),
				MediaType: asset.ContentType,
				SizeBytes: asset.FileSize,
			}); err != nil {
				return &TaskError{Code: "postgres_tag_upsert_failed", Class: "postgres", Message: err.Error(), Retryable: true}
			}
			tags++
		}
		if page.ContinuationToken == "" {
			break
		}
		continuationToken = page.ContinuationToken
	}

	deletedTags := 0
	run, err := w.queries.GetScanRun(ctx, task.ScanRunID)
	if err != nil {
		return &TaskError{Code: "postgres_scan_run_lookup_failed", Class: "postgres", Message: err.Error(), Retryable: true}
	}
	if run.ScanType == ScanTypeFullReconcile {
		deleted, err := w.queries.MarkMissingContainerTagsDeletedForRepository(ctx, db.MarkMissingContainerTagsDeletedForRepositoryParams{
			RepositoryID:                payload.RepositoryID,
			LastSeenAt:                  run.StartedAt,
			MissingInFullReconcileRunID: int8(task.ScanRunID, true),
		})
		if err != nil {
			return &TaskError{Code: "postgres_tag_delete_confirmation_failed", Class: "postgres", Message: err.Error(), Retryable: true}
		}
		deletedTags = len(deleted)
	}
	classifiedTags, taskErr := w.classifyActiveTagsForRepository(ctx, task.NexusInstanceID, payload.RepositoryID)
	if taskErr != nil {
		return taskErr
	}

	w.logger.Info("docker tags scanned", "task_id", task.ID, "scan_run_id", task.ScanRunID, "repository", payload.RepositoryName, "pages", pages, "images", len(images), "tags", tags, "classified_tags", classifiedTags, "deleted_confirmed", deletedTags, "bounded_since", payload.Scope.Since, "skipped_outside_window", skippedOutsideWindow, "skipped_missing_timestamp", skippedMissingTimestamp)
	if _, err := w.queries.EnqueueScanTask(ctx, db.EnqueueScanTaskParams{
		ScanRunID:       task.ScanRunID,
		NexusInstanceID: task.NexusInstanceID,
		TaskType:        TaskTypeManifestFetchBatch,
		TaskKey:         "manifest-fetch/" + payload.RepositoryName,
		RepositoryName:  textRequired(payload.RepositoryName),
		Subject:         textRequired(payload.RepositoryName),
		CostClass:       "registry_manifest",
		Priority:        -10,
		MaxAttempts:     3,
		Payload: jsonBytes(map[string]any{
			"repository_id":   payload.RepositoryID,
			"repository_name": payload.RepositoryName,
			"repository_url":  payload.RepositoryURL,
			"scope":           payload.Scope,
		}),
	}); err != nil {
		return &TaskError{Code: "postgres_task_enqueue_failed", Class: "postgres", Message: err.Error(), Retryable: true}
	}
	return nil
}

func (w *Worker) upsertNexusAsset(ctx context.Context, repositoryID int64, asset nexus.Asset) *TaskError {
	if asset.ID == "" {
		return nil
	}
	if _, err := w.queries.UpsertNexusAsset(ctx, db.UpsertNexusAssetParams{
		RepositoryID:     repositoryID,
		NexusAssetID:     text(asset.ID),
		Path:             text(asset.Path),
		DownloadUrl:      text(asset.DownloadURL),
		ContentType:      text(asset.ContentType),
		SizeBytes:        int8(asset.FileSize, true),
		BlobStoreName:    text(asset.BlobStoreName),
		ChecksumSha1:     text(asset.Checksum.SHA1),
		ChecksumSha256:   text(asset.Checksum.SHA256),
		LastDownloadedAt: parseNexusTime(asset.LastDownloaded),
		BlobCreatedAt:    parseNexusTime(asset.BlobCreated),
		LastModifiedAt:   parseNexusTime(asset.LastModified),
		Metadata: jsonBytes(map[string]any{
			"format":     asset.Format,
			"repository": asset.Repository,
		}),
	}); err != nil {
		return &TaskError{Code: "postgres_asset_upsert_failed", Class: "postgres", Message: err.Error(), Retryable: true}
	}
	return nil
}

func (w *Worker) upsertGenericArtifact(ctx context.Context, repositoryID int64, format string, asset nexus.Asset) *TaskError {
	artifact := genericArtifactFromAsset(format, asset)
	if artifact.Name == "" {
		return nil
	}
	if _, err := w.queries.UpsertGenericArtifact(ctx, db.UpsertGenericArtifactParams{
		RepositoryID:   repositoryID,
		Format:         format,
		Namespace:      textRequired(artifact.Namespace),
		Name:           artifact.Name,
		Version:        textRequired(artifact.Version),
		Path:           textRequired(asset.Path),
		SizeBytes:      int8(asset.FileSize, true),
		ChecksumSha256: text(asset.Checksum.SHA256),
		Metadata: jsonBytes(map[string]any{
			"nexus_asset_id":   asset.ID,
			"repository":       asset.Repository,
			"download_url":     asset.DownloadURL,
			"content_type":     asset.ContentType,
			"last_modified":    asset.LastModified,
			"last_downloaded":  asset.LastDownloaded,
			"blob_created":     asset.BlobCreated,
			"blob_store_name":  asset.BlobStoreName,
			"checksum_sha1":    asset.Checksum.SHA1,
			"parser":           artifact.Parser,
			"identity_quality": artifact.IdentityQuality,
		}),
	}); err != nil {
		return &TaskError{Code: "postgres_generic_artifact_upsert_failed", Class: "postgres", Message: err.Error(), Retryable: true}
	}
	return nil
}

type genericArtifactIdentity struct {
	Namespace       string
	Name            string
	Version         string
	Parser          string
	IdentityQuality string
}

func genericArtifactFromAsset(format string, asset nexus.Asset) genericArtifactIdentity {
	path := strings.Trim(asset.Path, "/")
	if path == "" {
		return genericArtifactIdentity{}
	}
	parts := strings.Split(path, "/")
	switch format {
	case "npm":
		return npmArtifactIdentity(parts, path)
	case "pypi":
		return pypiArtifactIdentity(parts, path)
	case "go":
		return goArtifactIdentity(parts, path)
	default:
		return genericArtifactIdentity{Name: path, Parser: "path", IdentityQuality: "path_only"}
	}
}

func npmArtifactIdentity(parts []string, path string) genericArtifactIdentity {
	if len(parts) >= 4 && strings.HasPrefix(parts[0], "@") && parts[2] == "-" {
		return genericArtifactIdentity{Namespace: parts[0], Name: parts[1], Version: versionFromFilename(parts[len(parts)-1], parts[1]), Parser: "npm-scoped-tarball", IdentityQuality: "parsed"}
	}
	if len(parts) >= 3 && parts[1] == "-" {
		name := parts[0]
		return genericArtifactIdentity{Name: name, Version: versionFromFilename(parts[len(parts)-1], name), Parser: "npm-tarball", IdentityQuality: "parsed"}
	}
	return genericArtifactIdentity{Name: path, Parser: "npm-path", IdentityQuality: "path_only"}
}

func pypiArtifactIdentity(parts []string, path string) genericArtifactIdentity {
	filename := parts[len(parts)-1]
	name := strings.TrimSuffix(filename, ".tar.gz")
	name = strings.TrimSuffix(name, ".zip")
	name = strings.TrimSuffix(name, ".whl")
	version := ""
	if split := strings.Split(name, "-"); len(split) >= 2 {
		name = split[0]
		version = split[1]
	}
	if name == "" {
		name = path
	}
	return genericArtifactIdentity{Name: name, Version: version, Parser: "pypi-filename", IdentityQuality: "parsed"}
}

func goArtifactIdentity(parts []string, path string) genericArtifactIdentity {
	if len(parts) >= 3 && parts[len(parts)-2] == "@v" {
		module := strings.Join(parts[:len(parts)-2], "/")
		version := strings.TrimSuffix(parts[len(parts)-1], ".zip")
		version = strings.TrimSuffix(version, ".mod")
		version = strings.TrimSuffix(version, ".info")
		return genericArtifactIdentity{Name: module, Version: version, Parser: "go-module-path", IdentityQuality: "parsed"}
	}
	return genericArtifactIdentity{Name: path, Parser: "go-path", IdentityQuality: "path_only"}
}

func versionFromFilename(filename, packageName string) string {
	filename = strings.TrimSuffix(filename, ".tgz")
	prefix := packageName + "-"
	if strings.HasPrefix(filename, prefix) {
		return strings.TrimPrefix(filename, prefix)
	}
	return ""
}

func dockerTagFromAssetPath(path string) (string, string, bool) {
	const prefix = "/v2/"
	const marker = "/manifests/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	trimmed := strings.TrimPrefix(path, prefix)
	image, tag, ok := strings.Cut(trimmed, marker)
	if !ok || image == "" || tag == "" {
		return "", "", false
	}
	if image == "-" || strings.HasPrefix(tag, "sha256:") {
		return "", "", false
	}
	return image, tag, true
}

func dockerDigest(asset nexus.Asset) string {
	if asset.Checksum.SHA256 == "" {
		return ""
	}
	if strings.HasPrefix(asset.Checksum.SHA256, "sha256:") {
		return asset.Checksum.SHA256
	}
	return "sha256:" + asset.Checksum.SHA256
}

func assetChangedSince(asset nexus.Asset, since time.Time) (bool, bool) {
	for _, value := range []string{asset.LastModified, asset.BlobCreated, asset.LastDownloaded} {
		if value == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			continue
		}
		return !parsed.Before(since), true
	}
	return false, false
}

func (w *Worker) fetchManifestBatch(ctx context.Context, task db.ScanTask) *TaskError {
	var payload struct {
		RepositoryID   int64     `json:"repository_id"`
		RepositoryName string    `json:"repository_name"`
		RepositoryURL  string    `json:"repository_url"`
		Scope          taskScope `json:"scope"`
	}
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return &TaskError{Code: "invalid_task_payload", Class: "worker", Message: err.Error(), Retryable: false}
	}
	if payload.RepositoryID == 0 || payload.RepositoryURL == "" {
		return &TaskError{Code: "invalid_task_payload", Class: "worker", Message: "repository_id and repository_url are required", Retryable: false}
	}
	client, err := registry.NewRepositoryClient(payload.RepositoryURL, w.nexusUser, w.nexusPass)
	if err != nil {
		return &TaskError{Code: "invalid_registry_config", Class: "config", Message: err.Error(), Retryable: false}
	}
	var tags []db.ListActiveTagsForRepositoryRow
	since, hasSince := scopeSince(payload.Scope)
	if hasSince {
		recentTags, err := w.queries.ListActiveTagsForRepositorySeenSince(ctx, db.ListActiveTagsForRepositorySeenSinceParams{
			ID:         payload.RepositoryID,
			LastSeenAt: timestamptz(since),
		})
		if err != nil {
			return &TaskError{Code: "postgres_tag_list_failed", Class: "postgres", Message: err.Error(), Retryable: true}
		}
		tags = make([]db.ListActiveTagsForRepositoryRow, 0, len(recentTags))
		for _, tag := range recentTags {
			tags = append(tags, db.ListActiveTagsForRepositoryRow(tag))
		}
	} else {
		tags, err = w.queries.ListActiveTagsForRepository(ctx, payload.RepositoryID)
	}
	if err != nil {
		return &TaskError{Code: "postgres_tag_list_failed", Class: "postgres", Message: err.Error(), Retryable: true}
	}

	seen := map[string]struct{}{}
	manifests := 0
	layers := 0
	for _, tag := range tags {
		if err := w.waitForRateLimit(ctx, task); err != nil {
			return rateLimitError(task.CostClass, err)
		}
		doc, err := client.FetchManifest(ctx, tag.ImageName, tag.Tag)
		if err != nil {
			return classifyUpstreamError("fetch manifest", "registry_manifest_fetch", err)
		}
		if doc.Digest == "" {
			doc.Digest = tag.Digest
		}
		if _, ok := seen[doc.Digest]; ok {
			continue
		}
		seen[doc.Digest] = struct{}{}

		manifestCount, layerCount, taskErr := w.storeManifestDocument(ctx, payload.RepositoryID, doc, true)
		if taskErr != nil {
			return taskErr
		}
		manifests += manifestCount
		layers += layerCount

		for _, child := range doc.Children {
			if _, ok := seen[child.Digest]; ok {
				continue
			}
			if err := w.waitForRateLimit(ctx, task); err != nil {
				return rateLimitError(task.CostClass, err)
			}
			childDoc, err := client.FetchManifest(ctx, tag.ImageName, child.Digest)
			if err != nil {
				return classifyUpstreamError("fetch child manifest", "registry_child_manifest_fetch", err)
			}
			if childDoc.Digest == "" {
				childDoc.Digest = child.Digest
			}
			seen[childDoc.Digest] = struct{}{}
			manifestCount, layerCount, taskErr := w.storeManifestDocument(ctx, payload.RepositoryID, childDoc, false)
			if taskErr != nil {
				return taskErr
			}
			manifests += manifestCount
			layers += layerCount
		}
	}

	w.logger.Info("manifests fetched", "task_id", task.ID, "scan_run_id", task.ScanRunID, "repository", payload.RepositoryName, "tags", len(tags), "manifests", manifests, "layers", layers)
	return nil
}

func (w *Worker) runTagReclassification(ctx context.Context, task db.ScanTask) *TaskError {
	var payload struct {
		NexusInstanceID int64  `json:"nexus_instance_id"`
		RepositoryName  string `json:"repository_name"`
	}
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return &TaskError{Code: "invalid_task_payload", Class: "worker", Message: err.Error(), Retryable: false}
	}
	nexusInstanceID := payload.NexusInstanceID
	if nexusInstanceID <= 0 {
		nexusInstanceID = task.NexusInstanceID
	}
	classified, taskErr := w.classifyActiveTags(ctx, nexusInstanceID, 0, payload.RepositoryName)
	if taskErr != nil {
		return taskErr
	}
	w.logger.Info("tag reclassification complete", "task_id", task.ID, "scan_run_id", task.ScanRunID, "nexus_instance_id", nexusInstanceID, "repository", payload.RepositoryName, "classified_tags", classified)
	return nil
}

func (w *Worker) classifyActiveTagsForRepository(ctx context.Context, nexusInstanceID, repositoryID int64) (int64, *TaskError) {
	return w.classifyActiveTags(ctx, nexusInstanceID, repositoryID, "")
}

func (w *Worker) classifyActiveTags(ctx context.Context, nexusInstanceID, repositoryID int64, repositoryName string) (int64, *TaskError) {
	classified, err := w.queries.ClassifyActiveTags(ctx, db.ClassifyActiveTagsParams{
		NexusInstanceID: optionalInt8(nexusInstanceID),
		RepositoryID:    optionalInt8(repositoryID),
		RepositoryName:  text(repositoryName),
	})
	if err != nil {
		return 0, &TaskError{Code: "postgres_tag_classification_failed", Class: "postgres", Message: err.Error(), Retryable: true}
	}
	return classified, nil
}

func (w *Worker) storeManifestDocument(ctx context.Context, repositoryID int64, doc registry.ManifestDocument, tagTarget bool) (int, int, *TaskError) {
	if doc.Digest == "" {
		return 0, 0, &TaskError{Code: "registry_manifest_missing_digest", Class: "nexus", Message: "manifest response did not include a digest", Retryable: true}
	}
	configDigest := pgtype.Text{}
	if doc.Config != nil {
		configDigest = text(doc.Config.Digest)
	}
	manifest, err := w.queries.UpsertDockerManifest(ctx, db.UpsertDockerManifestParams{
		RepositoryID:     repositoryID,
		Digest:           doc.Digest,
		MediaType:        text(doc.MediaType),
		ConfigDigest:     configDigest,
		LogicalSizeBytes: int8(manifestLogicalSize(doc), true),
		Metadata: jsonBytes(map[string]any{
			"tag_target":             tagTarget,
			"child_manifests":        len(doc.Children),
			"child_manifest_digests": childManifestDigests(doc.Children),
		}),
	})
	if err != nil {
		return 0, 0, &TaskError{Code: "postgres_manifest_upsert_failed", Class: "postgres", Message: err.Error(), Retryable: true}
	}
	layerCount := 0
	for idx, desc := range doc.Layers {
		layer, err := w.queries.UpsertDockerLayer(ctx, db.UpsertDockerLayerParams{
			RepositoryID: repositoryID,
			Digest:       desc.Digest,
			MediaType:    text(desc.MediaType),
			SizeBytes:    int8(desc.Size, true),
		})
		if err != nil {
			return 0, 0, &TaskError{Code: "postgres_layer_upsert_failed", Class: "postgres", Message: err.Error(), Retryable: true}
		}
		if err := w.queries.LinkDockerManifestLayer(ctx, db.LinkDockerManifestLayerParams{
			ManifestID: manifest.ID,
			LayerID:    layer.ID,
			Ordinal:    int32(idx),
		}); err != nil {
			return 0, 0, &TaskError{Code: "postgres_manifest_layer_link_failed", Class: "postgres", Message: err.Error(), Retryable: true}
		}
		layerCount++
	}
	return 1, layerCount, nil
}

func childManifestDigests(children []registry.Descriptor) []string {
	out := make([]string, 0, len(children))
	for _, child := range children {
		if child.Digest != "" {
			out = append(out, child.Digest)
		}
	}
	return out
}

func manifestLogicalSize(doc registry.ManifestDocument) int64 {
	if len(doc.Layers) > 0 || doc.Config != nil {
		var total int64
		if doc.Config != nil {
			total += doc.Config.Size
		}
		for _, layer := range doc.Layers {
			total += layer.Size
		}
		return total
	}
	var total int64
	for _, child := range doc.Children {
		total += child.Size
	}
	if total == 0 {
		total = doc.Size
	}
	return total
}

func unsupportedReason(repo nexus.Repository) string {
	if repo.Format == "docker" && repo.Type == "hosted" {
		return ""
	}
	if repo.Type == "hosted" && packageAssetIndexFormat(repo.Format) {
		return ""
	}
	if repo.Type != "hosted" {
		return "repository is not hosted"
	}
	return "repository format " + repo.Format + " is not supported yet"
}

func packageAssetIndexFormat(format string) bool {
	return format == "pypi" || format == "npm" || format == "go"
}

func (w *Worker) completeTask(ctx context.Context, task db.ScanTask, attemptID int64) error {
	if _, err := w.queries.CompleteScanTask(ctx, db.CompleteScanTaskParams{
		ID:        task.ID,
		LockToken: task.LockToken,
		Stats:     emptyJSON,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("task lease lost: %w", err)
		}
		return err
	}
	if err := w.queries.FinishScanTaskAttempt(ctx, db.FinishScanTaskAttemptParams{
		ID:           attemptID,
		Status:       TaskStatusSucceeded,
		ErrorCode:    pgtype.Text{},
		ErrorClass:   pgtype.Text{},
		ErrorMessage: pgtype.Text{},
		ErrorDetails: emptyJSON,
		Stats:        emptyJSON,
	}); err != nil {
		return fmt.Errorf("finish task attempt: %w", err)
	}
	if _, err := w.refreshRunAndReleaseLock(ctx, task.ScanRunID); err != nil {
		return fmt.Errorf("refresh scan run counters: %w", err)
	}
	if _, err := w.queries.RecordNexusIndexingSuccess(ctx, task.NexusInstanceID); err != nil {
		return fmt.Errorf("record nexus indexing success: %w", err)
	}
	return nil
}

func (w *Worker) failTask(ctx context.Context, task db.ScanTask, attemptID int64, taskErr *TaskError) error {
	status := TaskStatusFailed
	if taskErr.Retryable && task.Attempt < task.MaxAttempts {
		status = TaskStatusRetryable
	}

	if status == TaskStatusRetryable {
		if _, err := w.queries.RetryScanTask(ctx, db.RetryScanTaskParams{
			ID:           task.ID,
			LockToken:    task.LockToken,
			AvailableAt:  timestamptz(time.Now().UTC().Add(retryDelayForTaskError(task.Attempt, taskErr))),
			ErrorCode:    textRequired(taskErr.Code),
			ErrorClass:   textRequired(taskErr.Class),
			ErrorMessage: textRequired(taskErr.Message),
			ErrorDetails: jsonBytes(taskErr.Details),
		}); err != nil {
			return err
		}
	} else {
		if _, err := w.queries.FailScanTask(ctx, db.FailScanTaskParams{
			ID:           task.ID,
			LockToken:    task.LockToken,
			ErrorCode:    textRequired(taskErr.Code),
			ErrorClass:   textRequired(taskErr.Class),
			ErrorMessage: textRequired(taskErr.Message),
			ErrorDetails: jsonBytes(taskErr.Details),
		}); err != nil {
			return err
		}
	}

	if err := w.queries.FinishScanTaskAttempt(ctx, db.FinishScanTaskAttemptParams{
		ID:           attemptID,
		Status:       status,
		ErrorCode:    textRequired(taskErr.Code),
		ErrorClass:   textRequired(taskErr.Class),
		ErrorMessage: textRequired(taskErr.Message),
		ErrorDetails: jsonBytes(taskErr.Details),
		Stats:        emptyJSON,
	}); err != nil {
		return fmt.Errorf("finish task attempt: %w", err)
	}
	if _, err := w.refreshRunAndReleaseLock(ctx, task.ScanRunID); err != nil {
		return fmt.Errorf("refresh scan run counters: %w", err)
	}
	if upstreamErrorTriggersCircuit(taskErr) {
		openedUntil := circuitOpenUntil(time.Now().UTC(), task.Attempt)
		if _, err := w.queries.RecordNexusIndexingFailure(ctx, db.RecordNexusIndexingFailureParams{
			NexusInstanceID:  task.NexusInstanceID,
			LastFailureCode:  textRequired(taskErr.Code),
			LastFailureClass: textRequired(taskErr.Class),
			Threshold:        circuitThreshold(),
			OpenUntil:        timestamptz(openedUntil),
		}); err != nil {
			return fmt.Errorf("record nexus indexing failure: %w", err)
		}
	}
	return nil
}

func (w *Worker) refreshRunAndReleaseLock(ctx context.Context, scanRunID int64) (db.ScanRun, error) {
	run, err := w.queries.RefreshScanRunTaskCounters(ctx, scanRunID)
	if err != nil {
		return db.ScanRun{}, err
	}
	if run.Status == "succeeded" || run.Status == "failed" || run.Status == "canceled" {
		if err := w.queries.ReleaseOperationLockByRun(ctx, scanRunID); err != nil {
			return db.ScanRun{}, fmt.Errorf("release operation lock: %w", err)
		}
	}
	return run, nil
}

func retryDelay(attempt int32) time.Duration {
	if attempt <= 0 {
		return time.Second
	}
	delay := time.Duration(attempt*attempt) * time.Second
	if delay > time.Minute {
		return time.Minute
	}
	return delay
}
