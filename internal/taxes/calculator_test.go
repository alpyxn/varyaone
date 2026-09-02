package taxes

import "testing"

func TestCalculateExclusiveTax(t *testing.T) {
	result := mustCalculate(t, TaxCalculationInput{
		Lines: []TaxCalculationLine{{
			UnitPrice: "100",
			Quantity:  "2",
			Components: []TaxComponent{{
				Code:            "VAT",
				CalculationType: TaxPercentage,
				Rate:            "18",
			}},
		}},
		RoundScale: 2,
	})

	assertAmounts(t, result, "200", "0", "200", "36", "236", "236")
}

func TestCalculateInclusiveTax(t *testing.T) {
	result := mustCalculate(t, TaxCalculationInput{
		TaxMode: TaxInclusive,
		Lines: []TaxCalculationLine{{
			UnitPrice: "118",
			Quantity:  "1",
			Components: []TaxComponent{{
				CalculationType: TaxPercentage,
				Rate:            "18",
			}},
		}},
		RoundScale: 2,
	})

	assertAmounts(t, result, "118", "0", "100", "18", "118", "118")
}

func TestCalculateDiscountIsAppliedBeforeTax(t *testing.T) {
	result := mustCalculate(t, TaxCalculationInput{
		Lines: []TaxCalculationLine{{
			UnitPrice: "100",
			Quantity:  "2",
			Discount:  Discount{Kind: DiscountPercent, Amount: "10"},
			Components: []TaxComponent{{
				CalculationType: TaxPercentage,
				Rate:            "20",
			}},
		}},
		RoundScale: 2,
	})

	assertAmounts(t, result, "200", "20", "180", "36", "216", "216")
}

func TestCalculateMultiplePercentageAndQuantityComponents(t *testing.T) {
	result := mustCalculate(t, TaxCalculationInput{
		Lines: []TaxCalculationLine{{
			UnitPrice: "100",
			Quantity:  "2",
			Components: []TaxComponent{
				{Code: "VAT", CalculationType: TaxPercentage, Rate: "18"},
				{Code: "FEE", CalculationType: TaxQuantityBased, Rate: "2.5"},
			},
		}},
		RoundScale: 2,
	})

	assertAmounts(t, result, "200", "0", "200", "41", "241", "241")
	if len(result.Components) != 2 || result.Components[0].Amount != "36" || result.Components[1].Amount != "5" {
		t.Fatalf("unexpected component results: %#v", result.Components)
	}
}

func TestCalculateQuantityBasedTaxUsesQuantity(t *testing.T) {
	result := mustCalculate(t, TaxCalculationInput{
		Lines: []TaxCalculationLine{{
			UnitPrice: "10",
			Quantity:  "4",
			Components: []TaxComponent{{
				CalculationType: TaxQuantityBased,
				Rate:            "2.5",
			}},
		}},
		RoundScale: 2,
	})

	assertAmounts(t, result, "40", "0", "40", "10", "50", "50")
	if result.Components[0].BaseAmount != "4" {
		t.Fatalf("quantity tax base should be quantity, got %s", result.Components[0].BaseAmount)
	}
}

func TestCalculateWithholdingRatioReducesPayableOnly(t *testing.T) {
	numerator, denominator := 1, 2
	result := mustCalculate(t, TaxCalculationInput{
		Lines: []TaxCalculationLine{{
			UnitPrice: "100",
			Quantity:  "1",
			Components: []TaxComponent{{
				CalculationType:        TaxPercentage,
				Rate:                   "20",
				Withholding:            true,
				WithholdingNumerator:   &numerator,
				WithholdingDenominator: &denominator,
			}},
		}},
		RoundScale: 2,
	})

	assertAmounts(t, result, "100", "0", "100", "20", "120", "110")
	if result.WithholdingAmount != "10" || result.Components[0].WithholdingAmount != "10" {
		t.Fatalf("unexpected withholding amounts: result=%s component=%s", result.WithholdingAmount, result.Components[0].WithholdingAmount)
	}
}

func TestCalculateExemptionProducesZeroTax(t *testing.T) {
	result := mustCalculate(t, TaxCalculationInput{
		Lines: []TaxCalculationLine{{
			UnitPrice: "100",
			Quantity:  "1",
			Components: []TaxComponent{{
				CalculationType: TaxPercentage,
				Rate:            "20",
				Exempt:          true,
			}},
		}},
		RoundScale: 2,
	})

	assertAmounts(t, result, "100", "0", "100", "0", "100", "100")
	if !result.Components[0].Exempt || result.Components[0].Amount != "0" {
		t.Fatalf("unexpected exemption result: %#v", result.Components[0])
	}
}

func TestCalculateHalfUpRoundingAndExactDecimal(t *testing.T) {
	result := mustCalculate(t, TaxCalculationInput{
		Lines: []TaxCalculationLine{{
			UnitPrice: "100.05",
			Quantity:  "1",
			Components: []TaxComponent{{
				CalculationType: TaxPercentage,
				Rate:            "10",
			}},
		}},
		RoundScale: 2,
	})
	if result.TaxAmount != "10.01" {
		t.Fatalf("half-up rounding failed: got %s", result.TaxAmount)
	}

	exact := mustCalculate(t, TaxCalculationInput{
		Lines:      []TaxCalculationLine{{UnitPrice: "9007199254740993.1234", Quantity: "1"}},
		RoundScale: 4,
	})
	if exact.TotalAmount != "9007199254740993.1234" {
		t.Fatalf("exact decimal lost precision: got %s", exact.TotalAmount)
	}
}

func TestCalculateRejectsInvalidDecimalAndDiscount(t *testing.T) {
	_, err := Calculate(TaxCalculationInput{
		Lines:      []TaxCalculationLine{{UnitPrice: "1e3", Quantity: "1"}},
		RoundScale: 2,
	})
	if err != ErrInvalidTaxCalculation {
		t.Fatalf("expected invalid decimal error, got %v", err)
	}

	_, err = Calculate(TaxCalculationInput{
		Lines: []TaxCalculationLine{{
			UnitPrice: "10", Quantity: "1",
			Discount: Discount{Kind: DiscountFixed, Amount: "10.01"},
		}},
		RoundScale: 2,
	})
	if err != ErrDiscountExceedsTaxBase {
		t.Fatalf("expected discount error, got %v", err)
	}
}

func mustCalculate(t *testing.T, input TaxCalculationInput) TaxCalculationResult {
	t.Helper()
	result, err := Calculate(input)
	if err != nil {
		t.Fatalf("Calculate() error = %v", err)
	}
	return result
}

func assertAmounts(t *testing.T, result TaxCalculationResult, gross, discount, taxable, tax, total, payable string) {
	t.Helper()
	actual := []string{result.GrossAmount, result.DiscountAmount, result.TaxableAmount, result.TaxAmount, result.TotalAmount, result.PayableAmount}
	expected := []string{gross, discount, taxable, tax, total, payable}
	for i := range expected {
		if actual[i] != expected[i] {
			t.Fatalf("amount %d: got %q want %q; result=%#v", i, actual[i], expected[i], result)
		}
	}
}
