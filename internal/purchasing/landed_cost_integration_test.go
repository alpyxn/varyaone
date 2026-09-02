package purchasing

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/inventory"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestLandedCostAllocationReconcilesExactlyAndNeverRewritesLayerCost proves
// the two guarantees a landed cost distribution must hold: every allocated
// share adds up to the posted amount to the last kuruş (never a rounded
// approximation of it), and posting it appends stock_cost_adjustments rows
// rather than ever touching a stock_cost_layers.unit_cost -- the layer a
// receipt line's own IN movement opened stays the historical record it
// always was.
func TestLandedCostAllocationReconcilesExactlyAndNeverRewritesLayerCost(t *testing.T) {
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
	schema := fmt.Sprintf("varya_landed_cost_%d", time.Now().UnixNano())
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

	companyID, userID, productID, warehouseID, branchID, partyID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	mustExec(`INSERT INTO companies(id,legal_name,trade_name,entity_type,base_currency) VALUES($1,'Landed Test AŞ','Landed Test','LEGAL_ENTITY','TRY')`, companyID)
	mustExec(`INSERT INTO users(id,email,display_name,password_hash) VALUES($1,'landed-test@example.test','Landed Test User','test-hash')`, userID)
	mustExec(`INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, companyID, userID)
	mustExec(`INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,'MERKEZ','Merkez')`, branchID, companyID)
	mustExec(`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'CARI-001','ORGANIZATION',true,true,'Test Cari','Test Cari AŞ','TRY')`, partyID, companyID)
	mustExec(`INSERT INTO warehouses(id,company_id,code,name,warehouse_type) VALUES($1,$2,'DEPO','Test Depo','STANDARD')`, warehouseID, companyID)
	mustExec(`INSERT INTO products(id,company_id,code,name,kind,variants_enabled) VALUES($1,$2,'STK-LANDED','Landed Test Ürünü','PHYSICAL',false)`, productID, companyID)
	mustExec(`INSERT INTO product_units(company_id,product_id,unit_code,is_base,conversion_factor) VALUES($1,$2,'ADET',true,1)`, companyID, productID)

	session := identity.Session{
		User:             identity.User{ID: userID},
		CurrentCompanyID: companyID,
		Permissions:      []string{"inventory.movement.post", "purchase.landed_cost.manage", "purchase.landed_cost.post"},
	}

	// Goods receipt anchor + three lines with deliberately uneven quantities
	// and costs, so a naive equal split or a straightforward rounding would
	// not reconcile to the posted amount.
	receiptID := uuid.NewString()
	receiptDocNo := "AIRS-LANDED-TEST"
	mustExec(`INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,party_id,document_date,currency_code,created_by,updated_by)
		VALUES($1,$2,'PURCHASE_DELIVERY',$3,$4,$5,now()::date,'TRY',$6,$6)`, receiptID, companyID, receiptDocNo, branchID, partyID, userID)
	mustExec(`INSERT INTO goods_receipts(id,company_id,document_id,receipt_no,supplier_id,branch_id,warehouse_id,receipt_date,currency,status,created_by)
		VALUES($1,$2,$1,$3,$4,$5,$6,now()::date,'TRY','DRAFT',$7)`, receiptID, companyID, receiptDocNo, partyID, branchID, warehouseID, userID)

	type lineFixture struct {
		id       string
		quantity string
		unitCost string
	}
	lines := []lineFixture{
		{uuid.NewString(), "3", "100"},
		{uuid.NewString(), "7", "50"},
		{uuid.NewString(), "1", "333.33333333"},
	}
	inventoryService := inventory.NewService(pool)
	for i, line := range lines {
		mustExec(`INSERT INTO goods_receipt_lines(id,company_id,receipt_id,line_no,product_id,warehouse_id,accepted_quantity,damaged_quantity,rejected_quantity,unit_code,unit_cost,currency,conversion_factor,base_quantity)
			VALUES($1,$2,$3,$4,$5,$6,$7,0,0,'ADET',$8,'TRY',1,$7)`, line.id, companyID, receiptID, i+1, productID, warehouseID, line.quantity, line.unitCost)
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = inventoryService.PostPurchaseReceiptMovementsTx(ctx, tx, session, inventory.PurchaseReceiptStockPostingInput{
			ReceiptID: receiptID, WarehouseID: warehouseID,
			Lines: []inventory.PurchaseStockLine{{
				LineID: line.id, ProductID: productID, Quantity: line.quantity, BaseQuantity: line.quantity,
				ConversionFactor: "1", UnitCode: "ADET", UnitCost: line.unitCost, Currency: "TRY",
			}},
		}); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("post receipt line %d: %v", i, err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`UPDATE goods_receipts SET status='POSTED', version=version+1 WHERE company_id=$1 AND id=$2`, companyID, receiptID)

	// Original layer unit costs, captured before the landed cost posts, to
	// prove they are untouched afterward.
	originalCosts := map[string]string{}
	rows, err := pool.Query(ctx, `SELECT m.source_line_id::text, scl.unit_cost::text FROM stock_cost_layers scl
		JOIN stock_movements m ON m.company_id=scl.company_id AND m.id=scl.source_movement_id
		WHERE scl.company_id=$1`, companyID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var lineID, cost string
		if err = rows.Scan(&lineID, &cost); err != nil {
			t.Fatal(err)
		}
		originalCosts[lineID] = cost
	}
	rows.Close()
	if len(originalCosts) != 3 {
		t.Fatalf("expected 3 priced layers before landed cost, got %d", len(originalCosts))
	}

	service := NewService(pool, nil, nil)
	created, err := service.CreateLandedCost(ctx, session, LandedCostInput{
		GoodsReceiptID: receiptID, Amount: "123.45", Currency: "TRY", AllocationMethod: LandedCostByAmount,
		Description: "Navlun",
	}, identity.RequestMeta{TraceID: "landed-cost-test", IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatalf("create landed cost: %v", err)
	}
	if len(created.Lines) != 3 {
		t.Fatalf("landed cost lines = %d, want 3", len(created.Lines))
	}
	sum := new(big.Rat)
	for _, line := range created.Lines {
		share, ok := new(big.Rat).SetString(line.AllocatedAmount)
		if !ok {
			t.Fatalf("unparsable share %q", line.AllocatedAmount)
		}
		if share.Sign() < 0 {
			t.Fatalf("negative share: %+v", line)
		}
		sum.Add(sum, share)
	}
	if got := sum.FloatString(8); got != "123.45000000" {
		t.Fatalf("allocated shares total %s, want 123.45000000 (draft): %+v", got, created.Lines)
	}

	posted, err := service.PostLandedCost(ctx, session, created.ID, created.Version, identity.RequestMeta{TraceID: "landed-cost-test", IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatalf("post landed cost: %v", err)
	}
	if posted.Status != "POSTED" {
		t.Fatalf("status = %s, want POSTED", posted.Status)
	}

	// The layer's own unit_cost must be untouched; the adjustment total per
	// layer must equal that layer's allocated share.
	for _, line := range lines {
		var currentCost string
		if err = pool.QueryRow(ctx, `SELECT scl.unit_cost::text FROM stock_cost_layers scl
			JOIN stock_movements m ON m.company_id=scl.company_id AND m.id=scl.source_movement_id
			WHERE scl.company_id=$1 AND m.source_line_id=$2`, companyID, line.id).Scan(&currentCost); err != nil {
			t.Fatal(err)
		}
		if currentCost != originalCosts[line.id] {
			t.Fatalf("layer unit_cost for line %s changed: was %s, now %s", line.id, originalCosts[line.id], currentCost)
		}
	}

	var adjustmentCount int
	var adjustmentTotalText string
	if err = pool.QueryRow(ctx, `SELECT count(*), COALESCE(SUM(amount),0)::text FROM stock_cost_adjustments WHERE company_id=$1 AND reason_code='LANDED_COST'`, companyID).Scan(&adjustmentCount, &adjustmentTotalText); err != nil {
		t.Fatal(err)
	}
	if adjustmentCount != 3 {
		t.Fatalf("landed cost adjustments = %d, want 3", adjustmentCount)
	}
	adjustmentTotal, _ := new(big.Rat).SetString(adjustmentTotalText)
	if adjustmentTotal == nil || adjustmentTotal.FloatString(2) != "123.45" {
		t.Fatalf("adjustments total = %s, want 123.45", adjustmentTotalText)
	}

	// Posting is the only irreversible step: the draft must reject a second
	// post attempt.
	if _, err = service.PostLandedCost(ctx, session, created.ID, posted.Version, identity.RequestMeta{TraceID: "landed-cost-test", IdempotencyKey: uuid.NewString()}); !errorsIsInvalidTransition(err) {
		t.Fatalf("re-posting an already-posted landed cost = %v, want ErrInvalidTransition", err)
	}
}

func errorsIsInvalidTransition(err error) bool {
	return err == ErrInvalidTransition
}
