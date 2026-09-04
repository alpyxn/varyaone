package finance

import (
	"errors"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
)

func TestFormatAmountUsesExactHalfUpRounding(t *testing.T) {
	tests := []struct {
		input string
		scale int
		want  string
	}{
		{input: "1.005", scale: 2, want: "1.01"},
		{input: "-1.005", scale: 2, want: "-1.01"},
		{input: "10", scale: 4, want: "10.0000"},
	}
	for _, test := range tests {
		got, err := FormatAmount(test.input, test.scale)
		if err != nil || got != test.want {
			t.Fatalf("FormatAmount(%q,%d)=%q,%v; want %q", test.input, test.scale, got, err, test.want)
		}
	}
}

func TestFIFOAllocationsSortsByDocumentDateAndKeepsRemainderUnallocated(t *testing.T) {
	items := []OpenItem{
		{ID: "new", DocumentDate: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), OpenAmount: "50"},
		{ID: "old", DocumentDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), OpenAmount: "40"},
	}
	allocations, err := FIFOAllocations(items, "100")
	if err != nil {
		t.Fatal(err)
	}
	if len(allocations) != 2 || allocations[0].OpenItemID != "old" || allocations[0].Amount != "40.0000" || allocations[1].OpenItemID != "new" || allocations[1].Amount != "50.0000" {
		t.Fatalf("unexpected FIFO allocations: %+v", allocations)
	}
}

func TestFIFOAllocationsPrefersDueDateAndPlacesUndatedItemsLast(t *testing.T) {
	dueLater := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	dueSoon := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	items := []OpenItem{
		{ID: "undated", DocumentDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), OpenAmount: "10"},
		{ID: "later", DocumentDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), DueDate: &dueLater, OpenAmount: "10"},
		{ID: "soon", DocumentDate: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), DueDate: &dueSoon, OpenAmount: "10"},
	}
	allocations, err := FIFOAllocations(items, "30")
	if err != nil {
		t.Fatal(err)
	}
	if len(allocations) != 3 || allocations[0].OpenItemID != "soon" || allocations[1].OpenItemID != "later" || allocations[2].OpenItemID != "undated" {
		t.Fatalf("unexpected due-date FIFO order: %+v", allocations)
	}
}

func TestFIFOAllocationsRejectsNonPositivePayment(t *testing.T) {
	_, err := FIFOAllocations(nil, "0")
	if !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("zero FIFO amount returned an unclassified error: %v", err)
	}
}

func TestDomainErrorPreservesStableCode(t *testing.T) {
	err := domainError(ErrPaymentAllocationExceedsOpenAmount, "açık tutarı aşamaz")
	if ErrorCode(err) != "PAYMENT_ALLOCATION_EXCEEDS_OPEN_AMOUNT" || !errors.Is(err, ErrPaymentAllocationExceedsOpenAmount) {
		t.Fatalf("domain error code/unwrap mismatch: code=%q err=%v", ErrorCode(err), err)
	}
}

func TestIdempotencyConflictHasStableCode(t *testing.T) {
	err := domainError(ErrIdempotencyConflict, "aynı anahtar farklı veriyle kullanıldı")
	if ErrorCode(err) != "IDEMPOTENCY_CONFLICT" || !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict code/unwrap mismatch: code=%q err=%v", ErrorCode(err), err)
	}
}

func TestPaymentRequestHashIncludesAllocationAndInstrumentPayload(t *testing.T) {
	amount, err := parsePositive("100", 4)
	if err != nil {
		t.Fatal(err)
	}
	rate, err := parsePositive("1", 10)
	if err != nil {
		t.Fatal(err)
	}
	base := PaymentInput{
		PartyID: "party", PaymentKind: "COLLECTION", PaymentMethod: "CHECK", Currency: "TRY",
		Amount: "100.00", ExchangeRate: "1.0", Description: "Tahsilat",
		TransactionDate: time.Date(2026, 8, 21, 12, 0, 0, 0, time.FixedZone("TR", 3*60*60)),
		Instrument:      &InstrumentInput{InstrumentType: "CHECK", InstrumentNo: "CHK-1", Currency: "TRY", Amount: "100"},
		Allocations:     []AllocationInput{{OpenItemID: "item-1", Amount: "50"}},
	}
	if first, second := paymentRequestHash(base, amount, rate), paymentRequestHash(base, amount, rate); first != second {
		t.Fatal("identical payment requests produced different hashes")
	}
	changed := base
	changed.Allocations = []AllocationInput{{OpenItemID: "item-2", Amount: "50"}}
	if paymentRequestHash(base, amount, rate) == paymentRequestHash(changed, amount, rate) {
		t.Fatal("allocation changes were not included in the payment request hash")
	}
	changed = base
	changed.Instrument = &InstrumentInput{InstrumentType: "CHECK", InstrumentNo: "CHK-2", Currency: "TRY", Amount: "100"}
	if paymentRequestHash(base, amount, rate) == paymentRequestHash(changed, amount, rate) {
		t.Fatal("instrument changes were not included in the payment request hash")
	}
	changed = base
	changed.Allocations = nil
	changed.AutoAllocate = true
	if paymentRequestHash(changed, amount, rate) == paymentRequestHash(func() PaymentInput { c := changed; c.AutoAllocate = false; return c }(), amount, rate) {
		t.Fatal("auto_allocate flag was not included in the payment request hash")
	}
}

func TestPaymentRequestHashTreatsBusinessDateAndAllocationOrderAsStable(t *testing.T) {
	amount, _ := parsePositive("10", 4)
	rate, _ := parsePositive("1", 10)
	firstID, secondID := uuid.NewString(), uuid.NewString()
	first := PaymentInput{
		PartyID: "party", PaymentKind: "COLLECTION", PaymentMethod: "OTHER", Currency: "TRY", Amount: "10",
		TransactionDate: time.Date(2026, 8, 30, 23, 30, 0, 0, time.FixedZone("TR", 3*60*60)),
		Allocations:     []AllocationInput{{OpenItemID: secondID, Amount: "2.0"}, {OpenItemID: firstID, Amount: "3"}},
	}
	second := first
	second.TransactionDate = time.Date(2026, 8, 30, 0, 15, 0, 0, time.UTC)
	second.Allocations = []AllocationInput{{OpenItemID: firstID, Amount: "3.0000"}, {OpenItemID: secondID, Amount: "2.0000"}}
	if paymentRequestHash(first, amount, rate) != paymentRequestHash(second, amount, rate) {
		t.Fatal("equivalent business-date/allocation requests produced different hashes")
	}
}

func TestReversalPartyLedgerAmountsSwapOriginalSide(t *testing.T) {
	if debit, credit := reversalPartyLedgerAmounts("COLLECTION", "10.0000"); debit != "10.0000" || credit != "0" {
		t.Fatalf("collection reversal amounts = debit %q credit %q", debit, credit)
	}
	if debit, credit := reversalPartyLedgerAmounts("PAYMENT", "10.0000"); debit != "0" || credit != "10.0000" {
		t.Fatalf("payment reversal amounts = debit %q credit %q", debit, credit)
	}
}

func TestNormalizeIBANUsesCountryNeutralMod97(t *testing.T) {
	got, err := NormalizeIBAN("gb82 west 1234 5698 7654 32")
	if err != nil || got != "GB82WEST12345698765432" {
		t.Fatalf("NormalizeIBAN()=%q,%v", got, err)
	}
	if _, err = NormalizeIBAN("GB82WEST12345698765431"); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("invalid checksum was accepted: %v", err)
	}
}
