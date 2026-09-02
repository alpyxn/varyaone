package desktop

import (
	"archive/zip"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeTestZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for name, body := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRollbackIncludesBundledPostgres(t *testing.T) {
	if !slices.Contains(swappable, "pgsql") {
		t.Fatal("pgsql must be staged for rollback because release zips replace its binaries")
	}

	root := t.TempDir()
	layout := Layout{
		Home:       filepath.Join(root, "home"),
		InstallDir: filepath.Join(root, "install"),
	}
	oldPGCtl := filepath.Join(layout.InstallDir, "pgsql", "bin", "pg_ctl.exe")
	if err := os.MkdirAll(filepath.Dir(oldPGCtl), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPGCtl, []byte("old-postgres"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := stageRollback(layout); err != nil {
		t.Fatalf("stage rollback: %v", err)
	}
	if err := os.WriteFile(oldPGCtl, []byte("new-postgres"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := restoreRollback(layout); err != nil {
		t.Fatalf("restore rollback: %v", err)
	}

	got, err := os.ReadFile(oldPGCtl)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old-postgres" {
		t.Fatalf("restored pg_ctl = %q, want old-postgres", got)
	}
}

func TestExtractZipOverwritesExistingFiles(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "rel.zip")
	dest := filepath.Join(dir, "install")

	writeTestZip(t, zipPath, map[string]string{"varyaone.exe": "v1", "RELEASE": "v1"})
	if err := extractZip(zipPath, dest); err != nil {
		t.Fatalf("first extract: %v", err)
	}

	// A second release lands on top of the first without error.
	writeTestZip(t, zipPath, map[string]string{"varyaone.exe": "v2", "RELEASE": "v2"})
	if err := extractZip(zipPath, dest); err != nil {
		t.Fatalf("second extract: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "varyaone.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2" {
		t.Fatalf("want v2, got %q", got)
	}

	cleanStaleReplacements(dest)
	entries, _ := os.ReadDir(dest)
	for _, e := range entries {
		if filepath.Ext(e.Name()) != "" && len(e.Name()) > 5 && e.Name()[len(e.Name())-5:] == ".old-" {
			t.Fatalf("stale replacement left behind: %s", e.Name())
		}
	}
}
