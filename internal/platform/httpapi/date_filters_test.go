package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLedgerMovementDateRangeRejectsReversedRange(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/party-movements?from=2026-08-24&to=2026-08-23", nil)
	if _, _, err := ledgerMovementDateRange(r); err == nil {
		t.Fatal("reversed cari movement date range was accepted")
	}
}

func TestPaymentDateRangeUsesDateOnlyContract(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/finance/payments?from=2026-08-22&to=2026-08-23", nil)
	from, to, err := paymentDateRange(r)
	if err != nil {
		t.Fatal(err)
	}
	wantFrom := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	if !from.Equal(wantFrom) || !to.Equal(wantTo) {
		t.Fatalf("payment date range was not parsed as date-only: from=%v to=%v", from, to)
	}
}

func TestStockCountDateRangeParsesInclusiveBounds(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stock-counts?from=2026-08-22&to=2026-08-25", nil)
	from, to, err := stockCountDateRange(r)
	if err != nil {
		t.Fatal(err)
	}
	wantFrom := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 8, 25, 23, 59, 59, 999999999, time.UTC)
	if !from.Equal(wantFrom) || !to.Equal(wantTo) {
		t.Fatalf("unexpected stock count date bounds: %v..%v", from, to)
	}
}

func TestStockCountDateRangeRejectsReversedBounds(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stock-counts?from=2026-08-25&to=2026-08-24", nil)
	if _, _, err := stockCountDateRange(r); err == nil {
		t.Fatal("reversed stock count date range was accepted")
	}
}
