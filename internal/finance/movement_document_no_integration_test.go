package finance

import (
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
)

// A movement had no name of its own: the detail screen fell back to the row's
// UUID for its heading. These tests guard the two halves of the fix -- a
// movement entered by hand gets its own KH- number, and a movement that came
// from a tahsilat reports that payment's document number -- so the UI never has
// to print an identifier again.

func TestManualAccountMovementGetsItsOwnDocumentNumber(t *testing.T) {
	f := newReturnFixture(t, "manualno")
	accountID := uuid.NewString()
	f.exec(`INSERT INTO finance_accounts(id,company_id,account_type,code,name,currency) VALUES($1,$2,'CASH','KASA-1','Merkez Kasa','TRY')`, accountID, f.companyID)

	transactionDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	first, err := f.service.PostManualAccountMovement(f.ctx, f.session, AccountMovementInput{
		AccountID: accountID, Direction: "IN", Amount: "1250", TransactionDate: transactionDate,
		Description: "Kasaya elden giriş", IdempotencyKey: uuid.NewString(),
	}, identity.RequestMeta{TraceID: "manualno"})
	if err != nil {
		t.Fatalf("post manual movement: %v", err)
	}
	if want := "KH-2026-000001"; first.SourceDocumentNo != want {
		t.Fatalf("first manual movement number = %q, want %q", first.SourceDocumentNo, want)
	}

	second, err := f.service.PostManualAccountMovement(f.ctx, f.session, AccountMovementInput{
		AccountID: accountID, Direction: "IN", Amount: "300", TransactionDate: transactionDate,
		Description: "İkinci giriş", IdempotencyKey: uuid.NewString(),
	}, identity.RequestMeta{TraceID: "manualno"})
	if err != nil {
		t.Fatalf("post second manual movement: %v", err)
	}
	if want := "KH-2026-000002"; second.SourceDocumentNo != want {
		t.Fatalf("second manual movement number = %q, want %q", second.SourceDocumentNo, want)
	}

	// The number survives a read, which is what the detail page actually calls.
	reread, err := f.service.GetAccountMovement(f.ctx, f.session, first.ID)
	if err != nil {
		t.Fatalf("get movement: %v", err)
	}
	if reread.SourceDocumentNo != first.SourceDocumentNo {
		t.Fatalf("re-read number = %q, want %q", reread.SourceDocumentNo, first.SourceDocumentNo)
	}
}

func TestPaymentMovementReportsThePaymentDocumentNumber(t *testing.T) {
	f := newReturnFixture(t, "paymentno")
	accountID := uuid.NewString()
	f.exec(`INSERT INTO finance_accounts(id,company_id,account_type,code,name,currency) VALUES($1,$2,'CASH','KASA-1','Merkez Kasa','TRY')`, accountID, f.companyID)

	payment, err := f.service.PostCollection(f.ctx, f.session, PaymentInput{
		PartyID: f.partyID, AccountID: accountID, PaymentMethod: "CASH", Currency: "TRY",
		Amount: "500", ExchangeRate: "1", TransactionDate: time.Now().UTC(), IdempotencyKey: uuid.NewString(),
	}, identity.RequestMeta{TraceID: "paymentno", IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatalf("post collection: %v", err)
	}
	if payment.DocumentNo == "" {
		t.Fatal("collection has no document number")
	}

	var movementID string
	if err = f.pool.QueryRow(f.ctx, `SELECT id::text FROM finance_account_movements WHERE company_id=$1 AND source_type='finance_payment' AND source_id=$2`, f.companyID, payment.ID).Scan(&movementID); err != nil {
		t.Fatalf("find payment movement: %v", err)
	}
	movement, err := f.service.GetAccountMovement(f.ctx, f.session, movementID)
	if err != nil {
		t.Fatalf("get movement: %v", err)
	}
	// The movement is a consequence of the payment, so it is referred to by the
	// payment's number rather than getting a second number of its own.
	if movement.SourceDocumentNo != payment.DocumentNo {
		t.Fatalf("movement number = %q, want the payment's %q", movement.SourceDocumentNo, payment.DocumentNo)
	}

	// The cari hareket the same payment wrote carries the number too, so
	// /cari/hareketler can name it without reaching for an id.
	var snapshotDocumentNo string
	if err = f.pool.QueryRow(f.ctx, `SELECT COALESCE(snapshot->>'document_no','') FROM party_ledger_entries WHERE company_id=$1 AND source_type='finance_payment' AND source_id=$2`, f.companyID, payment.ID).Scan(&snapshotDocumentNo); err != nil {
		t.Fatalf("find party ledger entry: %v", err)
	}
	if snapshotDocumentNo != payment.DocumentNo {
		t.Fatalf("party ledger document_no = %q, want %q", snapshotDocumentNo, payment.DocumentNo)
	}
}
