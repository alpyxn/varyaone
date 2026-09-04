package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/alpyxn/varyaone/internal/hr/employee"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/go-chi/chi/v5"
)

type hrEmployeeHandler struct{ service *employee.Service }

func mountHREmployeeRoutes(router chi.Router, identityService *identity.Service, service *employee.Service) {
	auth := identityHandler{service: identityService}
	handler := hrEmployeeHandler{service: service}
	read := router.With(auth.requireSession)
	read.Get("/api/v1/hr/employees", handler.list)
	read.Get("/api/v1/hr/employees/readiness", handler.readiness)
	read.Get("/api/v1/hr/employees/{employeeID}", handler.get)
	read.Get("/api/v1/hr/employees/{employeeID}/private-profile", handler.getPrivate)
	read.Get("/api/v1/hr/employees/{employeeID}/address", handler.getAddress)
	read.Get("/api/v1/hr/occupation-codes", handler.searchOccupationCodes)
	write := router.With(auth.requireSession, auth.requireCSRF)
	write.Post("/api/v1/hr/employees", handler.create)
	write.Patch("/api/v1/hr/employees/{employeeID}", handler.update)
	write.Patch("/api/v1/hr/employees/{employeeID}/private-profile", handler.putPrivate)
	write.Put("/api/v1/hr/employees/{employeeID}/address", handler.putAddress)
}
func (h hrEmployeeHandler) list(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	result, err := h.service.List(r.Context(), sessionFromRequest(r), r.URL.Query().Get("q"), r.URL.Query().Get("status"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeHREmployeeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// readiness reports what is missing on each employee card before the given
// month can be put on the puantaj or run through the bordro.
func (h hrEmployeeHandler) readiness(w http.ResponseWriter, r *http.Request) {
	year, _ := strconv.Atoi(r.URL.Query().Get("year"))
	month, _ := strconv.Atoi(r.URL.Query().Get("month"))
	if year == 0 || month == 0 {
		now := time.Now().UTC()
		year, month = now.Year(), int(now.Month())
	}
	items, err := h.service.ListReadiness(r.Context(), sessionFromRequest(r), year, month)
	if err != nil {
		writeHREmployeeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h hrEmployeeHandler) get(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.Get(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"))
	if err != nil {
		writeHREmployeeError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h hrEmployeeHandler) create(w http.ResponseWriter, r *http.Request) {
	var input employee.Input
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Çalışan bilgileri geçersiz.")
		return
	}
	item, err := h.service.Create(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeHREmployeeError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusCreated, item)
}
func (h hrEmployeeHandler) update(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Çalışan güncellemesi için If-Match gereklidir.")
		return
	}
	var input employee.Input
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Çalışan bilgileri geçersiz.")
		return
	}
	item, err := h.service.Update(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"), version, input, requestMeta(r))
	if err != nil {
		writeHREmployeeError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h hrEmployeeHandler) getPrivate(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetPrivate(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"))
	if err != nil {
		writeHREmployeeError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h hrEmployeeHandler) putPrivate(w http.ResponseWriter, r *http.Request) {
	version := int64(0)
	if r.Header.Get("If-Match") != "" {
		parsed, err := parseIfMatch(r.Header.Get("If-Match"))
		if err != nil {
			writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Özel profil güncellemesi için If-Match gereklidir.")
			return
		}
		version = parsed
	}
	var input employee.PrivateProfileInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Özel profil bilgileri geçersiz.")
		return
	}
	item, err := h.service.PutPrivate(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"), version, input, requestMeta(r))
	if err != nil {
		writeHREmployeeError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h hrEmployeeHandler) searchOccupationCodes(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.service.SearchOccupationCodes(r.Context(), sessionFromRequest(r), r.URL.Query().Get("q"), limit)
	if err != nil {
		writeHREmployeeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h hrEmployeeHandler) getAddress(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetAddress(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"))
	if err != nil {
		writeHREmployeeError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h hrEmployeeHandler) putAddress(w http.ResponseWriter, r *http.Request) {
	version := int64(0)
	if r.Header.Get("If-Match") != "" {
		parsed, err := parseIfMatch(r.Header.Get("If-Match"))
		if err != nil {
			writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Adres güncellemesi için If-Match gereklidir.")
			return
		}
		version = parsed
	}
	var input employee.AddressInput
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Adres bilgileri geçersiz.")
		return
	}
	item, err := h.service.PutAddress(r.Context(), sessionFromRequest(r), chi.URLParam(r, "employeeID"), version, input, requestMeta(r))
	if err != nil {
		writeHREmployeeError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func writeHREmployeeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
	case errors.Is(err, identity.ErrConflict):
		writeError(w, r, http.StatusPreconditionFailed, "VERSION_CONFLICT", "Çalışan kaydı başka bir kullanıcı tarafından değiştirildi.")
	case errors.Is(err, employee.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "EMPLOYEE_NOT_FOUND", "Çalışan bulunamadı.")
	case errors.Is(err, employee.ErrInvalidPeriod):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Dönem yıl/ay geçersiz.")
	case errors.Is(err, employee.ErrLegislationMissing):
		writeError(w, r, http.StatusUnprocessableEntity, "PAYROLL_LEGISLATION_NOT_FOUND",
			"İşe giriş tarihini kapsayan aktif bordro mevzuatı yok; asgari ücret okunamadı.")
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Çalışan işlemi tamamlanamadı.")
	}
}
