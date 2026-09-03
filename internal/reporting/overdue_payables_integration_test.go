package reporting

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
)

// TestOverduePayablesReportsThePayableSide guards a report that silently
// returned nothing at all. DocumentSettlement fills AmountDue only for a
// receivable and AmountPayable for a payable; the payable report read
// AmountDue, found the empty string on every candidate and skipped them all,
// so "vadesi geçmiş borçlar" was permanently empty however many overdue
// supplier invoices existed.
func TestOverduePayablesReportsThePayableSide(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool := reportingTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{
		AdminName: "Rapor Yönetici", AdminEmail: "overdue@example.test", Password: "uzun-ve-guvenli-parola",
		LegalName: "Borç Rapor AŞ", TradeName: "Borç Rapor", EntityType: "LEGAL_ENTITY",
	}, identity.RequestMeta{TraceID: "overdue-test"})
	if err != nil {
		t.Fatal(err)
	}
	companyID, userID := session.CurrentCompanyID, session.User.ID
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	var branchID string
	if err = pool.QueryRow(ctx, `SELECT id FROM branches WHERE company_id=$1`, companyID).Scan(&branchID); err != nil {
		t.Fatal(err)
	}
	supplierID := uuid.NewString()
	mustExec(`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'TED-1','ORGANIZATION',false,true,'Tedarikçi','Tedarikçi AŞ','TRY')`, supplierID, companyID)

	// One overdue purchase invoice, and one overdue purchase return: the return
	// is a supplier credit, not a debt, so it must not be listed.
	openItem := func(typeCode, no, amount string) string {
		documentID, ledgerID, openItemID := uuid.NewString(), uuid.NewString(), uuid.NewString()
		mustExec(`INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,party_id,document_date,due_date,currency_code,grand_total,status,posted_at,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,'2026-01-10'::date,'2026-01-20'::date,'TRY',$7,'POSTED',now(),$8,$8)`,
			documentID, companyID, typeCode, no, branchID, supplierID, amount, userID)
		mustExec(`INSERT INTO party_ledger_entries(id,company_id,party_id,currency,entry_type,source_type,source_id,idempotency_key,description,debit,credit,document_date,actor_user_id) VALUES($1,$2,$3,'TRY','PAYABLE','document',$4,$5,'Alış faturası',0,$6,'2026-01-10'::date,$7)`,
			ledgerID, companyID, supplierID, documentID, "invoice:"+no, amount, userID)
		mustExec(`INSERT INTO finance_invoice_open_items(id,company_id,document_id,party_id,party_ledger_entry_id,side,currency,original_amount,base_currency,base_amount,document_date,due_date) VALUES($1,$2,$3,$4,$5,'PAYABLE','TRY',$6,'TRY',$6,'2026-01-10'::date,'2026-01-20'::date)`,
			openItemID, companyID, documentID, supplierID, ledgerID, amount)
		return documentID
	}
	openItem("PURCHASE_INVOICE", "AF-1", "1500.0000")
	openItem("PURCHASE_RETURN_INVOICE", "AI-1", "300.0000")

	service := NewService(pool, finance.NewService(pool))
	asOf, _ := time.Parse("2006-01-02", "2026-03-01")
	rows, err := service.OverduePayables(ctx, session, asOf)
	if err != nil {
		t.Fatalf("overdue payables: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("overdue payables returned %d rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].DocumentNo != "AF-1" {
		t.Fatalf("listed document = %q, want the purchase invoice AF-1", rows[0].DocumentNo)
	}
	if rows[0].AmountDue != "1500.0000" {
		t.Fatalf("amount due = %q, want 1500.0000", rows[0].AmountDue)
	}
}
