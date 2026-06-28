package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"time"
)

type Server struct {
	logger       *slog.Logger
	mux          *http.ServeMux
	readyCheck   func(context.Context) error
	corsOrigin   string
	auth         AuthenticateFunc
	session      SessionManager
	indexing     IndexingService
	cookieSecure bool
}

type Option func(*Server)

type AuthenticatedUser struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

type AuthenticateFunc func(ctx context.Context, username, password string) (AuthenticatedUser, error)

type SessionManager interface {
	CreateSession(ctx context.Context, username, password string) (Session, error)
	AuthenticateSession(ctx context.Context, token string) (AuthenticatedUser, error)
	RevokeSession(ctx context.Context, token string) error
}

type Session struct {
	Token     string
	ExpiresAt time.Time
	User      AuthenticatedUser
}

type IndexingService interface {
	StartIndexRun(ctx context.Context, req StartIndexRunRequest) (IndexRun, error)
	ListIndexRuns(ctx context.Context, limit int32) ([]IndexRun, error)
	ListIndexTasks(ctx context.Context, scanRunID int64, limit int32) ([]IndexTask, error)
	RetryIndexTask(ctx context.Context, taskID int64) (IndexTask, error)
	CancelIndexRun(ctx context.Context, scanRunID int64) (IndexRun, error)
	ListVerificationFindings(ctx context.Context, nexusInstanceID int64, status string, limit int32) ([]VerificationFinding, error)
	DismissVerificationFinding(ctx context.Context, findingID int64, dismissedBy string) (VerificationFinding, error)
	PrometheusMetrics(ctx context.Context) (string, error)
	ListNexusInstances(ctx context.Context) ([]NexusInstance, error)
	ListIndexingSchedules(ctx context.Context, nexusInstanceID int64) ([]IndexingSchedule, error)
	UpsertIndexingSchedule(ctx context.Context, req UpsertIndexingScheduleRequest) (IndexingSchedule, error)
	UpdateNexusRateLimits(ctx context.Context, nexusInstanceID int64, req RateLimitSettings) (IndexingControl, error)
	GetAnalyticsOverview(ctx context.Context, nexusInstanceID int64) (AnalyticsOverview, error)
	ListRepositoryAnalytics(ctx context.Context, req AnalyticsListRequest) ([]RepositoryAnalytics, error)
	ListImageAnalytics(ctx context.Context, req AnalyticsListRequest) ([]ImageAnalytics, error)
	ListTagAnalytics(ctx context.Context, req AnalyticsListRequest) ([]TagAnalytics, error)
	ListPackageFormatRepositories(ctx context.Context, req AnalyticsListRequest) ([]PackageFormatRepository, error)
	ListTrendSeries(ctx context.Context, req TrendSeriesRequest) ([]TrendPoint, error)
	ListTrendTopMovers(ctx context.Context, req TrendTopMoversRequest) ([]TrendTopMover, error)
	SimulateCleanupPolicy(ctx context.Context, req CleanupSimulationRequest) (CleanupSimulationResult, error)
	RequestCleanupExecution(ctx context.Context, req CleanupExecutionRequest) (CleanupExecution, error)
	ListTagPatterns(ctx context.Context) ([]TagPattern, error)
	UpsertTagPattern(ctx context.Context, req UpsertTagPatternRequest) (TagPattern, error)
	StartTagReclassification(ctx context.Context, req TagReclassificationRequest) (IndexRun, error)
	ListTagPatternAnalytics(ctx context.Context, req TagPatternListRequest) ([]TagPatternAnalytics, error)
	ListTagPatternGrowth(ctx context.Context, req TagPatternListRequest) ([]TagPatternGrowth, error)
	ListTagPatternExamples(ctx context.Context, req TagPatternListRequest) ([]TagPatternExample, error)
	ListTagPatternSuggestions(ctx context.Context, req TagPatternListRequest) ([]TagPatternSuggestion, error)
	PauseNexusIndexing(ctx context.Context, nexusInstanceID int64, reason, pausedBy string) (IndexingControl, error)
	ResumeNexusIndexing(ctx context.Context, nexusInstanceID int64) (IndexingControl, error)
}

type StartIndexRunRequest struct {
	NexusName string   `json:"nexus_name"`
	BaseURL   string   `json:"base_url"`
	ScanType  string   `json:"scan_type"`
	TaskKey   string   `json:"task_key"`
	Scope     RunScope `json:"scope"`
}

type RunScope struct {
	Repositories []string `json:"repositories,omitempty"`
	WindowDays   int      `json:"window_days,omitempty"`
}

type IndexRun struct {
	ID              int64      `json:"id"`
	NexusInstanceID int64      `json:"nexus_instance_id"`
	ScanType        string     `json:"scan_type"`
	Status          string     `json:"status"`
	Phase           string     `json:"phase,omitempty"`
	CompletedTasks  int64      `json:"completed_tasks"`
	FailedTasks     int64      `json:"failed_tasks"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
}

type IndexTask struct {
	ID              int64      `json:"id"`
	ScanRunID       int64      `json:"scan_run_id"`
	NexusInstanceID int64      `json:"nexus_instance_id"`
	TaskType        string     `json:"task_type"`
	TaskKey         string     `json:"task_key"`
	RepositoryName  string     `json:"repository_name,omitempty"`
	Subject         string     `json:"subject,omitempty"`
	CostClass       string     `json:"cost_class"`
	Status          string     `json:"status"`
	Attempt         int32      `json:"attempt"`
	MaxAttempts     int32      `json:"max_attempts"`
	AvailableAt     time.Time  `json:"available_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	LockedBy        string     `json:"locked_by,omitempty"`
	ErrorCode       string     `json:"error_code,omitempty"`
	ErrorClass      string     `json:"error_class,omitempty"`
	ErrorMessage    string     `json:"error_message,omitempty"`
	ErrorDetails    any        `json:"error_details,omitempty"`
}

type VerificationFinding struct {
	ID              int64      `json:"id"`
	NexusInstanceID int64      `json:"nexus_instance_id,omitempty"`
	ScanRunID       int64      `json:"scan_run_id,omitempty"`
	Severity        string     `json:"severity"`
	FindingType     string     `json:"finding_type"`
	RepositoryName  string     `json:"repository_name,omitempty"`
	Subject         string     `json:"subject,omitempty"`
	Expected        any        `json:"expected,omitempty"`
	Actual          any        `json:"actual,omitempty"`
	Message         string     `json:"message,omitempty"`
	Remediation     string     `json:"remediation,omitempty"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"created_at"`
	LastSeenAt      time.Time  `json:"last_seen_at"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	DismissedAt     *time.Time `json:"dismissed_at,omitempty"`
	Metadata        any        `json:"metadata,omitempty"`
}

type NexusInstance struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	BaseURL   string    `json:"base_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type IndexingSchedule struct {
	ID                   int64      `json:"id"`
	NexusInstanceID      int64      `json:"nexus_instance_id"`
	NexusName            string     `json:"nexus_name"`
	NexusBaseURL         string     `json:"nexus_base_url"`
	ScanType             string     `json:"scan_type"`
	Name                 string     `json:"name"`
	Enabled              bool       `json:"enabled"`
	ScheduleExpr         string     `json:"schedule_expr"`
	IntervalSeconds      int32      `json:"interval_seconds"`
	Timezone             string     `json:"timezone"`
	Scope                RunScope   `json:"scope"`
	LockConflictBehavior string     `json:"lock_conflict_behavior"`
	NextRunAt            *time.Time `json:"next_run_at,omitempty"`
	LastRunAt            *time.Time `json:"last_run_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type UpsertIndexingScheduleRequest struct {
	NexusName       string   `json:"nexus_name"`
	BaseURL         string   `json:"base_url"`
	Name            string   `json:"name"`
	ScanType        string   `json:"scan_type"`
	IntervalSeconds int32    `json:"interval_seconds"`
	Enabled         bool     `json:"enabled"`
	DueNow          bool     `json:"due_now"`
	Scope           RunScope `json:"scope"`
}

type RateLimitSettings struct {
	AssetListRPS        *float64 `json:"asset_list_rps,omitempty"`
	RegistryManifestRPS *float64 `json:"registry_manifest_rps,omitempty"`
	DefaultRPS          *float64 `json:"default_rps,omitempty"`
}

type AnalyticsListRequest struct {
	NexusInstanceID int64
	RepositoryName  string
	Search          string
	OlderThanDays   int
	NeverDownloaded bool
	Limit           int32
}

type AnalyticsOverview struct {
	RepositoryCount          int64      `json:"repository_count"`
	ImageCount               int64      `json:"image_count"`
	ActiveTagCount           int64      `json:"active_tag_count"`
	TagsMissingDigestCount   int64      `json:"tags_missing_digest_count"`
	TagsMissingManifestCount int64      `json:"tags_missing_manifest_count"`
	NexusAssetBytes          int64      `json:"nexus_asset_bytes"`
	DockerLogicalBytes       int64      `json:"docker_logical_bytes"`
	DockerUniqueLayerBytes   int64      `json:"docker_unique_layer_bytes"`
	LastSuccessfulRunAt      *time.Time `json:"last_successful_run_at,omitempty"`
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

type TrendTopMoversRequest struct {
	NexusInstanceID int64
	RepositoryID    int64
	ImageID         int64
	MetricName      string
	PatternName     string
	StartDate       string
	EndDate         string
	Limit           int32
}

type TrendPoint struct {
	SnapshotDate string     `json:"snapshot_date"`
	CapturedAt   *time.Time `json:"captured_at,omitempty"`
	MetricValue  *float64   `json:"metric_value,omitempty"`
	Missing      bool       `json:"missing"`
}

type TrendTopMover struct {
	RepositoryID   int64    `json:"repository_id"`
	RepositoryName string   `json:"repository_name"`
	ImageID        *int64   `json:"image_id,omitempty"`
	ImageName      string   `json:"image_name,omitempty"`
	StartValue     float64  `json:"start_value"`
	EndValue       float64  `json:"end_value"`
	DeltaValue     float64  `json:"delta_value"`
	DeltaPercent   *float64 `json:"delta_percent,omitempty"`
}

type CleanupSimulationRequest struct {
	NexusInstanceID int64  `json:"nexus_instance_id,omitempty"`
	RepositoryName  string `json:"repository_name,omitempty"`
	PolicyYAML      string `json:"policy_yaml"`
	Limit           int32  `json:"limit,omitempty"`
}

type CleanupSimulationResult struct {
	PolicyName                string                     `json:"policy_name"`
	EvaluatedTagCount         int64                      `json:"evaluated_tag_count"`
	CandidateTagCount         int64                      `json:"candidate_tag_count"`
	ProtectedTagCount         int64                      `json:"protected_tag_count"`
	WarningCount              int64                      `json:"warning_count"`
	EstimatedReclaimableBytes int64                      `json:"estimated_reclaimable_bytes"`
	Candidates                []CleanupSimulationTag     `json:"candidates"`
	Protected                 []CleanupSimulationTag     `json:"protected"`
	Warnings                  []CleanupSimulationWarning `json:"warnings"`
}

type CleanupSimulationTag struct {
	TagID                     int64      `json:"tag_id"`
	ImageID                   int64      `json:"image_id"`
	RepositoryID              int64      `json:"repository_id"`
	NexusInstanceID           int64      `json:"nexus_instance_id"`
	RepositoryName            string     `json:"repository_name"`
	ImageName                 string     `json:"image_name"`
	Tag                       string     `json:"tag"`
	PatternName               string     `json:"pattern_name"`
	RuleName                  string     `json:"rule_name,omitempty"`
	Reason                    string     `json:"reason,omitempty"`
	DockerLogicalBytes        int64      `json:"docker_logical_bytes"`
	EstimatedReclaimableBytes int64      `json:"estimated_reclaimable_bytes"`
	FirstSeenAt               time.Time  `json:"first_seen_at"`
	LastSeenAt                time.Time  `json:"last_seen_at"`
	LastDownloadedAt          *time.Time `json:"last_downloaded_at,omitempty"`
}

type CleanupSimulationWarning struct {
	Tag  CleanupSimulationTag `json:"tag"`
	Code string               `json:"code"`
}

type CleanupExecutionRequest struct {
	NexusInstanceID int64  `json:"nexus_instance_id"`
	RepositoryName  string `json:"repository_name,omitempty"`
	PolicyYAML      string `json:"policy_yaml"`
	ApprovalText    string `json:"approval_text"`
	Limit           int32  `json:"limit,omitempty"`
	RequestedBy     string `json:"-"`
}

type CleanupExecution struct {
	ID                          int64      `json:"id"`
	NexusInstanceID             int64      `json:"nexus_instance_id"`
	RepositoryName              string     `json:"repository_name,omitempty"`
	PolicyName                  string     `json:"policy_name"`
	Status                      string     `json:"status"`
	RequestedBy                 string     `json:"requested_by"`
	ApprovedBy                  string     `json:"approved_by,omitempty"`
	DestructiveEnabled          bool       `json:"destructive_enabled"`
	SimulationSummary           any        `json:"simulation_summary"`
	AuditLog                    any        `json:"audit_log"`
	PostDeleteVerificationRunID *int64     `json:"post_delete_verification_run_id,omitempty"`
	CreatedAt                   time.Time  `json:"created_at"`
	ApprovedAt                  *time.Time `json:"approved_at,omitempty"`
	FinishedAt                  *time.Time `json:"finished_at,omitempty"`
}

type RepositoryAnalytics struct {
	RepositoryID           int64      `json:"repository_id"`
	NexusInstanceID        int64      `json:"nexus_instance_id"`
	NexusName              string     `json:"nexus_name"`
	RepositoryName         string     `json:"repository_name"`
	ImageCount             int64      `json:"image_count"`
	ActiveTagCount         int64      `json:"active_tag_count"`
	NexusAssetBytes        int64      `json:"nexus_asset_bytes"`
	DockerLogicalBytes     int64      `json:"docker_logical_bytes"`
	DockerUniqueLayerBytes int64      `json:"docker_unique_layer_bytes"`
	LastSeenAt             *time.Time `json:"last_seen_at,omitempty"`
}

type ImageAnalytics struct {
	ImageID                int64      `json:"image_id"`
	RepositoryID           int64      `json:"repository_id"`
	NexusInstanceID        int64      `json:"nexus_instance_id"`
	NexusName              string     `json:"nexus_name"`
	RepositoryName         string     `json:"repository_name"`
	ImageName              string     `json:"image_name"`
	ActiveTagCount         int64      `json:"active_tag_count"`
	DockerLogicalBytes     int64      `json:"docker_logical_bytes"`
	DockerUniqueLayerBytes int64      `json:"docker_unique_layer_bytes"`
	OldestTagFirstSeenAt   *time.Time `json:"oldest_tag_first_seen_at,omitempty"`
	NewestTagLastSeenAt    *time.Time `json:"newest_tag_last_seen_at,omitempty"`
}

type TagAnalytics struct {
	TagID                     int64      `json:"tag_id"`
	ImageID                   int64      `json:"image_id"`
	RepositoryID              int64      `json:"repository_id"`
	NexusInstanceID           int64      `json:"nexus_instance_id"`
	NexusName                 string     `json:"nexus_name"`
	RepositoryName            string     `json:"repository_name"`
	ImageName                 string     `json:"image_name"`
	Tag                       string     `json:"tag"`
	Digest                    string     `json:"digest"`
	MediaType                 string     `json:"media_type"`
	NexusTagSizeBytes         int64      `json:"nexus_tag_size_bytes"`
	DockerLogicalBytes        int64      `json:"docker_logical_bytes"`
	DockerLayerBytes          int64      `json:"docker_layer_bytes"`
	EstimatedReclaimableBytes int64      `json:"estimated_reclaimable_bytes"`
	SharedLayerBytes          int64      `json:"shared_layer_bytes"`
	FirstSeenAt               time.Time  `json:"first_seen_at"`
	LastSeenAt                time.Time  `json:"last_seen_at"`
	LastDownloadedAt          *time.Time `json:"last_downloaded_at,omitempty"`
}

type PackageFormatRepository struct {
	RepositoryID    int64      `json:"repository_id"`
	NexusInstanceID int64      `json:"nexus_instance_id"`
	NexusName       string     `json:"nexus_name"`
	RepositoryName  string     `json:"repository_name"`
	Format          string     `json:"format"`
	Type            string     `json:"type"`
	SupportStatus   string     `json:"support_status"`
	SupportReason   string     `json:"support_reason"`
	AssetCount      int64      `json:"asset_count"`
	AssetBytes      int64      `json:"asset_bytes"`
	LastSeenAt      *time.Time `json:"last_seen_at,omitempty"`
	LastAssetSeenAt *time.Time `json:"last_asset_seen_at,omitempty"`
}

type TagPattern struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Description string    `json:"description"`
	TagRegex    string    `json:"tag_regex"`
	Priority    int32     `json:"priority"`
	Enabled     bool      `json:"enabled"`
	BuiltIn     bool      `json:"built_in"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type UpsertTagPatternRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	TagRegex    string `json:"tag_regex"`
	Priority    int32  `json:"priority"`
	Enabled     bool   `json:"enabled"`
}

type TagReclassificationRequest struct {
	NexusInstanceID int64  `json:"nexus_instance_id"`
	RepositoryName  string `json:"repository_name,omitempty"`
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

type TagPatternAnalytics struct {
	PatternName          string     `json:"pattern_name"`
	ActiveTagCount       int64      `json:"active_tag_count"`
	DockerLogicalBytes   int64      `json:"docker_logical_bytes"`
	OldestTagFirstSeenAt *time.Time `json:"oldest_tag_first_seen_at,omitempty"`
	NeverDownloadedCount int64      `json:"never_downloaded_count"`
}

type TagPatternGrowth struct {
	RepositoryName       string     `json:"repository_name"`
	ImageNamespace       string     `json:"image_namespace"`
	PatternName          string     `json:"pattern_name"`
	ActiveTagCount       int64      `json:"active_tag_count"`
	DockerLogicalBytes   int64      `json:"docker_logical_bytes"`
	GrowthTagCount       int64      `json:"growth_tag_count"`
	GrowthLogicalBytes   int64      `json:"growth_logical_bytes"`
	NeverDownloadedCount int64      `json:"never_downloaded_count"`
	OldestTagFirstSeenAt *time.Time `json:"oldest_tag_first_seen_at,omitempty"`
}

type TagPatternExample struct {
	PatternName    string    `json:"pattern_name"`
	RepositoryName string    `json:"repository_name"`
	ImageName      string    `json:"image_name"`
	Tag            string    `json:"tag"`
	FirstSeenAt    time.Time `json:"first_seen_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
}

type TagPatternSuggestion struct {
	TagShape       string   `json:"tag_shape"`
	TagCount       int64    `json:"tag_count"`
	ExampleTags    []string `json:"example_tags"`
	SuggestedRegex string   `json:"suggested_regex"`
}

type PauseIndexingRequest struct {
	Reason string `json:"reason"`
}

type IndexingControl struct {
	NexusInstanceID     int64      `json:"nexus_instance_id"`
	Paused              bool       `json:"paused"`
	PausedReason        string     `json:"paused_reason,omitempty"`
	PausedAt            *time.Time `json:"paused_at,omitempty"`
	PausedBy            string     `json:"paused_by,omitempty"`
	CircuitFailureCount int32      `json:"circuit_failure_count"`
	CircuitOpenUntil    *time.Time `json:"circuit_open_until,omitempty"`
	LastFailureCode     string     `json:"last_failure_code,omitempty"`
	LastFailureClass    string     `json:"last_failure_class,omitempty"`
	AssetListRPS        *float64   `json:"asset_list_rps,omitempty"`
	RegistryManifestRPS *float64   `json:"registry_manifest_rps,omitempty"`
	DefaultRPS          *float64   `json:"default_rps,omitempty"`
}

func WithReadyCheck(fn func(context.Context) error) Option {
	return func(s *Server) {
		s.readyCheck = fn
	}
}

func WithAuthenticator(auth AuthenticateFunc) Option {
	return func(s *Server) {
		s.auth = auth
	}
}

func WithSessionManager(session SessionManager) Option {
	return func(s *Server) {
		s.session = session
	}
}

func WithIndexingService(indexing IndexingService) Option {
	return func(s *Server) {
		s.indexing = indexing
	}
}

func WithSecureSessionCookie(secure bool) Option {
	return func(s *Server) {
		s.cookieSecure = secure
	}
}

func WithCORSOrigin(origin string) Option {
	return func(s *Server) {
		s.corsOrigin = origin
	}
}

func NewServer(logger *slog.Logger, opts ...Option) http.Handler {
	s := &Server{
		logger: logger,
		mux:    http.NewServeMux(),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.routes()
	return s
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

var requestCounter uint64

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	requestID := requestIDFrom(r)
	w.Header().Set("X-Request-ID", requestID)
	if s.corsOrigin != "" {
		w.Header().Set("Access-Control-Allow-Origin", s.corsOrigin)
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Request-ID")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logger.Error("panic recovered", "request_id", requestID, "panic", recovered, "stack", string(debug.Stack()))
			writeProblem(rec, http.StatusInternalServerError, "internal_error", "unexpected server error", requestID, false)
		}
		s.logger.Info("request",
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	}()

	s.mux.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, requestID)))
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/v1/health", s.health)
	s.mux.HandleFunc("GET /api/v1/ready", s.ready)
	s.mux.HandleFunc("GET /api/v1/me", s.me)
	s.mux.HandleFunc("POST /api/v1/session", s.login)
	s.mux.HandleFunc("DELETE /api/v1/session", s.logout)
	s.mux.HandleFunc("GET /api/v1/admin/status", s.adminStatus)
	s.mux.HandleFunc("GET /api/v1/index/runs", s.listIndexRuns)
	s.mux.HandleFunc("POST /api/v1/index/runs", s.startIndexRun)
	s.mux.HandleFunc("POST /api/v1/index/runs/{scan_run_id}/cancel", s.cancelIndexRun)
	s.mux.HandleFunc("GET /api/v1/index/tasks", s.listIndexTasks)
	s.mux.HandleFunc("POST /api/v1/index/tasks/{task_id}/retry", s.retryIndexTask)
	s.mux.HandleFunc("GET /api/v1/index/verification/findings", s.listVerificationFindings)
	s.mux.HandleFunc("POST /api/v1/index/verification/findings/{finding_id}/dismiss", s.dismissVerificationFinding)
	s.mux.HandleFunc("GET /api/v1/admin/nexus-instances", s.listNexusInstances)
	s.mux.HandleFunc("GET /api/v1/admin/indexing-schedules", s.listIndexingSchedules)
	s.mux.HandleFunc("POST /api/v1/admin/indexing-schedules", s.upsertIndexingSchedule)
	s.mux.HandleFunc("PUT /api/v1/admin/nexus/{nexus_instance_id}/rate-limits", s.updateNexusRateLimits)
	s.mux.HandleFunc("GET /api/v1/analytics/overview", s.getAnalyticsOverview)
	s.mux.HandleFunc("GET /api/v1/analytics/repositories", s.listRepositoryAnalytics)
	s.mux.HandleFunc("GET /api/v1/analytics/images", s.listImageAnalytics)
	s.mux.HandleFunc("GET /api/v1/analytics/tags", s.listTagAnalytics)
	s.mux.HandleFunc("GET /api/v1/packages/repositories", s.listPackageFormatRepositories)
	s.mux.HandleFunc("GET /api/v1/analytics/trends", s.listTrendSeries)
	s.mux.HandleFunc("GET /api/v1/analytics/trends/top-movers", s.listTrendTopMovers)
	s.mux.HandleFunc("POST /api/v1/cleanup/simulations", s.simulateCleanupPolicy)
	s.mux.HandleFunc("POST /api/v1/cleanup/executions", s.requestCleanupExecution)
	s.mux.HandleFunc("GET /api/v1/analytics/tag-patterns", s.listTagPatternAnalytics)
	s.mux.HandleFunc("GET /api/v1/analytics/tag-patterns/growth", s.listTagPatternGrowth)
	s.mux.HandleFunc("GET /api/v1/analytics/tag-patterns/examples", s.listTagPatternExamples)
	s.mux.HandleFunc("GET /api/v1/analytics/tag-patterns/suggestions", s.listTagPatternSuggestions)
	s.mux.HandleFunc("GET /api/v1/admin/tag-patterns", s.listTagPatterns)
	s.mux.HandleFunc("POST /api/v1/admin/tag-patterns", s.upsertTagPattern)
	s.mux.HandleFunc("POST /api/v1/admin/tag-patterns/reclassify", s.startTagReclassification)
	s.mux.HandleFunc("POST /api/v1/index/nexus/{nexus_instance_id}/pause", s.pauseNexusIndexing)
	s.mux.HandleFunc("POST /api/v1/index/nexus/{nexus_instance_id}/resume", s.resumeNexusIndexing)
	s.mux.HandleFunc("GET /metrics", s.metrics)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if s.readyCheck != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.readyCheck(ctx); err != nil {
			s.logger.Warn("readiness check failed", "request_id", requestIDFromContext(r.Context()), "error", err)
			writeProblem(w, http.StatusServiceUnavailable, "not_ready", "service is not ready", requestIDFromContext(r.Context()), true)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	user, err := s.authenticateRequest(r)
	if err != nil {
		writeUnauthorized(w, requestIDFromContext(r.Context()))
		return
	}

	writeJSON(w, http.StatusOK, user)
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.session == nil {
		writeProblem(w, http.StatusServiceUnavailable, "auth_not_configured", "authentication is not configured", requestIDFromContext(r.Context()), true)
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "invalid JSON request body", requestIDFromContext(r.Context()), false)
		return
	}

	session, err := s.session.CreateSession(r.Context(), req.Username, req.Password)
	if err != nil {
		writeUnauthorized(w, requestIDFromContext(r.Context()))
		return
	}

	http.SetCookie(w, s.sessionCookie(session.Token, session.ExpiresAt))
	writeJSON(w, http.StatusOK, loginResponse{
		Username:  session.User.Username,
		Role:      session.User.Role,
		ExpiresAt: session.ExpiresAt,
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if s.session != nil {
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			_ = s.session.RevokeSession(r.Context(), cookie.Value)
		}
	}
	http.SetCookie(w, s.expiredSessionCookie())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listIndexRuns(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuthenticated(w, r); !ok {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}

	runs, err := s.indexing.ListIndexRuns(r.Context(), 50)
	if err != nil {
		s.logger.Error("list index runs failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "index_runs_list_failed", "index runs could not be loaded", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) listIndexTasks(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuthenticated(w, r); !ok {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	scanRunID := queryInt64(r, "scan_run_id")
	limit := int32(queryInt64(r, "limit"))
	tasks, err := s.indexing.ListIndexTasks(r.Context(), scanRunID, limit)
	if err != nil {
		s.logger.Error("list index tasks failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "index_tasks_list_failed", "index tasks could not be loaded", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *Server) startIndexRun(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}

	var req StartIndexRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "invalid JSON request body", requestIDFromContext(r.Context()), false)
		return
	}
	run, err := s.indexing.StartIndexRun(r.Context(), req)
	if err != nil {
		var conflict operationLockConflictError
		if errors.As(err, &conflict) {
			blockingRunID, blockingScanType := conflict.OperationLockConflict()
			writeProblem(w, http.StatusConflict, "operation_lock_conflict", "blocked by active "+blockingScanType+" run "+strconv.FormatInt(blockingRunID, 10), requestIDFromContext(r.Context()), true)
			return
		}
		s.logger.Error("start index run failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "index_run_start_failed", "index run could not be started", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (s *Server) cancelIndexRun(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	scanRunID, ok := pathInt64(w, r, "scan_run_id")
	if !ok {
		return
	}
	run, err := s.indexing.CancelIndexRun(r.Context(), scanRunID)
	if err != nil {
		s.logger.Error("cancel index run failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "index_run_cancel_failed", "index run could not be canceled", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) retryIndexTask(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	taskID, ok := pathInt64(w, r, "task_id")
	if !ok {
		return
	}
	task, err := s.indexing.RetryIndexTask(r.Context(), taskID)
	if err != nil {
		s.logger.Error("retry index task failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "index_task_retry_failed", "index task could not be retried", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) listVerificationFindings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuthenticated(w, r); !ok {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	nexusInstanceID := queryInt64(r, "nexus_instance_id")
	status := r.URL.Query().Get("status")
	limit := int32(queryInt64(r, "limit"))
	findings, err := s.indexing.ListVerificationFindings(r.Context(), nexusInstanceID, status, limit)
	if err != nil {
		s.logger.Error("list verification findings failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "verification_findings_list_failed", "verification findings could not be loaded", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"findings": findings})
}

func (s *Server) dismissVerificationFinding(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdminUser(w, r)
	if !ok {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	findingID, ok := pathInt64(w, r, "finding_id")
	if !ok {
		return
	}
	finding, err := s.indexing.DismissVerificationFinding(r.Context(), findingID, user.Username)
	if err != nil {
		s.logger.Error("dismiss verification finding failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "verification_finding_dismiss_failed", "verification finding could not be dismissed", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, finding)
}

func (s *Server) listNexusInstances(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuthenticated(w, r); !ok {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	instances, err := s.indexing.ListNexusInstances(r.Context())
	if err != nil {
		s.logger.Error("list nexus instances failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "nexus_instances_list_failed", "Nexus instances could not be loaded", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": instances})
}

func (s *Server) listIndexingSchedules(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuthenticated(w, r); !ok {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	schedules, err := s.indexing.ListIndexingSchedules(r.Context(), queryInt64(r, "nexus_instance_id"))
	if err != nil {
		s.logger.Error("list indexing schedules failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "indexing_schedules_list_failed", "indexing schedules could not be loaded", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": schedules})
}

func (s *Server) upsertIndexingSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	var req UpsertIndexingScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "invalid JSON request body", requestIDFromContext(r.Context()), false)
		return
	}
	if req.IntervalSeconds <= 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_schedule", "interval_seconds must be greater than zero", requestIDFromContext(r.Context()), false)
		return
	}
	schedule, err := s.indexing.UpsertIndexingSchedule(r.Context(), req)
	if err != nil {
		s.logger.Error("upsert indexing schedule failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "indexing_schedule_upsert_failed", "indexing schedule could not be saved", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, schedule)
}

func (s *Server) updateNexusRateLimits(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	nexusInstanceID, ok := pathInt64(w, r, "nexus_instance_id")
	if !ok {
		return
	}
	var req RateLimitSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "invalid JSON request body", requestIDFromContext(r.Context()), false)
		return
	}
	control, err := s.indexing.UpdateNexusRateLimits(r.Context(), nexusInstanceID, req)
	if err != nil {
		s.logger.Error("update nexus rate limits failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "nexus_rate_limits_update_failed", "Nexus rate limits could not be saved", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, control)
}

func (s *Server) getAnalyticsOverview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuthenticated(w, r); !ok {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	overview, err := s.indexing.GetAnalyticsOverview(r.Context(), queryInt64(r, "nexus_instance_id"))
	if err != nil {
		s.logger.Error("get analytics overview failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "analytics_overview_failed", "analytics overview could not be loaded", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) listRepositoryAnalytics(w http.ResponseWriter, r *http.Request) {
	s.listAnalytics(w, r, func(ctx context.Context, req AnalyticsListRequest) (any, error) {
		rows, err := s.indexing.ListRepositoryAnalytics(ctx, req)
		return map[string]any{"repositories": rows}, err
	})
}

func (s *Server) listImageAnalytics(w http.ResponseWriter, r *http.Request) {
	s.listAnalytics(w, r, func(ctx context.Context, req AnalyticsListRequest) (any, error) {
		rows, err := s.indexing.ListImageAnalytics(ctx, req)
		return map[string]any{"images": rows}, err
	})
}

func (s *Server) listTagAnalytics(w http.ResponseWriter, r *http.Request) {
	s.listAnalytics(w, r, func(ctx context.Context, req AnalyticsListRequest) (any, error) {
		rows, err := s.indexing.ListTagAnalytics(ctx, req)
		return map[string]any{"tags": rows}, err
	})
}

func (s *Server) listPackageFormatRepositories(w http.ResponseWriter, r *http.Request) {
	s.listAnalytics(w, r, func(ctx context.Context, req AnalyticsListRequest) (any, error) {
		rows, err := s.indexing.ListPackageFormatRepositories(ctx, req)
		return map[string]any{"repositories": rows}, err
	})
}

func (s *Server) listTrendSeries(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuthenticated(w, r); !ok {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	req := TrendSeriesRequest{
		NexusInstanceID: queryInt64(r, "nexus_instance_id"),
		RepositoryID:    queryInt64(r, "repository_id"),
		ImageID:         queryInt64(r, "image_id"),
		MetricName:      r.URL.Query().Get("metric_name"),
		PatternName:     r.URL.Query().Get("pattern_name"),
		StartDate:       r.URL.Query().Get("start_date"),
		EndDate:         r.URL.Query().Get("end_date"),
	}
	rows, err := s.indexing.ListTrendSeries(r.Context(), req)
	if err != nil {
		s.logger.Error("list trend series failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "trend_series_list_failed", "trend series could not be loaded", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"points": rows})
}

func (s *Server) listTrendTopMovers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuthenticated(w, r); !ok {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	req := TrendTopMoversRequest{
		NexusInstanceID: queryInt64(r, "nexus_instance_id"),
		RepositoryID:    queryInt64(r, "repository_id"),
		ImageID:         queryInt64(r, "image_id"),
		MetricName:      r.URL.Query().Get("metric_name"),
		PatternName:     r.URL.Query().Get("pattern_name"),
		StartDate:       r.URL.Query().Get("start_date"),
		EndDate:         r.URL.Query().Get("end_date"),
		Limit:           int32(queryInt64(r, "limit")),
	}
	rows, err := s.indexing.ListTrendTopMovers(r.Context(), req)
	if err != nil {
		s.logger.Error("list trend top movers failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "trend_top_movers_list_failed", "trend top movers could not be loaded", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"movers": rows})
}

func (s *Server) simulateCleanupPolicy(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuthenticated(w, r); !ok {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	var req CleanupSimulationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "invalid JSON request body", requestIDFromContext(r.Context()), false)
		return
	}
	result, err := s.indexing.SimulateCleanupPolicy(r.Context(), req)
	if err != nil {
		var invalid invalidRequestError
		if errors.As(err, &invalid) && invalid.InvalidRequest() {
			writeProblem(w, http.StatusBadRequest, "cleanup_policy_invalid", err.Error(), requestIDFromContext(r.Context()), false)
			return
		}
		s.logger.Error("simulate cleanup policy failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "cleanup_simulation_failed", "cleanup simulation could not be run", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) requestCleanupExecution(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdminUser(w, r)
	if !ok {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	var req CleanupExecutionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "invalid JSON request body", requestIDFromContext(r.Context()), false)
		return
	}
	req.RequestedBy = user.Username
	execution, err := s.indexing.RequestCleanupExecution(r.Context(), req)
	if err != nil {
		var invalid invalidRequestError
		if errors.As(err, &invalid) && invalid.InvalidRequest() {
			writeProblem(w, http.StatusBadRequest, "cleanup_execution_invalid", err.Error(), requestIDFromContext(r.Context()), false)
			return
		}
		s.logger.Error("request cleanup execution failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "cleanup_execution_failed", "cleanup execution could not be requested", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusAccepted, execution)
}

func (s *Server) listTagPatternAnalytics(w http.ResponseWriter, r *http.Request) {
	s.listTagPatternsData(w, r, func(ctx context.Context, req TagPatternListRequest) (any, error) {
		rows, err := s.indexing.ListTagPatternAnalytics(ctx, req)
		return map[string]any{"patterns": rows}, err
	})
}

func (s *Server) listTagPatternGrowth(w http.ResponseWriter, r *http.Request) {
	s.listTagPatternsData(w, r, func(ctx context.Context, req TagPatternListRequest) (any, error) {
		rows, err := s.indexing.ListTagPatternGrowth(ctx, req)
		return map[string]any{"growth": rows}, err
	})
}

func (s *Server) listTagPatternExamples(w http.ResponseWriter, r *http.Request) {
	s.listTagPatternsData(w, r, func(ctx context.Context, req TagPatternListRequest) (any, error) {
		rows, err := s.indexing.ListTagPatternExamples(ctx, req)
		return map[string]any{"examples": rows}, err
	})
}

func (s *Server) listTagPatternSuggestions(w http.ResponseWriter, r *http.Request) {
	s.listTagPatternsData(w, r, func(ctx context.Context, req TagPatternListRequest) (any, error) {
		rows, err := s.indexing.ListTagPatternSuggestions(ctx, req)
		return map[string]any{"suggestions": rows}, err
	})
}

func (s *Server) listTagPatternsData(w http.ResponseWriter, r *http.Request, load func(context.Context, TagPatternListRequest) (any, error)) {
	if _, ok := s.requireAuthenticated(w, r); !ok {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	req := TagPatternListRequest{
		NexusInstanceID:  queryInt64(r, "nexus_instance_id"),
		RepositoryName:   r.URL.Query().Get("repository_name"),
		PatternName:      r.URL.Query().Get("pattern_name"),
		OlderThanDays:    int(queryInt64(r, "older_than_days")),
		GrowthWindowDays: int(queryInt64(r, "growth_window_days")),
		NeverDownloaded:  queryBool(r, "never_downloaded"),
		Limit:            int32(queryInt64(r, "limit")),
	}
	result, err := load(r.Context(), req)
	if err != nil {
		s.logger.Error("list tag pattern data failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "tag_pattern_data_list_failed", "tag pattern data could not be loaded", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listTagPatterns(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuthenticated(w, r); !ok {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	patterns, err := s.indexing.ListTagPatterns(r.Context())
	if err != nil {
		s.logger.Error("list tag patterns failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "tag_patterns_list_failed", "tag patterns could not be loaded", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"patterns": patterns})
}

func (s *Server) upsertTagPattern(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	var req UpsertTagPatternRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "invalid JSON request body", requestIDFromContext(r.Context()), false)
		return
	}
	if req.Name == "" || req.TagRegex == "" {
		writeProblem(w, http.StatusBadRequest, "invalid_tag_pattern", "name and tag_regex are required", requestIDFromContext(r.Context()), false)
		return
	}
	pattern, err := s.indexing.UpsertTagPattern(r.Context(), req)
	if err != nil {
		s.logger.Error("upsert tag pattern failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "tag_pattern_upsert_failed", "tag pattern could not be saved", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, pattern)
}

func (s *Server) startTagReclassification(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	var req TagReclassificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "invalid JSON request body", requestIDFromContext(r.Context()), false)
		return
	}
	if req.NexusInstanceID <= 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_reclassification", "nexus_instance_id is required", requestIDFromContext(r.Context()), false)
		return
	}
	run, err := s.indexing.StartTagReclassification(r.Context(), req)
	if err != nil {
		s.logger.Error("start tag reclassification failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "tag_reclassification_start_failed", "tag reclassification could not be started", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (s *Server) listAnalytics(w http.ResponseWriter, r *http.Request, load func(context.Context, AnalyticsListRequest) (any, error)) {
	if _, ok := s.requireAuthenticated(w, r); !ok {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	req := AnalyticsListRequest{
		NexusInstanceID: queryInt64(r, "nexus_instance_id"),
		RepositoryName:  r.URL.Query().Get("repository_name"),
		Search:          r.URL.Query().Get("search"),
		OlderThanDays:   int(queryInt64(r, "older_than_days")),
		NeverDownloaded: queryBool(r, "never_downloaded"),
		Limit:           int32(queryInt64(r, "limit")),
	}
	result, err := load(r.Context(), req)
	if err != nil {
		s.logger.Error("list analytics failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "analytics_list_failed", "analytics rows could not be loaded", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) pauseNexusIndexing(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdminUser(w, r)
	if !ok {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	nexusInstanceID, ok := pathInt64(w, r, "nexus_instance_id")
	if !ok {
		return
	}
	var req PauseIndexingRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_request", "invalid JSON request body", requestIDFromContext(r.Context()), false)
			return
		}
	}
	control, err := s.indexing.PauseNexusIndexing(r.Context(), nexusInstanceID, req.Reason, user.Username)
	if err != nil {
		s.logger.Error("pause nexus indexing failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "indexing_pause_failed", "indexing could not be paused", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, control)
}

func (s *Server) resumeNexusIndexing(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.indexing == nil {
		writeProblem(w, http.StatusServiceUnavailable, "indexing_not_configured", "indexing is not configured", requestIDFromContext(r.Context()), true)
		return
	}
	nexusInstanceID, ok := pathInt64(w, r, "nexus_instance_id")
	if !ok {
		return
	}
	control, err := s.indexing.ResumeNexusIndexing(r.Context(), nexusInstanceID)
	if err != nil {
		s.logger.Error("resume nexus indexing failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		writeProblem(w, http.StatusInternalServerError, "indexing_resume_failed", "indexing could not be resumed", requestIDFromContext(r.Context()), true)
		return
	}
	writeJSON(w, http.StatusOK, control)
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	if s.indexing == nil {
		_, _ = w.Write([]byte("# HELP nexus_lens_build_info Static build info for Nexus Lens.\n# TYPE nexus_lens_build_info gauge\nnexus_lens_build_info 1\n"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	body, err := s.indexing.PrometheusMetrics(ctx)
	if err != nil {
		s.logger.Error("metrics collection failed", "request_id", requestIDFromContext(r.Context()), "error", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("# metrics collection failed\n"))
		return
	}
	_, _ = w.Write([]byte(body))
}

const sessionCookieName = "nexus_lens_session"

func (s *Server) authenticateRequest(r *http.Request) (AuthenticatedUser, error) {
	if s.session != nil {
		if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
			return s.session.AuthenticateSession(r.Context(), cookie.Value)
		}
	}

	if s.auth == nil {
		return AuthenticatedUser{}, errAuthNotConfigured{}
	}
	username, password, ok := r.BasicAuth()
	if !ok {
		return AuthenticatedUser{}, errUnauthorized{}
	}
	return s.auth(r.Context(), username, password)
}

func (s *Server) requireAuthenticated(w http.ResponseWriter, r *http.Request) (AuthenticatedUser, bool) {
	user, err := s.authenticateRequest(r)
	if err != nil {
		writeUnauthorized(w, requestIDFromContext(r.Context()))
		return AuthenticatedUser{}, false
	}
	return user, true
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	user, ok := s.requireAuthenticated(w, r)
	if !ok {
		return false
	}
	if user.Role != "admin" {
		writeProblem(w, http.StatusForbidden, "forbidden", "admin role required", requestIDFromContext(r.Context()), false)
		return false
	}
	return true
}

func (s *Server) requireAdminUser(w http.ResponseWriter, r *http.Request) (AuthenticatedUser, bool) {
	user, ok := s.requireAuthenticated(w, r)
	if !ok {
		return AuthenticatedUser{}, false
	}
	if user.Role != "admin" {
		writeProblem(w, http.StatusForbidden, "forbidden", "admin role required", requestIDFromContext(r.Context()), false)
		return AuthenticatedUser{}, false
	}
	return user, true
}

func pathInt64(w http.ResponseWriter, r *http.Request, key string) (int64, bool) {
	value := r.PathValue(key)
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		writeProblem(w, http.StatusBadRequest, "invalid_path_parameter", "invalid "+key, requestIDFromContext(r.Context()), false)
		return 0, false
	}
	return parsed, true
}

func queryInt64(r *http.Request, key string) int64 {
	value := r.URL.Query().Get(key)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func queryBool(r *http.Request, key string) bool {
	value := r.URL.Query().Get(key)
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}

func (s *Server) sessionCookie(token string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

func (s *Server) expiredSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

type errUnauthorized struct{}

func (errUnauthorized) Error() string { return "unauthorized" }

type errAuthNotConfigured struct{}

func (errAuthNotConfigured) Error() string { return "auth not configured" }

type operationLockConflictError interface {
	error
	OperationLockConflict() (int64, string)
}

type invalidRequestError interface {
	error
	InvalidRequest() bool
}

func writeUnauthorized(w http.ResponseWriter, requestID string) {
	w.Header().Set("WWW-Authenticate", `Basic realm="nexus-lens"`)
	writeProblem(w, http.StatusUnauthorized, "unauthorized", "authentication required", requestID, false)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type problemResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Retryable bool   `json:"retryable"`
}

func writeProblem(w http.ResponseWriter, status int, code, message, requestID string, retryable bool) {
	writeJSON(w, status, problemResponse{
		Code:      code,
		Message:   message,
		RequestID: requestID,
		Retryable: retryable,
	})
}

type requestIDKey struct{}

func requestIDFrom(r *http.Request) string {
	if requestID := r.Header.Get("X-Request-ID"); requestID != "" {
		return requestID
	}
	return "req-" + strconv.FormatUint(atomic.AddUint64(&requestCounter, 1), 10)
}

func requestIDFromContext(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDKey{}).(string)
	return requestID
}
