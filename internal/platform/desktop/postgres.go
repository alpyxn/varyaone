package desktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
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
	// portPinned records an explicit VARYAONE_DESKTOP_PG_PORT: a pinned port is
	// never stepped over, so a self-hoster who chose one gets the failure rather
	// than a cluster that quietly moved somewhere else.
	portPinned bool
	dbName     string
	dbUser     string
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
	if bundled != "" && bin == bundled {
		if err := verifyPGBundle(bin); err != nil {
			return nil, err
		}
	}
	port, pinned := defaultPGPort, false
	if v := os.Getenv("VARYAONE_DESKTOP_PG_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 65535 {
			port, pinned = n, true
		}
	}
	return &Postgres{
		layout: layout, bin: bin, port: port, portPinned: pinned,
		dbName: pgDBName, dbUser: pgDBUser,
	}, nil
}

// verifyPGBundle catches a broken or partially-extracted PostgreSQL bundle
// before it turns into an opaque "initdb: exit status 0xC0000135". A bad updater
// zip or a broken install lands here.
func verifyPGBundle(bin string) error {
	for _, name := range []string{"initdb", "postgres", "pg_ctl", "psql", "pg_dump", "pg_restore"} {
		fi, err := os.Stat(filepath.Join(bin, exe(name)))
		if err != nil {
			return fmt.Errorf("PostgreSQL paketi eksik: %s bulunamadı (%s) — yeniden kurun", exe(name), bin)
		}
		if fi.Size() < 20_000 {
			return fmt.Errorf("PostgreSQL paketi bozuk: %s yalnızca %d bayt — kurulum dosyası eksik, yeniden kurun", exe(name), fi.Size())
		}
	}
	if runtime.GOOS != "windows" {
		return nil
	}
	entries, err := os.ReadDir(bin)
	if err != nil {
		return fmt.Errorf("PostgreSQL bin dizini okunamadı (%s): %w", bin, err)
	}
	var dlls int
	have := map[string]bool{}
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		if !strings.HasSuffix(n, ".dll") {
			continue
		}
		dlls++
		have[n] = true
	}
	if dlls < 20 {
		return fmt.Errorf("PostgreSQL paketi eksik: %s içinde yalnızca %d DLL var — yeniden kurun", bin, dlls)
	}
	// App-local MSVC runtime — bundled by fetch-tools.ps1; its absence is the
	// classic initdb 0xC0000135 on a box without the VC++ redistributable.
	for _, crt := range []string{"vcruntime140.dll", "vcruntime140_1.dll", "msvcp140.dll"} {
		if !have[crt] {
			return fmt.Errorf("PostgreSQL paketi eksik: %s yok (%s) — MSVC çalışma zamanı gömülü değil, yeniden kurun", crt, bin)
		}
	}
	return nil
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
	if err := os.MkdirAll(p.layout.Home, 0o755); err != nil {
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

	// initdb can leave a non-empty, unusable directory when the machine loses
	// power or a runtime dependency fails halfway through. Build the cluster in
	// a sibling temporary directory and publish it with a rename only after
	// initdb has completed. Any old partial directory is preserved for support.
	//
	// initdb must create the data directory itself: on Windows it fails to
	// change permissions on a directory that already exists ("could not change
	// permissions of directory ...: Permission denied"), so hand it a
	// not-yet-existing child of the temp dir rather than the temp dir itself.
	initParent, err := os.MkdirTemp(p.layout.Home, "pgdata.init-")
	if err != nil {
		return fmt.Errorf("create temporary database directory: %w", err)
	}
	defer os.RemoveAll(initParent)
	initDir := filepath.Join(initParent, "pgdata")

	cmd := exec.CommandContext(ctx, p.tool("initdb"),
		"--pgdata="+initDir,
		"--username="+p.dbUser,
		"--auth=scram-sha-256",
		"--pwfile="+pwFile,
		"--encoding=UTF8",
		"--locale-provider=libc",
		"--no-locale",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("initdb: %w: %s%s", err, out, p.initdbFailureHint(err))
	}
	if !fileExists(filepath.Join(initDir, "PG_VERSION")) {
		return errors.New("initdb completed without creating PG_VERSION")
	}

	target := p.layout.PGData()
	if _, statErr := os.Stat(target); statErr == nil {
		entries, readErr := os.ReadDir(target)
		if readErr != nil {
			return fmt.Errorf("inspect incomplete database directory: %w", readErr)
		}
		if len(entries) == 0 {
			if err := os.Remove(target); err != nil {
				return fmt.Errorf("remove empty database directory: %w", err)
			}
		} else {
			archive := filepath.Join(p.layout.Home, "pgdata.incomplete-"+time.Now().UTC().Format("20060102T150405.000000000Z"))
			if err := os.Rename(target, archive); err != nil {
				return fmt.Errorf("archive incomplete database directory: %w", err)
			}
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect database directory: %w", statErr)
	}
	if err := os.Rename(initDir, target); err != nil {
		return fmt.Errorf("publish initialized database directory: %w", err)
	}
	return nil
}

// initdbFailureHint adds context when initdb produced no output of its own — the
// usual sign it could not even start (a missing DLL: 0xC0000135). It reports the
// binary size and how many DLLs sit next to it, so a truncated bundle is obvious
// from the log without shell access to the machine.
func (p *Postgres) initdbFailureHint(err error) string {
	if !strings.Contains(err.Error(), "0xc0000135") && !strings.Contains(err.Error(), "0xc000007b") {
		return ""
	}
	tool := p.tool("initdb")
	size := int64(-1)
	if fi, statErr := os.Stat(tool); statErr == nil {
		size = fi.Size()
	}
	dlls := 0
	if entries, rdErr := os.ReadDir(p.bin); rdErr == nil {
		for _, e := range entries {
			if strings.EqualFold(filepath.Ext(e.Name()), ".dll") {
				dlls++
			}
		}
	}
	return fmt.Sprintf(" [hint: initdb.exe %d bytes, %d DLLs in %s — a missing/zero-byte "+
		"binary or <20 DLLs means the PostgreSQL bundle was extracted incompletely]",
		size, dlls, p.bin)
}

// Start boots the cluster (idempotent) and waits until it accepts connections,
// then ensures the application database exists.
func (p *Postgres) Start(ctx context.Context) error {
	if err := p.resolvePort(); err != nil {
		return err
	}
	if p.running() {
		return p.ensureDatabase(ctx)
	}
	if err := os.MkdirAll(p.layout.Logs(), 0o755); err != nil {
		return fmt.Errorf("prepare log directory: %w", err)
	}
	// pg_ctl only ever appends to these, so an install that runs for years would
	// otherwise grow them without bound. The cluster is stopped here, so nothing
	// holds the old file open.
	logFile := filepath.Join(p.layout.Logs(), "postgres.log")
	rollLog(logFile, clusterLogMaxBytes)
	rollLog(filepath.Join(p.layout.Logs(), "pg_ctl.log"), clusterLogMaxBytes)
	// The squashed baseline migration creates the whole schema in one
	// transaction (hundreds of objects), which overflows the default
	// max_locks_per_transaction (64) lock table on a fresh cluster.
	opts := fmt.Sprintf("-p %d -c listen_addresses=127.0.0.1 -c unix_socket_directories= -c max_locks_per_transaction=256", p.port)
	cmd := exec.CommandContext(ctx, p.tool("pg_ctl"),
		"start", "-w", "-t", "60",
		"--pgdata="+p.layout.PGData(),
		"--log="+logFile,
		"--options="+opts,
	)
	if out, err := runDetachedCapture(cmd, filepath.Join(p.layout.Logs(), "pg_ctl.log")); err != nil {
		return fmt.Errorf("pg_ctl start: %w: %s", err, out)
	}
	if err := p.waitReady(ctx, 60*time.Second); err != nil {
		return err
	}
	// A TCP dial only proves *something* answers on the port. Confirm pg_ctl
	// still recognises the cluster as ours before any client connects, so a
	// foreign PostgreSQL that grabbed the port between resolvePort and now is
	// never mistaken for the bundled one and migrated into.
	if !p.running() {
		return fmt.Errorf("port %d üzerinde başka bir PostgreSQL çalışıyor — Varya One kümesi başlatılamadı", p.port)
	}
	return p.ensureDatabase(ctx)
}

// clusterLogMaxBytes caps postgres.log / pg_ctl.log; past it a single previous
// generation is kept as "<name>.1" and the rest discarded.
const clusterLogMaxBytes = 32 << 20

func rollLog(path string, maxBytes int64) {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() <= maxBytes {
		return
	}
	_ = os.Remove(path + ".1")
	_ = os.Rename(path, path+".1")
}

// pgPortScanRange is how many ports past the preferred one Start will try.
const pgPortScanRange = 64

// resolvePort settles which TCP port this cluster uses, before pg_ctl runs.
//
// The default 5433 is a preference, not a guarantee: a leftover postmaster from
// a hard-killed run, a second Varya One install, or an unrelated PostgreSQL can
// already own it, and a desktop install has to come up anyway. So a cluster that
// is already running keeps whatever port its postmaster.pid records (that is the
// port its clients must use), and otherwise a busy port is stepped over until a
// free one is found. An explicitly pinned VARYAONE_DESKTOP_PG_PORT is never
// moved — that operator wants the failure, not a silent relocation.
func (p *Postgres) resolvePort() error {
	if port, ok := p.runningPort(); ok {
		p.port = port
		return nil
	}
	if p.portPinned || !tcpPortBusy(p.port) {
		return nil
	}
	for candidate := p.port + 1; candidate < p.port+pgPortScanRange; candidate++ {
		if candidate > 65535 {
			break
		}
		if !tcpPortBusy(candidate) {
			p.port = candidate
			return nil
		}
	}
	return fmt.Errorf("kullanılabilir veritabanı portu bulunamadı (%d-%d denendi)",
		p.port, p.port+pgPortScanRange-1)
}

// runningPort returns the port a live cluster in our data directory is listening
// on, read from line 4 of postmaster.pid. A stale pid file left by a crash is
// ignored: pg_ctl status is what decides whether the cluster is actually up.
func (p *Postgres) runningPort() (int, bool) {
	if !p.running() {
		return 0, false
	}
	b, err := os.ReadFile(filepath.Join(p.layout.PGData(), "postmaster.pid"))
	if err != nil {
		return 0, false
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) < 4 {
		return 0, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(lines[3]))
	if err != nil || port <= 0 || port > 65535 {
		return 0, false
	}
	return port, true
}

// tcpPortBusy reports whether anything already holds the loopback port.
func tcpPortBusy(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}

// runDetachedCapture runs a command that leaves a long-lived process behind and
// returns whatever it printed, captured through capturePath instead of a pipe.
//
// exec.Cmd's CombinedOutput() plumbs stdout/stderr through an anonymous pipe and
// waits for that pipe to reach EOF. On Windows `pg_ctl start` launches the
// postmaster under a cmd.exe that outlives pg_ctl and inherits its handles, so
// the write end of that pipe stays open for the whole life of the cluster and
// CombinedOutput() blocks until PostgreSQL shuts down — the desktop stack hangs
// forever on the boot that starts the cluster. Handing the child a plain file
// instead means no pipe and no reader goroutine, so Wait returns as soon as
// pg_ctl itself exits.
func runDetachedCapture(cmd *exec.Cmd, capturePath string) ([]byte, error) {
	f, err := os.OpenFile(capturePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		// Without a capture file the command still has to run; give up the
		// output rather than the cluster.
		cmd.Stdout, cmd.Stderr = nil, nil
		return nil, cmd.Run()
	}
	cmd.Stdout, cmd.Stderr = f, f
	runErr := cmd.Run()
	_ = f.Close()
	out, _ := os.ReadFile(capturePath)
	return out, runErr
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
