package inventory

import (
	"fmt"
	"testing"
)

func TestMovementFeedPaginatesWithoutDuplicateOperationLines(t *testing.T) {
	f := newTransferStockFixture(t, "100", "0", true)
	expected := map[string]bool{}
	for i := 0; i < 7; i++ {
		o, err := f.service.PostStockMovementOperation(f.ctx, StockMovementOperationInput{CompanyID: transferTestCompany, ActorUserID: transferTestUser, WarehouseID: transferTestSource, ProductID: transferTestProduct,
			MovementType: MovementManualAdjustment, Direction: DirectionIn, UnitCode: "ADET", Currency: "TRY", ReasonCode: "PURCHASE_RECEIPT", IdempotencyKey: fmt.Sprintf("feed-%d", i), Lines: []StockMovementOperationLine{{VariantID: transferTestVariant, Quantity: "2"}}})
		if err != nil {
			t.Fatal(err)
		}
		expected[o.ID] = true
		m, err := f.service.PostMovement(f.ctx, variantMovementInput(transferTestVariant))
		if err != nil {
			t.Fatal(err)
		}
		expected[m.ID] = true
	}
	filter := MovementListFilter{CompanyID: transferTestCompany, UserID: transferTestUser, Limit: 3}
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err := f.service.ListMovementFeed(f.ctx, filter, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Items {
			id := ""
			switch v := row.(type) {
			case Movement:
				id = v.ID
			case StockMovementOperation:
				id = v.ID
			default:
				t.Fatalf("unexpected row: %T", row)
			}
			if seen[id] || !expected[id] {
				t.Fatalf("duplicate or child row %s", id)
			}
			seen[id] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != len(expected) {
		t.Fatalf("lost rows: %d/%d", len(seen), len(expected))
	}
	filtered := filter
	filtered.MovementType = MovementSalesDispatch
	filtered.Query = "STK-TEST-BLK-M"
	page, err := f.service.ListMovementFeed(f.ctx, filtered, "")
	if err != nil || len(page.Items) != 3 || page.NextCursor == "" {
		t.Fatalf("server variant/type filter: %+v %v", page, err)
	}
	filtered.Direction = DirectionIn
	if _, err = f.service.ListMovementFeed(f.ctx, filtered, page.NextCursor); err == nil {
		t.Fatal("accepted changed filter cursor")
	}
	filtered.WarehouseID = transferTestDestination
	page, err = f.service.ListMovementFeed(f.ctx, filtered, "")
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("warehouse filter: %+v %v", page, err)
	}
}
