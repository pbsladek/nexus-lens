package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestNewServiceDefaultsSessionTTL(t *testing.T) {
	service := NewService(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if service.sessionTTL != DefaultSessionTTL {
		t.Fatalf("session ttl = %s, want %s", service.sessionTTL, DefaultSessionTTL)
	}
}

func TestAuthServiceRejectsEmptyCredentialsBeforeDatabase(t *testing.T) {
	service := NewService(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()

	if _, err := service.Authenticate(ctx, "", "password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate err = %v", err)
	}
	if _, err := service.CreateSession(ctx, "admin", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("CreateSession err = %v", err)
	}
	if _, _, err := service.authenticateRow(ctx, "", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("authenticateRow err = %v", err)
	}
}

func TestAuthenticateSessionRejectsEmptyTokenBeforeDatabase(t *testing.T) {
	service := NewService(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := service.AuthenticateSession(context.Background(), ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("AuthenticateSession err = %v", err)
	}
}

func TestRevokeSessionIgnoresEmptyTokenBeforeDatabase(t *testing.T) {
	service := NewService(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := service.RevokeSession(context.Background(), ""); err != nil {
		t.Fatalf("RevokeSession err = %v", err)
	}
}

func TestServiceSessionTTLCanBeOverriddenInPackage(t *testing.T) {
	service := NewService(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	service.sessionTTL = time.Minute
	if service.sessionTTL != time.Minute {
		t.Fatalf("session ttl = %s", service.sessionTTL)
	}
}
