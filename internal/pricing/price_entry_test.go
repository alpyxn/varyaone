package pricing

import (
	"errors"
	"testing"

	"github.com/alpyxn/varyaone/internal/identity"
)

func TestValidatePriceEntryAcceptsVariantTarget(t *testing.T) {
	variantID := "00000000-0000-4000-8000-000000000003"
	entry := PriceEntry{PriceListID: "00000000-0000-4000-8000-000000000001", ItemID: "00000000-0000-4000-8000-000000000002", VariantID: &variantID, ValidFrom: "2026-01-01", UnitPrice: "10"}
	if err := validateEntry(entry); err != nil {
		t.Fatalf("variant price target rejected: %v", err)
	}
	invalid := "not-a-uuid"
	entry.VariantID = &invalid
	if err := validateEntry(entry); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("invalid variant target returned %v", err)
	}
}
