package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alpyxn/varyaone/internal/email"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/go-chi/chi/v5"
)

type emailHandler struct {
	templates *email.TemplateService
	compose   *email.ComposeService
}

func mountEmailRoutes(router chi.Router, identityService *identity.Service, templates *email.TemplateService, compose *email.ComposeService) {
	auth := identityHandler{service: identityService}
	h := emailHandler{templates: templates, compose: compose}
	read := router.With(auth.requireSession)
	write := router.With(auth.requireSession, auth.requireCSRF)

	read.Get("/api/v1/email-templates", h.listTemplates)
	read.Get("/api/v1/email-templates/{templateID}", h.getTemplate)
	write.Post("/api/v1/email-templates", h.createTemplate)
	write.Patch("/api/v1/email-templates/{templateID}", h.updateTemplate)
	write.Post("/api/v1/email-templates/{templateID}/status", h.setTemplateStatus)

	write.Post("/api/v1/email/preview", h.preview)
	write.Post("/api/v1/email/messages", h.send)
}

func (h emailHandler) listTemplates(w http.ResponseWriter, r *http.Request) {
	includeArchived := r.URL.Query().Get("status") == "all" || r.URL.Query().Get("archived") == "true"
	items, err := h.templates.List(r.Context(), sessionFromRequest(r), r.URL.Query().Get("scope"), includeArchived)
	if err != nil {
		writeEmailError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h emailHandler) getTemplate(w http.ResponseWriter, r *http.Request) {
	item, err := h.templates.Get(r.Context(), sessionFromRequest(r), chi.URLParam(r, "templateID"))
	if err != nil {
		writeEmailError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h emailHandler) createTemplate(w http.ResponseWriter, r *http.Request) {
	var input email.TemplateInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Taslak verisi geçersiz.")
		return
	}
	item, err := h.templates.Create(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeEmailError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h emailHandler) updateTemplate(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Taslak güncellemesi için If-Match gereklidir.")
		return
	}
	var input email.TemplateInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Taslak verisi geçersiz.")
		return
	}
	item, err := h.templates.Update(r.Context(), sessionFromRequest(r), chi.URLParam(r, "templateID"), version, input, requestMeta(r))
	if err != nil {
		writeEmailError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h emailHandler) setTemplateStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Active bool `json:"active"`
	}
	if decodeJSON(r, &body) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Durum verisi geçersiz.")
		return
	}
	item, err := h.templates.SetActive(r.Context(), sessionFromRequest(r), chi.URLParam(r, "templateID"), body.Active, requestMeta(r))
	if err != nil {
		writeEmailError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h emailHandler) preview(w http.ResponseWriter, r *http.Request) {
	var input email.SendRequest
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Önizleme verisi geçersiz.")
		return
	}
	result, err := h.compose.Preview(r.Context(), sessionFromRequest(r), input)
	if err != nil {
		writeEmailError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h emailHandler) send(w http.ResponseWriter, r *http.Request) {
	var input email.SendRequest
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Gönderim verisi geçersiz.")
		return
	}
	result, err := h.compose.Send(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeEmailError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeEmailError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, identity.ErrConflict):
		writeError(w, r, http.StatusPreconditionFailed, "VERSION_CONFLICT", "Taslak başka bir kullanıcı tarafından değiştirildi.")
	case errors.Is(err, email.ErrTemplateNotFound):
		writeError(w, r, http.StatusNotFound, "EMAIL_TEMPLATE_NOT_FOUND", "E-posta taslağı bulunamadı.")
	case errors.Is(err, email.ErrSMTPNotConfigured):
		writeError(w, r, http.StatusUnprocessableEntity, "SMTP_SETTINGS_NOT_FOUND", "E-posta gönderimi için SMTP ayarları yapılmamış.")
	case errors.Is(err, email.ErrNothingToSend):
		writeError(w, r, http.StatusUnprocessableEntity, "EMAIL_NOTHING_TO_SEND", "Gönderilecek geçerli alıcı yok.")
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "İşlem tamamlanamadı.")
	}
}
