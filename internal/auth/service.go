package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/pbsladek/nexus-lens/internal/db"
)

const (
	RoleAdmin    = "admin"
	RoleReadOnly = "read_only"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type User struct {
	Username     string
	Role         string
	Capabilities []string
}

type Service struct {
	queries    *db.Queries
	logger     *slog.Logger
	sessionTTL time.Duration
}

func NewService(queries *db.Queries, logger *slog.Logger) *Service {
	return &Service{queries: queries, logger: logger, sessionTTL: DefaultSessionTTL}
}

func (s *Service) BootstrapInitialAdmin(ctx context.Context, username, password string) error {
	count, err := s.queries.CountAppUsers(ctx)
	if err != nil {
		return fmt.Errorf("count app users: %w", err)
	}
	if count > 0 {
		s.logger.Info("initial admin bootstrap skipped", "reason", "app users already exist")
		return nil
	}
	if username == "" || password == "" {
		s.logger.Warn("initial admin bootstrap skipped", "reason", "missing username or password")
		return nil
	}

	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := s.queries.CreateAppUser(ctx, db.CreateAppUserParams{
		Username:     username,
		PasswordHash: hash,
		Role:         RoleAdmin,
	}); err != nil {
		return fmt.Errorf("create initial admin: %w", err)
	}

	s.logger.Info("initial admin created", "username", username)
	return nil
}

func (s *Service) Authenticate(ctx context.Context, username, password string) (User, error) {
	_, user, err := s.authenticateRow(ctx, username, password)
	return user, err
}

func (s *Service) CreateSession(ctx context.Context, username, password string) (Session, error) {
	userRow, user, err := s.authenticateRow(ctx, username, password)
	if err != nil {
		return Session{}, err
	}

	token, tokenHash, err := newSessionToken()
	if err != nil {
		return Session{}, err
	}
	expiresAt := time.Now().UTC().Add(s.sessionTTL)
	if _, err := s.queries.CreateAppSession(ctx, db.CreateAppSessionParams{
		UserID:    userRow.ID,
		TokenHash: tokenHash,
		ExpiresAt: timestamptz(expiresAt),
	}); err != nil {
		return Session{}, fmt.Errorf("create app session: %w", err)
	}

	return Session{Token: token, ExpiresAt: expiresAt, User: user}, nil
}

func (s *Service) AuthenticateSession(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrInvalidCredentials
	}
	session, err := s.queries.GetActiveAppSessionByTokenHash(ctx, hashSessionToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrInvalidCredentials
		}
		return User{}, fmt.Errorf("load app session: %w", err)
	}
	return User{Username: session.Username, Role: session.Role, Capabilities: session.Capabilities}, nil
}

func (s *Service) RevokeSession(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if err := s.queries.RevokeAppSessionByTokenHash(ctx, hashSessionToken(token)); err != nil {
		return fmt.Errorf("revoke app session: %w", err)
	}
	return nil
}

func (s *Service) authenticateRow(ctx context.Context, username, password string) (db.GetActiveAppUserByUsernameRow, User, error) {
	if username == "" || password == "" {
		return db.GetActiveAppUserByUsernameRow{}, User{}, ErrInvalidCredentials
	}

	user, err := s.queries.GetActiveAppUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.GetActiveAppUserByUsernameRow{}, User{}, ErrInvalidCredentials
		}
		return db.GetActiveAppUserByUsernameRow{}, User{}, fmt.Errorf("load app user: %w", err)
	}

	ok, err := VerifyPassword(user.PasswordHash, password)
	if err != nil {
		s.logger.Warn("stored password hash could not be verified", "username", username, "error", err)
		return db.GetActiveAppUserByUsernameRow{}, User{}, ErrInvalidCredentials
	}
	if !ok {
		return db.GetActiveAppUserByUsernameRow{}, User{}, ErrInvalidCredentials
	}

	return user, User{Username: user.Username, Role: user.Role, Capabilities: user.Capabilities}, nil
}
