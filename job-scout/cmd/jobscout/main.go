package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jobscout/jobscout/internal/api"
	"github.com/jobscout/jobscout/internal/config"
	"github.com/jobscout/jobscout/internal/db"
	"github.com/jobscout/jobscout/internal/pipeline"
	"github.com/jobscout/jobscout/internal/scraper"
)

func main() {
	mode := "api"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	cfg := config.Load()
	configureLogging(cfg.Debug)

	switch mode {
	case "api":
		runAPI(cfg)
	case "worker":
		runWorker(cfg)
	default:
		slog.Error("unknown mode (expected 'api' or 'worker')", "mode", mode)
		os.Exit(1)
	}
}

func configureLogging(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})))
}

func runAPI(cfg config.Config) {
	ctx := context.Background()

	database, err := db.Connect(ctx, cfg.DatabaseURL())
	if err != nil {
		fatal("connect db", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		fatal("migrate", err)
	}
	if err := db.SeedSettings(ctx, database); err != nil {
		fatal("seed settings", err)
	}

	temporalClient, err := pipeline.Dial(ctx, cfg.TemporalAddress)
	if err != nil {
		fatal("dial temporal", err)
	}
	defer temporalClient.Close()

	handler := &api.Handler{
		DB:        database,
		Temporal:  temporalClient,
		Schedules: temporalClient.ScheduleClient(),
		Scraper:   scraper.NewServiceWithConfig(database, cfg),
		Cfg:       cfg,
	}
	if err := handler.InstallSchedules(ctx); err != nil {
		fatal("install schedules", err)
	}
	// The next scrape, sync scrape, or re-evaluate starts the dispatchers again
	// if this boot attempt fails, so a failure must not stop the API.
	if err := handler.StartDispatchers(ctx); err != nil {
		slog.Error("start filter/process-pending dispatchers failed", "err", err)
	}

	srv := api.NewServer(":"+cfg.APIPort, handler)

	go func() {
		slog.Info("starting api server", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err.Error() != "http: Server closed" {
			fatal("http server", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	slog.Info("shutting down api server")
	_ = srv.Shutdown(context.Background())
}

func runWorker(cfg config.Config) {
	ctx := context.Background()

	database, err := db.Connect(ctx, cfg.DatabaseURL())
	if err != nil {
		fatal("connect db", err)
	}
	defer database.Close()

	temporalClient, err := pipeline.Dial(ctx, cfg.TemporalAddress)
	if err != nil {
		fatal("dial temporal", err)
	}
	defer temporalClient.Close()

	acts := &pipeline.Activities{
		Scraper:                   scraper.NewServiceWithConfig(database, cfg),
		DB:                        database,
		Webhook:                   pipeline.NewWebhookClient(cfg),
		Temporal:                  temporalClient,
		NotifyMaxJobs:             cfg.NotifyMaxJobs,
		NotifyClaimTimeoutSeconds: cfg.NotifyClaimTimeoutSeconds,
		NotificationsConfigured:   cfg.NotificationsConfigured(),
	}

	slog.Info("starting temporal workers",
		"main", config.TaskQueue,
		"apify", config.ApifyScrapeTaskQueue,
		"linkedin", config.LinkedInScrapeTaskQueue,
	)
	if err := pipeline.RunWorker(temporalClient, acts); err != nil {
		fatal("run worker", err)
	}
}

func fatal(what string, err error) {
	slog.Error("fatal", "what", what, "err", err)
	os.Exit(1)
}
