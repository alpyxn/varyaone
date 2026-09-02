package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alpyxn/varyaone/internal/exchange"
	"github.com/alpyxn/varyaone/internal/platform/config"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/alpyxn/varyaone/internal/platform/outbox"
	"github.com/alpyxn/varyaone/internal/pulse"
	"github.com/alpyxn/varyaone/internal/update"
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
		logger.Info("pulse enabled", "endpoint", cfg.PulseEndpoint, "usage_summary", cfg.PulseEnabled, "install_ping", cfg.PulseInstallPing)
		go runPulseScheduler(ctx, logger, cfg, pulse.NewService(pool, cfg))
		go runUpdateScheduler(ctx, logger, update.NewService(pool, cfg))
	}
	if err := outbox.New(pool, logger).Run(ctx); err != nil {
		return fmt.Errorf("run outbox worker: %w", err)
	}
	logger.Info("worker stopped")
	return nil
}

func runPulseScheduler(ctx context.Context, logger *slog.Logger, cfg config.Config, service *pulse.Service) {
	installDone := !cfg.PulseInstallPing

	tick := func() {
		if !installDone {
			done, err := service.AnnounceInstall(ctx)
			switch {
			case err != nil && ctx.Err() == nil:
				logger.Warn("pulse install announce failed", "error", err)
			case done:
				installDone = true
			}
		}
		if cfg.PulseEnabled {
			if err := service.RunDue(ctx); err != nil && ctx.Err() == nil {
				logger.Warn("pulse usage-summary cycle failed", "error", err)
			}
		}
	}

	tick()
	// Nothing recurring is left once the install ping has landed and the daily
	// usage summary is off.
	if installDone && !cfg.PulseEnabled {
		return
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
			if installDone && !cfg.PulseEnabled {
				return
			}
		}
	}
}

func runUpdateScheduler(ctx context.Context, logger *slog.Logger, service *update.Service) {
	check := func() {
		if err := service.CheckDue(ctx); err != nil && ctx.Err() == nil {
			logger.Warn("update check cycle failed", "error", err)
		}
	}
	check()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
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
