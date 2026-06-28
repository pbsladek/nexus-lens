package indexing

import (
	"errors"
	"fmt"
	"time"
)

const (
	ScanTypeRecentSync          = "recent_sync"
	ScanTypeBoundedReindex      = "bounded_reindex"
	ScanTypeFullReconcile       = "full_reconcile"
	ScanTypeVerification        = "verification"
	ScanTypeTrendSnapshot       = "trend_snapshot"
	ScanTypeTagReclassification = "tag_reclassification"
	TriggerSourceManual         = "manual"
	TriggerSourceScheduler      = "scheduler"
	TaskTypeNoop                = "noop"
	TaskTypeRepositoryDiscovery = "repository_discovery"
	TaskTypeDockerTagScan       = "docker_tag_scan"
	TaskTypeManifestFetchBatch  = "manifest_fetch_batch"
	TaskTypeVerificationChecks  = "verification_checks"
	TaskTypeTrendSnapshot       = "trend_snapshot"
	TaskTypeTagReclassification = "tag_reclassification"
	TaskStatusSucceeded         = "succeeded"
	TaskStatusFailed            = "failed"
	TaskStatusRetryable         = "retryable"
	DefaultWorkerBatchSize      = 10
	DefaultWorkerLease          = 5 * time.Minute
	DefaultWorkerPollPeriod     = 2 * time.Second
)

var ErrUnsupportedTaskType = errors.New("unsupported task type")

type EnqueueRepositoryDiscoveryRunRequest struct {
	NexusName      string
	BaseURL        string
	ScanType       string
	TaskKey        string
	TriggerSource  string
	TriggerJobName string
	Scope          RunScope
}

type ScheduleRequest struct {
	NexusName       string
	BaseURL         string
	Name            string
	ScanType        string
	IntervalSeconds int32
	Enabled         bool
	DueNow          bool
	Scope           RunScope
}

type RateLimitSettings struct {
	AssetListRPS        *float64
	RegistryManifestRPS *float64
	DefaultRPS          *float64
}

type AnalyticsListRequest struct {
	NexusInstanceID int64
	RepositoryName  string
	Search          string
	OlderThanDays   int
	NeverDownloaded bool
	Limit           int32
}

type TagPatternRequest struct {
	Name        string
	DisplayName string
	Description string
	TagRegex    string
	Priority    int32
	Enabled     bool
}

type TagPatternListRequest struct {
	NexusInstanceID  int64
	RepositoryName   string
	PatternName      string
	OlderThanDays    int
	GrowthWindowDays int
	NeverDownloaded  bool
	Limit            int32
}

type TrendSeriesRequest struct {
	NexusInstanceID int64
	RepositoryID    int64
	ImageID         int64
	MetricName      string
	PatternName     string
	StartDate       string
	EndDate         string
}

type TagReclassificationRequest struct {
	NexusInstanceID int64
	RepositoryName  string
}

type SchedulerResult struct {
	Processed int
	Enqueued  int
	Skipped   int
}

type RunScope struct {
	Repositories []string `json:"repositories,omitempty"`
	WindowDays   int      `json:"window_days,omitempty"`
}

type TaskError struct {
	Code       string
	Class      string
	Message    string
	Retryable  bool
	RetryAfter time.Duration
	Details    map[string]any
}

func (e TaskError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

type OperationLockConflict struct {
	NexusInstanceID   int64
	BlockingScanRunID int64
	BlockingScanType  string
	BlockingStatus    string
}

func (e *OperationLockConflict) Error() string {
	return fmt.Sprintf("nexus instance %d is blocked by %s scan run %d", e.NexusInstanceID, e.BlockingScanType, e.BlockingScanRunID)
}

func (e *OperationLockConflict) OperationLockConflict() (int64, string) {
	return e.BlockingScanRunID, e.BlockingScanType
}
