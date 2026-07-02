package auth

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/pbsladek/nexus-lens/internal/db"
)

type authFakeDBTX struct {
	rowFunc  func(query string, args []any) authFakeRow
	execFunc func(query string, args []any) (pgconn.CommandTag, error)
	queries  []string
	args     [][]any
}

func (f *authFakeDBTX) Exec(_ context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	f.queries = append(f.queries, query)
	f.args = append(f.args, args)
	if f.execFunc != nil {
		return f.execFunc(query, args)
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (f *authFakeDBTX) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, fmt.Errorf("unexpected query")
}

func (f *authFakeDBTX) QueryRow(_ context.Context, query string, args ...interface{}) pgx.Row {
	f.queries = append(f.queries, query)
	f.args = append(f.args, args)
	if f.rowFunc == nil {
		return authFakeRow{err: fmt.Errorf("unexpected query row: %s", authQueryName(query))}
	}
	return f.rowFunc(query, args)
}

type authFakeRow struct {
	values []any
	err    error
}

func (r authFakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan destination count %d does not match values %d", len(dest), len(r.values))
	}
	for i := range dest {
		if err := authAssignScanValue(dest[i], r.values[i]); err != nil {
			return fmt.Errorf("scan value %d: %w", i, err)
		}
	}
	return nil
}

func authAssignScanValue(dest any, value any) error {
	target := reflect.ValueOf(dest)
	if target.Kind() != reflect.Pointer || target.IsNil() {
		return fmt.Errorf("destination is not a nonnil pointer")
	}
	elem := target.Elem()
	if value == nil {
		elem.SetZero()
		return nil
	}
	source := reflect.ValueOf(value)
	if source.Type().AssignableTo(elem.Type()) {
		elem.Set(source)
		return nil
	}
	if source.Type().ConvertibleTo(elem.Type()) {
		elem.Set(source.Convert(elem.Type()))
		return nil
	}
	return fmt.Errorf("cannot assign %T to %s", value, elem.Type())
}

func authQueryName(query string) string {
	line, _, _ := strings.Cut(query, "\n")
	return strings.TrimPrefix(line, "-- name: ")
}

func TestBootstrapInitialAdminCreatesUserWhenNoUsersExist(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	created := false
	fake := &authFakeDBTX{}
	fake.rowFunc = func(query string, args []any) authFakeRow {
		switch {
		case strings.Contains(query, "CountAppUsers"):
			return authFakeRow{values: []any{int64(0)}}
		case strings.Contains(query, "CreateAppUser"):
			created = true
			if args[0] != "admin" || args[2] != RoleAdmin {
				t.Fatalf("create user args = %+v", args)
			}
			if args[1] == "" || args[1] == "password" {
				t.Fatalf("password hash was not generated: %+v", args)
			}
			return authFakeRow{values: appUserValues(1, "admin", args[1].(string), RoleAdmin, now)}
		default:
			return authFakeRow{err: fmt.Errorf("unexpected query row: %s", authQueryName(query))}
		}
	}
	service := NewService(db.New(fake), slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := service.BootstrapInitialAdmin(context.Background(), "admin", "password"); err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatalf("expected initial admin to be created")
	}
}

func TestAuthServiceAuthenticatesAndCreatesSessionWithFakeDB(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	hash, err := HashPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	var sessionHash string
	fake := &authFakeDBTX{}
	fake.rowFunc = func(query string, args []any) authFakeRow {
		switch {
		case strings.Contains(query, "GetActiveAppUserByUsername"):
			if args[0] != "admin" {
				t.Fatalf("username arg = %+v", args)
			}
			return authFakeRow{values: activeUserValues(1, "admin", hash, RoleAdmin, []string{"read", "manage_index"}, now)}
		case strings.Contains(query, "CreateAppSession"):
			sessionHash = args[1].(string)
			if args[0] != int64(1) || sessionHash == "" {
				t.Fatalf("session args = %+v", args)
			}
			return authFakeRow{values: appSessionValues(10, 1, sessionHash, now, now.Add(time.Hour))}
		default:
			return authFakeRow{err: fmt.Errorf("unexpected query row: %s", authQueryName(query))}
		}
	}
	service := NewService(db.New(fake), slog.New(slog.NewTextHandler(io.Discard, nil)))
	service.sessionTTL = time.Hour

	user, err := service.Authenticate(context.Background(), "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "admin" || user.Role != RoleAdmin || len(user.Capabilities) != 2 {
		t.Fatalf("user = %+v", user)
	}
	session, err := service.CreateSession(context.Background(), "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if session.Token == "" || session.User.Username != "admin" || sessionHash == "" || sessionHash == session.Token {
		t.Fatalf("session = %+v sessionHash=%q", session, sessionHash)
	}
}

func TestAuthServiceAuthenticatesAndRevokesSessionWithFakeDB(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	token := "session-token"
	fake := &authFakeDBTX{}
	fake.rowFunc = func(query string, args []any) authFakeRow {
		switch {
		case strings.Contains(query, "GetActiveAppSessionByTokenHash"):
			if args[0] != hashSessionToken(token) {
				t.Fatalf("token hash arg = %+v", args)
			}
			return authFakeRow{values: activeSessionValues(10, 1, args[0].(string), "admin", RoleAdmin, []string{"read"}, now, now.Add(time.Hour))}
		default:
			return authFakeRow{err: fmt.Errorf("unexpected query row: %s", authQueryName(query))}
		}
	}
	fake.execFunc = func(query string, args []any) (pgconn.CommandTag, error) {
		if !strings.Contains(query, "RevokeAppSessionByTokenHash") {
			t.Fatalf("unexpected exec query: %s", authQueryName(query))
		}
		if args[0] != hashSessionToken(token) {
			t.Fatalf("revoke token hash arg = %+v", args)
		}
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	service := NewService(db.New(fake), slog.New(slog.NewTextHandler(io.Discard, nil)))

	user, err := service.AuthenticateSession(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "admin" || user.Role != RoleAdmin {
		t.Fatalf("user = %+v", user)
	}
	if err := service.RevokeSession(context.Background(), token); err != nil {
		t.Fatal(err)
	}
}

func appUserValues(id int64, username, hash, role string, now time.Time) []any {
	return []any{id, username, hash, role, authPGTime(now), authPGTime(now), pgtype.Timestamptz{}}
}

func activeUserValues(id int64, username, hash, role string, capabilities []string, now time.Time) []any {
	return []any{id, username, hash, role, capabilities, authPGTime(now), authPGTime(now), pgtype.Timestamptz{}}
}

func appSessionValues(id, userID int64, tokenHash string, createdAt, expiresAt time.Time) []any {
	return []any{id, userID, tokenHash, authPGTime(createdAt), authPGTime(expiresAt), pgtype.Timestamptz{}}
}

func activeSessionValues(id, userID int64, tokenHash, username, role string, capabilities []string, createdAt, expiresAt time.Time) []any {
	return []any{id, userID, tokenHash, authPGTime(createdAt), authPGTime(expiresAt), pgtype.Timestamptz{}, username, role, capabilities}
}

func authPGTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
