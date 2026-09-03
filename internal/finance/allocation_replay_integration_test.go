package finance

import (
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
)

// TestReallocateAfterUnallocateWritesANewAllocation guards the silent no-op:
// the per-allocation idempotency key used to be the bare payment+open-item
// pair, so allocating again after an unallocate matched the original -- by then
// reversed -- row and replayed it. The API answered 200 with an allocation
// while nothing was written, the invoice stayed open and the payment stayed an
// advance. A second command must write a second row.
func TestReallocateAfterUnallocateWritesANewAllocation(t *testing.T) {
	f := newReturnFixture(t, "realloc")
	accountID := uuid.NewString()
	f.exec(`INSERT INTO finance_accounts(id,company_id,account_type,code,name,currency) VALUES($1,$2,'CASH','KASA-1','Merkez Kasa','TRY')`, accountID, f.companyID)
	invoiceID, _ := f.document("SALES_INVOICE", "SF-1", "2026-01-10", "1000", "10")
	openItemID := f.openItem(invoiceID, "2026-01-10", "1000.0000")

	meta := func() identity.RequestMeta {
		return identity.RequestMeta{TraceID: "realloc-test", IdempotencyKey: uuid.NewString()}
	}
	payment, err := f.service.PostCollection(f.ctx, f.session, PaymentInput{
		PartyID: f.partyID, AccountID: accountID, PaymentMethod: "CASH", Currency: "TRY",
		Amount: "1000", ExchangeRate: "1", TransactionDate: time.Now().UTC(), IdempotencyKey: uuid.NewString(),
	}, meta())
	if err != nil {
		t.Fatalf("post collection: %v", err)
	}

	first, err := f.service.AllocatePayment(f.ctx, f.session, payment.ID, []AllocationInput{{OpenItemID: openItemID, Amount: "400"}}, meta())
	if err != nil {
		t.Fatalf("first allocation: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first allocation returned %d rows, want 1", len(first))
	}
	if got := f.openAmounts()["SF-1"]; got != "600.0000" {
		t.Fatalf("open amount after allocating 400 = %q, want 600.0000", got)
	}

	if _, err = f.service.UnallocatePayment(f.ctx, f.session, payment.ID, []string{first[0].ID}, meta()); err != nil {
		t.Fatalf("unallocate: %v", err)
	}
	if got := f.openAmounts()["SF-1"]; got != "1000.0000" {
		t.Fatalf("open amount after unallocating = %q, want 1000.0000", got)
	}

	// Re-allocating the same amount to the same invoice from the same payment.
	second, err := f.service.AllocatePayment(f.ctx, f.session, payment.ID, []AllocationInput{{OpenItemID: openItemID, Amount: "400"}}, meta())
	if err != nil {
		t.Fatalf("re-allocation after unallocate: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("re-allocation returned %d rows, want 1", len(second))
	}
	if second[0].ID == first[0].ID {
		t.Fatal("re-allocation replayed the reversed allocation instead of writing a new one")
	}
	if got := f.openAmounts()["SF-1"]; got != "600.0000" {
		t.Fatalf("open amount after re-allocating 400 = %q, want 600.0000", got)
	}

	// A different amount to the same invoice used to be a permanent 409.
	third, err := f.service.AllocatePayment(f.ctx, f.session, payment.ID, []AllocationInput{{OpenItemID: openItemID, Amount: "150"}}, meta())
	if err != nil {
		t.Fatalf("second allocation with a different amount: %v", err)
	}
	if len(third) != 1 {
		t.Fatalf("second allocation returned %d rows, want 1", len(third))
	}
	if got := f.openAmounts()["SF-1"]; got != "450.0000" {
		t.Fatalf("open amount after allocating 400+150 = %q, want 450.0000", got)
	}

	// A genuine retry -- the same command under the same Idempotency-Key -- must
	// still replay rather than allocate twice.
	retryMeta := identity.RequestMeta{TraceID: "realloc-test", IdempotencyKey: uuid.NewString()}
	if _, err = f.service.AllocatePayment(f.ctx, f.session, payment.ID, []AllocationInput{{OpenItemID: openItemID, Amount: "100"}}, retryMeta); err != nil {
		t.Fatalf("fourth allocation: %v", err)
	}
	if _, err = f.service.AllocatePayment(f.ctx, f.session, payment.ID, []AllocationInput{{OpenItemID: openItemID, Amount: "100"}}, retryMeta); err != nil {
		t.Fatalf("retry of the fourth allocation: %v", err)
	}
	if got := f.openAmounts()["SF-1"]; got != "350.0000" {
		t.Fatalf("open amount after a retried 100 allocation = %q, want 350.0000 (the retry must not allocate twice)", got)
	}
}

// TestAccountStatementBalancesCoverTheWholePeriod guards the ekstre header:
// closing balance used to be a running total over the returned page, so a
// period with more movements than the page size reported the balance after the
// first `limit` rows as the period's closing balance.
func TestAccountStatementBalancesCoverTheWholePeriod(t *testing.T) {
	f := newReturnFixture(t, "statement")
	accountID := uuid.NewString()
	f.exec(`INSERT INTO finance_accounts(id,company_id,account_type,code,name,currency) VALUES($1,$2,'CASH','KASA-1','Merkez Kasa','TRY')`, accountID, f.companyID)

	// One movement before the window (the opening balance) and twelve inside it.
	f.exec(`INSERT INTO finance_account_movements(id,company_id,account_id,movement_kind,direction,currency,amount,transaction_date,source_type,source_id,idempotency_key,description,exchange_rate,base_currency,base_amount) VALUES($1,$2,$3,'OPENING_BALANCE','IN','TRY',500,'2026-01-05','finance_account_movement',$1,'open','Açılış',1,'TRY',500)`,
		uuid.NewString(), f.companyID, accountID)
	for index := 0; index < 12; index++ {
		f.exec(`INSERT INTO finance_account_movements(id,company_id,account_id,movement_kind,direction,currency,amount,transaction_date,source_type,source_id,idempotency_key,description,exchange_rate,base_currency,base_amount) VALUES($1,$2,$3,'MANUAL_IN','IN','TRY',10,'2026-02-10','finance_account_movement',$1,$4,'Hareket',1,'TRY',10)`,
			uuid.NewString(), f.companyID, accountID, "mv-"+time.Now().Format("150405.000000000")+"-"+string(rune('a'+index)))
	}
	// A movement after the window must not reach either balance.
	f.exec(`INSERT INTO finance_account_movements(id,company_id,account_id,movement_kind,direction,currency,amount,transaction_date,source_type,source_id,idempotency_key,description,exchange_rate,base_currency,base_amount) VALUES($1,$2,$3,'MANUAL_IN','IN','TRY',999,'2026-04-01','finance_account_movement',$1,'after','Sonraki',1,'TRY',999)`,
		uuid.NewString(), f.companyID, accountID)

	from, _ := time.Parse("2006-01-02", "2026-02-01")
	to, _ := time.Parse("2006-01-02", "2026-02-28")
	// A page smaller than the period: the header must still cover all twelve.
	statement, err := f.service.AccountStatement(f.ctx, f.session, accountID, &from, &to, 5)
	if err != nil {
		t.Fatalf("account statement: %v", err)
	}
	if len(statement.Items) != 5 {
		t.Fatalf("page returned %d rows, want the requested 5", len(statement.Items))
	}
	if statement.OpeningBalance != "500.0000" {
		t.Fatalf("opening balance = %q, want 500.0000", statement.OpeningBalance)
	}
	if statement.ClosingBalance != "620.0000" {
		t.Fatalf("closing balance = %q, want 620.0000 (500 opening + 12 x 10, not just the page)", statement.ClosingBalance)
	}
}
