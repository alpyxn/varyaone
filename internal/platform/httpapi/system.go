package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/alpyxn/varyaone/internal/backup"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/opctl"
	"github.com/go-chi/chi/v5"
)

const backupPermission = "system.backup.manage"

// maxRestoreUpload caps the uploaded `.varya` archive itself. It is separate
// from the HTTP body limit below on purpose: a multipart body is the file plus
// its headers and boundaries, so a request carrying a file of exactly this size
// is legitimately larger than this size. Conflating the two rejected files at
// the documented limit.
const maxRestoreUpload = 8 << 30

// maxRestoreBody is the transport limit: the archive plus multipart overhead.
const maxRestoreBody = maxRestoreUpload + (1 << 20)

// restoreWaitWindow is how long the compatibility endpoint keeps the connection
// open waiting for a restore to finish before answering "still running, here is
// the operation id". It is not a timeout on the restore: the operation keeps
// going, and the client learns the outcome from the status endpoint. Answering
// rather than holding the socket open indefinitely is what makes the endpoint
// survive proxies with their own idle limits.
const restoreWaitWindow = 10 * time.Minute

type backupEngine interface {
	Create(context.Context, io.Writer) (backup.Manifest, error)
	Restore(context.Context, io.Reader, backup.RestoreOptions) (backup.Manifest, error)
	Verify(context.Context, io.Reader) (backup.Manifest, error)
}

type systemHandler struct {
	engine     backupEngine
	controller *opctl.Controller
	spoolDir   string
}

func mountSystemRoutes(router chi.Router, identityService *identity.Service, engine backupEngine, controller *opctl.Controller, spoolDir string) {
	auth := identityHandler{service: identityService}
	handler := systemHandler{engine: engine, controller: controller, spoolDir: spoolDir}
	router.Route("/api/v1/system", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Use(handler.requireBackupPermission)
		r.Get("/backup", handler.download)
		r.Get("/backup/", handler.download)
		r.Get("/operations", handler.listOperations)
		r.Get("/operations/current", handler.currentOperation)
		r.Get("/operations/{id}", handler.getOperation)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Post("/backup/restore", handler.restore)
			r.Post("/operations/{id}/resolve", handler.resolveOperation)
		})
	})
}

// requireBackupPermission gates every system route on the backup permission.
//
// Note what the permission covers, because it is wider than its name suggests:
// these routes act on the whole installation. One archive contains every
// company's data and one restore replaces all of them, so on an installation
// hosting more than one company this permission lets its holder read and
// overwrite data belonging to companies they are not a member of.
//
// That is the intended policy here — an installation is treated as belonging to
// one operator, with companies as that operator's legal entities rather than as
// separate tenants. Anyone deploying several unrelated customers onto a single
// installation should not rely on company membership for isolation.
func (h systemHandler) requireBackupPermission(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sessionFromRequest(r).HasPermission(backupPermission) {
			writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// unboundNetworkDeadlines removes this connection's read and write deadlines.
//
// The server sets ReadTimeout and WriteTimeout to 30 seconds for the ordinary
// API. Those are socket deadlines, and detaching the request *context* does not
// touch them: a backup download that takes two minutes to produce its first
// byte, or an 8 GiB upload on a slow link, dies on the socket regardless of how
// patient the context is. Clearing them here, on these routes only, is what
// makes a long operation actually possible — the general API keeps its limits.
func unboundNetworkDeadlines(w http.ResponseWriter, r *http.Request) {
	controller := http.NewResponseController(w)
	// Errors mean the ResponseWriter does not support deadlines (a test
	// recorder, a wrapped writer). The operation is still correct then; it is
	// only the socket limit that stays in force.
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	_ = r
}

// detachedContext frees a long-running operation from the router's request
// timeout and from client disconnection.
//
// Being explicit about the second part: context.WithoutCancel also detaches the
// cancellation that fires when the client goes away. That is deliberate for a
// restore — abandoning a half-applied restore because a browser tab closed
// would be far worse than finishing it — and it is why the status endpoint
// exists, so a disconnected client can still find out what happened.
func detachedContext(r *http.Request, limit time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), limit)
}

func (h systemHandler) download(w http.ResponseWriter, r *http.Request) {
	unboundNetworkDeadlines(w, r)
	lease, err := h.controller.Acquire(r.Context(), opctl.KindBackup, opctl.AcquireOptions{Actor: "api"})
	if err != nil {
		h.writeLeaseError(w, r, err, "BACKUP_BUSY")
		return
	}
	defer func() { _ = lease.Release() }()

	ctx, cancel := detachedContext(r, time.Hour)
	defer cancel()

	filename := backup.SuggestedFilename(time.Now())
	w.Header().Set("Content-Type", "application/x-varya-backup")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// A backup is the whole installation. No intermediary may keep a copy.
	w.Header().Set("Cache-Control", "private, no-store, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Operation-Id", lease.ID())

	if _, err := h.engine.Create(ctx, w); err != nil {
		_ = lease.Fail(opctl.PhaseFailedUnchanged, err)
		// Headers are already flushed, so the status code is spent. Aborting the
		// connection is what tells the client the body is incomplete; a clean
		// end would present a truncated file as a whole one.
		panic(fmt.Sprintf("backup stream failed: %v", err))
	}
	_ = lease.Result(opctl.PhaseCommitted, map[string]any{"kind": "download"})
}

// restore uploads, validates and applies an archive.
//
// The shape is upload → validate → execute → report, and each arrow is a place
// the operation can stop with the installation untouched. Nothing destructive
// begins until the upload has completed and verified, which means a connection
// that drops mid-upload cannot leave anything half-applied.
func (h systemHandler) restore(w http.ResponseWriter, r *http.Request) {
	unboundNetworkDeadlines(w, r)

	// An idempotency key lets a client that lost the response ask about the
	// operation instead of starting a second one.
	key := r.Header.Get("Idempotency-Key")

	lease, err := h.controller.Acquire(r.Context(), opctl.KindRestore, opctl.AcquireOptions{Actor: "api"})
	if err != nil {
		if key != "" {
			if id, found, lookupErr := h.controller.LookupKey(key); lookupErr == nil && found {
				// This exact request already has an operation. Report it rather
				// than reporting a conflict the caller cannot act on.
				h.writeOperation(w, r, id, http.StatusAccepted)
				return
			}
		}
		h.writeLeaseError(w, r, err, "RESTORE_BUSY")
		return
	}
	released := false
	defer func() {
		if !released {
			_ = lease.Release()
		}
	}()
	w.Header().Set("X-Operation-Id", lease.ID())

	if key != "" {
		if id, found, lookupErr := h.controller.LookupKey(key); lookupErr == nil && found {
			h.writeOperation(w, r, id, http.StatusAccepted)
			return
		}
		if err := h.controller.RememberKey(key, lease.ID()); err != nil {
			_ = lease.Fail(opctl.PhaseFailedUnchanged, err)
			writeError(w, r, http.StatusInternalServerError, "CONTROL_WRITE_FAILED",
				"İşlem kaydı yazılamadı; geri yükleme başlatılmadı.")
			return
		}
	}

	uploaded, err := h.spoolUpload(w, r)
	if err != nil {
		_ = lease.Fail(opctl.PhaseFailedUnchanged, err)
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			writeError(w, r, http.StatusRequestEntityTooLarge, "UPLOAD_TOO_LARGE", "Yedek dosyası boyut sınırını aşıyor.")
		case errors.Is(err, errNoFileField):
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "'file' alanında bir .varya dosyası gerekli.")
		default:
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Yedek dosyası okunamadı.")
		}
		return
	}
	spoolPath := uploaded.path
	defer func() { _ = os.Remove(spoolPath) }()

	force := uploaded.fields["force"] == "true"

	// Validate the spooled file, not the stream. The archive on disk is what
	// the restore will read, and it cannot change underneath us between the
	// check and the use.
	ctx, cancel := detachedContext(r, restoreWaitWindow+time.Hour)
	defer cancel()

	spool, err := os.Open(spoolPath)
	if err != nil {
		_ = lease.Fail(opctl.PhaseFailedUnchanged, err)
		writeError(w, r, http.StatusInternalServerError, "SPOOL_FAILED", "Yüklenen dosya okunamadı.")
		return
	}
	_, verifyErr := h.engine.Verify(ctx, spool)
	_ = spool.Close()
	if verifyErr != nil {
		_ = lease.Fail(opctl.PhaseFailedUnchanged, verifyErr)
		writeError(w, r, http.StatusUnprocessableEntity, "ARCHIVE_INVALID",
			fmt.Sprintf("Yedek dosyası geçersiz; sistem değişmedi: %v", verifyErr))
		return
	}
	if err := lease.Result(opctl.PhaseSafetyVerified, map[string]any{"source": "upload"}); err != nil {
		_ = lease.Fail(opctl.PhaseFailedUnchanged, err)
		writeError(w, r, http.StatusInternalServerError, "CONTROL_WRITE_FAILED", "İşlem kaydı yazılamadı.")
		return
	}

	archive, err := os.Open(spoolPath)
	if err != nil {
		_ = lease.Fail(opctl.PhaseFailedUnchanged, err)
		writeError(w, r, http.StatusInternalServerError, "SPOOL_FAILED", "Yüklenen dosya okunamadı.")
		return
	}
	manifest, restoreErr := h.engine.Restore(ctx, archive, backup.RestoreOptions{Force: force, Journal: lease})
	_ = archive.Close()

	switch {
	case restoreErr == nil:
		bumpDataGeneration()
		released = true
		_ = lease.Release()
		// Not "restart required": the swap terminated the pool's connections, so
		// it has already reconnected to the restored database. A restart only
		// gives in-flight requests and background work a clean start.
		slog.Default().Warn("backup restored via API; a service restart is recommended (./deploy.sh restart), not required",
			"operation", lease.ID(), "restored_from", manifest.CreatedAt, "migration_version", manifest.MigrationVersion)
		writeJSON(w, http.StatusOK, map[string]any{
			"operation_id":      lease.ID(),
			"restored_from":     manifest.CreatedAt,
			"migration_version": manifest.MigrationVersion,
			"objects":           len(manifest.Objects),
			"restart_required":  true,
		})
		return

	case errors.Is(restoreErr, backup.ErrSystemInconsistent):
		// The database did change, so open pages are stale either way.
		bumpDataGeneration()
		// Recorded as RECOVERY_REQUIRED, which keeps this installation out of
		// service on its next start until an operator resolves it.
		_ = lease.Fail(opctl.PhaseRecoveryRequired, restoreErr)
		slog.Default().Error("backup restore left the system inconsistent",
			"operation", lease.ID(), "error", restoreErr)
		code := "SYSTEM_INCONSISTENT"
		if errors.Is(restoreErr, backup.ErrStoragePartial) {
			code = "RESTORE_STORAGE_PARTIAL"
		}
		writeError(w, r, http.StatusInternalServerError, code,
			fmt.Sprintf("Veritabanı değişti ancak geri yükleme tamamlanamadı — sistemi kullanmayın, elle müdahale gerekli (işlem %s): %v", lease.ID(), restoreErr))
		return

	default:
		// Everything else failed before the database transaction committed.
		_ = lease.Fail(opctl.PhaseFailedUnchanged, restoreErr)
		switch {
		case errors.Is(restoreErr, backup.ErrArchiveNewer):
			writeError(w, r, http.StatusConflict, "ARCHIVE_NEWER", restoreErr.Error())
		case errors.Is(restoreErr, backup.ErrKeyMismatch):
			writeError(w, r, http.StatusConflict, "KEY_MISMATCH", restoreErr.Error())
		case errors.Is(restoreErr, backup.ErrToolMissing):
			writeError(w, r, http.StatusInternalServerError, "TOOL_MISSING", restoreErr.Error())
		case errors.Is(restoreErr, backup.ErrArchiveInvalid):
			writeError(w, r, http.StatusUnprocessableEntity, "ARCHIVE_INVALID",
				fmt.Sprintf("Yedek dosyası geçersiz; sistem değişmedi: %v", restoreErr))
		default:
			writeError(w, r, http.StatusUnprocessableEntity, "RESTORE_FAILED",
				fmt.Sprintf("Geri yükleme başarısız (sistem değişmedi): %v", restoreErr))
		}
	}
}

var errNoFileField = errors.New("multipart form has no file field")

// upload is a spooled archive plus the small scalar fields that came with it.
type upload struct {
	path   string
	fields map[string]string
}

// spoolUpload streams the uploaded archive to a private file and returns its
// path together with the form's scalar fields.
//
// It reads the multipart stream directly rather than calling ParseMultipartForm
// for two reasons. The archive is measured in gigabytes, and ParseMultipartForm
// decides for itself how much of that to hold in memory. And taking the reader
// permanently disables FormValue on this request — so the scalar fields have to
// be collected here, in the same pass, or they are silently lost.
func (h systemHandler) spoolUpload(w http.ResponseWriter, r *http.Request) (upload, error) {
	result := upload{fields: map[string]string{}}
	if r.ContentLength > maxRestoreBody {
		return result, &http.MaxBytesError{Limit: maxRestoreBody}
	}
	// Bound the stream before anything is written to disk. This also covers a
	// chunked request that advertises no Content-Length at all.
	r.Body = http.MaxBytesReader(w, r.Body, maxRestoreBody)
	reader, err := r.MultipartReader()
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(h.spoolDir, 0o700); err != nil {
		return result, err
	}
	cleanup := func() {
		if result.path != "" {
			_ = os.Remove(result.path)
			result.path = ""
		}
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			cleanup()
			return result, err
		}
		if part.FormName() != "file" {
			// Bound every scalar field so a "field" cannot smuggle gigabytes.
			value, readErr := io.ReadAll(io.LimitReader(part, 1<<20))
			_ = part.Close()
			if readErr != nil {
				cleanup()
				return result, readErr
			}
			result.fields[part.FormName()] = string(value)
			continue
		}
		if result.path != "" {
			_ = part.Close()
			cleanup()
			return result, errors.New("multipart form carries more than one file")
		}
		spool, createErr := os.CreateTemp(h.spoolDir, "upload-*.varya")
		if createErr != nil {
			_ = part.Close()
			cleanup()
			return result, createErr
		}
		path := spool.Name()
		written, copyErr := io.Copy(spool, io.LimitReader(part, maxRestoreUpload+1))
		_ = part.Close()
		syncErr := spool.Sync()
		closeErr := spool.Close()
		switch {
		case copyErr != nil:
			_ = os.Remove(path)
			return result, copyErr
		case written > maxRestoreUpload:
			_ = os.Remove(path)
			return result, &http.MaxBytesError{Limit: maxRestoreUpload}
		case syncErr != nil:
			_ = os.Remove(path)
			return result, syncErr
		case closeErr != nil:
			_ = os.Remove(path)
			return result, closeErr
		}
		result.path = path
	}
	if result.path == "" {
		return result, errNoFileField
	}
	return result, nil
}

func (h systemHandler) writeLeaseError(w http.ResponseWriter, r *http.Request, err error, busyCode string) {
	switch {
	case errors.Is(err, opctl.ErrBusy):
		writeError(w, r, http.StatusConflict, busyCode, "Zaten süren bir yedekleme veya geri yükleme var.")
	case errors.Is(err, opctl.ErrRecoveryRequired):
		writeError(w, r, http.StatusConflict, "RECOVERY_REQUIRED",
			fmt.Sprintf("Önceki sistem işlemi yarıda kaldı; yenisi başlatılamaz: %v", err))
	default:
		writeError(w, r, http.StatusInternalServerError, "CONTROL_UNAVAILABLE",
			fmt.Sprintf("İşlem koordinasyonu kullanılamıyor: %v", err))
	}
}

func (h systemHandler) currentOperation(w http.ResponseWriter, r *http.Request) {
	status, err := h.controller.Status()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "CONTROL_UNAVAILABLE", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"serviceable": status.Serviceable,
		"running":     status.Running,
		"reason":      status.Reason,
		"operation":   status.Operation,
	})
}

func (h systemHandler) getOperation(w http.ResponseWriter, r *http.Request) {
	h.writeOperation(w, r, chi.URLParam(r, "id"), http.StatusOK)
}

// writeOperation answers with one operation's recorded state and its journal.
// This is how a client that lost its connection mid-restore finds out what
// happened — including after signing in again, because the record outlives the
// session that started it.
func (h systemHandler) writeOperation(w http.ResponseWriter, r *http.Request, id string, status int) {
	record, err := h.controller.FindRecord(id)
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Böyle bir sistem işlemi yok.")
		return
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "CONTROL_UNAVAILABLE", err.Error())
		return
	}
	history, _ := h.controller.History(id)
	writeJSON(w, status, map[string]any{"operation": record, "history": history})
}

func (h systemHandler) listOperations(w http.ResponseWriter, r *http.Request) {
	ids, err := h.controller.Operations()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "CONTROL_UNAVAILABLE", err.Error())
		return
	}
	if len(ids) > 50 {
		ids = ids[:50]
	}
	records := make([]*opctl.Record, 0, len(ids))
	for _, id := range ids {
		if record, err := h.controller.FindRecord(id); err == nil {
			records = append(records, record)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"operations": records})
}

// resolveOperation is the operator's acknowledgement that a stuck operation has
// been dealt with. It changes no data — it only records that a person looked —
// and it is the only way an installation leaves RECOVERY_REQUIRED, because the
// question "is this database consistent now?" is not one software can answer
// about a restore it did not finish.
func (h systemHandler) resolveOperation(w http.ResponseWriter, r *http.Request) {
	note := r.FormValue("note")
	if note == "" {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR",
			"Çözüm notu gerekli: sistemi tekrar hizmete almadan önce ne yapıldığı kaydedilmelidir.")
		return
	}
	if err := h.controller.Resolve(note); err != nil {
		writeError(w, r, http.StatusConflict, "RESOLVE_FAILED", err.Error())
		return
	}
	h.writeOperation(w, r, chi.URLParam(r, "id"), http.StatusOK)
}

// defaultSpoolDir is where uploaded archives land before they are verified.
func defaultSpoolDir(controlDir string) string { return filepath.Join(controlDir, "uploads") }
