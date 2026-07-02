package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"

	"github.com/pbsladek/nexus-lens/internal/db"
	"github.com/pbsladek/nexus-lens/internal/nexus"
)

type cleanupPolicy struct {
	Name           string                 `yaml:"name"`
	Protected      cleanupMatcher         `yaml:"protected"`
	CandidateRules []cleanupCandidateRule `yaml:"candidate_rules"`
}

type cleanupMatcher struct {
	Patterns      []string `yaml:"patterns"`
	Tags          []string `yaml:"tags"`
	TagPrefixes   []string `yaml:"tag_prefixes"`
	ImagePrefixes []string `yaml:"image_prefixes"`
}

type cleanupCandidateRule struct {
	Name                string   `yaml:"name"`
	Patterns            []string `yaml:"patterns"`
	Tags                []string `yaml:"tags"`
	TagPrefixes         []string `yaml:"tag_prefixes"`
	ImagePrefixes       []string `yaml:"image_prefixes"`
	OlderThanDays       int      `yaml:"older_than_days"`
	NeverDownloaded     bool     `yaml:"never_downloaded"`
	MinReclaimableBytes int64    `yaml:"min_reclaimable_bytes"`
}

func (s *Service) ProcessNextCleanupExecution(ctx context.Context, req CleanupExecutionProcessRequest) (bool, error) {
	if strings.TrimSpace(req.WorkerID) == "" {
		req.WorkerID = "worker"
	}
	if req.LeaseSeconds <= 0 {
		req.LeaseSeconds = int32(DefaultWorkerLease.Seconds())
	}
	if req.DeleteRPS <= 0 {
		req.DeleteRPS = 0.5
	}

	reclaimed, err := s.queries.ReclaimExpiredCleanupExecutions(ctx)
	if err != nil {
		return false, fmt.Errorf("reclaim expired cleanup executions: %w", err)
	}
	for _, execution := range reclaimed {
		_, _ = s.createCleanupEvent(ctx, execution.ID, cleanupExecutionEventInput{
			EventType: "cleanup_reclaimed",
			Actor:     req.WorkerID,
			Message:   "expired cleanup execution lease reclaimed",
			Details: map[string]any{
				"previous_locked_by": textValue(execution.LockedBy),
			},
		})
	}

	execution, err := s.queries.ClaimQueuedCleanupExecution(ctx, db.ClaimQueuedCleanupExecutionParams{
		WorkerID:     text(req.WorkerID),
		LeaseSeconds: req.LeaseSeconds,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return len(reclaimed) > 0, nil
		}
		return len(reclaimed) > 0, fmt.Errorf("claim cleanup execution: %w", err)
	}
	if _, err := s.queries.AcquireCleanupLock(ctx, db.AcquireCleanupLockParams{
		NexusInstanceID:    execution.NexusInstanceID,
		CleanupExecutionID: execution.ID,
		Reason:             text("cleanup execution"),
	}); err != nil {
		events := appendCleanupAudit(execution.AuditLog, cleanupAuditEvent(time.Now().UTC(), req.WorkerID, "cleanup_lock_failed", "cleanup execution could not acquire cleanup lock", map[string]any{"error": err.Error()}))
		_, finishErr := s.queries.FinishCleanupExecution(ctx, db.FinishCleanupExecutionParams{
			ID:                execution.ID,
			Status:            "failed",
			SimulationSummary: execution.SimulationSummary,
			AuditLog:          jsonBytes(events),
		})
		if finishErr != nil {
			return true, fmt.Errorf("finish cleanup execution after lock failure: %w", finishErr)
		}
		return true, nil
	}
	defer func() {
		if err := s.queries.ReleaseCleanupLockByExecution(context.Background(), execution.ID); err != nil && s.logger != nil {
			s.logger.Error("release cleanup lock failed", "cleanup_execution_id", execution.ID, "error", err)
		}
	}()

	if _, err := s.createCleanupEvent(ctx, execution.ID, cleanupExecutionEventInput{
		EventType: "cleanup_started",
		Actor:     req.WorkerID,
		Message:   "cleanup execution started",
	}); err != nil && s.logger != nil {
		s.logger.Warn("create cleanup event failed", "cleanup_execution_id", execution.ID, "error", err)
	}

	finished, err := s.processClaimedCleanupExecution(ctx, execution, req)
	if err != nil {
		return true, err
	}
	if s.logger != nil {
		s.logger.Info("cleanup execution processed", "cleanup_execution_id", finished.ID, "status", finished.Status)
	}
	return true, nil
}

func (s *Service) processClaimedCleanupExecution(ctx context.Context, execution db.CleanupExecution, req CleanupExecutionProcessRequest) (db.CleanupExecution, error) {
	instance, err := s.queries.GetNexusInstanceByID(ctx, execution.NexusInstanceID)
	if err != nil {
		return db.CleanupExecution{}, fmt.Errorf("get nexus instance for cleanup execution: %w", err)
	}
	candidates, err := cleanupCandidatesFromExecution(execution)
	if err != nil {
		return db.CleanupExecution{}, err
	}
	audit := appendCleanupAudit(execution.AuditLog)
	stats := cleanupExecutionStats{}
	var verificationRunID pgtype.Int8
	var reconcileRunID pgtype.Int8

	if ok, message := cleanupMaintenanceWindowOpen(time.Now().UTC(), req.MaintenanceWindowStart, req.MaintenanceWindowEnd); !ok {
		audit = append(audit, cleanupAuditEvent(time.Now().UTC(), req.WorkerID, "blocked_maintenance_window", message, nil))
		_, _ = s.createCleanupEvent(ctx, execution.ID, cleanupExecutionEventInput{EventType: "blocked_maintenance_window", Actor: req.WorkerID, Message: message})
		return s.finishCleanupExecution(ctx, execution.ID, "blocked_maintenance_window", audit, stats, verificationRunID, reconcileRunID)
	}
	if strings.TrimSpace(req.NexusUsername) == "" || strings.TrimSpace(req.NexusPassword) == "" {
		audit = append(audit, cleanupAuditEvent(time.Now().UTC(), req.WorkerID, "blocked_missing_credentials", "cleanup execution requires Nexus credentials", nil))
		_, _ = s.createCleanupEvent(ctx, execution.ID, cleanupExecutionEventInput{EventType: "blocked_missing_credentials", Actor: req.WorkerID, Message: "cleanup execution requires Nexus credentials"})
		return s.finishCleanupExecution(ctx, execution.ID, "blocked_missing_credentials", audit, stats, verificationRunID, reconcileRunID)
	}
	client, err := nexus.NewClient(instance.BaseUrl, req.NexusUsername, req.NexusPassword)
	if err != nil {
		audit = append(audit, cleanupAuditEvent(time.Now().UTC(), req.WorkerID, "nexus_client_error", "cleanup execution could not create Nexus client", map[string]any{"error": err.Error()}))
		_, _ = s.createCleanupEvent(ctx, execution.ID, cleanupExecutionEventInput{EventType: "nexus_client_error", Actor: req.WorkerID, Message: "cleanup execution could not create Nexus client", ErrorMessage: err.Error()})
		return s.finishCleanupExecution(ctx, execution.ID, "failed", audit, stats, verificationRunID, reconcileRunID)
	}
	deleteDelay := cleanupDeleteDelay(req.DeleteRPS)
	for index, candidate := range candidates {
		latest, err := s.queries.GetCleanupExecution(ctx, execution.ID)
		if err != nil {
			return db.CleanupExecution{}, fmt.Errorf("check cleanup execution status: %w", err)
		}
		if latest.Status == "cancel_requested" {
			audit = append(audit, cleanupAuditEvent(time.Now().UTC(), req.WorkerID, "cleanup_canceled", "cleanup execution canceled before next delete", map[string]any{
				"deleted_count": stats.DeletedCount,
				"remaining":     len(candidates) - index,
			}))
			_, _ = s.createCleanupEvent(ctx, execution.ID, cleanupExecutionEventInput{EventType: "cleanup_canceled", Actor: req.WorkerID, Message: "cleanup execution canceled before next delete", Details: map[string]any{"remaining": len(candidates) - index}})
			return s.finishCleanupExecution(ctx, execution.ID, "canceled", audit, stats, verificationRunID, reconcileRunID)
		}
		if _, err := s.queries.TouchCleanupExecutionLease(ctx, db.TouchCleanupExecutionLeaseParams{ID: execution.ID, LeaseSeconds: req.LeaseSeconds}); err != nil {
			return db.CleanupExecution{}, fmt.Errorf("touch cleanup execution lease: %w", err)
		}
		if index > 0 && deleteDelay > 0 {
			timer := time.NewTimer(deleteDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return db.CleanupExecution{}, ctx.Err()
			case <-timer.C:
			}
		}
		stats, audit = s.processCleanupCandidate(ctx, execution.ID, req.WorkerID, client, candidate, stats, audit)
	}

	status := cleanupStatusFromStats(stats)
	if stats.DeletedCount > 0 {
		if err := s.queries.ReleaseCleanupLockByExecution(ctx, execution.ID); err != nil && s.logger != nil {
			s.logger.Error("release cleanup lock before post-delete runs failed", "cleanup_execution_id", execution.ID, "error", err)
		}
		repositories := cleanupExecutionCandidateRepositories(candidates)
		verificationRun, err := s.EnqueueRepositoryDiscoveryRun(ctx, EnqueueRepositoryDiscoveryRunRequest{
			NexusName:      instance.Name,
			BaseURL:        instance.BaseUrl,
			ScanType:       ScanTypeVerification,
			TriggerSource:  TriggerSourceManual,
			TriggerJobName: "cleanup-post-delete-verification",
			Scope:          RunScope{Repositories: repositories},
		})
		if err != nil {
			if status == "succeeded" {
				status = "completed_with_warnings"
			}
			audit = append(audit, cleanupAuditEvent(time.Now().UTC(), req.WorkerID, "post_delete_verification_enqueue_failed", "post-delete verification run could not be enqueued", map[string]any{"error": err.Error()}))
			_, _ = s.createCleanupEvent(ctx, execution.ID, cleanupExecutionEventInput{EventType: "post_delete_verification_enqueue_failed", Actor: req.WorkerID, Message: "post-delete verification run could not be enqueued", ErrorMessage: err.Error()})
		} else {
			verificationRunID = int8(verificationRun.ID, true)
			audit = append(audit, cleanupAuditEvent(time.Now().UTC(), req.WorkerID, "post_delete_verification_enqueued", "post-delete verification run enqueued", map[string]any{"scan_run_id": verificationRun.ID}))
			_, _ = s.createCleanupEvent(ctx, execution.ID, cleanupExecutionEventInput{EventType: "post_delete_verification_enqueued", Actor: req.WorkerID, Message: "post-delete verification run enqueued", Details: map[string]any{"scan_run_id": verificationRun.ID}})
		}
		reconcileRun, err := s.EnqueueRepositoryDiscoveryRun(ctx, EnqueueRepositoryDiscoveryRunRequest{
			NexusName:      instance.Name,
			BaseURL:        instance.BaseUrl,
			ScanType:       ScanTypeFullReconcile,
			TriggerSource:  TriggerSourceManual,
			TriggerJobName: "cleanup-post-delete-reconcile",
			Scope:          RunScope{Repositories: repositories},
		})
		if err != nil {
			if status == "succeeded" {
				status = "completed_with_warnings"
			}
			audit = append(audit, cleanupAuditEvent(time.Now().UTC(), req.WorkerID, "post_delete_reconcile_enqueue_failed", "post-delete reconcile run could not be enqueued", map[string]any{"error": err.Error()}))
			_, _ = s.createCleanupEvent(ctx, execution.ID, cleanupExecutionEventInput{EventType: "post_delete_reconcile_enqueue_failed", Actor: req.WorkerID, Message: "post-delete reconcile run could not be enqueued", ErrorMessage: err.Error()})
		} else {
			reconcileRunID = int8(reconcileRun.ID, true)
			audit = append(audit, cleanupAuditEvent(time.Now().UTC(), req.WorkerID, "post_delete_reconcile_enqueued", "post-delete reconcile run enqueued", map[string]any{"scan_run_id": reconcileRun.ID}))
			_, _ = s.createCleanupEvent(ctx, execution.ID, cleanupExecutionEventInput{EventType: "post_delete_reconcile_enqueued", Actor: req.WorkerID, Message: "post-delete reconcile run enqueued", Details: map[string]any{"scan_run_id": reconcileRun.ID}})
		}
	}
	return s.finishCleanupExecution(ctx, execution.ID, status, audit, stats, verificationRunID, reconcileRunID)
}

func (s *Service) processCleanupCandidate(ctx context.Context, executionID int64, actor string, client *nexus.Client, candidate cleanupExecutionCandidate, stats cleanupExecutionStats, audit []map[string]any) (cleanupExecutionStats, []map[string]any) {
	components, err := client.SearchDockerComponents(ctx, candidate.RepositoryName, candidate.ImageName, candidate.Tag)
	if err != nil {
		stats.FailedCount++
		audit = append(audit, cleanupExecutionCandidateAuditEvent(actor, "candidate_resolve_failed", "candidate component lookup failed", candidate, map[string]any{
			"error": err.Error(),
		}))
		_, _ = s.createCleanupEvent(ctx, executionID, cleanupExecutionCandidateEvent(actor, "candidate_resolve_failed", "failed", "candidate component lookup failed", candidate, "", map[string]any{"error": err.Error()}))
		return stats, audit
	}
	component, ok := uniqueMatchingApprovedDockerComponent(components, candidate)
	if !ok {
		stats.SkippedCount++
		audit = append(audit, cleanupExecutionCandidateAuditEvent(actor, "candidate_skipped_ambiguous_component", "candidate skipped because Nexus search did not return exactly one matching Docker component", candidate, map[string]any{
			"component_count": len(components),
		}))
		_, _ = s.createCleanupEvent(ctx, executionID, cleanupExecutionCandidateEvent(actor, "candidate_skipped_ambiguous_component", "skipped", "candidate skipped because Nexus search did not return exactly one matching Docker component", candidate, "", map[string]any{"component_count": len(components)}))
		return stats, audit
	}
	if !componentMatchesApprovedDigest(component, candidate.Digest) {
		stats.SkippedCount++
		audit = append(audit, cleanupExecutionCandidateAuditEvent(actor, "candidate_skipped_digest_mismatch", "candidate skipped because live Nexus component digest did not match the approved digest", candidate, map[string]any{
			"component_id": component.ID,
		}))
		_, _ = s.createCleanupEvent(ctx, executionID, cleanupExecutionCandidateEvent(actor, "candidate_skipped_digest_mismatch", "skipped", "candidate skipped because live Nexus component digest did not match the approved digest", candidate, component.ID, nil))
		return stats, audit
	}
	if err := client.DeleteComponent(ctx, component.ID); err != nil {
		stats.FailedCount++
		audit = append(audit, cleanupExecutionCandidateAuditEvent(actor, "candidate_delete_failed", "candidate component delete failed", candidate, map[string]any{
			"component_id": component.ID,
			"error":        err.Error(),
		}))
		_, _ = s.createCleanupEvent(ctx, executionID, cleanupExecutionCandidateEvent(actor, "candidate_delete_failed", "failed", "candidate component delete failed", candidate, component.ID, map[string]any{"error": err.Error()}))
		return stats, audit
	}
	if _, err := s.queries.MarkContainerTagDeleted(ctx, db.MarkContainerTagDeletedParams{
		ImageID: candidate.ImageID,
		Tag:     candidate.Tag,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			audit = append(audit, cleanupExecutionCandidateAuditEvent(actor, "candidate_index_already_inactive", "candidate component deleted in Nexus but local tag was already inactive", candidate, map[string]any{
				"component_id": component.ID,
			}))
			_, _ = s.createCleanupEvent(ctx, executionID, cleanupExecutionCandidateEvent(actor, "candidate_index_already_inactive", "warning", "candidate component deleted in Nexus but local tag was already inactive", candidate, component.ID, nil))
		} else {
			stats.DeletedCount++
			stats.FailedCount++
			audit = append(audit, cleanupExecutionCandidateAuditEvent(actor, "candidate_index_delete_mark_failed", "candidate component deleted in Nexus but local tag delete marking failed", candidate, map[string]any{
				"component_id": component.ID,
				"error":        err.Error(),
			}))
			_, _ = s.createCleanupEvent(ctx, executionID, cleanupExecutionCandidateEvent(actor, "candidate_index_delete_mark_failed", "failed", "candidate component deleted in Nexus but local tag delete marking failed", candidate, component.ID, map[string]any{"error": err.Error()}))
			return stats, audit
		}
	}
	stats.DeletedCount++
	audit = append(audit, cleanupExecutionCandidateAuditEvent(actor, "candidate_deleted", "candidate component deleted", candidate, map[string]any{
		"component_id": component.ID,
	}))
	_, _ = s.createCleanupEvent(ctx, executionID, cleanupExecutionCandidateEvent(actor, "candidate_deleted", "deleted", "candidate component deleted", candidate, component.ID, nil))
	return stats, audit
}

func (s *Service) finishCleanupExecution(ctx context.Context, executionID int64, status string, audit []map[string]any, stats cleanupExecutionStats, verificationRunID, reconcileRunID pgtype.Int8) (db.CleanupExecution, error) {
	summary := jsonBytes(map[string]any{
		"deleted_count": stats.DeletedCount,
		"skipped_count": stats.SkippedCount,
		"failed_count":  stats.FailedCount,
	})
	row, err := s.queries.FinishCleanupExecution(ctx, db.FinishCleanupExecutionParams{
		ID:                          executionID,
		Status:                      status,
		SimulationSummary:           summary,
		AuditLog:                    jsonBytes(audit),
		PostDeleteVerificationRunID: verificationRunID,
		PostDeleteReconcileRunID:    reconcileRunID,
	})
	if err != nil {
		return db.CleanupExecution{}, fmt.Errorf("finish cleanup execution: %w", err)
	}
	return row, nil
}

func cleanupCandidatesFromExecution(execution db.CleanupExecution) ([]cleanupExecutionCandidate, error) {
	if len(execution.ApprovedCandidates) == 0 {
		return nil, nil
	}
	var candidates []cleanupExecutionCandidate
	if err := json.Unmarshal(execution.ApprovedCandidates, &candidates); err != nil {
		return nil, fmt.Errorf("decode approved cleanup candidates: %w", err)
	}
	return candidates, nil
}

func appendCleanupAudit(raw []byte, events ...map[string]any) []map[string]any {
	audit := make([]map[string]any, 0)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &audit)
	}
	audit = append(audit, events...)
	return audit
}

func cleanupStatusFromStats(stats cleanupExecutionStats) string {
	switch {
	case stats.FailedCount > 0:
		return "failed"
	case stats.SkippedCount > 0:
		return "completed_with_warnings"
	case stats.DeletedCount == 0:
		return "succeeded_noop"
	default:
		return "succeeded"
	}
}

func cleanupExecutionCandidateRepositories(candidates []cleanupExecutionCandidate) []string {
	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		if candidate.RepositoryName != "" {
			seen[candidate.RepositoryName] = struct{}{}
		}
	}
	repositories := make([]string, 0, len(seen))
	for repository := range seen {
		repositories = append(repositories, repository)
	}
	sort.Strings(repositories)
	return repositories
}

func uniqueMatchingApprovedDockerComponent(components []nexus.Component, candidate cleanupExecutionCandidate) (nexus.Component, bool) {
	var matches []nexus.Component
	for _, component := range components {
		if component.Repository == candidate.RepositoryName &&
			component.Format == "docker" &&
			component.Name == candidate.ImageName &&
			component.Version == candidate.Tag {
			matches = append(matches, component)
		}
	}
	if len(matches) != 1 || matches[0].ID == "" {
		return nexus.Component{}, false
	}
	return matches[0], true
}

func componentMatchesApprovedDigest(component nexus.Component, approvedDigest string) bool {
	approvedDigest = strings.TrimPrefix(strings.TrimSpace(approvedDigest), "sha256:")
	if approvedDigest == "" {
		return true
	}
	for _, asset := range component.Assets {
		if asset.Checksum.SHA256 == approvedDigest {
			return true
		}
	}
	return false
}

func cleanupExecutionCandidateAuditEvent(actor, event, message string, candidate cleanupExecutionCandidate, details map[string]any) map[string]any {
	out := cleanupAuditEvent(time.Now().UTC(), actor, event, message, details)
	out["repository"] = candidate.RepositoryName
	out["image"] = candidate.ImageName
	out["tag"] = candidate.Tag
	out["tag_id"] = candidate.TagID
	out["rule_name"] = candidate.RuleName
	out["approved_digest"] = candidate.Digest
	return out
}

func cleanupExecutionCandidateEvent(actor, eventType, status, message string, candidate cleanupExecutionCandidate, componentID string, details map[string]any) cleanupExecutionEventInput {
	return cleanupExecutionEventInput{
		EventType:      eventType,
		Actor:          actor,
		RepositoryName: candidate.RepositoryName,
		ImageName:      candidate.ImageName,
		Tag:            candidate.Tag,
		TagID:          candidate.TagID,
		RuleName:       candidate.RuleName,
		ComponentID:    componentID,
		Status:         status,
		Message:        message,
		Details:        details,
	}
}

func (s *Service) SimulateCleanupPolicy(ctx context.Context, req CleanupSimulationRequest) (CleanupSimulationResult, error) {
	policy, err := parseCleanupPolicy(req.PolicyYAML)
	if err != nil {
		return CleanupSimulationResult{}, err
	}
	rows, err := s.queries.ListCleanupSimulationTags(ctx, db.ListCleanupSimulationTagsParams{
		NexusInstanceID: optionalInt8(req.NexusInstanceID),
		RepositoryName:  text(req.RepositoryName),
		LimitCount:      simulationLimit(req.Limit),
	})
	if err != nil {
		return CleanupSimulationResult{}, fmt.Errorf("list cleanup simulation tags: %w", err)
	}

	now := time.Now().UTC()
	result := CleanupSimulationResult{
		PolicyName:        policy.Name,
		EvaluatedTagCount: int64(len(rows)),
		Candidates:        make([]CleanupSimulationTag, 0),
		Protected:         make([]CleanupSimulationTag, 0),
		Warnings:          make([]CleanupSimulationWarning, 0),
	}
	for _, row := range rows {
		tag := cleanupSimulationTag(row)
		if row.MissingDigest {
			result.Warnings = append(result.Warnings, CleanupSimulationWarning{Tag: tag, Code: "missing_digest"})
		}
		if row.MissingManifest {
			result.Warnings = append(result.Warnings, CleanupSimulationWarning{Tag: tag, Code: "missing_manifest"})
		}
		if matchCleanupMatcher(policy.Protected, row) {
			tag.RuleName = "protected"
			tag.Reason = "matched protected rule"
			result.Protected = append(result.Protected, tag)
			continue
		}
		if rule, ok := matchCleanupCandidate(policy.CandidateRules, row, now); ok {
			tag.RuleName = rule.Name
			tag.Reason = cleanupCandidateReason(rule)
			result.Candidates = append(result.Candidates, tag)
			result.EstimatedReclaimableBytes += tag.EstimatedReclaimableBytes
		}
	}
	result.CandidateTagCount = int64(len(result.Candidates))
	result.ProtectedTagCount = int64(len(result.Protected))
	result.WarningCount = int64(len(result.Warnings))
	return result, nil
}

func (s *Service) ListCleanupExecutions(ctx context.Context, nexusInstanceID int64, limit int32) ([]db.CleanupExecution, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.queries.ListCleanupExecutions(ctx, db.ListCleanupExecutionsParams{
		NexusInstanceID: optionalInt8(nexusInstanceID),
		LimitCount:      limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list cleanup executions: %w", err)
	}
	return rows, nil
}

func (s *Service) ListCleanupExecutionEvents(ctx context.Context, cleanupExecutionID int64, limit int32) ([]db.CleanupExecutionEvent, error) {
	if cleanupExecutionID <= 0 {
		return nil, PolicyValidationError{Message: "cleanup execution id is required"}
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.queries.ListCleanupExecutionEvents(ctx, db.ListCleanupExecutionEventsParams{
		CleanupExecutionID: cleanupExecutionID,
		LimitCount:         limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list cleanup execution events: %w", err)
	}
	return rows, nil
}

func (s *Service) CancelCleanupExecution(ctx context.Context, cleanupExecutionID int64, canceledBy string) (db.CleanupExecution, error) {
	if cleanupExecutionID <= 0 {
		return db.CleanupExecution{}, PolicyValidationError{Message: "cleanup execution id is required"}
	}
	if strings.TrimSpace(canceledBy) == "" {
		canceledBy = "unknown"
	}
	row, err := s.queries.RequestCleanupExecutionCancel(ctx, db.RequestCleanupExecutionCancelParams{
		ID:         cleanupExecutionID,
		CanceledBy: text(canceledBy),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.CleanupExecution{}, PolicyValidationError{Message: "cleanup execution is not queued or running"}
		}
		return db.CleanupExecution{}, fmt.Errorf("cancel cleanup execution: %w", err)
	}
	_, _ = s.createCleanupEvent(ctx, row.ID, cleanupExecutionEventInput{
		EventType: "cleanup_cancel_requested",
		Actor:     canceledBy,
		Message:   "cleanup execution cancellation requested",
		Status:    row.Status,
	})
	return row, nil
}

func (s *Service) RequestCleanupExecution(ctx context.Context, req CleanupExecutionRequest) (db.CleanupExecution, error) {
	if req.NexusInstanceID <= 0 {
		return db.CleanupExecution{}, PolicyValidationError{Message: "nexus_instance_id is required"}
	}
	if strings.TrimSpace(req.RequestedBy) == "" {
		req.RequestedBy = "unknown"
	}
	simulation, err := s.SimulateCleanupPolicy(ctx, CleanupSimulationRequest{
		NexusInstanceID: req.NexusInstanceID,
		RepositoryName:  req.RepositoryName,
		PolicyYAML:      req.PolicyYAML,
		Limit:           req.Limit,
	})
	if err != nil {
		return db.CleanupExecution{}, err
	}
	if strings.TrimSpace(req.ApprovalText) != simulation.PolicyName {
		return db.CleanupExecution{}, PolicyValidationError{Message: "approval_text must match policy name " + simulation.PolicyName}
	}

	now := time.Now().UTC()
	audit := []map[string]any{cleanupAuditEvent(now, req.RequestedBy, "approval_recorded", "cleanup execution approval recorded", nil)}
	status := "blocked_read_only"
	finishedAt := timestamptz(now)
	deleteStats := cleanupExecutionStats{}
	approvedCandidates := cleanupApprovedCandidates(simulation.Candidates)

	if req.DestructiveEnabled {
		if conflict, err := s.operationLockConflict(ctx, req.NexusInstanceID); err != nil {
			return db.CleanupExecution{}, err
		} else if conflict != nil {
			status = "blocked_operation_lock"
			audit = append(audit, cleanupAuditEvent(now, req.RequestedBy, "blocked_operation_lock", "cleanup execution blocked by active index operation lock", map[string]any{
				"blocking_scan_run_id": conflict.BlockingScanRunID,
				"blocking_scan_type":   conflict.BlockingScanType,
				"blocking_status":      conflict.BlockingStatus,
			}))
		} else if ok, message := cleanupMaintenanceWindowOpen(now, req.MaintenanceWindowStart, req.MaintenanceWindowEnd); !ok {
			status = "blocked_maintenance_window"
			audit = append(audit, cleanupAuditEvent(now, req.RequestedBy, "blocked_maintenance_window", message, map[string]any{
				"maintenance_window_start": req.MaintenanceWindowStart,
				"maintenance_window_end":   req.MaintenanceWindowEnd,
				"checked_at_utc":           now.Format(time.RFC3339),
			}))
		} else if strings.TrimSpace(req.NexusUsername) == "" || strings.TrimSpace(req.NexusPassword) == "" {
			status = "blocked_missing_credentials"
			audit = append(audit, cleanupAuditEvent(now, req.RequestedBy, "blocked_missing_credentials", "cleanup execution requires Nexus credentials", nil))
		} else {
			status = "queued"
			finishedAt = pgtype.Timestamptz{}
			audit = append(audit, cleanupAuditEvent(now, req.RequestedBy, "cleanup_queued", "cleanup execution queued for worker processing", map[string]any{
				"approved_candidate_count": len(approvedCandidates),
			}))
		}
	} else {
		audit = append(audit, cleanupAuditEvent(now, req.RequestedBy, "blocked_read_only", "destructive cleanup execution blocked because read-only mode is enabled", nil))
	}

	summary := jsonBytes(map[string]any{
		"policy_name":                 simulation.PolicyName,
		"evaluated_tag_count":         simulation.EvaluatedTagCount,
		"candidate_tag_count":         simulation.CandidateTagCount,
		"protected_tag_count":         simulation.ProtectedTagCount,
		"warning_count":               simulation.WarningCount,
		"estimated_reclaimable_bytes": simulation.EstimatedReclaimableBytes,
		"deleted_count":               deleteStats.DeletedCount,
		"skipped_count":               deleteStats.SkippedCount,
		"failed_count":                deleteStats.FailedCount,
	})
	row, err := s.queries.CreateCleanupExecution(ctx, db.CreateCleanupExecutionParams{
		NexusInstanceID:             req.NexusInstanceID,
		RepositoryName:              text(req.RepositoryName),
		PolicyName:                  simulation.PolicyName,
		PolicyYaml:                  req.PolicyYAML,
		Status:                      status,
		RequestedBy:                 req.RequestedBy,
		ApprovedBy:                  text(req.RequestedBy),
		ApprovalText:                req.ApprovalText,
		DestructiveEnabled:          req.DestructiveEnabled,
		SimulationSummary:           summary,
		AuditLog:                    jsonBytes(audit),
		ApprovedCandidates:          jsonBytes(approvedCandidates),
		ApprovedCandidateCount:      int64(len(approvedCandidates)),
		PostDeleteVerificationRunID: pgtype.Int8{},
		PostDeleteReconcileRunID:    pgtype.Int8{},
		ApprovedAt:                  timestamptz(now),
		StartedAt:                   pgtype.Timestamptz{},
		FinishedAt:                  finishedAt,
	})
	if err != nil {
		return db.CleanupExecution{}, fmt.Errorf("create cleanup execution: %w", err)
	}
	for _, event := range audit {
		_, _ = s.createCleanupEvent(ctx, row.ID, cleanupEventFromAudit(event))
	}
	return row, nil
}

type cleanupExecutionStats struct {
	DeletedCount int
	SkippedCount int
	FailedCount  int
}

type cleanupExecutionCandidate struct {
	TagID                     int64  `json:"tag_id"`
	ImageID                   int64  `json:"image_id"`
	RepositoryID              int64  `json:"repository_id"`
	NexusInstanceID           int64  `json:"nexus_instance_id"`
	RepositoryName            string `json:"repository_name"`
	ImageName                 string `json:"image_name"`
	Tag                       string `json:"tag"`
	Digest                    string `json:"digest"`
	PatternName               string `json:"pattern_name"`
	RuleName                  string `json:"rule_name"`
	Reason                    string `json:"reason"`
	DockerLogicalBytes        int64  `json:"docker_logical_bytes"`
	EstimatedReclaimableBytes int64  `json:"estimated_reclaimable_bytes"`
}

type cleanupExecutionEventInput struct {
	EventType      string
	Actor          string
	RepositoryName string
	ImageName      string
	Tag            string
	TagID          int64
	RuleName       string
	ComponentID    string
	Status         string
	Message        string
	ErrorClass     string
	ErrorCode      string
	ErrorMessage   string
	Details        map[string]any
}

func cleanupApprovedCandidates(tags []CleanupSimulationTag) []cleanupExecutionCandidate {
	candidates := make([]cleanupExecutionCandidate, 0, len(tags))
	for _, tag := range tags {
		candidates = append(candidates, cleanupExecutionCandidate{
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
		})
	}
	return candidates
}

func (s *Service) createCleanupEvent(ctx context.Context, executionID int64, event cleanupExecutionEventInput) (db.CleanupExecutionEvent, error) {
	return s.queries.CreateCleanupExecutionEvent(ctx, db.CreateCleanupExecutionEventParams{
		CleanupExecutionID: executionID,
		EventType:          event.EventType,
		Actor:              text(event.Actor),
		RepositoryName:     text(event.RepositoryName),
		ImageName:          text(event.ImageName),
		Tag:                text(event.Tag),
		TagID:              optionalInt8(event.TagID),
		RuleName:           text(event.RuleName),
		ComponentID:        text(event.ComponentID),
		Status:             text(event.Status),
		Message:            text(event.Message),
		ErrorClass:         text(event.ErrorClass),
		ErrorCode:          text(event.ErrorCode),
		ErrorMessage:       text(event.ErrorMessage),
		Details:            jsonBytes(event.Details),
	})
}

func cleanupEventFromAudit(event map[string]any) cleanupExecutionEventInput {
	input := cleanupExecutionEventInput{
		EventType: stringFromMap(event, "event"),
		Actor:     stringFromMap(event, "actor"),
		Message:   stringFromMap(event, "message"),
		Details:   mapWithoutKeys(event, "event", "actor", "message"),
	}
	input.RepositoryName = stringFromMap(event, "repository")
	input.ImageName = stringFromMap(event, "image")
	input.Tag = stringFromMap(event, "tag")
	input.RuleName = stringFromMap(event, "rule_name")
	input.ComponentID = stringFromMap(event, "component_id")
	input.TagID = int64FromMap(event, "tag_id")
	return input
}

func (s *Service) executeCleanupDeletes(ctx context.Context, req CleanupExecutionRequest, instance db.GetNexusInstanceByIDRow, simulation CleanupSimulationResult, audit []map[string]any) (string, cleanupExecutionStats, []map[string]any) {
	stats := cleanupExecutionStats{}
	if len(simulation.Candidates) == 0 {
		audit = append(audit, cleanupAuditEvent(time.Now().UTC(), req.RequestedBy, "cleanup_noop", "cleanup execution found no candidate tags", nil))
		return "succeeded_noop", stats, audit
	}

	client, err := nexus.NewClient(instance.BaseUrl, req.NexusUsername, req.NexusPassword)
	if err != nil {
		stats.FailedCount = len(simulation.Candidates)
		audit = append(audit, cleanupAuditEvent(time.Now().UTC(), req.RequestedBy, "nexus_client_error", "cleanup execution could not create Nexus client", map[string]any{
			"error": err.Error(),
		}))
		return "failed", stats, audit
	}
	deleteDelay := cleanupDeleteDelay(req.DeleteRPS)
	for index, candidate := range simulation.Candidates {
		if index > 0 && deleteDelay > 0 {
			timer := time.NewTimer(deleteDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				stats.FailedCount += len(simulation.Candidates) - index
				audit = append(audit, cleanupAuditEvent(time.Now().UTC(), req.RequestedBy, "cleanup_context_canceled", "cleanup execution stopped because the request context was canceled", map[string]any{
					"error": ctx.Err().Error(),
				}))
				return "failed", stats, audit
			case <-timer.C:
			}
		}
		components, err := client.SearchDockerComponents(ctx, candidate.RepositoryName, candidate.ImageName, candidate.Tag)
		if err != nil {
			stats.FailedCount++
			audit = append(audit, cleanupCandidateAuditEvent(req.RequestedBy, "candidate_resolve_failed", "candidate component lookup failed", candidate, map[string]any{
				"error": err.Error(),
			}))
			continue
		}
		component, ok := uniqueMatchingDockerComponent(components, candidate)
		if !ok {
			stats.SkippedCount++
			audit = append(audit, cleanupCandidateAuditEvent(req.RequestedBy, "candidate_skipped_ambiguous_component", "candidate skipped because Nexus search did not return exactly one matching Docker component", candidate, map[string]any{
				"component_count": len(components),
			}))
			continue
		}
		if err := client.DeleteComponent(ctx, component.ID); err != nil {
			stats.FailedCount++
			audit = append(audit, cleanupCandidateAuditEvent(req.RequestedBy, "candidate_delete_failed", "candidate component delete failed", candidate, map[string]any{
				"component_id": component.ID,
				"error":        err.Error(),
			}))
			continue
		}
		if _, err := s.queries.MarkContainerTagDeleted(ctx, db.MarkContainerTagDeletedParams{
			ImageID: candidate.ImageID,
			Tag:     candidate.Tag,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				audit = append(audit, cleanupCandidateAuditEvent(req.RequestedBy, "candidate_index_already_inactive", "candidate component deleted in Nexus but local tag was already inactive", candidate, map[string]any{
					"component_id": component.ID,
				}))
			} else {
				stats.DeletedCount++
				stats.FailedCount++
				audit = append(audit, cleanupCandidateAuditEvent(req.RequestedBy, "candidate_index_delete_mark_failed", "candidate component deleted in Nexus but local tag delete marking failed", candidate, map[string]any{
					"component_id": component.ID,
					"error":        err.Error(),
				}))
				continue
			}
		}
		stats.DeletedCount++
		audit = append(audit, cleanupCandidateAuditEvent(req.RequestedBy, "candidate_deleted", "candidate component deleted", candidate, map[string]any{
			"component_id": component.ID,
		}))
	}

	switch {
	case stats.FailedCount > 0:
		return "failed", stats, audit
	case stats.SkippedCount > 0:
		return "completed_with_warnings", stats, audit
	default:
		return "succeeded", stats, audit
	}
}

func cleanupDeleteDelay(rps float64) time.Duration {
	if rps <= 0 {
		rps = 0.5
	}
	return time.Duration(float64(time.Second) / rps)
}

func cleanupMaintenanceWindowOpen(now time.Time, startValue, endValue string) (bool, string) {
	startValue = strings.TrimSpace(startValue)
	endValue = strings.TrimSpace(endValue)
	if startValue == "" && endValue == "" {
		return true, ""
	}
	start, err := time.Parse("15:04", startValue)
	if err != nil {
		return false, "cleanup maintenance window start must use HH:MM UTC"
	}
	end, err := time.Parse("15:04", endValue)
	if err != nil {
		return false, "cleanup maintenance window end must use HH:MM UTC"
	}
	currentMinute := now.UTC().Hour()*60 + now.UTC().Minute()
	startMinute := start.Hour()*60 + start.Minute()
	endMinute := end.Hour()*60 + end.Minute()
	if startMinute == endMinute {
		return false, "cleanup maintenance window start and end must differ"
	}
	if startMinute < endMinute {
		if currentMinute >= startMinute && currentMinute < endMinute {
			return true, ""
		}
		return false, "cleanup execution is outside the configured UTC maintenance window"
	}
	if currentMinute >= startMinute || currentMinute < endMinute {
		return true, ""
	}
	return false, "cleanup execution is outside the configured UTC maintenance window"
}

func uniqueMatchingDockerComponent(components []nexus.Component, candidate CleanupSimulationTag) (nexus.Component, bool) {
	var matches []nexus.Component
	for _, component := range components {
		if component.Repository == candidate.RepositoryName &&
			component.Format == "docker" &&
			component.Name == candidate.ImageName &&
			component.Version == candidate.Tag {
			matches = append(matches, component)
		}
	}
	if len(matches) != 1 || matches[0].ID == "" {
		return nexus.Component{}, false
	}
	return matches[0], true
}

func cleanupCandidateRepositories(candidates []CleanupSimulationTag) []string {
	seen := make(map[string]struct{})
	for _, candidate := range candidates {
		if candidate.RepositoryName != "" {
			seen[candidate.RepositoryName] = struct{}{}
		}
	}
	repositories := make([]string, 0, len(seen))
	for repository := range seen {
		repositories = append(repositories, repository)
	}
	sort.Strings(repositories)
	return repositories
}

func cleanupAuditEvent(at time.Time, actor, event, message string, details map[string]any) map[string]any {
	out := map[string]any{
		"at":      at.UTC().Format(time.RFC3339),
		"actor":   actor,
		"event":   event,
		"message": message,
	}
	for key, value := range details {
		out[key] = value
	}
	return out
}

func cleanupCandidateAuditEvent(actor, event, message string, candidate CleanupSimulationTag, details map[string]any) map[string]any {
	out := cleanupAuditEvent(time.Now().UTC(), actor, event, message, details)
	out["repository"] = candidate.RepositoryName
	out["image"] = candidate.ImageName
	out["tag"] = candidate.Tag
	out["tag_id"] = candidate.TagID
	out["rule_name"] = candidate.RuleName
	return out
}

func parseCleanupPolicy(raw string) (cleanupPolicy, error) {
	if strings.TrimSpace(raw) == "" {
		return cleanupPolicy{}, PolicyValidationError{Message: "policy_yaml is required"}
	}
	var policy cleanupPolicy
	if err := yaml.Unmarshal([]byte(raw), &policy); err != nil {
		return cleanupPolicy{}, PolicyValidationError{Message: "policy YAML is invalid: " + err.Error()}
	}
	if strings.TrimSpace(policy.Name) == "" {
		policy.Name = "cleanup-simulation"
	}
	if len(policy.CandidateRules) == 0 {
		return cleanupPolicy{}, PolicyValidationError{Message: "at least one candidate rule is required"}
	}
	for index, rule := range policy.CandidateRules {
		if strings.TrimSpace(rule.Name) == "" {
			return cleanupPolicy{}, PolicyValidationError{Message: fmt.Sprintf("candidate rule %d requires a name", index+1)}
		}
	}
	return policy, nil
}

func matchCleanupCandidate(rules []cleanupCandidateRule, row db.ListCleanupSimulationTagsRow, now time.Time) (cleanupCandidateRule, bool) {
	for _, rule := range rules {
		if !matchCleanupMatcher(cleanupMatcher{
			Patterns:      rule.Patterns,
			Tags:          rule.Tags,
			TagPrefixes:   rule.TagPrefixes,
			ImagePrefixes: rule.ImagePrefixes,
		}, row) {
			continue
		}
		if rule.OlderThanDays > 0 {
			if !row.FirstSeenAt.Valid || row.FirstSeenAt.Time.After(now.Add(-time.Duration(rule.OlderThanDays)*24*time.Hour)) {
				continue
			}
		}
		if rule.NeverDownloaded && timeFromAny(row.LastDownloadedAt) != nil {
			continue
		}
		if rule.MinReclaimableBytes > 0 && row.EstimatedReclaimableBytes < rule.MinReclaimableBytes {
			continue
		}
		return rule, true
	}
	return cleanupCandidateRule{}, false
}

func matchCleanupMatcher(matcher cleanupMatcher, row db.ListCleanupSimulationTagsRow) bool {
	hasMatcher := len(matcher.Patterns) > 0 || len(matcher.Tags) > 0 || len(matcher.TagPrefixes) > 0 || len(matcher.ImagePrefixes) > 0
	if !hasMatcher {
		return false
	}
	if len(matcher.Patterns) > 0 && !stringIn(row.PatternName, matcher.Patterns) {
		return false
	}
	if len(matcher.Tags) > 0 && !stringIn(row.Tag, matcher.Tags) {
		return false
	}
	if len(matcher.TagPrefixes) > 0 && !hasAnyPrefix(row.Tag, matcher.TagPrefixes) {
		return false
	}
	if len(matcher.ImagePrefixes) > 0 && !hasAnyPrefix(row.ImageName, matcher.ImagePrefixes) {
		return false
	}
	return true
}

func cleanupSimulationTag(row db.ListCleanupSimulationTagsRow) CleanupSimulationTag {
	tag := CleanupSimulationTag{
		TagID:                     row.TagID,
		ImageID:                   row.ImageID,
		RepositoryID:              row.RepositoryID,
		NexusInstanceID:           row.NexusInstanceID,
		RepositoryName:            row.RepositoryName,
		ImageName:                 row.ImageName,
		Tag:                       row.Tag,
		Digest:                    row.Digest,
		PatternName:               row.PatternName,
		DockerLogicalBytes:        row.DockerLogicalBytes,
		EstimatedReclaimableBytes: row.EstimatedReclaimableBytes,
		LastDownloadedAt:          timeFromAny(row.LastDownloadedAt),
	}
	if row.FirstSeenAt.Valid {
		tag.FirstSeenAt = row.FirstSeenAt.Time
	}
	if row.LastSeenAt.Valid {
		tag.LastSeenAt = row.LastSeenAt.Time
	}
	return tag
}

func cleanupCandidateReason(rule cleanupCandidateRule) string {
	parts := make([]string, 0, 3)
	if len(rule.Patterns) > 0 {
		parts = append(parts, "pattern")
	}
	if rule.OlderThanDays > 0 {
		parts = append(parts, fmt.Sprintf("older than %dd", rule.OlderThanDays))
	}
	if rule.NeverDownloaded {
		parts = append(parts, "never downloaded")
	}
	if len(parts) == 0 {
		return "matched candidate rule"
	}
	return strings.Join(parts, ", ")
}

func simulationLimit(limit int32) int32 {
	if limit <= 0 || limit > 5000 {
		return 1000
	}
	return limit
}

func stringIn(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func timeFromAny(value any) *time.Time {
	switch typed := value.(type) {
	case nil:
		return nil
	case time.Time:
		return &typed
	}
	return nil
}

func stringFromMap(values map[string]any, key string) string {
	if value, ok := values[key].(string); ok {
		return value
	}
	return ""
}

func int64FromMap(values map[string]any, key string) int64 {
	switch value := values[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func mapWithoutKeys(values map[string]any, keys ...string) map[string]any {
	out := make(map[string]any)
	skip := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		skip[key] = struct{}{}
	}
	for key, value := range values {
		if _, ok := skip[key]; ok {
			continue
		}
		out[key] = value
	}
	return out
}
