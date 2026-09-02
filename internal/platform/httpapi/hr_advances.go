package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/hr/advance"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/go-chi/chi/v5"
)

type hrAdvanceHandler struct{ service *advance.Service }

func mountHRAdvanceRoutes(router chi.Router, identityService *identity.Service, service *advance.Service) {
	auth := identityHandler{service: identityService}
	h := hrAdvanceHandler{service: service}
	read := router.With(auth.requireSession)
	read.Get("/api/v1/hr/employee-advances", h.list)
	read.Get("/api/v1/hr/employee-advances/{advanceID}", h.get)
	read.Get("/api/v1/hr/employees/{employeeID}/advances", h.listEmployee)
	write := router.With(auth.requireSession, auth.requireCSRF)
	write.Post("/api/v1/hr/employee-advances", h.create)
	write.Post("/api/v1/hr/employee-advances/{advanceID}/repayments", h.repay)
	write.Post("/api/v1/hr/employee-advances/{advanceID}/write-off", h.writeOff)
	write.Post("/api/v1/hr/employee-advance-transactions/{transactionID}/reverse", h.reverse)
}

func advanceFilters(r *http.Request) (advance.ListFilter, error) {
	q := r.URL.Query()
	f := advance.ListFilter{EmployeeID: q.Get("employee_id"), Status: q.Get("status"), Query: q.Get("q"), Balance: q.Get("balance")}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	var err error
	if q.Get("from") != "" {
		var t time.Time
		t, err = time.Parse("2006-01-02", q.Get("from"))
		f.From = &t
		if err != nil {
			return f, err
		}
	}
	if q.Get("to") != "" {
		var t time.Time
		t, err = time.Parse("2006-01-02", q.Get("to"))
		f.To = &t
	}
	return f, err
}
func (h hrAdvanceHandler) list(w http.ResponseWriter, r *http.Request) {
	f, err := advanceFilters(r)
	if err != nil {
		writeError(w, r, 400, "VALIDATION_ERROR", "Tarih filtresi geçersiz.")
		return
	}
	p, err := h.service.List(r.Context(), sessionFromRequest(r), f)
	if err != nil {
		writeHRAdvanceError(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}
func (h hrAdvanceHandler) listEmployee(w http.ResponseWriter, r *http.Request) {
	f, err := advanceFilters(r)
	f.EmployeeID = chi.URLParam(r, "employeeID")
	if err != nil {
		writeError(w, r, 400, "VALIDATION_ERROR", "Tarih filtresi geçersiz.")
		return
	}
	p, err := h.service.List(r.Context(), sessionFromRequest(r), f)
	if err != nil {
		writeHRAdvanceError(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}
func (h hrAdvanceHandler) get(w http.ResponseWriter, r *http.Request) {
	a, err := h.service.Get(r.Context(), sessionFromRequest(r), chi.URLParam(r, "advanceID"))
	if err != nil {
		writeHRAdvanceError(w, r, err)
		return
	}
	writeJSON(w, 200, a)
}
func (h hrAdvanceHandler) create(w http.ResponseWriter, r *http.Request) {
	var in advance.CreateInput
	if decodeJSON(r, &in) != nil {
		writeError(w, r, 400, "VALIDATION_ERROR", "Avans bilgileri geçersiz.")
		return
	}
	a, err := h.service.Create(r.Context(), sessionFromRequest(r), in)
	if err != nil {
		writeHRAdvanceError(w, r, err)
		return
	}
	writeJSON(w, 201, a)
}
func (h hrAdvanceHandler) repay(w http.ResponseWriter, r *http.Request) {
	var in advance.RepaymentInput
	if decodeJSON(r, &in) != nil {
		writeError(w, r, 400, "VALIDATION_ERROR", "Geri ödeme bilgileri geçersiz.")
		return
	}
	a, err := h.service.Repay(r.Context(), sessionFromRequest(r), chi.URLParam(r, "advanceID"), in)
	if err != nil {
		writeHRAdvanceError(w, r, err)
		return
	}
	writeJSON(w, 201, a)
}
func (h hrAdvanceHandler) writeOff(w http.ResponseWriter, r *http.Request) {
	var in advance.WriteOffInput
	if decodeJSON(r, &in) != nil {
		writeError(w, r, 400, "VALIDATION_ERROR", "Vazgeçme bilgileri geçersiz.")
		return
	}
	a, err := h.service.WriteOff(r.Context(), sessionFromRequest(r), chi.URLParam(r, "advanceID"), in)
	if err != nil {
		writeHRAdvanceError(w, r, err)
		return
	}
	writeJSON(w, 201, a)
}
func (h hrAdvanceHandler) reverse(w http.ResponseWriter, r *http.Request) {
	var in advance.ReverseInput
	if decodeJSON(r, &in) != nil {
		writeError(w, r, 400, "VALIDATION_ERROR", "Ters kayıt bilgileri geçersiz.")
		return
	}
	a, err := h.service.Reverse(r.Context(), sessionFromRequest(r), chi.URLParam(r, "transactionID"), in)
	if err != nil {
		writeHRAdvanceError(w, r, err)
		return
	}
	writeJSON(w, 201, a)
}

func writeHRAdvanceError(w http.ResponseWriter, r *http.Request, err error) {
	code := finance.ErrorCode(err)
	message := finance.ErrorMessage(err)
	if message == "" {
		message = err.Error()
	}
	switch {
	case errors.Is(err, identity.ErrForbidden):
		writeError(w, r, 403, "FORBIDDEN", "Bu işlem için yetkiniz veya hesap erişiminiz yok.")
	case errors.Is(err, identity.ErrValidation):
		writeError(w, r, 422, "VALIDATION_ERROR", message)
	case errors.Is(err, advance.ErrNotFound), errors.Is(err, advance.ErrTransactionNotFound):
		writeError(w, r, 404, err.Error(), "Avans veya hareket bulunamadı.")
	case errors.Is(err, advance.ErrClosed), errors.Is(err, advance.ErrExceedsBalance), errors.Is(err, advance.ErrHasDependencies), errors.Is(err, advance.ErrAlreadyReversed), errors.Is(err, advance.ErrIdempotencyConflict):
		writeError(w, r, 409, err.Error(), message)
	case code != "":
		writeError(w, r, 409, code, message)
	default:
		writeError(w, r, 500, "INTERNAL_ERROR", "Personel avansı işlemi tamamlanamadı.")
	}
}
