package indexing

import (
	"context"
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
	var verificationRunID pgtype.Int8
	deleteStats := cleanupExecutionStats{}

	if req.DestructiveEnabled {
		instance, err := s.queries.GetNexusInstanceByID(ctx, req.NexusInstanceID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return db.CleanupExecution{}, PolicyValidationError{Message: "nexus_instance_id was not found"}
			}
			return db.CleanupExecution{}, fmt.Errorf("get nexus instance: %w", err)
		}
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
			status, deleteStats, audit = s.executeCleanupDeletes(ctx, req, instance, simulation, audit)
			if deleteStats.DeletedCount > 0 {
				verificationRun, err := s.EnqueueRepositoryDiscoveryRun(ctx, EnqueueRepositoryDiscoveryRunRequest{
					NexusName:      instance.Name,
					BaseURL:        instance.BaseUrl,
					ScanType:       ScanTypeVerification,
					TriggerSource:  TriggerSourceManual,
					TriggerJobName: "cleanup-post-delete-verification",
					Scope: RunScope{
						Repositories: cleanupCandidateRepositories(simulation.Candidates),
					},
				})
				if err != nil {
					if status == "succeeded" {
						status = "completed_with_warnings"
					}
					audit = append(audit, cleanupAuditEvent(time.Now().UTC(), req.RequestedBy, "post_delete_verification_enqueue_failed", "post-delete verification run could not be enqueued", map[string]any{
						"error": err.Error(),
					}))
				} else {
					verificationRunID = int8(verificationRun.ID, true)
					audit = append(audit, cleanupAuditEvent(time.Now().UTC(), req.RequestedBy, "post_delete_verification_enqueued", "post-delete verification run enqueued", map[string]any{
						"scan_run_id": verificationRun.ID,
					}))
				}
			}
			finishedAt = timestamptz(time.Now().UTC())
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
		PostDeleteVerificationRunID: verificationRunID,
		ApprovedAt:                  timestamptz(now),
		FinishedAt:                  finishedAt,
	})
	if err != nil {
		return db.CleanupExecution{}, fmt.Errorf("create cleanup execution: %w", err)
	}
	return row, nil
}

type cleanupExecutionStats struct {
	DeletedCount int
	SkippedCount int
	FailedCount  int
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
