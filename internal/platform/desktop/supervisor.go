package desktop

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/app"
	"github.com/alpyxn/varyaone/internal/platform/config"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/platform/httpapi"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/alpyxn/varyaone/internal/platform/spa"
	"github.com/alpyxn/varyaone/internal/update"
	"golang.org/x/sync/errgroup"
)

// DefaultHTTPPort is the LAN-facing port the desktop server listens on.
const DefaultHTTPPort = 8080

// Supervisor is the `varyaone stack` runtime: bundled PostgreSQL + migrations +
// API + worker + embedded SPA + mDNS, all in one process.
type Supervisor struct {
	Layout   Layout
	HTTPPort int
	Logger   *slog.Logger
}

// NewSupervisor builds a Supervisor with discovered defaults.
func NewSupervisor(logger *slog.Logger) *Supervisor {
	port := DefaultHTTPPort
	if v := os.Getenv("VARYAONE_HTTP_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}
	return &Supervisor{Layout: DiscoverLayout(), HTTPPort: port, Logger: logger}
}

// Run blocks until ctx is cancelled or a component fails.
func (s *Supervisor) Run(ctx context.Context) error {
	if err := s.Layout.EnsureDirs(); err != nil {
		return fmt.Errorf("prepare data directories: %w", err)
	}
	// Fail fast with a clear message if the HTTP port is taken — otherwise the
	// server goroutine dies deep in startup and the reason is easy to miss.
	if err := s.checkHTTPPortFree(); err != nil {
		return err
	}
	// Sweep any ".old-<ts>" binaries a previous Windows in-place update left behind
	// (they can only be deleted once the old process has fully exited). The
	// update-apply process that produced the newest one is usually still alive
	// during its health-check wait when we get here, so also retry for a while.
	cleanStaleReplacements(s.Layout.InstallDir)
	go func() {
		for i := 0; i < 10; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
				cleanStaleReplacements(s.Layout.InstallDir)
			}
		}
	}()

	pg, err := NewPostgres(s.Layout)
	if err != nil {
		return err
	}
	if err := pg.EnsureInitialized(ctx); err != nil {
		return fmt.Errorf("initialize database cluster: %w", err)
	}
	if err := pg.Start(ctx); err != nil {
		return fmt.Errorf("start database cluster: %w", err)
	}
	// Best-effort clean shutdown of the cluster when the stack exits.
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 30_000_000_000)
		defer cancel()
		if err := pg.Stop(stopCtx); err != nil {
			s.Logger.Warn("postgres stop failed", "error", err)
		}
	}()

	cfg, err := s.config(pg)
	if err != nil {
		return err
	}

	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	runner := migrations.New(pool)
	if err := runner.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	group, groupCtx := errgroup.WithContext(ctx)

	group.Go(func() error {
		return app.RunServer(groupCtx, cfg, s.Logger, pool, runner, spaOption()...)
	})
	group.Go(func() error {
		return app.RunWorker(groupCtx, cfg, s.Logger, pool, runner)
	})
	mode := s.Layout.NetworkMode()
	group.Go(func() error {
		if mode != NetLAN {
			s.Logger.Info("local-only network mode: mDNS advertising disabled")
			<-groupCtx.Done()
			return nil
		}
		adv, err := Advertise(s.HTTPPort)
		if err != nil {
			// Discovery is a convenience; never fail the stack over it.
			s.Logger.Warn("mDNS advertise failed", "error", err)
			<-groupCtx.Done()
			return nil
		}
		defer adv.Shutdown()
		s.Logger.Info("advertising on LAN", "service", ServiceType, "port", s.HTTPPort)
		<-groupCtx.Done()
		return nil
	})

	// Update applier: the desktop has no external agent (Linux uses systemd), so
	// the stack itself watches for an operator-queued apply and launches the
	// updater as a detached process that outlives the service stop it triggers.
	group.Go(func() error {
		s.runUpdateApplier(groupCtx, update.NewService(pool, cfg))
		return nil
	})

	if mode == NetLAN {
		s.Logger.Info("varya one desktop stack ready", "mode", mode, "urls", LANURLs(s.HTTPPort))
	} else {
		s.Logger.Info("varya one desktop stack ready", "mode", mode, "url", fmt.Sprintf("http://127.0.0.1:%d", s.HTTPPort))
	}
	return group.Wait()
}

// checkHTTPPortFree probes the configured bind address so a port already in use
// surfaces as "8080 kullanımda" rather than an opaque late failure. Best-effort:
// a transient bind between here and the real listen is acceptable.
func (s *Supervisor) checkHTTPPortFree() error {
	addr := s.Layout.NetworkMode().BindHost() + ":" + strconv.Itoa(s.HTTPPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("HTTP portu %d kullanımda — başka bir uygulama mı çalışıyor? (%s)", s.HTTPPort, addr)
	}
	return ln.Close()
}

// updateApplierPoll is how often the stack checks for an operator-queued apply.
const updateApplierPoll = 2 * time.Minute

// runUpdateApplier watches the update state machine. When the operator queues an
// apply from the UI it launches `varyaone update-apply` detached — that process
// stops this service, swaps the binaries and starts the service again, so it
// must not be a child tied to our context.
func (s *Supervisor) runUpdateApplier(ctx context.Context, svc *update.Service) {
	if !svc.Configured() {
		<-ctx.Done()
		return
	}
	var lastSpawn time.Time
	tick := func() {
		// A spawned updater holds this lock for the whole run; don't stack a
		// second one on top, and back off after a recent attempt so a crashed
		// updater that left the state mid-flight can't become a spin loop.
		if fileExists(filepath.Join(s.Layout.Home, "update.lock")) {
			return
		}
		if time.Since(lastSpawn) < 15*time.Minute {
			return
		}
		act, err := svc.NextAction(ctx)
		if err != nil {
			if ctx.Err() == nil {
				s.Logger.Warn("update NextAction failed", "error", err)
			}
			return
		}
		if act.Action != "apply" {
			return
		}
		s.Logger.Info("update queued — launching detached updater", "target", act.TargetVersion)
		lastSpawn = time.Now()
		if err := spawnDetachedUpdater(s.Layout.InstallDir, act.TargetVersion); err != nil {
			s.Logger.Error("could not launch updater", "error", err)
			_ = svc.RecordResult(ctx, update.ResultInput{
				OK: false, FromVersion: act.FromVersion, ToVersion: act.TargetVersion,
				Error: "updater başlatılamadı: " + err.Error(),
			})
		}
	}

	t := time.NewTicker(updateApplierPoll)
	defer t.Stop()
	tick() // catch a request queued while the service was down
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}

// config overlays desktop-managed values on top of the process environment and
// runs the shared loader (so validation, master-key handling, etc. stay in one
// place).
func (s *Supervisor) config(pg *Postgres) (config.Config, error) {
	dbURL, err := pg.DatabaseURL()
	if err != nil {
		return config.Config{}, err
	}
	masterKey, err := s.masterKey()
	if err != nil {
		return config.Config{}, err
	}
	// Optional <Home>/settings.env overrides (self-hosters point the updater at
	// their own catalog); process env still wins over the file.
	file := settingsEnv(s.Layout)
	fileOrEnv := func(key, def string) string {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
		if v := strings.TrimSpace(file[key]); v != "" {
			return v
		}
		return def
	}

	overrides := map[string]string{
		"VARYAONE_ENV":              valueOr(os.Getenv("VARYAONE_ENV"), "production"),
		"VARYAONE_HTTP_ADDR":        s.Layout.NetworkMode().BindHost() + ":" + strconv.Itoa(s.HTTPPort),
		"VARYAONE_DATABASE_URL":     dbURL,
		"VARYAONE_MASTER_KEY":       masterKey,
		"VARYAONE_STORAGE_ROOT":     s.Layout.Storage(),
		"VARYAONE_RELEASE":          valueOr(os.Getenv("VARYAONE_RELEASE"), readReleaseFile(s.Layout)),
		"VARYAONE_APP_DATABASE_URL": "", // single bundled role; RLS role split is a later step
		// Update checks: default to the official varya-pulse catalog so a plain
		// install sees new releases without any configuration.
		"VARYAONE_PULSE_ENDPOINT":   fileOrEnv("VARYAONE_PULSE_ENDPOINT", defaultPulseEndpoint),
		"VARYAONE_PULSE_INGEST_KEY": fileOrEnv("VARYAONE_PULSE_INGEST_KEY", defaultPulseIngestKey),
	}
	getenv := func(key string) string {
		if v, ok := overrides[key]; ok {
			return v
		}
		if v, ok := file[key]; ok {
			return v
		}
		return os.Getenv(key)
	}
	return config.Load(getenv)
}

// masterKey reads or generates the persistent encryption master key.
func (s *Supervisor) masterKey() (string, error) {
	if v := os.Getenv("VARYAONE_MASTER_KEY"); v != "" {
		return v, nil
	}
	path := filepath.Join(s.Layout.Home, "master.key")
	if b, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	key := base64.StdEncoding.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(key), 0o600); err != nil {
		return "", err
	}
	return key, nil
}

func spaOption() []httpapi.RouterOption {
	if !spa.Enabled() {
		return nil
	}
	return []httpapi.RouterOption{httpapi.WithSPA(spa.Handler())}
}

func readReleaseFile(l Layout) string {
	if b, err := os.ReadFile(filepath.Join(l.InstallDir, "RELEASE")); err == nil {
		if v := strings.TrimSpace(string(b)); v != "" {
			return v
		}
	}
	return "desktop"
}

func valueOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
