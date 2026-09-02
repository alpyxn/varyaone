package commerce

import "testing"

func TestDirectionSelectsOverridePermissions(t *testing.T) {
	if got := DirectionSales.PriceOverridePermission(); got != "sales.price.override" {
		t.Fatalf("sales price permission = %q", got)
	}
	if got := DirectionPurchase.PriceOverridePermission(); got != "purchase.price.override" {
		t.Fatalf("purchase price permission = %q", got)
	}
	if got := DirectionPurchase.TaxOverridePermission(); got != "purchase.tax.override" {
		t.Fatalf("purchase tax permission = %q", got)
	}
	if DirectionSales.Valid() != true || Direction("OTHER").Valid() != false {
		t.Fatal("direction validation is wrong")
	}
}

// A price that matches a catalog candidate keeps that provenance; anything
// else is a manual override and must be treated as one.
func TestClassifyPriceRecognisesCatalogPrices(t *testing.T) {
	candidates := []PriceCandidate{
		{value: "", source: PriceSourceSpecial},
		{value: "100.00", source: PriceSourceLastPurchase},
		{value: "120", source: PriceSourceDefault},
	}
	if got := ClassifyPrice(candidates, "100"); got != PriceSourceLastPurchase {
		t.Fatalf("100 classified as %q, want %q", got, PriceSourceLastPurchase)
	}
	if got := ClassifyPrice(candidates, "120.000"); got != PriceSourceDefault {
		t.Fatalf("120.000 classified as %q, want %q", got, PriceSourceDefault)
	}
	if got := ClassifyPrice(candidates, "99"); got != "" {
		t.Fatalf("unmatched price classified as %q, want manual", got)
	}
	if got := ClassifyPrice(candidates, ""); got != "" {
		t.Fatalf("empty price classified as %q", got)
	}
}

// A foreign-currency document divides the base price by its own rate; a
// missing or zero rate must fail rather than quietly fall back to 1.
func TestConvertBasePriceRefusesUnusableRate(t *testing.T) {
	got, err := ConvertBasePrice("300", "2")
	if err != nil || got != "150" {
		t.Fatalf("ConvertBasePrice(300, 2) = %q, %v", got, err)
	}
	if _, err := ConvertBasePrice("300", "0"); err != ErrExchangeRateUnavailable {
		t.Fatalf("zero rate returned %v", err)
	}
	if _, err := ConvertBasePrice("300", ""); err != ErrExchangeRateUnavailable {
		t.Fatalf("empty rate returned %v", err)
	}
	if got, err := ConvertBasePrice("", "2"); err != nil || got != "" {
		t.Fatalf("empty price returned %q, %v", got, err)
	}
}
