package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/inventory"
	"github.com/go-chi/chi/v5"
)

type inventoryHandler struct{ service *inventory.Service }

func mountInventoryRoutes(router chi.Router, identityService *identity.Service, service *inventory.Service) {
	auth := identityHandler{service: identityService}
	h := inventoryHandler{service: service}
	// The identity routes own the /api/v1 mount point. Register the legacy
	// inventory aliases directly so adding inventory does not attempt to mount
	// the same chi subrouter a second time.
	read := router.With(auth.requireSession)
	read.Get("/api/v1/stock-movements", h.listMovements)
	read.Get("/api/v1/stock-movements/{movementID}", h.getMovement)
	read.Get("/api/v1/stock-movement-operations", h.listStockMovementOperations)
	read.Get("/api/v1/stock-movement-operations/{operationID}", h.getStockMovementOperation)
	read.Get("/api/v1/stock/positions", h.position)
	read.Get("/api/v1/warehouses", h.listWarehouses)
	read.Get("/api/v1/warehouses/{warehouseID}/locations", h.listLocations)
	read.Get("/api/v1/warehouses/{warehouseID}", h.getWarehouse)
	read.Get("/api/v1/warehouse-transfers", h.listTransfers)
	read.Get("/api/v1/warehouse-transfers/{transferID}", h.getTransfer)
	read.Get("/api/v1/stock-counts", h.listCountEngines)
	read.Get("/api/v1/stock-counts/{countID}", h.getCountEngine)
	read.Get("/api/v1/stock-counts/{countID}/sync", h.syncCountEngine)
	read.Get("/api/v1/lots", h.listLots)
	read.Get("/api/v1/lots/fefo", h.suggestFEFO)
	read.Get("/api/v1/lots/{lotID}", h.getLot)
	read.Get("/api/v1/serial-numbers", h.listSerialNumbers)
	read.Get("/api/v1/serial-numbers/{serialID}", h.getSerialNumber)

	write := router.With(auth.requireSession, auth.requireCSRF)
	write.Post("/api/v1/stock-movements", h.postMovement)
	write.Post("/api/v1/stock-movements/{movementID}/reverse", h.reverseMovement)
	write.Post("/api/v1/stock-movement-operations", h.postStockMovementOperation)
	write.Post("/api/v1/stock-movement-operations/{operationID}/reverse", h.reverseStockMovementOperation)
	write.Post("/api/v1/warehouses", h.createWarehouse)
	write.Put("/api/v1/warehouses/{warehouseID}", h.updateWarehouse)
	write.Delete("/api/v1/warehouses/{warehouseID}", h.deleteWarehouse)
	write.Post("/api/v1/warehouses/{warehouseID}/locations", h.createLocation)
	write.Post("/api/v1/warehouse-transfers", h.createTransfer)
	write.Post("/api/v1/warehouse-transfers/{transferID}/request", h.requestTransfer)
	write.Post("/api/v1/warehouse-transfers/{transferID}/approve", h.approveTransfer)
	write.Post("/api/v1/warehouse-transfers/{transferID}/ship", h.shipTransfer)
	write.Post("/api/v1/warehouse-transfers/{transferID}/receive", h.receiveTransfer)
	write.Post("/api/v1/warehouse-transfers/{transferID}/resolve", h.resolveTransferDiscrepancy)
	write.Post("/api/v1/warehouse-transfers/{transferID}/cancel", h.cancelTransfer)
	write.Post("/api/v1/stock-counts", h.startCountEngine)
	write.Post("/api/v1/stock-counts/{countID}/passes", h.createCountPass)
	write.Post("/api/v1/stock-counts/{countID}/sessions", h.createCountSession)
	write.Post("/api/v1/stock-counts/{countID}/lines", h.addCountScope)
	write.Post("/api/v1/stock-counts/{countID}/scan-events/batch", h.batchCountScans)
	write.Post("/api/v1/stock-counts/{countID}/lines/{lineID}/corrections", func(w http.ResponseWriter, r *http.Request) { h.countLineEvent(w, r, false) })
	write.Post("/api/v1/stock-counts/{countID}/lines/{lineID}/confirm-zero", func(w http.ResponseWriter, r *http.Request) { h.countLineEvent(w, r, true) })
	write.Post("/api/v1/stock-counts/{countID}/passes/{passID}/submit", h.submitCountPass)
	write.Post("/api/v1/stock-counts/{countID}/exceptions/{exceptionID}/resolve", h.resolveCountException)
	write.Post("/api/v1/stock-counts/{countID}/recount", h.recountCount)
	write.Post("/api/v1/stock-counts/{countID}/post", h.postCountEngine)
	write.Post("/api/v1/stock-counts/{countID}/cancel", h.cancelCountEngine)
	write.Post("/api/v1/lots", h.createLot)
	write.Post("/api/v1/serial-numbers", h.createSerialNumber)
}

func (h inventoryHandler) allowed(w http.ResponseWriter, r *http.Request, permission string) bool {
	if sessionFromRequest(r).HasPermission(permission) {
		return true
	}
	writeError(w, r, http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok veya kayıt kapsamınız dışında.")
	return false
}

func (h inventoryHandler) listMovements(w http.ResponseWriter, r *http.Request) {
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
	items, err := h.service.ListMovementsFiltered(r.Context(), filter)
	if err != nil {
		writeInventoryError(w, r, err, "Stok hareketleri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// parseMovementPostedAt accepts a timezone-bearing ISO-8601 timestamp. A
// date-only value is interpreted as a UTC calendar date; an end date expands
// to the last nanosecond of that UTC day so the API's range remains inclusive.
func parseMovementPostedAt(value string, endOfDay bool) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		parsed = parsed.UTC()
		return &parsed, nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, time.UTC)
	if err != nil {
		return nil, errors.New("posted_at ISO-8601 biçiminde olmalıdır")
	}
	if endOfDay {
		parsed = parsed.AddDate(0, 0, 1).Add(-time.Nanosecond)
	}
	return &parsed, nil
}

func movementListFilterFromRequest(r *http.Request) (inventory.MovementListFilter, error) {
	query := r.URL.Query()
	fromValue := strings.TrimSpace(query.Get("posted_at_from"))
	if fromValue == "" {
		fromValue = strings.TrimSpace(query.Get("from"))
	}
	toValue := strings.TrimSpace(query.Get("posted_at_to"))
	if toValue == "" {
		toValue = strings.TrimSpace(query.Get("to"))
	}
	from, err := parseMovementPostedAt(fromValue, false)
	if err != nil {
		return inventory.MovementListFilter{}, err
	}
	to, err := parseMovementPostedAt(toValue, true)
	if err != nil {
		return inventory.MovementListFilter{}, err
	}
	if from != nil && to != nil && to.Before(*from) {
		return inventory.MovementListFilter{}, errors.New("posted_at tarih aralığı geçersiz")
	}
	return inventory.MovementListFilter{
		WarehouseID:  query.Get("warehouse_id"),
		ProductID:    query.Get("product_id"),
		Query:        query.Get("q"),
		Direction:    query.Get("direction"),
		PostedAtFrom: from,
		PostedAtTo:   to,
		Limit:        queryLimit(r, 50, 200),
	}, nil
}

func (h inventoryHandler) getMovement(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.read") {
		return
	}
	item, err := h.service.GetMovement(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "movementID"), sessionFromRequest(r).User.ID)
	if err != nil {
		writeInventoryError(w, r, err, "Stok hareketi okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h inventoryHandler) position(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.read") {
		return
	}
	item, err := h.service.GetPosition(r.Context(), sessionFromRequest(r).CurrentCompanyID, r.URL.Query().Get("warehouse_id"), r.URL.Query().Get("product_id"), r.URL.Query().Get("variant_id"), r.URL.Query().Get("location_id"), r.URL.Query().Get("lot_id"), r.URL.Query().Get("serial_id"), sessionFromRequest(r).User.ID)
	if err != nil {
		writeInventoryError(w, r, err, "Stok pozisyonu okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h inventoryHandler) postMovement(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.movement.post") {
		return
	}
	var input inventory.MovementInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Stok hareketi bilgileri geçersiz.")
		return
	}
	input.CompanyID = sessionFromRequest(r).CurrentCompanyID
	input.ActorUserID = sessionFromRequest(r).User.ID
	if input.ExpiryOverride && !sessionFromRequest(r).HasPermission("inventory.fefo.override") {
		writeError(w, r, http.StatusForbidden, "FORBIDDEN", "SKT/FEFO aşımı için yetkiniz yok.")
		return
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	item, err := h.service.PostMovement(r.Context(), input)
	if err != nil {
		writeInventoryError(w, r, err, "Stok hareketi kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h inventoryHandler) reverseMovement(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.movement.reverse") {
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Stok ters kayıt bilgileri geçersiz.")
		return
	}
	item, err := h.service.ReverseMovement(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "movementID"), input.Reason, strings.TrimSpace(r.Header.Get("Idempotency-Key")), sessionFromRequest(r).User.ID)
	if err != nil {
		writeInventoryError(w, r, err, "Stok hareketi ters kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h inventoryHandler) reverseStockMovementOperation(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.movement.reverse") {
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Stok ters kayıt bilgileri geçersiz.")
		return
	}
	session := sessionFromRequest(r)
	item, err := h.service.ReverseStockMovementOperation(r.Context(), session.CurrentCompanyID, chi.URLParam(r, "operationID"), input.Reason, strings.TrimSpace(r.Header.Get("Idempotency-Key")), session.User.ID)
	if err != nil {
		writeInventoryError(w, r, err, "Stok işlemi ters kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h inventoryHandler) listWarehouses(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.read") {
		return
	}
	items, err := h.service.ListWarehousesFilteredForBranch(r.Context(), sessionFromRequest(r).CurrentCompanyID, r.URL.Query().Get("include_inactive") == "true", r.URL.Query().Get("q"), r.URL.Query().Get("branch_id"), sessionFromRequest(r).User.ID)
	if err != nil {
		writeInventoryError(w, r, err, "Depolar okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h inventoryHandler) getWarehouse(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.read") {
		return
	}
	item, err := h.service.GetWarehouse(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "warehouseID"), sessionFromRequest(r).User.ID)
	if err != nil {
		writeInventoryError(w, r, err, "Depo okunamadı.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h inventoryHandler) createWarehouse(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "organization.warehouse.manage") {
		return
	}
	var input inventory.WarehouseInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Depo bilgileri geçersiz.")
		return
	}
	input.CompanyID = sessionFromRequest(r).CurrentCompanyID
	input.ActorUserID = sessionFromRequest(r).User.ID
	item, err := h.service.CreateWarehouse(r.Context(), input)
	if err != nil {
		writeInventoryError(w, r, err, "Depo oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h inventoryHandler) updateWarehouse(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "organization.warehouse.manage") {
		return
	}
	version, err := expectedVersion(r)
	if err != nil || version < 1 {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Depo güncellemesi için geçerli If-Match sürümü gereklidir.")
		return
	}
	var input inventory.WarehouseUpdateInput
	if err = decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Depo bilgileri geçersiz.")
		return
	}
	session := sessionFromRequest(r)
	item, err := h.service.UpdateWarehouse(r.Context(), session.CurrentCompanyID, chi.URLParam(r, "warehouseID"), version, input, session.User.ID)
	if err != nil {
		writeInventoryError(w, r, err, "Depo güncellenemedi.")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(item.Version, 10)+`"`)
	writeJSON(w, http.StatusOK, item)
}

func (h inventoryHandler) deleteWarehouse(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "organization.warehouse.manage") {
		return
	}
	version, err := expectedVersion(r)
	if err != nil || version < 1 {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Depo silme işlemi için geçerli If-Match sürümü gereklidir.")
		return
	}
	session := sessionFromRequest(r)
	if err = h.service.DeleteWarehouse(r.Context(), session.CurrentCompanyID, chi.URLParam(r, "warehouseID"), version, session.User.ID); err != nil {
		writeInventoryError(w, r, err, "Depo silinemedi.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h inventoryHandler) listLocations(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.read") {
		return
	}
	items, err := h.service.ListLocations(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "warehouseID"), r.URL.Query().Get("include_inactive") == "true", sessionFromRequest(r).User.ID)
	if err != nil {
		writeInventoryError(w, r, err, "Lokasyonlar okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h inventoryHandler) createLocation(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "organization.warehouse.manage") {
		return
	}
	var input inventory.LocationInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Lokasyon bilgileri geçersiz.")
		return
	}
	input.CompanyID, input.WarehouseID = sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "warehouseID")
	input.ActorUserID = sessionFromRequest(r).User.ID
	item, err := h.service.CreateLocation(r.Context(), input)
	if err != nil {
		writeInventoryError(w, r, err, "Lokasyon oluşturulamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h inventoryHandler) listTransfers(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.read") {
		return
	}
	filter, err := transferListFilterFromRequest(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Transfer liste filtresi geçersiz.")
		return
	}
	session := sessionFromRequest(r)
	filter.CompanyID, filter.UserID = session.CurrentCompanyID, session.User.ID
	result, err := h.service.ListTransfersPaged(r.Context(), filter)
	if err != nil {
		writeInventoryError(w, r, err, "Transferler okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func transferListFilterFromRequest(r *http.Request) (inventory.TransferListFilter, error) {
	query := r.URL.Query()
	from, err := parseMovementPostedAt(query.Get("from"), false)
	if err != nil {
		return inventory.TransferListFilter{}, err
	}
	to, err := parseMovementPostedAt(query.Get("to"), true)
	if err != nil {
		return inventory.TransferListFilter{}, err
	}
	if from != nil && to != nil && to.Before(*from) {
		return inventory.TransferListFilter{}, errors.New("transfer tarih aralığı geçersiz")
	}
	activeOnly := false
	if value := strings.TrimSpace(query.Get("active")); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return inventory.TransferListFilter{}, errors.New("active true veya false olmalıdır")
		}
		activeOnly = parsed
	}
	return inventory.TransferListFilter{
		WarehouseID:   strings.TrimSpace(query.Get("warehouse_id")),
		ProductID:     strings.TrimSpace(query.Get("product_id")),
		State:         strings.TrimSpace(query.Get("state")),
		TransferType:  strings.TrimSpace(query.Get("transfer_type")),
		ActiveOnly:    activeOnly,
		Query:         strings.TrimSpace(query.Get("q")),
		CreatedAtFrom: from,
		CreatedAtTo:   to,
		Cursor:        strings.TrimSpace(query.Get("cursor")),
		Limit:         queryLimit(r, 50, 100),
	}, nil
}

func (h inventoryHandler) getTransfer(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.read") {
		return
	}
	item, err := h.service.GetTransfer(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "transferID"), sessionFromRequest(r).User.ID)
	if err != nil {
		writeInventoryError(w, r, err, "Transfer okunamadı.")
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, item.Version))
	writeJSON(w, http.StatusOK, item)
}

func (h inventoryHandler) createTransfer(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.transfer.request") {
		return
	}
	var input inventory.TransferInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Transfer bilgileri geçersiz.")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeError(w, r, http.StatusPreconditionRequired, "IDEMPOTENCY_KEY_REQUIRED", "Transfer oluşturmak için Idempotency-Key gereklidir.")
		return
	}
	if strings.EqualFold(strings.TrimSpace(input.TransferType), inventory.TransferTypeQuick) {
		if !h.allowed(w, r, "inventory.transfer.approve") || !h.allowed(w, r, "inventory.transfer.ship") || !h.allowed(w, r, "inventory.transfer.receive") {
			return
		}
	} else if strings.EqualFold(strings.TrimSpace(input.TransferType), inventory.TransferTypeWorkflow) || strings.TrimSpace(input.TransferType) == "" {
		if !h.allowed(w, r, "inventory.transfer.ship") {
			return
		}
	}
	input.CompanyID, input.RequestedBy = sessionFromRequest(r).CurrentCompanyID, sessionFromRequest(r).User.ID
	input.IdempotencyKey = key
	item, err := h.service.CreateTransfer(r.Context(), input)
	if err != nil {
		writeInventoryError(w, r, err, "Transfer oluşturulamadı.")
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, item.Version))
	writeJSON(w, http.StatusCreated, item)
}

var errTransferIfMatchRequired = errors.New("transfer If-Match required")

func expectedVersion(r *http.Request) (int64, error) {
	value := strings.TrimSpace(r.Header.Get("If-Match"))
	if value == "" {
		return 0, errTransferIfMatchRequired
	}
	return parseIfMatch(value)
}

func writeTransferVersionError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errTransferIfMatchRequired) {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Transfer işlemi için If-Match sürümü gereklidir.")
		return
	}
	writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "If-Match sürümü geçersiz.")
}

func (h inventoryHandler) transition(w http.ResponseWriter, r *http.Request, permission string, action func(string, string, int64) (inventory.Transfer, error)) {
	if !h.allowed(w, r, permission) {
		return
	}
	version, err := expectedVersion(r)
	if err != nil {
		writeTransferVersionError(w, r, err)
		return
	}
	item, err := action(sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "transferID"), version)
	if err != nil {
		writeInventoryError(w, r, err, "Transfer durumu değiştirilemedi.")
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, item.Version))
	writeJSON(w, http.StatusOK, item)
}

func (h inventoryHandler) requestTransfer(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, "inventory.transfer.request", func(companyID, id string, version int64) (inventory.Transfer, error) {
		return h.service.RequestTransfer(r.Context(), companyID, id, sessionFromRequest(r).User.ID, version)
	})
}

func (h inventoryHandler) approveTransfer(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, "inventory.transfer.approve", func(companyID, id string, version int64) (inventory.Transfer, error) {
		return h.service.ApproveTransfer(r.Context(), companyID, id, sessionFromRequest(r).User.ID, version)
	})
}

func (h inventoryHandler) shipTransfer(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, "inventory.transfer.ship", func(companyID, id string, version int64) (inventory.Transfer, error) {
		return h.service.ShipTransfer(r.Context(), companyID, id, sessionFromRequest(r).User.ID, version)
	})
}

func (h inventoryHandler) receiveTransfer(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.transfer.receive") {
		return
	}
	version, err := expectedVersion(r)
	if err != nil {
		writeTransferVersionError(w, r, err)
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		writeError(w, r, http.StatusPreconditionRequired, "IDEMPOTENCY_KEY_REQUIRED", "Transfer teslimi için Idempotency-Key gereklidir.")
		return
	}
	var input struct {
		Lines []inventory.ReceiveLineInput `json:"lines"`
	}
	if err = decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Teslim bilgileri geçersiz.")
		return
	}
	item, err := h.service.ReceiveTransferWithKey(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "transferID"), sessionFromRequest(r).User.ID, version, input.Lines, idempotencyKey)
	if err != nil {
		writeInventoryError(w, r, err, "Transfer teslimi kaydedilemedi.")
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, item.Version))
	writeJSON(w, http.StatusOK, item)
}

func (h inventoryHandler) resolveTransferDiscrepancy(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.transfer.reconcile") {
		return
	}
	var input inventory.TransferResolutionInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Transfer farkı çözüm bilgileri geçersiz.")
		return
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	item, err := h.service.ResolveTransferDiscrepancy(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "transferID"), sessionFromRequest(r).User.ID, input)
	if err != nil {
		writeInventoryError(w, r, err, "Transfer farkı çözülemedi.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h inventoryHandler) cancelTransfer(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.transfer.request") {
		return
	}
	version, err := expectedVersion(r)
	if err != nil {
		writeTransferVersionError(w, r, err)
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if err = decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Transfer iptal bilgileri geçersiz.")
		return
	}
	item, err := h.service.CancelTransfer(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "transferID"), input.Reason, sessionFromRequest(r).User.ID, version)
	if err != nil {
		writeInventoryError(w, r, err, "Transfer iptal edilemedi.")
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, item.Version))
	writeJSON(w, http.StatusOK, item)
}

func (h inventoryHandler) listLots(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.lot_serial.read") {
		return
	}
	items, err := h.service.ListLots(r.Context(), sessionFromRequest(r).CurrentCompanyID, r.URL.Query().Get("product_id"), r.URL.Query().Get("q"), queryLimit(r, 50, 200), r.URL.Query().Get("warehouse_id"), sessionFromRequest(r).User.ID)
	if err != nil {
		writeInventoryError(w, r, err, "Lotlar okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h inventoryHandler) getLot(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.lot_serial.read") {
		return
	}
	item, err := h.service.GetLot(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "lotID"), sessionFromRequest(r).User.ID)
	if err != nil {
		writeInventoryError(w, r, err, "Lot okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h inventoryHandler) suggestFEFO(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.lot_serial.read") {
		return
	}
	items, err := h.service.SuggestFEFO(r.Context(), sessionFromRequest(r).CurrentCompanyID, r.URL.Query().Get("warehouse_id"), r.URL.Query().Get("product_id"), queryLimit(r, 50, 200), sessionFromRequest(r).User.ID)
	if err != nil {
		writeInventoryError(w, r, err, "FEFO önerileri okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h inventoryHandler) listSerialNumbers(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.lot_serial.read") {
		return
	}
	items, err := h.service.ListSerialNumbers(r.Context(), sessionFromRequest(r).CurrentCompanyID, r.URL.Query().Get("product_id"), r.URL.Query().Get("q"), queryLimit(r, 50, 200), r.URL.Query().Get("warehouse_id"), sessionFromRequest(r).User.ID)
	if err != nil {
		writeInventoryError(w, r, err, "Seri numaraları okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h inventoryHandler) getSerialNumber(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.lot_serial.read") {
		return
	}
	item, err := h.service.GetSerialNumber(r.Context(), sessionFromRequest(r).CurrentCompanyID, chi.URLParam(r, "serialID"), sessionFromRequest(r).User.ID)
	if err != nil {
		writeInventoryError(w, r, err, "Seri numarası okunamadı.")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h inventoryHandler) createLot(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusConflict, "LOT_SERIAL_REQUIRES_MOVEMENT", "Lot yalnız stok girişi sırasında oluşturulabilir.")
}

func (h inventoryHandler) createSerialNumber(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusConflict, "LOT_SERIAL_REQUIRES_MOVEMENT", "Seri numarası yalnız stok girişi sırasında oluşturulabilir.")
}

func queryLimit(r *http.Request, fallback, max int) int {
	value, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || value < 1 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}

func writeInventoryError(w http.ResponseWriter, r *http.Request, err error, fallback string) {
	code := inventory.ErrorCode(err)
	status := http.StatusUnprocessableEntity
	message := strings.TrimPrefix(err.Error(), code+": ")
	if errors.Is(err, identity.ErrForbidden) {
		status, code, message = http.StatusForbidden, "FORBIDDEN", "Bu işlem için yetkiniz yok veya kayıt kapsamınız dışında."
	} else if errors.Is(err, identity.ErrConflict) || errors.Is(err, inventory.ErrConflict) {
		status, code, message = http.StatusPreconditionFailed, "VERSION_CONFLICT", "Kayıt başka bir kullanıcı tarafından değiştirilmiş."
	} else if errors.Is(err, identity.ErrValidation) {
		code, message = "VALIDATION_ERROR", strings.TrimPrefix(err.Error(), identity.ErrValidation.Error()+": ")
	} else if errors.Is(err, inventory.ErrNotFound) {
		status, code, message = http.StatusNotFound, "NOT_FOUND", "İstenen stok kaydı bulunamadı."
	} else if errors.Is(err, inventory.ErrStockCountEngineReviewRequired) {
		status, code, message = http.StatusUnprocessableEntity, inventory.ErrStockCountEngineReviewRequired.Error(), "Eksik sayım satırları veya açık inceleme kayıtları tamamlanmadan sayıma işlenemez."
	} else if code == "" {
		status, code, message = http.StatusInternalServerError, "INTERNAL_ERROR", fallback
	}
	if code == "" {
		code, message = "INTERNAL_ERROR", fallback
	}
	if message == "" {
		message = fallback
	}
	if errors.Is(err, inventory.ErrWarehouseHasHistory) {
		status, code, message = http.StatusConflict, inventory.ErrWarehouseHasHistory.Error(), "Bu depoda hareket bulunduğu için silinemez."
	} else if errors.Is(err, inventory.ErrWarehouseInUse) {
		status, code, message = http.StatusConflict, inventory.ErrWarehouseInUse.Error(), "Depo ilişkili kayıtlar nedeniyle silinemez."
	} else if errors.Is(err, inventory.ErrWarehouseHasOpenTransfer) {
		status, code, message = http.StatusConflict, inventory.ErrWarehouseHasOpenTransfer.Error(), "Devam eden transferi bulunan depo pasife alınamaz."
	} else if errors.Is(err, inventory.ErrWarehouseTypeImmutable) {
		status, code, message = http.StatusConflict, inventory.ErrWarehouseTypeImmutable.Error(), "Depo türü oluşturulduktan sonra değiştirilemez."
	} else if errors.Is(err, inventory.ErrWarehouseSystem) {
		status, code, message = http.StatusConflict, inventory.ErrWarehouseSystem.Error(), "Sistem deposu değiştirilemez veya silinemez."
	}
	if errors.Is(err, inventory.ErrVariantRequired) {
		status, code, message = http.StatusUnprocessableEntity, inventory.ErrVariantRequired.Error(), "Varyantlı üründe aktif varyant seçilmelidir."
	} else if errors.Is(err, inventory.ErrVariantInactive) {
		status, code, message = http.StatusUnprocessableEntity, inventory.ErrVariantInactive.Error(), "Pasif varyant seçilemez."
	} else if errors.Is(err, inventory.ErrVariantProductMismatch) {
		status, code, message = http.StatusUnprocessableEntity, inventory.ErrVariantProductMismatch.Error(), "Varyant seçilen ürünle eşleşmiyor."
	}
	switch code {
	case "INSUFFICIENT_STOCK", "SERIAL_ALREADY_IN_STOCK", "SERIAL_NOT_AVAILABLE", "LOT_EXPIRED", "WAREHOUSE_TRANSFER_INVALID_STATE", "STOCK_COUNT_ALREADY_POSTED", "MOVEMENT_ALREADY_REVERSED", "IDEMPOTENCY_CONFLICT", "WAREHOUSE_INACTIVE", "WAREHOUSE_NOT_STANDARD", "WAREHOUSE_MOVEMENT_NOT_ALLOWED", "WAREHOUSE_TYPE_IMMUTABLE", "WAREHOUSE_TRANSFER_SAME_WAREHOUSE":
		if status == http.StatusUnprocessableEntity {
			status = http.StatusConflict
		}
	}
	writeError(w, r, status, code, message)
}
