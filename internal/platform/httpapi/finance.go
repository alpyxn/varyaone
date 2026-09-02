package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/idempotency"
	"github.com/go-chi/chi/v5"
)

type financeHandler struct{ service *finance.Service }

func mountFinanceRoutes(router chi.Router, identityService *identity.Service, service *finance.Service) {
	auth := identityHandler{service: identityService}
	h := financeHandler{service: service}
	router.Route("/api/v1/finance", func(r chi.Router) {
		r.Use(auth.requireSession)
		mountTypedFinanceAccountRoutes(r, auth, h, "cash")
		mountTypedFinanceAccountRoutes(r, auth, h, "bank")
		r.Get("/accounts", h.listAccounts)
		r.Get("/accounts/{accountID}", h.getAccount)
		r.Get("/movements", h.listUnifiedMovements)
		r.Get("/movements/{movementID}", h.getUnifiedMovement)
		r.Get("/collections", h.listCollections)
		r.Get("/collections/{paymentID}", h.getPayment)
		r.Get("/payments", h.listPayments)
		r.Get("/payments/{paymentID}", h.getPayment)
		r.Get("/transfers", h.listFinanceTransfers)
		r.Get("/transfers/{transferID}", h.getFinanceTransfer)
		r.Group(func(r chi.Router) {
			r.Use(auth.requireCSRF)
			r.Post("/allocation-preview", h.previewAllocation)
			r.Post("/accounts", h.createAccount)
			r.Post("/collections", h.postCollection)
			r.Post("/payments", h.postPayment)
			r.Post("/payments/{paymentID}/allocations", h.allocate)
			r.Post("/payments/{paymentID}/allocations/auto", h.allocateAuto)
			r.Post("/payments/{paymentID}/allocations/reverse", h.unallocate)
			r.Post("/collections/{paymentID}/allocations", h.allocate)
			r.Post("/collections/{paymentID}/allocations/auto", h.allocateAuto)
			r.Post("/collections/{paymentID}/allocations/reverse", h.unallocate)
			r.Post("/payments/{paymentID}/reverse", h.reversePayment)
			r.Post("/manual-entries", h.postManualEntry)
			r.Post("/collections/{paymentID}/reverse", h.reversePayment)
			r.Post("/transfers", h.postFinanceTransfer)
			r.Post("/transfers/{transferID}/reverse", h.reverseFinanceTransfer)
			r.Post("/party-transfers", h.postPartyTransfer)
		})
	})
	router.Route("/api/v1/invoice-open-items", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/", h.listOpenItems)
	})
	router.With(auth.requireSession).Get("/api/v1/invoice-open-items", h.listOpenItems)
}

func mountTypedFinanceAccountRoutes(r chi.Router, auth identityHandler, h financeHandler, kind string) {
	accountPath := "/" + kind + "-accounts"
	movementPath := "/" + kind + "-movements"
	r.Get(accountPath, h.listTypedAccounts)
	r.Get(accountPath+"/{accountID}", h.getTypedAccount)
	r.Get(accountPath+"/{accountID}/balance", h.getAccountBalance)
	r.Get(accountPath+"/{accountID}/statement", h.getAccountStatement)
	r.Get(movementPath, h.listAccountMovements)
	r.Get(movementPath+"/{movementID}", h.getAccountMovement)
	r.Group(func(r chi.Router) {
		r.Use(auth.requireCSRF)
		r.Post(accountPath, h.createTypedAccount)
		r.Put(accountPath+"/{accountID}", h.updateTypedAccount)
		r.Post(accountPath+"/{accountID}/activate", h.activateAccount)
		r.Post(accountPath+"/{accountID}/deactivate", h.deactivateAccount)
		r.Post(accountPath+"/{accountID}/opening-balance", h.postOpeningBalance)
		r.Post(movementPath+"/manual", h.postManualAccountMovement)
	})
}

func financeAccountType(r *http.Request) string {
	if strings.Contains(r.URL.Path, "/bank-") {
		return "BANK"
	}
	return "CASH"
}

func (h financeHandler) listTypedAccounts(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListAccounts(r.Context(), sessionFromRequest(r), financeAccountType(r), r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		writeFinanceError(w, r, err, "Finans hesapları okunamadı.")
		return
	}
	for index := range items {
		items[index].IBAN = finance.MaskIBAN(items[index].IBAN)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h financeHandler) getTypedAccount(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetAccount(r.Context(), sessionFromRequest(r), chi.URLParam(r, "accountID"))
	if err == nil && item.AccountType != financeAccountType(r) {
		err = identity.ErrForbidden
	}
	if err != nil {
		writeFinanceError(w, r, err, "Finans hesabı okunamadı.")
		return
	}
	item.IBAN = finance.MaskIBAN(item.IBAN)
	w.Header().Set("ETag", formatETag(item.Version))
	writeJSON(w, http.StatusOK, item)
}

func (h financeHandler) createTypedAccount(w http.ResponseWriter, r *http.Request) {
	var input finance.AccountInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Finans hesabı bilgileri geçersiz.")
		return
	}
	input.AccountType = financeAccountType(r)
	item, err := h.service.CreateAccount(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeFinanceError(w, r, err, "Finans hesabı oluşturulamadı.")
		return
	}
	item.IBAN = finance.MaskIBAN(item.IBAN)
	w.Header().Set("ETag", formatETag(item.Version))
	writeJSON(w, http.StatusCreated, item)
}

func (h financeHandler) updateTypedAccount(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Hesap güncellemesi için güncel If-Match sürümü gereklidir.")
		return
	}
	var input finance.AccountInput
	if err = decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Finans hesabı bilgileri geçersiz.")
		return
	}
	input.AccountType = financeAccountType(r)
	item, err := h.service.UpdateAccount(r.Context(), sessionFromRequest(r), chi.URLParam(r, "accountID"), version, input, requestMeta(r))
	if err != nil {
		writeFinanceError(w, r, err, "Finans hesabı güncellenemedi.")
		return
	}
	item.IBAN = finance.MaskIBAN(item.IBAN)
	w.Header().Set("ETag", formatETag(item.Version))
	writeJSON(w, http.StatusOK, item)
}

func (h financeHandler) activateAccount(w http.ResponseWriter, r *http.Request) {
	h.setAccountActive(w, r, true)
}
func (h financeHandler) deactivateAccount(w http.ResponseWriter, r *http.Request) {
	h.setAccountActive(w, r, false)
}
func (h financeHandler) setAccountActive(w http.ResponseWriter, r *http.Request, active bool) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Hesap işlemi için güncel If-Match sürümü gereklidir.")
		return
	}
	item, err := h.service.SetAccountActive(r.Context(), sessionFromRequest(r), chi.URLParam(r, "accountID"), version, active, requestMeta(r))
	if err != nil {
		writeFinanceError(w, r, err, "Finans hesabı durumu değiştirilemedi.")
		return
	}
	item.IBAN = finance.MaskIBAN(item.IBAN)
	w.Header().Set("ETag", formatETag(item.Version))
	writeJSON(w, http.StatusOK, item)
}

func (h financeHandler) getAccountBalance(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetAccountBalance(r.Context(), sessionFromRequest(r), chi.URLParam(r, "accountID"))
	if err != nil {
		writeFinanceError(w, r, err, "Hesap bakiyesi okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func optionalFinanceDate(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func validateFinanceDateRange(from, to *time.Time) error {
	if from != nil && to != nil && to.Before(*from) {
		return errors.New("tarih aralığı ters")
	}
	return nil
}

func (h financeHandler) getAccountStatement(w http.ResponseWriter, r *http.Request) {
	from, err := optionalFinanceDate(r.URL.Query().Get("from"))
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Ekstre başlangıç tarihi geçersiz.")
		return
	}
	to, err := optionalFinanceDate(r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Ekstre bitiş tarihi geçersiz.")
		return
	}
	if err = validateFinanceDateRange(from, to); err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Ekstre tarih aralığı geçersiz.")
		return
	}
	item, err := h.service.AccountStatement(r.Context(), sessionFromRequest(r), chi.URLParam(r, "accountID"), from, to, queryLimit(r, 100, 500))
	if err != nil {
		writeFinanceError(w, r, err, "Hesap ekstresi okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h financeHandler) listAccountMovements(w http.ResponseWriter, r *http.Request) {
	from, err := optionalFinanceDate(r.URL.Query().Get("from"))
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Hareket başlangıç tarihi geçersiz.")
		return
	}
	to, err := optionalFinanceDate(r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Hareket bitiş tarihi geçersiz.")
		return
	}
	if err = validateFinanceDateRange(from, to); err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Hareket tarih aralığı geçersiz.")
		return
	}
	items, err := h.service.ListAccountMovements(r.Context(), sessionFromRequest(r), financeAccountType(r), r.URL.Query().Get("account_id"), from, to, queryLimit(r, 100, 500))
	if err != nil {
		writeFinanceError(w, r, err, "Hesap hareketleri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h financeHandler) listUnifiedMovements(w http.ResponseWriter, r *http.Request) {
	from, err := optionalFinanceDate(r.URL.Query().Get("from"))
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Hareket başlangıç tarihi geçersiz.")
		return
	}
	to, err := optionalFinanceDate(r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Hareket bitiş tarihi geçersiz.")
		return
	}
	if err = validateFinanceDateRange(from, to); err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Hareket tarih aralığı geçersiz.")
		return
	}
	page, err := h.service.ListAllAccountMovements(r.Context(), sessionFromRequest(r), r.URL.Query().Get("account_id"), r.URL.Query().Get("direction"), r.URL.Query().Get("cursor"), from, to, queryLimit(r, 100, 500))
	if err != nil {
		writeFinanceError(w, r, err, "Hesap hareketleri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h financeHandler) getUnifiedMovement(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetAccountMovement(r.Context(), sessionFromRequest(r), chi.URLParam(r, "movementID"))
	if err != nil {
		writeFinanceError(w, r, err, "Hesap hareketi okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h financeHandler) getAccountMovement(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetAccountMovement(r.Context(), sessionFromRequest(r), chi.URLParam(r, "movementID"))
	if err == nil && item.AccountType != financeAccountType(r) {
		err = identity.ErrForbidden
	}
	if err != nil {
		writeFinanceError(w, r, err, "Hesap hareketi okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func decodeAccountMovement(w http.ResponseWriter, r *http.Request, accountID string) (finance.AccountMovementInput, bool) {
	var input finance.AccountMovementInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Hesap hareketi bilgileri geçersiz.")
		return input, false
	}
	if accountID != "" {
		input.AccountID = accountID
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	return input, true
}

func (h financeHandler) postOpeningBalance(w http.ResponseWriter, r *http.Request) {
	input, ok := decodeAccountMovement(w, r, chi.URLParam(r, "accountID"))
	if !ok {
		return
	}
	item, err := h.service.PostOpeningBalance(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeFinanceError(w, r, err, "Açılış bakiyesi kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h financeHandler) postManualAccountMovement(w http.ResponseWriter, r *http.Request) {
	input, ok := decodeAccountMovement(w, r, "")
	if !ok {
		return
	}
	item, err := h.service.PostManualAccountMovement(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeFinanceError(w, r, err, "Manuel hesap hareketi kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h financeHandler) listFinanceTransfers(w http.ResponseWriter, r *http.Request) {
	from, err := optionalFinanceDate(r.URL.Query().Get("from"))
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Transfer başlangıç tarihi geçersiz.")
		return
	}
	to, err := optionalFinanceDate(r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Transfer bitiş tarihi geçersiz.")
		return
	}
	if err = validateFinanceDateRange(from, to); err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Transfer tarih aralığı geçersiz.")
		return
	}
	items, err := h.service.ListFinanceTransfers(r.Context(), sessionFromRequest(r), r.URL.Query().Get("account_id"), from, to, queryLimit(r, 100, 500))
	if err != nil {
		writeFinanceError(w, r, err, "Transferler okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h financeHandler) getFinanceTransfer(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetFinanceTransfer(r.Context(), sessionFromRequest(r), chi.URLParam(r, "transferID"))
	if err != nil {
		writeFinanceError(w, r, err, "Transfer okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h financeHandler) postFinanceTransfer(w http.ResponseWriter, r *http.Request) {
	var input finance.FinanceTransferInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Transfer bilgileri geçersiz.")
		return
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	item, err := h.service.PostFinanceTransfer(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeFinanceError(w, r, err, "Transfer kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h financeHandler) reverseFinanceTransfer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason          string `json:"reason"`
		TransactionDate string `json:"transaction_date"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Ters kayıt bilgileri geçersiz.")
		return
	}
	var date time.Time
	var err error
	if strings.TrimSpace(body.TransactionDate) != "" {
		date, err = time.Parse("2006-01-02", body.TransactionDate)
		if err != nil {
			writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Ters kayıt tarihi geçersiz.")
			return
		}
	}
	item, err := h.service.ReverseFinanceTransfer(r.Context(), sessionFromRequest(r), chi.URLParam(r, "transferID"), strings.TrimSpace(r.Header.Get("Idempotency-Key")), body.Reason, date, requestMeta(r))
	if err != nil {
		writeFinanceError(w, r, err, "Transfer ters kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h financeHandler) previewAllocation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		PartyID     string `json:"party_id"`
		Currency    string `json:"currency"`
		PaymentKind string `json:"payment_kind"`
		Amount      string `json:"amount"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Dağıtım önerisi bilgileri geçersiz.")
		return
	}
	if strings.TrimSpace(input.PaymentKind) == "" {
		input.PaymentKind = "COLLECTION"
	}
	if strings.TrimSpace(input.Currency) == "" {
		writeError(w, r, http.StatusUnprocessableEntity, "ALLOCATION_CURRENCY_REQUIRED", "Dağıtım önerisi için para birimi seçilmelidir.")
		return
	}
	side := "RECEIVABLE"
	if strings.EqualFold(input.PaymentKind, "PAYMENT") {
		side = "PAYABLE"
	}
	items, err := h.service.ListOpenItems(r.Context(), sessionFromRequest(r), input.PartyID, input.Currency, side, queryLimit(r, 200, 500))
	if err != nil {
		writeFinanceError(w, r, err, "Açık faturalar okunamadı.")
		return
	}
	if len(items) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "allocations": []any{}, "reason": "NO_OPEN_ITEMS"})
		return
	}
	allocations, err := finance.FIFOAllocations(items, input.Amount)
	if err != nil {
		writeFinanceError(w, r, err, "Dağıtım önerisi oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "allocations": allocations})
}

func (h financeHandler) listAccounts(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ListAccounts(r.Context(), sessionFromRequest(r), r.URL.Query().Get("type"), r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		writeFinanceError(w, r, err, "Finans hesapları okunamadı.")
		return
	}
	for index := range items {
		items[index].IBAN = finance.MaskIBAN(items[index].IBAN)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h financeHandler) getAccount(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetAccount(r.Context(), sessionFromRequest(r), chi.URLParam(r, "accountID"))
	if err != nil {
		writeFinanceError(w, r, err, "Finans hesabı okunamadı.")
		return
	}
	item.IBAN = finance.MaskIBAN(item.IBAN)
	writeJSON(w, http.StatusOK, item)
}

func (h financeHandler) createAccount(w http.ResponseWriter, r *http.Request) {
	var input finance.AccountInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Finans hesabı bilgileri geçersiz.")
		return
	}
	item, err := h.service.CreateAccount(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeFinanceError(w, r, err, "Finans hesabı oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h financeHandler) getPayment(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetPaymentDetail(r.Context(), sessionFromRequest(r), chi.URLParam(r, "paymentID"))
	if err != nil {
		writeFinanceError(w, r, err, "Tahsilat/ödeme okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h financeHandler) listPayments(w http.ResponseWriter, r *http.Request) {
	from, to, err := paymentDateRange(r)
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Ödeme tarih aralığı geçersiz.")
		return
	}
	result, err := h.service.ListPaymentsPaged(r.Context(), sessionFromRequest(r), finance.PaymentListOptions{PartyID: r.URL.Query().Get("party_id"), Method: r.URL.Query().Get("method"), Status: r.URL.Query().Get("status"), AccountID: r.URL.Query().Get("account_id"), AmountMin: r.URL.Query().Get("amount_min"), AmountMax: r.URL.Query().Get("amount_max"), From: from, To: to, Cursor: r.URL.Query().Get("cursor"), Limit: queryLimit(r, 50, 200)})
	if err != nil {
		writeFinanceError(w, r, err, "Tahsilat ve ödemeler okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h financeHandler) listCollections(w http.ResponseWriter, r *http.Request) {
	from, to, err := paymentDateRange(r)
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Tahsilat tarih aralığı geçersiz.")
		return
	}
	result, err := h.service.ListPaymentsPaged(r.Context(), sessionFromRequest(r), finance.PaymentListOptions{Kind: "COLLECTION", PartyID: r.URL.Query().Get("party_id"), Method: r.URL.Query().Get("method"), Status: r.URL.Query().Get("status"), AccountID: r.URL.Query().Get("account_id"), AmountMin: r.URL.Query().Get("amount_min"), AmountMax: r.URL.Query().Get("amount_max"), From: from, To: to, Cursor: r.URL.Query().Get("cursor"), Limit: queryLimit(r, 50, 200)})
	if err != nil {
		writeFinanceError(w, r, err, "Tahsilatlar okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func paymentDateRange(r *http.Request) (*time.Time, *time.Time, error) {
	parse := func(value string) (*time.Time, error) {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, nil
		}
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			return nil, err
		}
		return &parsed, nil
	}
	from, err := parse(r.URL.Query().Get("from"))
	if err != nil {
		return nil, nil, err
	}
	to, err := parse(r.URL.Query().Get("to"))
	if err != nil {
		return nil, nil, err
	}
	if from != nil && to != nil && to.Before(*from) {
		return nil, nil, errors.New("tarih aralığı ters")
	}
	return from, to, nil
}

func (h financeHandler) listOpenItems(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.ListOpenItemsPage(r.Context(), sessionFromRequest(r), r.URL.Query().Get("party_id"), r.URL.Query().Get("currency"), r.URL.Query().Get("side"), r.URL.Query().Get("cursor"), queryLimit(r, 100, 500))
	if err != nil {
		writeFinanceError(w, r, err, "Açık cari kalemler okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h financeHandler) postCollection(w http.ResponseWriter, r *http.Request) {
	var input finance.PaymentInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Tahsilat bilgileri geçersiz.")
		return
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	if method := strings.ToUpper(strings.TrimSpace(input.PaymentMethod)); method != "CASH" && method != "BANK" {
		writeError(w, r, http.StatusUnprocessableEntity, "PAYMENT_METHOD_NOT_SUPPORTED", "Tahsilat için yalnız kasa veya banka hesabı kullanılabilir.")
		return
	}
	item, err := h.service.PostCollection(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeFinanceError(w, r, err, "Tahsilat kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h financeHandler) postPayment(w http.ResponseWriter, r *http.Request) {
	var input finance.PaymentInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Ödeme bilgileri geçersiz.")
		return
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	if method := strings.ToUpper(strings.TrimSpace(input.PaymentMethod)); method != "CASH" && method != "BANK" {
		writeError(w, r, http.StatusUnprocessableEntity, "PAYMENT_METHOD_NOT_SUPPORTED", "Ödeme için yalnız kasa veya banka hesabı kullanılabilir.")
		return
	}
	item, err := h.service.PostPaymentCommand(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeFinanceError(w, r, err, "Ödeme kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h financeHandler) allocate(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeError(w, r, http.StatusPreconditionRequired, "IDEMPOTENCY_KEY_REQUIRED", "Tahsis işlemi için Idempotency-Key gereklidir.")
		return
	}
	var input struct {
		Allocations []finance.AllocationInput `json:"allocations"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Tahsis bilgileri geçersiz.")
		return
	}
	meta := requestMeta(r)
	meta.IdempotencyKey = key
	items, err := h.service.AllocatePayment(r.Context(), sessionFromRequest(r), chi.URLParam(r, "paymentID"), input.Allocations, meta)
	if err != nil {
		writeFinanceError(w, r, err, "Tahsis kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h financeHandler) allocateAuto(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeError(w, r, http.StatusPreconditionRequired, "IDEMPOTENCY_KEY_REQUIRED", "Otomatik dağıtım için Idempotency-Key gereklidir.")
		return
	}
	meta := requestMeta(r)
	meta.IdempotencyKey = key
	items, err := h.service.AllocatePaymentFIFO(r.Context(), sessionFromRequest(r), chi.URLParam(r, "paymentID"), meta)
	if err != nil {
		writeFinanceError(w, r, err, "Otomatik dağıtım yapılamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h financeHandler) unallocate(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeError(w, r, http.StatusPreconditionRequired, "IDEMPOTENCY_KEY_REQUIRED", "Tahsis geri alma işlemi için Idempotency-Key gereklidir.")
		return
	}
	var input struct {
		AllocationIDs []string `json:"allocation_ids"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Tahsis geri alma bilgileri geçersiz.")
		return
	}
	meta := requestMeta(r)
	meta.IdempotencyKey = key
	items, err := h.service.UnallocatePayment(r.Context(), sessionFromRequest(r), chi.URLParam(r, "paymentID"), input.AllocationIDs, meta)
	if err != nil {
		writeFinanceError(w, r, err, "Tahsis geri alınamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h financeHandler) reversePayment(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Reason          string `json:"reason"`
		TransactionDate string `json:"transaction_date"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Ters kayıt bilgileri geçersiz.")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	var transactionDate time.Time
	var err error
	if strings.TrimSpace(input.TransactionDate) != "" {
		transactionDate, err = time.Parse("2006-01-02", input.TransactionDate)
		if err != nil {
			writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Ters kayıt tarihi geçersiz.")
			return
		}
	}
	item, err := h.service.ReversePayment(r.Context(), sessionFromRequest(r), chi.URLParam(r, "paymentID"), key, input.Reason, transactionDate, requestMeta(r))
	if err != nil {
		writeFinanceError(w, r, err, "Ödeme ters kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h financeHandler) postManualEntry(w http.ResponseWriter, r *http.Request) {
	var input finance.ManualEntryInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Manuel cari hareket bilgileri geçersiz.")
		return
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	item, err := h.service.PostManualEntry(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeFinanceError(w, r, err, "Manuel cari hareket kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h financeHandler) postPartyTransfer(w http.ResponseWriter, r *http.Request) {
	var input finance.PartyTransferInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Cari virman bilgileri geçersiz.")
		return
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	item, err := h.service.PostPartyTransfer(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeFinanceError(w, r, err, "Cari virman kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func writeFinanceError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	if errors.Is(err, idempotency.ErrKeyRequired) {
		writeError(w, r, http.StatusPreconditionRequired, "IDEMPOTENCY_KEY_REQUIRED", "Bu işlem için Idempotency-Key gereklidir.")
		return
	}
	if errors.Is(err, idempotency.ErrPayloadConflict) {
		writeError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Aynı anahtar farklı finans verisiyle kullanıldı.")
		return
	}
	if errors.Is(err, idempotency.ErrCommandInProgress) {
		writeError(w, r, http.StatusConflict, "COMMAND_IN_PROGRESS", "Aynı finans işlemi hâlen işleniyor.")
		return
	}
	code := finance.ErrorCode(err)
	status := http.StatusUnprocessableEntity
	message := strings.TrimPrefix(err.Error(), code+": ")
	if code == "" {
		if errors.Is(err, identity.ErrForbidden) {
			status, code, message = http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok veya kayıt kapsamınız dışında."
		} else if errors.Is(err, identity.ErrConflict) {
			status, code, message = http.StatusPreconditionFailed, "VERSION_CONFLICT", "Kayıt başka bir kullanıcı tarafından değiştirilmiş."
		} else if errors.Is(err, identity.ErrValidation) {
			code = "VALIDATION_ERROR"
			message = strings.TrimPrefix(err.Error(), identity.ErrValidation.Error()+": ")
		} else {
			status, code, message = http.StatusInternalServerError, "INTERNAL_ERROR", fallback
			slog.Default().Error("finance request failed", "trace_id", TraceID(r.Context()), "error", err.Error())
		}
	} else {
		switch code {
		case "PAYMENT_ALREADY_POSTED", "PAYMENT_ALLOCATION_EXCEEDS_OPEN_AMOUNT", "CURRENCY_MISMATCH", "PERIOD_LOCKED", "ALREADY_REVERSED", "IDEMPOTENCY_CONFLICT", "INVOICE_ALREADY_POSTED", "DOCUMENT_HAS_DEPENDENCIES", "EXCHANGE_RATE_REQUIRED", "NEGATIVE_BALANCE_BLOCKED", "NEGATIVE_BALANCE_CONFIRMATION_REQUIRED", "ACCOUNT_BRANCH_IMMUTABLE", "ACCOUNT_INACTIVE", "OPENING_BALANCE_ALREADY_EXISTS", "PAYMENT_INVALID_STATE":
			status = http.StatusConflict
		}
		if message == "" {
			message = fallback
		}
	}
	writeError(w, r, status, code, message)
}
