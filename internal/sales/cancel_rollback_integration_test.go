package sales

import (
	"context"
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

// salesStockAdapter is the minimal composition-root stitch the sales commercial
// service needs for the order -> dispatch -> cancel path.
type salesStockAdapter struct{ inv *inventory.Service }

func (a salesStockAdapter) PostInvoiceTx(ctx context.Context, tx pgx.Tx, session identity.Session, input StockPostingInput) error {
	return nil // not exercised by the dispatch path
}

func (a salesStockAdapter) PostCommercialStockTx(ctx context.Context, tx pgx.Tx, session identity.Session, input CommercialStockPostingInput) error {
	lines := make([]inventory.CommercialStockLine, 0, len(input.Lines))
	for _, l := range input.Lines {
		lines = append(lines, inventory.CommercialStockLine{LineID: l.LineID, ProductID: l.ProductID, VariantID: l.VariantID, WarehouseID: l.WarehouseID, Quantity: l.Quantity, BaseQuantity: l.BaseQuantity, ConversionFactor: l.ConversionFactor, UnitCode: l.UnitCode, UnitCost: l.UnitCost, Currency: l.Currency})
	}
	return a.inv.PostCommercialStockMovementsTx(ctx, tx, session, inventory.CommercialStockPostingInput{DocumentID: input.DocumentID, DocumentType: input.DocumentType, Lines: lines})
}

func (a salesStockAdapter) ReverseCommercialStockTx(ctx context.Context, tx pgx.Tx, session identity.Session, input CommercialStockReversalInput) error {
	return a.inv.ReverseInvoiceMovementsTx(ctx, tx, session, inventory.InvoiceStockReversalInput{DocumentID: input.DocumentID, DocumentType: input.DocumentType, ReversalKey: input.ReversalKey, Reason: input.Reason})
}

func (a salesStockAdapter) ReserveSalesOrderTx(ctx context.Context, tx pgx.Tx, session identity.Session, input SalesReservationInput) error {
	lines := make([]inventory.SalesReservationLine, 0, len(input.Lines))
	for _, l := range input.Lines {
		lines = append(lines, inventory.SalesReservationLine{OrderLineID: l.OrderLineID, ProductID: l.ProductID, VariantID: l.VariantID, WarehouseID: l.WarehouseID, Quantity: l.Quantity})
	}
	return a.inv.ReserveSalesOrderTx(ctx, tx, session, inventory.SalesReservationInput{OrderID: input.OrderID, Lines: lines})
}

func (a salesStockAdapter) toInvConsumptions(lines []SalesReservationConsumption) []inventory.SalesReservationConsumption {
	out := make([]inventory.SalesReservationConsumption, 0, len(lines))
	for _, l := range lines {
		out = append(out, inventory.SalesReservationConsumption{OrderID: l.OrderID, OrderLineID: l.OrderLineID, ProductID: l.ProductID, VariantID: l.VariantID, WarehouseID: l.WarehouseID, Quantity: l.Quantity})
	}
	return out
}

func (a salesStockAdapter) ConsumeSalesOrderReservationsTx(ctx context.Context, tx pgx.Tx, session identity.Session, lines []SalesReservationConsumption) error {
	return a.inv.ConsumeSalesOrderReservationsTx(ctx, tx, session, a.toInvConsumptions(lines))
}

func (a salesStockAdapter) ReleaseSalesOrderReservationsTx(ctx context.Context, tx pgx.Tx, session identity.Session, orderID string) error {
	return a.inv.ReleaseSalesOrderReservationsTx(ctx, tx, session, orderID)
}

func (a salesStockAdapter) RestoreSalesOrderReservationsTx(ctx context.Context, tx pgx.Tx, session identity.Session, lines []SalesReservationConsumption) error {
	return a.inv.RestoreSalesOrderReservationsTx(ctx, tx, session, a.toInvConsumptions(lines))
}

// TestCancellingSalesDispatchRollsBackOrderFulfillment proves the sales side of
// the cancel/projection fix: a cancelled dispatch drops its FULFILLMENT
// allocations, restores the reservations it consumed, moves the order back off
// FULFILLED, and lets the order be dispatched again.
func TestCancellingSalesDispatchRollsBackOrderFulfillment(t *testing.T) {
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
	schema := fmt.Sprintf("varya_sales_cancel_%d", time.Now().UnixNano())
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
	mustExec(`INSERT INTO companies(id,legal_name,trade_name,entity_type,base_currency) VALUES($1,'Sales Cancel AŞ','Sales Cancel','LEGAL_ENTITY','TRY')`, companyID)
	mustExec(`INSERT INTO users(id,email,display_name,password_hash) VALUES($1,'sales-cancel@example.test','Sales Cancel User','test-hash')`, userID)
	mustExec(`INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, companyID, userID)
	mustExec(`INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,'MERKEZ','Merkez')`, branchID, companyID)
	mustExec(`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'CARI-001','ORGANIZATION',true,false,'Test Müşteri','Test Müşteri AŞ','TRY')`, partyID, companyID)
	mustExec(`INSERT INTO warehouses(id,company_id,code,name,warehouse_type,branch_id) VALUES($1,$2,'DEPO','Test Depo','STANDARD',$3)`, warehouseID, companyID, branchID)
	mustExec(`INSERT INTO products(id,company_id,code,name,kind,variants_enabled) VALUES($1,$2,'STK-SC','Sales Cancel Ürünü','PHYSICAL',false)`, productID, companyID)
	mustExec(`INSERT INTO product_units(company_id,product_id,unit_code,is_base,conversion_factor) VALUES($1,$2,'ADET',true,1)`, companyID, productID)

	session := identity.Session{
		User:             identity.User{ID: userID},
		CurrentCompanyID: companyID,
		Permissions: []string{
			"inventory.movement.post", "inventory.movement.reverse",
			"sales.order.manage", "sales.order.read",
			"sales.dispatch.post", "sales.dispatch.read",
			"sales.price.override",
		},
	}
	inv := inventory.NewService(pool)

	// Opening stock: 50 units on hand at the warehouse.
	openingReceiptID := uuid.NewString()
	mustExec(`INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,party_id,document_date,currency_code,created_by,updated_by) VALUES($1,$2,'PURCHASE_DELIVERY','ACILIS-01',$3,$4,now()::date,'TRY',$5,$5)`, openingReceiptID, companyID, branchID, partyID, userID)
	openTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = inv.PostPurchaseReceiptMovementsTx(ctx, openTx, session, inventory.PurchaseReceiptStockPostingInput{
		ReceiptID: openingReceiptID, WarehouseID: warehouseID,
		Lines: []inventory.PurchaseStockLine{{LineID: uuid.NewString(), ProductID: productID, Quantity: "50", BaseQuantity: "50", ConversionFactor: "1", UnitCode: "ADET", UnitCost: "80", Currency: "TRY"}},
	}); err != nil {
		_ = openTx.Rollback(ctx)
		t.Fatalf("opening stock: %v", err)
	}
	if err = openTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	service := NewService(pool, salesStockAdapter{inv: inv})
	meta := func() identity.RequestMeta {
		return identity.RequestMeta{TraceID: "sales-cancel-test", IdempotencyKey: uuid.NewString()}
	}

	order, err := service.CreateSalesOrder(ctx, session, CommercialDocumentInput{
		BranchID: branchID, DefaultWarehouseID: warehouseID, PartyID: partyID,
		DocumentDate: time.Now().UTC(), CurrencyCode: "TRY",
		Lines: []CommercialLineInput{{
			LineType: "PRODUCT", ProductID: productID, WarehouseID: warehouseID,
			UnitCode: "ADET", Quantity: "10", UnitPrice: "100",
		}},
	}, meta())
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	order, err = service.ConfirmSalesOrder(ctx, session, order.ID, order.Version, meta())
	if err != nil {
		t.Fatalf("confirm order: %v", err)
	}
	orderLineID := order.Lines[0].ID

	dispatch, err := service.ConvertCommercial(ctx, session, SalesDispatch, order.ID, order.Version, meta(), "")
	if err != nil {
		t.Fatalf("convert to dispatch: %v", err)
	}
	dispatch, err = service.PostSalesDispatch(ctx, session, dispatch.ID, dispatch.Version, meta())
	if err != nil {
		t.Fatalf("post dispatch: %v", err)
	}

	orderStatus := func() string {
		t.Helper()
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM sales_orders WHERE company_id=$1 AND id=$2`, companyID, order.ID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		return status
	}
	fulfillmentAllocations := func() int {
		t.Helper()
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM commercial_line_allocations WHERE company_id=$1 AND source_line_id=$2 AND allocation_type='FULFILLMENT'`, companyID, orderLineID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	reservationState := func() (string, string) {
		t.Helper()
		var status, consumed string
		if err := pool.QueryRow(ctx, `SELECT status,consumed_quantity::text FROM sales_order_reservations WHERE company_id=$1 AND order_line_id=$2`, companyID, orderLineID).Scan(&status, &consumed); err != nil {
			t.Fatal(err)
		}
		return status, consumed
	}

	if got := orderStatus(); got != "FULFILLED" {
		t.Fatalf("after dispatch: order status=%s, want FULFILLED", got)
	}
	if got := fulfillmentAllocations(); got != 1 {
		t.Fatalf("after dispatch: FULFILLMENT allocations=%d, want 1", got)
	}
	if status, consumed := reservationState(); status != "CONSUMED" || consumed != "10.00000000" {
		t.Fatalf("after dispatch: reservation status=%s consumed=%s, want CONSUMED/10", status, consumed)
	}

	if _, err = service.TransitionCommercial(ctx, session, SalesDispatch, dispatch.ID, "cancel", dispatch.Version, meta(), "Yanlış sevkiyat"); err != nil {
		t.Fatalf("cancel dispatch: %v", err)
	}

	if got := orderStatus(); got != "CONFIRMED" {
		t.Fatalf("after cancel: order status=%s, want CONFIRMED", got)
	}
	if got := fulfillmentAllocations(); got != 0 {
		t.Fatalf("after cancel: FULFILLMENT allocations=%d, want 0", got)
	}
	if status, consumed := reservationState(); status != "ACTIVE" || consumed != "0.00000000" {
		t.Fatalf("after cancel: reservation status=%s consumed=%s, want ACTIVE/0", status, consumed)
	}

	// The order can be dispatched again.
	dispatch2, err := service.ConvertCommercial(ctx, session, SalesDispatch, order.ID, orderVersionOf(t, ctx, pool, companyID, order.ID), meta(), "")
	if err != nil {
		t.Fatalf("second convert to dispatch: %v", err)
	}
	if _, err = service.PostSalesDispatch(ctx, session, dispatch2.ID, dispatch2.Version, meta()); err != nil {
		t.Fatalf("post second dispatch: %v", err)
	}
	if got := orderStatus(); got != "FULFILLED" {
		t.Fatalf("after re-dispatch: order status=%s, want FULFILLED", got)
	}
}

func orderVersionOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, companyID, orderID string) int64 {
	t.Helper()
	var version int64
	if err := pool.QueryRow(ctx, `SELECT version FROM sales_orders WHERE company_id=$1 AND id=$2`, companyID, orderID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}
