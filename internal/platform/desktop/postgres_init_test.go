package desktop

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	script := `#!/bin/sh
for arg in "$@"; do
  case "$arg" in --pgdata=*) dir="${arg#--pgdata=}";; esac
done
test -n "$dir" || exit 2
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
