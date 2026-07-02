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
	if !containsString(user.Capabilities, CapabilityExecuteCleanup) || !containsString(user.Capabilities, CapabilityManageSettings) {
		t.Fatalf("capabilities = %+v", user.Capabilities)
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
	if !containsString(user.Capabilities, CapabilityManageIndex) {
		t.Fatalf("capabilities = %+v", user.Capabilities)
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

func TestGetRetentionSettingsAllowsReadOnly(t *testing.T) {
	indexing := &fakeIndexingService{
		retentionSettings: RetentionSettings{
			IntervalSeconds:         3600,
			ScanRunDays:             30,
			CleanupExecutionDays:    365,
			VerificationFindingDays: 180,
			TrendSnapshotDays:       365,
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/retention-settings", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var settings RetentionSettings
	if err := json.NewDecoder(rec.Body).Decode(&settings); err != nil {
		t.Fatal(err)
	}
	if settings.ScanRunDays != 30 || settings.CleanupExecutionDays != 365 {
		t.Fatalf("settings = %+v", settings)
	}
}

func TestUpdateRetentionSettingsRequiresManageSettings(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/retention-settings", bytes.NewBufferString(`{"interval_seconds":3600,"scan_run_days":30,"cleanup_execution_days":365,"verification_finding_days":180,"trend_snapshot_days":365}`))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestUpdateRetentionSettings(t *testing.T) {
	indexing := &fakeIndexingService{}
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
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/retention-settings", bytes.NewBufferString(`{"interval_seconds":7200,"scan_run_days":45,"cleanup_execution_days":400,"verification_finding_days":120,"trend_snapshot_days":730}`))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.retentionRequest.IntervalSeconds != 7200 ||
		indexing.retentionRequest.ScanRunDays != 45 ||
		indexing.retentionRequest.UpdatedBy != "admin" {
		t.Fatalf("retention request = %+v", indexing.retentionRequest)
	}
}

func TestUpdateRetentionSettingsValidatesValues(t *testing.T) {
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "admin", Role: "admin"},
			},
		}),
		WithIndexingService(&fakeIndexingService{}),
	)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/retention-settings", bytes.NewBufferString(`{"interval_seconds":30,"scan_run_days":0,"cleanup_execution_days":365,"verification_finding_days":180,"trend_snapshot_days":365}`))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestAPINegativePathsReturnProblemResponses(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		path        string
		body        string
		user        AuthenticatedUser
		indexing    IndexingService
		wantStatus  int
		wantCode    string
		wantMessage string
	}{
		{
			name:       "malformed login json",
			method:     http.MethodPost,
			path:       "/api/v1/session",
			body:       "{",
			user:       AuthenticatedUser{Username: "admin", Role: "admin"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "start run invalid json",
			method:     http.MethodPost,
			path:       "/api/v1/index/runs",
			body:       "{",
			user:       AuthenticatedUser{Username: "admin", Role: "admin"},
			indexing:   &fakeIndexingService{},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name:       "start run indexing not configured",
			method:     http.MethodPost,
			path:       "/api/v1/index/runs",
			body:       `{"scan_type":"recent_sync"}`,
			user:       AuthenticatedUser{Username: "admin", Role: "admin"},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "indexing_not_configured",
		},
		{
			name:       "read only schedule mutation forbidden",
			method:     http.MethodPost,
			path:       "/api/v1/admin/indexing-schedules",
			body:       `{"name":"recent-sync","scan_type":"recent_sync","interval_seconds":1800}`,
			user:       AuthenticatedUser{Username: "reader", Role: "read_only"},
			indexing:   &fakeIndexingService{},
			wantStatus: http.StatusForbidden,
			wantCode:   "forbidden",
		},
		{
			name:       "invalid retry task id",
			method:     http.MethodPost,
			path:       "/api/v1/index/tasks/not-a-number/retry",
			user:       AuthenticatedUser{Username: "admin", Role: "admin"},
			indexing:   &fakeIndexingService{},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_path_parameter",
		},
		{
			name:       "invalid tag pattern",
			method:     http.MethodPost,
			path:       "/api/v1/admin/tag-patterns",
			body:       `{"name":"missing_regex"}`,
			user:       AuthenticatedUser{Username: "admin", Role: "admin"},
			indexing:   &fakeIndexingService{},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_tag_pattern",
		},
		{
			name:       "invalid cleanup execution",
			method:     http.MethodPost,
			path:       "/api/v1/cleanup/executions",
			body:       `{"nexus_instance_id":7,"policy_yaml":"name: bad","approval_text":"bad"}`,
			user:       AuthenticatedUser{Username: "admin", Role: "admin"},
			indexing:   &fakeIndexingService{err: fakeInvalidRequestError("approval text mismatch")},
			wantStatus: http.StatusBadRequest,
			wantCode:   "cleanup_execution_invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := []Option{}
			if tt.user.Username != "" {
				opts = append(opts, WithSessionManager(&fakeSessionManager{
					session: Session{
						Token:     "session-token",
						ExpiresAt: time.Now().Add(time.Hour),
						User:      tt.user,
					},
				}))
			}
			if tt.indexing != nil {
				opts = append(opts, WithIndexingService(tt.indexing))
			}
			server := NewServer(slog.New(slog.NewTextHandler(io.Discard, nil)), opts...)
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("X-Request-ID", "req-negative")
			if tt.user.Username != "" {
				req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
			}
			rec := httptest.NewRecorder()

			server.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			var problem problemResponse
			if err := json.NewDecoder(rec.Body).Decode(&problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != tt.wantCode || problem.RequestID != "req-negative" {
				t.Fatalf("problem = %+v", problem)
			}
		})
	}
}

func TestCustomRoleCapabilitiesAuthorizeAccess(t *testing.T) {
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User: AuthenticatedUser{
					Username:     "settings-user",
					Role:         "settings_manager",
					Capabilities: []string{CapabilityRead, CapabilityManageSettings},
				},
			},
		}),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/status", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestCustomRoleCapabilitiesDoNotInheritAdminFallback(t *testing.T) {
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User: AuthenticatedUser{
					Username:     "cleanup-user",
					Role:         "cleanup_executor",
					Capabilities: []string{CapabilityRead, CapabilityExecuteCleanup},
				},
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
	indexing := &fakeIndexingService{
		runs: []IndexRun{{ID: 42, NexusInstanceID: 7, ScanType: "recent_sync", Status: "running", StartedAt: time.Now()}},
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/index/runs?limit=25", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.runLimit != 25 {
		t.Fatalf("run limit = %d, want 25", indexing.runLimit)
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

func TestListCleanupExecutionsAllowsReadOnly(t *testing.T) {
	now := time.Now()
	indexing := &fakeIndexingService{
		cleanupExecutions: []CleanupExecution{{
			ID:                     11,
			NexusInstanceID:        7,
			PolicyName:             "ci-cleanup",
			Status:                 "running",
			RequestedBy:            "admin",
			DestructiveEnabled:     true,
			ApprovedCandidateCount: 3,
			CreatedAt:              now,
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cleanup/executions?nexus_instance_id=7&limit=25", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.cleanupNexusID != 7 || indexing.cleanupLimit != 25 {
		t.Fatalf("cleanup list query = nexus %d limit %d", indexing.cleanupNexusID, indexing.cleanupLimit)
	}
	var body struct {
		Executions []CleanupExecution `json:"executions"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Executions) != 1 || body.Executions[0].ID != 11 {
		t.Fatalf("executions = %+v", body.Executions)
	}
}

func TestCancelCleanupExecutionRequiresAdmin(t *testing.T) {
	indexing := &fakeIndexingService{
		cleanupExecution: CleanupExecution{ID: 11, Status: "cancel_requested", RequestedBy: "admin", CreatedAt: time.Now()},
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cleanup/executions/11/cancel", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.cleanupExecutionID != 11 || indexing.cleanupCanceledBy != "admin" {
		t.Fatalf("cancel request = id %d by %q", indexing.cleanupExecutionID, indexing.cleanupCanceledBy)
	}
}

func TestListCleanupExecutionEventsAllowsReadOnly(t *testing.T) {
	now := time.Now()
	indexing := &fakeIndexingService{
		cleanupEvents: []CleanupExecutionEvent{{
			ID:                 21,
			CleanupExecutionID: 11,
			EventType:          "tag_deleted",
			Status:             "deleted",
			CreatedAt:          now,
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cleanup/executions/11/events?limit=25", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.cleanupEventID != 11 || indexing.cleanupEventLimit != 25 {
		t.Fatalf("event query = id %d limit %d", indexing.cleanupEventID, indexing.cleanupEventLimit)
	}
	var body struct {
		Events []CleanupExecutionEvent `json:"events"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Events) != 1 || body.Events[0].EventType != "tag_deleted" {
		t.Fatalf("events = %+v", body.Events)
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

func TestAdditionalAPIRoutesTranslateRequests(t *testing.T) {
	now := time.Now()
	rps := 2.5
	indexing := &fakeIndexingService{
		instances: []NexusInstance{{ID: 7, Name: "local", BaseURL: "http://nexus:8081", CreatedAt: now, UpdatedAt: now}},
		schedules: []IndexingSchedule{{
			ID: 3, NexusInstanceID: 7, NexusName: "local", ScanType: "recent_sync", Name: "recent-sync", Enabled: true, IntervalSeconds: 1800, CreatedAt: now, UpdatedAt: now,
		}},
		savedSchedule: IndexingSchedule{ID: 4, NexusInstanceID: 7, Name: "weekly", ScanType: "bounded_reindex", Enabled: true, IntervalSeconds: 604800, CreatedAt: now, UpdatedAt: now},
		control:       IndexingControl{NexusInstanceID: 7, AssetListRPS: &rps},
		repositories:  []RepositoryAnalytics{{RepositoryID: 1, NexusInstanceID: 7, RepositoryName: "docker-hosted", ActiveTagCount: 2, LastSeenAt: &now}},
		images:        []ImageAnalytics{{ImageID: 2, RepositoryID: 1, NexusInstanceID: 7, RepositoryName: "docker-hosted", ImageName: "demo/app", ActiveTagCount: 2}},
		dismissedFinding: VerificationFinding{
			ID: 17, Severity: "medium", FindingType: "missing_manifest", Status: "dismissed", CreatedAt: now, LastSeenAt: now,
		},
		patterns:     []TagPattern{{ID: 1, Name: "semver", DisplayName: "SemVer", TagRegex: "^v", Enabled: true, CreatedAt: now, UpdatedAt: now}},
		savedPattern: TagPattern{ID: 2, Name: "channel", DisplayName: "Channel", TagRegex: "^(latest|stable)$", Enabled: true, CreatedAt: now, UpdatedAt: now},
		metrics:      "nexus_lens_scan_tasks{status=\"pending\"} 1\n",
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
	do := func(method, path, body string, wantStatus int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != wantStatus {
			t.Fatalf("%s %s status = %d, want %d; body %s", method, path, rec.Code, wantStatus, rec.Body.String())
		}
		return rec
	}

	do(http.MethodGet, "/api/v1/admin/nexus-instances?limit=12", "", http.StatusOK)
	if indexing.instanceLimit != 12 {
		t.Fatalf("instance limit = %d", indexing.instanceLimit)
	}
	do(http.MethodGet, "/api/v1/admin/indexing-schedules?nexus_instance_id=7&limit=25", "", http.StatusOK)
	if indexing.scheduleNexusID != 7 || indexing.scheduleLimit != 25 {
		t.Fatalf("schedule query = nexus %d limit %d", indexing.scheduleNexusID, indexing.scheduleLimit)
	}
	do(http.MethodPost, "/api/v1/admin/indexing-schedules", `{"nexus_name":"local","base_url":"http://nexus:8081","name":"weekly","scan_type":"bounded_reindex","interval_seconds":604800,"enabled":true,"scope":{"repositories":["docker-hosted"],"window_days":7}}`, http.StatusOK)
	if indexing.scheduleRequest.Name != "weekly" || indexing.scheduleRequest.Scope.WindowDays != 7 {
		t.Fatalf("schedule request = %+v", indexing.scheduleRequest)
	}
	do(http.MethodPut, "/api/v1/admin/nexus/7/rate-limits", `{"asset_list_rps":2.5}`, http.StatusOK)
	if indexing.rateLimitNexusID != 7 || indexing.rateLimitRequest.AssetListRPS == nil || *indexing.rateLimitRequest.AssetListRPS != 2.5 {
		t.Fatalf("rate limit request = nexus %d %+v", indexing.rateLimitNexusID, indexing.rateLimitRequest)
	}
	do(http.MethodGet, "/api/v1/analytics/repositories?nexus_instance_id=7&search=docker&limit=25", "", http.StatusOK)
	if indexing.analyticsRequest.Search != "docker" || indexing.analyticsRequest.Limit != 25 {
		t.Fatalf("repository analytics request = %+v", indexing.analyticsRequest)
	}
	do(http.MethodGet, "/api/v1/analytics/images?nexus_instance_id=7&repository_name=docker-hosted&search=demo&limit=10", "", http.StatusOK)
	if indexing.analyticsRequest.RepositoryName != "docker-hosted" || indexing.analyticsRequest.Search != "demo" {
		t.Fatalf("image analytics request = %+v", indexing.analyticsRequest)
	}
	do(http.MethodPost, "/api/v1/index/verification/findings/17/dismiss", "", http.StatusOK)
	if indexing.dismissedFindingID != 17 || indexing.dismissedBy != "admin" {
		t.Fatalf("dismiss request = id %d by %q", indexing.dismissedFindingID, indexing.dismissedBy)
	}
	do(http.MethodGet, "/api/v1/admin/tag-patterns?limit=20", "", http.StatusOK)
	if indexing.patternLimit != 20 {
		t.Fatalf("pattern limit = %d", indexing.patternLimit)
	}
	do(http.MethodPost, "/api/v1/admin/tag-patterns", `{"name":"channel","display_name":"Channel","tag_regex":"^(latest|stable)$","priority":100,"enabled":true}`, http.StatusOK)
	if indexing.patternSaveRequest.Name != "channel" || indexing.patternSaveRequest.Priority != 100 {
		t.Fatalf("pattern save request = %+v", indexing.patternSaveRequest)
	}
	rec := do(http.MethodGet, "/metrics", "", http.StatusOK)
	if !strings.Contains(rec.Body.String(), "nexus_lens_scan_tasks") {
		t.Fatalf("metrics body = %q", rec.Body.String())
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/indexing-schedules?nexus_instance_id=7&limit=25", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.scheduleNexusID != 7 || indexing.scheduleLimit != 25 {
		t.Fatalf("schedule query = nexus %d limit %d", indexing.scheduleNexusID, indexing.scheduleLimit)
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

func TestCORSPreflight(t *testing.T) {
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithCORSOrigin("https://lens.example"),
	)
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/index/runs", nil)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://lens.example" {
		t.Fatalf("cors origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("cors credentials = %q", got)
	}
}

func TestSecureSessionCookieOption(t *testing.T) {
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSecureSessionCookie(true),
		WithSessionManager(&fakeSessionManager{
			session: Session{
				Token:     "session-token",
				ExpiresAt: time.Now().Add(time.Hour),
				User:      AuthenticatedUser{Username: "admin", Role: "admin"},
			},
		}),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/session", bytes.NewBufferString(`{"username":"admin","password":"admin"}`))
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookies = %+v", cookies)
	}
}

func TestListNexusInstancesAllowsAuthenticatedUser(t *testing.T) {
	now := time.Now()
	indexing := &fakeIndexingService{
		instances: []NexusInstance{{ID: 7, Name: "local", BaseURL: "http://nexus:8081", CreatedAt: now, UpdatedAt: now}},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{Token: "session-token", ExpiresAt: time.Now().Add(time.Hour), User: AuthenticatedUser{Username: "reader", Role: "read_only"}},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/nexus-instances?limit=25", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.instanceLimit != 25 {
		t.Fatalf("instance limit = %d", indexing.instanceLimit)
	}
	var body struct {
		Instances []NexusInstance `json:"instances"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Instances) != 1 || body.Instances[0].Name != "local" {
		t.Fatalf("instances = %+v", body.Instances)
	}
}

func TestListRepositoryAndImageAnalytics(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name        string
		path        string
		indexing    *fakeIndexingService
		assertBody  func(*testing.T, *httptest.ResponseRecorder)
		assertQuery func(*testing.T, *fakeIndexingService)
	}{
		{
			name: "repositories",
			path: "/api/v1/analytics/repositories?nexus_instance_id=7&search=docker&limit=25",
			indexing: &fakeIndexingService{repositories: []RepositoryAnalytics{{
				RepositoryID:           1,
				NexusInstanceID:        7,
				NexusName:              "local",
				RepositoryName:         "docker-hosted",
				ImageCount:             2,
				DockerLogicalBytes:     100,
				DockerUniqueLayerBytes: 50,
				LastSeenAt:             &now,
			}}},
			assertBody: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				var body struct {
					Repositories []RepositoryAnalytics `json:"repositories"`
				}
				if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if len(body.Repositories) != 1 || body.Repositories[0].RepositoryName != "docker-hosted" {
					t.Fatalf("repositories = %+v", body.Repositories)
				}
			},
		},
		{
			name: "images",
			path: "/api/v1/analytics/images?nexus_instance_id=7&repository_name=docker-hosted&search=demo&limit=25",
			indexing: &fakeIndexingService{images: []ImageAnalytics{{
				ImageID:              2,
				RepositoryID:         1,
				NexusInstanceID:      7,
				NexusName:            "local",
				RepositoryName:       "docker-hosted",
				ImageName:            "demo/app",
				ActiveTagCount:       3,
				DockerLogicalBytes:   100,
				NewestTagLastSeenAt:  &now,
				OldestTagFirstSeenAt: &now,
			}}},
			assertBody: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				var body struct {
					Images []ImageAnalytics `json:"images"`
				}
				if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if len(body.Images) != 1 || body.Images[0].ImageName != "demo/app" {
					t.Fatalf("images = %+v", body.Images)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewServer(
				slog.New(slog.NewTextHandler(io.Discard, nil)),
				WithSessionManager(&fakeSessionManager{
					session: Session{Token: "session-token", ExpiresAt: time.Now().Add(time.Hour), User: AuthenticatedUser{Username: "reader", Role: "read_only"}},
				}),
				WithIndexingService(tt.indexing),
			)
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
			rec := httptest.NewRecorder()

			server.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if tt.indexing.analyticsRequest.NexusInstanceID != 7 || tt.indexing.analyticsRequest.Limit != 25 {
				t.Fatalf("analytics request = %+v", tt.indexing.analyticsRequest)
			}
			tt.assertBody(t, rec)
		})
	}
}

func TestTagPatternExamplesSuggestionsAndList(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name       string
		path       string
		indexing   *fakeIndexingService
		assertBody func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name: "examples",
			path: "/api/v1/analytics/tag-patterns/examples?nexus_instance_id=7&repository_name=docker-hosted&pattern_name=feature_branch&limit=25",
			indexing: &fakeIndexingService{patternExamples: []TagPatternExample{{
				PatternName:    "feature_branch",
				RepositoryName: "docker-hosted",
				ImageName:      "demo/app",
				Tag:            "feature-x",
				FirstSeenAt:    now,
				LastSeenAt:     now,
			}}},
			assertBody: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				var body struct {
					Examples []TagPatternExample `json:"examples"`
				}
				if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if len(body.Examples) != 1 || body.Examples[0].Tag != "feature-x" {
					t.Fatalf("examples = %+v", body.Examples)
				}
			},
		},
		{
			name: "suggestions",
			path: "/api/v1/analytics/tag-patterns/suggestions?nexus_instance_id=7&repository_name=docker-hosted&limit=25",
			indexing: &fakeIndexingService{patternSuggestions: []TagPatternSuggestion{{
				TagShape:       "A-0",
				TagCount:       3,
				ExampleTags:    []string{"pr-1"},
				SuggestedRegex: "^[A-Za-z]+-[0-9]+$",
			}}},
			assertBody: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				var body struct {
					Suggestions []TagPatternSuggestion `json:"suggestions"`
				}
				if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if len(body.Suggestions) != 1 || body.Suggestions[0].TagShape != "A-0" {
					t.Fatalf("suggestions = %+v", body.Suggestions)
				}
			},
		},
		{
			name: "admin list",
			path: "/api/v1/admin/tag-patterns?limit=25",
			indexing: &fakeIndexingService{patterns: []TagPattern{{
				ID:          1,
				Name:        "semver",
				DisplayName: "SemVer",
				TagRegex:    "^v?[0-9]+",
				Enabled:     true,
				BuiltIn:     true,
				CreatedAt:   now,
				UpdatedAt:   now,
			}}},
			assertBody: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				var body struct {
					Patterns []TagPattern `json:"patterns"`
				}
				if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if len(body.Patterns) != 1 || body.Patterns[0].Name != "semver" {
					t.Fatalf("patterns = %+v", body.Patterns)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewServer(
				slog.New(slog.NewTextHandler(io.Discard, nil)),
				WithSessionManager(&fakeSessionManager{
					session: Session{Token: "session-token", ExpiresAt: time.Now().Add(time.Hour), User: AuthenticatedUser{Username: "reader", Role: "read_only"}},
				}),
				WithIndexingService(tt.indexing),
			)
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
			rec := httptest.NewRecorder()

			server.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			tt.assertBody(t, rec)
		})
	}
}

func TestResumeNexusIndexingRequiresAdmin(t *testing.T) {
	indexing := &fakeIndexingService{
		control: IndexingControl{NexusInstanceID: 7, Paused: false},
	}
	server := NewServer(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithSessionManager(&fakeSessionManager{
			session: Session{Token: "session-token", ExpiresAt: time.Now().Add(time.Hour), User: AuthenticatedUser{Username: "admin", Role: "admin"}},
		}),
		WithIndexingService(indexing),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/index/nexus/7/resume", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session-token"})
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if indexing.resumedNexusID != 7 {
		t.Fatalf("resumed nexus id = %d", indexing.resumedNexusID)
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
	cleanupExecutions       []CleanupExecution
	cleanupEvents           []CleanupExecutionEvent
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
	runLimit                int32
	findingNexusID          int64
	findingStatus           string
	findingLimit            int32
	scheduleNexusID         int64
	scheduleLimit           int32
	scheduleRequest         UpsertIndexingScheduleRequest
	instanceLimit           int32
	patternLimit            int32
	rateLimitNexusID        int64
	rateLimitRequest        RateLimitSettings
	retentionSettings       RetentionSettings
	retentionRequest        RetentionSettings
	analyticsNexusID        int64
	analyticsRequest        AnalyticsListRequest
	packageRequest          AnalyticsListRequest
	trendRequest            TrendSeriesRequest
	trendTopMoversRequest   TrendTopMoversRequest
	cleanupRequest          CleanupSimulationRequest
	cleanupExecutionRequest CleanupExecutionRequest
	cleanupNexusID          int64
	cleanupLimit            int32
	cleanupExecutionID      int64
	cleanupEventID          int64
	cleanupEventLimit       int32
	cleanupCanceledBy       string
	patternRequest          TagPatternListRequest
	patternSaveRequest      UpsertTagPatternRequest
	reclassificationRequest TagReclassificationRequest
	retriedTaskID           int64
	canceledRunID           int64
	dismissedFindingID      int64
	dismissedBy             string
	pausedReason            string
	pausedBy                string
	resumedNexusID          int64
	err                     error
}

type fakeInvalidRequestError string

func (e fakeInvalidRequestError) Error() string        { return string(e) }
func (e fakeInvalidRequestError) InvalidRequest() bool { return true }

func (f *fakeIndexingService) StartIndexRun(_ context.Context, req StartIndexRunRequest) (IndexRun, error) {
	f.request = req
	if f.err != nil {
		return IndexRun{}, f.err
	}
	return f.started, nil
}

func (f *fakeIndexingService) ListIndexRuns(_ context.Context, limit int32) ([]IndexRun, error) {
	f.runLimit = limit
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

func (f *fakeIndexingService) ListNexusInstances(_ context.Context, limit int32) ([]NexusInstance, error) {
	f.instanceLimit = limit
	return f.instances, nil
}

func (f *fakeIndexingService) ListIndexingSchedules(_ context.Context, nexusInstanceID int64, limit int32) ([]IndexingSchedule, error) {
	f.scheduleNexusID = nexusInstanceID
	f.scheduleLimit = limit
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

func (f *fakeIndexingService) GetRetentionSettings(context.Context) (RetentionSettings, error) {
	return f.retentionSettings, nil
}

func (f *fakeIndexingService) UpdateRetentionSettings(_ context.Context, req RetentionSettings) (RetentionSettings, error) {
	f.retentionRequest = req
	req.Configured = true
	return req, nil
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

func (f *fakeIndexingService) ListCleanupExecutions(_ context.Context, nexusInstanceID int64, limit int32) ([]CleanupExecution, error) {
	f.cleanupNexusID = nexusInstanceID
	f.cleanupLimit = limit
	return f.cleanupExecutions, nil
}

func (f *fakeIndexingService) CancelCleanupExecution(_ context.Context, cleanupExecutionID int64, canceledBy string) (CleanupExecution, error) {
	f.cleanupExecutionID = cleanupExecutionID
	f.cleanupCanceledBy = canceledBy
	return f.cleanupExecution, nil
}

func (f *fakeIndexingService) ListCleanupExecutionEvents(_ context.Context, cleanupExecutionID int64, limit int32) ([]CleanupExecutionEvent, error) {
	f.cleanupEventID = cleanupExecutionID
	f.cleanupEventLimit = limit
	return f.cleanupEvents, nil
}

func (f *fakeIndexingService) ListTagPatterns(_ context.Context, limit int32) ([]TagPattern, error) {
	f.patternLimit = limit
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

func (f *fakeIndexingService) ResumeNexusIndexing(_ context.Context, nexusInstanceID int64) (IndexingControl, error) {
	f.resumedNexusID = nexusInstanceID
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

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
