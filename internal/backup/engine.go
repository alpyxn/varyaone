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
	"path"
	"path/filepath"
	"runtime"
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

	// StorageMode states what the producer captured. Empty in archives written
	// before the field existed; see Manifest.StorageModeOrInferred, which is
	// what every reader should use. It is additive, so older readers ignore it
	// and older archives stay readable.
	StorageMode StorageMode `json:"storage_mode,omitempty"`
	// StorageProvider records which provider the installation was using. A
	// restore compares it so an archive taken from an object-store deployment
	// is not silently presented as a full backup of a local-storage one.
	StorageProvider string `json:"storage_provider,omitempty"`
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
	// PostgresBinDir optionally points at bundled PostgreSQL client tools. Empty
	// retains PATH discovery for container/server deployments.
	PostgresBinDir string
	// StorageProvider is the configured provider name ("local", "s3", …). The
	// engine backs up objects only for the local provider; recording the name
	// is what lets an archive say it is database-only rather than appear to be
	// a complete backup that happens to contain no files.
	StorageProvider string
	// AppDatabaseURL is the non-superuser connection the application serves
	// traffic with. A restore uses it to prove, before committing, that the
	// restored database is actually reachable by the role that will have to
	// read it — a restore that only the owner can open is a restore that fails
	// the moment traffic returns.
	AppDatabaseURL string
}

// Engine creates and restores `.varya` archives.
type Engine struct {
	databaseURL     string
	appDatabaseURL  string
	storageRoot     string
	storageProvider string
	release         string
	keyFingerprint  string
	pgDump          string
	pgRestore       string
	now             func() time.Time
}

// ErrToolMissing is returned when the PostgreSQL client binaries are absent.
var ErrToolMissing = errors.New("postgresql-client (pg_dump/pg_restore) kurulu değil")

// ErrArchiveNewer is returned by Restore when the archive was produced by a
// newer schema than this binary understands and Force was not set.
var ErrArchiveNewer = errors.New("yedek bu sürümden daha yeni bir şema içeriyor")

// ErrKeyMismatch is returned by Restore when the archive was produced under a
// different master key and Force was not set.
var ErrKeyMismatch = errors.New("yedek farklı bir ana anahtar ile alınmış")

// ErrSystemInconsistent marks every Restore failure that happened AFTER the
// database transaction committed. At that point the installation no longer
// matches either the state it started in or, necessarily, the archive: the
// database is new, but role privileges or the storage tree may not be. Callers
// must keep such an installation in maintenance — no traffic, no workers — until
// an operator has resolved it. Errors that do NOT wrap this one are proof the
// live system was never touched.
var ErrSystemInconsistent = errors.New("sistem tutarsız durumda: veritabanı değişti, geçiş tamamlanamadı")

// ErrStoragePartial is returned by Restore when the database was restored
// successfully but swapping the storage tree into place failed. The database is
// on the new content; the storage tree may be stale.
var ErrStoragePartial = fmt.Errorf("%w: veritabanı geri yüklendi ancak depolama dosyaları yerine konulamadı", ErrSystemInconsistent)

// NewEngine resolves the client binaries and returns a ready engine.
func NewEngine(opts Options) (*Engine, error) {
	if strings.TrimSpace(opts.DatabaseURL) == "" {
		return nil, errors.New("backup: DatabaseURL is required")
	}
	pgDump, err := postgresTool(opts.PostgresBinDir, "pg_dump")
	if err != nil {
		return nil, ErrToolMissing
	}
	pgRestore, err := postgresTool(opts.PostgresBinDir, "pg_restore")
	if err != nil {
		return nil, ErrToolMissing
	}
	engine := &Engine{
		databaseURL:     opts.DatabaseURL,
		appDatabaseURL:  strings.TrimSpace(opts.AppDatabaseURL),
		storageRoot:     strings.TrimSpace(opts.StorageRoot),
		storageProvider: strings.TrimSpace(opts.StorageProvider),
		release:         opts.Release,
		pgDump:          pgDump,
		pgRestore:       pgRestore,
		now:             time.Now,
	}
	if len(opts.MasterKey) > 0 {
		sum := sha256.Sum256(opts.MasterKey)
		engine.keyFingerprint = hex.EncodeToString(sum[:])[:16]
	}
	return engine, nil
}

func postgresTool(binDir, name string) (string, error) {
	if strings.TrimSpace(binDir) == "" {
		return exec.LookPath(name)
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(binDir, name)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", ErrToolMissing
	}
	return path, nil
}

// Create streams a complete `.varya` archive to w and returns the manifest it
// wrote as the first entry.
func (e *Engine) Create(ctx context.Context, w io.Writer) (Manifest, error) {
	// Decide, and check, what this backup is going to contain — before pausing
	// any writers. An installation whose storage volume failed to mount must
	// hear about it here, not receive a clean-looking archive that silently
	// contains none of its files.
	storageMode, err := e.storagePreflight()
	if err != nil {
		return Manifest{}, err
	}

	// Pin one instant. The database snapshot, the file tree and the metadata
	// all come from inside it, so the archive describes a state the
	// installation actually had rather than a blend of two.
	snapshot, err := e.beginSourceSnapshot(ctx)
	if err != nil {
		return Manifest{}, err
	}
	defer snapshot.Close(ctx)
	pinnedAt := e.now().UTC()

	// Writers are paused from here. Everything between this point and
	// releaseBarrier is the measured pause, so it contains only the hard-link
	// walk — no dumping, no hashing, no compression.
	e.cleanupInternalDirs()
	snapshotDir, objects, skipped, err := e.snapshotStorage(ctx)
	if err != nil {
		return Manifest{}, err
	}
	if snapshotDir != "" {
		defer func() { _ = os.RemoveAll(snapshotDir) }()
	}
	migrationVersion, pgServerNum, err := snapshot.meta(ctx)
	if err != nil {
		return Manifest{}, err
	}
	snapshot.releaseBarrier(ctx)
	storageCapturedAt := e.now().UTC()

	// pg_dump now joins the snapshot taken above. Without --snapshot it would
	// start its own transaction here and capture the database as of now, which
	// is after the file tree was pinned and after writers resumed.
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
	cmd := exec.CommandContext(ctx, e.pgDump,
		"--format=custom", "--no-owner", "--no-privileges",
		"--snapshot="+snapshot.snapshotID, e.databaseURL)
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

	if len(skipped) > 0 {
		// Files vanished or were unreadable during the walk. The archive is
		// still useful and must not claim to be a full recovery point.
		storageMode = StorageDegraded
	}

	manifest := Manifest{
		FormatVersion:        FormatVersion,
		CreatedAt:            pinnedAt,
		StorageMode:          storageMode,
		StorageProvider:      e.storageProvider,
		Release:              e.release,
		DatabaseName:         databaseName(e.databaseURL),
		MigrationVersion:     migrationVersion,
		PostgresServerNum:    pgServerNum,
		MasterKeyFingerprint: e.keyFingerprint,
		DatabaseDumpSize:     dumpSize,
		DatabaseDumpSHA256:   hex.EncodeToString(dumpDigest.Sum(nil)),
		Objects:              objects,
		DumpStartedAt:        pinnedAt,
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
		path := filepath.Join(snapshotDir, filepath.FromSlash(object.Key))
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

// RestoreJournal receives the engine's phase transitions so a caller can make
// them durable before the engine acts on them. The method names describe what
// the engine is about to do, not a generic "phase", because the only reason
// this interface exists is the distinction between the two sides of the switch.
//
// Switching is called BEFORE the first irreversible step and must have reached
// stable storage by the time it returns; everything after it may have changed
// the live installation. A nil journal disables recording, which is correct for
// tests and for read-only callers but never for a real restore.
type RestoreJournal interface {
	// Prepared reports that the archive is fully staged and verified and the
	// live installation has not been touched.
	Prepared(fields map[string]any) error
	// Switching is the point of no return: the intent to commit the database.
	Switching(fields map[string]any) error
	// Switched reports that the database transaction committed.
	Switched(fields map[string]any) error
	// Committed reports that every part of the switch finished.
	Committed(fields map[string]any) error
}

// RestoreOptions tunes a Restore call.
type RestoreOptions struct {
	// Force restores even when the archive schema is newer than this binary.
	//
	// Force covers exactly two checks: the schema-version gate and the master
	// key fingerprint. It has never covered — and must never cover — checksum
	// verification, path validation, resource budgets, the lease or disk
	// safety. Those are not preferences an operator can overrule; they are the
	// reasons the restore is safe to attempt at all.
	Force bool
	// Mode selects the staged candidate-database restore (default) or the
	// weaker in-place one. An installation that cannot support the former is
	// told so rather than silently given the latter.
	Mode RestoreMode
	// SkipMigrations leaves the candidate at the archive's schema version
	// instead of bringing it forward to this binary's. It exists for restoring
	// into an older release deliberately; the default brings the schema up,
	// because otherwise the application starts against a schema it has already
	// moved past.
	SkipMigrations bool
	// Journal, when set, records the operation's progress durably. Restore
	// fails rather than proceeding if a journal write fails: an irreversible
	// step whose intent could not be recorded is a step nobody will be able to
	// reconstruct afterwards, which is worse than not taking it.
	Journal RestoreJournal
}

// Restore rebuilds the database and the local storage tree from r. It is a
// destructive whole-system operation, but it is staged: the database dump and
// the entire storage tree are extracted and checksum-verified into sibling temp
// locations FIRST. Only once every byte checks out does it run pg_restore and
// swap the storage tree in with an atomic rename. A failure before that point
// leaves the live system untouched.
func (e *Engine) Restore(ctx context.Context, r io.Reader, opts RestoreOptions) (Manifest, error) {
	archive := tar.NewReader(r)

	manifest, err := readManifest(archive)
	if err != nil {
		return Manifest{}, err
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

	// An archive that carries storage objects cannot be restored into an
	// installation that has no storage root: the database would come back
	// referencing files that were never written. Fail here, before anything
	// live is touched, rather than silently skipping the storage half.
	if e.storageRoot == "" && len(manifest.Objects) > 0 {
		return Manifest{}, fmt.Errorf("yedek %d depolama objesi içeriyor ancak bu kurulumda depolama kökü tanımlı değil", len(manifest.Objects))
	}

	// Check there is room before writing anything. The alternative is finding
	// out half-way through staging, with a partly-extracted tree on a full
	// disk and fewer options than there were a minute earlier.
	if err = e.PreflightCapacity(manifest); err != nil {
		return Manifest{}, err
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
	if err = verifyDumpEntry(archive, manifest, dumpFile); err != nil {
		return Manifest{}, err
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

	// Build the replacement database before touching the live one. Everything
	// in restoreDatabase happens on a database nothing is using, so any failure
	// inside it leaves the installation exactly as it was.
	switch opts.Mode {
	case "", ModeCandidate:
		return e.restoreViaCandidate(ctx, manifest, dumpPath, stage, opts)
	case ModeInPlace:
		return e.restoreInPlace(ctx, manifest, dumpPath, &stage, opts)
	default:
		return Manifest{}, fmt.Errorf("bilinmeyen geri yükleme modu %q", opts.Mode)
	}
}

// restoreViaCandidate loads the archive into a new database, brings its schema
// forward, proves the application role can open it, and only then swaps it in.
func (e *Engine) restoreViaCandidate(ctx context.Context, manifest Manifest, dumpPath, stage string, opts RestoreOptions) (Manifest, error) {
	session, err := e.openMaintenance(ctx)
	if err != nil {
		return Manifest{}, err
	}
	defer session.Close(ctx)

	allowed, err := session.canCreateDatabase(ctx)
	if err != nil {
		return Manifest{}, err
	}
	if !allowed {
		return Manifest{}, fmt.Errorf("%w: geri yükleme için CREATEDB yetkisi gerekiyor "+
			"(bilinçli olarak yerinde geri yükleme yapılacaksa --in-place)", ErrCandidateUnsupported)
	}

	now := e.now().UTC()
	candidate := candidateName(session.liveName, now)
	retired := retiredName(session.liveName, now)

	if err := session.create(ctx, candidate); err != nil {
		return Manifest{}, err
	}
	committed := false
	defer func() {
		// Until the swap succeeds the candidate is scratch: it holds a second
		// copy of data the archive still has. After the swap it IS the
		// installation and must never be dropped here.
		if !committed {
			_ = session.drop(context.WithoutCancel(ctx), candidate)
		}
	}()

	candidateDSN, err := withDatabase(e.databaseURL, candidate)
	if err != nil {
		return Manifest{}, err
	}
	if err := e.restoreInto(ctx, candidateDSN, dumpPath); err != nil {
		return Manifest{}, err
	}
	if err := e.prepareCandidate(ctx, candidateDSN, candidate, opts); err != nil {
		return Manifest{}, err
	}

	if err := journalPrepared(opts.Journal, map[string]any{
		"created_at": manifest.CreatedAt, "objects": len(manifest.Objects),
		"migration_version":  manifest.MigrationVersion,
		"candidate_database": candidate, "staged_storage": stage != "",
	}); err != nil {
		return Manifest{}, err
	}

	// The point of no return. The intent names both databases, so a crash
	// between the two renames leaves a record that says exactly which database
	// holds the old installation and which holds the new one.
	if err := journalSwitching(opts.Journal, map[string]any{
		"live": session.liveName, "candidate": candidate, "retired": retired,
	}); err != nil {
		return Manifest{}, err
	}
	if err := session.swap(ctx, candidate, retired); err != nil {
		// swap puts the old database back when it can; if it could not, its
		// error says so and the caller must treat the system as inconsistent.
		if strings.Contains(err.Error(), retired) {
			return manifest, fmt.Errorf("%w: %v", ErrSystemInconsistent, err)
		}
		return Manifest{}, err
	}
	committed = true

	if err := journalSwitched(opts.Journal, map[string]any{"retired_database": retired}); err != nil {
		return manifest, fmt.Errorf("%w: veritabanı değişti ancak kayıt yazılamadı: %v", ErrSystemInconsistent, err)
	}

	if stage != "" {
		if err := e.swapStorage(stage); err != nil {
			return manifest, fmt.Errorf("%w: %v", ErrStoragePartial, err)
		}
	}

	// Health, after the switch and before anybody is told it worked: open the
	// live DSN as the application role and read something real.
	if err := e.verifyLive(ctx); err != nil {
		return manifest, fmt.Errorf("%w: geri yükleme sonrası uygulama kontrolü başarısız: %v", ErrSystemInconsistent, err)
	}
	if err := journalCommitted(opts.Journal, map[string]any{
		"objects": len(manifest.Objects), "retired_database": retired,
	}); err != nil {
		return manifest, fmt.Errorf("%w: geri yükleme bitti ancak kayıt yazılamadı: %v", ErrSystemInconsistent, err)
	}
	return manifest, nil
}

// prepareCandidate brings a freshly loaded candidate up to the state the
// application expects: current schema, the application role with its grants,
// and a proven login.
//
// Doing all three here rather than after the switch is the difference between
// finding a problem while the installation is still untouched and finding it
// while it is already live on the new database.
func (e *Engine) prepareCandidate(ctx context.Context, candidateDSN, candidate string, opts RestoreOptions) error {
	conn, err := pgx.Connect(ctx, candidateDSN)
	if err != nil {
		return fmt.Errorf("aday veritabanına bağlanılamadı: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	if !opts.SkipMigrations {
		// pg_dump captured the schema as it was. If this binary is newer, the
		// forward migrations run here, on the candidate — not on a live system,
		// and not left for whatever happens to restart first afterwards.
		if err := migrations.New(conn).Up(ctx); err != nil {
			return fmt.Errorf("aday veritabanında migration uygulanamadı: %w", err)
		}
	}
	// pg_dump runs with --no-privileges, so grants — and on a fresh cluster the
	// role itself — do not come back with the dump. The row-level security
	// policies do, being schema objects, so without this the restored database
	// rejects every application connection.
	if _, err := conn.Exec(ctx, migrations.AppRoleSQL()); err != nil {
		return fmt.Errorf("aday veritabanında varyaone_app rolü kurulamadı: %w", err)
	}
	if err := e.verifyAppAccess(ctx, candidate); err != nil {
		return err
	}
	return nil
}

// verifyAppAccess opens the candidate as the application role and reads a real
// table. A restore that only the owner can open looks perfect right up to the
// moment traffic comes back.
func (e *Engine) verifyAppAccess(ctx context.Context, database string) error {
	if e.appDatabaseURL == "" {
		// No separate application role configured: the owner connection is the
		// serving connection, and it has already been used successfully.
		return nil
	}
	dsn, err := withDatabase(e.appDatabaseURL, database)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("uygulama rolü aday veritabanına bağlanamadı: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	var companies int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM companies`).Scan(&companies); err != nil {
		return fmt.Errorf("uygulama rolü geri yüklenen veriyi okuyamadı: %w", err)
	}
	return nil
}

// verifyLive repeats the access check against the switched-in database.
func (e *Engine) verifyLive(ctx context.Context) error {
	return e.verifyAppAccess(ctx, databaseName(e.databaseURL))
}

// restoreInPlace is the legacy path: load straight into the live database.
//
// It is kept because an installation without CREATEDB has no alternative, and
// it is never selected automatically. Its limits are real and unfixable here:
// `--clean` removes only what the archive contains, so objects belonging to a
// newer schema survive the restore, and there is no point after pg_restore
// begins at which the previous database still exists.
func (e *Engine) restoreInPlace(ctx context.Context, manifest Manifest, dumpPath string, stage *string, opts RestoreOptions) (Manifest, error) {
	if err := journalPrepared(opts.Journal, map[string]any{
		"created_at": manifest.CreatedAt, "objects": len(manifest.Objects),
		"migration_version": manifest.MigrationVersion, "mode": string(ModeInPlace),
		"staged_storage": *stage != "",
	}); err != nil {
		return Manifest{}, err
	}
	if err := journalSwitching(opts.Journal, map[string]any{
		"database": databaseName(e.databaseURL), "mode": string(ModeInPlace),
	}); err != nil {
		return Manifest{}, err
	}
	e.terminateOtherConnections(ctx)
	if err := e.runRestore(ctx, dumpPath); err != nil {
		// --single-transaction --exit-on-error, so the database rolled back and
		// the storage tree has not been touched: the installation is unchanged.
		return Manifest{}, err
	}
	if err := journalSwitched(opts.Journal, nil); err != nil {
		return manifest, fmt.Errorf("%w: veritabanı yüklendi ancak kayıt yazılamadı: %v", ErrSystemInconsistent, err)
	}
	if err := e.restoreAppRole(ctx); err != nil {
		return manifest, fmt.Errorf("%w: varyaone_app rolü geri yüklenemedi: %v", ErrSystemInconsistent, err)
	}
	if *stage != "" {
		if err := e.swapStorage(*stage); err != nil {
			return manifest, fmt.Errorf("%w: %v", ErrStoragePartial, err)
		}
		*stage = ""
	}
	if err := e.verifyLive(ctx); err != nil {
		return manifest, fmt.Errorf("%w: geri yükleme sonrası uygulama kontrolü başarısız: %v", ErrSystemInconsistent, err)
	}
	if err := journalCommitted(opts.Journal, map[string]any{"objects": len(manifest.Objects)}); err != nil {
		return manifest, fmt.Errorf("%w: geri yükleme bitti ancak kayıt yazılamadı: %v", ErrSystemInconsistent, err)
	}
	return manifest, nil
}

func journalPrepared(j RestoreJournal, fields map[string]any) error {
	if j == nil {
		return nil
	}
	return j.Prepared(fields)
}

func journalSwitching(j RestoreJournal, fields map[string]any) error {
	if j == nil {
		return nil
	}
	return j.Switching(fields)
}

func journalSwitched(j RestoreJournal, fields map[string]any) error {
	if j == nil {
		return nil
	}
	return j.Switched(fields)
}

func journalCommitted(j RestoreJournal, fields map[string]any) error {
	if j == nil {
		return nil
	}
	return j.Committed(fields)
}

// Verify reads the whole archive and checks every checksum in the manifest
// (database dump plus each storage object) without touching the database or the
// storage tree. It is used as a pre-flight gate before an update or a restore.
//
// Verify and Restore share one parser, so an archive Verify accepts is an
// archive Restore will accept and vice versa. Note what this does and does not
// prove: it proves the bytes are the bytes the manifest describes. It does not
// prove the archive came from a trusted producer, nor that restoring it will
// succeed.
func (e *Engine) Verify(ctx context.Context, r io.Reader) (Manifest, error) {
	archive := tar.NewReader(r)

	manifest, err := readManifest(archive)
	if err != nil {
		return Manifest{}, err
	}
	if err = verifyDumpEntry(archive, manifest, io.Discard); err != nil {
		return Manifest{}, err
	}
	err = walkStorageEntries(ctx, archive, manifest, func(key string, expected ObjectEntry, body io.Reader) error {
		return checkObject(key, expected, body, io.Discard)
	})
	if err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// readManifest reads and validates the archive's first entry. Every caller goes
// through here so a manifest that one entry point would reject cannot be
// accepted by another.
func readManifest(archive *tar.Reader) (Manifest, error) {
	header, err := archive.Next()
	if err != nil || header.Name != manifestEntry {
		return Manifest{}, invalidArchive("ilk giriş %q", manifestName(header))
	}
	if header.Typeflag != tar.TypeReg {
		return Manifest{}, invalidArchive("manifest girdisi düzenli dosya değil")
	}
	if header.Size > maxManifestBytes {
		return Manifest{}, invalidArchive("manifest çok büyük (%d bayt)", header.Size)
	}
	var manifest Manifest
	// Unknown fields are tolerated on purpose: new metadata may be added to the
	// manifest additively without making older archives unreadable.
	if err = json.NewDecoder(io.LimitReader(archive, maxManifestBytes)).Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("manifest okunamadı: %w", err)
	}
	if err = validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// verifyDumpEntry reads the database.dump entry, checks its size and checksum
// and, when sink is not io.Discard, writes the bytes out as it goes.
func verifyDumpEntry(archive *tar.Reader, manifest Manifest, sink io.Writer) error {
	header, err := archive.Next()
	if err != nil || header.Name != dumpEntry {
		return invalidArchive("%q bekleniyordu", dumpEntry)
	}
	if header.Typeflag != tar.TypeReg {
		return invalidArchive("%q düzenli dosya değil", dumpEntry)
	}
	if header.Size != manifest.DatabaseDumpSize {
		return invalidArchive("veritabanı dökümü boyutu uyuşmuyor (%d != %d)", header.Size, manifest.DatabaseDumpSize)
	}
	digest := sha256.New()
	// LimitReader guards against a header that lies about its size in the other
	// direction; the size comparison below catches a short entry.
	n, err := io.Copy(io.MultiWriter(sink, digest), io.LimitReader(archive, manifest.DatabaseDumpSize+1))
	if err != nil {
		return fmt.Errorf("veritabanı dökümü okunamadı: %w", err)
	}
	if n != manifest.DatabaseDumpSize {
		return invalidArchive("veritabanı dökümü boyutu uyuşmuyor (%d != %d)", n, manifest.DatabaseDumpSize)
	}
	if hex.EncodeToString(digest.Sum(nil)) != manifest.DatabaseDumpSHA256 {
		return invalidArchive("veritabanı dökümü bozuk: sağlama uyuşmuyor")
	}
	return nil
}

// walkStorageEntries drives the storage section of an archive through a single
// parser shared by Verify and Restore. It enforces the archive budget rules
// (no unexpected keys, no duplicates, no size drift, nothing missing) and hands
// each accepted entry's body to sink.
func walkStorageEntries(ctx context.Context, archive *tar.Reader, manifest Manifest, sink func(key string, expected ObjectEntry, body io.Reader) error) error {
	want := make(map[string]ObjectEntry, len(manifest.Objects))
	for _, object := range manifest.Objects {
		want[object.Key] = object
	}
	seen := make(map[string]bool, len(want))

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		key, ok, err := storageEntryKey(header)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		expected, known := want[key]
		if !known {
			return invalidArchive("arşiv manifestte bulunmayan %q objesini içeriyor", key)
		}
		if seen[key] {
			return invalidArchive("arşiv %q objesini birden fazla kez içeriyor", key)
		}
		if header.Size != expected.Size {
			return invalidArchive("depolama objesi %q boyutu uyuşmuyor (%d != %d)", key, header.Size, expected.Size)
		}
		if err := sink(key, expected, io.LimitReader(archive, expected.Size+1)); err != nil {
			return err
		}
		seen[key] = true
	}

	if missing := missingKeys(want, seen); len(missing) > 0 {
		return invalidArchive("arşivde %d depolama objesi eksik: %s", len(missing), strings.Join(clip(missing, 5), ", "))
	}
	return nil
}

// checkObject streams one storage object through a SHA-256 digest into sink and
// fails unless both the length and the checksum match the manifest.
func checkObject(key string, expected ObjectEntry, body io.Reader, sink io.Writer) error {
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(sink, digest), body)
	if err != nil {
		return fmt.Errorf("depolama objesi %q okunamadı: %w", key, err)
	}
	if n != expected.Size {
		return invalidArchive("depolama objesi %q boyutu uyuşmuyor (%d != %d)", key, n, expected.Size)
	}
	if hex.EncodeToString(digest.Sum(nil)) != expected.SHA256 {
		return invalidArchive("depolama objesi %q bozuk: sağlama uyuşmuyor", key)
	}
	return nil
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

// cleanupInternalDirs removes leftover engine scratch directories from a
// previous crashed run.
//
// It deliberately removes only snapshot and staging directories. Both hold
// nothing but a second copy of data that still exists elsewhere: a snapshot is
// hard-links to live objects, a stage is content that can be re-extracted from
// the archive. A `.varya-retired-*` directory is the opposite — after a crash
// part-way through swapStorage it can be the ONLY copy of the installation's
// files, so it is never deleted here. Retired directories are reported instead
// and must be resolved by an operator (or by a future recovery pass that can
// read the operation journal); deleting one on the strength of its name alone
// is how a crash turns into data loss.
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
		if strings.HasPrefix(name, snapPrefix) || strings.HasPrefix(name, stagePrefix) {
			_ = os.RemoveAll(filepath.Join(e.storageRoot, name))
		}
	}
}

// RetiredDirs lists `.varya-retired-*` directories left in the storage root.
// A non-empty result means a previous restore was interrupted between moving
// the old files aside and putting the new ones in place: the directories hold
// recovery data and the installation needs an operator's attention.
func (e *Engine) RetiredDirs() []string {
	if e.storageRoot == "" {
		return nil
	}
	entries, err := os.ReadDir(e.storageRoot)
	if err != nil {
		return nil
	}
	var retired []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), retiredPrefix) {
			retired = append(retired, filepath.Join(e.storageRoot, entry.Name()))
		}
	}
	slices.Sort(retired)
	return retired
}

// stageStorage extracts every storage/* entry the manifest expects into a
// working directory INSIDE the storage root, verifying size and checksum for
// each. It returns an error (and leaves nothing behind) unless every manifest
// object was present and intact.
//
// Extraction is confined to the staging directory by an os.Root handle: every
// path in the archive is resolved relative to that root by the kernel-backed
// API, which refuses to traverse out of it and never follows a symlink. The key
// validation in validateObjectKey already rejects escaping paths, so this is the
// second of two independent barriers — a correct checksum on a hostile key must
// not be able to put a byte anywhere but here.
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

	root, err := os.OpenRoot(stage)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()

	err = walkStorageEntries(ctx, archive, manifest, func(key string, expected ObjectEntry, body io.Reader) error {
		if parent := path.Dir(key); parent != "." {
			if err := root.MkdirAll(filepath.FromSlash(parent), 0o750); err != nil {
				return err
			}
		}
		// O_EXCL: the staging directory is freshly created and walkStorageEntries
		// rejects duplicate keys, so an existing name here means something is
		// wrong and must not be overwritten.
		file, openErr := root.OpenFile(filepath.FromSlash(key), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if openErr != nil {
			return fmt.Errorf("depolama objesi %q oluşturulamadı: %w", key, openErr)
		}
		checkErr := checkObject(key, expected, body, file)
		closeErr := file.Close()
		if checkErr != nil {
			return checkErr
		}
		return closeErr
	})
	if err != nil {
		return "", err
	}

	ok = true
	return stage, nil
}

// swapStorage swaps the fully-staged tree in for the live content by moving
// top-level entries: the current real entries are renamed aside into a retired
// directory, then the staged entries are renamed into place. The storage root
// itself is never renamed (in production it is a bind mount, and renaming a
// mount point fails with EBUSY). Each rename is atomic and the number of
// top-level entries is tiny, so the inconsistency window is a few renames wide.
//
// On failure it tries to put the live entries back. Whether or not that
// succeeds, the retired directory is kept: it holds the installation's previous
// files, and while the outcome of a rollback is in any doubt that copy is the
// only thing standing between an interrupted restore and permanent data loss.
// A caller that sees an error here must treat the installation as inconsistent
// and leave it in maintenance.
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
	// rollback returns the names it could NOT put back. An empty result means
	// the live tree is exactly as it was before the swap started.
	rollback := func() []string {
		var stuck []string
		for _, name := range moved {
			if renameErr := os.Rename(filepath.Join(retired, name), filepath.Join(root, name)); renameErr != nil {
				stuck = append(stuck, name)
			}
		}
		return stuck
	}
	fail := func(cause error) error {
		stuck := rollback()
		if len(stuck) > 0 {
			return fmt.Errorf("%w; geri alma da başarısız: %s hâlâ %s içinde",
				cause, strings.Join(clip(stuck, 5), ", "), retired)
		}
		return fmt.Errorf("%w (önceki dosyalar korundu: %s)", cause, retired)
	}

	for _, entry := range liveEntries {
		name := entry.Name()
		if isInternalName(name) {
			continue
		}
		if err := os.Rename(filepath.Join(root, name), filepath.Join(retired, name)); err != nil {
			return fail(err)
		}
		moved = append(moved, name)
	}

	stagedEntries, err := os.ReadDir(stage)
	if err != nil {
		return fail(err)
	}
	var landed []string
	for _, entry := range stagedEntries {
		name := entry.Name()
		if err := os.Rename(filepath.Join(stage, name), filepath.Join(root, name)); err != nil {
			// Pull out whatever staged entries already landed so the live
			// entries have somewhere to go back to. The staged content is not
			// recovery data — it can be re-extracted from the archive — so
			// removing it here is safe in a way removing `retired` is not.
			for _, name := range landed {
				_ = os.RemoveAll(filepath.Join(root, name))
			}
			return fail(err)
		}
		landed = append(landed, name)
	}

	// Past this point the new tree is live and complete. The retired copy is now
	// superseded rather than load-bearing, so it may go.
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
		// WalkDir does not follow symlinks, so entry.Type() is the lstat type.
		// Only regular files may enter a backup: following a symlink would pull
		// a file from outside the storage root into the archive under a storage
		// key, and opening a FIFO or a device would block the backup outright.
		if !entry.Type().IsRegular() {
			return fmt.Errorf("depolama kökünde düzenli olmayan dosya: %q (%s)", pathName, entry.Type())
		}
		rel, relErr := filepath.Rel(e.storageRoot, pathName)
		if relErr != nil {
			return relErr
		}
		key := filepath.ToSlash(rel)
		// The same gate the read side uses. A key the engine would refuse to
		// restore must never be written into an archive in the first place.
		if err := validateObjectKey(key); err != nil {
			return err
		}
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
		if !entry.Type().IsRegular() {
			return fmt.Errorf("snapshot'ta düzenli olmayan dosya: %q (%s)", pathName, entry.Type())
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

// copyFile is the cross-device fallback for the hard-link snapshot. It opens
// the source with O_NOFOLLOW and re-checks through the file descriptor that it
// really is a regular file, so a symlink swapped in between the walk's lstat
// and this open cannot redirect the copy.
func copyFile(src, dst string) error {
	in, err := os.OpenFile(src, os.O_RDONLY|openNoFollowFlag, 0)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("düzenli olmayan dosya kopyalanamaz: %q (%s)", src, info.Mode())
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
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
