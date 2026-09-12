package finance

import (
	"errors"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
)

func TestUnallocationAndReallocationPreserveHistoricalAging(t *testing.T) {
	f := newReturnFixture(t, "allocation_history")
	today := time.Now().UTC().Truncate(24 * time.Hour)
	past := today.AddDate(0, 0, -10)
	documentDate := past.AddDate(0, 0, -1).Format("2006-01-02")
	doc, _ := f.document("SALES_INVOICE", "HISTORY-1", documentDate, "100", "1")
	openItem := f.openItem(doc, documentDate, "100")
	payment, err := f.service.PostCollection(f.ctx, f.session, PaymentInput{
		PartyID: f.partyID, PaymentMethod: "OTHER", Currency: "TRY", Amount: "100",
		TransactionDate: past, IdempotencyKey: uuid.NewString(),
		Allocations: []AllocationInput{{OpenItemID: openItem, Amount: "100"}},
	}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	assertAging := func(date time.Time, want string) {
		t.Helper()
		report, err := f.service.PartyAging(f.ctx, f.session, date, f.partyID, "TRY", "RECEIVABLE")
		if err != nil {
			t.Fatal(err)
		}
		got := "0.0000"
		if len(report.Items) > 0 {
			got = report.Items[0].Total
		}
		if got != want {
			t.Fatalf("aging on %s = %s, want %s", date.Format("2006-01-02"), got, want)
		}
	}
	assertAging(past, "0.0000") // Backdated payment keeps its business date.
	var allocationID string
	if err = f.pool.QueryRow(f.ctx, `SELECT id FROM finance_payment_allocations WHERE company_id=$1 AND payment_id=$2`, f.companyID, payment.ID).Scan(&allocationID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.UnallocatePayment(f.ctx, f.session, payment.ID, []string{allocationID}, identity.RequestMeta{IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	assertAging(past, "0.0000")
	assertAging(today, "100.0000")
	// Applying the released credit to a different invoice must not backdate its
	// settlement or undo the historical settlement of the first invoice.
	doc, _ = f.document("SALES_INVOICE", "HISTORY-2", documentDate, "100", "1")
	second := f.openItem(doc, documentDate, "100")
	if _, err = f.service.AllocatePayment(f.ctx, f.session, payment.ID, []AllocationInput{{OpenItemID: second, Amount: "100"}}, identity.RequestMeta{IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	assertAging(past, "100.0000")
	assertAging(today, "100.0000")
	// Reject a reversal preceding the attribution changes instead of making
	// a payment and its allocations disagree in historical reports.
	reversalDate := today.AddDate(0, 0, -1)
	if _, err = f.service.ReversePayment(f.ctx, f.session, payment.ID, uuid.NewString(), "İptal", reversalDate, identity.RequestMeta{}); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("reversal before reallocation: %v", err)
	}
	if _, err = f.service.ReversePayment(f.ctx, f.session, payment.ID, uuid.NewString(), "İptal", today, identity.RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	assertAging(past, "100.0000")
	assertAging(reversalDate, "100.0000")
	assertAging(today, "200.0000")
}

func TestAutomaticAllocationsRespectBranchScopes(t *testing.T) {
	for _, kind := range []string{"COLLECTION", "PAYMENT"} {
		t.Run(kind, func(t *testing.T) {
			f := newReturnFixture(t, "fifo_scope")
			today := time.Now().UTC().Truncate(24 * time.Hour)
			makeItem := func(no string, days int) string {
				date := today.AddDate(0, 0, days).Format("2006-01-02")
				doc, _ := f.document("SALES_INVOICE", no, date, "100", "1")
				id := f.openItem(doc, date, "100")
				return id
			}
			if kind == "PAYMENT" {
				f.exec(`UPDATE parties SET is_supplier=true WHERE company_id=$1 AND id=$2`, f.companyID, f.partyID)
			}
			// This fixture creates ordinary receivables; payable behavior is covered
			// below by inserting supplier invoices and their immutable open items.
			if kind == "PAYMENT" {
				makeItem = func(no string, days int) string {
					doc, ledger, id := uuid.NewString(), uuid.NewString(), uuid.NewString()
					date := today.AddDate(0, 0, days).Format("2006-01-02")
					f.exec(`INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,party_id,document_date,currency_code,grand_total,status,posted_at,created_by,updated_by) VALUES($1,$2,'PURCHASE_INVOICE',$3,$4,$5,$6::date,'TRY',100,'POSTED',now(),$7,$7)`, doc, f.companyID, no, f.branchID, f.partyID, date, f.userID)
					f.exec(`INSERT INTO party_ledger_entries(id,company_id,party_id,currency,entry_type,source_type,source_id,idempotency_key,description,debit,credit,document_date,actor_user_id) VALUES($1,$2,$3,'TRY','PAYABLE','document',$4,$5,'Alış',0,100,$6::date,$7)`, ledger, f.companyID, f.partyID, doc, uuid.NewString(), date, f.userID)
					f.exec(`INSERT INTO finance_invoice_open_items(id,company_id,document_id,party_id,party_ledger_entry_id,side,currency,original_amount,base_currency,base_amount,document_date) VALUES($1,$2,$3,$4,$5,'PAYABLE','TRY',100,'TRY',100,$6::date)`, id, f.companyID, doc, f.partyID, ledger, date)
					return id
				}
			}
			hidden := makeItem("OUT-OF-SCOPE", -20)
			allowed := uuid.NewString()
			f.exec(`INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,'ALLOWED','Allowed')`, allowed, f.companyID)
			f.branchID = allowed
			visible := makeItem("IN-SCOPE", -10)
			f.exec(`INSERT INTO membership_branch_scopes(company_id,user_id,branch_id) VALUES($1,$2,$3)`, f.companyID, f.userID, allowed)
			input := PaymentInput{PaymentKind: kind, PartyID: f.partyID, PaymentMethod: "OTHER", Currency: "TRY", Amount: "40", TransactionDate: today, IdempotencyKey: uuid.NewString(), AutoAllocate: true}
			if _, err := f.service.PostPayment(f.ctx, f.session, input, identity.RequestMeta{}); err != nil {
				t.Fatal(err)
			}
			input.AutoAllocate = false
			input.Amount = "80"
			input.IdempotencyKey = uuid.NewString()
			payment, err := f.service.PostPayment(f.ctx, f.session, input, identity.RequestMeta{})
			if err != nil {
				t.Fatal(err)
			}
			allocations, err := f.service.AllocatePaymentFIFO(f.ctx, f.session, payment.ID, identity.RequestMeta{IdempotencyKey: uuid.NewString()})
			if err != nil {
				t.Fatal(err)
			}
			if len(allocations) != 1 || allocations[0].OpenItemID != visible || allocations[0].Amount != "60.0000" {
				t.Fatalf("scoped FIFO = %+v", allocations)
			}
			// Excess remains unapplied; it must never spill into the hidden invoice.
			if _, err = f.service.AllocatePayment(f.ctx, f.session, payment.ID, []AllocationInput{{OpenItemID: hidden, Amount: "20"}}, identity.RequestMeta{IdempotencyKey: uuid.NewString()}); !errors.Is(err, identity.ErrForbidden) {
				t.Fatalf("explicit hidden allocation: %v", err)
			}
			var count int
			if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM finance_payment_allocations WHERE company_id=$1 AND open_item_id=$2`, f.companyID, hidden).Scan(&count); err != nil || count != 0 {
				t.Fatalf("hidden invoice allocations=%d: %v", count, err)
			}
		})
	}
}

// Rows written before migration 157 have no effective_date. Read their
// existing metadata without rewriting the immutable ledger or allocations.
func TestLegacyAllocationDatesRemainReadable(t *testing.T) {
	f := newReturnFixture(t, "legacy_allocation_dates")
	today := time.Now().UTC().Truncate(24 * time.Hour)
	past := today.AddDate(0, 0, -10)
	doc, _ := f.document("SALES_INVOICE", "LEGACY", past.Format("2006-01-02"), "100", "1")
	item := f.openItem(doc, past.Format("2006-01-02"), "100")
	payment, err := f.service.PostCollection(f.ctx, f.session, PaymentInput{PartyID: f.partyID, PaymentMethod: "OTHER", Currency: "TRY", Amount: "100", TransactionDate: past, IdempotencyKey: uuid.NewString()}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	allocationID := uuid.NewString()
	f.exec(`INSERT INTO finance_payment_allocations(id,company_id,payment_id,party_id,target_type,target_id,open_item_id,currency,amount,idempotency_key,actor_user_id,allocated_at) VALUES($1,$2,$3,$4,'DOCUMENT',$5,$6,'TRY',100,$7,$8,$9)`, allocationID, f.companyID, payment.ID, f.partyID, doc, item, uuid.NewString(), f.userID, past)
	f.exec(`INSERT INTO finance_payment_allocations(id,company_id,payment_id,party_id,target_type,target_id,open_item_id,currency,amount,idempotency_key,reversal_of_id,actor_user_id,allocated_at) SELECT $1,company_id,payment_id,party_id,target_type,target_id,open_item_id,currency,amount,$2,id,actor_user_id,$3 FROM finance_payment_allocations WHERE company_id=$4 AND id=$5`, uuid.NewString(), uuid.NewString(), today, f.companyID, allocationID)
	for _, tc := range []struct {
		date time.Time
		want string
	}{{past, "0"}, {today, "100"}} {
		var open string
		if err = f.pool.QueryRow(f.ctx, `SELECT COALESCE(sum(open_amount),0)::text FROM finance_scheduled_dues($1,$2::date,$3)`, f.companyID, tc.date.Format("2006-01-02"), item).Scan(&open); err != nil {
			t.Fatal(err)
		}
		if mustRat(open).Cmp(mustRat(tc.want)) != 0 {
			t.Fatalf("legacy open on %s = %s, want %s", tc.date, open, tc.want)
		}
	}
	var untouched int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM finance_payment_allocations WHERE company_id=$1 AND effective_date IS NULL`, f.companyID).Scan(&untouched); err != nil || untouched != 2 {
		t.Fatalf("legacy rows changed: %d, %v", untouched, err)
	}
}
