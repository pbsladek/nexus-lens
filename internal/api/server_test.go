package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealth(t *testing.T) {
	server := NewServer(slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRequestIDHeader(t *testing.T) {
	server := NewServer(slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("X-Request-ID", "req-test")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != "req-test" {
		t.Fatalf("X-Request-ID = %q, want req-test", got)
	}
}

func TestReadyProblemResponse(t *testing.T) {
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithReadyCheck(func(context.Context) error { return errTestNotReady{} }),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ready", nil)
	req.Header.Set("X-Request-ID", "req-ready")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	var problem problemResponse
	if err := json.NewDecoder(rec.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "not_ready" || problem.RequestID != "req-ready" || !problem.Retryable {
		t.Fatalf("problem = %+v", problem)
	}
}

func TestMe(t *testing.T) {
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithAuthenticator(func(context.Context, string, string) (AuthenticatedUser, error) {
			return AuthenticatedUser{Username: "admin", Role: "admin"}, nil
		}),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.SetBasicAuth("admin", "admin")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var user AuthenticatedUser
	if err := json.NewDecoder(rec.Body).Decode(&user); err != nil {
		t.Fatal(err)
	}
	if user.Username != "admin" || user.Role != "admin" {
		t.Fatalf("user = %+v", user)
	}
}

func TestMeRequiresAuth(t *testing.T) {
	server := NewServer(slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestSessionLoginMeAndLogout(t *testing.T) {
	sessions := &fakeSessionManager{
		session: Session{
			Token:     "session-token",
			ExpiresAt: time.Now().Add(time.Hour),
			User:      AuthenticatedUser{Username: "admin", Role: "admin"},
		},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(sessions),
	)

	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/session", bytes.NewBufferString(`{"username":"admin","password":"admin"}`))
	loginRec := httptest.NewRecorder()
	server.ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d", loginRec.Code, http.StatusOK)
	}
	cookies := loginRec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName || cookies[0].Value != "session-token" {
		t.Fatalf("cookies = %+v", cookies)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	meReq.AddCookie(cookies[0])
	meRec := httptest.NewRecorder()
	server.ServeHTTP(meRec, meReq)

	if meRec.Code != http.StatusOK {
		t.Fatalf("me status = %d, want %d", meRec.Code, http.StatusOK)
	}
	var user AuthenticatedUser
	if err := json.NewDecoder(meRec.Body).Decode(&user); err != nil {
		t.Fatal(err)
	}
	if user.Username != "admin" || user.Role != "admin" {
		t.Fatalf("user = %+v", user)
	}

	logoutReq := httptest.NewRequest(http.MethodDelete, "/api/v1/session", nil)
	logoutReq.AddCookie(cookies[0])
	logoutRec := httptest.NewRecorder()
	server.ServeHTTP(logoutRec, logoutReq)

	if logoutRec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want %d", logoutRec.Code, http.StatusNoContent)
	}
	if sessions.revoked != "session-token" {
		t.Fatalf("revoked token = %q, want session-token", sessions.revoked)
	}
}

func TestAdminStatusRequiresAdminRole(t *testing.T) {
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "reader", Role: "read_only"},
			},
		}),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/status", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestListIndexRunsAllowsReadOnly(t *testing.T) {
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "reader", Role: "read_only"},
			},
		}),
		WithIndexingService(&fakeIndexingService{
			runs: []IndexRun{{ID: 42, NexusInstanceID: 7, ScanType: "recent_sync", Status: "running", StartedAt: time.Now()}},
		}),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/index/runs", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body struct {
		Runs []IndexRun `json:"runs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Runs) != 1 || body.Runs[0].ID != 42 {
		t.Fatalf("runs = %+v", body.Runs)
	}
}

func TestAnalyticsOverviewAllowsReadOnly(t *testing.T) {
	indexing := &fakeIndexingService{
		overview: AnalyticsOverview{
			RepositoryCount:        2,
			ImageCount:             5,
			ActiveTagCount:         11,
			NexusAssetBytes:        1024,
			DockerLogicalBytes:     2048,
			DockerUniqueLayerBytes: 512,
		},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "reader", Role: "read_only"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/overview?nexus_instance_id=7", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.analyticsNexusID != 7 {
		t.Fatalf("analytics nexus id = %d, want 7", indexing.analyticsNexusID)
	}
	var body AnalyticsOverview
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.RepositoryCount != 2 || body.DockerLogicalBytes != 2048 {
		t.Fatalf("overview = %+v", body)
	}
}

func TestListTagAnalyticsFilters(t *testing.T) {
	now := time.Now()
	indexing := &fakeIndexingService{
		tags: []TagAnalytics{{
			TagID:                     99,
			RepositoryName:            "docker-hosted-a",
			ImageName:                 "app",
			Tag:                       "feature-x",
			Digest:                    "sha256:abc",
			DockerLogicalBytes:        2048,
			EstimatedReclaimableBytes: 1024,
			FirstSeenAt:               now,
			LastSeenAt:                now,
		}},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "reader", Role: "read_only"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/tags?nexus_instance_id=7&repository_name=docker-hosted-a&search=feature&older_than_days=30&never_downloaded=true&limit=25", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.analyticsRequest.NexusInstanceID != 7 ||
		indexing.analyticsRequest.RepositoryName != "docker-hosted-a" ||
		indexing.analyticsRequest.Search != "feature" ||
		indexing.analyticsRequest.OlderThanDays != 30 ||
		!indexing.analyticsRequest.NeverDownloaded ||
		indexing.analyticsRequest.Limit != 25 {
		t.Fatalf("analytics request = %+v", indexing.analyticsRequest)
	}
	var body struct {
		Tags []TagAnalytics `json:"tags"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tags) != 1 || body.Tags[0].TagID != 99 {
		t.Fatalf("tags = %+v", body.Tags)
	}
}

func TestListPackageFormatRepositoriesAllowsReadOnly(t *testing.T) {
	now := time.Now()
	indexing := &fakeIndexingService{
		packageRepositories: []PackageFormatRepository{{
			RepositoryID:    12,
			NexusInstanceID: 7,
			NexusName:       "local",
			RepositoryName:  "pypi-hosted",
			Format:          "pypi",
			Type:            "hosted",
			SupportStatus:   "discover_only",
			SupportReason:   "hosted package repository discovered",
			LastSeenAt:      &now,
		}},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "reader", Role: "read_only"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/packages/repositories?nexus_instance_id=7&search=pypi&limit=25", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.packageRequest.NexusInstanceID != 7 ||
		indexing.packageRequest.Search != "pypi" ||
		indexing.packageRequest.Limit != 25 {
		t.Fatalf("package request = %+v", indexing.packageRequest)
	}
	var body struct {
		Repositories []PackageFormatRepository `json:"repositories"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Repositories) != 1 || body.Repositories[0].SupportStatus != "discover_only" {
		t.Fatalf("repositories = %+v", body.Repositories)
	}
}

func TestListTrendSeriesAllowsReadOnly(t *testing.T) {
	value := 42.0
	now := time.Now()
	indexing := &fakeIndexingService{
		trendPoints: []TrendPoint{{
			SnapshotDate: "2026-06-28",
			CapturedAt:   &now,
			MetricValue:  &value,
			Missing:      false,
		}},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "reader", Role: "read_only"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/trends?nexus_instance_id=7&repository_id=11&image_id=22&metric_name=tag_pattern_count&pattern_name=feature_branch&start_date=2026-06-01&end_date=2026-06-28", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.trendRequest.NexusInstanceID != 7 ||
		indexing.trendRequest.RepositoryID != 11 ||
		indexing.trendRequest.ImageID != 22 ||
		indexing.trendRequest.MetricName != "tag_pattern_count" ||
		indexing.trendRequest.PatternName != "feature_branch" ||
		indexing.trendRequest.StartDate != "2026-06-01" ||
		indexing.trendRequest.EndDate != "2026-06-28" {
		t.Fatalf("trend request = %+v", indexing.trendRequest)
	}
	var body struct {
		Points []TrendPoint `json:"points"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Points) != 1 || body.Points[0].SnapshotDate != "2026-06-28" {
		t.Fatalf("points = %+v", body.Points)
	}
}

func TestListTrendTopMoversAllowsReadOnly(t *testing.T) {
	percent := 50.0
	indexing := &fakeIndexingService{
		trendTopMovers: []TrendTopMover{{
			RepositoryID:   11,
			RepositoryName: "docker-hosted-a",
			StartValue:     100,
			EndValue:       150,
			DeltaValue:     50,
			DeltaPercent:   &percent,
		}},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "reader", Role: "read_only"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/trends/top-movers?nexus_instance_id=7&repository_id=11&image_id=22&metric_name=estimated_reclaimable_bytes&pattern_name=feature_branch&start_date=2026-06-01&end_date=2026-06-28&limit=10", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.trendTopMoversRequest.NexusInstanceID != 7 ||
		indexing.trendTopMoversRequest.RepositoryID != 11 ||
		indexing.trendTopMoversRequest.ImageID != 22 ||
		indexing.trendTopMoversRequest.MetricName != "estimated_reclaimable_bytes" ||
		indexing.trendTopMoversRequest.PatternName != "feature_branch" ||
		indexing.trendTopMoversRequest.StartDate != "2026-06-01" ||
		indexing.trendTopMoversRequest.EndDate != "2026-06-28" ||
		indexing.trendTopMoversRequest.Limit != 10 {
		t.Fatalf("trend top movers request = %+v", indexing.trendTopMoversRequest)
	}
	var body struct {
		Movers []TrendTopMover `json:"movers"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Movers) != 1 || body.Movers[0].RepositoryName != "docker-hosted-a" {
		t.Fatalf("movers = %+v", body.Movers)
	}
}

func TestSimulateCleanupPolicyAllowsReadOnly(t *testing.T) {
	indexing := &fakeIndexingService{
		cleanupSimulation: CleanupSimulationResult{
			PolicyName:                "ci-cleanup",
			EvaluatedTagCount:         2,
			CandidateTagCount:         1,
			ProtectedTagCount:         1,
			EstimatedReclaimableBytes: 1024,
			Candidates: []CleanupSimulationTag{{
				TagID:          101,
				RepositoryName: "docker-hosted-a",
				ImageName:      "demo/app",
				Tag:            "feature-123",
				PatternName:    "feature_branch",
				RuleName:       "old feature branches",
			}},
			Protected: []CleanupSimulationTag{{
				TagID:          102,
				RepositoryName: "docker-hosted-a",
				ImageName:      "demo/app",
				Tag:            "latest",
				PatternName:    "channel",
			}},
		},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "reader", Role: "read_only"},
			},
		}),
		WithIndexingService(indexing),
	)
	body := strings.NewReader(`{"nexus_instance_id":7,"repository_name":"docker-hosted-a","policy_yaml":"name: ci-cleanup\ncandidate_rules:\n  - name: old feature branches\n    patterns: [feature_branch]\n","limit":25}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cleanup/simulations", body)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.cleanupRequest.NexusInstanceID != 7 ||
		indexing.cleanupRequest.RepositoryName != "docker-hosted-a" ||
		indexing.cleanupRequest.Limit != 25 ||
		!strings.Contains(indexing.cleanupRequest.PolicyYAML, "feature_branch") {
		t.Fatalf("cleanup request = %+v", indexing.cleanupRequest)
	}
	var response CleanupSimulationResult
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.PolicyName != "ci-cleanup" || len(response.Candidates) != 1 || len(response.Protected) != 1 {
		t.Fatalf("cleanup response = %+v", response)
	}
}

func TestRequestCleanupExecutionRequiresAdminAndRecordsApproval(t *testing.T) {
	now := time.Now()
	indexing := &fakeIndexingService{
		cleanupExecution: CleanupExecution{
			ID:                 9,
			NexusInstanceID:    7,
			PolicyName:         "ci-cleanup",
			Status:             "blocked_read_only",
			RequestedBy:        "admin",
			DestructiveEnabled: false,
			SimulationSummary:  map[string]any{"candidate_tag_count": 1},
			AuditLog:           []any{map[string]any{"event": "approval_recorded"}},
			CreatedAt:          now,
		},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "admin", Role: "admin"},
			},
		}),
		WithIndexingService(indexing),
	)
	body := strings.NewReader(`{"nexus_instance_id":7,"policy_yaml":"name: ci-cleanup\ncandidate_rules:\n  - name: old feature branches\n    patterns: [feature_branch]\n","approval_text":"ci-cleanup","limit":25}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cleanup/executions", body)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if indexing.cleanupExecutionRequest.RequestedBy != "admin" ||
		indexing.cleanupExecutionRequest.NexusInstanceID != 7 ||
		indexing.cleanupExecutionRequest.ApprovalText != "ci-cleanup" {
		t.Fatalf("cleanup execution request = %+v", indexing.cleanupExecutionRequest)
	}
	var response CleanupExecution
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "blocked_read_only" || response.DestructiveEnabled {
		t.Fatalf("cleanup execution = %+v", response)
	}
}

func TestListTagPatternAnalyticsAllowsReadOnly(t *testing.T) {
	now := time.Now()
	indexing := &fakeIndexingService{
		patternAnalytics: []TagPatternAnalytics{{
			PatternName:          "semver",
			ActiveTagCount:       2,
			DockerLogicalBytes:   4096,
			OldestTagFirstSeenAt: &now,
			NeverDownloadedCount: 1,
		}},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "reader", Role: "read_only"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/tag-patterns?nexus_instance_id=7&repository_name=docker-hosted-a&older_than_days=30&never_downloaded=true&limit=25", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.patternRequest.NexusInstanceID != 7 ||
		indexing.patternRequest.RepositoryName != "docker-hosted-a" ||
		indexing.patternRequest.OlderThanDays != 30 ||
		!indexing.patternRequest.NeverDownloaded ||
		indexing.patternRequest.Limit != 25 {
		t.Fatalf("pattern request = %+v", indexing.patternRequest)
	}
	var body struct {
		Patterns []TagPatternAnalytics `json:"patterns"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Patterns) != 1 || body.Patterns[0].PatternName != "semver" {
		t.Fatalf("patterns = %+v", body.Patterns)
	}
}

func TestListTagPatternGrowthFilters(t *testing.T) {
	now := time.Now()
	indexing := &fakeIndexingService{
		patternGrowth: []TagPatternGrowth{{
			RepositoryName:       "docker-hosted-a",
			ImageNamespace:       "demo",
			PatternName:          "feature_branch",
			ActiveTagCount:       3,
			DockerLogicalBytes:   4096,
			GrowthTagCount:       2,
			GrowthLogicalBytes:   2048,
			NeverDownloadedCount: 1,
			OldestTagFirstSeenAt: &now,
		}},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "reader", Role: "read_only"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics/tag-patterns/growth?nexus_instance_id=7&repository_name=docker-hosted-a&pattern_name=feature_branch&growth_window_days=14&never_downloaded=true&limit=25", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.patternRequest.NexusInstanceID != 7 ||
		indexing.patternRequest.RepositoryName != "docker-hosted-a" ||
		indexing.patternRequest.PatternName != "feature_branch" ||
		indexing.patternRequest.GrowthWindowDays != 14 ||
		!indexing.patternRequest.NeverDownloaded ||
		indexing.patternRequest.Limit != 25 {
		t.Fatalf("pattern request = %+v", indexing.patternRequest)
	}
	var body struct {
		Growth []TagPatternGrowth `json:"growth"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Growth) != 1 || body.Growth[0].ImageNamespace != "demo" {
		t.Fatalf("growth = %+v", body.Growth)
	}
}

func TestStartTagReclassificationRequiresAdmin(t *testing.T) {
	indexing := &fakeIndexingService{
		reclassificationRun: IndexRun{ID: 88, NexusInstanceID: 7, ScanType: "tag_reclassification", Status: "running", StartedAt: time.Now()},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "admin", Role: "admin"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tag-patterns/reclassify", bytes.NewBufferString(`{"nexus_instance_id":7,"repository_name":"docker-hosted-a"}`))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if indexing.reclassificationRequest.NexusInstanceID != 7 || indexing.reclassificationRequest.RepositoryName != "docker-hosted-a" {
		t.Fatalf("reclassification request = %+v", indexing.reclassificationRequest)
	}
}

func TestStartIndexRunRequiresAdmin(t *testing.T) {
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "reader", Role: "read_only"},
			},
		}),
		WithIndexingService(&fakeIndexingService{}),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/index/runs", bytes.NewBufferString(`{"scan_type":"recent_sync"}`))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestStartIndexRun(t *testing.T) {
	indexing := &fakeIndexingService{
		started: IndexRun{ID: 42, NexusInstanceID: 7, ScanType: "recent_sync", Status: "running", StartedAt: time.Now()},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "admin", Role: "admin"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/index/runs", bytes.NewBufferString(`{"nexus_name":"local","base_url":"http://nexus:8081","scan_type":"bounded_reindex","scope":{"repositories":["docker-hosted-a"],"window_days":30}}`))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if indexing.request.NexusName != "local" || indexing.request.ScanType != "bounded_reindex" {
		t.Fatalf("request = %+v", indexing.request)
	}
	if indexing.request.Scope.WindowDays != 30 || len(indexing.request.Scope.Repositories) != 1 || indexing.request.Scope.Repositories[0] != "docker-hosted-a" {
		t.Fatalf("request = %+v", indexing.request)
	}
}

func TestStartIndexRunOperationLockConflict(t *testing.T) {
	indexing := &fakeIndexingService{
		err: fakeOperationLockConflict{
			runID:    42,
			scanType: "full_reconcile",
		},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "admin", Role: "admin"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/index/runs", bytes.NewBufferString(`{"nexus_name":"local","base_url":"http://nexus:8081","scan_type":"recent_sync"}`))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	var problem problemResponse
	if err := json.NewDecoder(rec.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "operation_lock_conflict" {
		t.Fatalf("problem = %+v", problem)
	}
}

func TestPauseNexusIndexing(t *testing.T) {
	indexing := &fakeIndexingService{
		control: IndexingControl{NexusInstanceID: 7, Paused: true, PausedReason: "maintenance"},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "admin", Role: "admin"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/index/nexus/7/pause", bytes.NewBufferString(`{"reason":"maintenance"}`))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.pausedReason != "maintenance" || indexing.pausedBy != "admin" {
		t.Fatalf("pause request = reason %q by %q", indexing.pausedReason, indexing.pausedBy)
	}
}

func TestListIndexTasks(t *testing.T) {
	now := time.Now()
	indexing := &fakeIndexingService{
		tasks: []IndexTask{{ID: 99, ScanRunID: 42, TaskType: "repository_discovery", Status: "failed", AvailableAt: now}},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "reader", Role: "read_only"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/index/tasks?scan_run_id=42&limit=25", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.taskScanRunID != 42 || indexing.taskLimit != 25 {
		t.Fatalf("task query = scan run %d limit %d", indexing.taskScanRunID, indexing.taskLimit)
	}
	var body struct {
		Tasks []IndexTask `json:"tasks"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tasks) != 1 || body.Tasks[0].ID != 99 {
		t.Fatalf("tasks = %+v", body.Tasks)
	}
}

func TestRetryIndexTaskRequiresAdmin(t *testing.T) {
	indexing := &fakeIndexingService{
		retriedTask: IndexTask{ID: 99, Status: "retryable", AvailableAt: time.Now()},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "admin", Role: "admin"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/index/tasks/99/retry", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.retriedTaskID != 99 {
		t.Fatalf("retried task id = %d", indexing.retriedTaskID)
	}
}

func TestCancelIndexRunRequiresAdmin(t *testing.T) {
	indexing := &fakeIndexingService{
		canceledRun: IndexRun{ID: 42, Status: "canceled", StartedAt: time.Now()},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "admin", Role: "admin"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/index/runs/42/cancel", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.canceledRunID != 42 {
		t.Fatalf("canceled run id = %d", indexing.canceledRunID)
	}
}

func TestListVerificationFindingsAllowsReadOnly(t *testing.T) {
	now := time.Now()
	indexing := &fakeIndexingService{
		findings: []VerificationFinding{{ID: 17, Severity: "medium", FindingType: "docker_tag_manifest_missing", Status: "open", CreatedAt: now, LastSeenAt: now}},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "reader", Role: "read_only"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/index/verification/findings?nexus_instance_id=7&status=open&limit=25", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.findingNexusID != 7 || indexing.findingStatus != "open" || indexing.findingLimit != 25 {
		t.Fatalf("finding query = nexus %d status %q limit %d", indexing.findingNexusID, indexing.findingStatus, indexing.findingLimit)
	}
	var body struct {
		Findings []VerificationFinding `json:"findings"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Findings) != 1 || body.Findings[0].ID != 17 {
		t.Fatalf("findings = %+v", body.Findings)
	}
}

func TestDismissVerificationFindingRequiresAdmin(t *testing.T) {
	now := time.Now()
	indexing := &fakeIndexingService{
		dismissedFinding: VerificationFinding{ID: 17, Severity: "medium", FindingType: "docker_tag_manifest_missing", Status: "dismissed", CreatedAt: now, LastSeenAt: now},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "admin", Role: "admin"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/index/verification/findings/17/dismiss", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.dismissedFindingID != 17 || indexing.dismissedBy != "admin" {
		t.Fatalf("dismissed finding = id %d by %q", indexing.dismissedFindingID, indexing.dismissedBy)
	}
}

func TestMetricsUsesIndexingService(t *testing.T) {
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithIndexingService(&fakeIndexingService{metrics: "nexus_lens_build_info 1\n"}),
	)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); body != "nexus_lens_build_info 1\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestListIndexingSchedulesAllowsReadOnly(t *testing.T) {
	now := time.Now()
	indexing := &fakeIndexingService{
		schedules: []IndexingSchedule{{
			ID:              3,
			NexusInstanceID: 7,
			NexusName:       "local",
			ScanType:        "recent_sync",
			Name:            "recent-sync",
			Enabled:         true,
			IntervalSeconds: 1800,
			CreatedAt:       now,
			UpdatedAt:       now,
		}},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "reader", Role: "read_only"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/indexing-schedules?nexus_instance_id=7", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.scheduleNexusID != 7 {
		t.Fatalf("schedule nexus id = %d, want 7", indexing.scheduleNexusID)
	}
}

func TestUpdateNexusRateLimitsRequiresAdmin(t *testing.T) {
	assetRPS := 3.5
	indexing := &fakeIndexingService{
		control: IndexingControl{NexusInstanceID: 7, AssetListRPS: &assetRPS},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "admin", Role: "admin"},
			},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/nexus/7/rate-limits", bytes.NewBufferString(`{"asset_list_rps":3.5}`))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.rateLimitNexusID != 7 || indexing.rateLimitRequest.AssetListRPS == nil || *indexing.rateLimitRequest.AssetListRPS != 3.5 {
		t.Fatalf("rate limit request = id %d req %+v", indexing.rateLimitNexusID, indexing.rateLimitRequest)
	}
}

type errTestNotReady struct{}

func (errTestNotReady) Error() string { return "not ready" }

type fakeSessionManager struct {
	session Session
	revoked string
}

func (f *fakeSessionManager) CreateSession(context.Context, string, string) (Session, error) {
	if f.session.Token == "" {
		return Session{}, errors.New("invalid credentials")
	}
	return f.session, nil
}

func (f *fakeSessionManager) AuthenticateSession(_ context.Context, token string) (AuthenticatedUser, error) {
	if token != f.session.Token {
		return AuthenticatedUser{}, errors.New("invalid session")
	}
	return f.session.User, nil
}

func (f *fakeSessionManager) RevokeSession(_ context.Context, token string) error {
	f.revoked = token
	return nil
}

type fakeIndexingService struct {
	started                 IndexRun
	runs                    []IndexRun
	tasks                   []IndexTask
	findings                []VerificationFinding
	instances               []NexusInstance
	schedules               []IndexingSchedule
	overview                AnalyticsOverview
	repositories            []RepositoryAnalytics
	images                  []ImageAnalytics
	tags                    []TagAnalytics
	packageRepositories     []PackageFormatRepository
	trendPoints             []TrendPoint
	trendTopMovers          []TrendTopMover
	cleanupSimulation       CleanupSimulationResult
	cleanupExecution        CleanupExecution
	patterns                []TagPattern
	patternAnalytics        []TagPatternAnalytics
	patternGrowth           []TagPatternGrowth
	patternExamples         []TagPatternExample
	patternSuggestions      []TagPatternSuggestion
	retriedTask             IndexTask
	canceledRun             IndexRun
	dismissedFinding        VerificationFinding
	savedSchedule           IndexingSchedule
	savedPattern            TagPattern
	reclassificationRun     IndexRun
	control                 IndexingControl
	metrics                 string
	request                 StartIndexRunRequest
	taskScanRunID           int64
	taskLimit               int32
	findingNexusID          int64
	findingStatus           string
	findingLimit            int32
	scheduleNexusID         int64
	scheduleRequest         UpsertIndexingScheduleRequest
	rateLimitNexusID        int64
	rateLimitRequest        RateLimitSettings
	analyticsNexusID        int64
	analyticsRequest        AnalyticsListRequest
	packageRequest          AnalyticsListRequest
	trendRequest            TrendSeriesRequest
	trendTopMoversRequest   TrendTopMoversRequest
	cleanupRequest          CleanupSimulationRequest
	cleanupExecutionRequest CleanupExecutionRequest
	patternRequest          TagPatternListRequest
	patternSaveRequest      UpsertTagPatternRequest
	reclassificationRequest TagReclassificationRequest
	retriedTaskID           int64
	canceledRunID           int64
	dismissedFindingID      int64
	dismissedBy             string
	pausedReason            string
	pausedBy                string
	err                     error
}

func (f *fakeIndexingService) StartIndexRun(_ context.Context, req StartIndexRunRequest) (IndexRun, error) {
	f.request = req
	if f.err != nil {
		return IndexRun{}, f.err
	}
	return f.started, nil
}

func (f *fakeIndexingService) ListIndexRuns(context.Context, int32) ([]IndexRun, error) {
	return f.runs, nil
}

func (f *fakeIndexingService) ListIndexTasks(_ context.Context, scanRunID int64, limit int32) ([]IndexTask, error) {
	f.taskScanRunID = scanRunID
	f.taskLimit = limit
	return f.tasks, nil
}

func (f *fakeIndexingService) RetryIndexTask(_ context.Context, taskID int64) (IndexTask, error) {
	f.retriedTaskID = taskID
	return f.retriedTask, nil
}

func (f *fakeIndexingService) CancelIndexRun(_ context.Context, scanRunID int64) (IndexRun, error) {
	f.canceledRunID = scanRunID
	return f.canceledRun, nil
}

func (f *fakeIndexingService) ListVerificationFindings(_ context.Context, nexusInstanceID int64, status string, limit int32) ([]VerificationFinding, error) {
	f.findingNexusID = nexusInstanceID
	f.findingStatus = status
	f.findingLimit = limit
	return f.findings, nil
}

func (f *fakeIndexingService) DismissVerificationFinding(_ context.Context, findingID int64, dismissedBy string) (VerificationFinding, error) {
	f.dismissedFindingID = findingID
	f.dismissedBy = dismissedBy
	return f.dismissedFinding, nil
}

func (f *fakeIndexingService) PrometheusMetrics(context.Context) (string, error) {
	return f.metrics, nil
}

func (f *fakeIndexingService) ListNexusInstances(context.Context) ([]NexusInstance, error) {
	return f.instances, nil
}

func (f *fakeIndexingService) ListIndexingSchedules(_ context.Context, nexusInstanceID int64) ([]IndexingSchedule, error) {
	f.scheduleNexusID = nexusInstanceID
	return f.schedules, nil
}

func (f *fakeIndexingService) UpsertIndexingSchedule(_ context.Context, req UpsertIndexingScheduleRequest) (IndexingSchedule, error) {
	f.scheduleRequest = req
	return f.savedSchedule, nil
}

func (f *fakeIndexingService) UpdateNexusRateLimits(_ context.Context, nexusInstanceID int64, req RateLimitSettings) (IndexingControl, error) {
	f.rateLimitNexusID = nexusInstanceID
	f.rateLimitRequest = req
	return f.control, nil
}

func (f *fakeIndexingService) GetAnalyticsOverview(_ context.Context, nexusInstanceID int64) (AnalyticsOverview, error) {
	f.analyticsNexusID = nexusInstanceID
	return f.overview, nil
}

func (f *fakeIndexingService) ListRepositoryAnalytics(_ context.Context, req AnalyticsListRequest) ([]RepositoryAnalytics, error) {
	f.analyticsRequest = req
	return f.repositories, nil
}

func (f *fakeIndexingService) ListImageAnalytics(_ context.Context, req AnalyticsListRequest) ([]ImageAnalytics, error) {
	f.analyticsRequest = req
	return f.images, nil
}

func (f *fakeIndexingService) ListTagAnalytics(_ context.Context, req AnalyticsListRequest) ([]TagAnalytics, error) {
	f.analyticsRequest = req
	return f.tags, nil
}

func (f *fakeIndexingService) ListPackageFormatRepositories(_ context.Context, req AnalyticsListRequest) ([]PackageFormatRepository, error) {
	f.packageRequest = req
	return f.packageRepositories, nil
}

func (f *fakeIndexingService) ListTrendSeries(_ context.Context, req TrendSeriesRequest) ([]TrendPoint, error) {
	f.trendRequest = req
	return f.trendPoints, nil
}

func (f *fakeIndexingService) ListTrendTopMovers(_ context.Context, req TrendTopMoversRequest) ([]TrendTopMover, error) {
	f.trendTopMoversRequest = req
	return f.trendTopMovers, nil
}

func (f *fakeIndexingService) SimulateCleanupPolicy(_ context.Context, req CleanupSimulationRequest) (CleanupSimulationResult, error) {
	f.cleanupRequest = req
	if f.err != nil {
		return CleanupSimulationResult{}, f.err
	}
	return f.cleanupSimulation, nil
}

func (f *fakeIndexingService) RequestCleanupExecution(_ context.Context, req CleanupExecutionRequest) (CleanupExecution, error) {
	f.cleanupExecutionRequest = req
	if f.err != nil {
		return CleanupExecution{}, f.err
	}
	return f.cleanupExecution, nil
}

func (f *fakeIndexingService) ListTagPatterns(context.Context) ([]TagPattern, error) {
	return f.patterns, nil
}

func (f *fakeIndexingService) UpsertTagPattern(_ context.Context, req UpsertTagPatternRequest) (TagPattern, error) {
	f.patternSaveRequest = req
	return f.savedPattern, nil
}

func (f *fakeIndexingService) StartTagReclassification(_ context.Context, req TagReclassificationRequest) (IndexRun, error) {
	f.reclassificationRequest = req
	return f.reclassificationRun, nil
}

func (f *fakeIndexingService) ListTagPatternAnalytics(_ context.Context, req TagPatternListRequest) ([]TagPatternAnalytics, error) {
	f.patternRequest = req
	return f.patternAnalytics, nil
}

func (f *fakeIndexingService) ListTagPatternGrowth(_ context.Context, req TagPatternListRequest) ([]TagPatternGrowth, error) {
	f.patternRequest = req
	return f.patternGrowth, nil
}

func (f *fakeIndexingService) ListTagPatternExamples(_ context.Context, req TagPatternListRequest) ([]TagPatternExample, error) {
	f.patternRequest = req
	return f.patternExamples, nil
}

func (f *fakeIndexingService) ListTagPatternSuggestions(_ context.Context, req TagPatternListRequest) ([]TagPatternSuggestion, error) {
	f.patternRequest = req
	return f.patternSuggestions, nil
}

func (f *fakeIndexingService) PauseNexusIndexing(_ context.Context, _ int64, reason, pausedBy string) (IndexingControl, error) {
	f.pausedReason = reason
	f.pausedBy = pausedBy
	return f.control, nil
}

func (f *fakeIndexingService) ResumeNexusIndexing(context.Context, int64) (IndexingControl, error) {
	return f.control, nil
}

type fakeOperationLockConflict struct {
	runID    int64
	scanType string
}

func (f fakeOperationLockConflict) Error() string {
	return "operation lock conflict"
}

func (f fakeOperationLockConflict) OperationLockConflict() (int64, string) {
	return f.runID, f.scanType
}
