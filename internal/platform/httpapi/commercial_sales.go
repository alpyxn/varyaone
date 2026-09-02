package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/inventory"
	"github.com/alpyxn/varyaone/internal/platform/httpapi/contract"
	"github.com/alpyxn/varyaone/internal/platform/idempotency"
	"github.com/alpyxn/varyaone/internal/sales"
	"github.com/go-chi/chi/v5"
)

type commercialSalesHandler struct {
	service *sales.Service
	kind    sales.CommercialKind
}

func mountTypedSalesRoutes(router chi.Router, identityService *identity.Service, service *sales.Service) {
	auth := identityHandler{service: identityService}
	router.Route("/api/v1/sales", func(r chi.Router) {
		for _, resource := range []string{"quotes", "orders", "dispatches", "invoices", "returns"} {
			kind, _ := sales.CommercialKindForResource(resource)
			h := commercialSalesHandler{service: service, kind: kind}
			r.Route("/"+resource, func(r chi.Router) {
				r.Use(auth.requireSession)
				r.Get("/", h.list)
				r.Get("/{documentID}", h.get)
				r.Group(func(r chi.Router) {
					r.Use(auth.requireCSRF)
					r.Post("/", h.create)
					r.Post("/create-from-source", h.createFromSource)
					r.Put("/{documentID}", h.update)
					r.Delete("/{documentID}", h.delete)
					r.Post("/{documentID}/convert", h.convert)
					r.Post("/{documentID}/{command}", h.command)
				})
			})
		}
	})
}

func (h commercialSalesHandler) list(w http.ResponseWriter, r *http.Request) {
	options := sales.CommercialListOptions{
		Status:            r.URL.Query().Get("status"),
		LifecycleStatus:   r.URL.Query().Get("lifecycle_status"),
		FulfillmentStatus: r.URL.Query().Get("fulfillment_status"),
		InvoicingStatus:   r.URL.Query().Get("invoicing_status"),
		PaymentStatus:     r.URL.Query().Get("payment_status"),
		PartyID:           r.URL.Query().Get("party_id"),
		BranchID:          r.URL.Query().Get("branch_id"),
		CurrencyCode:      r.URL.Query().Get("currency_code"),
		ForReference:      r.URL.Query().Get("for_reference") == "true",
		ReferenceTarget:   r.URL.Query().Get("reference_target"),
		Cursor:            r.URL.Query().Get("cursor"),
		Search:            r.URL.Query().Get("q"),
		Sort:              r.URL.Query().Get("sort"),
		Limit:             queryLimit(r, 50, 100),
	}
	var err error
	if value := strings.TrimSpace(r.URL.Query().Get("from")); value != "" {
		parsed, parseErr := time.Parse("2006-01-02", value)
		if parseErr != nil {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Başlangıç tarihi geçersiz.")
			return
		}
		options.From = &parsed
	}
	if value := strings.TrimSpace(r.URL.Query().Get("to")); value != "" {
		parsed, parseErr := time.Parse("2006-01-02", value)
		if parseErr != nil {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Bitiş tarihi geçersiz.")
			return
		}
		options.To = &parsed
	}
	result, err := h.service.ListCommercialDocuments(r.Context(), sessionFromRequest(r), h.kind, options)
	if err != nil {
		writeCommercialError(w, r, err, "Belgeler okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h commercialSalesHandler) get(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetCommercialDocument(r.Context(), sessionFromRequest(r), h.kind, chi.URLParam(r, "documentID"))
	if err != nil {
		writeCommercialError(w, r, err, "Belge okunamadı.")
		return
	}
	writeCommercialDocument(w, http.StatusOK, item)
}

func (h commercialSalesHandler) create(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyHeader(w, r) {
		return
	}
	var input sales.CommercialDocumentInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Belge bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateCommercialDraft(r.Context(), sessionFromRequest(r), h.kind, input, requestMeta(r))
	if err != nil {
		writeCommercialError(w, r, err, "Belge oluşturulamadı.")
		return
	}
	writeCommercialDocument(w, http.StatusCreated, item)
}

func (h commercialSalesHandler) createFromSource(w http.ResponseWriter, r *http.Request) {
	// The conversion endpoint accepts the same canonical input as draft create;
	// the service validates source type, company, branch, party, currency and
	// line allocations before persisting the new draft.
	h.create(w, r)
}

func (h commercialSalesHandler) convert(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyHeader(w, r) {
		return
	}
	version, err := parseRequiredIfMatch(r)
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Dönüştürme için güncel If-Match sürümü gereklidir.")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if h.kind == sales.SalesReturn {
		if err = decodeJSON(r, &body); err != nil {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İade bilgileri geçersiz.")
			return
		}
	}
	item, err := h.service.ConvertCommercial(r.Context(), sessionFromRequest(r), h.kind, chi.URLParam(r, "documentID"), version, requestMeta(r), strings.TrimSpace(body.Reason))
	if err != nil {
		writeCommercialError(w, r, err, "Belge dönüştürülemedi.")
		return
	}
	writeCommercialDocument(w, http.StatusCreated, item)
}

func (h commercialSalesHandler) update(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyHeader(w, r) {
		return
	}
	version, err := parseRequiredIfMatch(r)
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Taslak güncellemesi için güncel If-Match sürümü gereklidir.")
		return
	}
	var input sales.CommercialDocumentInput
	if err = decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Belge bilgileri geçersiz.")
		return
	}
	item, err := h.service.UpdateCommercialDraft(r.Context(), sessionFromRequest(r), h.kind, chi.URLParam(r, "documentID"), version, input, requestMeta(r))
	if err != nil {
		writeCommercialError(w, r, err, "Taslak güncellenemedi.")
		return
	}
	writeCommercialDocument(w, http.StatusOK, item)
}

func (h commercialSalesHandler) delete(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyHeader(w, r) {
		return
	}
	version, err := parseRequiredIfMatch(r)
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Taslak silme işlemi için güncel If-Match sürümü gereklidir.")
		return
	}
	if err = h.service.DeleteCommercialDraft(r.Context(), sessionFromRequest(r), h.kind, chi.URLParam(r, "documentID"), version, requestMeta(r)); err != nil {
		writeCommercialError(w, r, err, "Taslak silinemedi.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h commercialSalesHandler) command(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyHeader(w, r) {
		return
	}
	version, err := parseRequiredIfMatch(r)
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Belge işlemi için güncel If-Match sürümü gereklidir.")
		return
	}
	command := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "command")))
	if command != "send" && command != "accept" && command != "reject" && command != "confirm" && command != "post" && command != "finalize" && command != "cancel" {
		writeError(w, r, http.StatusBadRequest, "INVALID_DOCUMENT_RELATION", "Belge işlemi geçersiz.")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if command == "cancel" {
		if err = decodeJSON(r, &body); err != nil {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İptal bilgileri geçersiz.")
			return
		}
	}
	if command == "finalize" {
		command = "post"
	}
	item, err := h.service.TransitionCommercial(r.Context(), sessionFromRequest(r), h.kind, chi.URLParam(r, "documentID"), command, version, requestMeta(r), strings.TrimSpace(body.Reason))
	if err != nil {
		writeCommercialError(w, r, err, "Belge işlemi tamamlanamadı.")
		return
	}
	writeCommercialDocument(w, http.StatusOK, item)
}

func writeCommercialDocument(w http.ResponseWriter, status int, item sales.CommercialDocument) {
	if item.Version > 0 {
		w.Header().Set("ETag", strconv.FormatInt(item.Version, 10))
	}
	writeJSON(w, status, item)
}

func writeCommercialError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	if errors.Is(err, sales.ErrCommercialNotFound) {
		writeError(w, r, http.StatusNotFound, "NOT_FOUND", "İstenen belge bulunamadı.")
		return
	}
	var commercialErr *sales.CommercialError
	if errors.As(err, &commercialErr) {
		status := http.StatusUnprocessableEntity
		switch commercialErr.Code {
		case sales.CommercialErrorAlreadyPosted, sales.CommercialErrorNotEditable, sales.CommercialErrorOverFulfillment, sales.CommercialErrorInsufficientStock:
			status = http.StatusConflict
		case sales.CommercialErrorProductInactive, sales.CommercialErrorVariantInactive, sales.CommercialErrorPartyInactive, sales.CommercialErrorWarehouseInactive, sales.CommercialErrorInvalidStateTransition, sales.CommercialErrorCalculationChanged, sales.CommercialErrorSourceAlreadyConsumed, sales.CommercialErrorSourceCancelled, sales.CommercialErrorSourcePartyMismatch, sales.CommercialErrorSourceCurrencyMismatch, sales.CommercialErrorDocumentHasDependencies:
			status = http.StatusConflict
		case sales.CommercialErrorPeriodLocked:
			status = http.StatusConflict
		case sales.CommercialErrorWarehouseUnauthorized:
			status = http.StatusForbidden
		case sales.CommercialErrorExchangeRateUnavailable:
			status = http.StatusServiceUnavailable
		}
		message := commercialErrorMessage(commercialErr.Code)
		// A dependency error names the specific downstream document, so surface
		// the domain message rather than the generic per-code text.
		if commercialErr.Code == sales.CommercialErrorDocumentHasDependencies && commercialErr.Err != nil {
			if detail := strings.TrimSpace(commercialErr.Err.Error()); detail != "" {
				message = strings.ToUpper(detail[:1]) + detail[1:]
			}
		}
		details := map[string]any{}
		if commercialErr.Field != "" {
			details["field"] = commercialErr.Field
			details["message"] = message
		}
		if commercialErr.Line > 0 {
			details["line"] = commercialErr.Line
			details["message"] = message
		}
		writeCommercialErrorDetails(w, r, status, commercialErr.Code, message, details)
		return
	}
	if errors.Is(err, idempotency.ErrKeyRequired) {
		writeError(w, r, http.StatusPreconditionRequired, "IDEMPOTENCY_KEY_REQUIRED", "Bu işlem için Idempotency-Key gereklidir.")
		return
	}
	if errors.Is(err, idempotency.ErrPayloadConflict) {
		writeError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Aynı anahtar farklı belge verisiyle kullanıldı.")
		return
	}
	if errors.Is(err, idempotency.ErrCommandInProgress) {
		writeError(w, r, http.StatusConflict, "COMMAND_IN_PROGRESS", "Aynı belge işlemi hâlen yürütülüyor.")
		return
	}
	if errors.Is(err, identity.ErrForbidden) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok veya kayıt kapsamınız dışında.")
		return
	}
	if errors.Is(err, identity.ErrConflict) {
		writeError(w, r, http.StatusPreconditionFailed, sales.CommercialErrorDocumentModified, "Bu belge başka bir kullanıcı tarafından değiştirildi. Güncel hali yükleyin.")
		return
	}
	if errors.Is(err, inventory.ErrInsufficientStock) {
		writeError(w, r, http.StatusConflict, sales.CommercialErrorInsufficientStock, "Kullanılabilir stok yetersiz.")
		return
	}
	if code := finance.ErrorCode(err); code != "" {
		message := finance.ErrorMessage(err)
		if message == "" {
			message = "Belge finans işlemi tamamlanamadı."
		} else {
			message = strings.ToUpper(message[:1]) + message[1:]
		}
		writeError(w, r, http.StatusConflict, code, message)
		return
	}
	if errors.Is(err, identity.ErrValidation) {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", strings.TrimPrefix(err.Error(), identity.ErrValidation.Error()+": "))
		return
	}
	if writeConcurrencyConflictIfSerializationFailure(w, r, err) {
		return
	}
	// Keep the public response generic, but retain the trace-correlated server
	// error so database/serialization failures are diagnosable. Do not log
	// request bodies, headers or credentials here.
	slog.Default().Error("commercial request failed", "trace_id", TraceID(r.Context()), "error", err)
	writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", fallback)
}

func commercialErrorMessage(code string) string {
	switch code {
	case sales.CommercialErrorInsufficientStock:
		return "Kullanılabilir stok yetersiz."
	case sales.CommercialErrorOverFulfillment:
		return "Kaynak satır miktarı aşılamaz."
	case sales.CommercialErrorAlreadyPosted:
		return "Belge daha önce işlendi."
	case sales.CommercialErrorNotEditable:
		return "Yalnız taslak belge düzenlenebilir."
	case sales.CommercialErrorPeriodLocked:
		return "Belge tarihi kilitli dönemdedir."
	case sales.CommercialErrorInvalidRelation:
		return "Belge ilişkisi geçersiz."
	case sales.CommercialErrorInvalidPartyRole:
		return "Aktif müşteri carisi gereklidir."
	case sales.CommercialErrorWarehouseRequired:
		return "Ürün satırı için depo gereklidir."
	case sales.CommercialErrorWarehouseUnauthorized:
		return "Seçilen depoya erişim yetkiniz yok."
	case sales.CommercialErrorPriceRequired:
		return "Satış fiyatı bulunamadı."
	case sales.CommercialErrorTaxProfileInvalid:
		return "Vergi profili geçersiz."
	case sales.CommercialErrorPaymentUnavailable:
		return "Finans posting yeteneği kullanılamıyor."
	case sales.CommercialErrorExchangeRateUnavailable:
		return "Belge para birimi için güncel kur alınamadı."
	case sales.CommercialErrorVariantRequired:
		return "Varyantlı ürün için varyant seçilmelidir."
	case sales.CommercialErrorReturnReasonRequired:
		return "İade gerekçesi gereklidir."
	case sales.CommercialErrorProductInactive:
		return "Seçilen ürün artık aktif değil."
	case sales.CommercialErrorVariantInactive:
		return "Seçilen varyant artık aktif değil."
	case sales.CommercialErrorPartyInactive:
		return "Seçilen cari artık aktif değil."
	case sales.CommercialErrorWarehouseInactive:
		return "Seçilen depo artık aktif değil."
	case sales.CommercialErrorDocumentModified:
		return "Bu belge başka bir kullanıcı tarafından değiştirildi. Güncel hali yükleyin."
	case sales.CommercialErrorInvalidStateTransition:
		return "Belge mevcut durumunda bu işleme uygun değil."
	case sales.CommercialErrorCalculationChanged:
		return "Belge toplamları yeniden hesaplandı."
	case sales.CommercialErrorSourceAlreadyConsumed:
		return "Kaynak belgenin kullanılabilir miktarı kalmadı."
	case sales.CommercialErrorSourceCancelled:
		return "İptal edilmiş kaynak belge kullanılamaz."
	case sales.CommercialErrorSourcePartyMismatch:
		return "Kaynak belge farklı bir cariye ait."
	case sales.CommercialErrorSourceCurrencyMismatch:
		return "Kaynak belge farklı bir para biriminde."
	case sales.CommercialErrorDocumentHasDependencies:
		return "Belgenin bağlı işlemleri varken bu işlem yapılamaz."
	case sales.CommercialErrorDocumentHasNoLines:
		return "Belgeyi kesinleştirmeden önce en az bir satır ekleyin."
	case sales.CommercialErrorDuplicateDocumentNo:
		return "Bu belge numarası zaten kullanılıyor. Farklı bir numara girin veya boş bırakıp otomatik numara alın."
	default:
		return "Belge bilgileri geçersiz."
	}
}

func writeCommercialErrorDetails(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	writeJSON(w, status, contract.ErrorResponse{Code: code, Message: message, Details: details, TraceId: TraceID(r.Context())})
}
