package postgres

import (
	"strings"
	"testing"
)

func TestConfigFromEnvURLTakesPrecedenceAndRedacts(t *testing.T) {
	t.Setenv("NEXUS_LENS_DATABASE_URL", "postgres://user:secret@example:5432/app?sslmode=disable")
	t.Setenv("PGHOST", "ignored")
	t.Setenv("PGDATABASE", "ignored")
	t.Setenv("PGUSER", "ignored")
	t.Setenv("PGPASSWORD", "ignored")
	t.Setenv("PGSSLMODE", "verify-full")

	cfg := FromEnv()
	conn, err := cfg.ConnString()
	if err != nil {
		t.Fatal(err)
	}
	if conn != "postgres://user:secret@example:5432/app?sslmode=disable" {
		t.Fatalf("conn string = %q", conn)
	}
	redacted := cfg.Redacted()
	if redacted["url"] != true || redacted["host"] != "ignored" {
		t.Fatalf("redacted = %+v", redacted)
	}
	if _, ok := redacted["password"]; ok {
		t.Fatalf("redacted output includes password: %+v", redacted)
	}
}

func TestConfigConnStringFromPGEnvAndDefaults(t *testing.T) {
	cfg := Config{
		Host:     "postgres.local",
		Port:     5433,
		Database: "nexus_lens",
		User:     "nexus",
		Password: "secret",
	}

	conn, err := cfg.ConnString()
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		"postgres://nexus:secret@postgres.local:5433/nexus_lens",
		"sslmode=require",
	} {
		if !strings.Contains(conn, part) {
			t.Fatalf("conn string %q missing %q", conn, part)
		}
	}
}

func TestConfigConnStringValidation(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "missing host", cfg: Config{}, want: "PGHOST or NEXUS_LENS_DATABASE_URL is required"},
		{name: "missing database", cfg: Config{Host: "postgres"}, want: "PGDATABASE is required"},
		{name: "missing user", cfg: Config{Host: "postgres", Database: "app"}, want: "PGUSER is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.cfg.ConnString()
			if err == nil || err.Error() != tt.want {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}
