package inventory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	transferTestCompany      = "10000000-0000-4000-8000-000000000001"
	transferTestUser         = "10000000-0000-4000-8000-000000000008"
	transferTestProduct      = "10000000-0000-4000-8000-000000000002"
	transferTestSource       = "10000000-0000-4000-8000-000000000003"
	transferTestDestination  = "10000000-0000-4000-8000-000000000004"
	transferTestTransit      = "10000000-0000-4000-8000-000000000005"
	transferTestVariant      = "10000000-0000-4000-8000-000000000007"
	transferTestColorDef     = "10000000-0000-4000-8000-000000000010"
	transferTestSizeDef      = "10000000-0000-4000-8000-000000000011"
	transferTestBlackOption  = "10000000-0000-4000-8000-000000000012"
	transferTestMediumOption = "10000000-0000-4000-8000-000000000013"
)

type transferStockFixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	service *Service
}

func newTransferStockFixture(t *testing.T, physical, reserved string, variantFixture ...bool) transferStockFixture {
	t.Helper()
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_transfer_stock_%d", time.Now().UnixNano())
	if _, err = base.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		base.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		base.Close()
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		base.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = base.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		base.Close()
	})
	if err = migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO companies(id,legal_name,trade_name,entity_type)
		VALUES($1,'Transfer Test AŞ','Transfer Test','LEGAL_ENTITY')`, transferTestCompany); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,email,display_name,password_hash)
		VALUES($1,'transfer-test@example.test','Transfer Test User','test-hash')`, transferTestUser); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO company_memberships(company_id,user_id)
		VALUES($1,$2)`, transferTestCompany, transferTestUser); err != nil {
		t.Fatal(err)
	}
	variantsEnabled := len(variantFixture) > 0 && variantFixture[0]
	if _, err = pool.Exec(ctx, `INSERT INTO products(id,company_id,code,name,kind,variants_enabled)
		VALUES($1,$2,'STK-TEST','Transfer Test Stoku','PHYSICAL',$3)`, transferTestProduct, transferTestCompany, variantsEnabled); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO product_units(company_id,product_id,unit_code,is_base) VALUES($1,$2,'ADET',true)`,
		transferTestCompany, transferTestProduct); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO warehouses(id,company_id,code,name,warehouse_type,is_transit,is_system)
		WITH transit_guard AS (SELECT set_config('varyaone.allow_system_transit','on',true))
		SELECT warehouse_rows.* FROM (VALUES
		($1::uuid,$4::uuid,'KAYNAK','Kaynak Depo','STANDARD',false,false),
		($2::uuid,$4::uuid,'HEDEF','Hedef Depo','STANDARD',false,false),
		($3::uuid,$4::uuid,'TRANSIT','Sistem Transit','TRANSIT',true,true)) AS warehouse_rows(id,company_id,code,name,warehouse_type,is_transit,is_system), transit_guard`,
		transferTestSource, transferTestDestination, transferTestTransit, transferTestCompany); err != nil {
		t.Fatal(err)
	}
	var variantID any
	if len(variantFixture) > 0 && variantFixture[0] {
		if _, err = pool.Exec(ctx, `INSERT INTO variant_definitions(id,company_id,code,name)
			VALUES($1,$3,'COLOR','Renk'),($2,$3,'SIZE','Beden')`, transferTestColorDef, transferTestSizeDef, transferTestCompany); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO variant_definition_options(id,company_id,definition_id,code,name,short_code,sort_order)
			VALUES($1,$3,$4,'BLACK','Siyah','SYH',1),($2,$3,$5,'M','M','M',1)`, transferTestBlackOption, transferTestMediumOption, transferTestCompany, transferTestColorDef, transferTestSizeDef); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO product_variant_definitions(company_id,product_id,definition_id,position)
			VALUES($1,$2,$3,1),($1,$2,$4,2)`, transferTestCompany, transferTestProduct, transferTestColorDef, transferTestSizeDef); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO product_variant_allowed_options(company_id,product_id,definition_id,option_id)
			VALUES($1,$2,$3,$4),($1,$2,$5,$6)`, transferTestCompany, transferTestProduct, transferTestColorDef, transferTestBlackOption, transferTestSizeDef, transferTestMediumOption); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO product_variants(id,company_id,product_id,variant_code,variant_signature)
			VALUES($1,$2,$3,'STK-TEST-BLK-M',$4)`, transferTestVariant, transferTestCompany, transferTestProduct, transferTestColorDef+"="+transferTestBlackOption+"|"+transferTestSizeDef+"="+transferTestMediumOption); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO product_variant_values(company_id,variant_id,definition_id,option_id)
			VALUES($1,$2,$3,$4),($1,$2,$5,$6)`, transferTestCompany, transferTestVariant, transferTestColorDef, transferTestBlackOption, transferTestSizeDef, transferTestMediumOption); err != nil {
			t.Fatal(err)
		}
		variantID = transferTestVariant
	}
	if _, err = pool.Exec(ctx, `INSERT INTO stock_positions(id,company_id,warehouse_id,product_id,variant_id,physical_quantity,reserved_quantity)
		VALUES('10000000-0000-4000-8000-000000000006',$1,$2,$3,$4,$5,$6)`,
		transferTestCompany, transferTestSource, transferTestProduct, variantID, physical, reserved); err != nil {
		t.Fatal(err)
	}
	return transferStockFixture{ctx: ctx, pool: pool, service: NewService(pool)}
}

func (f transferStockFixture) createInTransitTransfer(t *testing.T, number, quantity string) Transfer {
	t.Helper()
	item, err := f.service.CreateTransfer(f.ctx, TransferInput{
		CompanyID:              transferTestCompany,
		TransferNo:             number,
		TransferType:           TransferTypeWorkflow,
		SourceWarehouseID:      transferTestSource,
		DestinationWarehouseID: transferTestDestination,
		TransitWarehouseID:     transferTestTransit,
		RequestedBy:            transferTestUser,
		Lines: []TransferLineInput{{
			ProductID: transferTestProduct,
			Quantity:  quantity,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.State != TransferInTransit {
		t.Fatalf("created workflow transfer state=%s, want %s", item.State, TransferInTransit)
	}
	return item
}

func TestWorkflowTransferCreateWithVariant(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0", true)
	item, err := fixture.service.CreateTransfer(fixture.ctx, TransferInput{
		CompanyID:              transferTestCompany,
		TransferNo:             "TRF-WORKFLOW-VARIANT",
		TransferType:           TransferTypeWorkflow,
		SourceWarehouseID:      transferTestSource,
		DestinationWarehouseID: transferTestDestination,
		RequestedBy:            transferTestUser,
		IdempotencyKey:         "transfer-workflow-variant-create",
		Lines: []TransferLineInput{{
			ProductID: transferTestProduct,
			VariantID: transferTestVariant,
			Quantity:  "4",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.State != TransferInTransit {
		t.Fatalf("workflow state=%s, want %s", item.State, TransferInTransit)
	}
	if item.TransitWarehouseID != transferTestTransit || len(item.Lines) != 1 || item.Lines[0].VariantID == nil {
		t.Fatalf("workflow routing/variant=%+v", item)
	}
	physical, reserved, available := fixture.rawBalance(t, transferTestSource)
	assertQuantity(t, physical, "6", "source physical")
	assertQuantity(t, reserved, "0", "source reserved")
	assertQuantity(t, available, "6", "source available")
	transitPhysical, transitReserved, transitAvailable := fixture.rawBalance(t, transferTestTransit)
	assertQuantity(t, transitPhysical, "4", "transit physical")
	assertQuantity(t, transitReserved, "4", "transit reserved")
	assertQuantity(t, transitAvailable, "0", "transit available")
	var reservationState string
	if err = fixture.pool.QueryRow(fixture.ctx, `SELECT state FROM warehouse_transfer_reservations WHERE company_id=$1 AND transfer_line_id=$2`, transferTestCompany, item.Lines[0].ID).Scan(&reservationState); err != nil {
		t.Fatal(err)
	}
	if reservationState != "CONSUMED" {
		t.Fatalf("reservation state=%s, want CONSUMED", reservationState)
	}
}

func (f transferStockFixture) rawBalance(t *testing.T, warehouseID string) (physical, reserved, available string) {
	t.Helper()
	err := f.pool.QueryRow(f.ctx, `SELECT
		COALESCE(SUM(physical_quantity),0)::text,
		COALESCE(SUM(reserved_quantity),0)::text,
		COALESCE(SUM(available_quantity),0)::text
		FROM stock_positions
		WHERE company_id=$1 AND warehouse_id=$2 AND product_id=$3`,
		transferTestCompany, warehouseID, transferTestProduct).Scan(&physical, &reserved, &available)
	if err != nil {
		t.Fatal(err)
	}
	return physical, reserved, available
}

func assertQuantity(t *testing.T, got, want, label string) {
	t.Helper()
	if decimalCompare(got, want) != 0 {
		t.Fatalf("%s=%s, want %s", label, got, want)
	}
}

func TestTransferCannotConsumeReservedStock(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "3")
	_, err := fixture.service.CreateTransfer(fixture.ctx, TransferInput{
		CompanyID:              transferTestCompany,
		RequestedBy:            transferTestUser,
		TransferNo:             "TRF-RESERVED",
		TransferType:           TransferTypeWorkflow,
		SourceWarehouseID:      transferTestSource,
		DestinationWarehouseID: transferTestDestination,
		TransitWarehouseID:     transferTestTransit,
		Lines:                  []TransferLineInput{{ProductID: transferTestProduct, Quantity: "8"}},
	})
	if !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("reserved stock create error=%v, want %v", err, ErrInsufficientStock)
	}
	physical, reserved, available := fixture.rawBalance(t, transferTestSource)
	assertQuantity(t, physical, "10", "source physical")
	assertQuantity(t, reserved, "3", "source reserved")
	assertQuantity(t, available, "7", "source available")
	transit, _, _ := fixture.rawBalance(t, transferTestTransit)
	assertQuantity(t, transit, "0", "transit physical after rejected shipment")

	var transferCount int
	if err = fixture.pool.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM warehouse_transfers WHERE company_id=$1`, transferTestCompany).Scan(&transferCount); err != nil {
		t.Fatal(err)
	}
	if transferCount != 0 {
		t.Fatalf("rejected workflow create left transfers=%d, want 0", transferCount)
	}
}

func TestQuickTransferCreateReplayDoesNotShipTwice(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0")
	input := TransferInput{
		CompanyID:              transferTestCompany,
		RequestedBy:            transferTestUser,
		TransferNo:             "TRF-QUICK-REPLAY",
		TransferType:           TransferTypeQuick,
		SourceWarehouseID:      transferTestSource,
		DestinationWarehouseID: transferTestDestination,
		TransitWarehouseID:     transferTestTransit,
		IdempotencyKey:         "transfer-quick-create-replay",
		Lines: []TransferLineInput{{
			ProductID: transferTestProduct,
			Quantity:  "4",
		}},
	}
	first, err := fixture.service.CreateTransfer(fixture.ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.CreateTransfer(fixture.ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || second.State != TransferReceived {
		t.Fatalf("quick replay first=%s second=%s state=%s", first.ID, second.ID, second.State)
	}
	if first.ArrivalAt == nil || !first.ArrivalAt.Equal(first.CreatedAt) {
		t.Fatalf("quick transfer arrival_at=%v, want created_at=%v", first.ArrivalAt, first.CreatedAt)
	}
	_, _, sourceAvailable := fixture.rawBalance(t, transferTestSource)
	transitPhysical, _, _ := fixture.rawBalance(t, transferTestTransit)
	destinationPhysical, _, _ := fixture.rawBalance(t, transferTestDestination)
	assertQuantity(t, sourceAvailable, "6", "source available after quick replay")
	assertQuantity(t, transitPhysical, "0", "transit physical after quick replay")
	assertQuantity(t, destinationPhysical, "4", "destination physical after quick replay")
	var transferCount, movementCount int
	if err = fixture.pool.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM warehouse_transfers
		WHERE company_id=$1 AND transfer_no=$2`, transferTestCompany, input.TransferNo).Scan(&transferCount); err != nil {
		t.Fatal(err)
	}
	if err = fixture.pool.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM stock_movements
		WHERE company_id=$1 AND source_id=$2`, transferTestCompany, first.ID).Scan(&movementCount); err != nil {
		t.Fatal(err)
	}
	if transferCount != 1 || movementCount != 4 {
		t.Fatalf("quick replay transfers=%d movements=%d, want 1/4", transferCount, movementCount)
	}
	if _, err = fixture.service.CancelTransfer(fixture.ctx, transferTestCompany, first.ID, "Sevk iptali", transferTestUser, second.Version); !errors.Is(err, ErrWarehouseTransferInvalidState) {
		t.Fatalf("completed quick transfer cancellation error=%v, want %v", err, ErrWarehouseTransferInvalidState)
	}
}

func TestQuickTransferRejectsInsufficientStockAtomically(t *testing.T) {
	fixture := newTransferStockFixture(t, "2", "0")
	input := TransferInput{
		CompanyID:              transferTestCompany,
		RequestedBy:            transferTestUser,
		TransferNo:             "TRF-QUICK-INSUFFICIENT",
		TransferType:           TransferTypeQuick,
		SourceWarehouseID:      transferTestSource,
		DestinationWarehouseID: transferTestDestination,
		TransitWarehouseID:     transferTestTransit,
		Lines: []TransferLineInput{{
			ProductID: transferTestProduct,
			Quantity:  "3",
		}},
	}
	if _, err := fixture.service.CreateTransfer(fixture.ctx, input); !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("quick transfer error=%v, want %v", err, ErrInsufficientStock)
	}

	physical, reserved, available := fixture.rawBalance(t, transferTestSource)
	assertQuantity(t, physical, "2", "source physical after rejected quick transfer")
	assertQuantity(t, reserved, "0", "source reserved after rejected quick transfer")
	assertQuantity(t, available, "2", "source available after rejected quick transfer")
	transitPhysical, _, _ := fixture.rawBalance(t, transferTestTransit)
	destinationPhysical, _, _ := fixture.rawBalance(t, transferTestDestination)
	assertQuantity(t, transitPhysical, "0", "transit physical after rejected quick transfer")
	assertQuantity(t, destinationPhysical, "0", "destination physical after rejected quick transfer")

	var transferCount, movementCount int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM warehouse_transfers
		WHERE company_id=$1 AND transfer_no=$2`, transferTestCompany, input.TransferNo).Scan(&transferCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM stock_movements
		WHERE company_id=$1 AND source_type='WAREHOUSE_TRANSFER'`, transferTestCompany).Scan(&movementCount); err != nil {
		t.Fatal(err)
	}
	if transferCount != 0 || movementCount != 0 {
		t.Fatalf("rejected quick transfer left transfer=%d movements=%d, want 0/0", transferCount, movementCount)
	}
}

func TestTransferDatabaseRejectsLifecycleBypassRerouteAndDeactivation(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0")
	transfer, err := fixture.service.CreateTransfer(fixture.ctx, TransferInput{
		CompanyID:              transferTestCompany,
		RequestedBy:            transferTestUser,
		TransferNo:             "TRF-GUARDS",
		TransferType:           TransferTypeWorkflow,
		SourceWarehouseID:      transferTestSource,
		DestinationWarehouseID: transferTestDestination,
		TransitWarehouseID:     transferTestTransit,
		IdempotencyKey:         "transfer-guard-create",
		Lines: []TransferLineInput{{
			ProductID: transferTestProduct,
			Quantity:  "4",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = fixture.service.PostMovement(fixture.ctx, MovementInput{
		CompanyID:         transferTestCompany,
		ActorUserID:       transferTestUser,
		WarehouseID:       transferTestDestination,
		ProductID:         transferTestProduct,
		MovementType:      MovementTransferIn,
		Direction:         DirectionIn,
		Quantity:          "1",
		ReasonCode:        "TRANSFER",
		ReasonDescription: "Yaşam döngüsü bypass denemesi",
		SourceType:        "WAREHOUSE_TRANSFER",
		SourceID:          transfer.ID,
		SourceLineID:      transfer.Lines[0].ID,
		IdempotencyKey:    "transfer-bypass-in",
	}); !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("generic transfer inbound bypass error=%v, want %v", err, ErrInsufficientStock)
	}

	inactive := false
	if _, err = fixture.service.UpdateWarehouse(fixture.ctx, transferTestCompany, transferTestSource, 1, WarehouseUpdateInput{
		Code:     "KAYNAK",
		Name:     "Kaynak Depo",
		Type:     WarehouseStandard,
		IsActive: &inactive,
	}, transferTestUser); !errors.Is(err, ErrWarehouseHasOpenTransfer) {
		t.Fatalf("open-transfer warehouse deactivation error=%v, want %v", err, ErrWarehouseHasOpenTransfer)
	}

	if _, err = fixture.pool.Exec(fixture.ctx, `UPDATE warehouse_transfer_lines
		SET quantity=quantity+1 WHERE company_id=$1 AND transfer_id=$2`, transferTestCompany, transfer.ID); err == nil {
		t.Fatal("shipped transfer line quantity was mutable")
	}
	if _, err = fixture.pool.Exec(fixture.ctx, `UPDATE warehouse_transfers
		SET transfer_type='QUICK' WHERE company_id=$1 AND id=$2`, transferTestCompany, transfer.ID); err == nil {
		t.Fatal("shipped transfer routing/type was mutable")
	}
}

func TestTransferShipmentAndDeliveryConserveAvailableStock(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0")
	shipped := fixture.createInTransitTransfer(t, "TRF-LIFECYCLE", "4")
	var err error
	_, _, sourceAvailable := fixture.rawBalance(t, transferTestSource)
	transitPhysical, _, _ := fixture.rawBalance(t, transferTestTransit)
	_, _, destinationAvailable := fixture.rawBalance(t, transferTestDestination)
	assertQuantity(t, sourceAvailable, "6", "source available while shipped")
	assertQuantity(t, transitPhysical, "4", "transit physical while shipped")
	assertQuantity(t, destinationAvailable, "0", "destination available before delivery")

	transitPosition, err := fixture.service.GetPosition(fixture.ctx, transferTestCompany, transferTestTransit, transferTestProduct, "", "", "", "", transferTestUser)
	if err != nil {
		t.Fatal(err)
	}
	assertQuantity(t, transitPosition.AvailableQuantity, "0", "ordinary transit availability")

	received, err := fixture.service.ReceiveTransferWithKey(fixture.ctx, transferTestCompany, shipped.ID, transferTestUser, shipped.Version, nil, "transfer-lifecycle-receive")
	if err != nil {
		t.Fatal(err)
	}
	if received.State != TransferReceived {
		t.Fatalf("received state=%s, want %s", received.State, TransferReceived)
	}
	_, _, sourceAvailable = fixture.rawBalance(t, transferTestSource)
	transitPhysical, _, _ = fixture.rawBalance(t, transferTestTransit)
	_, _, destinationAvailable = fixture.rawBalance(t, transferTestDestination)
	assertQuantity(t, sourceAvailable, "6", "source available after delivery")
	assertQuantity(t, transitPhysical, "0", "transit physical after delivery")
	assertQuantity(t, destinationAvailable, "4", "destination available after delivery")

	if _, err = fixture.service.ReceiveTransferWithKey(fixture.ctx, transferTestCompany, shipped.ID, transferTestUser, shipped.Version, nil, "transfer-lifecycle-receive"); err != nil {
		t.Fatalf("idempotent receive replay failed: %v", err)
	}
	var movementCount, linkedCount int
	if err = fixture.pool.QueryRow(fixture.ctx, `SELECT COUNT(*),COUNT(*) FILTER (
		WHERE transfer_id=$2 AND transfer_line_id=$3)
		FROM stock_movements WHERE company_id=$1 AND source_id=$2`,
		transferTestCompany, shipped.ID, shipped.Lines[0].ID).Scan(&movementCount, &linkedCount); err != nil {
		t.Fatal(err)
	}
	if movementCount != 4 || linkedCount != 4 {
		t.Fatalf("movement provenance count=%d linked=%d, want 4/4", movementCount, linkedCount)
	}
}

func TestTransferCancellationReturnsOutstandingStockOnce(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0")
	shipped := fixture.createInTransitTransfer(t, "TRF-CANCEL", "4")
	var err error
	cancelled, err := fixture.service.CancelTransfer(fixture.ctx, transferTestCompany, shipped.ID, "Sevk iptali", transferTestUser, shipped.Version)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.State != TransferCancelled {
		t.Fatalf("cancelled state=%s, want %s", cancelled.State, TransferCancelled)
	}
	if _, err = fixture.service.CancelTransfer(fixture.ctx, transferTestCompany, shipped.ID, "Sevk iptali", transferTestUser, shipped.Version); err != nil {
		t.Fatalf("idempotent cancellation replay failed: %v", err)
	}
	_, _, sourceAvailable := fixture.rawBalance(t, transferTestSource)
	transitPhysical, _, _ := fixture.rawBalance(t, transferTestTransit)
	_, _, destinationAvailable := fixture.rawBalance(t, transferTestDestination)
	assertQuantity(t, sourceAvailable, "10", "source available after cancellation")
	assertQuantity(t, transitPhysical, "0", "transit physical after cancellation")
	assertQuantity(t, destinationAvailable, "0", "destination available after cancellation")
	var movementCount int
	if err = fixture.pool.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM stock_movements WHERE company_id=$1 AND source_id=$2`, transferTestCompany, shipped.ID).Scan(&movementCount); err != nil {
		t.Fatal(err)
	}
	if movementCount != 4 {
		t.Fatalf("cancelled transfer movement count=%d, want 4", movementCount)
	}
}

func TestConcurrentCreateAndShipCannotConsumeSameAvailableStock(t *testing.T) {
	fixture := newTransferStockFixture(t, "10", "0")
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, number := range []string{"TRF-RACE-1", "TRF-RACE-2"} {
		group.Add(1)
		go func(transferNo string) {
			defer group.Done()
			_, err := fixture.service.CreateTransfer(fixture.ctx, TransferInput{
				CompanyID:              transferTestCompany,
				RequestedBy:            transferTestUser,
				TransferNo:             transferNo,
				TransferType:           TransferTypeWorkflow,
				SourceWarehouseID:      transferTestSource,
				DestinationWarehouseID: transferTestDestination,
				TransitWarehouseID:     transferTestTransit,
				Lines:                  []TransferLineInput{{ProductID: transferTestProduct, Quantity: "7"}},
			})
			results <- err
		}(number)
	}
	group.Wait()
	close(results)
	succeeded, insufficient := 0, 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrInsufficientStock):
			insufficient++
		default:
			t.Fatalf("unexpected concurrent create-and-ship error: %v", err)
		}
	}
	if succeeded != 1 || insufficient != 1 {
		t.Fatalf("concurrent create-and-ship result success=%d insufficient=%d, want 1/1", succeeded, insufficient)
	}
	_, reserved, sourceAvailable := fixture.rawBalance(t, transferTestSource)
	transitPhysical, _, _ := fixture.rawBalance(t, transferTestTransit)
	assertQuantity(t, reserved, "0", "source reserved after concurrent create-and-ship")
	assertQuantity(t, sourceAvailable, "3", "source available after concurrent create-and-ship")
	assertQuantity(t, transitPhysical, "7", "transit physical after concurrent create-and-ship")
}
