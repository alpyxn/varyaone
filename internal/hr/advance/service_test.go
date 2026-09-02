package advance

import (
	"errors"
	"testing"

	"github.com/alpyxn/varyaone/internal/identity"
)

func TestNormalizeAmountRequiresPositiveTwoDecimalTRYString(t *testing.T) {
	for _, value := range []string{"1", "1.0", "1.000", "0.00", "01.00", "-1.00", "1,00"} {
		if _, err := normalizeAmount(value); !errors.Is(err, identity.ErrValidation) {
			t.Errorf("normalizeAmount(%q) error = %v, want validation", value, err)
		}
	}
	if got, err := normalizeAmount(" 1250.00 "); err != nil || got != "1250.00" {
		t.Fatalf("valid amount = %q, %v", got, err)
	}
}

func TestPayloadHashIsStableAndPayloadSensitive(t *testing.T) {
	a := payloadHash(RepaymentInput{Amount: "10.00", TransactionDate: "2026-08-30", AccountID: "a", IdempotencyKey: "key"})
	b := payloadHash(RepaymentInput{Amount: "10.00", TransactionDate: "2026-08-30", AccountID: "a", IdempotencyKey: "key"})
	c := payloadHash(RepaymentInput{Amount: "11.00", TransactionDate: "2026-08-30", AccountID: "a", IdempotencyKey: "key"})
	if a != b {
		t.Fatal("same payload produced different hash")
	}
	if a == c {
		t.Fatal("different payload produced same hash")
	}
}
