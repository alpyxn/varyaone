package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/alpyxn/varyaone/internal/backup"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/go-chi/chi/v5"
)

const backupPermission = "system.backup.manage"

// maxRestoreUpload caps the uploaded `.varya` archive. A full-system archive is
// dominated by the compressed pg_dump plus stored objects; 8 GiB is generous
// for the single-tenant deployments this engine targets.
const maxRestoreUpload = 8 << 30

type systemHandler struct{ engine *backup.Engine }

// restoreMu serializes restores across the process. A restore rebuilds the whole
// database with pg_restore --clean; running two at once, or letting a second one
// start while the first is mid-flight, corrupts the target. A restore also
// invalidates every pooled connection this server holds, so callers are told to
// restart the API afterwards.
var restoreMu sync.Mutex

func mountSystemRoutes(router chi.Router, identityService *identity.Service, engine *backup.Engine) {
	auth := identityHandler{service: identityService}
	handler := systemHandler{engine: engine}
	router.Route("/api/v1/system/backup", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Use(handler.requireBackupPermission)
		r.Get("/", handler.download)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Post("/restore", handler.restore)
		})
	})
}

func (h systemHandler) requireBackupPermission(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sessionFromRequest(r).HasPermission(backupPermission) {
			writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// detachedContext frees a long-running backup/restore from the router's 30s
// request timeout while still stopping if the client disconnects.
func detachedContext(r *http.Request, limit time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), limit)
}

func (h systemHandler) download(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := detachedContext(r, time.Hour)
	defer cancel()

	filename := backup.SuggestedFilename(time.Now())
	w.Header().Set("Content-Type", "application/x-varya-backup")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if _, err := h.engine.Create(ctx, w); err != nil {
		// Headers are likely already flushed; log-and-abort is the best we can
		// do. A truncated download fails the client-side checksum on restore.
		panic(fmt.Sprintf("backup stream failed: %v", err))
	}
}

func (h systemHandler) restore(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := detachedContext(r, 2*time.Hour)
	defer cancel()

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Yedek dosyası okunamadı.")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "'file' alanında bir .varya dosyası gerekli.")
		return
	}
	defer file.Close()

	if !restoreMu.TryLock() {
		writeError(w, r, http.StatusConflict, "RESTORE_BUSY", "Zaten süren bir geri yükleme var.")
		return
	}
	defer restoreMu.Unlock()

	force := r.FormValue("force") == "true"
	manifest, err := h.engine.Restore(ctx, http.MaxBytesReader(w, file, maxRestoreUpload), backup.RestoreOptions{Force: force})
	switch {
	case errors.Is(err, backup.ErrArchiveNewer):
		writeError(w, r, http.StatusConflict, "ARCHIVE_NEWER", err.Error())
	case errors.Is(err, backup.ErrKeyMismatch):
		writeError(w, r, http.StatusConflict, "KEY_MISMATCH", err.Error())
	case errors.Is(err, backup.ErrToolMissing):
		writeError(w, r, http.StatusInternalServerError, "TOOL_MISSING", err.Error())
	case errors.Is(err, backup.ErrStoragePartial):
		slog.Default().Error("backup restore left storage inconsistent", "error", err)
		writeError(w, r, http.StatusInternalServerError, "RESTORE_STORAGE_PARTIAL",
			fmt.Sprintf("Veritabanı geri yüklendi ancak dosya deposu yerine konulamadı — elle müdahale gerekli: %v", err))
	case err != nil:
		writeError(w, r, http.StatusUnprocessableEntity, "RESTORE_FAILED", fmt.Sprintf("Geri yükleme başarısız: %v", err))
	default:
		slog.Default().Warn("backup restored via API; restart the API to drop stale pooled connections",
			"restored_from", manifest.CreatedAt, "migration_version", manifest.MigrationVersion)
		writeJSON(w, http.StatusOK, map[string]any{
			"restored_from":     manifest.CreatedAt,
			"migration_version": manifest.MigrationVersion,
			"objects":           len(manifest.Objects),
			"restart_required":  true,
		})
	}
}
