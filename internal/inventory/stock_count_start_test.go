package inventory

import "testing"

func TestStartStockCountWithNullableStockDimensions(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0", true)
	if _, err := fixture.service.PostMovement(fixture.ctx, variantMovementInput(transferTestVariant)); err != nil {
		t.Fatal(err)
	}

	count, err := fixture.service.StartStockCount(fixture.ctx, StockCountInput{
		CompanyID: transferTestCompany, PostedBy: transferTestUser,
		WarehouseID: transferTestSource,
	})
	if err != nil {
		t.Fatalf("start stock count: %v", err)
	}
	if len(count.Lines) != 1 {
		t.Fatalf("line count=%d, want 1", len(count.Lines))
	}
	if count.Lines[0].VariantID == nil {
		t.Fatal("variant scope lost its variant id")
	}
}
