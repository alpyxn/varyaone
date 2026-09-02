package inventory

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestCostLedgerNeverDivergesFromPhysicalStock reproduces, end to end, the
// exact defect this migration set closes: a costless IN movement (the shape
// a sales return posts when its source sale's own cost cannot be resolved)
// used to open no cost layer at all. A later OUT movement then walked past
// that shortfall silently -- apply_stock_cost_layer()'s FOR loop simply had
// nothing to iterate and exited, so SUM(remaining_quantity) permanently fell
// behind the physical stock ledger with no record of it happening.
func TestCostLedgerNeverDivergesFromPhysicalStock(t *testing.T) {
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
	schema := fmt.Sprintf("varya_cost_ledger_%d", time.Now().UnixNano())
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
	// This applies every migration in this repository from an empty schema,
	// including the 000101/000102 trigger hardening and backfill -- a fresh,
	// real proof that they are not specific to any pre-existing database.
	if err = migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}

	companyID := uuid.NewString()
	userID := uuid.NewString()
	productID := uuid.NewString()
	warehouseID := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO companies(id,legal_name,trade_name,entity_type,base_currency)
		VALUES($1,'Maliyet Test AŞ','Maliyet Test','LEGAL_ENTITY','TRY')`, companyID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,email,display_name,password_hash)
		VALUES($1,'cost-ledger-test@example.test','Cost Ledger Test User','test-hash')`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, companyID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO products(id,company_id,code,name,kind,variants_enabled)
		VALUES($1,$2,'STK-COST','Maliyet Test Ürünü','PHYSICAL',false)`, productID, companyID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO product_units(company_id,product_id,unit_code,is_base,conversion_factor)
		VALUES($1,$2,'ADET',true,1)`, companyID, productID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO warehouses(id,company_id,code,name,warehouse_type)
		VALUES($1,$2,'DEPO','Test Depo','STANDARD')`, warehouseID, companyID); err != nil {
		t.Fatal(err)
	}
	branchID := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,'MERKEZ','Merkez')`, branchID, companyID); err != nil {
		t.Fatal(err)
	}
	partyID := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency)
		VALUES($1,$2,'CARI-001','ORGANIZATION',true,true,'Test Cari','Test Cari AŞ','TRY')`, partyID, companyID); err != nil {
		t.Fatal(err)
	}
	// The stock-posting effect claim table anchors every source document to a
	// documents(company_id,id) row (commercial_effect_claims_document_fk).
	// Production always creates this row in the same transaction as the
	// document header itself; this test posts stock directly, so it must
	// create the anchor row itself first.
	insertDocumentAnchor := func(documentID, documentTypeCode string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,party_id,document_date,currency_code,created_by,updated_by)
			VALUES($1,$2,$3,$4,$5,$6,now()::date,'TRY',$7,$7)`,
			documentID, companyID, documentTypeCode, documentTypeCode+"-"+documentID[:8], branchID, partyID, userID); err != nil {
			t.Fatalf("document anchor %s: %v", documentTypeCode, err)
		}
	}

	withTx := func(fn func(pgx.Tx) error) error {
		tx, txErr := pool.Begin(ctx)
		if txErr != nil {
			return txErr
		}
		if err := fn(tx); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		return tx.Commit(ctx)
	}

	service := NewService(pool)
	session := identity.Session{
		User:             identity.User{ID: userID},
		CurrentCompanyID: companyID,
		Permissions:      []string{"inventory.movement.post"},
	}

	// 1. A real, priced purchase receipt: 10 units @ 100 TRY opens one real
	//    (non-provisional) layer.
	receiptID := uuid.NewString()
	insertDocumentAnchor(receiptID, "PURCHASE_DELIVERY")
	if err = withTx(func(tx pgx.Tx) error {
		return service.PostPurchaseReceiptMovementsTx(ctx, tx, session, PurchaseReceiptStockPostingInput{
			ReceiptID:   receiptID,
			WarehouseID: warehouseID,
			Lines: []PurchaseStockLine{{
				LineID: uuid.NewString(), ProductID: productID, Quantity: "10", BaseQuantity: "10",
				ConversionFactor: "1", UnitCode: "ADET", UnitCost: "100", Currency: "TRY",
			}},
		})
	}); err != nil {
		t.Fatalf("purchase receipt: %v", err)
	}

	// 2. A sale dispatches 4 units; the trigger consumes FIFO from the real
	//    layer, leaving 6 remaining.
	firstSaleID := uuid.NewString()
	insertDocumentAnchor(firstSaleID, "SALES_INVOICE")
	if err = withTx(func(tx pgx.Tx) error {
		return service.PostCommercialStockMovementsTx(ctx, tx, session, CommercialStockPostingInput{
			DocumentID: firstSaleID, DocumentType: "SALES_INVOICE",
			Lines: []CommercialStockLine{{
				LineID: uuid.NewString(), ProductID: productID, WarehouseID: warehouseID,
				Quantity: "4", BaseQuantity: "4", ConversionFactor: "1", UnitCode: "ADET",
			}},
		})
	}); err != nil {
		t.Fatalf("sales dispatch: %v", err)
	}

	// 3. A return of 2 units posts with NO cost -- exactly what a sales
	//    return posts when its source sale's cost cannot be resolved. Before
	//    000101 this opened no layer at all for the returned quantity.
	returnID := uuid.NewString()
	insertDocumentAnchor(returnID, "SALES_RETURN_INVOICE")
	if err = withTx(func(tx pgx.Tx) error {
		return service.PostCommercialStockMovementsTx(ctx, tx, session, CommercialStockPostingInput{
			DocumentID: returnID, DocumentType: "SALES_RETURN_INVOICE",
			Lines: []CommercialStockLine{{
				LineID: uuid.NewString(), ProductID: productID, WarehouseID: warehouseID,
				Quantity: "2", BaseQuantity: "2", ConversionFactor: "1", UnitCode: "ADET",
			}},
		})
	}); err != nil {
		t.Fatalf("sales return: %v", err)
	}

	assertLedgerInSync := func(step string, wantPhysical string) {
		t.Helper()
		var physical, layered string
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0)::text
			  FROM stock_movements WHERE company_id=$1 AND warehouse_id=$2 AND product_id=$3`,
			companyID, warehouseID, productID).Scan(&physical); err != nil {
			t.Fatalf("%s: physical query: %v", step, err)
		}
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(remaining_quantity),0)::text
			  FROM stock_cost_layers WHERE company_id=$1 AND warehouse_id=$2 AND product_id=$3`,
			companyID, warehouseID, productID).Scan(&layered); err != nil {
			t.Fatalf("%s: layer query: %v", step, err)
		}
		if physical != wantPhysical {
			t.Fatalf("%s: physical stock = %s, want %s", step, physical, wantPhysical)
		}
		if physical != layered {
			t.Fatalf("%s: cost ledger diverged from physical stock: physical=%s layered=%s", step, physical, layered)
		}
	}
	// Physical: 10 - 4 + 2 = 8. This is the assertion that fails without the
	// provisional-layer fix: layered stayed at 6.
	assertLedgerInSync("after costless return", "8.00000000")

	// 4. A second sale dispatches the remaining 8 units. Under the old
	//    trigger this would only find 6 units of remaining layer quantity
	//    (the return's 2 units were never backed by a layer) and silently
	//    stop there -- the movement itself would still post, because the
	//    physical stock check operates on the ledger, not on the cost
	//    projection.
	secondSaleID := uuid.NewString()
	insertDocumentAnchor(secondSaleID, "SALES_INVOICE")
	if err = withTx(func(tx pgx.Tx) error {
		return service.PostCommercialStockMovementsTx(ctx, tx, session, CommercialStockPostingInput{
			DocumentID: secondSaleID, DocumentType: "SALES_INVOICE",
			Lines: []CommercialStockLine{{
				LineID: uuid.NewString(), ProductID: productID, WarehouseID: warehouseID,
				Quantity: "8", BaseQuantity: "8", ConversionFactor: "1", UnitCode: "ADET",
			}},
		})
	}); err != nil {
		t.Fatalf("second sales dispatch: %v", err)
	}
	// Physical: 8 - 8 = 0.
	assertLedgerInSync("after final dispatch", "0.00000000")

	var unpricedReceipts, unpricedConsumptions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM stock_cost_unpriced_events WHERE company_id=$1 AND event_type='UNPRICED_RECEIPT'`, companyID).Scan(&unpricedReceipts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM stock_cost_unpriced_events WHERE company_id=$1 AND event_type='UNPRICED_CONSUMPTION'`, companyID).Scan(&unpricedConsumptions); err != nil {
		t.Fatal(err)
	}
	if unpricedReceipts != 1 {
		t.Fatalf("unpriced receipt events = %d, want 1 (the costless return)", unpricedReceipts)
	}
	if unpricedConsumptions != 0 {
		t.Fatalf("unpriced consumption events = %d, want 0: the provisional layer must have covered the final dispatch", unpricedConsumptions)
	}
}
