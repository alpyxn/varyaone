package purchasing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/inventory"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// receiptStockAdapter is the minimal composition-root stitch the purchasing
// service needs for the goods-receipt post/cancel path: inbound movements on
// finalize, and their reversal on cancel.
type receiptStockAdapter struct{ inv *inventory.Service }

func (a receiptStockAdapter) PostPurchaseReceiptMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, input inventory.PurchaseReceiptStockPostingInput) error {
	return a.inv.PostPurchaseReceiptMovementsTx(ctx, tx, session, input)
}

func (a receiptStockAdapter) PostPurchaseReturnMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, input inventory.PurchaseReturnStockPostingInput) error {
	return a.inv.PostPurchaseReturnMovementsTx(ctx, tx, session, input)
}

func (a receiptStockAdapter) ReversePurchaseMovementsTx(ctx context.Context, tx pgx.Tx, session identity.Session, input inventory.PurchaseStockReversalInput) error {
	return a.inv.ReverseInvoiceMovementsTx(ctx, tx, session, inventory.InvoiceStockReversalInput{
		DocumentID: input.DocumentID, DocumentType: input.SourceType, WarehouseID: input.WarehouseID,
		ReversalKey: input.ReversalKey, Reason: input.Reason,
	})
}

// TestCancellingGoodsReceiptRollsBackOrderProjection proves the fix for the
// cancel/projection gap: a cancelled goods receipt returns the source order's
// received_quantity to zero, drops its FULFILLMENT allocations, moves the
// order back off FULFILLED, and frees the quantity for a fresh receipt.
func TestCancellingGoodsReceiptRollsBackOrderProjection(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_cancel_rollback_%d", time.Now().UnixNano())
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

	companyID, userID, productID, warehouseID, branchID, supplierID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	mustExec(`INSERT INTO companies(id,legal_name,trade_name,entity_type,base_currency) VALUES($1,'Cancel Test AŞ','Cancel Test','LEGAL_ENTITY','TRY')`, companyID)
	mustExec(`INSERT INTO users(id,email,display_name,password_hash) VALUES($1,'cancel-test@example.test','Cancel Test User','test-hash')`, userID)
	mustExec(`INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, companyID, userID)
	mustExec(`INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,'MERKEZ','Merkez')`, branchID, companyID)
	mustExec(`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'TED-001','ORGANIZATION',false,true,'Test Tedarikçi','Test Tedarikçi AŞ','TRY')`, supplierID, companyID)
	mustExec(`INSERT INTO warehouses(id,company_id,code,name,warehouse_type,branch_id) VALUES($1,$2,'DEPO','Test Depo','STANDARD',$3)`, warehouseID, companyID, branchID)
	mustExec(`INSERT INTO products(id,company_id,code,name,kind,variants_enabled) VALUES($1,$2,'STK-CANCEL','Cancel Test Ürünü','PHYSICAL',false)`, productID, companyID)
	mustExec(`INSERT INTO product_units(company_id,product_id,unit_code,is_base,conversion_factor) VALUES($1,$2,'ADET',true,1)`, companyID, productID)

	session := identity.Session{
		User:             identity.User{ID: userID},
		CurrentCompanyID: companyID,
		Permissions: []string{
			"inventory.movement.post", "inventory.movement.reverse",
			"purchase.order.manage", "purchase.order.read",
			"purchase.receipt.post", "purchase.receipt.read",
		},
	}

	service := NewService(pool, receiptStockAdapter{inv: inventory.NewService(pool)}, nil)
	meta := func() identity.RequestMeta {
		return identity.RequestMeta{TraceID: "cancel-rollback-test", IdempotencyKey: uuid.NewString()}
	}

	order, err := service.CreatePurchaseOrder(ctx, session, PurchaseOrderInput{
		SupplierID: supplierID, BranchID: branchID, WarehouseID: warehouseID,
		OrderDate: time.Now().UTC().Truncate(24 * time.Hour), Currency: "TRY",
		Lines: []PurchaseOrderLine{{
			LineType: "PRODUCT", ProductID: productID, ProductNameSnapshot: "Cancel Test Ürünü",
			WarehouseID: warehouseID, UnitCode: "ADET", OrderedQuantity: "10", UnitPrice: "100", Currency: "TRY",
		}},
	}, meta())
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	if order, err = service.ConfirmPurchaseOrder(ctx, session, order.ID, order.Version, meta()); err != nil {
		t.Fatalf("confirm order: %v", err)
	}
	orderLineID := order.Lines[0].ID

	receipt, err := service.CreateGoodsReceipt(ctx, session, GoodsReceiptInput{
		PurchaseOrderID: order.ID, SupplierID: supplierID, BranchID: branchID, WarehouseID: warehouseID,
		ReceiptDate: time.Now().UTC().Truncate(24 * time.Hour), Currency: "TRY",
		Lines: []GoodsReceiptLine{{
			PurchaseOrderLineID: &orderLineID, ProductID: productID, WarehouseID: warehouseID,
			AcceptedQuantity: "10", DamagedQuantity: "0", RejectedQuantity: "0",
			UnitCode: "ADET", UnitCost: "100", Currency: "TRY", ConversionFactor: "1",
		}},
	}, meta())
	if err != nil {
		t.Fatalf("create receipt: %v", err)
	}
	if receipt, err = service.FinalizeGoodsReceipt(ctx, session, receipt.ID, receipt.Version, meta()); err != nil {
		t.Fatalf("finalize receipt: %v", err)
	}

	assertOrderLine := func(when, wantReceived, wantStatus string) {
		t.Helper()
		var received, status string
		if err := pool.QueryRow(ctx, `SELECT pol.received_quantity::text, po.status
			FROM purchase_order_lines pol JOIN purchase_orders po ON po.company_id=pol.company_id AND po.id=pol.order_id
			WHERE pol.company_id=$1 AND pol.id=$2`, companyID, orderLineID).Scan(&received, &status); err != nil {
			t.Fatal(err)
		}
		if received != wantReceived || status != wantStatus {
			t.Fatalf("%s: order line received=%s status=%s, want received=%s status=%s", when, received, status, wantReceived, wantStatus)
		}
	}
	assertFulfillmentAllocations := func(when string, want int) {
		t.Helper()
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM commercial_line_allocations
			WHERE company_id=$1 AND source_line_id=$2 AND allocation_type='FULFILLMENT'`, companyID, orderLineID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%s: FULFILLMENT allocations = %d, want %d", when, count, want)
		}
	}

	assertOrderLine("after finalize", "10.00000000", "FULFILLED")
	assertFulfillmentAllocations("after finalize", 1)

	if _, err = service.CancelGoodsReceipt(ctx, session, receipt.ID, receipt.Version, "Yanlış mal kabul", meta()); err != nil {
		t.Fatalf("cancel receipt: %v", err)
	}

	assertOrderLine("after cancel", "0.00000000", "CONFIRMED")
	assertFulfillmentAllocations("after cancel", 0)

	// The freed quantity must be receivable again.
	receipt2, err := service.CreateGoodsReceipt(ctx, session, GoodsReceiptInput{
		PurchaseOrderID: order.ID, SupplierID: supplierID, BranchID: branchID, WarehouseID: warehouseID,
		ReceiptDate: time.Now().UTC().Truncate(24 * time.Hour), Currency: "TRY",
		Lines: []GoodsReceiptLine{{
			PurchaseOrderLineID: &orderLineID, ProductID: productID, WarehouseID: warehouseID,
			AcceptedQuantity: "4", DamagedQuantity: "0", RejectedQuantity: "0",
			UnitCode: "ADET", UnitCost: "100", Currency: "TRY", ConversionFactor: "1",
		}},
	}, meta())
	if err != nil {
		t.Fatalf("create second receipt: %v", err)
	}
	if _, err = service.FinalizeGoodsReceipt(ctx, session, receipt2.ID, receipt2.Version, meta()); err != nil {
		t.Fatalf("finalize second receipt: %v", err)
	}
	assertOrderLine("after re-receive", "4.00000000", "PARTIALLY_FULFILLED")
}

// TestGoodsReceiptDamagedQuantityDoesNotAdvanceProjections proves the
// accepted-only projection fix: damaged/rejected units are recorded on the
// receipt line but never advance received_quantity, never become stock, and
// never inflate the line's commercial registry base quantity (which caps
// invoicing and returns).
func TestGoodsReceiptDamagedQuantityDoesNotAdvanceProjections(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_gr_damaged_%d", time.Now().UnixNano())
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

	companyID, userID, productID, warehouseID, branchID, supplierID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	mustExec(`INSERT INTO companies(id,legal_name,trade_name,entity_type,base_currency) VALUES($1,'Damaged Test AŞ','Damaged Test','LEGAL_ENTITY','TRY')`, companyID)
	mustExec(`INSERT INTO users(id,email,display_name,password_hash) VALUES($1,'damaged-test@example.test','Damaged Test User','test-hash')`, userID)
	mustExec(`INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, companyID, userID)
	mustExec(`INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,'MERKEZ','Merkez')`, branchID, companyID)
	mustExec(`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'TED-001','ORGANIZATION',false,true,'Test Tedarikçi','Test Tedarikçi AŞ','TRY')`, supplierID, companyID)
	mustExec(`INSERT INTO warehouses(id,company_id,code,name,warehouse_type,branch_id) VALUES($1,$2,'DEPO','Test Depo','STANDARD',$3)`, warehouseID, companyID, branchID)
	mustExec(`INSERT INTO products(id,company_id,code,name,kind,variants_enabled) VALUES($1,$2,'STK-DMG','Damaged Test Ürünü','PHYSICAL',false)`, productID, companyID)
	mustExec(`INSERT INTO product_units(company_id,product_id,unit_code,is_base,conversion_factor) VALUES($1,$2,'ADET',true,1)`, companyID, productID)

	session := identity.Session{
		User:             identity.User{ID: userID},
		CurrentCompanyID: companyID,
		Permissions: []string{
			"inventory.movement.post",
			"purchase.order.manage", "purchase.order.read",
			"purchase.receipt.post", "purchase.receipt.read",
		},
	}
	service := NewService(pool, receiptStockAdapter{inv: inventory.NewService(pool)}, nil)
	meta := func() identity.RequestMeta {
		return identity.RequestMeta{TraceID: "damaged-test", IdempotencyKey: uuid.NewString()}
	}

	order, err := service.CreatePurchaseOrder(ctx, session, PurchaseOrderInput{
		SupplierID: supplierID, BranchID: branchID, WarehouseID: warehouseID,
		OrderDate: time.Now().UTC().Truncate(24 * time.Hour), Currency: "TRY",
		Lines: []PurchaseOrderLine{{
			LineType: "PRODUCT", ProductID: productID, ProductNameSnapshot: "Damaged Test Ürünü",
			WarehouseID: warehouseID, UnitCode: "ADET", OrderedQuantity: "10", UnitPrice: "100", Currency: "TRY",
		}},
	}, meta())
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	if order, err = service.ConfirmPurchaseOrder(ctx, session, order.ID, order.Version, meta()); err != nil {
		t.Fatalf("confirm order: %v", err)
	}
	orderLineID := order.Lines[0].ID

	receipt, err := service.CreateGoodsReceipt(ctx, session, GoodsReceiptInput{
		PurchaseOrderID: order.ID, SupplierID: supplierID, BranchID: branchID, WarehouseID: warehouseID,
		ReceiptDate: time.Now().UTC().Truncate(24 * time.Hour), Currency: "TRY",
		Lines: []GoodsReceiptLine{{
			PurchaseOrderLineID: &orderLineID, ProductID: productID, WarehouseID: warehouseID,
			AcceptedQuantity: "8", DamagedQuantity: "2", RejectedQuantity: "0",
			UnitCode: "ADET", UnitCost: "100", Currency: "TRY", ConversionFactor: "1",
		}},
	}, meta())
	if err != nil {
		t.Fatalf("create receipt: %v", err)
	}
	if receipt, err = service.FinalizeGoodsReceipt(ctx, session, receipt.ID, receipt.Version, meta()); err != nil {
		t.Fatalf("finalize receipt: %v", err)
	}

	var received, status string
	if err = pool.QueryRow(ctx, `SELECT pol.received_quantity::text, po.status
		FROM purchase_order_lines pol JOIN purchase_orders po ON po.company_id=pol.company_id AND po.id=pol.order_id
		WHERE pol.company_id=$1 AND pol.id=$2`, companyID, orderLineID).Scan(&received, &status); err != nil {
		t.Fatal(err)
	}
	if received != "8.00000000" {
		t.Fatalf("received_quantity=%s, want 8 (accepted only)", received)
	}
	if status != "PARTIALLY_FULFILLED" {
		t.Fatalf("order status=%s, want PARTIALLY_FULFILLED", status)
	}

	var onHand string
	if err = pool.QueryRow(ctx, `SELECT COALESCE(SUM(physical_quantity),0)::text FROM stock_positions WHERE company_id=$1 AND product_id=$2`, companyID, productID).Scan(&onHand); err != nil {
		t.Fatal(err)
	}
	if onHand != "8.00000000" {
		t.Fatalf("on-hand stock=%s, want 8", onHand)
	}

	var registryBase string
	if err = pool.QueryRow(ctx, `SELECT r.base_quantity::text FROM commercial_line_registry r
		WHERE r.company_id=$1 AND r.document_id=$2 AND r.aggregate_type='GOODS_RECEIPT'`, companyID, receipt.ID).Scan(&registryBase); err != nil {
		t.Fatal(err)
	}
	if registryBase != "8.00000000" {
		t.Fatalf("goods receipt registry base_quantity=%s, want 8 (caps invoicing/returns)", registryBase)
	}
}

// TestGoodsReceiptDraftPostPermissionSeparation proves the segregation of
// duties: purchase.receipt.draft can prepare a goods receipt but not finalize
// it; purchase.receipt.post can do both.
func TestGoodsReceiptDraftPostPermissionSeparation(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_gr_perm_%d", time.Now().UnixNano())
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

	companyID, userID, productID, warehouseID, branchID, supplierID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	mustExec(`INSERT INTO companies(id,legal_name,trade_name,entity_type,base_currency) VALUES($1,'Perm Test AŞ','Perm Test','LEGAL_ENTITY','TRY')`, companyID)
	mustExec(`INSERT INTO users(id,email,display_name,password_hash) VALUES($1,'perm-test@example.test','Perm Test User','test-hash')`, userID)
	mustExec(`INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, companyID, userID)
	mustExec(`INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,'MERKEZ','Merkez')`, branchID, companyID)
	mustExec(`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'TED-001','ORGANIZATION',false,true,'Test Tedarikçi','Test Tedarikçi AŞ','TRY')`, supplierID, companyID)
	mustExec(`INSERT INTO warehouses(id,company_id,code,name,warehouse_type,branch_id) VALUES($1,$2,'DEPO','Test Depo','STANDARD',$3)`, warehouseID, companyID, branchID)
	mustExec(`INSERT INTO products(id,company_id,code,name,kind,variants_enabled) VALUES($1,$2,'STK-PERM','Perm Test Ürünü','PHYSICAL',false)`, productID, companyID)
	mustExec(`INSERT INTO product_units(company_id,product_id,unit_code,is_base,conversion_factor) VALUES($1,$2,'ADET',true,1)`, companyID, productID)

	service := NewService(pool, receiptStockAdapter{inv: inventory.NewService(pool)}, nil)
	meta := func() identity.RequestMeta {
		return identity.RequestMeta{TraceID: "perm-test", IdempotencyKey: uuid.NewString()}
	}
	sessionWith := func(perms ...string) identity.Session {
		return identity.Session{User: identity.User{ID: userID}, CurrentCompanyID: companyID, Permissions: perms}
	}

	receiptInput := func() GoodsReceiptInput {
		return GoodsReceiptInput{
			SupplierID: supplierID, BranchID: branchID, WarehouseID: warehouseID,
			ReceiptDate: time.Now().UTC().Truncate(24 * time.Hour), Currency: "TRY",
			Lines: []GoodsReceiptLine{{
				ProductID: productID, WarehouseID: warehouseID,
				AcceptedQuantity: "5", DamagedQuantity: "0", RejectedQuantity: "0",
				UnitCode: "ADET", UnitCost: "100", Currency: "TRY", ConversionFactor: "1",
			}},
		}
	}

	// A preparer can draft but not finalize.
	preparer := sessionWith("purchase.receipt.draft", "inventory.movement.post")
	draft, err := service.CreateGoodsReceipt(ctx, preparer, receiptInput(), meta())
	if err != nil {
		t.Fatalf("preparer create: %v", err)
	}
	if _, err = service.FinalizeGoodsReceipt(ctx, preparer, draft.ID, draft.Version, meta()); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("preparer finalize err=%v, want ErrForbidden", err)
	}

	// A poster can both draft and finalize.
	poster := sessionWith("purchase.receipt.post", "inventory.movement.post")
	draft2, err := service.CreateGoodsReceipt(ctx, poster, receiptInput(), meta())
	if err != nil {
		t.Fatalf("poster create: %v", err)
	}
	if _, err = service.FinalizeGoodsReceipt(ctx, poster, draft2.ID, draft2.Version, meta()); err != nil {
		t.Fatalf("poster finalize: %v", err)
	}
}
