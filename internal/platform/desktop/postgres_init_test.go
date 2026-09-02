package desktop

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestEnsureInitializedArchivesPartialClusterAndPublishesAtomically(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper is a POSIX shell script")
	}
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	initdb := filepath.Join(bin, "initdb")
	// Mimic real initdb: it creates the data directory itself (we no longer
	// pre-create it, because Windows initdb cannot chmod an existing one).
	script := `#!/bin/sh
for arg in "$@"; do
  case "$arg" in --pgdata=*) dir="${arg#--pgdata=}";; esac
done
test -n "$dir" || exit 2
test ! -e "$dir" || exit 3
mkdir -p "$dir"
printf '18\n' > "$dir/PG_VERSION"
`
	if err := os.WriteFile(initdb, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(home, "pgdata")
	if err := os.MkdirAll(partial, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partial, "orphaned-file"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := &Postgres{layout: Layout{Home: home}, bin: bin, dbName: pgDBName, dbUser: pgDBUser}
	if err := p.EnsureInitialized(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(partial, "PG_VERSION")) {
		t.Fatal("initialized cluster was not published")
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	foundArchive := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "pgdata.incomplete-") && fileExists(filepath.Join(home, entry.Name(), "orphaned-file")) {
			foundArchive = true
		}
		if strings.HasPrefix(entry.Name(), "pgdata.init-") {
			t.Fatalf("temporary initialization directory leaked: %s", entry.Name())
		}
	}
	if !foundArchive {
		t.Fatal("partial cluster was not archived")
	}
}

// runDetachedCapture must return as soon as the command itself exits, even
// though the command leaves a longer-lived process behind holding the inherited
// output handles — the shape of `pg_ctl start`, which hands the postmaster off
// to a helper that outlives it. exec.Cmd's own CombinedOutput() waits for the
// pipe to reach EOF and would block here for the child's whole lifetime.
func TestRunDetachedCaptureDoesNotWaitForInheritedChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper is a POSIX shell script")
	}
	capture := filepath.Join(t.TempDir(), "capture.log")
	cmd := exec.Command("/bin/sh", "-c", "sleep 30 & echo started; exit 0")

	done := make(chan struct{})
	var out []byte
	var runErr error
	go func() {
		defer close(done)
		out, runErr = runDetachedCapture(cmd, capture)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runDetachedCapture blocked on the surviving grandchild")
	}
	if runErr != nil {
		t.Fatalf("run: %v", runErr)
	}
	if strings.TrimSpace(string(out)) != "started" {
		t.Fatalf("captured output = %q, want %q", out, "started")
	}
}

func TestResolvePortStepsOverABusyPort(t *testing.T) {
	// Occupy the preferred port with something that is not our cluster — a
	// leftover postmaster from a hard-killed run, a second install, or an
	// unrelated PostgreSQL all look like this.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	taken := busy.Addr().(*net.TCPAddr).Port

	p := &Postgres{layout: Layout{Home: t.TempDir()}, port: taken}
	if err := p.resolvePort(); err != nil {
		t.Fatalf("resolvePort: %v", err)
	}
	if p.port == taken {
		t.Fatalf("resolvePort kept the busy port %d", taken)
	}
	if tcpPortBusy(p.port) {
		t.Fatalf("resolvePort chose port %d, which is also busy", p.port)
	}
}

// An operator who pins VARYAONE_DESKTOP_PG_PORT wants the explicit failure, not
// a cluster that silently moved to a port their tooling does not know about.
func TestResolvePortKeepsAPinnedPort(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	taken := busy.Addr().(*net.TCPAddr).Port

	p := &Postgres{layout: Layout{Home: t.TempDir()}, port: taken, portPinned: true}
	if err := p.resolvePort(); err != nil {
		t.Fatalf("resolvePort: %v", err)
	}
	if p.port != taken {
		t.Fatalf("pinned port moved from %d to %d", taken, p.port)
	}
}

func TestRollLogKeepsOneGeneration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "postgres.log")
	if err := os.WriteFile(path, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}

	rollLog(path, 100) // under the cap: left alone
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatal("rollLog rotated a log that was under the cap")
	}

	rollLog(path, 4) // over the cap: rotated
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("rollLog left the oversized log in place")
	}
	rotated, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("read rotated log: %v", err)
	}
	if string(rotated) != "0123456789" {
		t.Fatalf("rotated log = %q", rotated)
	}
}
