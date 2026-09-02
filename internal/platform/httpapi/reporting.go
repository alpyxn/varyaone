package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/reporting"
	"github.com/go-chi/chi/v5"
)

type reportingHandler struct{ service *reporting.Service }

func mountReportingRoutes(router chi.Router, identityService *identity.Service, service *reporting.Service) {
	auth := identityHandler{service: identityService}
	h := reportingHandler{service: service}
	router.Route("/api/v1/reports", func(r chi.Router) {
		r.Use(auth.requireSession)
		r.Get("/stock-valuation", h.stockValuation)
		r.Get("/top-selling-products", h.topSellingProducts)
		r.Get("/sales-profitability", h.salesProfitability)
		r.Get("/overdue-receivables", h.overdueReceivables)
		r.Get("/overdue-payables", h.overduePayables)
		r.Get("/party-balances", h.partyBalances)
		r.Get("/stock-status", h.stockStatus)
		r.Get("/stock-movements", h.stockMovements)
		r.Get("/sales", h.salesList)
		r.Get("/purchases", h.purchaseList)
		r.Get("/cash-movements", h.cashMovements)
		r.Get("/bank-movements", h.bankMovements)
		r.Get("/tax-summary", h.taxSummary)
	})
}

func reportFilters(r *http.Request) reporting.ReportFilters {
	q := r.URL.Query()
	get := func(key string) string { return strings.TrimSpace(q.Get(key)) }
	return reporting.ReportFilters{
		WarehouseID:  get("warehouse_id"),
		ProductID:    get("product_id"),
		PartyID:      get("party_id"),
		CategoryID:   get("category_id"),
		CurrencyCode: strings.ToUpper(get("currency")),
		SalesRepID:   get("sales_rep_id"),
		AccountID:    get("account_id"),
		MovementType: strings.ToUpper(get("movement_type")),
		OnlyNonZero:  get("only_non_zero") == "true",
	}
}

func (h reportingHandler) partyBalances(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.PartyBalances(r.Context(), sessionFromRequest(r), reportFilters(r))
	if err != nil {
		writeReportingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h reportingHandler) stockStatus(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.StockStatus(r.Context(), sessionFromRequest(r), reportFilters(r))
	if err != nil {
		writeReportingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h reportingHandler) stockMovements(w http.ResponseWriter, r *http.Request) {
	from, to, err := reportDateRange(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Tarih aralığı geçersiz.")
		return
	}
	items, err := h.service.StockMovements(r.Context(), sessionFromRequest(r), from, to, reportFilters(r))
	if err != nil {
		writeReportingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h reportingHandler) salesList(w http.ResponseWriter, r *http.Request) {
	from, to, err := reportDateRange(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Tarih aralığı geçersiz.")
		return
	}
	items, err := h.service.SalesList(r.Context(), sessionFromRequest(r), from, to, reportFilters(r))
	if err != nil {
		writeReportingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h reportingHandler) purchaseList(w http.ResponseWriter, r *http.Request) {
	from, to, err := reportDateRange(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Tarih aralığı geçersiz.")
		return
	}
	items, err := h.service.PurchaseList(r.Context(), sessionFromRequest(r), from, to, reportFilters(r))
	if err != nil {
		writeReportingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h reportingHandler) cashMovements(w http.ResponseWriter, r *http.Request) {
	from, to, err := reportDateRange(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Tarih aralığı geçersiz.")
		return
	}
	items, err := h.service.CashMovements(r.Context(), sessionFromRequest(r), from, to, reportFilters(r))
	if err != nil {
		writeReportingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h reportingHandler) bankMovements(w http.ResponseWriter, r *http.Request) {
	from, to, err := reportDateRange(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Tarih aralığı geçersiz.")
		return
	}
	items, err := h.service.BankMovements(r.Context(), sessionFromRequest(r), from, to, reportFilters(r))
	if err != nil {
		writeReportingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h reportingHandler) overduePayables(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.OverduePayables(r.Context(), sessionFromRequest(r), time.Now().UTC())
	if err != nil {
		writeReportingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h reportingHandler) taxSummary(w http.ResponseWriter, r *http.Request) {
	from, to, err := reportDateRange(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Tarih aralığı geçersiz.")
		return
	}
	direction := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("direction")))
	items, err := h.service.TaxSummary(r.Context(), sessionFromRequest(r), from, to, direction)
	if err != nil {
		writeReportingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h reportingHandler) stockValuation(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.StockValuation(r.Context(), sessionFromRequest(r))
	if err != nil {
		writeReportingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h reportingHandler) topSellingProducts(w http.ResponseWriter, r *http.Request) {
	from, to, err := reportDateRange(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Tarih aralığı geçersiz.")
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil {
			limit = parsed
		}
	}
	items, err := h.service.TopSellingProducts(r.Context(), sessionFromRequest(r), from, to, limit)
	if err != nil {
		writeReportingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h reportingHandler) salesProfitability(w http.ResponseWriter, r *http.Request) {
	from, to, err := reportDateRange(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Tarih aralığı geçersiz.")
		return
	}
	items, err := h.service.SalesProfitability(r.Context(), sessionFromRequest(r), from, to)
	if err != nil {
		writeReportingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h reportingHandler) overdueReceivables(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.OverdueReceivables(r.Context(), sessionFromRequest(r), time.Now().UTC())
	if err != nil {
		writeReportingError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func reportDateRange(r *http.Request) (time.Time, time.Time, error) {
	from := time.Now().UTC().AddDate(0, -1, 0)
	to := time.Now().UTC()
	if raw := r.URL.Query().Get("from"); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		from = parsed
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		to = parsed
	}
	return from, to, nil
}

func writeReportingError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, identity.ErrForbidden) {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu rapor için yetkiniz yok.")
		return
	}
	writeError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "Rapor okunamadı.")
}
