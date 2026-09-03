package finance

import (
	"bytes"
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

// TestPartyAgingBucketsOpenItemsByDueDate proves the aging read model against a
// live schema: every still-open invoice lands in exactly one due-date bucket,
// a settled portion leaves the report, and the directional permission gate
// holds.
func TestPartyAgingBucketsOpenItemsByDueDate(t *testing.T) {
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
	schema := fmt.Sprintf("varya_fin_aging_%d", time.Now().UnixNano())
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

	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{
		AdminName: "Finans Yönetici", AdminEmail: "fin-aging@example.test", Password: "uzun-ve-guvenli-parola",
		LegalName: "Yaşlandırma AŞ", TradeName: "Yaşlandırma", EntityType: "LEGAL_ENTITY",
	}, identity.RequestMeta{TraceID: "fin-aging-test"})
	if err != nil {
		t.Fatal(err)
	}
	companyID, userID := session.CurrentCompanyID, session.User.ID

	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, execErr := pool.Exec(ctx, sql, args...); execErr != nil {
			t.Fatalf("fixture: %v", execErr)
		}
	}

	var branchID string
	if err = pool.QueryRow(ctx, `SELECT id FROM branches WHERE company_id=$1`, companyID).Scan(&branchID); err != nil {
		t.Fatal(err)
	}

	partyID := uuid.NewString()
	mustExec(`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'CARI-YAS','ORGANIZATION',true,false,'Yaşlandırma Müşteri','Yaşlandırma Müşteri AŞ','TRY')`, partyID, companyID)

	accountID := uuid.NewString()
	mustExec(`INSERT INTO finance_accounts(id,company_id,account_type,code,name,currency) VALUES($1,$2,'CASH','KASA-1','Merkez Kasa','TRY')`, accountID, companyID)

	asOf := time.Now().UTC().Truncate(24 * time.Hour)
	day := func(offset int) string { return asOf.AddDate(0, 0, offset).Format("2006-01-02") }

	invoices := []struct {
		docNo, docDate, dueDate, amount string
	}{
		{"INV-OLD", day(-120), day(-100), "100.0000"}, // 90+ gün
		{"INV-MID", day(-40), day(-10), "50.0000"},    // 1-30 gün
		{"INV-NEW", day(-5), day(10), "40.0000"},      // vadesi gelmemiş
	}
	for _, inv := range invoices {
		docID, ledgerID, openItemID := uuid.NewString(), uuid.NewString(), uuid.NewString()
		mustExec(`INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,party_id,document_date,currency_code,grand_total,created_by,updated_by) VALUES($1,$2,'SALES_INVOICE',$3,$4,$5,$6::date,'TRY',$7,$8,$8)`,
			docID, companyID, inv.docNo, branchID, partyID, inv.docDate, inv.amount, userID)
		mustExec(`INSERT INTO party_ledger_entries(id,company_id,party_id,currency,entry_type,source_type,source_id,idempotency_key,description,debit,credit,document_date,actor_user_id) VALUES($1,$2,$3,'TRY','SALES_INVOICE','document',$4,$5,$6,$7,0,$8::date,$9)`,
			ledgerID, companyID, partyID, docID, "invoice:"+inv.docNo, "Satış faturası "+inv.docNo, inv.amount, inv.docDate, userID)
		mustExec(`INSERT INTO finance_invoice_open_items(id,company_id,document_id,party_id,party_ledger_entry_id,side,currency,original_amount,base_currency,base_amount,document_date,due_date) VALUES($1,$2,$3,$4,$5,'RECEIVABLE','TRY',$6,'TRY',$6,$7::date,$8::date)`,
			openItemID, companyID, docID, partyID, ledgerID, inv.amount, inv.docDate, inv.dueDate)
	}

	svc := NewService(pool)

	// A 20 TRY collection settles part of the oldest invoice; the aging report
	// must show 80 left in the 90+ bucket, not the original 100.
	if _, err = svc.PostCollection(ctx, session, PaymentInput{
		PartyID:         partyID,
		AccountID:       accountID,
		PaymentMethod:   "CASH",
		Currency:        "TRY",
		Amount:          "20",
		ExchangeRate:    "1",
		Description:     "Kısmi tahsilat",
		TransactionDate: asOf,
		IdempotencyKey:  uuid.NewString(),
		AutoAllocate:    true,
	}, identity.RequestMeta{TraceID: "fin-aging-test", IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatalf("post collection: %v", err)
	}

	report, err := svc.PartyAging(ctx, session, asOf, "", "", "RECEIVABLE")
	if err != nil {
		t.Fatalf("party aging: %v", err)
	}
	if len(report.Items) != 1 {
		t.Fatalf("expected a single party/currency row, got %d: %+v", len(report.Items), report.Items)
	}
	row := report.Items[0]
	for _, check := range []struct {
		name, got, want string
	}{
		{"not_due", row.NotDue, "40.0000"},
		{"days_0_30", row.Bucket0To30, "50.0000"},
		{"days_31_60", row.Bucket31To60, "0.0000"},
		{"days_61_90", row.Bucket61To90, "0.0000"},
		{"days_90_plus", row.Bucket90Plus, "80.0000"},
		{"overdue_total", row.Overdue, "130.0000"},
		{"total", row.Total, "170.0000"},
	} {
		if check.got != check.want {
			t.Fatalf("%s = %q, want %q (row: %+v)", check.name, check.got, check.want, row)
		}
	}
	if row.PartyID != partyID || row.Currency != "TRY" || row.Side != "RECEIVABLE" {
		t.Fatalf("unexpected row identity: %+v", row)
	}
	if report.AsOf != asOf.Format("2006-01-02") {
		t.Fatalf("as_of = %q, want %q", report.AsOf, asOf.Format("2006-01-02"))
	}

	// Payables are a different side: the receivable rows must not leak into it.
	payables, err := svc.PartyAging(ctx, session, asOf, "", "", "PAYABLE")
	if err != nil {
		t.Fatalf("payable aging: %v", err)
	}
	if len(payables.Items) != 0 {
		t.Fatalf("expected no payable rows, got %+v", payables.Items)
	}

	// A caller holding only the payable permission must never receive
	// receivables, with or without an explicit side filter.
	payableOnly := identity.Session{User: session.User, CurrentCompanyID: companyID, Permissions: []string{"finance.payment.read"}}
	if _, err = svc.PartyAging(ctx, payableOnly, asOf, "", "", "RECEIVABLE"); err != identity.ErrForbidden {
		t.Fatalf("expected ErrForbidden for receivable side, got %v", err)
	}
	unfiltered, err := svc.PartyAging(ctx, payableOnly, asOf, "", "", "")
	if err != nil {
		t.Fatalf("unfiltered aging for payable-only caller: %v", err)
	}
	if len(unfiltered.Items) != 0 {
		t.Fatalf("payable-only caller received receivable rows: %+v", unfiltered.Items)
	}

	denied := identity.Session{User: session.User, CurrentCompanyID: companyID}
	if _, err = svc.PartyAging(ctx, denied, asOf, "", "", ""); err != identity.ErrForbidden {
		t.Fatalf("expected ErrForbidden without finance read permissions, got %v", err)
	}
	if _, err = svc.PartyAging(ctx, session, asOf, "", "TRYY", ""); err == nil {
		t.Fatal("expected a validation error for an invalid currency")
	}
}
