package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/alpyxn/varyaone/internal/backup"
	"github.com/alpyxn/varyaone/internal/platform/app"
	"github.com/alpyxn/varyaone/internal/platform/config"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/platform/desktop"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "varyaone:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return usageError()
	}

	// Desktop subcommands manage their own bundled PostgreSQL and configuration,
	// so they run before the shared config/database bootstrap below.
	switch os.Args[1] {
	case "stack":
		return desktop.RunAsService(desktop.NewStackLogger())
	case "service":
		if len(os.Args) != 3 {
			return errors.New("usage: varyaone service <ensure|repair|install|uninstall|start|stop|restart|status|wait-ready>")
		}
		return desktop.Control(os.Args[2])
	case "update-apply":
		target := ""
		for i := 2; i < len(os.Args)-1; i++ {
			if os.Args[i] == "--target" {
				target = os.Args[i+1]
			}
		}
		// Cancel a stuck update on Ctrl+C / service-manager stop instead of
		// hanging forever (download and health-check both honour the context).
		applyCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return desktop.NewUpdater(desktop.NewUpdateLogger()).
			Apply(applyCtx, target)
	case "netmode":
		if len(os.Args) != 3 {
			return errors.New("usage: varyaone netmode <local|lan>")
		}
		mode := desktop.NetMode(os.Args[2])
		if mode != desktop.NetLocal && mode != desktop.NetLAN {
			return errors.New("usage: varyaone netmode <local|lan>")
		}
		return desktop.ApplyNetworkMode(mode, slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	}

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	logger := newLogger(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	runner := migrations.New(pool)

	// Migrations run as the owning connection; the server and worker serve
	// traffic through ServingDatabaseURL, which is the non-superuser varyaone_app
	// role when VARYAONE_APP_DATABASE_URL is set (otherwise the same pool).
	servingPool := pool
	if cfg.ServingDatabaseURL() != cfg.DatabaseURL {
		servingPool, err = database.OpenServing(ctx, cfg.ServingDatabaseURL())
		if err != nil {
			return fmt.Errorf("open serving database: %w", err)
		}
		defer servingPool.Close()
	}

	switch os.Args[1] {
	case "backup":
		return runBackup(ctx, cfg, logger)
	case "server":
		return app.RunServer(ctx, cfg, logger, servingPool, runner)
	case "worker":
		return app.RunWorker(ctx, cfg, logger, servingPool, runner)
	case "migrate":
		if len(os.Args) != 3 {
			return errors.New("usage: varyaone migrate <up|status>")
		}
		switch os.Args[2] {
		case "up":
			return runner.Up(ctx)
		case "status":
			status, err := runner.Status(ctx)
			if err != nil {
				return err
			}
			fmt.Printf("current=%d latest=%d pending=%d\n", status.Current, status.Latest, status.Pending)
			if status.Pending > 0 {
				return migrations.ErrPending
			}
			return nil
		default:
			return errors.New("usage: varyaone migrate <up|status>")
		}
	default:
		return usageError()
	}
}

func usageError() error {
	return errors.New("usage: varyaone <server|worker|stack|service <ensure|repair|install|uninstall|start|stop|restart|status|wait-ready>|netmode <local|lan>|update-apply [--target v]|migrate up|migrate status|backup create <file>|backup restore <file>|backup verify <file>>")
}

func runBackup(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if len(os.Args) < 4 {
		return errors.New("usage: varyaone backup <create|restore|verify> <file|-> [--force]")
	}
	engine, err := backup.NewEngine(backup.Options{
		DatabaseURL:    cfg.DatabaseURL,
		StorageRoot:    cfg.StorageRoot,
		Release:        cfg.Release,
		MasterKey:      cfg.MasterKey,
		PostgresBinDir: cfg.PostgresBinDir,
	})
	if err != nil {
		return err
	}
	target := os.Args[3]
	switch os.Args[2] {
	case "verify":
		in := os.Stdin
		if target != "-" {
			file, openErr := os.Open(target)
			if openErr != nil {
				return openErr
			}
			defer file.Close()
			in = file
		}
		manifest, verifyErr := engine.Verify(ctx, in)
		if verifyErr != nil {
			return verifyErr
		}
		logger.Info("backup verified",
			"created_at", manifest.CreatedAt,
			"migration_version", manifest.MigrationVersion,
			"objects", len(manifest.Objects),
			"skipped_objects", len(manifest.SkippedObjects),
			"dump_bytes", manifest.DatabaseDumpSize)
		return nil
	case "create":
		out := os.Stdout
		if target != "-" {
			file, createErr := os.Create(target)
			if createErr != nil {
				return createErr
			}
			defer file.Close()
			out = file
		}
		manifest, createErr := engine.Create(ctx, out)
		if createErr != nil {
			return createErr
		}
		logger.Info("backup created", "migration_version", manifest.MigrationVersion, "objects", len(manifest.Objects), "dump_bytes", manifest.DatabaseDumpSize)
		return nil
	case "restore":
		force := len(os.Args) > 4 && os.Args[4] == "--force"
		in := os.Stdin
		if target != "-" {
			file, openErr := os.Open(target)
			if openErr != nil {
				return openErr
			}
			defer file.Close()
			in = file
		}
		manifest, restoreErr := engine.Restore(ctx, in, backup.RestoreOptions{Force: force})
		if restoreErr != nil {
			return restoreErr
		}
		logger.Info("backup restored", "created_at", manifest.CreatedAt, "migration_version", manifest.MigrationVersion, "objects", len(manifest.Objects))
		return nil
	default:
		return errors.New("usage: varyaone backup <create|restore|verify> <file|-> [--force]")
	}
}

func newLogger(cfg config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})).With(
		"service", "varyaone",
		"environment", cfg.Environment,
		"release", cfg.Release,
	)
}
