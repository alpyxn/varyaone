package taxes

import (
	"errors"
	"testing"

	"github.com/alpyxn/varyaone/internal/identity"
)

func TestValidateDefinitionRequiresSourceMetadata(t *testing.T) {
	err := validateDefinition(TaxDefinition{Code: "KDV", Name: "Katma Değer Vergisi"})
	if !errors.Is(err, identity.ErrValidation) {
		t.Fatal("definition without source was accepted")
	}
}

func TestValidateRateRequiresDateAndDecimalRate(t *testing.T) {
	if err := validateRate(TaxRate{TaxDefinitionID: "not-a-uuid", Rate: "18", ValidFrom: "2026-01-01"}); err == nil {
		t.Fatal("invalid definition ID was accepted")
	}
	if err := validateRate(TaxRate{TaxDefinitionID: "00000000-0000-4000-8000-000000000001", Rate: "18.123456789", ValidFrom: "2026-01-01"}); err == nil {
		t.Fatal("rate beyond supported scale was accepted")
	}
	if err := validateRate(TaxRate{TaxDefinitionID: "00000000-0000-4000-8000-000000000001", Rate: "18", ValidFrom: "2026-01-01", ValidTo: func() *string { value := "2025-12-31"; return &value }()}); err == nil {
		t.Fatal("inverted rate period was accepted")
	}
}

func TestQuantityBasedTaxAllowsUnitAmountAbovePercentageLimit(t *testing.T) {
	input := TaxRate{
		TaxDefinitionID: "00000000-0000-4000-8000-000000000001",
		Rate:            "125.50",
		CalculationType: "QUANTITY_BASED",
		ValidFrom:       "2026-01-01",
	}
	if err := validateRate(input); err != nil {
		t.Fatalf("quantity-based unit amount was rejected: %v", err)
	}
	input.CalculationType = "PERCENTAGE"
	if err := validateRate(input); err == nil {
		t.Fatal("percentage tax above 100 was accepted")
	}
}
