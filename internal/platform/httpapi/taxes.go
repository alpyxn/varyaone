package httpapi

import (
	"net/http"
	"strconv"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/taxes"
	"github.com/go-chi/chi/v5"
)

type taxHandler struct{ service *taxes.Service }

func mountTaxRoutes(router chi.Router, identityService *identity.Service, service *taxes.Service) {
	auth := identityHandler{service: identityService}
	h := taxHandler{service: service}
	router.Route("/api/v1/taxes", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/definitions", h.listDefinitions)
		r.With(auth.requireCSRF).Post("/definitions", h.createDefinition)
		r.With(auth.requireCSRF).Put("/definitions/{definitionID}", h.updateDefinition)
		r.With(auth.requireCSRF).Post("/definitions/{definitionID}/deactivate", h.deactivateDefinition)
		r.Get("/definitions/{definitionID}/rates", h.listRates)
		r.With(auth.requireCSRF).Post("/definitions/{definitionID}/rates", h.createRate)
		r.Get("/exemptions", h.listExemptions)
		r.Get("/withholding-rules", h.listWithholdingRules)
	})
}

func (h taxHandler) listDefinitions(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListDefinitions(r.Context(), sessionFromRequest(r), r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		writeModuleError(w, r, err, "Vergi tanımları okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h taxHandler) createDefinition(w http.ResponseWriter, r *http.Request) {
	var input taxes.TaxDefinition
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Vergi tanımı bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateDefinition(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Vergi tanımı oluşturulamadı.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusCreated, item)
}
func (h taxHandler) updateDefinition(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Vergi tanımı güncellemesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	var input taxes.TaxDefinition
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Vergi tanımı bilgileri geçersiz.")
		return
	}
	item, err := h.service.UpdateDefinition(r.Context(), sessionFromRequest(r), chi.URLParam(r, "definitionID"), version, input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Vergi tanımı güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h taxHandler) deactivateDefinition(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Vergi tanımı pasifleştirmesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	item, err := h.service.DeactivateDefinition(r.Context(), sessionFromRequest(r), chi.URLParam(r, "definitionID"), version, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Vergi tanımı pasifleştirilemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h taxHandler) listRates(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListRates(r.Context(), sessionFromRequest(r), chi.URLParam(r, "definitionID"), r.URL.Query().Get("on"))
	if err != nil {
		writeModuleError(w, r, err, "Vergi oranları okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h taxHandler) createRate(w http.ResponseWriter, r *http.Request) {
	var input taxes.TaxRate
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Vergi oranı bilgileri geçersiz.")
		return
	}
	input.TaxDefinitionID = chi.URLParam(r, "definitionID")
	item, err := h.service.CreateRate(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Vergi oranı oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
func (h taxHandler) listExemptions(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListExemptions(r.Context(), sessionFromRequest(r), r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		writeModuleError(w, r, err, "Vergi istisnaları okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h taxHandler) listWithholdingRules(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListWithholdingRules(r.Context(), sessionFromRequest(r), r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		writeModuleError(w, r, err, "Tevkifat kuralları okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
