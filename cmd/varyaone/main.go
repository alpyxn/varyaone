package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	// Embeds the IANA timezone database into the binary. Windows has no system
	// zoneinfo files anywhere Go looks (unlike Linux's /usr/share/zoneinfo), so
	// without this, time.LoadLocation rejects every timezone name — including
	// valid ones — on a Windows install. That surfaces as "geçersiz saat dilimi"
	// on any company-settings save, regardless of which field actually changed.
	_ "time/tzdata"

	"github.com/alpyxn/varyaone/internal/backup"
	"github.com/alpyxn/varyaone/internal/demo"
	"github.com/alpyxn/varyaone/internal/platform/app"
	"github.com/alpyxn/varyaone/internal/platform/config"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/platform/desktop"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/alpyxn/varyaone/internal/platform/opctl"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Exit codes. Anything that automates this binary — deploy.sh above all — has
// to be able to tell "the operation failed and the installation is untouched"
// from "the operation failed part-way through and the installation is not in
// any known state". The second must never be followed by bringing services back
// up, so it gets its own code rather than being buried in a message.
const (
	exitFailure            = 1
	exitSystemInconsistent = 3
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "varyaone:", err)
		if errors.Is(err, backup.ErrSystemInconsistent) {
			os.Exit(exitSystemInconsistent)
		}
		os.Exit(exitFailure)
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
	case "demo":
		if len(os.Args) != 3 {
			return errors.New("usage: varyaone demo <seed|reset>")
		}
		return runDemo(ctx, cfg, logger, pool, os.Args[2])
	case "system":
		return runSystem(ctx, cfg, logger)
	case "migrate":
		if len(os.Args) != 3 {
			return errors.New("usage: varyaone migrate <up|status>")
		}
		switch os.Args[2] {
		case "up":
			// Migrations are a writer, and they run as a startup dependency of
			// the API and the worker. Gating them here is what makes the whole
			// stack fail closed after an interrupted restore: nothing that
			// depends on a completed migration can start either.
			controller, controlErr := opctl.New(cfg.ControlDir)
			if controlErr != nil {
				return controlErr
			}
			// VARYAONE_OPERATION_ID names the operation this container belongs
			// to. The deploy sets it so its own migration is not blocked by
			// its own record; nothing else sets it, so nothing else gets in.
			if gateErr := controller.GateStartupFor(os.Getenv("VARYAONE_OPERATION_ID")); gateErr != nil {
				return gateErr
			}
			return runner.Up(ctx)
		case "status":
			status, err := runner.Status(ctx)
			if err != nil {
				return err
			}
			fmt.Printf("current=%d latest=%d pending=%d unknown=%d\n",
				status.Current, status.Latest, status.Pending, status.Unknown)
			if status.Pending > 0 {
				return migrations.ErrPending
			}
			if status.Unknown > 0 {
				return migrations.ErrSchemaNewer
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
	return errors.New("usage: varyaone <server|worker|stack|service <ensure|repair|install|uninstall|start|stop|restart|status|wait-ready>|netmode <local|lan>|migrate up|migrate status|demo seed|demo reset|backup create <file>|backup restore <file>|backup verify <file>|system status|system history <id>|system resolve <not>>")
}

// runDemo builds or rebuilds the shared showcase company. It refuses to run
// unless the installation is explicitly configured as the demo deployment:
// "reset" purges a company, and that must never be reachable by mistake on an
// installation holding real data.
func runDemo(ctx context.Context, cfg config.Config, logger *slog.Logger, pool *pgxpool.Pool, command string) error {
	if !cfg.DemoConfigured() {
		return errors.New("demo commands require VARYAONE_DEMO_MODE=true; refusing to run on a normal installation")
	}
	runner := demo.New(pool, demo.Options{
		MaintenanceDSN: cfg.DatabaseURL,
		MasterKey:      cfg.MasterKey,
		Email:          cfg.DemoEmail,
		Password:       cfg.DemoPassword,
		Logger:         logger,
	})
	switch command {
	case "seed":
		return runner.Ensure(ctx)
	case "reset":
		return runner.Reset(ctx)
	default:
		return errors.New("usage: varyaone demo <seed|reset>")
	}
}

// runSystem exposes the operation coordinator to an operator. These commands
// are how a person finds out what an interrupted operation did and tells the
// installation it may come back into service.
func runSystem(ctx context.Context, cfg config.Config, _ *slog.Logger) error {
	_ = ctx
	controller, err := opctl.New(cfg.ControlDir)
	if err != nil {
		return err
	}
	if len(os.Args) < 3 {
		return errors.New("usage: varyaone system <status|history [id]|retained|prune [--dry-run]|promote <db>|begin <kind> [not]|phase <id> <aşama> [not]|hold <sebep>|resolve <not>>")
	}
	switch os.Args[2] {
	case "status":
		status, statusErr := controller.Status()
		if statusErr != nil {
			return statusErr
		}
		body, _ := json.MarshalIndent(map[string]any{
			"serviceable": status.Serviceable,
			"running":     status.Running,
			"reason":      status.Reason,
			"operation":   status.Operation,
		}, "", "  ")
		fmt.Println(string(body))
		// Exit non-zero when the installation must not serve traffic, so a
		// script can gate on this without parsing the output.
		if !status.Serviceable {
			return errors.New("kurulum hizmete hazır değil: " + status.Reason)
		}
		return nil

	case "history":
		id := ""
		if len(os.Args) > 3 {
			id = os.Args[3]
		} else {
			status, statusErr := controller.Status()
			if statusErr != nil {
				return statusErr
			}
			if status.Operation == nil {
				return errors.New("kayıtlı bir sistem işlemi yok")
			}
			id = status.Operation.ID
		}
		entries, historyErr := controller.History(id)
		if historyErr != nil {
			return historyErr
		}
		for _, entry := range entries {
			body, _ := json.Marshal(entry)
			fmt.Println(string(body))
		}
		return nil

	case "retained":
		engine, engineErr := newBackupEngine(cfg)
		if engineErr != nil {
			return engineErr
		}
		retained, listErr := engine.ListRetainedDatabases(ctx)
		if listErr != nil {
			return listErr
		}
		for _, database := range retained {
			fmt.Printf("%s\t%s\t%d MB\n", database.Name,
				database.RetiredAt.Format(time.RFC3339), database.Bytes/(1<<20))
		}
		if len(retained) == 0 {
			fmt.Println("saklanan eski veritabanı yok")
		}
		return nil

	case "prune":
		engine, engineErr := newBackupEngine(cfg)
		if engineErr != nil {
			return engineErr
		}
		dryRun := len(os.Args) > 3 && os.Args[3] == "--dry-run"
		dropped, pruneErr := engine.PruneRetainedDatabases(ctx, backup.DefaultRetention, dryRun)
		if pruneErr != nil {
			return pruneErr
		}
		for _, name := range dropped {
			if dryRun {
				fmt.Printf("silinecek: %s\n", name)
			} else {
				fmt.Printf("silindi: %s\n", name)
			}
		}
		if len(dropped) == 0 {
			fmt.Println("silinecek eski veritabanı yok")
		}
		return nil

	case "promote":
		if len(os.Args) < 4 {
			return errors.New("usage: varyaone system promote <veritabanı adı>")
		}
		engine, engineErr := newBackupEngine(cfg)
		if engineErr != nil {
			return engineErr
		}
		// Rolling back is a destructive whole-system operation like any other,
		// so it takes the lease and leaves a record.
		lease, leaseErr := controller.Acquire(ctx, opctl.KindRestore, opctl.AcquireOptions{
			Actor: "cli", Wait: backupLeaseWait,
		})
		if leaseErr != nil {
			return leaseErr
		}
		defer func() { _ = lease.Release() }()
		if err := lease.Switching(map[string]any{"promote": os.Args[3]}); err != nil {
			return err
		}
		if err := engine.PromoteRetainedDatabase(ctx, os.Args[3]); err != nil {
			_ = lease.Fail(opctl.PhaseRecoveryRequired, err)
			return err
		}
		_ = lease.Result(opctl.PhaseCommitted, map[string]any{"promoted": os.Args[3]})
		fmt.Printf("%s canlıya alındı; servisleri yeniden başlatın\n", os.Args[3])
		return nil

	// begin/phase/hold are how the deploy script records what it is doing. It
	// is a shell pipeline of short-lived containers, so it cannot hold the
	// lease across a deploy; it writes the same journal instead. See
	// opctl/external.go.
	case "begin":
		if len(os.Args) < 4 {
			return errors.New("usage: varyaone system begin <kind> [not]")
		}
		note := strings.TrimSpace(strings.Join(os.Args[4:], " "))
		record, beginErr := controller.Begin(opctl.Kind(os.Args[3]), "deploy",
			externalOperationLifetime, map[string]any{"note": note})
		if beginErr != nil {
			return beginErr
		}
		// The id is the only output: the caller captures it and passes it back
		// to every later command of the same operation.
		fmt.Println(record.ID)
		return nil

	case "phase":
		if len(os.Args) < 5 {
			return errors.New("usage: varyaone system phase <id> <aşama> [not]")
		}
		note := strings.TrimSpace(strings.Join(os.Args[5:], " "))
		return controller.Advance(os.Args[3], opctl.Phase(os.Args[4]),
			externalOperationLifetime, note, nil)

	case "hold":
		if len(os.Args) < 4 || strings.TrimSpace(strings.Join(os.Args[3:], " ")) == "" {
			return errors.New("usage: varyaone system hold <sebep>")
		}
		record, holdErr := controller.Hold(opctl.KindDeploy, "deploy",
			strings.Join(os.Args[3:], " "))
		if holdErr != nil {
			return holdErr
		}
		fmt.Printf("kurulum bakımda: %s (%s)\n", record.ID, record.Error)
		return nil

	case "resolve":
		if len(os.Args) < 4 || strings.TrimSpace(strings.Join(os.Args[3:], " ")) == "" {
			// The note is mandatory. An installation that was recovered by hand
			// should carry a record of what was done for as long as its journal
			// survives; "resolve" with no explanation is how the reason for a
			// past inconsistency gets lost.
			return errors.New("usage: varyaone system resolve <ne yapıldığını anlatan not>")
		}
		if err := controller.Resolve(strings.Join(os.Args[3:], " ")); err != nil {
			return err
		}
		fmt.Println("işlem çözüldü olarak işaretlendi")
		return nil

	default:
		return errors.New("usage: varyaone system <status|history [id]|retained|prune [--dry-run]|promote <db>|begin <kind> [not]|phase <id> <aşama> [not]|hold <sebep>|resolve <not>>")
	}
}

// newBackupEngine builds the engine from configuration. Both the backup
// commands and the system commands need one, and they must agree about every
// option — a retention command that resolved the database name differently
// from the restore that created those databases would prune the wrong things.
func newBackupEngine(cfg config.Config) (*backup.Engine, error) {
	return backup.NewEngine(backup.Options{
		DatabaseURL:     cfg.DatabaseURL,
		AppDatabaseURL:  cfg.AppDatabaseURL,
		StorageRoot:     cfg.StorageRoot,
		StorageProvider: cfg.StorageProvider,
		Release:         cfg.Release,
		MasterKey:       cfg.MasterKey,
		PostgresBinDir:  cfg.PostgresBinDir,
	})
}

func runBackup(ctx context.Context, cfg config.Config, _ *slog.Logger) error {
	// Backup logs go to stderr, never stdout. `backup create -` writes the
	// archive itself to stdout, and a JSON log line appended to that stream
	// lands inside the .varya file: trailing bytes after the tar that a strict
	// reader has every reason to reject.
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("service", "varyaone", "release", cfg.Release)

	if len(os.Args) < 4 {
		return backupUsage()
	}
	command, target := os.Args[2], os.Args[3]
	force := false
	mode := backup.ModeCandidate
	skipMigrations := false
	requireComplete := false
	for _, arg := range os.Args[4:] {
		switch arg {
		case "--force":
			force = true
		case "--in-place":
			// Explicit, and never chosen automatically: the in-place path
			// cannot roll back and cannot remove objects the archive does not
			// know about. An operator who does not ask for it does not get it.
			mode = backup.ModeInPlace
		case "--skip-migrations":
			skipMigrations = true
		case "--require-complete":
			// Used for the safety backup taken before a restore: that archive
			// is the rollback point, so a partial one must fail loudly rather
			// than be filed away as if it were whole.
			requireComplete = true
		default:
			return fmt.Errorf("bilinmeyen seçenek: %s", arg)
		}
	}
	if command != "restore" && (force || mode != backup.ModeCandidate || skipMigrations) {
		return fmt.Errorf("--force, --in-place ve --skip-migrations yalnızca restore ile kullanılabilir")
	}
	if requireComplete && command != "create" {
		return fmt.Errorf("--require-complete yalnızca create ile kullanılabilir")
	}

	engine, err := newBackupEngine(cfg)
	if err != nil {
		return err
	}
	// An interrupted restore leaves the previous files in a retired directory.
	// Say so on every entry: it is recovery data, the engine will not delete it,
	// and nothing else reports it.
	for _, dir := range engine.RetiredDirs() {
		logger.Warn("kurtarılmayı bekleyen dosyalar var", "dizin", dir)
	}

	controller, err := opctl.New(cfg.ControlDir)
	if err != nil {
		return err
	}

	switch command {
	case "inspect":
		if target == "-" {
			return errors.New("inspect bir dosya yolu gerektirir (stdin desteklenmez)")
		}
		report, inspectErr := engine.Inspect(ctx, target)
		if inspectErr != nil {
			return inspectErr
		}
		body, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(body))
		if !report.Usable() {
			return errors.New("bu arşivden geri yükleme denenmemeli")
		}
		return nil

	case "verify":
		manifest, verifyErr := runVerify(ctx, engine, target)
		if verifyErr != nil {
			return verifyErr
		}
		logger.Info("backup verified",
			"created_at", manifest.CreatedAt,
			"migration_version", manifest.MigrationVersion,
			"objects", len(manifest.Objects),
			"skipped_objects", len(manifest.SkippedObjects),
			"dump_bytes", manifest.DatabaseDumpSize)
		if len(manifest.SkippedObjects) > 0 {
			logger.Warn("yedek eksik: bazı dosyalar alınamamış", "skipped_objects", len(manifest.SkippedObjects))
		}
		return nil

	case "create":
		// The lease is the same one the API and the deploy script take, so a
		// CLI backup cannot run alongside an API restore — the two would clean
		// up each other's working directories inside the storage root.
		lease, leaseErr := controller.Acquire(ctx, opctl.KindBackup, opctl.AcquireOptions{
			Actor: "cli", Wait: backupLeaseWait,
		})
		if leaseErr != nil {
			return leaseErr
		}
		defer func() { _ = lease.Release() }()
		logger.Info("operation started", "operation", lease.ID(), "kind", "backup")

		var manifest backup.Manifest
		var createErr error
		if target == "-" {
			manifest, createErr = engine.WriteArchive(ctx, os.Stdout)
			if createErr == nil && requireComplete {
				// The bytes are already on the pipe, so this cannot un-write
				// them — but it makes the command exit non-zero, and the caller
				// that asked for a complete backup discards what it collected
				// rather than filing a partial archive as a rollback point.
				createErr = manifest.RequireComplete()
			}
		} else {
			manifest, createErr = engine.CreateFile(ctx, target, backup.PublishOptions{RequireComplete: requireComplete})
		}
		if createErr != nil {
			// A backup never modifies the installation, so this is always
			// FAILED_UNCHANGED — there is nothing to recover from.
			_ = lease.Fail(opctl.PhaseFailedUnchanged, createErr)
			return createErr
		}
		_ = lease.Result(opctl.PhaseCommitted, map[string]any{"objects": len(manifest.Objects), "target": target})
		logBackupCreated(logger, manifest)
		if target != "-" {
			logger.Info("backup published", "path", target)
		}
		return nil

	case "restore":
		lease, leaseErr := controller.Acquire(ctx, opctl.KindRestore, opctl.AcquireOptions{
			Actor: "cli", Wait: backupLeaseWait,
		})
		if leaseErr != nil {
			return leaseErr
		}
		defer func() { _ = lease.Release() }()
		logger.Info("operation started", "operation", lease.ID(), "kind", "restore")

		in := os.Stdin
		if target != "-" {
			file, openErr := os.Open(target)
			if openErr != nil {
				_ = lease.Fail(opctl.PhaseFailedUnchanged, openErr)
				return openErr
			}
			defer func() { _ = file.Close() }()
			in = file
		}
		manifest, restoreErr := engine.Restore(ctx, in, backup.RestoreOptions{
			Force: force, Mode: mode, SkipMigrations: skipMigrations, Journal: lease,
		})
		if restoreErr != nil {
			if errors.Is(restoreErr, backup.ErrSystemInconsistent) {
				// The database has already changed. Recording RECOVERY_REQUIRED
				// is what stops the API and the worker from starting on top of
				// it; saying "restore failed" alone would invite exactly that.
				_ = lease.Fail(opctl.PhaseRecoveryRequired, restoreErr)
				logger.Error("SİSTEM TUTARSIZ: veritabanı değişti, geçiş tamamlanamadı; servisleri açmayın",
					"operation", lease.ID(), "error", restoreErr)
			} else {
				_ = lease.Fail(opctl.PhaseFailedUnchanged, restoreErr)
			}
			return restoreErr
		}
		logger.Info("backup restored", "operation", lease.ID(), "created_at", manifest.CreatedAt,
			"migration_version", manifest.MigrationVersion, "objects", len(manifest.Objects))
		return nil

	default:
		return backupUsage()
	}
}

// backupLeaseWait is how long a CLI operation waits for a busy installation
// before giving up. Long enough to queue behind a scheduled backup, short
// enough that an interactive command does not appear to hang.
const backupLeaseWait = 2 * time.Minute

// externalOperationLifetime is how long a deploy may sit in one phase before
// its claim on the installation lapses. A migration on a large installation is
// the long pole; the deadline has to outlast it comfortably, because a claim
// that expires under a still-running migration would let a second operation in
// alongside it.
const externalOperationLifetime = opctl.DefaultExternalLifetime

func backupUsage() error {
	return errors.New("usage: varyaone backup <create|restore|verify|inspect> <file|-> [--force] [--in-place] [--skip-migrations] [--require-complete]")
}

func runVerify(ctx context.Context, engine *backup.Engine, target string) (backup.Manifest, error) {
	if target == "-" {
		return engine.Verify(ctx, os.Stdin)
	}
	return engine.VerifyFile(ctx, target)
}

func logBackupCreated(logger *slog.Logger, manifest backup.Manifest) {
	logger.Info("backup created",
		"migration_version", manifest.MigrationVersion,
		"objects", len(manifest.Objects),
		"skipped_objects", len(manifest.SkippedObjects),
		"dump_bytes", manifest.DatabaseDumpSize)
	if len(manifest.SkippedObjects) > 0 {
		// A skipped object is a file the archive does not contain. Reporting
		// this only in the manifest let an incomplete backup read as a clean one.
		logger.Warn("yedek eksik: bazı dosyalar alınamadı", "skipped_objects", len(manifest.SkippedObjects))
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
