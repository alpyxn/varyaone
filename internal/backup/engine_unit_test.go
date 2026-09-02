package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestNewEngineUsesBundledPostgresTools(t *testing.T) {
	bin := t.TempDir()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	for _, name := range []string{"pg_dump" + suffix, "pg_restore" + suffix} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("tool"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	engine, err := NewEngine(Options{DatabaseURL: "postgres://user:secret@localhost/db", PostgresBinDir: bin})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(engine.pgDump) != bin || filepath.Dir(engine.pgRestore) != bin {
		t.Fatalf("bundled tools not selected: dump=%q restore=%q", engine.pgDump, engine.pgRestore)
	}
}

// buildArchive writes a minimal but well-formed `.varya` archive: manifest,
// database.dump, then one tar entry per storage object. It deliberately does not
// go through Engine.Create so the checksum-verification paths can be tested
// without a live PostgreSQL.
func buildArchive(t *testing.T, dump []byte, objects map[string][]byte) []byte {
	t.Helper()

	manifest := Manifest{
		FormatVersion:      FormatVersion,
		CreatedAt:          time.Unix(0, 0).UTC(),
		DatabaseDumpSize:   int64(len(dump)),
		DatabaseDumpSHA256: sha256Hex(dump),
	}
	for key, body := range objects {
		manifest.Objects = append(manifest.Objects, ObjectEntry{
			Key: key, Size: int64(len(body)), SHA256: sha256Hex(body),
		})
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	write := func(name string, body []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	mb, _ := json.MarshalIndent(manifest, "", "  ")
	write(manifestEntry, mb)
	write(dumpEntry, dump)
	for key, body := range objects {
		write(storagePrefix+key, body)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestVerifyAcceptsIntactArchive(t *testing.T) {
	archive := buildArchive(t, []byte("PGDMP-fake-dump"), map[string][]byte{
		"media/a.bin": bytes.Repeat([]byte("x"), 1000),
		"media/b.txt": []byte("hello"),
	})
	e := &Engine{now: time.Now}
	m, err := e.Verify(context.Background(), bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(m.Objects) != 2 {
		t.Fatalf("objects = %d, want 2", len(m.Objects))
	}
}

func TestVerifyRejectsCorruptDump(t *testing.T) {
	archive := buildArchive(t, []byte("PGDMP-fake-dump"), nil)
	// Flip a byte inside the database.dump payload.
	idx := bytes.Index(archive, []byte("PGDMP-fake-dump"))
	if idx < 0 {
		t.Fatal("dump marker not found")
	}
	archive[idx+2] ^= 0xff
	e := &Engine{now: time.Now}
	if _, err := e.Verify(context.Background(), bytes.NewReader(archive)); err == nil {
		t.Fatal("Verify accepted a corrupt dump")
	}
}

func TestVerifyRejectsCorruptObject(t *testing.T) {
	body := bytes.Repeat([]byte("secret"), 64)
	archive := buildArchive(t, []byte("dump"), map[string][]byte{"media/x.bin": body})
	idx := bytes.Index(archive, body)
	if idx < 0 {
		t.Fatal("object body not found")
	}
	archive[idx] ^= 0x01
	e := &Engine{now: time.Now}
	if _, err := e.Verify(context.Background(), bytes.NewReader(archive)); err == nil {
		t.Fatal("Verify accepted a corrupt storage object")
	}
}

func TestVerifyRejectsMissingObject(t *testing.T) {
	// Manifest claims an object the tar never carries.
	manifest := Manifest{
		FormatVersion:      FormatVersion,
		DatabaseDumpSize:   4,
		DatabaseDumpSHA256: sha256Hex([]byte("dump")),
		Objects:            []ObjectEntry{{Key: "media/ghost.bin", Size: 3, SHA256: sha256Hex([]byte("abc"))}},
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	mb, _ := json.MarshalIndent(manifest, "", "  ")
	_ = tw.WriteHeader(&tar.Header{Name: manifestEntry, Size: int64(len(mb)), Mode: 0o600})
	_, _ = tw.Write(mb)
	_ = tw.WriteHeader(&tar.Header{Name: dumpEntry, Size: 4, Mode: 0o600})
	_, _ = tw.Write([]byte("dump"))
	_ = tw.Close()

	e := &Engine{now: time.Now}
	if _, err := e.Verify(context.Background(), bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("Verify accepted an archive with a missing object")
	}
}

func TestSnapshotStorageToleratesConcurrentDelete(t *testing.T) {
	root := filepath.Join(t.TempDir(), "storage")
	mustWrite(t, filepath.Join(root, "keep/a.bin"), []byte("aaaa"))
	mustWrite(t, filepath.Join(root, "keep/b.bin"), []byte("bbbbbb"))
	mustWrite(t, filepath.Join(root, ".varya-object-tmp123"), []byte("in-flight"))

	e := &Engine{storageRoot: root, now: time.Now}
	snap, objects, skipped, err := e.snapshotStorage(context.Background())
	if err != nil {
		t.Fatalf("snapshotStorage: %v", err)
	}
	defer os.RemoveAll(snap)

	if len(objects) != 2 {
		t.Fatalf("objects = %d (%+v), want 2", len(objects), objects)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none", skipped)
	}
	for _, o := range objects {
		if o.Key == ".varya-object-tmp123" {
			t.Fatal("snapshot captured an in-flight temp object")
		}
	}
	// The snapshot must be independent of later deletes in the live tree.
	if err := os.RemoveAll(filepath.Join(root, "keep")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(snap, "keep/a.bin")); err != nil {
		t.Fatalf("snapshot lost a file after live delete: %v", err)
	}
}

func TestSwapStorageReplacesContentsNotRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "storage")
	mustWrite(t, filepath.Join(root, "companies/old.bin"), []byte("old"))
	mustWrite(t, filepath.Join(root, "keep-me.txt"), []byte("stale"))

	// The storage root must survive as the very same directory (it is a mount
	// point in production) — pin its identity by inode.
	var before syscall.Stat_t
	if err := syscall.Stat(root, &before); err != nil {
		t.Fatal(err)
	}

	stage := filepath.Join(root, ".varya-stage-test")
	mustWrite(t, filepath.Join(stage, "companies/new.bin"), []byte("new"))
	mustWrite(t, filepath.Join(stage, "fresh.txt"), []byte("fresh"))

	e := &Engine{storageRoot: root, now: time.Now}
	if err := e.swapStorage(stage); err != nil {
		t.Fatalf("swapStorage: %v", err)
	}

	var after syscall.Stat_t
	if err := syscall.Stat(root, &after); err != nil {
		t.Fatal(err)
	}
	if before.Ino != after.Ino {
		t.Fatalf("storage root directory was replaced (inode %d -> %d)", before.Ino, after.Ino)
	}
	if b, err := os.ReadFile(filepath.Join(root, "companies/new.bin")); err != nil || string(b) != "new" {
		t.Fatalf("new content not in place: %v %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(root, "keep-me.txt")); !os.IsNotExist(err) {
		t.Fatalf("pre-existing file not replaced by the staged tree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "fresh.txt")); err != nil {
		t.Fatalf("staged file missing: %v", err)
	}
	// No engine working directories left behind.
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if len(entry.Name()) > 6 && entry.Name()[:6] == ".varya" {
			t.Fatalf("leftover working dir: %s", entry.Name())
		}
	}
}

func mustWrite(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}
