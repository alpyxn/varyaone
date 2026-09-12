package httpapi

import (
	"net/http"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/go-chi/chi/v5"
)

func (h financeHandler) listPaymentPlans(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	result, err := h.service.ListPaymentPlanSummaries(r.Context(), sessionFromRequest(r), finance.PaymentPlanListOptions{
		PartyID: q.Get("party_id"), Side: q.Get("side"), Search: q.Get("q"), Status: q.Get("status"), Currency: q.Get("currency"),
		DueFrom: q.Get("due_from"), DueTo: q.Get("due_to"), Preset: q.Get("preset"), Sort: q.Get("sort"), Cursor: q.Get("cursor"), Limit: queryLimit(r, 50, 100),
	})
	if err != nil {
		writeFinanceError(w, r, err, "Planlar okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h financeHandler) getPaymentPlan(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.GetPaymentPlan(r.Context(), sessionFromRequest(r), chi.URLParam(r, "planID"))
	if err != nil {
		writeFinanceError(w, r, err, "Plan okunamadı.")
		return
	}
	w.Header().Set("ETag", formatETag(item.Version))
	writeJSON(w, http.StatusOK, item)
}
func (h financeHandler) createPaymentPlan(w http.ResponseWriter, r *http.Request) {
	var input finance.PaymentPlanInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Plan bilgileri geçersiz.")
		return
	}
	input.IdempotencyKey = r.Header.Get("Idempotency-Key")
	if input.IdempotencyKey == "" {
		writeError(w, r, http.StatusPreconditionRequired, "IDEMPOTENCY_KEY_REQUIRED", "Bu işlem için Idempotency-Key gereklidir.")
		return
	}
	item, err := h.service.CreatePaymentPlan(r.Context(), sessionFromRequest(r), input, requestMeta(r))
	if err != nil {
		writeFinanceError(w, r, err, "Plan oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
func (h financeHandler) cancelPaymentPlan(w http.ResponseWriter, r *http.Request) {
	version, err := parseIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Güncel kayıt sürümü gereklidir.")
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İptal bilgileri geçersiz.")
		return
	}
	item, err := h.service.CancelPaymentPlan(r.Context(), sessionFromRequest(r), chi.URLParam(r, "planID"), version, input.Reason, requestMeta(r))
	if err != nil {
		writeFinanceError(w, r, err, "Plan iptal edilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}
