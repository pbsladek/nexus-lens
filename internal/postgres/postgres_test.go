package postgres

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestDatabaseNilLifecycle(t *testing.T) {
	var db *Database
	db.Close()
	if err := db.Ping(context.Background()); err == nil || err.Error() != "database is not configured" {
		t.Fatalf("nil database ping err = %v", err)
	}

	db = &Database{}
	db.Close()
	if err := db.Ping(context.Background()); err == nil || err.Error() != "database is not configured" {
		t.Fatalf("empty database ping err = %v", err)
	}
}

func TestGooseSlogLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	gooseLogger := gooseSlogLogger{logger: logger}

	gooseLogger.Printf("migration %d", 1)
	gooseLogger.Fatalf("failed %s", "migration")

	out := buf.String()
	if !strings.Contains(out, "migration 1") || !strings.Contains(out, "failed migration") {
		t.Fatalf("logger output = %q", out)
	}
}
