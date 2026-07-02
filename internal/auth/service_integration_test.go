package auth

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pbsladek/nexus-lens/internal/db"
	"github.com/pbsladek/nexus-lens/internal/postgres"
)

func TestIntegrationBootstrapAuthenticateSessionAndRevoke(t *testing.T) {
	queries, pool := migratedAuthQueries(t)
	ctx := context.Background()
	service := NewService(queries, slog.Default())

	if err := service.BootstrapInitialAdmin(ctx, "", ""); err != nil {
		t.Fatal(err)
	}
	count, err := queries.CountAppUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("user count after empty bootstrap = %d", count)
	}

	if err := service.BootstrapInitialAdmin(ctx, "admin", "correct-password"); err != nil {
		t.Fatal(err)
	}
	if err := service.BootstrapInitialAdmin(ctx, "second", "password"); err != nil {
		t.Fatal(err)
	}
	count, err = queries.CountAppUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("user count after repeated bootstrap = %d", count)
	}

	user, err := service.Authenticate(ctx, "admin", "correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "admin" || user.Role != RoleAdmin || !containsCapability(user.Capabilities, "manage_index") {
		t.Fatalf("user = %+v", user)
	}
	if _, err := service.Authenticate(ctx, "admin", "wrong-password"); err != ErrInvalidCredentials {
		t.Fatalf("wrong password err = %v", err)
	}

	session, err := service.CreateSession(ctx, "admin", "correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if session.Token == "" || !session.ExpiresAt.After(time.Now()) {
		t.Fatalf("session = %+v", session)
	}
	sessionUser, err := service.AuthenticateSession(ctx, session.Token)
	if err != nil {
		t.Fatal(err)
	}
	if sessionUser.Username != "admin" {
		t.Fatalf("session user = %+v", sessionUser)
	}
	if err := service.RevokeSession(ctx, session.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateSession(ctx, session.Token); err != ErrInvalidCredentials {
		t.Fatalf("revoked session err = %v", err)
	}

	if _, err := pool.Exec(ctx, "UPDATE app_users SET disabled_at = now() WHERE username = 'admin'"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, "admin", "correct-password"); err != ErrInvalidCredentials {
		t.Fatalf("disabled user err = %v", err)
	}
}

func containsCapability(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func migratedAuthQueries(t *testing.T) (*db.Queries, *pgxpool.Pool) {
	t.Helper()
	rawURL := os.Getenv("NEXUS_LENS_TEST_DATABASE_URL")
	if rawURL == "" {
		t.Skip("set NEXUS_LENS_TEST_DATABASE_URL to run Postgres integration tests")
	}

	ctx := context.Background()
	adminPool, err := pgxpool.New(ctx, rawURL)
	if err != nil {
		t.Fatalf("open admin test database pool: %v", err)
	}
	schema := "nexus_lens_auth_test_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = adminPool.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		adminPool.Close()
	})

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	values := parsed.Query()
	values.Set("search_path", schema)
	parsed.RawQuery = values.Encode()

	database, err := postgres.Open(ctx, postgres.Config{URL: parsed.String()}, slog.Default())
	if err != nil {
		t.Fatalf("open migrated test database: %v", err)
	}
	t.Cleanup(database.Close)

	return db.New(database.Pool), database.Pool
}
