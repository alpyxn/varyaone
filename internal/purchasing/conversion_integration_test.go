package purchasing

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPurchaseConversionPreservesDiscountAndResolvesTaxes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dsn := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("DB required")
	}
	base, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("review_purchase_%d", time.Now().UnixNano())
	if _, e = base.Exec(ctx, `CREATE SCHEMA `+schema); e != nil {
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	p, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { p.Close(); base.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); base.Close() }()
	if e = migrations.New(p).Up(ctx); e != nil {
		t.Fatal(e)
	}
	c, u, b, w, party, product := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, e := p.Exec(ctx, sql, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO companies(id,legal_name,trade_name,entity_type,base_currency) VALUES($1,'Review','Review','LEGAL_ENTITY','TRY')`, c)
	exec(`INSERT INTO users(id,email,display_name,password_hash) VALUES($1,'review@example.test','Review','hash')`, u)
	exec(`INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, c, u)
	exec(`INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,'B','Branch')`, b, c)
	exec(`INSERT INTO warehouses(id,company_id,code,name,warehouse_type,branch_id) VALUES($1,$2,'W','Warehouse','STANDARD',$3)`, w, c, b)
	exec(`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'P','ORGANIZATION',false,true,'Supplier','Supplier','TRY')`, party, c)
	exec(`INSERT INTO products(id,company_id,code,name,kind,purchase_price) VALUES($1,$2,'S','Service','SERVICE',100)`, product, c)
	exec(`INSERT INTO product_units(company_id,product_id,unit_code,is_base,conversion_factor) VALUES($1,$2,'ADET',true,1)`, c, product)
	session := identity.Session{User: identity.User{ID: u}, CurrentCompanyID: c, Permissions: []string{"purchase.order.manage", "purchase.order.read", "purchase.invoice.draft", "purchase.invoice.read", "purchase.price.override", "purchase.tax.override"}}
	s := NewService(p, nil, nil)
	meta := func() identity.RequestMeta { return identity.RequestMeta{IdempotencyKey: uuid.NewString()} }
	order, e := s.CreatePurchaseOrder(ctx, session, PurchaseOrderInput{SupplierID: party, BranchID: b, WarehouseID: w, Currency: "TRY", Lines: []PurchaseOrderLine{{LineType: "SERVICE", ProductID: product, ProductNameSnapshot: "Service", UnitCode: "ADET", OrderedQuantity: "10", UnitPrice: "100", DiscountAmount: "100", NetAmount: "900"}}}, meta())
	if e != nil {
		t.Fatal(e)
	}
	order, e = s.ConfirmPurchaseOrder(ctx, session, order.ID, order.Version, meta())
	if e != nil {
		t.Fatal(e)
	}
	result, e := s.ConvertPurchaseDocument(ctx, session, PurchaseInvoiceKind, order.ID, order.Version, meta())
	if e != nil {
		t.Fatal(e)
	}
	invoice := result.(PurchaseInvoice)
	t.Logf("order net total=%s invoice payable=%s invoice discount=%s", order.Total, invoice.PayableTotal, invoice.Lines[0].DiscountAmount)
	if compare(invoice.PayableTotal, "900") != 0 {
		t.Errorf("purchase conversion lost agreed order discount")
	}
	exec(`UPDATE products SET purchase_tax_rate=20 WHERE company_id=$1 AND id=$2`, c, product)
	result, e = s.ConvertPurchaseDocument(ctx, session, PurchaseInvoiceKind, order.ID, order.Version, meta())
	if e != nil {
		t.Fatal(e)
	}
	invoice = result.(PurchaseInvoice)
	t.Logf("taxed product converted invoice tax=%s components=%+v", invoice.Lines[0].TaxAmount, invoice.Lines[0].TaxComponentsSnapshot)
	if compare(invoice.Lines[0].TaxAmount, "180") != 0 || compare(invoice.PayableTotal, "1080") != 0 {
		t.Errorf("conversion hard-coded zero tax overrides configured tax")
	}
	session.Permissions = session.Permissions[:len(session.Permissions)-1]
	_, e = s.ConvertPurchaseDocument(ctx, session, PurchaseInvoiceKind, order.ID, order.Version, meta())
	if e != nil {
		t.Fatalf("ordinary conversion must not require tax override: %v", e)
	}
	// An explicit zero is still an override; only omission asks for defaults.
	manual := invoice.Lines[0]
	manual.TaxAmount = "0"
	_, e = s.CreatePurchaseInvoice(ctx, session, PurchaseInvoiceInput{SupplierID: party, BranchID: b, PurchaseOrderID: order.ID, Currency: "TRY", Lines: []PurchaseInvoiceLine{manual}}, meta())
	if e == nil {
		t.Fatal("explicit zero tax bypassed override permission")
	}
	session.Permissions = append(session.Permissions, "purchase.tax.override")
	if _, e = s.CreatePurchaseInvoice(ctx, session, PurchaseInvoiceInput{SupplierID: party, BranchID: b, PurchaseOrderID: order.ID, Currency: "TRY", Lines: []PurchaseInvoiceLine{manual}}, meta()); e != nil {
		t.Fatalf("authorized explicit tax override: %v", e)
	}
}
