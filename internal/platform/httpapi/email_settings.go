package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/payroll/delivery"
	"github.com/go-chi/chi/v5"
)

type emailSettingsHandler struct{ service *delivery.SMTPSettingsService }

func mountEmailSettingsRoutes(router chi.Router, identityService *identity.Service, service *delivery.SMTPSettingsService) {
	auth := identityHandler{service: identityService}
	handler := emailSettingsHandler{service: service}
	read := router.With(auth.requireSession)
	read.Get("/api/v1/settings/email", handler.get)
	write := router.With(auth.requireSession, auth.requireCSRF)
	write.Put("/api/v1/settings/email", handler.put)
	write.Post("/api/v1/settings/email/test", handler.test)
}
func (h emailSettingsHandler) get(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.Get(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeEmailSettingsError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h emailSettingsHandler) put(w http.ResponseWriter, r *http.Request) {
	var input delivery.SMTPSettingsInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "E-posta ayarları geçersiz.")
		return
	}
	version := int64(0)
	if r.Header.Get("If-Match") != "" {
		parsed, err := parseIfMatch(r.Header.Get("If-Match"))
		if err != nil {
			writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Güncelleme için geçerli If-Match gereklidir.")
			return
		}
		version = parsed
	}
	item, err := h.service.Put(r.Context(), sessionFromRequest(r), version, input, requestMeta(r))
	if err != nil {
		writeEmailSettingsError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h emailSettingsHandler) test(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.Test(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeEmailSettingsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func writeEmailSettingsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case errors.Is(err, delivery.ErrSMTPSettingsNotFound):
		writeError(w, r, http.StatusNotFound, "SMTP_SETTINGS_NOT_FOUND", "E-posta ayarları bulunamadı.")
	case errors.Is(err, delivery.ErrSMTPValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "SMTP_SETTINGS_INVALID", "TLS e-posta ayarları geçersiz.")
	case errors.Is(err, delivery.ErrSMTPVersionConflict):
		writeError(w, r, http.StatusPreconditionFailed, "VERSION_CONFLICT", "E-posta ayarları başka bir kullanıcı tarafından değiştirildi.")
	case err.Error() == "SMTP_TEST_FAILED":
		writeError(w, r, http.StatusUnprocessableEntity, "SMTP_TEST_FAILED", "SMTP bağlantısı veya kimlik doğrulaması başarısız oldu.")
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "E-posta ayarları işlenemedi.")
	}
}
