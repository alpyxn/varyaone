package inventory

import (
	"testing"
)

func TestTransferLineIdentitySnapshotsAreCompanyScopedAndDoNotFallback(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0")
	transfer := fixture.createInTransitTransfer(t, "TRF-SNAPSHOT-SCOPE", "2")
	lineID := transfer.Lines[0].ID

	identities, err := fixture.service.LoadTransferLineIdentities(fixture.ctx, transferTestCompany, []string{lineID, lineID})
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 1 {
		t.Fatalf("identity count=%d, want 1 after duplicate input de-duplication", len(identities))
	}
	got := identities[0]
	if got.CompanyID != transferTestCompany || got.ProductCode != "STK-TEST" || got.ProductName != "Transfer Test Stoku" {
		t.Fatalf("unexpected company/product snapshot: %+v", got)
	}

	if _, err = fixture.pool.Exec(fixture.ctx, `UPDATE products SET code='STK-CHANGED',name='Changed Product' WHERE company_id=$1 AND id=$2`, transferTestCompany, transferTestProduct); err != nil {
		t.Fatal(err)
	}
	identities, err = fixture.service.LoadTransferLineIdentities(fixture.ctx, transferTestCompany, []string{lineID})
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 1 || identities[0].ProductCode != "STK-TEST" || identities[0].ProductName != "Transfer Test Stoku" {
		t.Fatalf("snapshot drifted to mutable product data: %+v", identities)
	}

	identities, err = fixture.service.LoadTransferLineIdentities(fixture.ctx, "10000000-0000-4000-8000-000000000009", []string{lineID})
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 0 {
		t.Fatalf("cross-company identity lookup returned %+v", identities)
	}
}

func TestTransferLineIdentitySnapshotBackfillsOnInsertAndCapturesVariantDescription(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0", true)
	transfer, err := fixture.service.CreateTransfer(fixture.ctx, TransferInput{
		CompanyID:              transferTestCompany,
		RequestedBy:            transferTestUser,
		TransferNo:             "TRF-SNAPSHOT-VARIANT",
		TransferType:           TransferTypeWorkflow,
		SourceWarehouseID:      transferTestSource,
		DestinationWarehouseID: transferTestDestination,
		TransitWarehouseID:     transferTestTransit,
		Lines:                  []TransferLineInput{{ProductID: transferTestProduct, VariantID: transferTestVariant, Quantity: "2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The service create path supplies only product/variant IDs. Migration 66's
	// insert trigger is the bounded integration point that captures the names.
	if _, err = fixture.pool.Exec(fixture.ctx, `UPDATE warehouse_transfer_lines
		SET product_code_snapshot='',product_name_snapshot='',variant_code_snapshot='',variant_description_snapshot=''
		WHERE company_id=$1 AND id=$2`, transferTestCompany, transfer.Lines[0].ID); err == nil {
		t.Fatal("in-transit snapshot columns were mutable")
	}

	identities, err := fixture.service.LoadTransferLineIdentities(fixture.ctx, transferTestCompany, []string{transfer.Lines[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 1 {
		t.Fatalf("variant identity count=%d, want 1", len(identities))
	}
	if identities[0].VariantCode != "STK-TEST-BLK-M" || identities[0].VariantDescription != "Siyah / M" {
		t.Fatalf("unexpected variant snapshot: %+v", identities[0])
	}
}

func TestTransferLineIdentitySnapshotsRemainImmutableAfterQuickReceipt(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0")
	transfer, err := fixture.service.CreateTransfer(fixture.ctx, TransferInput{
		CompanyID:              transferTestCompany,
		RequestedBy:            transferTestUser,
		TransferNo:             "TRF-SNAPSHOT-QUICK",
		TransferType:           TransferTypeQuick,
		SourceWarehouseID:      transferTestSource,
		DestinationWarehouseID: transferTestDestination,
		TransitWarehouseID:     transferTestTransit,
		Lines:                  []TransferLineInput{{ProductID: transferTestProduct, Quantity: "2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if transfer.State != TransferReceived {
		t.Fatalf("quick transfer state=%s, want %s", transfer.State, TransferReceived)
	}
	if _, err = fixture.pool.Exec(fixture.ctx, `UPDATE warehouse_transfer_lines
		SET product_name_snapshot='tampered' WHERE company_id=$1 AND id=$2`, transferTestCompany, transfer.Lines[0].ID); err == nil {
		t.Fatal("received snapshot columns were mutable")
	}
}
