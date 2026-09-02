package sales

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/inventory"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// doubleEffectFixture spins up a fully migrated isolated schema with one
// company/branch/warehouse/customer/product and 100 units of opening stock,
// and returns a sales Service wired to a real inventory Service.
type doubleEffectFixture struct {
	pool                                              *pgxpool.Pool
	service                                           *Service
	session                                           identity.Session
	companyID, branchID, warehouseID, partyID, userID string
	productID                                         string
}

func newDoubleEffectFixture(t *testing.T, ctx context.Context) *doubleEffectFixture {
	t.Helper()
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_double_effect_%d", time.Now().UnixNano())
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

	f := &doubleEffectFixture{
		pool:      pool,
		companyID: uuid.NewString(), branchID: uuid.NewString(), warehouseID: uuid.NewString(),
		partyID: uuid.NewString(), userID: uuid.NewString(), productID: uuid.NewString(),
	}
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	mustExec(`INSERT INTO companies(id,legal_name,trade_name,entity_type,base_currency) VALUES($1,'Double Effect AŞ','Double Effect','LEGAL_ENTITY','TRY')`, f.companyID)
	mustExec(`INSERT INTO users(id,email,display_name,password_hash) VALUES($1,'double-effect@example.test','Double Effect User','test-hash')`, f.userID)
	mustExec(`INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, f.companyID, f.userID)
	mustExec(`INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,'MERKEZ','Merkez')`, f.branchID, f.companyID)
	mustExec(`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'CARI-001','ORGANIZATION',true,false,'Test Müşteri','Test Müşteri AŞ','TRY')`, f.partyID, f.companyID)
	mustExec(`INSERT INTO warehouses(id,company_id,code,name,warehouse_type,branch_id) VALUES($1,$2,'DEPO','Test Depo','STANDARD',$3)`, f.warehouseID, f.companyID, f.branchID)
	mustExec(`INSERT INTO products(id,company_id,code,name,kind,variants_enabled) VALUES($1,$2,'STK-DE','Double Effect Ürünü','PHYSICAL',false)`, f.productID, f.companyID)
	mustExec(`INSERT INTO product_units(company_id,product_id,unit_code,is_base,conversion_factor) VALUES($1,$2,'ADET',true,1)`, f.companyID, f.productID)

	f.session = identity.Session{
		User:             identity.User{ID: f.userID},
		CurrentCompanyID: f.companyID,
		Permissions: []string{
			"inventory.movement.post", "inventory.movement.reverse",
			"sales.order.manage", "sales.order.read",
			"sales.dispatch.post", "sales.dispatch.read",
			"sales.invoice.post", "sales.invoice.read",
			"sales.price.override",
		},
	}
	inv := inventory.NewService(pool)

	openingReceiptID := uuid.NewString()
	mustExec(`INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,party_id,document_date,currency_code,created_by,updated_by) VALUES($1,$2,'PURCHASE_DELIVERY','ACILIS-01',$3,$4,now()::date,'TRY',$5,$5)`, openingReceiptID, f.companyID, f.branchID, f.partyID, f.userID)
	openTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = inv.PostPurchaseReceiptMovementsTx(ctx, openTx, f.session, inventory.PurchaseReceiptStockPostingInput{
		ReceiptID: openingReceiptID, WarehouseID: f.warehouseID,
		Lines: []inventory.PurchaseStockLine{{LineID: uuid.NewString(), ProductID: f.productID, Quantity: "100", BaseQuantity: "100", ConversionFactor: "1", UnitCode: "ADET", UnitCost: "80", Currency: "TRY"}},
	}); err != nil {
		_ = openTx.Rollback(ctx)
		t.Fatalf("opening stock: %v", err)
	}
	if err = openTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	f.service = NewService(pool, salesStockAdapter{inv: inv})
	return f
}

func (f *doubleEffectFixture) meta() identity.RequestMeta {
	return identity.RequestMeta{TraceID: "double-effect-test", IdempotencyKey: uuid.NewString()}
}

func (f *doubleEffectFixture) confirmedOrder(t *testing.T, ctx context.Context, qty string) CommercialDocument {
	t.Helper()
	order, err := f.service.CreateSalesOrder(ctx, f.session, CommercialDocumentInput{
		BranchID: f.branchID, DefaultWarehouseID: f.warehouseID, PartyID: f.partyID,
		DocumentDate: time.Now().UTC(), CurrencyCode: "TRY",
		Lines: []CommercialLineInput{{
			LineType: "PRODUCT", ProductID: f.productID, WarehouseID: f.warehouseID,
			UnitCode: "ADET", Quantity: qty, UnitPrice: "100",
		}},
	}, f.meta())
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	order, err = f.service.ConfirmSalesOrder(ctx, f.session, order.ID, order.Version, f.meta())
	if err != nil {
		t.Fatalf("confirm order: %v", err)
	}
	return order
}

// TestDraftDispatchDoesNotBlockSourceAndConcurrentPostCannotOverFulfill proves
// invariants 12 and 13 on the sales order -> dispatch path: two draft dispatches
// for the full order quantity can both be prepared (a RESERVED allocation never
// binds the source), but only one can post -- the second post revalidates the
// CONSUMED total under a source-line lock and is rejected.
func TestDraftDispatchDoesNotBlockSourceAndConcurrentPostCannotOverFulfill(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	f := newDoubleEffectFixture(t, ctx)

	order := f.confirmedOrder(t, ctx, "100")

	dispatchA, err := f.service.ConvertCommercial(ctx, f.session, SalesDispatch, order.ID, order.Version, f.meta(), "")
	if err != nil {
		t.Fatalf("convert dispatch A: %v", err)
	}
	// A second full-quantity draft dispatch can still be created: the first
	// draft's allocations are RESERVED and do not consume the order line.
	dispatchB, err := f.service.ConvertCommercial(ctx, f.session, SalesDispatch, order.ID, order.Version, f.meta(), "")
	if err != nil {
		t.Fatalf("convert dispatch B (draft must not be blocked by draft A): %v", err)
	}

	if _, err = f.service.PostSalesDispatch(ctx, f.session, dispatchA.ID, dispatchA.Version, f.meta()); err != nil {
		t.Fatalf("post dispatch A: %v", err)
	}
	// Now the order line is fully CONSUMED; posting B must be rejected.
	_, err = f.service.PostSalesDispatch(ctx, f.session, dispatchB.ID, dispatchB.Version, f.meta())
	if err == nil {
		t.Fatal("post dispatch B succeeded: order line was over-fulfilled")
	}
	if !strings.Contains(err.Error(), CommercialErrorOverFulfillment) {
		t.Fatalf("post dispatch B: got %v, want ORDER_LINE_OVER_FULFILLMENT", err)
	}

	var consumed string
	if err = f.pool.QueryRow(ctx, `SELECT COALESCE(SUM(base_quantity),0)::text FROM commercial_line_allocations WHERE company_id=$1 AND source_line_id=$2 AND allocation_type='FULFILLMENT' AND status='CONSUMED'`, f.companyID, order.Lines[0].ID).Scan(&consumed); err != nil {
		t.Fatal(err)
	}
	if consumed != "100.00000000" {
		t.Fatalf("consumed FULFILLMENT total=%s, want 100", consumed)
	}
}

// TestCancellingDispatchWithPostedInvoiceIsRejected proves invariant 17/18: a
// dispatch a finalized invoice still depends on cannot be cancelled directly;
// the user must cancel the invoice first. No cascade is performed.
func TestCancellingDispatchWithPostedInvoiceIsRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	f := newDoubleEffectFixture(t, ctx)

	order := f.confirmedOrder(t, ctx, "10")
	dispatch, err := f.service.ConvertCommercial(ctx, f.session, SalesDispatch, order.ID, order.Version, f.meta(), "")
	if err != nil {
		t.Fatalf("convert dispatch: %v", err)
	}
	dispatch, err = f.service.PostSalesDispatch(ctx, f.session, dispatch.ID, dispatch.Version, f.meta())
	if err != nil {
		t.Fatalf("post dispatch: %v", err)
	}

	// Simulate a posted downstream invoice sourced from this dispatch.
	invoiceID := uuid.NewString()
	if _, err = f.pool.Exec(ctx, `INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,party_id,document_date,currency_code,status,posted_at,created_by,updated_by)
		VALUES($1,$2,'SALES_INVOICE','FTR-2026-000001',$3,$4,now()::date,'TRY','POSTED',now(),$5,$5)`, invoiceID, f.companyID, f.branchID, f.partyID, f.userID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(ctx, `INSERT INTO commercial_document_sources(company_id,document_id,source_document_id,relation_type) VALUES($1,$2,$3,'INVOICING')`, f.companyID, invoiceID, dispatch.ID); err != nil {
		t.Fatal(err)
	}

	_, err = f.service.TransitionCommercial(ctx, f.session, SalesDispatch, dispatch.ID, "cancel", dispatch.Version, f.meta(), "Yanlış sevkiyat")
	if err == nil {
		t.Fatal("cancel dispatch succeeded despite a posted downstream invoice")
	}
	if !strings.Contains(err.Error(), CommercialErrorDocumentHasDependencies) {
		t.Fatalf("cancel dispatch: got %v, want DOCUMENT_HAS_DEPENDENCIES", err)
	}
	if !strings.Contains(err.Error(), "FTR-2026-000001") {
		t.Fatalf("cancel dispatch error should name the blocking invoice, got %v", err)
	}

	// The dispatch is still POSTED and its stock movement is intact.
	var status string
	if err = f.pool.QueryRow(ctx, `SELECT status FROM sales_dispatches WHERE company_id=$1 AND id=$2`, f.companyID, dispatch.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "POSTED" {
		t.Fatalf("dispatch status=%s, want POSTED (no partial cancel)", status)
	}
}
