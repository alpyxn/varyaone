// Package backup implements the Varya One `.varya` full-system backup engine.
//
// A `.varya` file is an uncompressed tar archive with a fixed entry order:
//
//	manifest.json   metadata + checksums (always first)
//	database.dump   `pg_dump --format=custom` output (schema + data + sequences)
//	storage/<key>   every object stored by the local storage provider
//
// The engine never enumerates business modules: `pg_dump` captures every table
// that exists and the storage walk captures every object key, so features added
// later are included automatically with no code change.
//
// Robustness guarantees:
//
//   - Create hard-links the storage tree into a sibling snapshot directory before
//     archiving, so concurrent uploads/deletes cannot truncate an object mid-write
//     or fail the whole run. Objects that vanish before the link is taken are
//     recorded in Manifest.SkippedObjects rather than aborting the backup.
//   - Restore stages the database dump and the full storage tree into sibling
//     temp locations and verifies every checksum BEFORE it touches the live
//     database. The storage tree is swapped in with an atomic rename, so a failure
//     at any earlier point leaves the running system completely untouched.
//   - Verify performs the same end-to-end checksum pass without touching anything.
package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/jackc/pgx/v5"
)

const (
	// FormatVersion is bumped only on a breaking change to the archive layout.
	FormatVersion = 1

	manifestEntry = "manifest.json"
	dumpEntry     = "database.dump"
	storagePrefix = "storage/"

	// internalPrefix marks every entry the engine itself creates inside the
	// storage root: the local provider's in-flight temp objects
	// (`.varya-object-*`) plus the engine's own snapshot / restore-staging /
	// retired working directories. Nothing with this prefix is ever archived or
	// restored, and the working directories live INSIDE the storage root so all
	// hard-links and renames stay on one filesystem (the storage root is a bind
	// mount in production; renaming the mount point itself fails with EBUSY).
	internalPrefix = ".varya-"

	snapPrefix    = ".varya-snap-"
	stagePrefix   = ".varya-stage-"
	retiredPrefix = ".varya-retired-"
)

func isInternalName(name string) bool { return strings.HasPrefix(name, internalPrefix) }

// ObjectEntry records one storage object captured in the archive.
type ObjectEntry struct {
	Key    string `json:"key"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest is the first entry in every `.varya` archive.
type Manifest struct {
	FormatVersion    int       `json:"format_version"`
	CreatedAt        time.Time `json:"created_at"`
	Release          string    `json:"release"`
	DatabaseName     string    `json:"database_name"`
	MigrationVersion int64     `json:"migration_version"`
	// PostgresServerNum is the source cluster's server_version_num (e.g. 180004).
	// Zero in archives written before this field existed. A restore uses it only
	// to log when the dump crosses a PostgreSQL major boundary.
	PostgresServerNum    int           `json:"postgres_server_num,omitempty"`
	MasterKeyFingerprint string        `json:"master_key_fingerprint,omitempty"`
	DatabaseDumpSize     int64         `json:"database_dump_size"`
	DatabaseDumpSHA256   string        `json:"database_dump_sha256"`
	Objects              []ObjectEntry `json:"objects"`

	// DumpStartedAt / StorageCapturedAt bracket the (small) window during which
	// the database dump and the storage snapshot were taken. They exist purely
	// for post-mortem diagnostics.
	DumpStartedAt     time.Time `json:"dump_started_at,omitempty"`
	StorageCapturedAt time.Time `json:"storage_captured_at,omitempty"`

	// SkippedObjects lists storage keys that disappeared or were unreadable when
	// the snapshot was taken. A non-empty list means the backup is intentionally
	// incomplete for those keys (they were being deleted concurrently).
	SkippedObjects []string `json:"skipped_objects,omitempty"`
}

// Options configures a new Engine.
type Options struct {
	// DatabaseURL is the PostgreSQL connection string passed verbatim to
	// pg_dump / pg_restore and used for small metadata queries.
	DatabaseURL string
	// StorageRoot is the local storage provider root. Object backup/restore is
	// only supported for the local provider today; an empty root disables the
	// storage portion of the archive.
	StorageRoot string
	Release     string
	// MasterKey, when set, records a fingerprint in the manifest so a restore
	// against a differently-keyed deployment can warn about unreadable
	// encrypted columns.
	MasterKey []byte
}

// Engine creates and restores `.varya` archives.
type Engine struct {
	databaseURL    string
	storageRoot    string
	release        string
	keyFingerprint string
	pgDump         string
	pgRestore      string
	now            func() time.Time
}

// ErrToolMissing is returned when the PostgreSQL client binaries are absent.
var ErrToolMissing = errors.New("postgresql-client (pg_dump/pg_restore) kurulu değil")

// ErrArchiveNewer is returned by Restore when the archive was produced by a
// newer schema than this binary understands and Force was not set.
var ErrArchiveNewer = errors.New("yedek bu sürümden daha yeni bir şema içeriyor")

// ErrKeyMismatch is returned by Restore when the archive was produced under a
// different master key and Force was not set.
var ErrKeyMismatch = errors.New("yedek farklı bir ana anahtar ile alınmış")

// ErrStoragePartial is returned by Restore when the database was restored
// successfully but swapping the storage tree into place failed. The database is
// on the new content; the storage tree may be stale. Callers must treat this as
// a system-inconsistent state requiring operator attention.
var ErrStoragePartial = errors.New("veritabanı geri yüklendi ancak depolama dosyaları yerine konulamadı")

// NewEngine resolves the client binaries and returns a ready engine.
func NewEngine(opts Options) (*Engine, error) {
	if strings.TrimSpace(opts.DatabaseURL) == "" {
		return nil, errors.New("backup: DatabaseURL is required")
	}
	pgDump, err := exec.LookPath("pg_dump")
	if err != nil {
		return nil, ErrToolMissing
	}
	pgRestore, err := exec.LookPath("pg_restore")
	if err != nil {
		return nil, ErrToolMissing
	}
	engine := &Engine{
		databaseURL: opts.DatabaseURL,
		storageRoot: strings.TrimSpace(opts.StorageRoot),
		release:     opts.Release,
		pgDump:      pgDump,
		pgRestore:   pgRestore,
		now:         time.Now,
	}
	if len(opts.MasterKey) > 0 {
		sum := sha256.Sum256(opts.MasterKey)
		engine.keyFingerprint = hex.EncodeToString(sum[:])[:16]
	}
	return engine, nil
}

// Create streams a complete `.varya` archive to w and returns the manifest it
// wrote as the first entry.
func (e *Engine) Create(ctx context.Context, w io.Writer) (Manifest, error) {
	dumpStartedAt := e.now().UTC()

	dumpFile, err := os.CreateTemp("", "varya-dump-*.pgdump")
	if err != nil {
		return Manifest{}, fmt.Errorf("create dump temp file: %w", err)
	}
	dumpPath := dumpFile.Name()
	defer func() {
		_ = dumpFile.Close()
		_ = os.Remove(dumpPath)
	}()

	dumpDigest := sha256.New()
	cmd := exec.CommandContext(ctx, e.pgDump, "--format=custom", "--no-owner", "--no-privileges", e.databaseURL)
	var stderr bytes.Buffer
	cmd.Stdout = io.MultiWriter(dumpFile, dumpDigest)
	cmd.Stderr = &stderr
	if err = cmd.Run(); err != nil {
		return Manifest{}, fmt.Errorf("pg_dump: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	dumpSize, err := dumpFile.Seek(0, io.SeekCurrent)
	if err != nil {
		return Manifest{}, err
	}
	if _, err = dumpFile.Seek(0, io.SeekStart); err != nil {
		return Manifest{}, err
	}

	e.cleanupInternalDirs()

	// A hard-link snapshot pins every object's inode so concurrent writers in the
	// storage provider (which always writes to a temp name then renames) cannot
	// change what we archive after we have hashed it.
	snapshot, objects, skipped, err := e.snapshotStorage(ctx)
	if err != nil {
		return Manifest{}, err
	}
	if snapshot != "" {
		defer func() { _ = os.RemoveAll(snapshot) }()
	}
	storageCapturedAt := e.now().UTC()

	migrationVersion, pgServerNum, err := e.readSourceMeta(ctx)
	if err != nil {
		return Manifest{}, err
	}

	manifest := Manifest{
		FormatVersion:        FormatVersion,
		CreatedAt:            e.now().UTC(),
		Release:              e.release,
		DatabaseName:         databaseName(e.databaseURL),
		MigrationVersion:     migrationVersion,
		PostgresServerNum:    pgServerNum,
		MasterKeyFingerprint: e.keyFingerprint,
		DatabaseDumpSize:     dumpSize,
		DatabaseDumpSHA256:   hex.EncodeToString(dumpDigest.Sum(nil)),
		Objects:              objects,
		DumpStartedAt:        dumpStartedAt,
		StorageCapturedAt:    storageCapturedAt,
		SkippedObjects:       skipped,
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, err
	}

	archive := tar.NewWriter(w)
	writeHeader := func(name string, size int64) error {
		return archive.WriteHeader(&tar.Header{
			Name: name, Mode: 0o600, Size: size,
			ModTime: manifest.CreatedAt, Typeflag: tar.TypeReg,
		})
	}

	if err = writeHeader(manifestEntry, int64(len(manifestBytes))); err != nil {
		return Manifest{}, err
	}
	if _, err = archive.Write(manifestBytes); err != nil {
		return Manifest{}, err
	}

	if err = writeHeader(dumpEntry, dumpSize); err != nil {
		return Manifest{}, err
	}
	if _, err = io.Copy(archive, dumpFile); err != nil {
		return Manifest{}, fmt.Errorf("write database dump: %w", err)
	}

	written := 0
	for _, object := range objects {
		if err = ctx.Err(); err != nil {
			return Manifest{}, err
		}
		path := filepath.Join(snapshot, filepath.FromSlash(object.Key))
		file, openErr := os.Open(path)
		if openErr != nil {
			return Manifest{}, fmt.Errorf("open storage snapshot %q: %w", object.Key, openErr)
		}
		if err = writeHeader(storagePrefix+object.Key, object.Size); err != nil {
			_ = file.Close()
			return Manifest{}, err
		}
		n, copyErr := io.Copy(archive, file)
		_ = file.Close()
		if copyErr != nil {
			return Manifest{}, fmt.Errorf("write storage object %q: %w", object.Key, copyErr)
		}
		if n != object.Size {
			return Manifest{}, fmt.Errorf("depolama snapshot'ı %q boyutu değişti (%d != %d)", object.Key, n, object.Size)
		}
		written++
	}
	if written != len(objects) {
		return Manifest{}, fmt.Errorf("depolama tutarsızlığı: %d/%d obje arşive yazıldı", written, len(objects))
	}

	if err = archive.Close(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// RestoreOptions tunes a Restore call.
type RestoreOptions struct {
	// Force restores even when the archive schema is newer than this binary.
	Force bool
}

// Restore rebuilds the database and the local storage tree from r. It is a
// destructive whole-system operation, but it is staged: the database dump and
// the entire storage tree are extracted and checksum-verified into sibling temp
// locations FIRST. Only once every byte checks out does it run pg_restore and
// swap the storage tree in with an atomic rename. A failure before that point
// leaves the live system untouched.
func (e *Engine) Restore(ctx context.Context, r io.Reader, opts RestoreOptions) (Manifest, error) {
	archive := tar.NewReader(r)

	header, err := archive.Next()
	if err != nil || header.Name != manifestEntry {
		return Manifest{}, fmt.Errorf("geçersiz .varya arşivi: ilk giriş %q", manifestName(header))
	}
	var manifest Manifest
	if err = json.NewDecoder(archive).Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("manifest okunamadı: %w", err)
	}
	if manifest.FormatVersion != FormatVersion {
		return Manifest{}, fmt.Errorf("desteklenmeyen yedek format sürümü %d", manifest.FormatVersion)
	}
	if !opts.Force {
		latest, latestErr := migrations.Latest()
		if latestErr != nil {
			return Manifest{}, latestErr
		}
		if manifest.MigrationVersion > latest {
			return Manifest{}, fmt.Errorf("%w (yedek=%d, bu sürüm=%d)", ErrArchiveNewer, manifest.MigrationVersion, latest)
		}
		if e.keyFingerprint != "" && manifest.MasterKeyFingerprint != "" && e.keyFingerprint != manifest.MasterKeyFingerprint {
			return Manifest{}, fmt.Errorf("%w: yedek farklı bir VARYAONE_MASTER_KEY ile alınmış, şifreli alanlar okunamaz (--force ile geçilebilir)", ErrKeyMismatch)
		}
	}

	header, err = archive.Next()
	if err != nil || header.Name != dumpEntry {
		return Manifest{}, fmt.Errorf("geçersiz .varya arşivi: %q bekleniyordu", dumpEntry)
	}
	dumpFile, err := os.CreateTemp("", "varya-restore-*.pgdump")
	if err != nil {
		return Manifest{}, err
	}
	dumpPath := dumpFile.Name()
	defer func() {
		_ = dumpFile.Close()
		_ = os.Remove(dumpPath)
	}()
	digest := sha256.New()
	dumpN, err := io.Copy(io.MultiWriter(dumpFile, digest), archive)
	if err != nil {
		return Manifest{}, fmt.Errorf("veritabanı dökümü çıkarılamadı: %w", err)
	}
	if dumpN != manifest.DatabaseDumpSize {
		return Manifest{}, fmt.Errorf("veritabanı dökümü boyutu uyuşmuyor (%d != %d)", dumpN, manifest.DatabaseDumpSize)
	}
	if got := hex.EncodeToString(digest.Sum(nil)); got != manifest.DatabaseDumpSHA256 {
		return Manifest{}, fmt.Errorf("veritabanı dökümü bozuk: sağlama uyuşmuyor")
	}
	if err = dumpFile.Close(); err != nil {
		return Manifest{}, err
	}

	// Stage the storage tree BEFORE touching the database: a truncated archive or
	// a checksum mismatch here must abort with nothing changed.
	var stage string
	if e.storageRoot != "" {
		e.cleanupInternalDirs()
		stage, err = e.stageStorage(ctx, archive, manifest)
		if err != nil {
			return Manifest{}, err
		}
		defer func() {
			if stage != "" {
				_ = os.RemoveAll(stage)
			}
		}()
	}

	e.terminateOtherConnections(ctx)
	if err = e.runRestore(ctx, dumpPath); err != nil {
		return Manifest{}, err
	}
	if err = e.restoreAppRole(ctx); err != nil {
		return Manifest{}, fmt.Errorf("restore varyaone_app role: %w", err)
	}

	if stage != "" {
		if err = e.swapStorage(stage); err != nil {
			return manifest, fmt.Errorf("%w: %v", ErrStoragePartial, err)
		}
		stage = ""
	}
	return manifest, nil
}

// Verify reads the whole archive and checks every checksum in the manifest
// (database dump plus each storage object) without touching the database or the
// storage tree. It is used as a pre-flight gate before an update or a restore.
func (e *Engine) Verify(ctx context.Context, r io.Reader) (Manifest, error) {
	archive := tar.NewReader(r)

	header, err := archive.Next()
	if err != nil || header.Name != manifestEntry {
		return Manifest{}, fmt.Errorf("geçersiz .varya arşivi: ilk giriş %q", manifestName(header))
	}
	var manifest Manifest
	if err = json.NewDecoder(archive).Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("manifest okunamadı: %w", err)
	}
	if manifest.FormatVersion != FormatVersion {
		return Manifest{}, fmt.Errorf("desteklenmeyen yedek format sürümü %d", manifest.FormatVersion)
	}

	header, err = archive.Next()
	if err != nil || header.Name != dumpEntry {
		return Manifest{}, fmt.Errorf("geçersiz .varya arşivi: %q bekleniyordu", dumpEntry)
	}
	dumpDigest := sha256.New()
	dumpN, err := io.Copy(dumpDigest, archive)
	if err != nil {
		return Manifest{}, fmt.Errorf("veritabanı dökümü okunamadı: %w", err)
	}
	if dumpN != manifest.DatabaseDumpSize {
		return Manifest{}, fmt.Errorf("veritabanı dökümü boyutu uyuşmuyor (%d != %d)", dumpN, manifest.DatabaseDumpSize)
	}
	if got := hex.EncodeToString(dumpDigest.Sum(nil)); got != manifest.DatabaseDumpSHA256 {
		return Manifest{}, fmt.Errorf("veritabanı dökümü bozuk: sağlama uyuşmuyor")
	}

	want := make(map[string]ObjectEntry, len(manifest.Objects))
	for _, object := range manifest.Objects {
		want[object.Key] = object
	}
	seen := make(map[string]bool, len(want))
	for {
		if err = ctx.Err(); err != nil {
			return Manifest{}, err
		}
		header, err = archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, err
		}
		if !strings.HasPrefix(header.Name, storagePrefix) {
			continue
		}
		key := strings.TrimPrefix(header.Name, storagePrefix)
		expected, known := want[key]
		if !known {
			continue
		}
		objectDigest := sha256.New()
		n, copyErr := io.Copy(objectDigest, archive)
		if copyErr != nil {
			return Manifest{}, fmt.Errorf("depolama objesi %q okunamadı: %w", key, copyErr)
		}
		if n != expected.Size || hex.EncodeToString(objectDigest.Sum(nil)) != expected.SHA256 {
			return Manifest{}, fmt.Errorf("depolama objesi %q bozuk: sağlama uyuşmuyor", key)
		}
		seen[key] = true
	}
	if missing := missingKeys(want, seen); len(missing) > 0 {
		return Manifest{}, fmt.Errorf("arşivde %d depolama objesi eksik: %s", len(missing), strings.Join(clip(missing, 5), ", "))
	}
	return manifest, nil
}

func (e *Engine) runRestore(ctx context.Context, dumpPath string) error {
	cmd := exec.CommandContext(ctx, e.pgRestore,
		"--clean", "--if-exists", "--no-owner", "--no-privileges",
		"--single-transaction", "--exit-on-error",
		"--dbname="+e.databaseURL, dumpPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// cleanupInternalDirs removes any leftover engine working directories (snapshot,
// restore-staging, retired) from a previous crashed run. Both entry points that
// call it (Create, Restore) are serialized by their callers, so this cannot race
// a live operation.
func (e *Engine) cleanupInternalDirs() {
	if e.storageRoot == "" {
		return
	}
	entries, err := os.ReadDir(e.storageRoot)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, snapPrefix) ||
			strings.HasPrefix(name, stagePrefix) ||
			strings.HasPrefix(name, retiredPrefix) {
			_ = os.RemoveAll(filepath.Join(e.storageRoot, name))
		}
	}
}

// stageStorage extracts every storage/* entry the manifest expects into a
// working directory INSIDE the storage root, verifying size and checksum for
// each. It returns an error (and leaves nothing behind) unless every manifest
// object was present and intact.
func (e *Engine) stageStorage(ctx context.Context, archive *tar.Reader, manifest Manifest) (string, error) {
	if err := os.MkdirAll(e.storageRoot, 0o750); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(e.storageRoot, stagePrefix+e.now().UTC().Format("20060102T150405Z")+"-")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(stage)
		}
	}()

	want := make(map[string]ObjectEntry, len(manifest.Objects))
	for _, object := range manifest.Objects {
		want[object.Key] = object
	}
	seen := make(map[string]bool, len(want))

	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		header, nextErr := archive.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return "", nextErr
		}
		if !strings.HasPrefix(header.Name, storagePrefix) {
			continue
		}
		key := strings.TrimPrefix(header.Name, storagePrefix)
		expected, known := want[key]
		if !known {
			continue
		}
		destination := filepath.Join(stage, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
			return "", err
		}
		file, openErr := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if openErr != nil {
			return "", openErr
		}
		objectDigest := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(file, objectDigest), archive)
		closeErr := file.Close()
		if copyErr != nil {
			return "", fmt.Errorf("depolama objesi %q çıkarılamadı: %w", key, copyErr)
		}
		if closeErr != nil {
			return "", closeErr
		}
		if n != expected.Size {
			return "", fmt.Errorf("depolama objesi %q boyutu uyuşmuyor (%d != %d)", key, n, expected.Size)
		}
		if got := hex.EncodeToString(objectDigest.Sum(nil)); got != expected.SHA256 {
			return "", fmt.Errorf("depolama objesi %q bozuk: sağlama uyuşmuyor", key)
		}
		seen[key] = true
	}

	if missing := missingKeys(want, seen); len(missing) > 0 {
		return "", fmt.Errorf("arşivde %d depolama objesi eksik: %s", len(missing), strings.Join(clip(missing, 5), ", "))
	}

	ok = true
	return stage, nil
}

// swapStorage swaps the fully-staged tree in for the live content by moving
// top-level entries: the current real entries are renamed aside into a retired
// directory, then the staged entries are renamed into place. The storage root
// itself is never renamed (in production it is a bind mount, and renaming a
// mount point fails with EBUSY). Each rename is atomic and the number of
// top-level entries is tiny, so the inconsistency window is a few renames wide;
// on failure the moved-aside entries are restored.
func (e *Engine) swapStorage(stage string) error {
	root := e.storageRoot
	if err := os.MkdirAll(root, 0o750); err != nil {
		return err
	}

	retired, err := os.MkdirTemp(root, retiredPrefix+e.now().UTC().Format("20060102T150405Z")+"-")
	if err != nil {
		return err
	}

	liveEntries, err := os.ReadDir(root)
	if err != nil {
		return err
	}

	var moved []string // names relocated into retired/
	restore := func() {
		for _, name := range moved {
			_ = os.Rename(filepath.Join(retired, name), filepath.Join(root, name))
		}
	}

	for _, entry := range liveEntries {
		name := entry.Name()
		if isInternalName(name) {
			continue
		}
		if err := os.Rename(filepath.Join(root, name), filepath.Join(retired, name)); err != nil {
			restore()
			_ = os.RemoveAll(retired)
			return err
		}
		moved = append(moved, name)
	}

	stagedEntries, err := os.ReadDir(stage)
	if err != nil {
		restore()
		_ = os.RemoveAll(retired)
		return err
	}
	for _, entry := range stagedEntries {
		name := entry.Name()
		if err := os.Rename(filepath.Join(stage, name), filepath.Join(root, name)); err != nil {
			// Roll back: pull out whatever staged entries already landed, then
			// put the live entries back.
			for _, s := range stagedEntries {
				if s.Name() == name {
					break
				}
				_ = os.RemoveAll(filepath.Join(root, s.Name()))
			}
			restore()
			_ = os.RemoveAll(retired)
			return err
		}
	}

	_ = os.RemoveAll(retired)
	_ = os.RemoveAll(stage)
	return nil
}

// snapshotStorage hard-links (or, on failure, copies) the storage tree into a
// working directory INSIDE the storage root and hashes the stable copy. Objects
// that vanish before they can be captured are returned in the skipped list
// rather than failing the run.
func (e *Engine) snapshotStorage(ctx context.Context) (snapshotDir string, objects []ObjectEntry, skipped []string, err error) {
	if e.storageRoot == "" {
		return "", nil, nil, nil
	}
	info, statErr := os.Stat(e.storageRoot)
	if errors.Is(statErr, os.ErrNotExist) || (statErr == nil && !info.IsDir()) {
		return "", nil, nil, nil
	}
	if statErr != nil {
		return "", nil, nil, statErr
	}

	snapshotDir, err = os.MkdirTemp(e.storageRoot, snapPrefix+e.now().UTC().Format("20060102T150405Z")+"-")
	if err != nil {
		return "", nil, nil, fmt.Errorf("depolama snapshot dizini oluşturulamadı: %w", err)
	}
	done := false
	defer func() {
		if !done {
			_ = os.RemoveAll(snapshotDir)
		}
	}()

	linkErr := filepath.WalkDir(e.storageRoot, func(pathName string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			// A directory that disappeared mid-walk is tolerable; anything else is not.
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// Never descend into the engine's own working directories.
		if isInternalName(entry.Name()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(e.storageRoot, pathName)
		if relErr != nil {
			return relErr
		}
		key := filepath.ToSlash(rel)
		destination := filepath.Join(snapshotDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
			return err
		}
		if err := os.Link(pathName, destination); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				skipped = append(skipped, key)
				return nil
			}
			// Cross-device or a link-count limit: fall back to a byte copy.
			if copyErr := copyFile(pathName, destination); copyErr != nil {
				if errors.Is(copyErr, os.ErrNotExist) || errors.Is(copyErr, os.ErrPermission) {
					skipped = append(skipped, key)
					return nil
				}
				return copyErr
			}
		}
		return nil
	})
	if linkErr != nil {
		return "", nil, nil, linkErr
	}

	hashErr := filepath.WalkDir(snapshotDir, func(pathName string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(snapshotDir, pathName)
		if relErr != nil {
			return relErr
		}
		file, openErr := os.Open(pathName)
		if openErr != nil {
			return openErr
		}
		digest := sha256.New()
		size, copyErr := io.Copy(digest, file)
		_ = file.Close()
		if copyErr != nil {
			return copyErr
		}
		objects = append(objects, ObjectEntry{
			Key:    filepath.ToSlash(rel),
			Size:   size,
			SHA256: hex.EncodeToString(digest.Sum(nil)),
		})
		return nil
	})
	if hashErr != nil {
		return "", nil, nil, hashErr
	}

	slices.SortFunc(objects, func(a, b ObjectEntry) int { return strings.Compare(a.Key, b.Key) })
	slices.Sort(skipped)
	done = true
	return snapshotDir, objects, skipped, nil
}

func (e *Engine) readSourceMeta(ctx context.Context) (migrationVersion int64, pgServerNum int, err error) {
	conn, err := pgx.Connect(ctx, e.databaseURL)
	if err != nil {
		return 0, 0, fmt.Errorf("connect for metadata: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if err = conn.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM platform_schema_migrations`).Scan(&migrationVersion); err != nil {
		return 0, 0, fmt.Errorf("read migration version: %w", err)
	}
	// server_version_num: 180004 for 18.4. Recorded so a restore can tell it is
	// loading a dump taken on a different PostgreSQL major.
	if err = conn.QueryRow(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&pgServerNum); err != nil {
		return 0, 0, fmt.Errorf("read server version: %w", err)
	}
	return migrationVersion, pgServerNum, nil
}

// restoreAppRole re-creates the varyaone_app role and its privileges after a
// restore. pg_dump runs with --no-privileges, so grants (and, on a fresh
// cluster, the role itself) do not come back with the dump. The row-level
// security policies do — they are schema objects — so without this step a
// restored database would reject every varyaone_app connection.
func (e *Engine) restoreAppRole(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, e.databaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if _, err := conn.Exec(ctx, migrations.AppRoleSQL()); err != nil {
		return err
	}
	return nil
}

// terminateOtherConnections best-effort frees the target database of other
// sessions so pg_restore's DROP statements do not deadlock behind the running
// application pool. Failures are non-fatal.
func (e *Engine) terminateOtherConnections(ctx context.Context) {
	conn, err := pgx.Connect(ctx, e.databaseURL)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	_, _ = conn.Exec(ctx, `
		SELECT pg_terminate_backend(pid)
		FROM pg_stat_activity
		WHERE datname = current_database() AND pid <> pg_backend_pid()`)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func missingKeys(want map[string]ObjectEntry, seen map[string]bool) []string {
	var missing []string
	for key := range want {
		if !seen[key] {
			missing = append(missing, key)
		}
	}
	slices.Sort(missing)
	return missing
}

// clip returns at most n entries, appending an ellipsis marker when it truncates.
func clip(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	return append(items[:n:n], "…")
}

func databaseName(databaseURL string) string {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(parsed.Path, "/")
}

func manifestName(header *tar.Header) string {
	if header == nil {
		return "<yok>"
	}
	return header.Name
}

// SuggestedFilename returns a stable, sortable archive name for a given time.
func SuggestedFilename(at time.Time) string {
	return fmt.Sprintf("varyaone-%s.varya", at.UTC().Format("20060102-1504"))
}
