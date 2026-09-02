package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/inventory"
	"github.com/go-chi/chi/v5"
)

func countEngineResponse(item inventory.StockCountEngine) map[string]any {
	return map[string]any{
		"id": item.ID, "company_id": item.CompanyID, "count_no": item.CountNo, "description": item.Description,
		"warehouse_id": item.WarehouseID, "warehouse_code": item.WarehouseCode, "warehouse_name": item.WarehouseName,
		"state": item.State, "status": item.State, "movement_policy": item.MovementPolicy,
		"scope_mode": item.ScopeMode, "snapshot_at": item.SnapshotAt, "started_at": item.StartedAt, "finished_at": item.FinishedAt, "version": item.Version,
		"passes": item.Passes, "scopes": item.Scopes, "lines": item.Scopes, "exceptions": item.Exceptions,
	}
}

func stockCountDateRange(r *http.Request) (*time.Time, *time.Time, error) {
	query := r.URL.Query()
	fromValue := strings.TrimSpace(query.Get("from"))
	toValue := strings.TrimSpace(query.Get("to"))
	from, err := parseMovementPostedAt(fromValue, false)
	if err != nil {
		return nil, nil, err
	}
	to, err := parseMovementPostedAt(toValue, true)
	if err != nil {
		return nil, nil, err
	}
	if from != nil && to != nil && to.Before(*from) {
		return nil, nil, errors.New("sayım tarih aralığı geçersiz")
	}
	return from, to, nil
}

func (h inventoryHandler) addCountScope(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.count.post") {
		return
	}
	var body struct {
		ProductID string `json:"product_id"`
		VariantID string `json:"variant_id"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Sayım stok satırı geçersiz.")
		return
	}
	item, err := h.service.AddStockCountEngineScope(r.Context(), inventory.StockCountEngineAddScopeInput{
		CompanyID: sessionFromRequest(r).CurrentCompanyID, CountID: chi.URLParam(r, "countID"), ProductID: body.ProductID, VariantID: body.VariantID,
		ActorUserID: sessionFromRequest(r).User.ID,
	})
	if err != nil {
		writeInventoryError(w, r, err, "Stok sayım kapsamına eklenemedi.")
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, item.Version))
	writeJSON(w, http.StatusOK, countEngineResponse(item))
}

func (h inventoryHandler) startCountEngine(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.count.post") {
		return
	}
	var input inventory.StockCountEngineStartInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Sayım bilgileri geçersiz.")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeError(w, r, http.StatusPreconditionRequired, "IDEMPOTENCY_KEY_REQUIRED", "Sayımı başlatmak için işlem anahtarı gereklidir.")
		return
	}
	session := sessionFromRequest(r)
	input.CompanyID, input.ActorUserID, input.IdempotencyKey = session.CurrentCompanyID, session.User.ID, key
	item, err := h.service.StartStockCountEngine(r.Context(), input)
	if err != nil {
		writeInventoryError(w, r, err, "Sayım başlatılamadı.")
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, item.Version))
	writeJSON(w, http.StatusCreated, countEngineResponse(item))
}

func (h inventoryHandler) listCountEngines(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.read") {
		return
	}
	session := sessionFromRequest(r)
	dateFrom, dateTo, err := stockCountDateRange(r)
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Sayım tarih aralığı geçersiz.")
		return
	}
	result, err := h.service.ListStockCountEngines(r.Context(), session.CurrentCompanyID, r.URL.Query().Get("state"), queryLimit(r, 50, 100), session.User.ID, dateFrom, dateTo, r.URL.Query().Get("cursor"))
	if err != nil {
		writeInventoryError(w, r, err, "Sayımlar okunamadı.")
		return
	}
	views := make([]map[string]any, 0, len(result.Items))
	for _, item := range result.Items {
		views = append(views, countEngineResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": views, "next_cursor": result.NextCursor})
}

func (h inventoryHandler) getCountEngine(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.read") {
		return
	}
	session := sessionFromRequest(r)
	item, err := h.service.GetStockCountEngine(r.Context(), session.CurrentCompanyID, chi.URLParam(r, "countID"), session.User.ID)
	if err != nil {
		writeInventoryError(w, r, err, "Sayım okunamadı.")
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, item.Version))
	writeJSON(w, http.StatusOK, countEngineResponse(item))
}

func (h inventoryHandler) createCountPass(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.count.post") {
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, 400, "VALIDATION_ERROR", "Sayım turu geçersiz.")
		return
	}
	if strings.EqualFold(strings.TrimSpace(body.Mode), inventory.StockCountEngineBlind) {
		writeError(w, r, http.StatusUnprocessableEntity, "VALIDATION_ERROR", "Kör sayım kullanılamaz.")
		return
	}
	session := sessionFromRequest(r)
	item, err := h.service.StartStockCountPass(r.Context(), inventory.StockCountEnginePassInput{CompanyID: session.CurrentCompanyID, CountID: chi.URLParam(r, "countID"), Mode: inventory.StockCountEngineOpen, ActorUserID: session.User.ID})
	if err != nil {
		writeInventoryError(w, r, err, "Sayım turu başlatılamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h inventoryHandler) createCountSession(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.count.post") {
		return
	}
	var body struct {
		PassID   string `json:"pass_id"`
		DeviceID string `json:"device_id"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, 400, "VALIDATION_ERROR", "Sayım oturumu geçersiz.")
		return
	}
	session := sessionFromRequest(r)
	id, err := h.service.StartStockCountSession(r.Context(), inventory.StockCountEngineSessionInput{CompanyID: session.CurrentCompanyID, CountID: chi.URLParam(r, "countID"), PassID: body.PassID, ClientSessionID: body.DeviceID, ActorUserID: session.User.ID})
	if err != nil {
		writeInventoryError(w, r, err, "Sayım oturumu başlatılamadı.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "session_id": id})
}

type countScanEventRequest struct {
	EventID   string      `json:"event_id"`
	Barcode   string      `json:"barcode"`
	Quantity  json.Number `json:"quantity"`
	ScannedAt time.Time   `json:"scanned_at"`
}

func (h inventoryHandler) batchCountScans(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.count.post") {
		return
	}
	var body struct {
		SessionID string                  `json:"session_id"`
		Events    []countScanEventRequest `json:"events"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, 400, "VALIDATION_ERROR", "Barkod taramaları geçersiz.")
		return
	}
	session := sessionFromRequest(r)
	countID := chi.URLParam(r, "countID")
	passID, err := h.service.CurrentStockCountPassID(r.Context(), session.CurrentCompanyID, countID, body.SessionID)
	if err != nil {
		writeInventoryError(w, r, err, "Açık sayım turu bulunamadı.")
		return
	}
	events := make([]inventory.StockCountEngineEventInput, 0, len(body.Events))
	for _, event := range body.Events {
		events = append(events, inventory.StockCountEngineEventInput{EventID: event.EventID, Barcode: event.Barcode, Quantity: event.Quantity.String(), OccurredAt: event.ScannedAt, EventType: inventory.StockCountEngineScan})
	}
	items, err := h.service.BatchScanStockCount(r.Context(), inventory.StockCountEngineBatchInput{CompanyID: session.CurrentCompanyID, CountID: countID, PassID: passID, SessionID: body.SessionID, ActorUserID: session.User.ID, Events: events})
	if err != nil {
		writeInventoryError(w, r, err, "Barkod taramaları kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": items})
}

func (h inventoryHandler) countLineEvent(w http.ResponseWriter, r *http.Request, zero bool) {
	if !h.allowed(w, r, "inventory.count.post") {
		return
	}
	var body struct {
		EventID  string      `json:"event_id"`
		PassID   string      `json:"pass_id"`
		Quantity json.Number `json:"quantity"`
		Reason   string      `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, 400, "VALIDATION_ERROR", "Sayım satırı geçersiz.")
		return
	}
	session := sessionFromRequest(r)
	countID := chi.URLParam(r, "countID")
	passID := strings.TrimSpace(body.PassID)
	if passID == "" {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Sayım turu seçilmelidir.")
		return
	}
	input := inventory.StockCountEngineEventInput{CompanyID: session.CurrentCompanyID, EventID: body.EventID, CountID: countID, PassID: passID, ScopeID: chi.URLParam(r, "lineID"), Quantity: body.Quantity.String(), Reason: body.Reason, ActorUserID: session.User.ID}
	var items []inventory.StockCountEngineEvent
	var err error
	if zero {
		items, err = h.service.ConfirmStockCountZero(r.Context(), input)
	} else {
		items, err = h.service.CorrectStockCount(r.Context(), input)
	}
	if err != nil {
		writeInventoryError(w, r, err, "Sayım satırı kaydedilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": items})
}

func (h inventoryHandler) submitCountPass(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.count.post") {
		return
	}
	session := sessionFromRequest(r)
	reviewed, reviewErr := h.service.SubmitStockCountPassAndReviewForCompany(r.Context(), session.CurrentCompanyID, chi.URLParam(r, "countID"), chi.URLParam(r, "passID"), session.User.ID)
	if reviewErr != nil {
		writeInventoryError(w, r, reviewErr, "Sayım incelemeye gönderilemedi.")
		return
	}
	view := countEngineResponse(reviewed)
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, reviewed.Version))
	writeJSON(w, http.StatusOK, view)
}

func (h inventoryHandler) resolveCountException(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.count.post") {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "İnceleme kararı geçersiz.")
		return
	}
	session := sessionFromRequest(r)
	item, err := h.service.ResolveStockCountEngineException(r.Context(), session.CurrentCompanyID, chi.URLParam(r, "countID"), chi.URLParam(r, "exceptionID"), session.User.ID, body.Reason)
	if err != nil {
		writeInventoryError(w, r, err, "İnceleme kararı kaydedilemedi.")
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, item.Version))
	writeJSON(w, http.StatusOK, countEngineResponse(item))
}

func (h inventoryHandler) recountCount(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.count.post") {
		return
	}
	version, err := expectedVersion(r)
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Sayımı yeniden sayıma almak için güncel sürüm gereklidir.")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeError(w, r, http.StatusPreconditionRequired, "IDEMPOTENCY_KEY_REQUIRED", "Sayımı yeniden sayıma almak için işlem anahtarı gereklidir.")
		return
	}
	session := sessionFromRequest(r)
	item, err := h.service.ReopenStockCountEngineForRecount(r.Context(), inventory.StockCountEngineRecountInput{
		CompanyID: session.CurrentCompanyID, CountID: chi.URLParam(r, "countID"), ExpectedVersion: version,
		IdempotencyKey: key, ActorUserID: session.User.ID,
	})
	if err != nil {
		writeInventoryError(w, r, err, "Sayım yeniden sayıma alınamadı.")
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, item.Version))
	writeJSON(w, http.StatusOK, countEngineResponse(item))
}

func (h inventoryHandler) syncCountEngine(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.read") {
		return
	}
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	session := sessionFromRequest(r)
	item, err := h.service.SyncStockCountEngine(r.Context(), inventory.StockCountEngineSyncInput{CompanyID: session.CurrentCompanyID, CountID: chi.URLParam(r, "countID"), AfterSeq: cursor, ActorUserID: session.User.ID})
	if err != nil {
		writeInventoryError(w, r, err, "Sayım eşitlenemedi.")
		return
	}
	count, err := h.service.GetStockCountEngine(r.Context(), session.CurrentCompanyID, chi.URLParam(r, "countID"), session.User.ID)
	if err != nil {
		writeInventoryError(w, r, err, "Sayım yenilenemedi.")
		return
	}
	view := countEngineResponse(count)
	view["events"] = item.Events
	if len(item.Events) > 0 {
		view["cursor"] = item.Events[len(item.Events)-1].EventSeq
	} else {
		view["cursor"] = cursor
	}
	writeJSON(w, http.StatusOK, view)
}

func (h inventoryHandler) postCountEngine(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.count.post") {
		return
	}
	version, err := expectedVersion(r)
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Sayımı post etmek için güncel sürüm gereklidir.")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeError(w, r, http.StatusPreconditionRequired, "IDEMPOTENCY_KEY_REQUIRED", "Sayımı post etmek için işlem anahtarı gereklidir.")
		return
	}
	session := sessionFromRequest(r)
	item, err := h.service.PostStockCountEngine(r.Context(), inventory.StockCountEnginePostInput{CompanyID: session.CurrentCompanyID, CountID: chi.URLParam(r, "countID"), ExpectedVersion: version, IdempotencyKey: key, ActorUserID: session.User.ID})
	if err != nil {
		writeInventoryError(w, r, err, "Sayım post edilemedi.")
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, item.Version))
	writeJSON(w, http.StatusOK, countEngineResponse(item))
}

func (h inventoryHandler) cancelCountEngine(w http.ResponseWriter, r *http.Request) {
	if !h.allowed(w, r, "inventory.count.post") {
		return
	}
	version, err := expectedVersion(r)
	if err != nil {
		writeError(w, r, http.StatusPreconditionRequired, "IF_MATCH_REQUIRED", "Sayımı iptal etmek için güncel sürüm gereklidir.")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = decodeJSON(r, &body)
	if strings.TrimSpace(body.Reason) == "" {
		body.Reason = "Kullanıcı tarafından iptal edildi"
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		key = "count-cancel:" + chi.URLParam(r, "countID") + ":" + strconv.FormatInt(version, 10)
	}
	session := sessionFromRequest(r)
	item, err := h.service.CancelStockCountEngine(r.Context(), inventory.StockCountEngineCancelInput{CompanyID: session.CurrentCompanyID, CountID: chi.URLParam(r, "countID"), ExpectedVersion: version, IdempotencyKey: key, Reason: body.Reason, ActorUserID: session.User.ID})
	if err != nil {
		writeInventoryError(w, r, err, "Sayım iptal edilemedi.")
		return
	}
	writeJSON(w, http.StatusOK, countEngineResponse(item))
}
