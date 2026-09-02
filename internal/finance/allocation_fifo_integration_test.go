package finance

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAutoAllocateDistributesOldestOpenItemsFirst proves the "en eski borçtan
// dağıt" path end to end: PaymentInput.AutoAllocate on a fresh collection and
// AllocatePaymentFIFO on an existing advance both consume the party's oldest
// open invoices first, leave any excess as an advance, and are idempotent.
func TestAutoAllocateDistributesOldestOpenItemsFirst(t *testing.T) {
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
	schema := fmt.Sprintf("varya_fin_fifo_%d", time.Now().UnixNano())
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
	session, err := identityService.Setup(ctx, identity.SetupInput{AdminName: "Finans Yönetici", AdminEmail: "fin-fifo@example.test", Password: "uzun-ve-guvenli-parola", LegalName: "Finans FIFO AŞ", TradeName: "Finans FIFO", EntityType: "LEGAL_ENTITY"}, identity.RequestMeta{TraceID: "fin-fifo-test"})
	if err != nil {
		t.Fatal(err)
	}
	companyID := session.CurrentCompanyID
	userID := session.User.ID

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

	partyID := uuid.NewString()
	mustExec(`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'CARI-FIFO','ORGANIZATION',true,false,'FIFO Müşteri','FIFO Müşteri AŞ','TRY')`, partyID, companyID)

	accountID := uuid.NewString()
	mustExec(`INSERT INTO finance_accounts(id,company_id,account_type,code,name,currency) VALUES($1,$2,'CASH','KASA-1','Merkez Kasa','TRY')`, accountID, companyID)

	// Two open sales invoices: the older one (INV-OLD) must be settled first.
	type invoiceFixture struct {
		docNo  string
		date   string
		amount string
	}
	invoices := []invoiceFixture{
		{docNo: "INV-OLD", date: "2026-06-01", amount: "100.0000"},
		{docNo: "INV-NEW", date: "2026-07-15", amount: "80.0000"},
	}
	openItemIDs := make([]string, 0, len(invoices))
	for _, inv := range invoices {
		docID := uuid.NewString()
		ledgerID := uuid.NewString()
		openItemID := uuid.NewString()
		mustExec(`INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,party_id,document_date,currency_code,grand_total,created_by,updated_by) VALUES($1,$2,'SALES_INVOICE',$3,$4,$5,$6::date,'TRY',$7,$8,$8)`, docID, companyID, inv.docNo, branchID, partyID, inv.date, inv.amount, userID)
		mustExec(`INSERT INTO party_ledger_entries(id,company_id,party_id,currency,entry_type,source_type,source_id,idempotency_key,description,debit,credit,document_date,actor_user_id) VALUES($1,$2,$3,'TRY','SALES_INVOICE','document',$4,$5,$6,$7,0,$8::date,$9)`, ledgerID, companyID, partyID, docID, "invoice:"+inv.docNo, "Satış faturası "+inv.docNo, inv.amount, inv.date, userID)
		mustExec(`INSERT INTO finance_invoice_open_items(id,company_id,document_id,party_id,party_ledger_entry_id,side,currency,original_amount,base_currency,base_amount,document_date) VALUES($1,$2,$3,$4,$5,'RECEIVABLE','TRY',$6,'TRY',$6,$7::date)`, openItemID, companyID, docID, partyID, ledgerID, inv.amount, inv.date)
		openItemIDs = append(openItemIDs, openItemID)
	}

	svc := NewService(pool)
	today := time.Now().UTC()
	meta := func() identity.RequestMeta {
		return identity.RequestMeta{TraceID: "fin-fifo-test", IdempotencyKey: uuid.NewString()}
	}

	// 1) A collection of 250 with AutoAllocate: 100 -> INV-OLD, 80 -> INV-NEW,
	//    70 stays on the party ledger as an advance.
	// Description intentionally left blank: the party ledger / movement rows
	// require a non-empty description, so the service must default it.
	collection, err := svc.PostCollection(ctx, session, PaymentInput{
		PartyID:         partyID,
		AccountID:       accountID,
		PaymentMethod:   "CASH",
		Currency:        "TRY",
		Amount:          "250",
		ExchangeRate:    "1",
		TransactionDate: today,
		IdempotencyKey:  uuid.NewString(),
		AutoAllocate:    true,
	}, meta())
	if err != nil {
		t.Fatalf("post collection: %v", err)
	}
	if strings.TrimSpace(collection.Description) == "" {
		t.Fatal("blank description was not defaulted")
	}

	detail, err := svc.GetPaymentDetail(ctx, session, collection.ID)
	if err != nil {
		t.Fatalf("get collection detail: %v", err)
	}
	if len(detail.Allocations) != 2 {
		t.Fatalf("expected 2 allocations, got %d: %+v", len(detail.Allocations), detail.Allocations)
	}
	byOpenItem := map[string]string{}
	for _, a := range detail.Allocations {
		byOpenItem[a.OpenItemID] = a.Amount
	}
	if got := byOpenItem[openItemIDs[0]]; got != "100.0000" {
		t.Fatalf("INV-OLD allocation = %q, want 100.0000", got)
	}
	if got := byOpenItem[openItemIDs[1]]; got != "80.0000" {
		t.Fatalf("INV-NEW allocation = %q, want 80.0000", got)
	}
	if detail.UnappliedAmount != "70.0000" {
		t.Fatalf("unapplied amount = %q, want 70.0000", detail.UnappliedAmount)
	}

	// 2) A second collection posted as a pure advance, then distributed after the
	//    fact with AllocatePaymentFIFO. Only 70 of open items remain (INV-OLD had
	//    already taken 100 of its 100; wait, both are fully settled now) -> there
	//    is nothing left to allocate, so the command reports it.
	advance, err := svc.PostCollection(ctx, session, PaymentInput{
		PartyID:         partyID,
		AccountID:       accountID,
		PaymentMethod:   "CASH",
		Currency:        "TRY",
		Amount:          "50",
		ExchangeRate:    "1",
		Description:     "Avans tahsilat",
		TransactionDate: today,
		IdempotencyKey:  uuid.NewString(),
	}, meta())
	if err != nil {
		t.Fatalf("post advance collection: %v", err)
	}
	if _, err = svc.AllocatePaymentFIFO(ctx, session, advance.ID, meta()); err == nil {
		t.Fatal("expected AllocatePaymentFIFO to fail when no open items remain")
	}

	// 3) Add a third open invoice, then AllocatePaymentFIFO must consume it and
	//    be idempotent under a repeated key.
	docID := uuid.NewString()
	ledgerID := uuid.NewString()
	openItemID := uuid.NewString()
	mustExec(`INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,party_id,document_date,currency_code,grand_total,created_by,updated_by) VALUES($1,$2,'SALES_INVOICE','INV-3',$3,$4,'2026-08-01'::date,'TRY','40.0000',$5,$5)`, docID, companyID, branchID, partyID, userID)
	mustExec(`INSERT INTO party_ledger_entries(id,company_id,party_id,currency,entry_type,source_type,source_id,idempotency_key,description,debit,credit,document_date,actor_user_id) VALUES($1,$2,$3,'TRY','SALES_INVOICE','document',$4,'invoice:INV-3','Satış faturası INV-3','40.0000',0,'2026-08-01'::date,$5)`, ledgerID, companyID, partyID, docID, userID)
	mustExec(`INSERT INTO finance_invoice_open_items(id,company_id,document_id,party_id,party_ledger_entry_id,side,currency,original_amount,base_currency,base_amount,document_date) VALUES($1,$2,$3,$4,$5,'RECEIVABLE','TRY','40.0000','TRY','40.0000','2026-08-01'::date)`, openItemID, companyID, docID, partyID, ledgerID)

	key := meta()
	first, err := svc.AllocatePaymentFIFO(ctx, session, advance.ID, key)
	if err != nil {
		t.Fatalf("allocate fifo: %v", err)
	}
	if len(first) != 1 || first[0].OpenItemID != openItemID || first[0].Amount != "40.0000" {
		t.Fatalf("unexpected fifo allocation: %+v", first)
	}
	replay, err := svc.AllocatePaymentFIFO(ctx, session, advance.ID, key)
	if err != nil {
		t.Fatalf("allocate fifo replay: %v", err)
	}
	if len(replay) != 1 || replay[0].ID != first[0].ID {
		t.Fatalf("idempotent replay diverged: %+v vs %+v", replay, first)
	}

	advanceDetail, err := svc.GetPaymentDetail(ctx, session, advance.ID)
	if err != nil {
		t.Fatalf("advance detail: %v", err)
	}
	if advanceDetail.UnappliedAmount != "10.0000" {
		t.Fatalf("advance unapplied = %q, want 10.0000", advanceDetail.UnappliedAmount)
	}

	// 4) An unallocated original row must not be selected again when the
	// payment is reversed; only the still-active allocation gets a reversal.
	collectionDetail, err := svc.GetPaymentDetail(ctx, session, collection.ID)
	if err != nil {
		t.Fatalf("collection detail before unallocate: %v", err)
	}
	if len(collectionDetail.Allocations) < 2 {
		t.Fatalf("expected collection allocations before unallocate, got %+v", collectionDetail.Allocations)
	}
	unallocated, err := svc.UnallocatePayment(ctx, session, collection.ID, []string{collectionDetail.Allocations[0].ID}, meta())
	if err != nil {
		t.Fatalf("unallocate collection allocation: %v", err)
	}
	if len(unallocated) != 1 || unallocated[0].ReversalOfID == nil {
		t.Fatalf("unexpected unallocation result: %+v", unallocated)
	}
	reversal, err := svc.ReversePayment(ctx, session, collection.ID, uuid.NewString(), "unallocate then reverse", today, meta())
	if err != nil {
		t.Fatalf("reverse collection after unallocate: %v", err)
	}
	var reversalAllocationCount, originalChildCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM finance_payment_allocations WHERE company_id=$1 AND payment_id=$2 AND reversal_of_id IS NOT NULL`, companyID, reversal.ID).Scan(&reversalAllocationCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM finance_payment_allocations WHERE company_id=$1 AND reversal_of_id=$2`, companyID, collectionDetail.Allocations[0].ID).Scan(&originalChildCount); err != nil {
		t.Fatal(err)
	}
	if reversalAllocationCount != 1 || originalChildCount != 1 {
		t.Fatalf("payment reversal re-reversed an unallocated row: reversal allocations=%d original children=%d", reversalAllocationCount, originalChildCount)
	}
}
