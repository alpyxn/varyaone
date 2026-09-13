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

// TestManualEntryOpenItemLifecycle proves a manual cari movement behaves like
// an invoice balance end to end: it is listed as an open item, aged, collected
// explicitly and FIFO, planned into installments and collected per installment,
// and a reversal closes it only while nothing is allocated.
func TestManualEntryOpenItemLifecycle(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_fin_manual_oi_%d", time.Now().UnixNano())
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
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{
		AdminName: "Finans Yönetici", AdminEmail: "fin-manual-oi@example.test", Password: "uzun-ve-guvenli-parola",
		LegalName: "Manuel Kalem AŞ", TradeName: "Manuel Kalem", EntityType: "LEGAL_ENTITY",
	}, identity.RequestMeta{TraceID: "fin-manual-oi-test"})
	if err != nil {
		t.Fatal(err)
	}
	companyID := session.CurrentCompanyID
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, execErr := pool.Exec(ctx, sql, args...); execErr != nil {
			t.Fatalf("fixture: %v", execErr)
		}
	}
	partyID := uuid.NewString()
	mustExec(`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'CARI-MAN','ORGANIZATION',true,true,'Manuel Cari','Manuel Cari AŞ','TRY')`, partyID, companyID)
	accountID := uuid.NewString()
	mustExec(`INSERT INTO finance_accounts(id,company_id,account_type,code,name,currency) VALUES($1,$2,'CASH','KASA-M','Merkez Kasa','TRY')`, accountID, companyID)

	svc := NewService(pool)
	meta := identity.RequestMeta{TraceID: "fin-manual-oi-test"}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	day := func(offset int) string { return today.AddDate(0, 0, offset).Format("2006-01-02") }

	postManual := func(kind, amount, reversalOf string) ManualEntry {
		t.Helper()
		entry, postErr := svc.PostManualEntry(ctx, session, ManualEntryInput{
			PartyID: partyID, EntryKind: kind, Currency: "TRY", Amount: amount,
			Description: "Manuel " + kind, TransactionDate: today,
			IdempotencyKey: uuid.NewString(), ReversalOfID: reversalOf,
		}, meta)
		if postErr != nil {
			t.Fatalf("post manual %s %s: %v", kind, amount, postErr)
		}
		return entry
	}
	collect := func(input PaymentInput) {
		t.Helper()
		input.PartyID, input.AccountID, input.PaymentMethod, input.Currency = partyID, accountID, "CASH", "TRY"
		input.ExchangeRate, input.Description, input.TransactionDate = "1", "Tahsilat", today
		input.IdempotencyKey = uuid.NewString()
		if _, postErr := svc.PostCollection(ctx, session, input, identity.RequestMeta{TraceID: "fin-manual-oi-test", IdempotencyKey: uuid.NewString()}); postErr != nil {
			t.Fatalf("post collection %+v: %v", input, postErr)
		}
	}
	receivables := func() []OpenItem {
		t.Helper()
		items, listErr := svc.ListOpenItems(ctx, session, partyID, "TRY", "RECEIVABLE", 100)
		if listErr != nil {
			t.Fatalf("list open items: %v", listErr)
		}
		return items
	}
	agingTotal := func(side string) string {
		t.Helper()
		report, agingErr := svc.PartyAging(ctx, session, today, partyID, "TRY", side)
		if agingErr != nil {
			t.Fatalf("party aging: %v", agingErr)
		}
		if len(report.Items) == 0 {
			return "0.0000"
		}
		return report.Items[0].Total
	}

	debit := postManual("DEBIT", "1000", "")
	items := receivables()
	if len(items) != 1 || items[0].OpenAmount != "1000.0000" || items[0].DocumentID != "" || items[0].DocumentNo != debit.DocumentNo {
		t.Fatalf("manual debit must be one open receivable, got %+v", items)
	}
	openItemID := items[0].ID
	if got := agingTotal("RECEIVABLE"); got != "1000.0000" {
		t.Fatalf("aging total = %s, want 1000.0000", got)
	}

	collect(PaymentInput{Amount: "300", Allocations: []AllocationInput{{OpenItemID: openItemID, Amount: "300"}}})
	collect(PaymentInput{Amount: "200", AutoAllocate: true})
	if items = receivables(); len(items) != 1 || items[0].OpenAmount != "500.0000" {
		t.Fatalf("after explicit and FIFO collections open amount must be 500, got %+v", items)
	}
	if got := agingTotal("RECEIVABLE"); got != "500.0000" {
		t.Fatalf("aging total = %s, want 500.0000", got)
	}

	plan, err := svc.CreatePaymentPlan(ctx, session, PaymentPlanInput{
		PartyID: partyID, Side: "RECEIVABLE", Currency: "TRY", Description: "Manuel taksit",
		OpenItemIDs:    []string{openItemID},
		Installments:   []PlanInstallmentInput{{DueDate: day(0), Amount: "250"}, {DueDate: day(30), Amount: "250"}},
		IdempotencyKey: uuid.NewString(),
	}, meta)
	if err != nil {
		t.Fatalf("create payment plan from manual entry: %v", err)
	}
	if len(plan.Sources) != 1 || plan.Sources[0].DocumentID != "" || plan.Sources[0].DocumentNo != debit.DocumentNo {
		t.Fatalf("plan source must reference the manual entry, got %+v", plan.Sources)
	}
	collect(PaymentInput{Amount: "250", PaymentPlanID: plan.ID, InstallmentNo: 1, Allocations: []AllocationInput{{OpenItemID: openItemID, Amount: "250"}}})
	if items, err = svc.ListOpenItems(ctx, session, partyID, "TRY", "RECEIVABLE", 100); err != nil || len(items) != 1 || items[0].OpenAmount != "250.0000" {
		t.Fatalf("after installment collection open amount must be 250, got %+v (%v)", items, err)
	}

	if _, err = svc.PostManualEntry(ctx, session, ManualEntryInput{
		PartyID: partyID, EntryKind: "CREDIT", Currency: "TRY", Amount: "1000", Description: "Ters kayıt",
		TransactionDate: today, IdempotencyKey: uuid.NewString(), ReversalOfID: debit.ID,
	}, meta); err == nil {
		t.Fatal("reversing an allocated manual entry must be refused")
	}

	unallocated := postManual("DEBIT", "100", "")
	if len(receivables()) != 2 {
		t.Fatal("second manual debit must add an open item")
	}
	postManual("CREDIT", "100", unallocated.ID)
	if items = receivables(); len(items) != 1 || items[0].ID != openItemID {
		t.Fatalf("reversal must close the reversed entry's open item, got %+v", items)
	}
	if got := agingTotal("RECEIVABLE"); got != "250.0000" {
		t.Fatalf("aging total after reversal = %s, want 250.0000", got)
	}

	postManual("CREDIT", "400", "")
	payables, err := svc.ListOpenItems(ctx, session, partyID, "TRY", "PAYABLE", 100)
	if err != nil || len(payables) != 1 || payables[0].OpenAmount != "400.0000" {
		t.Fatalf("manual credit must be one open payable, got %+v (%v)", payables, err)
	}
	if got := agingTotal("PAYABLE"); got != "400.0000" {
		t.Fatalf("payable aging total = %s, want 400.0000", got)
	}
}
