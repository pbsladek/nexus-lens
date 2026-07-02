package indexing

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/pbsladek/nexus-lens/internal/db"
)

func TestNormalizeRetentionSettingsAppliesDefaults(t *testing.T) {
	settings := normalizeRetentionSettings(RetentionSettings{})
	if settings.IntervalSeconds != 3600 ||
		settings.ScanRunDays != 30 ||
		settings.CleanupExecutionDays != 365 ||
		settings.VerificationFindingDays != 180 ||
		settings.TrendSnapshotDays != 365 {
		t.Fatalf("settings = %+v", settings)
	}

	custom := normalizeRetentionSettings(RetentionSettings{
		IntervalSeconds:         7200,
		ScanRunDays:             45,
		CleanupExecutionDays:    400,
		VerificationFindingDays: 120,
		TrendSnapshotDays:       730,
	})
	if custom.IntervalSeconds != 7200 || custom.ScanRunDays != 45 || custom.TrendSnapshotDays != 730 {
		t.Fatalf("custom settings = %+v", custom)
	}
}

func TestMergeRetentionSettingsOverlaysDatabaseRow(t *testing.T) {
	updatedAt := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	settings := mergeRetentionSettings(RetentionSettings{
		IntervalSeconds:         3600,
		ScanRunDays:             30,
		CleanupExecutionDays:    365,
		VerificationFindingDays: 180,
		TrendSnapshotDays:       365,
	}, db.AppRetentionSetting{
		ScanRunDays:       pgtype.Int4{Int32: 60, Valid: true},
		TrendSnapshotDays: pgtype.Int4{Int32: 730, Valid: true},
		UpdatedBy:         pgtype.Text{String: "admin", Valid: true},
		UpdatedAt:         pgtype.Timestamptz{Time: updatedAt, Valid: true},
	})

	if !settings.Configured ||
		settings.IntervalSeconds != 3600 ||
		settings.ScanRunDays != 60 ||
		settings.CleanupExecutionDays != 365 ||
		settings.TrendSnapshotDays != 730 ||
		settings.UpdatedBy != "admin" ||
		settings.UpdatedAt == nil ||
		!settings.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("merged settings = %+v", settings)
	}
}

func TestServiceLimitHelpers(t *testing.T) {
	if analyticsLimit(0) != 50 || analyticsLimit(250) != 50 || analyticsLimit(25) != 25 {
		t.Fatalf("analyticsLimit returned unexpected values")
	}
	if boundedLimit(0, 100) != 100 || boundedLimit(250, 100) != 100 || boundedLimit(25, 100) != 25 {
		t.Fatalf("boundedLimit returned unexpected values")
	}
}

func TestTrendDateOrDefault(t *testing.T) {
	fallback := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if got, err := trendDateOrDefault("", fallback); err != nil || !got.Equal(fallback) {
		t.Fatalf("trendDateOrDefault fallback = %s, %v", got, err)
	}
	got, err := trendDateOrDefault("2026-06-01", fallback)
	if err != nil || got.Format("2006-01-02") != "2026-06-01" {
		t.Fatalf("trendDateOrDefault parsed = %s, %v", got, err)
	}
	if _, err := trendDateOrDefault("06/01/2026", fallback); err == nil {
		t.Fatalf("expected invalid trend date to fail")
	}
}

func TestScanTypeRequiresOperationLock(t *testing.T) {
	if !scanTypeRequiresOperationLock(ScanTypeFullReconcile) || !scanTypeRequiresOperationLock(ScanTypeBoundedReindex) {
		t.Fatalf("expected full and bounded scans to require operation locks")
	}
	if scanTypeRequiresOperationLock(ScanTypeRecentSync) || scanTypeRequiresOperationLock(ScanTypeVerification) {
		t.Fatalf("did not expect recent sync or verification to require operation locks")
	}
}

func TestServiceErrorTypes(t *testing.T) {
	policyErr := PolicyValidationError{Message: "bad policy"}
	if policyErr.Error() != "bad policy" || !policyErr.InvalidRequest() {
		t.Fatalf("policy error = %q invalid=%v", policyErr.Error(), policyErr.InvalidRequest())
	}
	taskErr := TaskError{Code: "code", Message: "message"}
	if taskErr.Error() != "message" {
		t.Fatalf("task error = %q", taskErr.Error())
	}
	taskErr.Message = ""
	if taskErr.Error() != "code" {
		t.Fatalf("task fallback error = %q", taskErr.Error())
	}
	conflict := &OperationLockConflict{NexusInstanceID: 7, BlockingScanRunID: 42, BlockingScanType: ScanTypeFullReconcile}
	runID, scanType := conflict.OperationLockConflict()
	if runID != 42 || scanType != ScanTypeFullReconcile || conflict.Error() == "" {
		t.Fatalf("conflict = %q id=%d type=%s", conflict.Error(), runID, scanType)
	}
}
