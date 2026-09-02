package taxes

import (
	"errors"
	"math/big"
	"testing"
)

// A tiered discount is not the sum of its rates: %10 then %5 on 1000 leaves
// 855, not 850. Each step applies to what the previous one left.
func TestCascadingLineDiscountsApplyToTheRemainder(t *testing.T) {
	result := mustCalculate(t, TaxCalculationInput{
		Lines: []TaxCalculationLine{{
			UnitPrice: "1000",
			Quantity:  "1",
			Discounts: []TaxDiscount{
				{Kind: DiscountPercent, Amount: "10"},
				{Kind: DiscountPercent, Amount: "5"},
			},
			Components: []TaxComponent{{CalculationType: TaxPercentage, Rate: "20"}},
		}},
		RoundScale: 2,
	})

	assertAmounts(t, result, "1000", "145", "855", "171", "1026", "1026")
	if result.Lines[0].LineDiscountAmount != "145" || result.Lines[0].HeaderDiscountAmount != "0" {
		t.Fatalf("unexpected discount split: %#v", result.Lines[0])
	}
}

// A fixed discount in the chain is capped by what is left, not by the original
// gross, so a chain can never take more than the line is worth.
func TestCascadingDiscountChainCannotExceedTheLine(t *testing.T) {
	_, err := Calculate(TaxCalculationInput{
		Lines: []TaxCalculationLine{{
			UnitPrice: "100",
			Quantity:  "1",
			Discounts: []TaxDiscount{
				{Kind: DiscountPercent, Amount: "50"},
				{Kind: DiscountFixed, Amount: "60"},
			},
		}},
		RoundScale: 2,
	})
	if !errors.Is(err, ErrDiscountExceedsTaxBase) {
		t.Fatalf("expected discount overflow, got %v", err)
	}
}

// The document discount must reach the tax base: tax is computed after it, not
// on the undiscounted line.
func TestHeaderDiscountIsAppliedBeforeTax(t *testing.T) {
	result := mustCalculate(t, TaxCalculationInput{
		Lines: []TaxCalculationLine{
			{UnitPrice: "100", Quantity: "1", Components: []TaxComponent{{CalculationType: TaxPercentage, Rate: "20"}}},
			{UnitPrice: "300", Quantity: "1", Components: []TaxComponent{{CalculationType: TaxPercentage, Rate: "20"}}},
		},
		HeaderDiscounts: []TaxDiscount{{Kind: DiscountPercent, Amount: "10"}},
		RoundScale:      2,
	})

	// 400 gross, 40 document discount, 360 taxable, 72 VAT.
	assertAmounts(t, result, "400", "40", "360", "72", "432", "432")
	if result.HeaderDiscountAmount != "40" || result.LineDiscountAmount != "0" {
		t.Fatalf("unexpected document discount totals: %#v", result)
	}
	// Shares are proportional to the line bases: 100/400 and 300/400.
	if result.Lines[0].HeaderDiscountAmount != "10" || result.Lines[1].HeaderDiscountAmount != "30" {
		t.Fatalf("unexpected header discount shares: %#v", result.Lines)
	}
}

// The distributed shares must add up to the document discount to the last
// kuruş, including when the proportional split does not divide evenly.
func TestHeaderDiscountSharesReconcileExactly(t *testing.T) {
	result := mustCalculate(t, TaxCalculationInput{
		Lines: []TaxCalculationLine{
			{UnitPrice: "10", Quantity: "1"},
			{UnitPrice: "10", Quantity: "1"},
			{UnitPrice: "10", Quantity: "1"},
		},
		HeaderDiscounts: []TaxDiscount{{Kind: DiscountFixed, Amount: "10"}},
		RoundScale:      2,
	})

	// 10 split three ways is 3.3333...; the shares must still total 10.
	sum := new(big.Rat)
	for _, line := range result.Lines {
		share, ok := new(big.Rat).SetString(line.HeaderDiscountAmount)
		if !ok {
			t.Fatalf("unparsable share %q", line.HeaderDiscountAmount)
		}
		sum.Add(sum, share)
	}
	if got := formatRounded(sum, 2); got != "10" {
		t.Fatalf("shares total %s, want 10; lines=%#v", got, result.Lines)
	}
	if result.HeaderDiscountAmount != "10" {
		t.Fatalf("document discount total = %q, want 10", result.HeaderDiscountAmount)
	}
	if result.TaxableAmount != "20" {
		t.Fatalf("taxable = %q, want 20", result.TaxableAmount)
	}
}

// A document discount larger than the document is a business error, not a
// negative tax base.
func TestHeaderDiscountCannotExceedDocument(t *testing.T) {
	_, err := Calculate(TaxCalculationInput{
		Lines:           []TaxCalculationLine{{UnitPrice: "100", Quantity: "1"}},
		HeaderDiscounts: []TaxDiscount{{Kind: DiscountFixed, Amount: "150"}},
		RoundScale:      2,
	})
	if !errors.Is(err, ErrDiscountExceedsTaxBase) {
		t.Fatalf("expected document discount overflow, got %v", err)
	}
}

// Line and document discounts stack: the document discount works on what the
// line discounts left behind.
func TestLineAndHeaderDiscountsStack(t *testing.T) {
	result := mustCalculate(t, TaxCalculationInput{
		Lines: []TaxCalculationLine{{
			UnitPrice:  "1000",
			Quantity:   "1",
			Discounts:  []TaxDiscount{{Kind: DiscountPercent, Amount: "10"}},
			Components: []TaxComponent{{CalculationType: TaxPercentage, Rate: "20"}},
		}},
		HeaderDiscounts: []TaxDiscount{{Kind: DiscountPercent, Amount: "10"}},
		RoundScale:      2,
	})

	// 1000 - 100 line = 900; 900 - 90 document = 810 taxable; VAT 162.
	assertAmounts(t, result, "1000", "190", "810", "162", "972", "972")
	if result.Lines[0].LineDiscountAmount != "100" || result.Lines[0].HeaderDiscountAmount != "90" {
		t.Fatalf("unexpected discount split: %#v", result.Lines[0])
	}
}
