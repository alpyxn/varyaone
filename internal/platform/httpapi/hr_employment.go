package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alpyxn/varyaone/internal/hr/employment"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/go-chi/chi/v5"
)

type hrEmploymentHandler struct{ service *employment.Service }

func mountHREmploymentRoutes(router chi.Router, identityService *identity.Service, service *employment.Service) {
	auth := identityHandler{service: identityService}
	h := hrEmploymentHandler{service: service}
	read := router.With(auth.requireSession)
	write := router.With(auth.requireSession, auth.requireCSRF)

	read.Get("/api/v1/hr/employees/{employeeID}/employments", h.listEmployments)
	write.Post("/api/v1/hr/employees/{employeeID}/employments", h.createEmployment)
	write.Post("/api/v1/hr/employees/{employeeID}/employments/{employmentID}/terminate", h.terminate)

	read.Get("/api/v1/hr/employees/{employeeID}/employment-terms", h.listTerms)
	write.Post("/api/v1/hr/employees/{employeeID}/employments/{employmentID}/terms", h.createTerm)
	write.Post("/api/v1/hr/employees/{employeeID}/employment-terms/{termID}/close", h.closeTerm)

	read.Get("/api/v1/hr/employees/{employeeID}/payroll-year-openings", h.listOpenings)
	write.Post("/api/v1/hr/employees/{employeeID}/payroll-year-openings", h.createOpening)
}

func (h hrEmploymentHandler) listEmployments(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListEmployments(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"))
	if err != nil {
		writeHREmploymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h hrEmploymentHandler) createEmployment(w http.ResponseWriter, r *http.Request) {
	var input employment.EmploymentInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Çalışma dönemi bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateEmployment(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"), input, requestMeta(r))
	if err != nil {
		writeHREmploymentError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusCreated, item)
}

func (h hrEmploymentHandler) terminate(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Çalışma dönemi sonlandırması için If-Match gereklidir.")
		return
	}
	var input employment.TerminateInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Sonlandırma bilgileri geçersiz.")
		return
	}
	item, err := h.service.TerminateEmployment(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"), chi.URLParam(r, "employmentID"), version, input, requestMeta(r))
	if err != nil {
		writeHREmploymentError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h hrEmploymentHandler) listTerms(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListTerms(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"), r.URL.Query().Get("employment_id"))
	if err != nil {
		writeHREmploymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h hrEmploymentHandler) createTerm(w http.ResponseWriter, r *http.Request) {
	var input employment.TermInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Ücret koşulu bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateTerm(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"), chi.URLParam(r, "employmentID"), input, requestMeta(r))
	if err != nil {
		writeHREmploymentError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusCreated, item)
}

func (h hrEmploymentHandler) closeTerm(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Ücret koşulu kapatma için If-Match gereklidir.")
		return
	}
	var input struct {
		EffectiveTo string `json:"effective_to"`
	}
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Bitiş tarihi geçersiz.")
		return
	}
	item, err := h.service.CloseTerm(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"), chi.URLParam(r, "termID"), version, input.EffectiveTo, requestMeta(r))
	if err != nil {
		writeHREmploymentError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h hrEmploymentHandler) listOpenings(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListOpenings(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"))
	if err != nil {
		writeHREmploymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h hrEmploymentHandler) createOpening(w http.ResponseWriter, r *http.Request) {
	var input employment.OpeningInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Açılış bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateOpening(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"), input, requestMeta(r))
	if err != nil {
		writeHREmploymentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func writeHREmploymentError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, identity.ErrConflict):
		writeError(w, r, http.StatusPreconditionFailed, "VERSION_CONFLICT", "Kayıt başka bir kullanıcı tarafından değiştirildi.")
	case errors.Is(err, employment.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "EMPLOYMENT_NOT_FOUND", "Çalışma dönemi bulunamadı.")
	case errors.Is(err, employment.ErrTermNotFound):
		writeError(w, r, http.StatusNotFound, "EMPLOYMENT_TERM_NOT_FOUND", "Ücret koşulu bulunamadı.")
	case errors.Is(err, employment.ErrEmployeeGone):
		writeError(w, r, http.StatusUnprocessableEntity, "EMPLOYMENT_EMPLOYEE_NOT_FOUND", "Çalışan bulunamadı.")
	case errors.Is(err, employment.ErrOpeningExists):
		writeError(w, r, http.StatusConflict, "PAYROLL_YEAR_OPENING_EXISTS", "Bu yıl ve kaynak için açılış kaydı zaten var.")
	case errors.Is(err, employment.ErrLegislationMissing):
		writeError(w, r, http.StatusUnprocessableEntity, "PAYROLL_LEGISLATION_NOT_FOUND", "Bu tarih için aktif bordro mevzuatı yok.")
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "İşlem tamamlanamadı.")
	}
}
