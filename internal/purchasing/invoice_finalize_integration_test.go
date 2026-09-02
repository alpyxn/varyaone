package purchasing

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestFinalizeReadLocksOnlyOwnRowNotNullableJoin is a regression test for the
// "FOR UPDATE cannot be applied to the nullable side of an outer join"
// (SQLSTATE 0A000) failure. FinalizePurchaseInvoice / FinalizePurchaseReturn
// read the exchange rate through a LEFT JOIN to documents and used a bare
// FOR UPDATE, which PostgreSQL rejects at plan time. The fix is FOR UPDATE OF i
// / FOR UPDATE OF r; this test runs both exact statements against a migrated
// schema and asserts they plan and return.
func TestFinalizeReadLocksOnlyOwnRowNotNullableJoin(t *testing.T) {
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
	schema := fmt.Sprintf("varya_pi_forupdate_%d", time.Now().UnixNano())
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

	companyID, userID, supplierID, branchID, warehouseID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	invoiceID, returnID := uuid.NewString(), uuid.NewString()
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	mustExec(`INSERT INTO companies(id,legal_name,trade_name,entity_type,base_currency) VALUES($1,'PI ForUpdate AŞ','PI ForUpdate','LEGAL_ENTITY','TRY')`, companyID)
	mustExec(`INSERT INTO users(id,email,display_name,password_hash) VALUES($1,'pi-forupdate@example.test','PI ForUpdate User','test-hash')`, userID)
	mustExec(`INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, companyID, userID)
	mustExec(`INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,'MERKEZ','Merkez')`, branchID, companyID)
	mustExec(`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'TED-001','ORGANIZATION',false,true,'Test Tedarikçi','Test Tedarikçi AŞ','TRY')`, supplierID, companyID)
	mustExec(`INSERT INTO warehouses(id,company_id,code,name,warehouse_type,branch_id) VALUES($1,$2,'DEPO','Test Depo','STANDARD',$3)`, warehouseID, companyID, branchID)
	// Both rows have a NULL document_id so the LEFT JOIN's right side is genuinely
	// nullable -- the exact shape that triggered SQLSTATE 0A000.
	mustExec(`INSERT INTO purchase_invoices(id,company_id,invoice_no,supplier_id,branch_id,invoice_date,currency,status,payable_total,created_by,version)
		VALUES($1,$2,'AF-2026-000001',$3,$4,now()::date,'TRY','DRAFT',1000,$5,1)`, invoiceID, companyID, supplierID, branchID, userID)
	mustExec(`INSERT INTO purchase_returns(id,company_id,return_no,supplier_id,branch_id,warehouse_id,return_date,currency,status,total,reason,created_by,version)
		VALUES($1,$2,'AI-2026-000001',$3,$4,$6,now()::date,'TRY','DRAFT',500,'hasarlı',$5,1)`, returnID, companyID, supplierID, branchID, userID, warehouseID)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var status string
	if err = tx.QueryRow(ctx, `SELECT i.status FROM purchase_invoices i
		LEFT JOIN documents d ON d.company_id=i.company_id AND d.id=i.document_id
		WHERE i.company_id=$1 AND i.id=$2 AND i.version=$3 FOR UPDATE OF i`, companyID, invoiceID, 1).Scan(&status); err != nil {
		t.Fatalf("invoice finalize read (FOR UPDATE OF i): %v", err)
	}
	if status != "DRAFT" {
		t.Fatalf("invoice status=%s, want DRAFT", status)
	}
	if err = tx.QueryRow(ctx, `SELECT r.status FROM purchase_returns r
		LEFT JOIN documents d ON d.company_id=r.company_id AND d.id=r.document_id
		WHERE r.company_id=$1 AND r.id=$2 AND r.version=$3 FOR UPDATE OF r`, companyID, returnID, 1).Scan(&status); err != nil {
		t.Fatalf("return finalize read (FOR UPDATE OF r): %v", err)
	}
	if status != "DRAFT" {
		t.Fatalf("return status=%s, want DRAFT", status)
	}
}
