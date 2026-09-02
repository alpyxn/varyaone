package httpapi

import (
	"net/http"
	"strings"

	"github.com/alpyxn/varyaone/internal/inventory"
	"github.com/go-chi/chi/v5"
)

func (h inventoryHandler) listStockMovementOperations(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.read") {
		return
	}
	filter, err := movementListFilterFromRequest(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Stok hareketi tarih filtresi geçersiz.")
		return
	}
	session := sessionFromRequest(r)
	filter.CompanyID = session.CurrentCompanyID
	filter.UserID = session.User.ID
	items, err := h.service.ListStockMovementOperations(r.Context(), filter)
	if err != nil {
		writeInventoryError(w, r, err, "Stok hareket operasyonları okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h inventoryHandler) getStockMovementOperation(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.read") {
		return
	}
	session := sessionFromRequest(r)
	item, err := h.service.GetStockMovementOperation(
		r.Context(),
		session.CurrentCompanyID,
		chi.URLParam(r, "operationID"),
		session.User.ID,
	)
	if err != nil {
		writeInventoryError(w, r, err, "Stok hareket operasyonu okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h inventoryHandler) postStockMovementOperation(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.movement.post") {
		return
	}
	var input inventory.StockMovementOperationInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Varyantlı stok hareketi bilgileri geçersiz.")
		return
	}
	session := sessionFromRequest(r)
	input.CompanyID = session.CurrentCompanyID
	input.ActorUserID = session.User.ID
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		input.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	item, err := h.service.PostStockMovementOperation(r.Context(), input)
	if err != nil {
		writeInventoryError(w, r, err, "Varyantlı stok hareketi kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
