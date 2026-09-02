package legislation

import (
	"context"
	"errors"
	"time"

	"github.com/alpyxn/varyaone/internal/money"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/jackc/pgx/v5"
)

// ErrPackNotFound is returned when no ACTIVE legislation pack covers a date.
var ErrPackNotFound = errors.New("PAYROLL_LEGISLATION_NOT_FOUND")

// ComponentDefinition describes a payroll component and its levy treatments as
// resolved from an active legislation pack.
type ComponentDefinition struct {
	Code       string
	Name       string
	Kind       string                   // EARNING, DEDUCTION, EMPLOYER_COST
	Ownership  string                   // SYSTEM, MANUAL
	Treatments map[string]TreatmentRule // keyed by levy: SGK, INCOME_TAX, STAMP_TAX
}

type Repository struct{ pool database.Querier }

func NewRepository(pool database.Querier) *Repository { return &Repository{pool: pool} }

func dec(value string) (money.Decimal, error) { return money.ParseDecimal(value, 12) }

// ActivePack returns the ACTIVE Turkish legislation pack covering the given
// date, with income-tax brackets loaded in sequence order.
func (r *Repository) ActivePack(ctx context.Context, companyID string, date time.Time) (*Pack, error) {
	var (
		packID, code                              string
		version                                   int
		effFrom, effTo                            time.Time
		minGross, sgkFloor, sgkCeiling, stampRate string
	)
	err := r.pool.QueryRow(ctx, `SELECT id::text,code,version,effective_from,effective_to,
 minimum_monthly_gross::text,sgk_daily_floor::text,sgk_daily_ceiling::text,stamp_tax_rate::text
 FROM payroll_legislation_packs
 WHERE company_id=$1 AND country_code='TR' AND status='ACTIVE'
   AND daterange(effective_from,effective_to,'[]') @> $2::date`,
		companyID, date.Format("2006-01-02")).
		Scan(&packID, &code, &version, &effFrom, &effTo, &minGross, &sgkFloor, &sgkCeiling, &stampRate)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPackNotFound
	}
	if err != nil {
		return nil, err
	}
	pack := &Pack{ID: packID, Code: code, Version: version, EffectiveFrom: effFrom, EffectiveTo: effTo}
	if pack.MinimumMonthlyGross, err = dec(minGross); err != nil {
		return nil, err
	}
	if pack.SGKDailyFloor, err = dec(sgkFloor); err != nil {
		return nil, err
	}
	if pack.SGKDailyCeiling, err = dec(sgkCeiling); err != nil {
		return nil, err
	}
	if pack.StampTaxRate, err = dec(stampRate); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT upper_bound::text,rate::text FROM payroll_income_tax_brackets
 WHERE company_id=$1 AND pack_id=$2 ORDER BY sequence`, companyID, packID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var upper *string
		var rate string
		if err := rows.Scan(&upper, &rate); err != nil {
			return nil, err
		}
		bracket := IncomeTaxBracket{}
		if bracket.Rate, err = dec(rate); err != nil {
			return nil, err
		}
		if upper != nil {
			bound, err := dec(*upper)
			if err != nil {
				return nil, err
			}
			bracket.UpperBound = &bound
		}
		pack.IncomeTaxBrackets = append(pack.IncomeTaxBrackets, bracket)
	}
	return pack, rows.Err()
}

// ContributionScheme resolves a scheme by code within a pack.
func (r *Repository) ContributionScheme(ctx context.Context, companyID, packID, code string) (*ContributionScheme, error) {
	var empSGK, empUnemp, erSGK, erUnemp, sgdpEmp, sgdpEr string
	err := r.pool.QueryRow(ctx, `SELECT employee_sgk_rate::text,employee_unemployment_rate::text,employer_sgk_rate::text,employer_unemployment_rate::text,
 sgdp_employee_rate::text,sgdp_employer_rate::text
 FROM payroll_contribution_schemes WHERE company_id=$1 AND pack_id=$2 AND code=$3`,
		companyID, packID, code).Scan(&empSGK, &empUnemp, &erSGK, &erUnemp, &sgdpEmp, &sgdpEr)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPackNotFound
	}
	if err != nil {
		return nil, err
	}
	scheme := &ContributionScheme{Code: code}
	if scheme.EmployeeSGKRate, err = dec(empSGK); err != nil {
		return nil, err
	}
	if scheme.EmployeeUnemploymentRate, err = dec(empUnemp); err != nil {
		return nil, err
	}
	if scheme.EmployerSGKRate, err = dec(erSGK); err != nil {
		return nil, err
	}
	if scheme.EmployerUnemploymentRate, err = dec(erUnemp); err != nil {
		return nil, err
	}
	if scheme.SGDPEmployeeRate, err = dec(sgdpEmp); err != nil {
		return nil, err
	}
	if scheme.SGDPEmployerRate, err = dec(sgdpEr); err != nil {
		return nil, err
	}
	return scheme, nil
}

// ComponentDefinitions returns every component definition in a pack keyed by code.
func (r *Repository) ComponentDefinitions(ctx context.Context, companyID, packID string) (map[string]ComponentDefinition, error) {
	rows, err := r.pool.Query(ctx, `SELECT id::text,code,name,component_kind,ownership FROM payroll_component_definitions
 WHERE company_id=$1 AND pack_id=$2`, companyID, packID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	defs := map[string]ComponentDefinition{}
	ids := map[string]string{}
	for rows.Next() {
		var id, code, name, kind, ownership string
		if err := rows.Scan(&id, &code, &name, &kind, &ownership); err != nil {
			return nil, err
		}
		defs[code] = ComponentDefinition{Code: code, Name: name, Kind: kind, Ownership: ownership, Treatments: map[string]TreatmentRule{}}
		ids[id] = code
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	trows, err := r.pool.Query(ctx, `SELECT component_definition_id::text,levy,treatment,COALESCE(limit_value,0)::text
 FROM payroll_component_treatments WHERE company_id=$1 AND component_definition_id = ANY(
   SELECT id FROM payroll_component_definitions WHERE company_id=$1 AND pack_id=$2)`, companyID, packID)
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	for trows.Next() {
		var defID, levy, treatment, limit string
		if err := trows.Scan(&defID, &levy, &treatment, &limit); err != nil {
			return nil, err
		}
		code, ok := ids[defID]
		if !ok {
			continue
		}
		limitDec, err := dec(limit)
		if err != nil {
			return nil, err
		}
		defs[code].Treatments[levy] = TreatmentRule{Treatment: Treatment(treatment), Limit: limitDec}
	}
	return defs, trows.Err()
}

// TreatmentFor returns the levy treatment for a component, defaulting to fully
// included when the pack defines no explicit rule.
func TreatmentFor(def ComponentDefinition, levy string) TreatmentRule {
	if rule, ok := def.Treatments[levy]; ok {
		return rule
	}
	zero, _ := money.ParseDecimal("0", 12)
	return TreatmentRule{Treatment: FullyIncluded, Limit: zero}
}
