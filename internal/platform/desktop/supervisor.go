package desktop

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
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

	if mode == NetLAN {
		s.Logger.Info("varya one desktop stack ready", "mode", mode, "urls", LANURLs(s.HTTPPort))
	} else {
		s.Logger.Info("varya one desktop stack ready", "mode", mode, "url", fmt.Sprintf("http://127.0.0.1:%d", s.HTTPPort))
	}
	return group.Wait()
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
	overrides := map[string]string{
		"VARYAONE_ENV":              valueOr(os.Getenv("VARYAONE_ENV"), "production"),
		"VARYAONE_HTTP_ADDR":        s.Layout.NetworkMode().BindHost() + ":" + strconv.Itoa(s.HTTPPort),
		"VARYAONE_DATABASE_URL":     dbURL,
		"VARYAONE_MASTER_KEY":       masterKey,
		"VARYAONE_STORAGE_ROOT":     s.Layout.Storage(),
		"VARYAONE_RELEASE":          valueOr(os.Getenv("VARYAONE_RELEASE"), readReleaseFile(s.Layout)),
		"VARYAONE_APP_DATABASE_URL": "", // single bundled role; RLS role split is a later step
	}
	getenv := func(key string) string {
		if v, ok := overrides[key]; ok {
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
