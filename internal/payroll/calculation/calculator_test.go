package calculation

import (
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/money"
	"github.com/alpyxn/varyaone/internal/payroll/legislation"
)

func d(value string) money.Decimal {
	parsed, err := money.ParseDecimal(value, 8)
	if err != nil {
		panic(err)
	}
	return parsed
}

func pack2026() *legislation.Pack {
	upper1, upper2, upper3, upper4 := d("190000"), d("400000"), d("1500000"), d("5300000")
	return &legislation.Pack{ID: "pack-2026", Code: "TR-2026", Version: 1,
		EffectiveFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EffectiveTo: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		MinimumMonthlyGross: d("33030"), SGKDailyFloor: d("1101"), SGKDailyCeiling: d("9909"), StampTaxRate: d("0.00759"),
		IncomeTaxBrackets: []legislation.IncomeTaxBracket{{UpperBound: &upper1, Rate: d("0.15")}, {UpperBound: &upper2, Rate: d("0.20")}, {UpperBound: &upper3, Rate: d("0.27")}, {UpperBound: &upper4, Rate: d("0.35")}, {Rate: d("0.40")}},
	}
}

func scheme(code, employerSGK string) *legislation.ContributionScheme {
	return &legislation.ContributionScheme{Code: code, EmployeeSGKRate: d("0.14"), EmployeeUnemploymentRate: d("0.01"), EmployerSGKRate: d(employerSGK), EmployerUnemploymentRate: d("0.02")}
}

func baseContext(contribution *legislation.ContributionScheme) Context {
	return Context{PeriodDate: time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC), RunType: "REGULAR", WageType: "GROSS", WagePeriod: "MONTHLY", Currency: "TRY", WorkType: "FULL_TIME", SGKStatus: "4A", MonthlyGross: d("33030"), PaidDays: d("30"), SGKDays: 30, FullMonth: true, PriorEmployerTaxPolicy: "SEPARATE", Pack: pack2026(), ContributionScheme: contribution}
}

func TestOfficial2026MinimumWageGolden(t *testing.T) {
	tests := []struct{ name, rate, cost string }{{"without discount", "0.2175", "40874.63"}, {"other sector", "0.1975", "40214.03"}, {"manufacturing", "0.1675", "39223.13"}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := (PayrollCalculator{}).Calculate(baseContext(scheme(test.name, test.rate)))
			if err != nil {
				t.Fatal(err)
			}
			assertAmount(t, "employee SGK", result.EmployeeSGK, "4624.20")
			assertAmount(t, "unemployment", result.EmployeeUnemployment, "330.30")
			assertAmount(t, "income tax", result.IncomeTax, "0.00")
			assertAmount(t, "stamp tax", result.StampTax, "0.00")
			assertAmount(t, "net", result.Net, "28075.50")
			assertAmount(t, "employer cost", result.EmployerCost, test.cost)
		})
	}
}

func TestProgressiveTaxUsesCumulativeDelta(t *testing.T) {
	ctx := baseContext(scheme("NO_DISCOUNT", "0.2175"))
	ctx.MonthlyGross = d("100000")
	ctx.PriorCumulativeTaxBase = d("180000")
	ctx.MinimumWagePriorBase = d("280755")
	result, err := (PayrollCalculator{}).Calculate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.IncomeTax.Sign() <= 0 {
		t.Fatalf("expected tax above exemption: %s", result.IncomeTax.String())
	}
}

func TestUnsupportedRunAndMissing2027PackAreExplicit(t *testing.T) {
	ctx := baseContext(scheme("NO_DISCOUNT", "0.2175"))
	ctx.RunType = "CORRECTION"
	_, err := (PayrollCalculator{}).Calculate(ctx)
	if !ErrorIsCode(err, ErrRunTypeNotSupported) {
		t.Fatalf("error=%v", err)
	}
	ctx = baseContext(scheme("NO_DISCOUNT", "0.2175"))
	ctx.PeriodDate = time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC)
	_, err = (PayrollCalculator{}).Calculate(ctx)
	if !ErrorIsCode(err, ErrLegislationNotFound) {
		t.Fatalf("error=%v", err)
	}
}

func TestPartialMonthUsesThirtyDayWageAndSGKDayCap(t *testing.T) {
	ctx := baseContext(scheme("NO_DISCOUNT", "0.2175"))
	ctx.FullMonth = false
	ctx.PaidDays = d("10")
	ctx.SGKDays = 10
	result, err := (PayrollCalculator{}).Calculate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, "base wage", result.Gross, "11010.00")
	assertAmount(t, "sgk base", result.SGKBase, "11010.00")
}

func TestPreviewWorksFromBareContext(t *testing.T) {
	// Mirrors the wage-preview HTTP handler: only Pack + ContributionScheme are
	// set, PeriodDate is left zero and must be defaulted from the pack.
	ctx := Context{Pack: pack2026(), ContributionScheme: scheme("NO_DISCOUNT", "0.2175")}
	result, err := (PayrollCalculator{}).Preview(ctx, "GROSS", d("33030"))
	if err != nil {
		t.Fatalf("bare-context preview failed: %v", err)
	}
	assertAmount(t, "net", result.Net, "28075.50")

	ctx = Context{Pack: pack2026(), ContributionScheme: scheme("NO_DISCOUNT", "0.2175")}
	back, err := (PayrollCalculator{}).Preview(ctx, "NET", d("28075.50"))
	if err != nil {
		t.Fatalf("bare-context net preview failed: %v", err)
	}
	assertAmount(t, "gross", back.Gross, "33030.00")
}

func TestPreviewGrossMatchesMinimumWageGolden(t *testing.T) {
	ctx := baseContext(scheme("NO_DISCOUNT", "0.2175"))
	result, err := (PayrollCalculator{}).Preview(ctx, "GROSS", d("33030"))
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, "income tax", result.IncomeTax, "0.00")
	assertAmount(t, "stamp tax", result.StampTax, "0.00")
	assertAmount(t, "net", result.Net, "28075.50")
}

func TestPreviewNetRoundTripsToGross(t *testing.T) {
	ctx := baseContext(scheme("NO_DISCOUNT", "0.2175"))
	golden, err := (PayrollCalculator{}).Preview(ctx, "GROSS", d("100000"))
	if err != nil {
		t.Fatal(err)
	}
	back, err := (PayrollCalculator{}).Preview(ctx, "NET", golden.Net)
	if err != nil {
		t.Fatal(err)
	}
	if back.Gross.Sub(d("100000")).Cmp(d("0.02")) > 0 && d("100000").Sub(back.Gross).Cmp(d("0.02")) > 0 {
		t.Fatalf("gross round-trip=%s want ~100000", back.Gross.String())
	}
	if back.Net.Sub(golden.Net).Cmp(d("0.01")) > 0 && golden.Net.Sub(back.Net).Cmp(d("0.01")) > 0 {
		t.Fatalf("net round-trip=%s want %s", back.Net.String(), golden.Net.String())
	}
}

func TestPreviewNetAtMinimumWage(t *testing.T) {
	ctx := baseContext(scheme("NO_DISCOUNT", "0.2175"))
	result, err := (PayrollCalculator{}).Preview(ctx, "NET", d("28075.50"))
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, "gross", result.Gross, "33030.00")
}

func TestAboveMinimumWageOnlyPartiallyExempt(t *testing.T) {
	ctx := baseContext(scheme("NO_DISCOUNT", "0.2175"))
	ctx.MonthlyGross = d("66060") // exactly double the 2026 minimum wage
	ctx.MinimumWagePriorBase = zero()
	result, err := (PayrollCalculator{}).Calculate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.IncomeTax.Sign() <= 0 {
		t.Fatalf("expected income tax above the minimum-wage exemption, got %s", result.IncomeTax.String())
	}
	if result.StampTax.Sign() <= 0 {
		t.Fatalf("expected stamp tax above the minimum-wage exemption, got %s", result.StampTax.String())
	}
	if result.IncomeTaxExemption.Sign() <= 0 {
		t.Fatalf("expected a partial income-tax exemption, got %s", result.IncomeTaxExemption.String())
	}
}

func TestPartialMonthMinimumWageKeepsFullExemption(t *testing.T) {
	ctx := baseContext(scheme("NO_DISCOUNT", "0.2175"))
	ctx.FullMonth = false
	ctx.PaidDays = d("15")
	ctx.SGKDays = 15
	ctx.MonthlyGross = d("33030")
	result, err := (PayrollCalculator{}).Calculate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, "base wage", result.Gross, "16515.00")
	assertAmount(t, "income tax", result.IncomeTax, "0.00")
	assertAmount(t, "stamp tax", result.StampTax, "0.00")
}

func assertAmount(t *testing.T, name string, got money.Decimal, want string) {
	t.Helper()
	if got.String() != want {
		t.Fatalf("%s=%s want %s", name, got.String(), want)
	}
}
