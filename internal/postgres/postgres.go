package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	dbmigrations "github.com/pbsladek/nexus-lens/db/migrations"
)

const migrationLockID int64 = 56010001

type Database struct {
	Pool *pgxpool.Pool
}

func Open(ctx context.Context, cfg Config, logger *slog.Logger) (*Database, error) {
	connString, err := cfg.ConnString()
	if err != nil {
		return nil, err
	}

	poolCfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}
	poolCfg.MaxConns = 10
	poolCfg.MinConns = 0
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	if err := runMigrations(ctx, connString, logger); err != nil {
		pool.Close()
		return nil, err
	}

	return &Database{Pool: pool}, nil
}

func (d *Database) Close() {
	if d != nil && d.Pool != nil {
		d.Pool.Close()
	}
}

func (d *Database) Ping(ctx context.Context) error {
	if d == nil || d.Pool == nil {
		return fmt.Errorf("database is not configured")
	}
	return d.Pool.Ping(ctx)
}

func runMigrations(ctx context.Context, connString string, logger *slog.Logger) error {
	connCfg, err := pgx.ParseConfig(connString)
	if err != nil {
		return fmt.Errorf("parse migration postgres config: %w", err)
	}

	db := stdlib.OpenDB(*connCfg)
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping migration database: %w", err)
	}

	if err := withAdvisoryLock(ctx, db, func() error {
		goose.SetBaseFS(dbmigrations.FS)
		goose.SetLogger(gooseSlogLogger{logger: logger})
		if err := goose.SetDialect("postgres"); err != nil {
			return fmt.Errorf("set goose dialect: %w", err)
		}
		if err := goose.UpContext(ctx, db, "."); err != nil {
			return fmt.Errorf("run migrations: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	logger.Info("postgres migrations complete")
	return nil
}

func withAdvisoryLock(ctx context.Context, db *sql.DB, fn func() error) error {
	if _, err := db.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", migrationLockID)
	}()

	return fn()
}

type gooseSlogLogger struct {
	logger *slog.Logger
}

func (l gooseSlogLogger) Printf(format string, args ...any) {
	l.logger.Info("goose", "message", fmt.Sprintf(format, args...))
}

func (l gooseSlogLogger) Fatalf(format string, args ...any) {
	l.logger.Error("goose fatal", "message", fmt.Sprintf(format, args...))
}
