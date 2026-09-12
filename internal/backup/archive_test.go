package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildRawArchive writes an archive from an explicit manifest and an explicit
// list of tar entries, so a test can produce combinations Engine.Create never
// would: escaping keys, duplicate entries, link entries, trailing data.
func buildRawArchive(t *testing.T, manifest Manifest, entries []*tar.Header, bodies [][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	head := &tar.Header{Name: manifestEntry, Mode: 0o600, Size: int64(len(manifestBytes)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(head); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(manifestBytes); err != nil {
		t.Fatal(err)
	}
	for i, header := range entries {
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if len(bodies[i]) > 0 {
			if _, err := tw.Write(bodies[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func regularEntry(name string, body []byte) *tar.Header {
	return &tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg}
}

// TestValidateObjectKeyRejectsEscapingPaths is the regression guard for B01: a
// manifest entry whose checksum is perfectly correct must still not be able to
// name a destination outside the staging directory.
func TestValidateObjectKeyRejectsEscapingPaths(t *testing.T) {
	bad := []string{
		"",
		"../escaped.txt",
		"media/../../escaped.txt",
		"/etc/passwd",
		"C:/Windows/system32/x",
		`media\escaped.txt`,
		"media//a.bin",
		"media/./a.bin",
		"media/",
		"./media/a.bin",
		"media/a.bin\x00",
		".varya-retired-x/a.bin",
		"media/.varya-snap-1/a.bin",
		"media/CON",
		"media/nul.txt",
		"media/trailing ",
		"media/trailing.",
		strings.Repeat("a", maxKeyLength+1),
	}
	for _, key := range bad {
		if err := validateObjectKey(key); err == nil {
			t.Errorf("validateObjectKey(%q) accepted an unsafe key", key)
		} else if !errors.Is(err, ErrArchiveInvalid) {
			t.Errorf("validateObjectKey(%q) = %v, want ErrArchiveInvalid", key, err)
		}
	}
	for _, key := range []string{"a.bin", "media/a.bin", "media/nested/deep/a.bin", "media/ünïcode.txt"} {
		if err := validateObjectKey(key); err != nil {
			t.Errorf("validateObjectKey(%q) rejected a valid key: %v", key, err)
		}
	}
}

// TestVerifyRejectsEscapingKeyWithCorrectChecksum is the end-to-end half of
// B01: the archive is internally consistent, every checksum matches, and it
// must still be refused.
func TestVerifyRejectsEscapingKeyWithCorrectChecksum(t *testing.T) {
	body := []byte("owned")
	dump := []byte("dump")
	manifest := Manifest{
		FormatVersion:      FormatVersion,
		CreatedAt:          time.Unix(0, 0).UTC(),
		DatabaseDumpSize:   int64(len(dump)),
		DatabaseDumpSHA256: sha256Hex(dump),
		Objects:            []ObjectEntry{{Key: "../escaped.txt", Size: int64(len(body)), SHA256: sha256Hex(body)}},
	}
	archive := buildRawArchive(t, manifest,
		[]*tar.Header{regularEntry(dumpEntry, dump), regularEntry(storagePrefix+"../escaped.txt", body)},
		[][]byte{dump, body})

	e := &Engine{now: time.Now}
	if _, err := e.Verify(context.Background(), bytes.NewReader(archive)); !errors.Is(err, ErrArchiveInvalid) {
		t.Fatalf("Verify = %v, want ErrArchiveInvalid", err)
	}
}

// TestStageStorageWritesNothingOutsideStage is the second barrier: here the
// manifest is entirely valid, and the escaping path is in the tar entry name
// instead. Extraction must refuse it and must not have created the file on the
// way to finding out.
func TestStageStorageWritesNothingOutsideStage(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "escaped.txt")
	_ = os.Remove(outside)

	body := []byte("owned")
	dump := []byte("dump")
	manifest := Manifest{
		FormatVersion:      FormatVersion,
		CreatedAt:          time.Unix(0, 0).UTC(),
		DatabaseDumpSize:   int64(len(dump)),
		DatabaseDumpSHA256: sha256Hex(dump),
		Objects:            []ObjectEntry{{Key: "media/a.bin", Size: int64(len(body)), SHA256: sha256Hex(body)}},
	}
	archive := buildRawArchive(t, manifest,
		[]*tar.Header{regularEntry(dumpEntry, dump), regularEntry(storagePrefix+"../../escaped.txt", body)},
		[][]byte{dump, body})

	e := &Engine{storageRoot: root, now: time.Now}
	reader := tar.NewReader(bytes.NewReader(archive))
	if _, err := readManifest(reader); err != nil {
		t.Fatalf("readManifest rejected a valid manifest: %v", err)
	}
	if err := verifyDumpEntry(reader, manifest, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := e.stageStorage(context.Background(), reader, manifest); !errors.Is(err, ErrArchiveInvalid) {
		t.Fatalf("stageStorage = %v, want ErrArchiveInvalid", err)
	}
	if _, err := os.Lstat(outside); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archive wrote outside the storage root: %s", outside)
	}
	// The failed staging directory must not be left behind either.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), stagePrefix) {
			t.Errorf("failed staging directory left behind: %s", entry.Name())
		}
	}
}

// TestVerifyRejectsUnsupportedEntryTypes guards B08: link, directory and device
// entries are archive tampering, not content to be skipped over.
func TestVerifyRejectsUnsupportedEntryTypes(t *testing.T) {
	dump := []byte("dump")
	base := Manifest{
		FormatVersion:      FormatVersion,
		CreatedAt:          time.Unix(0, 0).UTC(),
		DatabaseDumpSize:   int64(len(dump)),
		DatabaseDumpSHA256: sha256Hex(dump),
		Objects:            []ObjectEntry{{Key: "media/a.bin", Size: 0, SHA256: sha256Hex(nil)}},
	}
	for name, flag := range map[string]byte{
		"symlink":   tar.TypeSymlink,
		"hardlink":  tar.TypeLink,
		"directory": tar.TypeDir,
		"fifo":      tar.TypeFifo,
	} {
		t.Run(name, func(t *testing.T) {
			header := &tar.Header{Name: storagePrefix + "media/a.bin", Mode: 0o600, Typeflag: flag, Linkname: "/etc/passwd"}
			archive := buildRawArchive(t, base,
				[]*tar.Header{regularEntry(dumpEntry, dump), header},
				[][]byte{dump, nil})
			e := &Engine{now: time.Now}
			if _, err := e.Verify(context.Background(), bytes.NewReader(archive)); !errors.Is(err, ErrArchiveInvalid) {
				t.Fatalf("Verify accepted a %s entry: %v", name, err)
			}
		})
	}
}

func TestValidateManifestRejectsDuplicateAndClashingKeys(t *testing.T) {
	dump := sha256Hex(nil)
	empty := sha256Hex(nil)
	cases := map[string][]ObjectEntry{
		"duplicate key": {
			{Key: "media/a.bin", SHA256: empty},
			{Key: "media/a.bin", SHA256: empty},
		},
		"file is also a directory": {
			{Key: "media", SHA256: empty},
			{Key: "media/a.bin", SHA256: empty},
		},
		"negative size": {
			{Key: "media/a.bin", Size: -1, SHA256: empty},
		},
		"malformed checksum": {
			{Key: "media/a.bin", SHA256: "NOTHEX"},
		},
	}
	for name, objects := range cases {
		t.Run(name, func(t *testing.T) {
			manifest := Manifest{FormatVersion: FormatVersion, DatabaseDumpSHA256: dump, Objects: objects}
			if err := validateManifest(manifest); !errors.Is(err, ErrArchiveInvalid) {
				t.Fatalf("validateManifest = %v, want ErrArchiveInvalid", err)
			}
		})
	}
}

// TestVerifyRejectsUndeclaredObject guards against an archive smuggling content
// past the manifest: an entry nothing vouched for used to be skipped silently.
func TestVerifyRejectsUndeclaredObject(t *testing.T) {
	dump := []byte("dump")
	manifest := Manifest{
		FormatVersion:      FormatVersion,
		DatabaseDumpSize:   int64(len(dump)),
		DatabaseDumpSHA256: sha256Hex(dump),
	}
	extra := []byte("unlisted")
	archive := buildRawArchive(t, manifest,
		[]*tar.Header{regularEntry(dumpEntry, dump), regularEntry(storagePrefix+"media/extra.bin", extra)},
		[][]byte{dump, extra})
	e := &Engine{now: time.Now}
	if _, err := e.Verify(context.Background(), bytes.NewReader(archive)); !errors.Is(err, ErrArchiveInvalid) {
		t.Fatalf("Verify accepted an undeclared object: %v", err)
	}
}

// TestVerifyToleratesTrailingLogEntry is the compatibility half of B09. Older
// producers wrote the archive and then a JSON log line to the same stdout, so
// real `.varya` files exist with trailing bytes. Those files must keep reading.
func TestVerifyToleratesTrailingLogEntry(t *testing.T) {
	archive := buildArchive(t, []byte("PGDMP-fake-dump"), map[string][]byte{"media/a.txt": []byte("hello")})
	archive = append(archive, []byte(`{"time":"2026-09-11T00:00:00Z","level":"INFO","msg":"backup created"}`+"\n")...)
	e := &Engine{now: time.Now}
	if _, err := e.Verify(context.Background(), bytes.NewReader(archive)); err != nil {
		t.Fatalf("Verify rejected an archive with a trailing log line: %v", err)
	}
}

// TestSnapshotStorageRefusesSymlink guards B02: a symlink inside the storage
// root must fail the backup, not quietly pull an outside file into it.
func TestSnapshotStorageRefusesSymlink(t *testing.T) {
	if _, err := os.Lstat("/etc/hostname"); err != nil {
		t.Skip("no stable outside file to point at")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real.bin"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hostname", filepath.Join(root, "sneaky.bin")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	e := &Engine{storageRoot: root, now: time.Now}
	_, _, _, err := e.snapshotStorage(context.Background())
	if err == nil {
		t.Fatal("snapshotStorage followed a symlink out of the storage root")
	}
	if !strings.Contains(err.Error(), "düzenli olmayan") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCleanupInternalDirsKeepsRetired guards B04: snapshot and staging
// directories are disposable, a retired directory can be the only copy of the
// installation's files.
func TestCleanupInternalDirsKeepsRetired(t *testing.T) {
	root := t.TempDir()
	retired := filepath.Join(root, retiredPrefix+"20260911T000000Z-abc")
	snap := filepath.Join(root, snapPrefix+"20260911T000000Z-abc")
	stage := filepath.Join(root, stagePrefix+"20260911T000000Z-abc")
	for _, dir := range []string{retired, snap, stage} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(retired, "only-copy.bin"), []byte("irreplaceable"), 0o600); err != nil {
		t.Fatal(err)
	}

	e := &Engine{storageRoot: root, now: time.Now}
	e.cleanupInternalDirs()

	if _, err := os.Stat(filepath.Join(retired, "only-copy.bin")); err != nil {
		t.Fatalf("cleanup destroyed recovery data: %v", err)
	}
	for _, dir := range []string{snap, stage} {
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("cleanup left %s behind", dir)
		}
	}
	if got := e.RetiredDirs(); len(got) != 1 || got[0] != retired {
		t.Fatalf("RetiredDirs() = %v, want [%s]", got, retired)
	}
}

// TestRestoreRefusesStorageObjectsWithoutStorageRoot guards B08's "storage
// target missing but archive has files" case: the database must not be
// replaced with one that references files nothing will write.
func TestRestoreRefusesStorageObjectsWithoutStorageRoot(t *testing.T) {
	archive := buildArchive(t, []byte("dump"), map[string][]byte{"media/a.bin": []byte("x")})
	e := &Engine{now: time.Now} // no storageRoot
	_, err := e.Restore(context.Background(), bytes.NewReader(archive), RestoreOptions{Force: true})
	if err == nil || !strings.Contains(err.Error(), "depolama kökü") {
		t.Fatalf("Restore = %v, want a missing-storage-root refusal", err)
	}
}

// TestCreateFileIsAtomicAndVerified covers B09's publication rules.
func TestCreateFileIsAtomicAndVerified(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "varyaone-test.varya")
	if err := os.WriteFile(target, []byte("existing backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := &Engine{now: time.Now}
	if _, err := e.CreateFile(context.Background(), target, PublishOptions{}); !errors.Is(err, ErrArchiveExists) {
		t.Fatalf("CreateFile overwrote an existing backup: %v", err)
	}
	body, err := os.ReadFile(target)
	if err != nil || string(body) != "existing backup" {
		t.Fatalf("existing backup was modified: %q %v", body, err)
	}
}

// TestReadManifestRejectsOversizedManifest keeps a hostile manifest from being
// buffered into memory before anything has been checked.
func TestReadManifestRejectsOversizedManifest(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: manifestEntry, Mode: 0o600, Size: maxManifestBytes + 1, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	// The body is never written; readManifest must reject on the header alone.
	reader := tar.NewReader(bytes.NewReader(buf.Bytes()))
	if _, err := readManifest(reader); !errors.Is(err, ErrArchiveInvalid) {
		t.Fatalf("readManifest = %v, want ErrArchiveInvalid", err)
	}
}
