package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alpyxn/varyaone/internal/demo"
	"github.com/alpyxn/varyaone/internal/exchange"
	"github.com/alpyxn/varyaone/internal/platform/config"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/alpyxn/varyaone/internal/platform/outbox"
	"github.com/alpyxn/varyaone/internal/pulse"
	"github.com/jackc/pgx/v5/pgxpool"
)

func RunWorker(ctx context.Context, cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool, runner *migrations.Runner) error {
	startupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := (readiness{pool: pool, migrations: runner}).Check(startupCtx); err != nil {
		return fmt.Errorf("worker startup check: %w", err)
	}
	logger.Info("worker started", "mode", "postgres-outbox")
	exchangeService := exchange.NewService(pool)
	go runExchangeScheduler(ctx, logger, exchangeService)
	if cfg.PulseConfigured() {
		logger.Info("pulse enabled", "endpoint", cfg.PulseEndpoint, "install_ping", cfg.PulseInstallPing)
		go runPulseScheduler(ctx, logger, cfg, pulse.NewService(pool, cfg))
	}
	// The demo deployment rebuilds its single shared company on a timer; a
	// normal installation never starts this scheduler.
	if cfg.DemoConfigured() {
		demoRunner := newDemoRunner(cfg, logger, pool)
		logger.Info("demo mode enabled", "reset_interval", cfg.DemoResetInterval)
		go runDemoResetScheduler(ctx, logger, demoRunner)
	}
	if err := outbox.New(pool, logger).Run(ctx); err != nil {
		return fmt.Errorf("run outbox worker: %w", err)
	}
	logger.Info("worker stopped")
	return nil
}

// runDemoResetScheduler keeps the shared demo company fresh. It polls the due
// time recorded in demo_state rather than holding its own timer, so a worker
// restart cannot postpone a reset and two workers cannot both decide it is time
// (the reset itself also takes a cluster-wide lock).
func runDemoResetScheduler(ctx context.Context, logger *slog.Logger, runner *demo.Runner) {
	// The demo company must exist before anyone can visit, so build it once at
	// start-up if it is missing. This is what makes a fresh demo deployment
	// usable with no manual seeding step.
	if err := runner.Ensure(ctx); err != nil && ctx.Err() == nil {
		logger.Error("demo seeding failed", "error", err)
	}
	reset := func() {
		due, err := runner.DueForReset(ctx)
		if err != nil {
			if ctx.Err() == nil {
				logger.Warn("demo reset check failed", "error", err)
			}
			return
		}
		if !due {
			return
		}
		switch err = runner.Reset(ctx); {
		case errors.Is(err, demo.ErrResetInProgress):
			// Another process got there first; nothing to do.
		case err != nil && ctx.Err() == nil:
			logger.Error("demo reset failed", "error", err)
		case err == nil:
			logger.Info("demo company reset")
		}
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reset()
		}
	}
}

// runPulseScheduler lands the one-off anonymous install ping, retrying hourly
// until it succeeds (setup may not be finished, or the collector may be
// unreachable) and then stopping for good. There is no recurring telemetry:
// feedback is the only other thing this instance ever sends, and that is
// user-initiated.
func runPulseScheduler(ctx context.Context, logger *slog.Logger, cfg config.Config, service *pulse.Service) {
	if !cfg.PulseInstallPing {
		return
	}

	announce := func() bool {
		done, err := service.AnnounceInstall(ctx)
		if err != nil && ctx.Err() == nil {
			logger.Warn("pulse install announce failed", "error", err)
		}
		return done
	}

	if announce() {
		return
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if announce() {
				return
			}
		}
	}
}

func runExchangeScheduler(ctx context.Context, logger *slog.Logger, service *exchange.Service) {
	refresh := func() {
		if err := service.RefreshDue(ctx); err != nil && ctx.Err() == nil {
			logger.Warn("exchange-rate refresh cycle failed", "error", err)
		}
	}
	// Reconcile on startup so a missing/stale rate is not left until the next
	// six-hour boundary after a worker restart. The short cadence only checks
	// due state; providers are contacted only when a company is actually due.
	refresh()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}

// newDemoRunner builds the demo runner from configuration. Both the worker's
// reset timer and the API's visitor-triggered reset go through the same
// construction, so they agree on the schedule and the cooldown.
func newDemoRunner(cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool) *demo.Runner {
	return demo.New(pool, demo.Options{
		MaintenanceDSN: cfg.DatabaseURL,
		MasterKey:      cfg.MasterKey,
		Email:          cfg.DemoEmail,
		Password:       cfg.DemoPassword,
		Logger:         logger,
		ResetInterval:  cfg.DemoResetInterval,
		ResetCooldown:  demoResetCooldown,
	})
}

// demoResetCooldown is the shortest gap between two visitor-triggered resets.
// Long enough that the button cannot be used to keep the demo permanently
// empty, short enough that whoever finds a mess can clean it up themselves.
const demoResetCooldown = 5 * time.Minute
