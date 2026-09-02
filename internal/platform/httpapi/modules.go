package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/modules"
	"github.com/go-chi/chi/v5"
)

// modulePathPrefixes maps a request-path prefix to the feature module that owns
// it. requireSession consults this after resolving the session so a disabled
// module's endpoints answer 403 MODULE_DISABLED regardless of which mount
// registered them. Core routes (identity, settings, dashboard, currency,
// e-mail, media, search) are absent on purpose and always pass.
var modulePathPrefixes = []struct {
	prefix string
	module string
}{
	// HR (payroll lives under /api/v1/hr/* too).
	{"/api/v1/hr/", modules.HR},
	// Fixed assets. /api/v1/hr/employees/{id}/asset-assignments is HR-gated by
	// the earlier prefix, which is acceptable.
	{"/api/v1/fixed-asset", modules.FixedAsset},
	// Ön Muhasebe - cari.
	{"/api/v1/parties", modules.PreAccounting},
	{"/api/v1/party-movements", modules.PreAccounting},
	{"/api/v1/party-settings", modules.PreAccounting},
	// Ön Muhasebe - satış / alış.
	{"/api/v1/sales", modules.PreAccounting},
	{"/api/v1/purchases", modules.PreAccounting},
	// Ön Muhasebe - finans.
	{"/api/v1/finance", modules.PreAccounting},
	{"/api/v1/invoice-open-items", modules.PreAccounting},
	// Ön Muhasebe - stok / ürün.
	{"/api/v1/products", modules.PreAccounting},
	{"/api/v1/product-references", modules.PreAccounting},
	{"/api/v1/product-code-sequence", modules.PreAccounting},
	{"/api/v1/variant-definitions", modules.PreAccounting},
	{"/api/v1/variant-packages", modules.PreAccounting},
	{"/api/v1/warehouses", modules.PreAccounting},
	{"/api/v1/warehouse-transfers", modules.PreAccounting},
	{"/api/v1/stock-movements", modules.PreAccounting},
	{"/api/v1/stock-movement-operations", modules.PreAccounting},
	{"/api/v1/stock-counts", modules.PreAccounting},
	{"/api/v1/stock/", modules.PreAccounting},
	{"/api/v1/lots", modules.PreAccounting},
	{"/api/v1/serial-numbers", modules.PreAccounting},
	// Ön Muhasebe - raporlar ve aktarımlar.
	{"/api/v1/reports", modules.PreAccounting},
	{"/api/v1/imports", modules.PreAccounting},
	{"/api/v1/exports", modules.PreAccounting},
}

// moduleForPath returns the module owning path, or "" when the path is core or
// unmapped.
func moduleForPath(path string) string {
	for _, entry := range modulePathPrefixes {
		if strings.HasPrefix(path, entry.prefix) {
			return entry.module
		}
	}
	return ""
}

type moduleHandler struct{ service *identity.Service }

func mountModuleRoutes(router chi.Router, identityService *identity.Service) {
	auth := identityHandler{service: identityService}
	handler := moduleHandler{service: identityService}
	router.With(auth.requireSession).Get("/api/v1/modules", handler.list)
	router.With(auth.requireSession, auth.requireCSRF).Put("/api/v1/modules/{code}", handler.set)
}

func (h moduleHandler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListModules(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeModuleActivationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"modules": items})
}

func (h moduleHandler) set(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if decodeJSON(r, &input) != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Modül isteği geçersiz.")
		return
	}
	version := int64(0)
	if raw := r.Header.Get("If-Match"); raw != "" {
		parsed, err := parseIfMatch(raw)
		if err != nil {
			writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Modül güncellemesi için geçerli If-Match gereklidir.")
			return
		}
		version = parsed
	}
	item, err := h.service.SetModule(r.Context(), sessionFromRequest(r), chi.URLParam(r, "code"), input.Enabled, version, requestMeta(r))
	if err != nil {
		writeModuleActivationError(w, r, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func writeModuleActivationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok.")
	case errors.Is(err, identity.ErrConflict):
		writeError(w, r, http.StatusPreconditionFailed, "VERSION_CONFLICT", "Modül durumu başka bir kullanıcı tarafından değiştirildi.")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Bilinmeyen modül.")
	default:
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Modül durumu işlenemedi.")
	}
}
