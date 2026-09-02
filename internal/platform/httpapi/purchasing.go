package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/inventory"
	"github.com/alpyxn/varyaone/internal/platform/idempotency"
	"github.com/alpyxn/varyaone/internal/purchasing"
	"github.com/go-chi/chi/v5"
)

func mountPurchasingRoutes(router chi.Router, identityService *identity.Service, service *purchasing.Service) {
	// Typed purchasing routes are the only public commercial contract. The old
	// flat resource paths are deliberately not mounted as compatibility
	// adapters.
	mountTypedPurchasingRoutes(router, identityService, service)
	mountLandedCostRoutes(router, identityService, service)
}

func writePurchasingError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	switch {
	case errors.Is(err, idempotency.ErrKeyRequired):
		writeError(w, r, http.StatusPreconditionRequired, "IDEMPOTENCY_KEY_REQUIRED", "Bu işlem için Idempotency-Key gereklidir.")
	case errors.Is(err, idempotency.ErrPayloadConflict):
		writeError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Aynı Idempotency-Key farklı bir işlem için kullanıldı.")
	case errors.Is(err, idempotency.ErrCommandInProgress):
		writeError(w, r, http.StatusConflict, "COMMAND_IN_PROGRESS", "Aynı işlem hâlen yürütülüyor.")
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok veya kayıt kapsamınız dışında.")
	case errors.Is(err, identity.ErrConflict):
		writeError(w, r, http.StatusPreconditionFailed, purchasing.ErrDocumentModified.Error(), "Bu belge başka bir kullanıcı tarafından değiştirildi. Güncel hali yükleyin.")
	case errors.Is(err, purchasing.ErrOverDelivery):
		writeError(w, r, http.StatusConflict, "ORDER_LINE_OVER_FULFILLMENT", "Kaynak satır miktarı aşılamaz.")
	case errors.Is(err, purchasing.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "İstenen satın alma kaydı bulunamadı.")
	case errors.Is(err, purchasing.ErrInvalidTransition):
		writeError(w, r, http.StatusConflict, "INVALID_STATE_TRANSITION", "Belge mevcut durumunda bu işleme uygun değil.")
	case errors.Is(err, purchasing.ErrDocumentHasNoLines):
		writeError(w, r, http.StatusUnprocessableEntity, "DOCUMENT_HAS_NO_LINES", "Belgeyi kesinleştirmeden önce en az bir satır ekleyin.")
	case errors.Is(err, purchasing.ErrProductInactive):
		writeError(w, r, http.StatusConflict, "PRODUCT_INACTIVE", "Seçilen ürün artık aktif değil.")
	case errors.Is(err, purchasing.ErrVariantInactive):
		writeError(w, r, http.StatusConflict, "VARIANT_INACTIVE", "Seçilen varyant artık aktif değil.")
	case errors.Is(err, purchasing.ErrSupplierInactive):
		writeError(w, r, http.StatusConflict, "PARTY_INACTIVE", "Seçilen tedarikçi artık aktif değil.")
	case errors.Is(err, purchasing.ErrWarehouseInactive):
		writeError(w, r, http.StatusConflict, "WAREHOUSE_INACTIVE", "Seçilen depo artık aktif değil.")
	case errors.Is(err, purchasing.ErrWarehouseRequired):
		writeError(w, r, http.StatusUnprocessableEntity, "WAREHOUSE_REQUIRED", "Ürün satırı için depo gereklidir.")
	case errors.Is(err, purchasing.ErrVariantRequired):
		writeError(w, r, http.StatusUnprocessableEntity, "VARIANT_REQUIRED", "Varyantlı ürün için varyant seçilmelidir.")
	case errors.Is(err, purchasing.ErrExchangeRateUnavailable):
		writeError(w, r, http.StatusServiceUnavailable, "EXCHANGE_RATE_UNAVAILABLE", "Güncel kur kaynağına ulaşılamadı; kayıtlı kur yoksa belge oluşturulamaz.")
	case errors.Is(err, inventory.ErrInsufficientStock):
		writeError(w, r, http.StatusConflict, "INSUFFICIENT_AVAILABLE_STOCK", "Kullanılabilir stok yetersiz.")
	case errors.Is(err, finance.ErrPeriodLocked):
		writeError(w, r, http.StatusConflict, "DOCUMENT_PERIOD_LOCKED", "Belge tarihi kilitli dönemdedir.")
	case finance.ErrorCode(err) != "":
		message := finance.ErrorMessage(err)
		if message == "" {
			message = "Satın alma belgesi finans işlemi tamamlanamadı."
		} else {
			message = strings.ToUpper(message[:1]) + message[1:]
		}
		writeError(w, r, http.StatusConflict, finance.ErrorCode(err), message)
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", strings.TrimPrefix(err.Error(), identity.ErrValidation.Error()+": "))
	default:
		if writeConcurrencyConflictIfSerializationFailure(w, r, err) {
			return
		}
		// Keep the public response generic, but retain the trace-correlated
		// server error so database/serialization failures are diagnosable. Do
		// not log request bodies, headers or credentials here.
		slog.Default().Error("purchasing request failed", "trace_id", TraceID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", fallback)
	}
}

type landedCostHandler struct{ service *purchasing.Service }

func (h landedCostHandler) create(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyHeader(w, r) {
		return
	}
	var input purchasing.LandedCostInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Masraf dağıtımı bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateLandedCost(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writePurchasingError(w, r, err, "Masraf dağıtımı oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h landedCostHandler) get(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetLandedCost(r.Context(), sessionFromRequest(r), chi.URLParam(r, "landedCostID"))
	if err != nil {
		writePurchasingError(w, r, err, "Masraf dağıtımı okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h landedCostHandler) post(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyHeader(w, r) {
		return
	}
	version, err := parseRequiredIfMatch(r)
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Masraf dağıtımını kesinleştirmek için güncel If-Match sürümü gereklidir.")
		return
	}
	item, err := h.service.PostLandedCost(r.Context(), sessionFromRequest(r), chi.URLParam(r, "landedCostID"), version, requestMeta(r))
	if err != nil {
		writePurchasingError(w, r, err, "Masraf dağıtımı kesinleştirilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func mountLandedCostRoutes(router chi.Router, identityService *identity.Service, service *purchasing.Service) {
	auth := identityHandler{service: identityService}
	h := landedCostHandler{service: service}
	router.Route("/api/v1/purchases/landed-costs", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/{landedCostID}", h.get)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Post("/", h.create)
			r.Post("/{landedCostID}/post", h.post)
		})
	})
}
