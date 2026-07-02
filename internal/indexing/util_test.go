package indexing

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIndexingValueHelpers(t *testing.T) {
	if text("").Valid || text("x").String != "x" || !textRequired("").Valid {
		t.Fatalf("text helpers returned unexpected values")
	}
	if optionalInt8(0).Valid || optionalInt8(9).Int64 != 9 || optionalInt4(0).Valid || optionalInt4(7).Int32 != 7 {
		t.Fatalf("optional int helpers returned unexpected values")
	}
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if !timestamptz(now).Valid || !pgDate(now).Valid {
		t.Fatalf("time helpers returned invalid values")
	}
}

func TestIndexingParseAndJSONHelpers(t *testing.T) {
	if !parseNexusTime("2026-07-01T12:00:00Z").Valid || parseNexusTime("bad").Valid {
		t.Fatalf("parseNexusTime returned unexpected values")
	}
	if string(jsonBytes(map[string]any{"ok": true})) != `{"ok":true}` {
		t.Fatalf("jsonBytes did not marshal object")
	}
	if string(jsonBytes(func() {})) != "{}" {
		t.Fatalf("jsonBytes should fall back for unmarshalable values")
	}
	value := 2.5
	if !numericFromPtr(&value).Valid || numericFromPtr(nil).Valid {
		t.Fatalf("numericFromPtr returned unexpected values")
	}
}

func TestIndexingEnvHelpers(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "secret")
	if err := os.WriteFile(secretPath, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("INDEXING_SECRET_FILE", secretPath)
	if got := envOrFile("INDEXING_SECRET", "INDEXING_SECRET_FILE"); got != "from-file" {
		t.Fatalf("envOrFile file = %q", got)
	}
	t.Setenv("INDEXING_SECRET", "from-env")
	if got := envOrFile("INDEXING_SECRET", "INDEXING_SECRET_FILE"); got != "from-env" {
		t.Fatalf("envOrFile env = %q", got)
	}

	t.Setenv("INDEXING_BOOL", "true")
	t.Setenv("INDEXING_BOOL_BAD", "bad")
	t.Setenv("INDEXING_FLOAT", "3.5")
	t.Setenv("INDEXING_INT", "42")
	if !envBool("INDEXING_BOOL", false) || !envBool("INDEXING_BOOL_BAD", true) {
		t.Fatalf("envBool returned unexpected values")
	}
	if envFloat("INDEXING_FLOAT", 1) != 3.5 || envFloat("INDEXING_FLOAT_BAD", 1) != 1 {
		t.Fatalf("envFloat returned unexpected values")
	}
	if envInt("INDEXING_INT", 1) != 42 || envInt("INDEXING_INT_BAD", 1) != 1 {
		t.Fatalf("envInt returned unexpected values")
	}
}

func TestNewLockTokenAndWorkerID(t *testing.T) {
	token, err := newLockToken()
	if err != nil {
		t.Fatal(err)
	}
	if !token.Valid {
		t.Fatalf("lock token is invalid")
	}
	t.Setenv("NEXUS_LENS_WORKER_ID", "worker-test")
	if workerID() != "worker-test" {
		t.Fatalf("workerID did not use env")
	}
}
