package inventory

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func variantMovementInput(variantID string) MovementInput {
	return MovementInput{
		CompanyID:    transferTestCompany,
		ActorUserID:  transferTestUser,
		WarehouseID:  transferTestSource,
		ProductID:    transferTestProduct,
		VariantID:    variantID,
		MovementType: MovementSalesDispatch,
		Direction:    DirectionOut,
		Quantity:     "1",
		ReasonCode:   MovementSalesDispatch,
	}
}

func TestVariantMovementRequiresVariantAndRejectsParentLedgerRow(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0", true)
	_, err := fixture.service.PostMovement(fixture.ctx, variantMovementInput(""))
	if !errors.Is(err, ErrVariantRequired) {
		t.Fatalf("parent movement error=%v, want %v", err, ErrVariantRequired)
	}
}

func TestVariantMovementRejectsInactiveVariant(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0", true)
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE product_variants SET is_active=false WHERE company_id=$1 AND id=$2`, transferTestCompany, transferTestVariant); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.service.PostMovement(fixture.ctx, variantMovementInput(transferTestVariant))
	if !errors.Is(err, ErrVariantInactive) {
		t.Fatalf("inactive variant error=%v, want %v", err, ErrVariantInactive)
	}
}

func TestVariantMovementRejectsProductMismatchAndWrongCompany(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0", true)
	otherProduct := "10000000-0000-4000-8000-000000000008"
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO products(id,company_id,code,name,kind) VALUES($1,$2,'STK-OTHER','Başka Ürün','PHYSICAL')`, otherProduct, transferTestCompany); err != nil {
		t.Fatal(err)
	}
	input := variantMovementInput(transferTestVariant)
	input.ProductID = otherProduct
	if _, err := fixture.service.PostMovement(fixture.ctx, input); !errors.Is(err, ErrVariantProductMismatch) {
		t.Fatalf("product mismatch error=%v, want %v", err, ErrVariantProductMismatch)
	}
	otherCompany := "10000000-0000-4000-8000-000000000009"
	otherCompanyProduct := "10000000-0000-4000-8000-00000000000a"
	otherCompanyVariant := "10000000-0000-4000-8000-00000000000b"
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO companies(id,legal_name,trade_name,entity_type) VALUES($1,'Other Test AŞ','Other Test','LEGAL_ENTITY')`, otherCompany); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO products(id,company_id,code,name,kind,variants_enabled) VALUES($1,$2,'OTHER-STK','Diğer Firma Stoku','PHYSICAL',true)`, otherCompanyProduct, otherCompany); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO product_variants(id,company_id,product_id,variant_code,variant_signature) VALUES($1,$2,$3,'OTHER-STK-V1','MANUAL:OTHER-STK-V1')`, otherCompanyVariant, otherCompany, otherCompanyProduct); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.PostMovement(fixture.ctx, variantMovementInput(otherCompanyVariant)); !errors.Is(err, ErrVariantProductMismatch) {
		t.Fatalf("wrong-company variant error=%v, want %v", err, ErrVariantProductMismatch)
	}
}

func TestVariantPositionReturnsAggregateAndVariantPresentation(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "3", true)
	aggregate, err := fixture.service.GetPosition(fixture.ctx, transferTestCompany, transferTestSource, transferTestProduct, "", "", "", "", transferTestUser)
	if err != nil {
		t.Fatal(err)
	}
	if !aggregate.IsAggregate {
		t.Fatal("variant parent position was not marked as aggregate")
	}
	assertQuantity(t, aggregate.PhysicalQuantity, "10", "variant aggregate physical")
	assertQuantity(t, aggregate.ReservedQuantity, "3", "variant aggregate reserved")
	assertQuantity(t, aggregate.AvailableQuantity, "7", "variant aggregate available")

	variant, err := fixture.service.GetPosition(fixture.ctx, transferTestCompany, transferTestSource, transferTestProduct, transferTestVariant, "", "", "", transferTestUser)
	if err != nil {
		t.Fatal(err)
	}
	if variant.VariantCode != "STK-TEST-BLK-M" || variant.VariantDisplay["COLOR"] != "Siyah" {
		t.Fatalf("variant presentation=%+v code=%q", variant.VariantDisplay, variant.VariantCode)
	}
}

func TestVariantTransferRequiresVariantAndReturnsPresentation(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0", true)
	_, err := fixture.service.CreateTransfer(fixture.ctx, TransferInput{
		CompanyID:              transferTestCompany,
		RequestedBy:            transferTestUser,
		TransferNo:             "TRF-VARIANT-REQUIRED",
		TransferType:           TransferTypeWorkflow,
		SourceWarehouseID:      transferTestSource,
		DestinationWarehouseID: transferTestDestination,
		TransitWarehouseID:     transferTestTransit,
		Lines:                  []TransferLineInput{{ProductID: transferTestProduct, Quantity: "1"}},
	})
	if !errors.Is(err, ErrVariantRequired) {
		t.Fatalf("parent transfer error=%v, want %v", err, ErrVariantRequired)
	}

	item, err := fixture.service.CreateTransfer(fixture.ctx, TransferInput{
		CompanyID:              transferTestCompany,
		RequestedBy:            transferTestUser,
		TransferNo:             "TRF-VARIANT-PRESENTATION",
		TransferType:           TransferTypeWorkflow,
		SourceWarehouseID:      transferTestSource,
		DestinationWarehouseID: transferTestDestination,
		TransitWarehouseID:     transferTestTransit,
		Lines:                  []TransferLineInput{{ProductID: transferTestProduct, VariantID: transferTestVariant, Quantity: "2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Lines) != 1 || item.Lines[0].VariantCode != "STK-TEST-BLK-M" || item.Lines[0].VariantDescription != "Siyah / M" {
		t.Fatalf("transfer variant presentation=%+v", item.Lines)
	}
}

func TestConcurrentVariantTransfersCannotConsumeSameAvailableStock(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0", true)
	results := make(chan error, 2)
	for _, number := range []string{"TRF-VARIANT-RACE-1", "TRF-VARIANT-RACE-2"} {
		go func(transferNo string) {
			_, err := fixture.service.CreateTransfer(fixture.ctx, TransferInput{
				CompanyID:              transferTestCompany,
				RequestedBy:            transferTestUser,
				TransferNo:             transferNo,
				TransferType:           TransferTypeWorkflow,
				SourceWarehouseID:      transferTestSource,
				DestinationWarehouseID: transferTestDestination,
				TransitWarehouseID:     transferTestTransit,
				Lines:                  []TransferLineInput{{ProductID: transferTestProduct, VariantID: transferTestVariant, Quantity: "7"}},
			})
			results <- err
		}(number)
	}
	succeeded, insufficient := 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrInsufficientStock):
			insufficient++
		default:
			t.Fatalf("unexpected concurrent variant transfer error: %v", err)
		}
	}
	if succeeded != 1 || insufficient != 1 {
		t.Fatalf("concurrent variant transfer success=%d insufficient=%d, want 1/1", succeeded, insufficient)
	}
	_, _, available := fixture.rawBalance(t, transferTestSource)
	assertQuantity(t, available, "3", "variant source available after race")
}

func TestStockMovementOperationRollsBackEveryLineWhenOneVariantIsInsufficient(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0", true)
	secondVariant := "10000000-0000-4000-8000-000000000014"
	secondPosition := "10000000-0000-4000-8000-000000000015"
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO product_variants(id,company_id,product_id,variant_code,variant_signature)
		VALUES($1,$2,$3,'STK-TEST-BLK-M-2','SECOND-VARIANT');
		INSERT INTO product_variant_values(company_id,variant_id,definition_id,option_id)
		SELECT company_id,$1,definition_id,option_id
		FROM product_variant_values
		WHERE company_id=$2 AND variant_id=$3;
		INSERT INTO stock_positions(id,company_id,warehouse_id,product_id,variant_id,physical_quantity,reserved_quantity)
		VALUES($4,$2,$5,$3,$1,'0','0')`,
		pgx.QueryExecModeSimpleProtocol,
		secondVariant, transferTestCompany, transferTestProduct, secondPosition, transferTestSource); err != nil {
		t.Fatal(err)
	}

	_, err := fixture.service.PostStockMovementOperation(fixture.ctx, StockMovementOperationInput{
		CompanyID: transferTestCompany, ActorUserID: transferTestUser, WarehouseID: transferTestSource, ProductID: transferTestProduct,
		MovementType: MovementManualAdjustment, Direction: DirectionOut, UnitCode: "ADET",
		ReasonCode: "SALES_DISPATCH", IdempotencyKey: "stock-operation-atomic-test",
		Lines: []StockMovementOperationLine{
			{VariantID: transferTestVariant, Quantity: "2"},
			{VariantID: secondVariant, Quantity: "1"},
		},
	})
	if !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("operation error=%v, want %v", err, ErrInsufficientStock)
	}
	physical, reserved, available := fixture.rawBalance(t, transferTestSource)
	assertQuantity(t, physical, "10", "physical after rolled back operation")
	assertQuantity(t, reserved, "0", "reserved after rolled back operation")
	assertQuantity(t, available, "10", "available after rolled back operation")
	var movementCount, lineCount int
	if err = fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM stock_movements WHERE company_id=$1 AND source_type='STOCK_MOVEMENT_OPERATION'`, transferTestCompany).Scan(&movementCount); err != nil {
		t.Fatal(err)
	}
	if err = fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM stock_movement_operation_lines WHERE company_id=$1`, transferTestCompany).Scan(&lineCount); err != nil {
		t.Fatal(err)
	}
	if movementCount != 0 || lineCount != 0 {
		t.Fatalf("rolled back operation left movement rows=%d line rows=%d", movementCount, lineCount)
	}
}

func TestStockMovementOperationPostsMultipleInboundVariantsIdempotently(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0", true)
	const (
		secondVariant  = "10000000-0000-4000-8000-000000000016"
		redOption      = "10000000-0000-4000-8000-000000000017"
		largeOption    = "10000000-0000-4000-8000-000000000018"
		secondPosition = "10000000-0000-4000-8000-000000000019"
	)
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO variant_definition_options(id,company_id,definition_id,code,name,short_code,sort_order)
		VALUES($1,$3,$4,'RED','Kırmızı','KRM',2),($2,$3,$5,'L','L','L',2);
		INSERT INTO product_variant_allowed_options(company_id,product_id,definition_id,option_id)
		VALUES($3,$6,$4,$1),($3,$6,$5,$2);
		INSERT INTO product_variants(id,company_id,product_id,variant_code,variant_signature)
		VALUES($7,$3,$6,'STK-TEST-RED-L',$4 || '=' || $1 || '|' || $5 || '=' || $2);
		INSERT INTO product_variant_values(company_id,variant_id,definition_id,option_id)
		VALUES($3,$7,$4,$1),($3,$7,$5,$2);
		INSERT INTO stock_positions(id,company_id,warehouse_id,product_id,variant_id,physical_quantity,reserved_quantity)
		VALUES($8,$3,$9,$6,$7,'0','0')`,
		pgx.QueryExecModeSimpleProtocol,
		redOption, largeOption, transferTestCompany, transferTestColorDef, transferTestSizeDef,
		transferTestProduct, secondVariant, secondPosition, transferTestSource); err != nil {
		t.Fatal(err)
	}

	input := StockMovementOperationInput{
		CompanyID: transferTestCompany, ActorUserID: transferTestUser, WarehouseID: transferTestSource, ProductID: transferTestProduct,
		MovementType: MovementManualAdjustment, Direction: DirectionIn, UnitCode: "ADET", Currency: "TRY",
		ReasonCode: "PURCHASE_RECEIPT", IdempotencyKey: "stock-operation-inbound-idempotency-test",
		Lines: []StockMovementOperationLine{
			{VariantID: transferTestVariant, Quantity: "5"},
			{VariantID: secondVariant, Quantity: "3"},
		},
	}
	first, err := fixture.service.PostStockMovementOperation(fixture.ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Lines) != 2 {
		t.Fatalf("operation lines=%d, want 2", len(first.Lines))
	}
	physical, reserved, available := fixture.rawBalance(t, transferTestSource)
	assertQuantity(t, physical, "18", "physical after inbound operation")
	assertQuantity(t, reserved, "0", "reserved after inbound operation")
	assertQuantity(t, available, "18", "available after inbound operation")

	replayed, err := fixture.service.PostStockMovementOperation(fixture.ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != first.ID || len(replayed.Lines) != 2 {
		t.Fatalf("replayed operation=%+v, want id=%s with 2 lines", replayed, first.ID)
	}
	physical, _, available = fixture.rawBalance(t, transferTestSource)
	assertQuantity(t, physical, "18", "physical after idempotent replay")
	assertQuantity(t, available, "18", "available after idempotent replay")

	var movementCount int
	if err = fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM stock_movements
		WHERE company_id=$1 AND source_type='STOCK_MOVEMENT_OPERATION' AND source_id=$2`,
		transferTestCompany, first.ID).Scan(&movementCount); err != nil {
		t.Fatal(err)
	}
	if movementCount != 2 {
		t.Fatalf("operation movements=%d, want 2", movementCount)
	}
}
