// Package legislation defines immutable inputs consumed by the payroll engine.
package legislation

import (
	"time"

	"github.com/alpyxn/varyaone/internal/money"
)

type Pack struct {
	ID                  string
	Code                string
	Version             int
	EffectiveFrom       time.Time
	EffectiveTo         time.Time
	MinimumMonthlyGross money.Decimal
	SGKDailyFloor       money.Decimal
	SGKDailyCeiling     money.Decimal
	StampTaxRate        money.Decimal
	IncomeTaxBrackets   []IncomeTaxBracket
}

type IncomeTaxBracket struct {
	// UpperBound is a cumulative base. Nil means the final unbounded bracket.
	UpperBound *money.Decimal
	Rate       money.Decimal
}

type ContributionScheme struct {
	Code                     string
	EmployeeSGKRate          money.Decimal
	EmployeeUnemploymentRate money.Decimal
	EmployerSGKRate          money.Decimal
	EmployerUnemploymentRate money.Decimal
	// Sosyal Güvenlik Destek Primi oranları (4/a emekli çalışan).
	SGDPEmployeeRate money.Decimal
	SGDPEmployerRate money.Decimal
}

type Treatment string

const (
	FullyIncluded Treatment = "FULLY_INCLUDED"
	FullyExempt   Treatment = "FULLY_EXEMPT"
	DailyLimit    Treatment = "DAILY_LIMIT"
	MonthlyLimit  Treatment = "MONTHLY_LIMIT"
	PercentLimit  Treatment = "PERCENT_LIMIT"
)

type TreatmentRule struct {
	Treatment Treatment
	// Limit is required for limit treatments. PercentLimit is expressed as a
	// decimal ratio (for example 0.10), never as a binary float or whole percent.
	Limit money.Decimal
}
