package inventory

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTransferStateMachine(t *testing.T) {
	valid := [][2]string{
		{TransferDraft, TransferRequested},
		{TransferDraft, TransferApproved},
		{TransferRequested, TransferApproved},
		{TransferApproved, TransferInTransit},
		{TransferInTransit, TransferPartiallyReceived},
		{TransferPartiallyReceived, TransferReceived},
		{TransferInTransit, TransferCancelled},
		{TransferPartiallyReceived, TransferCancelled},
	}
	for _, transition := range valid {
		if err := ValidateTransferTransition(transition[0], transition[1]); err != nil {
			t.Fatalf("valid transfer transition %v returned %v", transition, err)
		}
	}
	for _, transition := range [][2]string{{TransferDraft, TransferReceived}, {TransferReceived, TransferDraft}, {TransferReceived, TransferCancelled}, {TransferCancelled, TransferRequested}} {
		if err := ValidateTransferTransition(transition[0], transition[1]); !errors.Is(err, ErrWarehouseTransferInvalidState) {
			t.Fatalf("invalid transfer transition %v returned %v", transition, err)
		}
	}
}

func TestTransferOutstandingQuantityExcludesReceivedAndResolvedStock(t *testing.T) {
	if got := transferOutstandingQuantity("10.125", "2.5", "1.125"); got != "6.5" {
		t.Fatalf("outstanding transfer quantity=%q, want 6.5", got)
	}
	if got := transferOutstandingQuantity("4", "3", "1"); got != "0" {
		t.Fatalf("fully resolved transfer quantity=%q, want 0", got)
	}
	if got := transferOutstandingQuantity("4", "5", "0"); got != "0" {
		t.Fatalf("negative outstanding transfer quantity=%q, want 0", got)
	}
}

func TestTransferListFilterNormalizesWorkflowStatesAndType(t *testing.T) {
	got, err := normalizeTransferListFilter(TransferListFilter{
		CompanyID:    "00000000-0000-4000-8000-000000000001",
		WarehouseID:  "00000000-0000-4000-8000-000000000002",
		ProductID:    "00000000-0000-4000-8000-000000000003",
		State:        "in_transit, partially_received",
		TransferType: "quick",
		Query:        "TRF-2026 depo",
		Limit:        25,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.TransferType != TransferTypeQuick || got.WarehouseID == "" || got.ProductID == "" || len(got.States) != 2 {
		t.Fatalf("unexpected normalized transfer filter: %+v", got)
	}
	if got.Query != "TRF-2026 depo" {
		t.Fatalf("transfer search query was not preserved: %q", got.Query)
	}
	if got.States[0] != TransferInTransit || got.States[1] != TransferPartiallyReceived {
		t.Fatalf("workflow states were not normalized: %+v", got.States)
	}
}

func TestTransferSearchTokenEscapesILIKEWildcards(t *testing.T) {
	if got := escapeSearchToken(`a%_\b`); got != `a\%\_\\b` {
		t.Fatalf("escaped transfer search token=%q", got)
	}
}

func TestTransferListFilterActiveOnlyIncludesUnfinishedWorkflowStates(t *testing.T) {
	got, err := normalizeTransferListFilter(TransferListFilter{
		CompanyID:  "00000000-0000-4000-8000-000000000001",
		ActiveOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{TransferRequested, TransferApproved, TransferInTransit, TransferPartiallyReceived} {
		found := false
		for _, actual := range got.States {
			if actual == state {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("active filter omitted %s: %+v", state, got.States)
		}
	}
}

func TestTransferListFilterRejectsInvalidProductID(t *testing.T) {
	if _, err := normalizeTransferListFilter(TransferListFilter{
		CompanyID: "00000000-0000-4000-8000-000000000001",
		ProductID: "not-a-uuid",
	}); err == nil {
		t.Fatal("invalid product filter was accepted")
	}
}

func TestCreateTransferRejectsSameWarehouseWithStableCode(t *testing.T) {
	warehouseID := "00000000-0000-4000-8000-000000000002"
	_, err := (*Service)(nil).CreateTransfer(context.Background(), TransferInput{
		CompanyID:              "00000000-0000-4000-8000-000000000001",
		SourceWarehouseID:      warehouseID,
		DestinationWarehouseID: warehouseID,
		Lines: []TransferLineInput{{
			ProductID: "00000000-0000-4000-8000-000000000003",
			Quantity:  "1",
		}},
	})
	if !errors.Is(err, ErrTransferSameWarehouse) || ErrorCode(err) != ErrTransferSameWarehouse.Error() {
		t.Fatalf("same-warehouse transfer error=%v code=%q", err, ErrorCode(err))
	}
}

func TestCountStateMachine(t *testing.T) {
	if !CanTransitionCount(CountInProgress, Counted) || !CanTransitionCount(CountReview, CountPosted) {
		t.Fatal("valid count transitions were rejected")
	}
	if CanTransitionCount(CountPosted, CountReview) || CanTransitionCount(CountCancelled, CountPosted) {
		t.Fatal("posted/cancelled count can transition")
	}
}

func TestStockCountEngineAllowsReviewRecountWithoutReopeningTerminalStates(t *testing.T) {
	if !CanTransitionStockCountEngine(StockCountEngineReview, StockCountEngineInProgress) {
		t.Fatal("review count cannot return to recount")
	}
	if CanTransitionStockCountEngine(StockCountEnginePosted, StockCountEngineInProgress) ||
		CanTransitionStockCountEngine(StockCountEngineCancelled, StockCountEngineInProgress) {
		t.Fatal("terminal count can return to recount")
	}
}

func TestSnapshotReconciliationUsesExactDecimals(t *testing.T) {
	expected, difference, err := ReconcileSnapshot("10.125", []string{"1.375", "-0.5"}, "12.250")
	if err != nil {
		t.Fatal(err)
	}
	if expected != "11" || difference != "1.25" {
		t.Fatalf("expected exact snapshot reconciliation, got expected=%q difference=%q", expected, difference)
	}
}

func TestSnapshotReconciliationRejectsNegativeCount(t *testing.T) {
	if _, _, err := ReconcileSnapshot("1", nil, "-1"); err == nil {
		t.Fatal("negative count was accepted")
	}
}

func TestReverseDirectionMirrorsMovement(t *testing.T) {
	if reverseDirection(DirectionIn) != DirectionOut || reverseDirection(DirectionOut) != DirectionIn {
		t.Fatal("movement reversal did not mirror direction")
	}
}

func TestManualMovementReasonsAreDirectionAware(t *testing.T) {
	valid := [][3]string{
		{DirectionIn, "PURCHASE_RECEIPT", "inbound purchase receipt"},
		{DirectionIn, "SALES_RETURN", "inbound sales return"},
		{DirectionOut, "DAMAGE", "outbound damage"},
		{DirectionOut, "WASTE", "outbound waste"},
	}
	for _, item := range valid {
		if !validManualReason(item[0], item[1]) {
			t.Fatalf("%s reason %s should be valid", item[0], item[1])
		}
	}
	for _, item := range [][3]string{
		{DirectionIn, "DAMAGE", "inbound damage"},
		{DirectionIn, "WASTE", "inbound waste"},
		{DirectionOut, "PURCHASE_RECEIPT", "outbound purchase receipt"},
		{DirectionOut, "SALES_RETURN", "outbound sales return"},
	} {
		if validManualReason(item[0], item[1]) {
			t.Fatalf("%s reason %s should be rejected", item[0], item[1])
		}
	}
}

func TestWarehouseMovementPolicy(t *testing.T) {
	if !warehouseIsUsable(WarehouseStandard, true) {
		t.Fatal("active standard warehouse should be usable")
	}
	if warehouseIsUsable(WarehouseQuarantine, true) || warehouseIsUsable(WarehouseStandard, false) {
		t.Fatal("special or passive warehouse should not be usable for balances")
	}
	for _, item := range []struct {
		name       string
		warehouse  string
		active     bool
		movement   string
		direction  string
		controlled bool
		reversal   bool
		wantErr    bool
	}{
		{name: "special inbound", warehouse: WarehouseQuarantine, active: true, movement: MovementPurchaseReceipt, direction: DirectionIn},
		{name: "special manual outbound", warehouse: WarehouseQuarantine, active: true, movement: MovementManualAdjustment, direction: DirectionOut, wantErr: true},
		{name: "uncontrolled standard transfer outbound", warehouse: WarehouseStandard, active: true, movement: MovementTransferOut, direction: DirectionOut, wantErr: true},
		{name: "controlled standard transfer outbound", warehouse: WarehouseStandard, active: true, movement: MovementTransferOut, direction: DirectionOut, controlled: true},
		{name: "uncontrolled transit inbound", warehouse: WarehouseTransit, active: true, movement: MovementTransferIn, direction: DirectionIn, wantErr: true},
		{name: "controlled transit inbound", warehouse: WarehouseTransit, active: true, movement: MovementTransferIn, direction: DirectionIn, controlled: true},
		{name: "transit controlled outbound", warehouse: WarehouseTransit, active: true, movement: MovementTransferOut, direction: DirectionOut, controlled: true},
		{name: "special reversal", warehouse: WarehouseReturn, active: true, movement: MovementReconciliation, direction: DirectionOut, controlled: true, reversal: true},
		{name: "passive inbound", warehouse: WarehouseStandard, active: false, movement: MovementPurchaseReceipt, direction: DirectionIn, wantErr: true},
	} {
		t.Run(item.name, func(t *testing.T) {
			err := warehouseAllowsMovement(item.warehouse, item.active, item.movement, item.direction, item.controlled, item.reversal)
			if (err != nil) != item.wantErr {
				t.Fatalf("warehouse policy error=%v, wantErr=%v", err, item.wantErr)
			}
		})
	}
}

func TestWarehouseTypeIsImmutableAfterCreation(t *testing.T) {
	if err := validateWarehouseTypeUnchanged(WarehouseStandard, WarehouseStandard); err != nil {
		t.Fatalf("unchanged warehouse type was rejected: %v", err)
	}

	err := validateWarehouseTypeUnchanged(WarehouseStandard, WarehouseQuarantine)
	if !errors.Is(err, ErrWarehouseTypeImmutable) {
		t.Fatalf("warehouse type change error = %v, want %v", err, ErrWarehouseTypeImmutable)
	}
	if got := ErrorCode(err); got != ErrWarehouseTypeImmutable.Error() {
		t.Fatalf("warehouse type change code = %q, want %q", got, ErrWarehouseTypeImmutable.Error())
	}
}

func TestNormalizeMovementListFilterNormalizesDirectionAndUTC(t *testing.T) {
	from := time.Date(2026, 8, 22, 10, 0, 0, 0, time.FixedZone("TRT", 3*60*60))
	to := time.Date(2026, 8, 22, 12, 0, 0, 0, time.FixedZone("TRT", 3*60*60))
	got, err := normalizeMovementListFilter(MovementListFilter{
		CompanyID:    "00000000-0000-4000-8000-000000000001",
		ProductID:    "00000000-0000-4000-8000-000000000002",
		Direction:    "out",
		PostedAtFrom: &from,
		PostedAtTo:   &to,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Direction != DirectionOut || got.Limit != 50 {
		t.Fatalf("unexpected normalized filter: %+v", got)
	}
	if !got.PostedAtFrom.Equal(from.UTC()) || !got.PostedAtTo.Equal(to.UTC()) {
		t.Fatalf("posted_at bounds were not normalized to UTC: from=%v to=%v", got.PostedAtFrom, got.PostedAtTo)
	}
}

func TestNormalizeMovementListFilterRejectsInvalidRangeAndDirection(t *testing.T) {
	from := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	to := from.Add(-time.Minute)
	for name, filter := range map[string]MovementListFilter{
		"direction": {CompanyID: "00000000-0000-4000-8000-000000000001", Direction: "SIDEWAYS"},
		"range":     {CompanyID: "00000000-0000-4000-8000-000000000001", PostedAtFrom: &from, PostedAtTo: &to},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeMovementListFilter(filter); err == nil {
				t.Fatal("invalid movement filter was accepted")
			}
		})
	}
}

func TestDecimalMaxAvoidsDoubleCountingResolvedDamage(t *testing.T) {
	if got := decimalMax("4", "2"); got != "4" {
		t.Fatalf("decimalMax(4,2)=%q", got)
	}
	if got := decimalMax("2", "4"); got != "4" {
		t.Fatalf("decimalMax(2,4)=%q", got)
	}
	if got := decimalMax("2.5000", "2.5"); got != "2.5000" {
		t.Fatalf("decimalMax(2.5000,2.5)=%q", got)
	}
}
