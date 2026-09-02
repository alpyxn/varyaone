package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/inventory"
)

func TestWriteInventoryErrorMapsReviewRequiredToUnprocessableEntity(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/stock-counts/count/post", nil)

	writeInventoryError(recorder, request, inventory.ErrStockCountEngineReviewRequired, "fallback")

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d, want %d", recorder.Code, http.StatusUnprocessableEntity)
	}
	var response struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != inventory.ErrStockCountEngineReviewRequired.Error() {
		t.Fatalf("code=%q, want %q", response.Code, inventory.ErrStockCountEngineReviewRequired.Error())
	}
	if response.Message == "fallback" || response.Message == "" {
		t.Fatalf("message=%q, want actionable review message", response.Message)
	}
}

func TestWriteInventoryErrorMapsOpenWarehouseTransferToConflict(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/warehouses/id", nil)

	writeInventoryError(recorder, request, inventory.ErrWarehouseHasOpenTransfer, "fallback")

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d, want %d", recorder.Code, http.StatusConflict)
	}
	var response struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != inventory.ErrWarehouseHasOpenTransfer.Error() {
		t.Fatalf("code=%q, want %q", response.Code, inventory.ErrWarehouseHasOpenTransfer.Error())
	}
	if response.Message != "Devam eden transferi bulunan depo pasife alınamaz." {
		t.Fatalf("message=%q, want open-transfer message", response.Message)
	}
}

func TestTransferExpectedVersionRequiresIfMatch(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/warehouse-transfers/id/ship", nil)
	if _, err := expectedVersion(request); !errors.Is(err, errTransferIfMatchRequired) {
		t.Fatalf("missing If-Match error=%v", err)
	}
	request.Header.Set("If-Match", `"7"`)
	version, err := expectedVersion(request)
	if err != nil || version != 7 {
		t.Fatalf("parsed transfer version=%d error=%v", version, err)
	}
}

func TestMovementListFilterParsesISODateRangeAndDirection(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stock-movements?from=2026-08-22&to=2026-08-23&direction=out&product_id=00000000-0000-4000-8000-000000000001", nil)
	filter, err := movementListFilterFromRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	if filter.Direction != "out" || filter.ProductID == "" {
		t.Fatalf("query filters were not preserved: %+v", filter)
	}
	wantFrom := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 8, 23, 23, 59, 59, 999999999, time.UTC)
	if !filter.PostedAtFrom.Equal(wantFrom) || !filter.PostedAtTo.Equal(wantTo) {
		t.Fatalf("date-only range was not expanded in UTC: from=%v to=%v", filter.PostedAtFrom, filter.PostedAtTo)
	}
}

func TestMovementListFilterRejectsTimezoneLessTimestamp(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stock-movements?posted_at_from=2026-08-22T12:00:00", nil)
	if _, err := movementListFilterFromRequest(r); err == nil {
		t.Fatal("timezone-less posted_at timestamp was accepted")
	}
}

func TestMovementListFilterPreservesSearchQuery(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stock-movements?q=fire%20ve%20zayi", nil)
	filter, err := movementListFilterFromRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	if filter.Query != "fire ve zayi" {
		t.Fatalf("search query was not preserved: %q", filter.Query)
	}
}

func TestTransferListFilterParsesWarehouseStateTypeAndActive(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/warehouse-transfers?warehouse_id=00000000-0000-4000-8000-000000000001&product_id=00000000-0000-4000-8000-000000000002&state=IN_TRANSIT,PARTIALLY_RECEIVED&transfer_type=quick&active=true&q=TRF-2026%20depo", nil)
	filter, err := transferListFilterFromRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	if filter.WarehouseID == "" || filter.ProductID == "" || filter.State != "IN_TRANSIT,PARTIALLY_RECEIVED" || filter.TransferType != "quick" || !filter.ActiveOnly || filter.Query != "TRF-2026 depo" {
		t.Fatalf("transfer list filters were not preserved: %+v", filter)
	}
}

func TestTransferListFilterRejectsInvalidActiveFlag(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/warehouse-transfers?active=maybe", nil)
	if _, err := transferListFilterFromRequest(r); err == nil {
		t.Fatal("invalid active filter was accepted")
	}
}

func TestTransferListFilterParsesInclusiveDateRange(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/warehouse-transfers?from=2026-08-22&to=2026-08-23", nil)
	filter, err := transferListFilterFromRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	wantFrom := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 8, 23, 23, 59, 59, 999999999, time.UTC)
	if !filter.CreatedAtFrom.Equal(wantFrom) || !filter.CreatedAtTo.Equal(wantTo) {
		t.Fatalf("transfer date range was not inclusive: from=%v to=%v", filter.CreatedAtFrom, filter.CreatedAtTo)
	}
}
