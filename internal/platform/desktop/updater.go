package desktop

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/config"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/update"
)

// Updater applies a release by swapping prebuilt Windows artifacts. It mirrors
// the phases and rollback contract of deploy/varyaone-update-agent.sh +
// `deploy.sh update`, reporting progress through the same update.Service state
// machine so the control panel and web UI show identical progress.
type Updater struct {
	Layout   Layout
	HTTPPort int
	Logger   *slog.Logger
}

// NewUpdater builds an Updater with discovered defaults.
func NewUpdater(logger *slog.Logger) *Updater {
	s := NewSupervisor(logger)
	return &Updater{Layout: s.Layout, HTTPPort: s.HTTPPort, Logger: logger}
}

// updateHTTPClient downloads a release artifact. The timeout is generous (large
// bundles on slow links) but bounded so a dead connection cannot hang the
// updater forever; the request context still cancels it earlier on Ctrl+C.
var updateHTTPClient = &http.Client{Timeout: 30 * time.Minute}

// Apply runs the full pipeline for target (empty target = latest known).
func (u *Updater) Apply(ctx context.Context, target string) (err error) {
	// One updater at a time: the control panel and the poll loop can both fire.
	unlock, err := u.acquireLock()
	if err != nil {
		return err
	}
	defer unlock()

	pg, err := NewPostgres(u.Layout)
	if err != nil {
		return err
	}
	svc, closeDB, err := u.updateService(ctx, pg)
	if err != nil {
		return err
	}
	defer closeDB()

	status, err := svc.Status(ctx)
	if err != nil {
		return fmt.Errorf("read update status: %w", err)
	}
	if status.Latest == nil {
		return fmt.Errorf("no release metadata available")
	}
	if target == "" {
		target = status.Latest.Version
	}
	from := status.CurrentVersion
	art := status.Latest

	report := func(phase, msg string) { _ = svc.RecordProgress(ctx, phase, msg) }
	fail := func(phase string, cause error, rolledBack bool) error {
		_ = svc.RecordResult(ctx, update.ResultInput{
			OK: false, RolledBack: rolledBack, FromVersion: from, ToVersion: target,
			Error: fmt.Sprintf("%s: %v", phase, cause),
		})
		return fmt.Errorf("%s: %w", phase, cause)
	}

	// 1. preflight
	report("preflight", "sürüm ve artefakt kontrol ediliyor")
	if art.WindowsArtifactURL == "" || art.WindowsSHA256 == "" {
		return fail("preflight", fmt.Errorf("release %s has no Windows artifact", target), false)
	}
	// The pipeline needs room for the downloaded zip, its extraction, and a full
	// pre-update .varya dump alongside the existing cluster. Bail early rather
	// than fail mid-swap.
	const minFreeBytes = 3 << 30
	for _, dir := range []string{u.Layout.Home, u.Layout.InstallDir} {
		free, ferr := freeDiskBytes(dir)
		if ferr != nil {
			u.Logger.Warn("could not check free disk space", "dir", dir, "error", ferr)
			continue
		}
		if free < minFreeBytes {
			return fail("preflight", fmt.Errorf("insufficient disk space at %s: %.1f GiB free, need %d GiB",
				dir, float64(free)/(1<<30), minFreeBytes>>30), false)
		}
	}

	// 2. backup
	report("backup", "güncelleme öncesi yedek alınıyor")
	backupPath := filepath.Join(u.Layout.Backups(),
		fmt.Sprintf("pre-update-%s.varya", time.Now().UTC().Format("20060102T150405Z")))
	if err := u.run(ctx, pg, u.selfExe(), "backup", "create", backupPath); err != nil {
		return fail("backup", err, false)
	}

	// 3. download + verify
	report("download", "yeni sürüm indiriliyor")
	zipPath := filepath.Join(u.Layout.Downloads(), "varyaone-"+sanitize(target)+".zip")
	if err := download(ctx, art.WindowsArtifactURL, zipPath); err != nil {
		return fail("download", err, false)
	}
	if err := verifySHA256(zipPath, art.WindowsSHA256); err != nil {
		return fail("download", err, false)
	}

	// 4. stop the running service (also stops the bundled cluster). It must be
	// fully down before we swap binaries and touch the data dir — a live server
	// holds file locks and would race the migration. Nothing is changed yet, so
	// bailing here is safe.
	report("stop", "servis durduruluyor")
	closeDB()
	if err := Control("stop"); err != nil {
		u.Logger.Warn("service stop returned error (continuing)", "error", err)
	}
	if err := waitServiceStopped(30 * time.Second); err != nil {
		return fail("stop", err, false)
	}

	// 5. swap: move current install aside, extract the new bundle
	report("swap", "dosyalar değiştiriliyor")
	if err := os.RemoveAll(u.Layout.Rollback()); err != nil {
		return u.rollback(pg, "swap", err, false, from, target)
	}
	if err := stageRollback(u.Layout); err != nil {
		return u.rollback(pg, "swap", err, false, from, target)
	}
	if err := extractZip(zipPath, u.Layout.InstallDir); err != nil {
		return u.rollback(pg, "swap", err, true, from, target)
	}

	// 5b. PostgreSQL major upgrade. The binaries just swapped in may be a newer
	// major than the on-disk data directory, which then refuses to start. Move
	// the old cluster aside, initdb on the new major, and load the pre-update
	// .varya dump into it. The archived cluster is the exact pre-update state, so
	// any later failure restores it verbatim.
	var pgArchive string
	oldPGMajor, _ := pg.DataDirMajor()
	if newPGMajor, verErr := pg.BinariesMajor(); verErr == nil && oldPGMajor > 0 && newPGMajor > oldPGMajor {
		report("pg-upgrade", fmt.Sprintf("PostgreSQL %d → %d: veritabanı taşınıyor", oldPGMajor, newPGMajor))
		_ = pg.Stop(ctx)
		archived, aerr := pg.ArchiveDataDir(oldPGMajor)
		if aerr != nil {
			return u.rollback(pg, "pg-upgrade", aerr, true, from, target)
		}
		pgArchive = archived
		if e := pg.EnsureInitialized(ctx); e != nil {
			return u.rollbackPGUpgrade(pg, "pg-upgrade", e, pgArchive, from, target)
		}
		if e := pg.Start(ctx); e != nil {
			return u.rollbackPGUpgrade(pg, "pg-upgrade", e, pgArchive, from, target)
		}
		if e := u.run(ctx, pg, u.selfExe(), "backup", "restore", backupPath, "--force"); e != nil {
			return u.rollbackPGUpgrade(pg, "pg-upgrade", e, pgArchive, from, target)
		}
	}

	// 6. migrate against the bundled cluster
	report("migrate", "veritabanı şeması güncelleniyor")
	if err := pg.Start(ctx); err != nil {
		return u.rollbackAfter(ctx, pg, "migrate", err, false, backupPath, pgArchive, from, target)
	}
	if err := u.run(ctx, pg, u.selfExe(), "migrate", "up"); err != nil {
		return u.rollbackAfter(ctx, pg, "migrate", err, true, backupPath, pgArchive, from, target)
	}

	// 7. restart service
	report("restart", "servis yeniden başlatılıyor")
	_ = pg.Stop(ctx)
	if err := Control("start"); err != nil {
		return u.rollbackAfter(ctx, pg, "restart", err, true, backupPath, pgArchive, from, target)
	}

	// 8. health check
	report("healthcheck", "sağlık kontrolü")
	if err := u.waitHealthy(ctx, 3*time.Minute); err != nil {
		return u.rollbackAfter(ctx, pg, "healthcheck", err, true, backupPath, pgArchive, from, target)
	}

	// 9. success
	svc2, close2, dbErr := u.updateService(ctx, pg)
	if dbErr == nil {
		defer close2()
		_ = svc2.RecordResult(ctx, update.ResultInput{OK: true, FromVersion: from, ToVersion: target})
	}
	_ = os.RemoveAll(u.Layout.Rollback())
	cleanStaleReplacements(u.Layout.InstallDir)
	_ = os.Remove(zipPath)
	if pgArchive != "" {
		_ = os.RemoveAll(pgArchive)
		u.Logger.Info("postgres major upgraded", "from_major", oldPGMajor)
	}
	u.Logger.Info("update applied", "from", from, "to", target)
	return nil
}

// rollbackAfter reverts a failure that happened after the file swap. When the
// PostgreSQL major was upgraded (pgArchive set) the archived cluster is the exact
// pre-update state, so it is put back verbatim and the .varya restore is skipped.
func (u *Updater) rollbackAfter(ctx context.Context, pg *Postgres, phase string, cause error, schemaChanged bool, backupPath, pgArchive, from, target string) error {
	if pgArchive != "" {
		return u.rollbackPGUpgrade(pg, phase, cause, pgArchive, from, target)
	}
	return u.rollbackWithDB(ctx, pg, phase, cause, schemaChanged, backupPath, from, target)
}

// rollbackPGUpgrade reverts a failed major upgrade: stop the new cluster, discard
// its data directory, move the archived pre-upgrade cluster back, then restore
// the previous binaries and restart.
func (u *Updater) rollbackPGUpgrade(pg *Postgres, phase string, cause error, pgArchive, from, target string) error {
	u.Logger.Error("postgres major upgrade failed — restoring pre-upgrade cluster", "phase", phase, "error", cause)
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_ = pg.Stop(stopCtx)
	if err := pg.RestoreArchivedDataDir(pgArchive); err != nil {
		u.Logger.Error("CRITICAL: could not restore pre-upgrade data directory", "archive", pgArchive, "error", err)
	}
	return u.rollback(pg, phase, cause, true, from, target)
}

// rollback restores files only (schema untouched).
func (u *Updater) rollback(pg *Postgres, phase string, cause error, filesExtracted bool, from, target string) error {
	u.Logger.Error("update failed — rolling back files", "phase", phase, "error", cause)
	if filesExtracted {
		_ = restoreRollback(u.Layout)
	}
	_ = Control("start")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	_ = u.waitHealthy(ctx, 3*time.Minute)
	if svc, closeDB, err := u.updateService(ctx, pg); err == nil {
		defer closeDB()
		_ = svc.RecordResult(ctx, update.ResultInput{
			OK: false, RolledBack: true, FromVersion: from, ToVersion: target,
			Error: fmt.Sprintf("%s: %v", phase, cause),
		})
	}
	return fmt.Errorf("%s failed, rolled back: %w", phase, cause)
}

// rollbackWithDB also restores the pre-update backup when the schema changed.
func (u *Updater) rollbackWithDB(ctx context.Context, pg *Postgres, phase string, cause error, schemaChanged bool, backupPath, from, target string) error {
	u.Logger.Error("update failed — rolling back", "phase", phase, "error", cause)
	_ = restoreRollback(u.Layout)
	if schemaChanged {
		startCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := pg.Start(startCtx); err == nil {
			if err := u.run(ctx, pg, u.selfExe(), "backup", "restore", backupPath, "--force"); err != nil {
				u.Logger.Error("CRITICAL: database restore failed", "error", err)
			}
			_ = pg.Stop(startCtx)
		}
	}
	return u.rollback(pg, phase, cause, true, from, target)
}

func (u *Updater) updateService(ctx context.Context, pg *Postgres) (*update.Service, func(), error) {
	if err := pg.Start(ctx); err != nil {
		return nil, func() {}, err
	}
	dbURL, err := pg.DatabaseURL()
	if err != nil {
		return nil, func() {}, err
	}
	pool, err := database.Open(ctx, dbURL)
	if err != nil {
		return nil, func() {}, err
	}
	getenv := func(key string) string {
		switch key {
		case "VARYAONE_DATABASE_URL":
			return dbURL
		case "VARYAONE_MASTER_KEY":
			if v := os.Getenv(key); v != "" {
				return v
			}
			b, _ := os.ReadFile(filepath.Join(u.Layout.Home, "master.key"))
			return strings.TrimSpace(string(b))
		}
		return os.Getenv(key)
	}
	cfg, err := config.Load(getenv)
	if err != nil {
		pool.Close()
		return nil, func() {}, err
	}
	return update.NewService(pool, cfg), pool.Close, nil
}

func (u *Updater) waitHealthy(ctx context.Context, timeout time.Duration) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/health/ready", u.HTTPPort)
	deadline := time.Now().Add(timeout)
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("health check did not pass within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func (u *Updater) run(ctx context.Context, pg *Postgres, name string, args ...string) error {
	env := []string{}
	if dbURL, err := pg.DatabaseURL(); err == nil {
		env = append(env, "VARYAONE_DATABASE_URL="+dbURL)
	}
	if b, err := os.ReadFile(filepath.Join(u.Layout.Home, "master.key")); err == nil {
		env = append(env, "VARYAONE_MASTER_KEY="+strings.TrimSpace(string(b)))
	}
	env = append(env, "VARYAONE_STORAGE_ROOT="+u.Layout.Storage())
	return runCommand(ctx, u.Layout.InstallDir, env, name, args...)
}

func (u *Updater) selfExe() string {
	return filepath.Join(u.Layout.InstallDir, exe("varyaone"))
}

// acquireLock takes an exclusive updater lock so a control-panel trigger and the
// background poll loop cannot run Apply concurrently. A lock older than 2h is
// treated as stale (a crashed updater) and stolen.
func (u *Updater) acquireLock() (func(), error) {
	path := filepath.Join(u.Layout.Home, "update.lock")
	if fi, err := os.Stat(path); err == nil {
		if time.Since(fi.ModTime()) < 2*time.Hour {
			return nil, fmt.Errorf("another update is already running (lock: %s)", path)
		}
		u.Logger.Warn("stealing stale update lock", "path", path, "age", time.Since(fi.ModTime()).Round(time.Second))
		_ = os.Remove(path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("acquire update lock: %w", err)
	}
	fmt.Fprintf(f, "%d\n", os.Getpid())
	_ = f.Close()
	return func() { _ = os.Remove(path) }, nil
}

// waitServiceStopped blocks until the OS service reports not-running (or is not
// installed at all, e.g. a portable run), or the timeout elapses.
func waitServiceStopped(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		installed, running := ServiceState()
		if !installed || !running {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("service still running after %s", timeout)
		}
		time.Sleep(time.Second)
	}
}

/* ------------------------------------------------------------ file helpers -- */

// stageRollback copies the swappable parts of the install (binary + web bundle)
// into Home/rollback so a failed update can be reverted.
func stageRollback(l Layout) error {
	if err := os.MkdirAll(l.Rollback(), 0o755); err != nil {
		return err
	}
	for _, rel := range swappable {
		src := filepath.Join(l.InstallDir, rel)
		if !fileExists(src) {
			continue
		}
		if err := copyTree(src, filepath.Join(l.Rollback(), rel)); err != nil {
			return err
		}
	}
	return nil
}

func restoreRollback(l Layout) error {
	for _, rel := range swappable {
		src := filepath.Join(l.Rollback(), rel)
		if !fileExists(src) {
			continue
		}
		dst := filepath.Join(l.InstallDir, rel)
		_ = os.RemoveAll(dst)
		if err := copyTree(src, dst); err != nil {
			return err
		}
	}
	return nil
}

// swappable lists install-relative paths a release artifact replaces. pgsql/ and
// the data Home are never touched by an update.
var swappable = []string{exe("varyaone"), exe("varyaone-panel"), exe("varyaone-client"), "web", "RELEASE"}

func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(src, dst, info.Mode())
	}
	if err := os.MkdirAll(dst, info.Mode()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := createOrReplace(dst, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// createOrReplace opens path for writing, truncating an existing file. On Windows
// a currently-running or otherwise locked executable (notably varyaone.exe, which
// is the process applying the update) cannot be opened with O_TRUNC; rename it out
// of the way first so the fresh copy lands at the canonical path. The renamed
// ".old-<ts>" file is swept up by cleanStaleReplacements once the update settles.
func createOrReplace(path string, mode os.FileMode) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err == nil {
		return f, nil
	}
	if runtime.GOOS != "windows" {
		return nil, err
	}
	aside := path + ".old-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if renErr := os.Rename(path, aside); renErr != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
}

// cleanStaleReplacements removes ".old-<ts>" files left by createOrReplace after a
// Windows in-place binary swap. Best-effort: a file still locked by a lingering
// handle is skipped and retried on the next call.
func cleanStaleReplacements(root string) {
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if i := strings.LastIndex(d.Name(), ".old-"); i >= 0 {
			if _, convErr := strconv.ParseInt(d.Name()[i+len(".old-"):], 10, 64); convErr == nil {
				_ = os.Remove(p)
			}
		}
		return nil
	})
}

func download(ctx context.Context, url, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := updateHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

func verifySHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, strings.TrimSpace(want)) {
		return fmt.Errorf("checksum mismatch: got %s want %s", got, want)
	}
	return nil
}

func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		target := filepath.Join(destDir, filepath.Clean(f.Name))
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("zip entry escapes destination: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, f.Mode()); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := createOrReplace(target, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, s)
}
