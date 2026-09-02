package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/purchasing"
	"github.com/go-chi/chi/v5"
)

type commercialPurchasingHandler struct {
	service *purchasing.Service
	kind    purchasing.PurchaseKind
}

func mountTypedPurchasingRoutes(router chi.Router, identityService *identity.Service, service *purchasing.Service) {
	auth := identityHandler{service: identityService}
	router.Route("/api/v1/purchases", func(r chi.Router) {
		for _, resource := range []string{"orders", "dispatches", "invoices", "returns"} {
			kind, _ := purchasing.PurchaseKindForResource(resource)
			h := commercialPurchasingHandler{service: service, kind: kind}
			r.Route("/"+resource, func(r chi.Router) {
				r.Use(auth.requireSession)
				r.Get("/", h.list)
				r.Get("/{documentID}", h.get)
				r.Group(func(r chi.Router) {
					r.Use(auth.requireCSRF)
					r.Post("/", h.create)
					r.Post("/create-from-source", h.create)
					r.Put("/{documentID}", h.update)
					r.Delete("/{documentID}", h.delete)
					r.Post("/{documentID}/convert", h.convert)
					r.Post("/{documentID}/{command}", h.command)
				})
			})
		}
	})
}

func (h commercialPurchasingHandler) list(w http.ResponseWriter, r *http.Request) {
	options := purchasing.PurchaseListOptions{
		Status:            r.URL.Query().Get("status"),
		LifecycleStatus:   r.URL.Query().Get("lifecycle_status"),
		FulfillmentStatus: r.URL.Query().Get("fulfillment_status"),
		InvoicingStatus:   r.URL.Query().Get("invoicing_status"),
		PaymentStatus:     r.URL.Query().Get("payment_status"),
		SupplierID:        r.URL.Query().Get("supplier_id"),
		BranchID:          r.URL.Query().Get("branch_id"),
		CurrencyCode:      r.URL.Query().Get("currency_code"),
		ForReference:      r.URL.Query().Get("for_reference") == "true",
		ReferenceTarget:   r.URL.Query().Get("reference_target"),
		Cursor:            r.URL.Query().Get("cursor"),
		Search:            r.URL.Query().Get("q"),
		Sort:              r.URL.Query().Get("sort"),
		Limit:             queryLimit(r, 50, 100),
	}
	if value := strings.TrimSpace(r.URL.Query().Get("from")); value != "" {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Başlangıç tarihi geçersiz.")
			return
		}
		options.From = &parsed
	}
	if value := strings.TrimSpace(r.URL.Query().Get("to")); value != "" {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Bitiş tarihi geçersiz.")
			return
		}
		options.To = &parsed
	}
	result, err := h.service.ListPurchaseDocuments(r.Context(), sessionFromRequest(r), h.kind, options)
	if err != nil {
		writePurchasingError(w, r, err, "Satın alma belgeleri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h commercialPurchasingHandler) get(w http.ResponseWriter, r *http.Request) {
	var (
		item any
		err  error
	)
	session := sessionFromRequest(r)
	switch h.kind {
	case purchasing.PurchaseOrderKind:
		item, err = h.service.GetPurchaseOrder(r.Context(), session, chi.URLParam(r, "documentID"))
	case purchasing.GoodsReceiptKind:
		item, err = h.service.GetGoodsReceipt(r.Context(), session, chi.URLParam(r, "documentID"))
	case purchasing.PurchaseInvoiceKind:
		item, err = h.service.GetPurchaseInvoice(r.Context(), session, chi.URLParam(r, "documentID"))
	case purchasing.PurchaseReturnKind:
		item, err = h.service.GetPurchaseReturn(r.Context(), session, chi.URLParam(r, "documentID"))
	default:
		err = purchasing.ErrNotFound
	}
	if err != nil {
		writePurchasingError(w, r, err, "Satın alma belgesi okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h commercialPurchasingHandler) create(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyHeader(w, r) {
		return
	}
	session := sessionFromRequest(r)
	var item any
	var err error
	var decodeErr error
	switch h.kind {
	case purchasing.PurchaseOrderKind:
		var input purchasing.PurchaseOrderInput
		if decodeErr = decodeJSON(r, &input); decodeErr == nil {
			item, err = h.service.CreatePurchaseOrder(r.Context(), session, input, requestMeta(r))
		}
	case purchasing.GoodsReceiptKind:
		var input purchasing.GoodsReceiptInput
		if decodeErr = decodeJSON(r, &input); decodeErr == nil {
			item, err = h.service.CreateGoodsReceipt(r.Context(), session, input, requestMeta(r))
		}
	case purchasing.PurchaseInvoiceKind:
		var input purchasing.PurchaseInvoiceInput
		if decodeErr = decodeJSON(r, &input); decodeErr == nil {
			item, err = h.service.CreatePurchaseInvoice(r.Context(), session, input, requestMeta(r))
		}
	case purchasing.PurchaseReturnKind:
		var input purchasing.PurchaseReturnInput
		if decodeErr = decodeJSON(r, &input); decodeErr == nil {
			item, err = h.service.CreatePurchaseReturn(r.Context(), session, input, requestMeta(r))
		}
	default:
		err = purchasing.ErrNotFound
	}
	if decodeErr != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Satın alma belgesi bilgileri geçersiz.")
		return
	}
	if err != nil {
		writePurchasingError(w, r, err, "Satın alma belgesi kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h commercialPurchasingHandler) update(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyHeader(w, r) {
		return
	}
	version, err := parseRequiredIfMatch(r)
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Taslak güncellemesi için güncel If-Match sürümü gereklidir.")
		return
	}
	var item any
	session := sessionFromRequest(r)
	var decodeErr error
	switch h.kind {
	case purchasing.PurchaseOrderKind:
		var input purchasing.PurchaseOrderInput
		if decodeErr = decodeJSON(r, &input); decodeErr == nil {
			item, err = h.service.UpdatePurchaseOrder(r.Context(), session, chi.URLParam(r, "documentID"), version, input, requestMeta(r))
		}
	case purchasing.GoodsReceiptKind:
		var input purchasing.GoodsReceiptInput
		if decodeErr = decodeJSON(r, &input); decodeErr == nil {
			item, err = h.service.UpdateGoodsReceipt(r.Context(), session, chi.URLParam(r, "documentID"), version, input, requestMeta(r))
		}
	case purchasing.PurchaseInvoiceKind:
		var input purchasing.PurchaseInvoiceInput
		if decodeErr = decodeJSON(r, &input); decodeErr == nil {
			item, err = h.service.UpdatePurchaseInvoice(r.Context(), session, chi.URLParam(r, "documentID"), version, input, requestMeta(r))
		}
	case purchasing.PurchaseReturnKind:
		var input purchasing.PurchaseReturnInput
		if decodeErr = decodeJSON(r, &input); decodeErr == nil {
			item, err = h.service.UpdatePurchaseReturn(r.Context(), session, chi.URLParam(r, "documentID"), version, input, requestMeta(r))
		}
	default:
		err = purchasing.ErrNotFound
	}
	if decodeErr != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Alış belgesi bilgileri geçersiz.")
		return
	}
	if err != nil {
		writePurchasingError(w, r, err, "Taslak alış belgesi güncellenemedi.")
		return
	}
	if itemVersion := purchaseItemVersion(item); itemVersion > 0 {
		w.Header().Set("ETag", formatETag(itemVersion))
	}
	writeJSON(w, http.StatusOK, item)
}

func (h commercialPurchasingHandler) delete(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyHeader(w, r) {
		return
	}
	version, err := parseRequiredIfMatch(r)
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Taslak silme işlemi için güncel If-Match sürümü gereklidir.")
		return
	}
	if h.kind == purchasing.PurchaseOrderKind {
		err = h.service.DeletePurchaseOrder(r.Context(), sessionFromRequest(r), chi.URLParam(r, "documentID"), version, requestMeta(r))
	} else {
		err = h.service.DeletePurchaseDraft(r.Context(), sessionFromRequest(r), h.kind, chi.URLParam(r, "documentID"), version, requestMeta(r))
	}
	if err != nil {
		writePurchasingError(w, r, err, "Alış siparişi silinemedi.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h commercialPurchasingHandler) convert(w http.ResponseWriter, r *http.Request) {
	if h.kind == purchasing.PurchaseOrderKind {
		writeError(w, r, http.StatusConflict, "INVALID_DOCUMENT_RELATION", "Alış siparişi başka bir belgeye dönüştürülemez.")
		return
	}
	if !requireIdempotencyHeader(w, r) {
		return
	}
	version, err := parseRequiredIfMatch(r)
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Dönüştürme için güncel If-Match sürümü gereklidir.")
		return
	}
	item, err := h.service.ConvertPurchaseDocument(r.Context(), sessionFromRequest(r), h.kind, chi.URLParam(r, "documentID"), version, requestMeta(r))
	if err != nil {
		writePurchasingError(w, r, err, "Alış belgesi dönüştürülemedi.")
		return
	}
	if itemVersion := purchaseItemVersion(item); itemVersion > 0 {
		w.Header().Set("ETag", formatETag(itemVersion))
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h commercialPurchasingHandler) command(w http.ResponseWriter, r *http.Request) {
	command := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "command")))
	if !requireIdempotencyHeader(w, r) {
		return
	}
	version, err := parseRequiredIfMatch(r)
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "İşlem için güncel If-Match sürümü gereklidir.")
		return
	}
	session := sessionFromRequest(r)
	var item any
	if command == "cancel" {
		var input struct {
			Reason string `json:"reason"`
		}
		if err = decodeJSON(r, &input); err != nil || strings.TrimSpace(input.Reason) == "" {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İptal gerekçesi gereklidir.")
			return
		}
		switch h.kind {
		case purchasing.PurchaseOrderKind:
			item, err = h.service.CancelPurchaseOrder(r.Context(), session, chi.URLParam(r, "documentID"), version, input.Reason, requestMeta(r))
		case purchasing.GoodsReceiptKind:
			item, err = h.service.CancelGoodsReceipt(r.Context(), session, chi.URLParam(r, "documentID"), version, input.Reason, requestMeta(r))
		case purchasing.PurchaseInvoiceKind:
			item, err = h.service.CancelPurchaseInvoice(r.Context(), session, chi.URLParam(r, "documentID"), version, input.Reason, requestMeta(r))
		case purchasing.PurchaseReturnKind:
			item, err = h.service.CancelPurchaseReturn(r.Context(), session, chi.URLParam(r, "documentID"), version, input.Reason, requestMeta(r))
		default:
			err = purchasing.ErrInvalidTransition
		}
	} else if command == "confirm" && h.kind == purchasing.PurchaseOrderKind {
		item, err = h.service.ConfirmPurchaseOrder(r.Context(), session, chi.URLParam(r, "documentID"), version, requestMeta(r))
	} else if command == "finalize" || command == "post" {
		switch h.kind {
		case purchasing.GoodsReceiptKind:
			item, err = h.service.FinalizeGoodsReceipt(r.Context(), session, chi.URLParam(r, "documentID"), version, requestMeta(r))
		case purchasing.PurchaseInvoiceKind:
			item, err = h.service.FinalizePurchaseInvoice(r.Context(), session, chi.URLParam(r, "documentID"), version, requestMeta(r))
		case purchasing.PurchaseReturnKind:
			item, err = h.service.FinalizePurchaseReturn(r.Context(), session, chi.URLParam(r, "documentID"), version, requestMeta(r))
		default:
			err = purchasing.ErrInvalidTransition
		}
	} else {
		writeError(w, r, http.StatusConflict, "DOCUMENT_NOT_EDITABLE", "Bu satın alma belgesi için bu işlem kullanılamaz.")
		return
	}
	if err != nil {
		writePurchasingError(w, r, err, "Alış belgesi işlemi tamamlanamadı.")
		return
	}
	if itemVersion := purchaseItemVersion(item); itemVersion > 0 {
		w.Header().Set("ETag", formatETag(itemVersion))
	}
	writeJSON(w, http.StatusOK, item)
}

func purchaseItemVersion(item any) int64 {
	switch value := item.(type) {
	case purchasing.PurchaseOrder:
		return value.Version
	case purchasing.GoodsReceipt:
		return value.Version
	case purchasing.PurchaseInvoice:
		return value.Version
	case purchasing.PurchaseReturn:
		return value.Version
	default:
		return 0
	}
}

func formatETag(version int64) string {
	return strings.TrimSpace(strconv.FormatInt(version, 10))
}
