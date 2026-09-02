package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/pricing"
	"github.com/go-chi/chi/v5"
)

type pricingHandler struct{ service *pricing.Service }

func mountPricingRoutes(router chi.Router, identityService *identity.Service, service *pricing.Service) {
	auth := identityHandler{service: identityService}
	h := pricingHandler{service: service}
	router.Route("/api/v1/pricing/currencies", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/", h.listCurrencies)
		r.With(auth.requireCSRF).Post("/", h.createCurrency)
		r.With(auth.requireCSRF).Put("/{code}", h.updateCurrency)
	})
	router.Route("/api/v1/price-lists", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/", h.listPriceLists)
		r.With(auth.requireCSRF).Post("/", h.createPriceList)
		r.With(auth.requireCSRF).Put("/{priceListID}", h.updatePriceList)
		r.With(auth.requireCSRF).Post("/{priceListID}/deactivate", h.deactivatePriceList)
		r.With(auth.requireCSRF).Post("/{priceListID}/activate", h.activatePriceList)
		r.Get("/{priceListID}/entries", h.listEntries)
		r.Get("/{priceListID}/resolve", h.resolvePrice)
		r.With(auth.requireCSRF).Post("/{priceListID}/entries", h.createEntry)
		r.With(auth.requireCSRF).Put("/{priceListID}/entries/{entryID}", h.updateEntry)
	})
}

func (h pricingHandler) listCurrencies(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListCurrencies(r.Context(), sessionFromRequest(r), r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		writeModuleError(w, r, err, "Para birimleri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h pricingHandler) createCurrency(w http.ResponseWriter, r *http.Request) {
	var input pricing.Currency
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Para birimi bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateCurrency(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Para birimi oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
func (h pricingHandler) updateCurrency(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Para birimi güncellemesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	var input pricing.Currency
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Para birimi bilgileri geçersiz.")
		return
	}
	item, err := h.service.UpdateCurrency(r.Context(), sessionFromRequest(r), chi.URLParam(r, "code"), version, input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Para birimi güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h pricingHandler) listPriceLists(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListPriceLists(r.Context(), sessionFromRequest(r), r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		writeModuleError(w, r, err, "Fiyat listeleri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h pricingHandler) createPriceList(w http.ResponseWriter, r *http.Request) {
	var input pricing.PriceList
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Fiyat listesi bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreatePriceList(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Fiyat listesi oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
func (h pricingHandler) updatePriceList(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Fiyat listesi güncellemesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	var input pricing.PriceList
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Fiyat listesi bilgileri geçersiz.")
		return
	}
	item, err := h.service.UpdatePriceList(r.Context(), sessionFromRequest(r), chi.URLParam(r, "priceListID"), version, input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Fiyat listesi güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h pricingHandler) deactivatePriceList(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Fiyat listesi pasifleştirmesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	item, err := h.service.DeactivatePriceList(r.Context(), sessionFromRequest(r), chi.URLParam(r, "priceListID"), version, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Fiyat listesi pasifleştirilemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h pricingHandler) activatePriceList(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Fiyat listesi etkinleştirmesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	item, err := h.service.ActivatePriceList(r.Context(), sessionFromRequest(r), chi.URLParam(r, "priceListID"), version, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Fiyat listesi etkinleştirilemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
func (h pricingHandler) listEntries(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListPriceEntries(r.Context(), sessionFromRequest(r), chi.URLParam(r, "priceListID"), r.URL.Query().Get("item_id"), r.URL.Query().Get("variant_id"), r.URL.Query().Get("on"))
	if err != nil {
		writeModuleError(w, r, err, "Fiyat satırları okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h pricingHandler) resolvePrice(w http.ResponseWriter, r *http.Request) {
	itemID := r.URL.Query().Get("item_id")
	if itemID == "" {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Fiyat çözümlemesi için ürün gereklidir.")
		return
	}
	on := r.URL.Query().Get("on")
	if on == "" {
		on = time.Now().Format("2006-01-02")
	}
	item, err := h.service.ResolvePrice(r.Context(), sessionFromRequest(r), chi.URLParam(r, "priceListID"), itemID, r.URL.Query().Get("variant_id"), on)
	if err != nil {
		writeModuleError(w, r, err, "Fiyat bulunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (h pricingHandler) createEntry(w http.ResponseWriter, r *http.Request) {
	var input pricing.PriceEntry
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Fiyat satırı bilgileri geçersiz.")
		return
	}
	input.PriceListID = chi.URLParam(r, "priceListID")
	item, err := h.service.CreatePriceEntry(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Fiyat satırı oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
func (h pricingHandler) updateEntry(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Fiyat satırı güncellemesi için geçerli If-Match başlığı gereklidir.")
		return
	}
	var input pricing.PriceEntry
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Fiyat satırı bilgileri geçersiz.")
		return
	}
	input.PriceListID = chi.URLParam(r, "priceListID")
	item, err := h.service.UpdatePriceEntry(r.Context(), sessionFromRequest(r), chi.URLParam(r, "entryID"), version, input, requestMeta(r))
	if err != nil {
		writeModuleError(w, r, err, "Fiyat satırı güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}
