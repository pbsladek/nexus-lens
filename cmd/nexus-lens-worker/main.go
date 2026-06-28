package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/pbsladek/nexus-lens/internal/db"
	"github.com/pbsladek/nexus-lens/internal/indexing"
	"github.com/pbsladek/nexus-lens/internal/postgres"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pgCfg := postgres.FromEnv()
	if pgCfg.Enabled() {
		logger.Info("postgres configured", "postgres", pgCfg.Redacted())
		startupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		database, err := postgres.Open(startupCtx, pgCfg, logger)
		cancel()
		if err != nil {
			logger.Error("postgres startup failed", "error", err)
			os.Exit(1)
		}
		defer database.Close()
		queries := db.New(database.Pool)
		indexingService := indexing.NewService(queries, logger)
		if os.Getenv("NEXUS_LENS_SEED_RECENT_SYNC_SCHEDULE") == "true" {
			scheduleCtx, scheduleCancel := context.WithTimeout(ctx, 10*time.Second)
			if _, err := indexingService.UpsertSchedule(scheduleCtx, indexing.ScheduleRequest{
				NexusName:       envOrDefault("NEXUS_LENS_SCHEDULE_NEXUS_NAME", "local"),
				BaseURL:         envOrDefault("NEXUS_LENS_SCHEDULE_NEXUS_URL", "http://nexus:8081"),
				Name:            envOrDefault("NEXUS_LENS_SCHEDULE_NAME", "recent-sync"),
				ScanType:        indexing.ScanTypeRecentSync,
				IntervalSeconds: envInt32OrDefault("NEXUS_LENS_SCHEDULE_INTERVAL_SECONDS", 1800),
				Enabled:         true,
				DueNow:          os.Getenv("NEXUS_LENS_SCHEDULE_DUE_NOW") == "true",
			}); err != nil {
				scheduleCancel()
				logger.Error("seed recent sync schedule failed", "error", err)
				os.Exit(1)
			}
			scheduleCancel()
		}
		if os.Getenv("NEXUS_LENS_ENQUEUE_DISCOVERY_ON_START") == "true" || os.Getenv("NEXUS_LENS_ENQUEUE_NOOP_ON_START") == "true" {
			enqueueCtx, enqueueCancel := context.WithTimeout(ctx, 10*time.Second)
			if _, err := indexingService.EnqueueRepositoryDiscoveryRun(enqueueCtx, indexing.EnqueueRepositoryDiscoveryRunRequest{
				NexusName: envOrDefault("NEXUS_LENS_DISCOVERY_NEXUS_NAME", envOrDefault("NEXUS_LENS_NOOP_NEXUS_NAME", "local")),
				BaseURL:   envOrDefault("NEXUS_LENS_DISCOVERY_NEXUS_URL", envOrDefault("NEXUS_LENS_NOOP_NEXUS_URL", "http://nexus:8081")),
				ScanType:  indexing.ScanTypeRecentSync,
				TaskKey:   "startup-repository-discovery",
			}); err != nil {
				enqueueCancel()
				logger.Error("enqueue startup repository discovery run failed", "error", err)
				os.Exit(1)
			}
			enqueueCancel()
		}

		worker := indexing.NewWorker(
			queries,
			logger,
			indexing.WithWorkerPodName(os.Getenv("HOSTNAME")),
		)
		logger.Info("worker started")
		if err := worker.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error("worker stopped with error", "error", err)
			os.Exit(1)
		}
	} else {
		logger.Warn("postgres not configured; worker will idle without database")
		<-ctx.Done()
	}

	logger.Info("worker stopped")
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt32OrDefault(key string, fallback int32) int32 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return int32(parsed)
}
