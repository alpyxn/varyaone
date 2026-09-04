// Package calculation contains the pure, database-free Turkish payroll engine.
package calculation

import (
	"errors"
	"fmt"
	"time"

	"github.com/alpyxn/varyaone/internal/money"
	"github.com/alpyxn/varyaone/internal/payroll/legislation"
)

const EngineVersion = "tr-payroll-v1"

type ErrorCode string

const (
	ErrLegislationNotFound    ErrorCode = "PAYROLL_LEGISLATION_NOT_FOUND"
	ErrRunTypeNotSupported    ErrorCode = "PAYROLL_RUN_TYPE_NOT_SUPPORTED"
	ErrPopulationNotSupported ErrorCode = "PAYROLL_POPULATION_NOT_SUPPORTED"
	ErrComponentNotSupported  ErrorCode = "PAYROLL_COMPONENT_NOT_SUPPORTED"
	ErrSGKStatusNotSupported  ErrorCode = "PAYROLL_SGK_STATUS_NOT_SUPPORTED"
	ErrSickLeaveTreatment     ErrorCode = "SICK_LEAVE_TREATMENT_REQUIRED"
	ErrOpeningBalanceRequired ErrorCode = "PAYROLL_OPENING_BALANCE_REQUIRED"
	ErrNegativeNet            ErrorCode = "PAYROLL_NEGATIVE_NET"
	ErrReconciliation         ErrorCode = "PAYROLL_RECONCILIATION_FAILED"
)

type CalculationError struct {
	Code      ErrorCode
	Field     string
	Component string
	Message   string
}

func (e *CalculationError) Error() string { return string(e.Code) + ": " + e.Message }

type Context struct {
	PeriodDate time.Time
	RunType    string
	WageType   string
	WagePeriod string
	Currency   string
	WorkType   string
	SGKStatus  string

	MonthlyGross money.Decimal
	PaidDays     money.Decimal
	SGKDays      int
	FullMonth    bool

	PriorEmployerTaxPolicy string
	PriorCumulativeTaxBase money.Decimal
	HasCarryOpening        bool
	HasPriorCompanyMonths  bool
	HasCompanyOpening      bool
	HasUnresolvedSickLeave bool
	HasLegalDeduction      bool

	Pack                 *legislation.Pack
	ContributionScheme   *legislation.ContributionScheme
	MinimumWagePriorBase money.Decimal
	Components           []InputComponent
}

type InputComponent struct {
	Code       string
	Name       string
	Kind       string // EARNING or DEDUCTION
	Ownership  string // SYSTEM or MANUAL
	Amount     money.Decimal
	WorkedDays money.Decimal
	SGK        legislation.TreatmentRule
	IncomeTax  legislation.TreatmentRule
	StampTax   legislation.TreatmentRule
}

type Component struct {
	Code   string
	Name   string
	Kind   string
	Amount money.Decimal
}

type Result struct {
	EngineVersion          string
	LegislationPackID      string
	LegislationPackVersion int
	Gross                  money.Decimal
	SGKBase                money.Decimal
	EmployeeSGK            money.Decimal
	EmployeeUnemployment   money.Decimal
	IncomeTaxBase          money.Decimal
	IncomeTaxBeforeExempt  money.Decimal
	IncomeTaxExemption     money.Decimal
	IncomeTax              money.Decimal
	StampTaxBeforeExempt   money.Decimal
	StampTaxExemption      money.Decimal
	StampTax               money.Decimal
	ApprovedDeductions     money.Decimal
	TotalDeductions        money.Decimal
	Net                    money.Decimal
	EmployerSGK            money.Decimal
	EmployerUnemployment   money.Decimal
	EmployerContributions  money.Decimal
	EmployerCost           money.Decimal
	Components             []Component
}

type PayrollCalculator struct{}

func (PayrollCalculator) Calculate(ctx Context) (Result, error) {
	if err := validateContext(ctx); err != nil {
		return Result{}, err
	}
	baseWage := ctx.MonthlyGross
	if !ctx.FullMonth {
		baseWage = q(mul(div(ctx.MonthlyGross, integer(30), 12), ctx.PaidDays))
	} else {
		baseWage = q(baseWage)
	}

	result := Result{
		EngineVersion: EngineVersion, LegislationPackID: ctx.Pack.ID,
		LegislationPackVersion: ctx.Pack.Version,
		Components:             []Component{{Code: "BASE_WAGE", Name: "Asıl ücret", Kind: "EARNING", Amount: baseWage}},
	}
	gross, sgkIncluded, incomeTaxIncluded, stampIncluded := baseWage, baseWage, baseWage, baseWage
	approvedDeductions := zero()
	for _, input := range ctx.Components {
		amount := q(input.Amount)
		if input.Kind == "DEDUCTION" {
			approvedDeductions = approvedDeductions.Add(amount)
			result.Components = append(result.Components, Component{Code: input.Code, Name: input.Name, Kind: input.Kind, Amount: amount})
			continue
		}
		gross = gross.Add(amount)
		sgkIncluded = sgkIncluded.Add(applyTreatment(amount, input.WorkedDays, input.SGK))
		incomeTaxIncluded = incomeTaxIncluded.Add(applyTreatment(amount, input.WorkedDays, input.IncomeTax))
		stampIncluded = stampIncluded.Add(applyTreatment(amount, input.WorkedDays, input.StampTax))
		result.Components = append(result.Components, Component{Code: input.Code, Name: input.Name, Kind: input.Kind, Amount: amount})
	}
	result.Gross = q(gross)
	result.ApprovedDeductions = q(approvedDeductions)

	if ctx.SGKDays == 0 || sgkIncluded.Sign() == 0 {
		result.SGKBase = zero()
	} else {
		floor := mul(ctx.Pack.SGKDailyFloor, integer(ctx.SGKDays))
		ceiling := mul(ctx.Pack.SGKDailyCeiling, integer(ctx.SGKDays))
		result.SGKBase = q(clamp(sgkIncluded, floor, ceiling))
	}
	empSGKRate, empUnempRate, erSGKRate, erUnempRate := effectiveContributionRates(ctx)
	result.EmployeeSGK = q(mul(result.SGKBase, empSGKRate))
	result.EmployeeUnemployment = q(mul(result.SGKBase, empUnempRate))

	// The minimum-wage income-tax and stamp-tax exemptions track the employee's
	// paid days: a half-worked month earns half the monthly exemption, exactly
	// as it earns half the wage. dayRatio is 1 for a full month.
	dayRatio := dayRatioOf(ctx)
	minimumGross := q(mul(ctx.Pack.MinimumMonthlyGross, dayRatio))

	result.IncomeTaxBase = q(nonNegative(incomeTaxIncluded.Sub(result.EmployeeSGK).Sub(result.EmployeeUnemployment)))
	currentTax := progressiveDelta(ctx.PriorCumulativeTaxBase, result.IncomeTaxBase, ctx.Pack.IncomeTaxBrackets)
	result.IncomeTaxBeforeExempt = q(currentTax)
	minimumTaxable := q(nonNegative(minimumGross.Sub(
		q(mul(minimumGross, ctx.ContributionScheme.EmployeeSGKRate))).Sub(
		q(mul(minimumGross, ctx.ContributionScheme.EmployeeUnemploymentRate)))))
	minimumExemption := q(progressiveDelta(ctx.MinimumWagePriorBase, minimumTaxable, ctx.Pack.IncomeTaxBrackets))
	result.IncomeTaxExemption = min(result.IncomeTaxBeforeExempt, minimumExemption)
	result.IncomeTax = q(result.IncomeTaxBeforeExempt.Sub(result.IncomeTaxExemption))

	result.StampTaxBeforeExempt = q(mul(stampIncluded, ctx.Pack.StampTaxRate))
	minimumStampBase := min(baseWage, minimumGross)
	result.StampTaxExemption = min(result.StampTaxBeforeExempt, q(mul(minimumStampBase, ctx.Pack.StampTaxRate)))
	result.StampTax = q(result.StampTaxBeforeExempt.Sub(result.StampTaxExemption))

	result.TotalDeductions = q(result.EmployeeSGK.Add(result.EmployeeUnemployment).Add(result.IncomeTax).Add(result.StampTax).Add(result.ApprovedDeductions))
	result.Net = q(result.Gross.Sub(result.TotalDeductions))
	if result.Net.Sign() < 0 {
		return Result{}, calculationError(ErrNegativeNet, "components", "", "onaylı kesintiler net ücreti sıfırın altına düşürüyor")
	}
	result.EmployerSGK = q(mul(result.SGKBase, erSGKRate))
	result.EmployerUnemployment = q(mul(result.SGKBase, erUnempRate))
	result.EmployerContributions = q(result.EmployerSGK.Add(result.EmployerUnemployment))
	result.EmployerCost = q(result.Gross.Add(result.EmployerContributions))
	// Reconcile the itemised component rows against the headline totals. This is
	// the check that a rendered payslip depends on: every earning row must add up
	// to Gross, and every deduction row plus the statutory levies to
	// TotalDeductions.
	earningSum, deductionSum := zero(), zero()
	for _, component := range result.Components {
		switch component.Kind {
		case "EARNING":
			earningSum = earningSum.Add(component.Amount)
		case "DEDUCTION":
			deductionSum = deductionSum.Add(component.Amount)
		}
	}
	deductionSum = deductionSum.Add(result.EmployeeSGK).Add(result.EmployeeUnemployment).Add(result.IncomeTax).Add(result.StampTax)
	if difference(earningSum, result.Gross).Cmp(decimal("0.01")) > 0 ||
		difference(deductionSum, result.TotalDeductions).Cmp(decimal("0.01")) > 0 ||
		difference(result.Gross.Sub(result.TotalDeductions), result.Net).Cmp(decimal("0.01")) > 0 {
		return Result{}, calculationError(ErrReconciliation, "", "", "bordro bileşenleri uzlaştırılamadı")
	}
	return result, nil
}

// dayRatioOf is the fraction of a 30-day month the employee is paid for, capped
// at 1. A full month, or an unprorated month with no day information, is 1.
func dayRatioOf(ctx Context) money.Decimal {
	if ctx.FullMonth || ctx.PaidDays.Cmp(integer(30)) >= 0 {
		return one()
	}
	return div(ctx.PaidDays, integer(30), 12)
}

// Preview estimates a single, standalone month of payroll for either a target
// GROSS or a target NET monthly wage, treating the month as full (30 paid/SGK
// days, no manual components, no prior cumulative tax base) — i.e. "if this
// were the employee's only month with the company". It is used by the wage
// entry form and the minimum-wage panel to show gross/net side by side; it is
// an estimate, not a substitute for an actual payroll run (a real run's net
// can differ once cumulative tax base, partial months or manual components
// apply).
func (c PayrollCalculator) Preview(ctx Context, mode string, amount money.Decimal) (Result, error) {
	ctx.RunType = "REGULAR"
	ctx.WageType = "GROSS"
	ctx.WagePeriod = "MONTHLY"
	ctx.FullMonth = true
	ctx.PaidDays = integer(30)
	ctx.SGKDays = 30
	if ctx.PeriodDate.IsZero() && ctx.Pack != nil {
		ctx.PeriodDate = ctx.Pack.EffectiveFrom
	}
	if ctx.Currency == "" {
		ctx.Currency = "TRY"
	}
	if ctx.WorkType == "" {
		ctx.WorkType = "FULL_TIME"
	}
	if ctx.SGKStatus == "" {
		ctx.SGKStatus = "4A"
	}
	if ctx.PriorEmployerTaxPolicy == "" {
		ctx.PriorEmployerTaxPolicy = "SEPARATE"
	}
	ctx.PriorCumulativeTaxBase = zero()
	ctx.HasCarryOpening = false
	ctx.HasPriorCompanyMonths = false
	ctx.HasCompanyOpening = false
	ctx.HasUnresolvedSickLeave = false
	ctx.HasLegalDeduction = false
	ctx.Components = nil

	switch mode {
	case "GROSS":
		ctx.MonthlyGross = amount
		return c.Calculate(ctx)
	case "NET":
		return c.calculateForTargetNet(ctx, amount)
	default:
		return Result{}, calculationError(ErrPopulationNotSupported, "mode", "", "önizleme modu brüt ya da net olmalı")
	}
}

// calculateForTargetNet bisects on the gross wage until Calculate's net lands
// on targetNet: net is monotonically non-decreasing in gross, so bisection
// converges. It first doubles an upper bound until it produces a net at or
// above the target (net is always <= gross, so the bound cannot be read off
// the target directly).
func (c PayrollCalculator) calculateForTargetNet(ctx Context, targetNet money.Decimal) (Result, error) {
	if targetNet.Sign() < 0 {
		return Result{}, calculationError(ErrPopulationNotSupported, "amount", "", "net tutar negatif olamaz")
	}
	lo := zero()
	hi := targetNet.Add(decimal("1"))
	var probe Result
	for i := 0; i < 40; i++ {
		ctx.MonthlyGross = hi
		result, err := c.Calculate(ctx)
		if err != nil {
			return Result{}, err
		}
		probe = result
		if result.Net.Cmp(targetNet) >= 0 {
			break
		}
		hi = q(mul(hi, decimal("2")))
	}
	if probe.Net.Cmp(targetNet) < 0 {
		return Result{}, calculationError(ErrPopulationNotSupported, "amount", "", "hedef net için brüt bulunamadı")
	}
	for i := 0; i < 60; i++ {
		mid := q(div(lo.Add(hi), integer(2), 12))
		ctx.MonthlyGross = mid
		result, err := c.Calculate(ctx)
		if err != nil {
			return Result{}, err
		}
		cmp := result.Net.Cmp(targetNet)
		if cmp == 0 || hi.Sub(lo).Cmp(decimal("0.01")) <= 0 {
			return result, nil
		}
		if cmp < 0 {
			lo = mid
		} else {
			hi = mid
		}
	}
	ctx.MonthlyGross = hi
	return c.Calculate(ctx)
}

func validateContext(ctx Context) error {
	if ctx.Pack == nil || ctx.PeriodDate.Before(ctx.Pack.EffectiveFrom) || ctx.PeriodDate.After(ctx.Pack.EffectiveTo) {
		return calculationError(ErrLegislationNotFound, "period", "", "dönem için aktif mevzuat paketi yok")
	}
	if ctx.RunType != "REGULAR" {
		return calculationError(ErrRunTypeNotSupported, "run_type", "", "v1 yalnız REGULAR bordroyu destekler")
	}
	switch ctx.SGKStatus {
	case "4A", "4A_SGDP", "4A_NO_UNEMPLOYMENT":
		// desteklenen 4/a varyantları
	case "APPRENTICE", "4B", "4C":
		return calculationError(ErrSGKStatusNotSupported, "sgk_status", "",
			"çırak/stajyer, 4/b (Bağ-Kur) ve 4/c (Emekli Sandığı) için otomatik bordro hesaplanmaz; bu çalışan için manuel bordro gerekir")
	default:
		return calculationError(ErrSGKStatusNotSupported, "sgk_status", "", "tanımsız sigortalılık statüsü")
	}
	// Each of these used to collapse into one "çalışan v1 bordro kapsamı dışında",
	// which told the user nothing about which field to go and fix.
	if ctx.Currency != "TRY" {
		return calculationError(ErrPopulationNotSupported, "currency", "", "ücret TRY dışında bir para biriminde; bordro yalnız TRY hesaplar")
	}
	if ctx.WorkType != "FULL_TIME" {
		return calculationError(ErrPopulationNotSupported, "work_type", "", "çalışma türü tam zamanlı değil; bu çalışanın bordrosu otomatik hesaplanmaz")
	}
	if ctx.WageType != "GROSS" || ctx.WagePeriod != "MONTHLY" {
		return calculationError(ErrPopulationNotSupported, "wage_basis", "", "ücret aylık brüt olarak tanımlanmalı")
	}
	if ctx.ContributionScheme == nil || ctx.ContributionScheme.Code == "" {
		return calculationError(ErrPopulationNotSupported, "contribution_scheme", "", "SGK teşvik/indirim kodu tanımlı değil")
	}
	if ctx.MonthlyGross.Sign() <= 0 {
		return calculationError(ErrPopulationNotSupported, "gross_wage", "", "brüt ücret tanımlı değil")
	}
	if ctx.SGKDays < 0 || ctx.SGKDays > 30 || ctx.PaidDays.Sign() < 0 {
		return calculationError(ErrPopulationNotSupported, "attendance", "", "puantajdaki gün sayısı geçersiz")
	}
	if ctx.HasUnresolvedSickLeave {
		return calculationError(ErrSickLeaveTreatment, "leave", "", "rapor/hastalık uygulaması açıklığa kavuşturulmalı")
	}
	if ctx.HasLegalDeduction {
		return calculationError(ErrComponentNotSupported, "legal_deduction", "", "haciz ve nafaka limit motoru v1 kapsamında değil")
	}
	if ctx.PriorEmployerTaxPolicy == "CARRY" && !ctx.HasCarryOpening {
		return calculationError(ErrOpeningBalanceRequired, "prior_cumulative_tax_base", "", "önceki işveren carry-over açılışı zorunlu")
	}
	if ctx.HasPriorCompanyMonths && !ctx.HasCompanyOpening {
		return calculationError(ErrOpeningBalanceRequired, "company_cumulative_tax_base", "", "şirket içi kümülatif matrah açılışı zorunlu")
	}
	allowed := map[string]bool{"BONUS": true, "PREMIUM": true, "MEAL": true, "TRANSPORT": true, "ADVANCE": true, "OTHER_DEDUCTION": true, "OVERTIME": true, "WEEK_REST": true, "PUBLIC_HOLIDAY": true}
	for _, component := range ctx.Components {
		if !allowed[component.Code] || (component.Ownership == "SYSTEM" && component.Code != "OVERTIME" && component.Code != "WEEK_REST" && component.Code != "PUBLIC_HOLIDAY") {
			return calculationError(ErrComponentNotSupported, "components", component.Code, "bordro bileşeni v1 kapsamında değil")
		}
		if component.Amount.Sign() < 0 || (component.Kind != "EARNING" && component.Kind != "DEDUCTION") {
			return calculationError(ErrComponentNotSupported, "components", component.Code, "bileşen tutarı veya türü geçersiz")
		}
	}
	return nil
}

// effectiveContributionRates adjusts the scheme's SGK/unemployment rates for the
// employee's insurance status. 4/a normal uses the scheme as-is; the SGDP
// (working retiree) status swaps in the support-premium rate and drops
// unemployment; the "no unemployment" status only drops unemployment.
func effectiveContributionRates(ctx Context) (empSGK, empUnemp, erSGK, erUnemp money.Decimal) {
	s := ctx.ContributionScheme
	empSGK, empUnemp = s.EmployeeSGKRate, s.EmployeeUnemploymentRate
	erSGK, erUnemp = s.EmployerSGKRate, s.EmployerUnemploymentRate
	switch ctx.SGKStatus {
	case "4A_NO_UNEMPLOYMENT":
		empUnemp, erUnemp = zero(), zero()
	case "4A_SGDP":
		empSGK, erSGK = s.SGDPEmployeeRate, s.SGDPEmployerRate
		empUnemp, erUnemp = zero(), zero()
	}
	return empSGK, empUnemp, erSGK, erUnemp
}

func applyTreatment(amount, workedDays money.Decimal, rule legislation.TreatmentRule) money.Decimal {
	switch rule.Treatment {
	case legislation.FullyExempt:
		return zero()
	case legislation.DailyLimit:
		return q(nonNegative(amount.Sub(mul(rule.Limit, workedDays))))
	case legislation.MonthlyLimit:
		return q(nonNegative(amount.Sub(rule.Limit)))
	case legislation.PercentLimit:
		return q(mul(amount, one().Sub(rule.Limit)))
	default:
		return amount
	}
}

func progressiveDelta(prior, current money.Decimal, brackets []legislation.IncomeTaxBracket) money.Decimal {
	return progressiveTax(prior.Add(current), brackets).Sub(progressiveTax(prior, brackets))
}

func progressiveTax(base money.Decimal, brackets []legislation.IncomeTaxBracket) money.Decimal {
	base = nonNegative(base)
	tax, lower := zero(), zero()
	for _, bracket := range brackets {
		segment := base.Sub(lower)
		if segment.Sign() <= 0 {
			break
		}
		if bracket.UpperBound != nil {
			width := bracket.UpperBound.Sub(lower)
			segment = min(segment, width)
		}
		tax = tax.Add(mul(segment, bracket.Rate))
		if bracket.UpperBound == nil || base.Cmp(*bracket.UpperBound) <= 0 {
			break
		}
		lower = *bracket.UpperBound
	}
	return tax
}

func calculationError(code ErrorCode, field, component, message string) error {
	return &CalculationError{Code: code, Field: field, Component: component, Message: message}
}

func ErrorIsCode(err error, code ErrorCode) bool {
	var target *CalculationError
	return errors.As(err, &target) && target.Code == code
}

func q(value money.Decimal) money.Decimal {
	value, err := value.Quantize(2, money.HalfUp)
	if err != nil {
		panic(err)
	}
	return value
}
func div(left, right money.Decimal, scale int) money.Decimal {
	value, err := left.Div(right, scale)
	if err != nil {
		panic(err)
	}
	return value
}
func mul(left, right money.Decimal) money.Decimal { return left.Mul(right) }
func clamp(value, floor, ceiling money.Decimal) money.Decimal {
	return min(maximum(value, floor), ceiling)
}
func min(left, right money.Decimal) money.Decimal {
	if left.Cmp(right) <= 0 {
		return left
	}
	return right
}
func maximum(left, right money.Decimal) money.Decimal {
	if left.Cmp(right) >= 0 {
		return left
	}
	return right
}
func nonNegative(value money.Decimal) money.Decimal { return maximum(value, zero()) }
func difference(left, right money.Decimal) money.Decimal {
	value := left.Sub(right)
	if value.Sign() < 0 {
		return zero().Sub(value)
	}
	return value
}
func decimal(value string) money.Decimal {
	parsed, err := money.ParseDecimal(value, 12)
	if err != nil {
		panic(fmt.Sprintf("invalid calculator decimal %q", value))
	}
	return parsed
}
func zero() money.Decimal             { return decimal("0") }
func one() money.Decimal              { return decimal("1") }
func integer(value int) money.Decimal { return decimal(fmt.Sprintf("%d", value)) }
