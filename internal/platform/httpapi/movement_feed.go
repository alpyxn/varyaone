package httpapi

import "net/http"

func (h inventoryHandler) listStockMovementFeed(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.read") {
		return
	}
	filter, err := movementListFilterFromRequest(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Stok hareketi filtreleri geçersiz.")
		return
	}
	session := sessionFromRequest(r)
	filter.CompanyID = session.CurrentCompanyID
	filter.UserID = session.User.ID
	result, err := h.service.ListMovementFeed(r.Context(), filter, r.URL.Query().Get("cursor"))
	if err != nil {
		writeInventoryError(w, r, err, "Stok hareketleri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}
