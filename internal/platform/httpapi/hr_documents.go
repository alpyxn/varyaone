package httpapi

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/alpyxn/varyaone/internal/hr/document"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/go-chi/chi/v5"
)

type hrDocumentHandler struct{ service *document.Service }

func mountHRDocumentRoutes(router chi.Router, identityService *identity.Service, service *document.Service) {
	auth := identityHandler{service: identityService}
	h := hrDocumentHandler{service: service}
	read := router.With(auth.requireSession)
	write := router.With(auth.requireSession, auth.requireCSRF)
	read.Get("/api/v1/hr/employees/{employeeID}/documents", h.list)
	read.Get("/api/v1/hr/employees/{employeeID}/documents/{documentID}/download", h.download)
	write.Post("/api/v1/hr/employees/{employeeID}/documents", h.upload)
	write.Delete("/api/v1/hr/employees/{employeeID}/documents/{documentID}", h.archive)
}

func (h hrDocumentHandler) list(w http.ResponseWriter, r *http.Request) {
	archivedOnly := r.URL.Query().Get("archived") == "true" || r.URL.Query().Get("include_archived") == "true"
	items, err := h.service.List(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"), archivedOnly)
	if err != nil {
		writeHRDocumentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h hrDocumentHandler) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 25<<20+1024)
	if err := r.ParseMultipartForm(25 << 20); err != nil {
		writeError(w, r, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "Belge boyutu sınırı aşıyor.")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Belge dosyası gereklidir.")
		return
	}
	defer file.Close()
	item, err := h.service.Upload(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"),
		r.FormValue("document_type"), r.FormValue("sensitivity"), header.Filename, file, requestMeta(r))
	if err != nil {
		writeHRDocumentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h hrDocumentHandler) download(w http.ResponseWriter, r *http.Request) {
	reader, info, filename, err := h.service.Open(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"), chi.URLParam(r, "documentID"))
	if err != nil {
		writeHRDocumentError(w, r, err)
		return
	}
	defer reader.Close()
	if info.ContentType != "" {
		w.Header().Set("Content-Type", info.ContentType)
	}
	if info.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	}
	// RFC 5987: ASCII fallback + UTF-8 encoded name so Turkish characters survive.
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' {
			return '_'
		}
		return r
	}, filename)
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+ascii+`"; filename*=UTF-8''`+url.PathEscape(filename))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

func (h hrDocumentHandler) archive(w http.ResponseWriter, r *http.Request) {
	err := h.service.Archive(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"), chi.URLParam(r, "documentID"), requestMeta(r))
	if err != nil {
		writeHRDocumentError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeHRDocumentError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu belge için yetkiniz yok.")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, document.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "EMPLOYEE_DOCUMENT_NOT_FOUND", "Belge bulunamadı.")
	case errors.Is(err, document.ErrArchived):
		writeError(w, r, http.StatusConflict, "EMPLOYEE_DOCUMENT_ARCHIVED", "Belge zaten arşivlenmiş.")
	case errors.Is(err, document.ErrEmployeeGone):
		writeError(w, r, http.StatusUnprocessableEntity, "EMPLOYEE_DOCUMENT_EMPLOYEE_NOT_FOUND", "Çalışan bulunamadı.")
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Belge işlemi tamamlanamadı.")
	}
}
