package desktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Postgres manages a bundled PostgreSQL cluster local to this install: it runs
// initdb on first boot, starts/stops via pg_ctl, and hands back a connection
// URL. It never opens the cluster beyond 127.0.0.1.
type Postgres struct {
	layout Layout
	bin    string // directory holding initdb / pg_ctl / postgres
	port   int
	dbName string
	dbUser string
}

const (
	defaultPGPort = 5433 // avoid colliding with a system PostgreSQL on 5432
	pgDBName      = "varyaone"
	pgDBUser      = "varyaone"
)

// NewPostgres locates the PostgreSQL binaries (bundled under <install>/pgsql/bin,
// else on PATH).
func NewPostgres(layout Layout) (*Postgres, error) {
	bundled := filepath.Join(layout.InstallDir, "pgsql", "bin")
	bin := ""
	if fileExists(filepath.Join(bundled, exe("pg_ctl"))) {
		bin = bundled
	} else if p, err := exec.LookPath("pg_ctl"); err == nil {
		bin = filepath.Dir(p)
	} else {
		return nil, fmt.Errorf("bundled PostgreSQL not found under %s and pg_ctl is not on PATH", bundled)
	}
	port := defaultPGPort
	if v := os.Getenv("VARYAONE_DESKTOP_PG_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			port = n
		}
	}
	return &Postgres{layout: layout, bin: bin, port: port, dbName: pgDBName, dbUser: pgDBUser}, nil
}

func (p *Postgres) tool(name string) string { return filepath.Join(p.bin, exe(name)) }

// DatabaseURL is the superuser/owner connection string for this cluster.
func (p *Postgres) DatabaseURL() (string, error) {
	pw, err := p.password()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable",
		p.dbUser, pw, p.port, p.dbName), nil
}

// DataDirMajor reads the PostgreSQL major version that initialised the data
// directory (the single integer in <pgdata>/PG_VERSION). Returns 0 when the data
// directory does not exist yet.
func (p *Postgres) DataDirMajor() (int, error) {
	b, err := os.ReadFile(filepath.Join(p.layout.PGData(), "PG_VERSION"))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	major, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("parse PG_VERSION %q: %w", strings.TrimSpace(string(b)), err)
	}
	return major, nil
}

// BinariesMajor reports the major version of the bundled (or PATH) PostgreSQL
// binaries this Postgres will run — e.g. `pg_ctl (PostgreSQL) 19.1` -> 19.
func (p *Postgres) BinariesMajor() (int, error) {
	out, err := exec.Command(p.tool("pg_ctl"), "--version").Output()
	if err != nil {
		return 0, fmt.Errorf("pg_ctl --version: %w", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, fmt.Errorf("pg_ctl --version: empty output")
	}
	last := fields[len(fields)-1] // "19.1" or "19beta1" or "19"
	num := strings.FieldsFunc(last, func(r rune) bool { return r < '0' || r > '9' })
	if len(num) == 0 {
		return 0, fmt.Errorf("pg_ctl --version: cannot parse %q", last)
	}
	return strconv.Atoi(num[0])
}

// ArchiveDataDir renames the current data directory aside (cluster must already
// be stopped) and returns the archived path, so a failed major upgrade can put
// the exact pre-upgrade cluster back with RestoreArchivedDataDir.
func (p *Postgres) ArchiveDataDir(fromMajor int) (string, error) {
	src := p.layout.PGData()
	archived := fmt.Sprintf("%s.pg%d-%s", src, fromMajor, time.Now().UTC().Format("20060102T150405Z"))
	if err := os.Rename(src, archived); err != nil {
		return "", fmt.Errorf("archive data directory: %w", err)
	}
	return archived, nil
}

// RestoreArchivedDataDir discards whatever is at the data directory now and moves
// the archived pre-upgrade cluster back into place.
func (p *Postgres) RestoreArchivedDataDir(archived string) error {
	dst := p.layout.PGData()
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return os.Rename(archived, dst)
}

// EnsureInitialized runs initdb if the data directory is empty.
func (p *Postgres) EnsureInitialized(ctx context.Context) error {
	if fileExists(filepath.Join(p.layout.PGData(), "PG_VERSION")) {
		return nil
	}
	if err := os.MkdirAll(p.layout.PGData(), 0o700); err != nil {
		return err
	}
	pw, err := p.password()
	if err != nil {
		return err
	}
	pwFile := filepath.Join(p.layout.Home, "pg_initpw.tmp")
	if err := os.WriteFile(pwFile, []byte(pw), 0o600); err != nil {
		return err
	}
	defer os.Remove(pwFile)

	cmd := exec.CommandContext(ctx, p.tool("initdb"),
		"--pgdata="+p.layout.PGData(),
		"--username="+p.dbUser,
		"--auth=scram-sha-256",
		"--pwfile="+pwFile,
		"--encoding=UTF8",
		"--no-locale",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("initdb: %w: %s", err, out)
	}
	return nil
}

// Start boots the cluster (idempotent) and waits until it accepts connections,
// then ensures the application database exists.
func (p *Postgres) Start(ctx context.Context) error {
	if p.running() {
		return p.ensureDatabase(ctx)
	}
	logFile := filepath.Join(p.layout.Logs(), "postgres.log")
	opts := fmt.Sprintf("-p %d -c listen_addresses=127.0.0.1 -c unix_socket_directories=", p.port)
	cmd := exec.CommandContext(ctx, p.tool("pg_ctl"),
		"start", "-w", "-t", "60",
		"--pgdata="+p.layout.PGData(),
		"--log="+logFile,
		"--options="+opts,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pg_ctl start: %w: %s", err, out)
	}
	if err := p.waitReady(ctx, 60*time.Second); err != nil {
		return err
	}
	return p.ensureDatabase(ctx)
}

// Stop shuts the cluster down (fast mode). Safe to call when already stopped.
func (p *Postgres) Stop(ctx context.Context) error {
	if !p.running() {
		return nil
	}
	cmd := exec.CommandContext(ctx, p.tool("pg_ctl"),
		"stop", "-w", "-m", "fast", "--pgdata="+p.layout.PGData())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pg_ctl stop: %w: %s", err, out)
	}
	return nil
}

func (p *Postgres) running() bool {
	cmd := exec.Command(p.tool("pg_ctl"), "status", "--pgdata="+p.layout.PGData())
	return cmd.Run() == nil
}

func (p *Postgres) waitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p.port), time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("postgres did not become ready within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// ensureDatabase creates the application database if initdb only made postgres/<user>.
func (p *Postgres) ensureDatabase(ctx context.Context) error {
	psql := p.tool("psql")
	if !fileExists(psql) {
		return nil // psql not bundled; migrations will fail loudly if the DB is missing
	}
	pw, err := p.password()
	if err != nil {
		return err
	}
	base := fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/postgres?sslmode=disable", p.dbUser, pw, p.port)
	check := exec.CommandContext(ctx, psql, base, "-tAc",
		fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname='%s'", p.dbName))
	out, _ := check.Output()
	if strings.TrimSpace(string(out)) == "1" {
		return nil
	}
	create := exec.CommandContext(ctx, psql, base, "-c",
		fmt.Sprintf("CREATE DATABASE %s OWNER %s", p.dbName, p.dbUser))
	if o, err := create.CombinedOutput(); err != nil {
		return fmt.Errorf("create database: %w: %s", err, o)
	}
	return nil
}

// password reads (or generates on first use) the cluster superuser password.
func (p *Postgres) password() (string, error) {
	path := filepath.Join(p.layout.Home, "pg_password")
	if b, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	pw := hex.EncodeToString(buf)
	if err := os.MkdirAll(p.layout.Home, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(pw), 0o600); err != nil {
		return "", err
	}
	return pw, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
