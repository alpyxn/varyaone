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
	"github.com/jackc/pgx/v5/pgxpool"
)

// A draft with lines used to fail to delete: the header's ON DELETE CASCADE
// removed the lines only after the header row was gone, and the line
// immutability trigger — which reads the parent's status — took the missing
// parent for a posted document and refused with SQLSTATE 55000.
func TestDeletingSalesInvoiceDraftRemovesItsLines(t *testing.T) {
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
	schema := fmt.Sprintf("varya_sales_delete_%d", time.Now().UnixNano())
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
	mustExec(`INSERT INTO companies(id,legal_name,trade_name,entity_type,base_currency) VALUES($1,'Draft Sil AŞ','Draft Sil','LEGAL_ENTITY','TRY')`, companyID)
	mustExec(`INSERT INTO users(id,email,display_name,password_hash) VALUES($1,'draft-delete@example.test','Draft Sil User','test-hash')`, userID)
	mustExec(`INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, companyID, userID)
	mustExec(`INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,'MERKEZ','Merkez')`, branchID, companyID)
	mustExec(`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'CARI-001','ORGANIZATION',true,false,'Test Müşteri','Test Müşteri AŞ','TRY')`, partyID, companyID)
	mustExec(`INSERT INTO warehouses(id,company_id,code,name,warehouse_type,branch_id) VALUES($1,$2,'DEPO','Test Depo','STANDARD',$3)`, warehouseID, companyID, branchID)
	mustExec(`INSERT INTO products(id,company_id,code,name,kind,variants_enabled) VALUES($1,$2,'STK-DS','Draft Sil Ürünü','PHYSICAL',false)`, productID, companyID)
	mustExec(`INSERT INTO product_units(company_id,product_id,unit_code,is_base,conversion_factor) VALUES($1,$2,'ADET',true,1)`, companyID, productID)

	session := identity.Session{
		User:             identity.User{ID: userID},
		CurrentCompanyID: companyID,
		Permissions:      []string{"sales.invoice.draft", "sales.invoice.post", "sales.invoice.read", "sales.price.override"},
	}
	service := NewService(pool, salesStockAdapter{inv: inventory.NewService(pool)})
	meta := identity.RequestMeta{TraceID: "sales-draft-delete-test", IdempotencyKey: uuid.NewString()}

	invoice, err := service.CreateSalesInvoice(ctx, session, CommercialDocumentInput{
		BranchID: branchID, DefaultWarehouseID: warehouseID, PartyID: partyID,
		DocumentDate: time.Now().UTC(), CurrencyCode: "TRY",
		Lines: []CommercialLineInput{{
			LineType: "PRODUCT", ProductID: productID, WarehouseID: warehouseID,
			UnitCode: "ADET", Quantity: "3", UnitPrice: "250",
		}},
	}, meta)
	if err != nil {
		t.Fatalf("create invoice draft: %v", err)
	}
	if len(invoice.Lines) != 1 {
		t.Fatalf("expected one line on the draft, got %d", len(invoice.Lines))
	}

	if err = service.DeleteCommercialDraft(ctx, session, SalesInvoice, invoice.ID, invoice.Version,
		identity.RequestMeta{TraceID: "sales-draft-delete-test", IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatalf("delete invoice draft: %v", err)
	}

	var remaining int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM sales_invoice_lines WHERE company_id=$1 AND document_id=$2`, companyID, invoice.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("expected the lines to be gone, %d left", remaining)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM sales_invoices WHERE company_id=$1 AND id=$2`, companyID, invoice.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("expected the draft header to be gone, %d left", remaining)
	}
}
