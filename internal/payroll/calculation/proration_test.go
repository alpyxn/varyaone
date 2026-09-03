package calculation

import "testing"

// A minimum-wage employee who works half the month owes no income or stamp tax:
// the statutory exemptions have to shrink with the paid days, exactly as the
// wage does. Charging the full monthly exemption base is harmless here, but the
// same mistake overstates the exemption for a partial month at a higher wage.
func TestPartialMonthMinimumWagePaysNoTax(t *testing.T) {
	ctx := baseContext(scheme("NO_DISCOUNT", "0.2175"))
	ctx.FullMonth = false
	ctx.PaidDays = d("15")
	ctx.SGKDays = 15
	result, err := (PayrollCalculator{}).Calculate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, "gross", result.Gross, "16515.00")
	assertAmount(t, "income tax", result.IncomeTax, "0.00")
	assertAmount(t, "stamp tax", result.StampTax, "0.00")
}

// The exemption for a half month must be half the monthly one, not the whole of
// it — otherwise a partial month at a wage above minimum shelters twice as much
// income as the law allows.
func TestPartialMonthExemptionIsProrated(t *testing.T) {
	full := baseContext(scheme("NO_DISCOUNT", "0.2175"))
	full.MonthlyGross = d("120000")
	fullResult, err := (PayrollCalculator{}).Calculate(full)
	if err != nil {
		t.Fatal(err)
	}

	half := full
	half.FullMonth = false
	half.PaidDays = d("15")
	half.SGKDays = 15
	halfResult, err := (PayrollCalculator{}).Calculate(half)
	if err != nil {
		t.Fatal(err)
	}
	if halfResult.IncomeTaxExemption.Cmp(fullResult.IncomeTaxExemption) >= 0 {
		t.Fatalf("half-month exemption %s is not below the full-month %s",
			halfResult.IncomeTaxExemption, fullResult.IncomeTaxExemption)
	}
	if halfResult.StampTaxExemption.Cmp(fullResult.StampTaxExemption) >= 0 {
		t.Fatalf("half-month stamp exemption %s is not below the full-month %s",
			halfResult.StampTaxExemption, fullResult.StampTaxExemption)
	}
}

// Reconciliation must actually compare the itemised rows with the headline
// totals, so a payslip can never render numbers that disagree with the run.
func TestComponentRowsReconcileWithTotals(t *testing.T) {
	ctx := baseContext(scheme("NO_DISCOUNT", "0.2175"))
	ctx.MonthlyGross = d("80000")
	ctx.Components = []InputComponent{
		{Code: "BONUS", Name: "Prim", Kind: "EARNING", Ownership: "MANUAL", Amount: d("5000"), WorkedDays: d("30")},
		{Code: "ADVANCE", Name: "Avans", Kind: "DEDUCTION", Ownership: "MANUAL", Amount: d("2000"), WorkedDays: d("30")},
	}
	result, err := (PayrollCalculator{}).Calculate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	earnings := zero()
	for _, c := range result.Components {
		if c.Kind == "EARNING" {
			earnings = earnings.Add(c.Amount)
		}
	}
	if earnings.Cmp(result.Gross) != 0 {
		t.Fatalf("earning rows sum to %s, gross is %s", earnings, result.Gross)
	}
}
