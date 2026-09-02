package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/media"
	"github.com/alpyxn/varyaone/internal/storage"
	"github.com/go-chi/chi/v5"
)

type mediaHandler struct{ service *media.Service }

func mountMediaRoutes(router chi.Router, identityService *identity.Service, service *media.Service) {
	auth := identityHandler{service: identityService}
	h := mediaHandler{service: service}
	router.Route("/api/v1/products/{productID}/images", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/", h.listImages)
		r.Get("/{imageID}/variants/{variantCode}", h.openImageVariant)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Post("/", h.uploadImage)
			r.Patch("/{imageID}", h.updateImage)
			r.Delete("/{imageID}", h.archiveImage)
		})
	})
	router.Route("/api/v1/products/{productID}/attachments", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/", h.listAttachments)
		r.Get("/{attachmentID}/download", h.openAttachment)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Post("/", h.uploadAttachment)
			r.Delete("/{attachmentID}", h.archiveAttachment)
		})
	})
	router.With(auth.requireSession).Get("/api/v1/products/{productID}/images", h.listImages)
	router.With(auth.requireSession).Get("/api/v1/products/{productID}/images/{imageID}/variants/{variantCode}", h.openImageVariant)
	router.With(auth.requireSession, auth.requireCSRF).Post("/api/v1/products/{productID}/images", h.uploadImage)
	router.With(auth.requireSession).Get("/api/v1/products/{productID}/attachments", h.listAttachments)
	router.With(auth.requireSession).Get("/api/v1/products/{productID}/attachments/{attachmentID}/download", h.openAttachment)
	router.With(auth.requireSession, auth.requireCSRF).Post("/api/v1/products/{productID}/attachments", h.uploadAttachment)
}

func (h mediaHandler) listImages(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListImages(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), r.URL.Query().Get("variant_id"), queryLimit(r, 100, 200))
	if err != nil {
		writeMediaError(w, r, err, "Ürün görselleri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h mediaHandler) uploadImage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20+1024)
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		writeMediaError(w, r, err, "Görsel yüklenemedi.")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Görsel dosyası gereklidir.")
		return
	}
	defer file.Close()
	position, _ := strconv.Atoi(r.FormValue("position"))
	primary, _ := strconv.ParseBool(r.FormValue("is_primary"))
	item, err := h.service.UploadImage(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), r.FormValue("variant_id"), header.Filename, file, position, primary, requestMeta(r))
	if err != nil {
		writeMediaError(w, r, err, "Görsel yüklenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusCreated, item)
}

func (h mediaHandler) updateImage(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Görsel güncellemesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	var input media.ImagePresentationInput
	if err = decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Görsel sunum bilgileri geçersiz.")
		return
	}
	item, err := h.service.UpdateImagePresentation(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), chi.URLParam(r, "imageID"), version, input, requestMeta(r))
	if err != nil {
		writeMediaError(w, r, err, "Görsel güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h mediaHandler) archiveImage(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Görsel arşivlemek için geçerli If-Match başlığı gereklidir.")
		return
	}
	err = h.service.ArchiveImage(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), chi.URLParam(r, "imageID"), version, requestMeta(r))
	if err != nil {
		writeMediaError(w, r, err, "Görsel arşivlenemedi.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h mediaHandler) openImageVariant(w http.ResponseWriter, r *http.Request) {
	file, info, err := h.service.OpenImageVariant(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), chi.URLParam(r, "imageID"), chi.URLParam(r, "variantCode"))
	if err != nil {
		writeMediaError(w, r, err, "Görsel açılamadı.")
		return
	}
	defer file.Close()
	writeObjectHeaders(w, info, "")
	_, _ = io.Copy(w, file)
}

func (h mediaHandler) listAttachments(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListAttachments(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), r.URL.Query().Get("variant_id"), queryLimit(r, 100, 200))
	if err != nil {
		writeMediaError(w, r, err, "Ürün ekleri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h mediaHandler) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20+1024)
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		writeMediaError(w, r, err, "Ek dosyası yüklenemedi.")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Ek dosyası gereklidir.")
		return
	}
	defer file.Close()
	item, err := h.service.UploadAttachment(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), r.FormValue("variant_id"), header.Filename, r.FormValue("attachment_kind"), r.FormValue("description"), file, requestMeta(r))
	if err != nil {
		writeMediaError(w, r, err, "Ek dosyası yüklenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusCreated, item)
}

func (h mediaHandler) archiveAttachment(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Ek arşivlemek için geçerli If-Match başlığı gereklidir.")
		return
	}
	err = h.service.ArchiveAttachment(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), chi.URLParam(r, "attachmentID"), version, requestMeta(r))
	if err != nil {
		writeMediaError(w, r, err, "Ek dosyası arşivlenemedi.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h mediaHandler) openAttachment(w http.ResponseWriter, r *http.Request) {
	file, info, filename, err := h.service.OpenAttachment(r.Context(), sessionFromRequest(r), chi.URLParam(r, "productID"), chi.URLParam(r, "attachmentID"))
	if err != nil {
		writeMediaError(w, r, err, "Ek dosyası açılamadı.")
		return
	}
	defer file.Close()
	writeObjectHeaders(w, info, filename)
	_, _ = io.Copy(w, file)
}

func writeObjectHeaders(w http.ResponseWriter, info storage.ObjectInfo, filename string) {
	contentType := strings.TrimSpace(info.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	if info.Size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	}
	if info.SHA256 != "" {
		w.Header().Set("ETag", `"`+info.SHA256+`"`)
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if filename != "" {
		// Keep a user-friendly download name while stripping header-breaking
		// characters. The immutable storage key is never exposed in a header.
		filename = strings.NewReplacer("\r", "", "\n", "", `"`, "'").Replace(filename)
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	}
}

func writeMediaError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	if errors.Is(err, identity.ErrForbidden) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok veya kayıt kapsamınız dışında.")
		return
	}
	if errors.Is(err, media.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "İstenen medya kaydı bulunamadı.")
		return
	}
	if errors.Is(err, media.ErrInvalidProduct) || errors.Is(err, media.ErrInvalidVariant) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "Ürün veya varyant bulunamadı.")
		return
	}
	if errors.Is(err, media.ErrConflict) || errors.Is(err, identity.ErrConflict) {
		writeError(w, r, http.StatusPreconditionFailed, "VERSION_CONFLICT", "Kayıt başka bir kullanıcı tarafından değiştirilmiş.")
		return
	}
	if errors.Is(err, storage.ErrUnsupportedImage) || errors.Is(err, storage.ErrHEICDisabled) || errors.Is(err, storage.ErrImageDimensions) || errors.Is(err, storage.ErrImageTooLarge) || errors.Is(err, storage.ErrWebPEncoderUnavailable) {
		writeError(w, r, http.StatusUnprocessableEntity, "IMAGE_INVALID", "Görsel biçimi veya ölçüleri desteklenmiyor.")
		return
	}
	if errors.Is(err, identity.ErrValidation) {
		message := strings.TrimPrefix(err.Error(), identity.ErrValidation.Error()+": ")
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", message)
		return
	}
	writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", fallback)
}
