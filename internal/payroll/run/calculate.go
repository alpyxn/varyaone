package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/money"
	"github.com/alpyxn/varyaone/internal/payroll/calculation"
	"github.com/alpyxn/varyaone/internal/payroll/legislation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type populationRow struct {
	EmployeeID             string
	Code                   string
	Name                   string
	PositionTitle          string
	GrossWage              string
	Currency               string
	WorkType               string
	ContributionSchemeCode string
	PriorEmployerTaxPolicy string
	SgkStatus              string
	EarliestStart          time.Time
}

// Calculate opens a new immutable generation and runs the pure payroll engine
// for every in-scope employee. All work is synchronous.
func (s *Service) Calculate(ctx context.Context, session identity.Session, runID string, meta identity.RequestMeta) (Run, error) {
	if !session.HasPermission("hr.payroll.calculate") {
		return Run{}, identity.ErrForbidden
	}
	current, err := s.Get(ctx, session, runID)
	if err != nil {
		return Run{}, err
	}
	if current.Status == "FINALIZED" {
		return Run{}, ErrRunNotDraft
	}
	year, month := current.PeriodYear, current.PeriodMonth
	periodDate := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := periodDate.AddDate(0, 1, -1)
	daysInMonth := monthEnd.Day()

	var tsChecksum string
	if err = s.pool.QueryRow(ctx, `SELECT COALESCE(checksum,'') FROM timesheet_periods WHERE company_id=$1 AND id=$2 AND status='FINALIZED'`,
		session.CurrentCompanyID, current.TimesheetPeriodID).Scan(&tsChecksum); errors.Is(err, pgx.ErrNoRows) || tsChecksum == "" {
		return Run{}, ErrTimesheetNotFinal
	} else if err != nil {
		return Run{}, err
	}

	pack, err := s.repo.ActivePack(ctx, session.CurrentCompanyID, periodDate)
	if err != nil {
		if errors.Is(err, legislation.ErrPackNotFound) {
			return Run{}, ErrLegislationMissing
		}
		return Run{}, err
	}
	componentDefs, err := s.repo.ComponentDefinitions(ctx, session.CurrentCompanyID, pack.ID)
	if err != nil {
		return Run{}, err
	}

	population, err := s.loadPopulation(ctx, session.CurrentCompanyID, periodDate, monthEnd)
	if err != nil {
		return Run{}, err
	}

	// Fingerprint. It is stable for the duration of this synchronous run.
	popParts := make([]string, 0, len(population))
	for _, p := range population {
		popParts = append(popParts, p.EmployeeID+"|"+p.GrossWage+"|"+p.ContributionSchemeCode+"|"+p.SgkStatus)
	}
	populationHash := hashStrings(popParts...)
	inputChecksum := s.manualInputChecksum(ctx, session.CurrentCompanyID, runID)
	var manualCount int64
	_ = s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM payroll_manual_components WHERE company_id=$1 AND payroll_run_id=$2 AND archived_at IS NULL`,
		session.CurrentCompanyID, runID).Scan(&manualCount)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var nextGenNo int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(generation_no),0)+1 FROM payroll_calculation_generations WHERE company_id=$1 AND payroll_run_id=$2`,
		session.CurrentCompanyID, runID).Scan(&nextGenNo); err != nil {
		return Run{}, err
	}
	generationID := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO payroll_calculation_generations(id,company_id,payroll_run_id,generation_no,status,engine_version,
 legislation_pack_version,population_hash,timesheet_checksum,manual_input_version,input_checksum)
 VALUES($1,$2,$3,$4,'RUNNING',$5,$6,$7,$8,$9,$10)`,
		generationID, session.CurrentCompanyID, runID, nextGenNo, calculation.EngineVersion, pack.Version,
		populationHash, tsChecksum, manualCount, inputChecksum); err != nil {
		return Run{}, mapConstraint(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE payroll_runs SET status='CALCULATING',updated_at=now(),version=version+1 WHERE company_id=$1 AND id=$2`,
		session.CurrentCompanyID, runID); err != nil {
		return Run{}, err
	}

	failures := []map[string]any{}
	if len(population) == 0 {
		failures = append(failures, map[string]any{
			"code":    "PAYROLL_POPULATION_EMPTY",
			"message": "bordro döneminde hesaplanabilecek çalışan bulunamadı",
		})
	}
	var totalGross, totalDeductions, totalNet, totalEmployerCost money.Decimal
	totalGross, _ = money.ParseDecimal("0", 2)
	totalDeductions, _ = money.ParseDecimal("0", 2)
	totalNet, _ = money.ParseDecimal("0", 2)
	totalEmployerCost, _ = money.ParseDecimal("0", 2)

	for _, p := range population {
		result, calcCtx, calcErr := s.calculateEmployee(ctx, tx, session.CurrentCompanyID, runID, p, pack, componentDefs, year, month, daysInMonth, periodDate, monthEnd)
		payrollID := uuid.NewString()
		empSnapshot, _ := json.Marshal(map[string]any{"code": p.Code, "name": p.Name, "position_title": p.PositionTitle,
			"sgk_status": sgkStatusOrDefault(p.SgkStatus), "contribution_scheme_code": p.ContributionSchemeCode})
		companySnapshot, _ := json.Marshal(map[string]any{"company_id": session.CurrentCompanyID})
		legislationSnapshot, _ := json.Marshal(map[string]any{"pack_id": pack.ID, "pack_version": pack.Version, "code": pack.Code})
		attendanceSnapshot, _ := json.Marshal(map[string]any{
			"paid_days": calcCtx.PaidDays.String(), "sgk_days": calcCtx.SGKDays, "full_month": calcCtx.FullMonth,
			"timesheet_period_id": current.TimesheetPeriodID, "checksum": tsChecksum,
		})
		empChecksum := sha256Hex(p.EmployeeID + "|" + p.GrossWage + "|" + p.ContributionSchemeCode + "|" + p.SgkStatus + "|" + inputChecksum)

		if calcErr != nil {
			detail := errorDetail(calcErr)
			failures = append(failures, map[string]any{"employee_id": p.EmployeeID, "error": detail})
			errBytes, _ := json.Marshal([]map[string]any{detail})
			if _, err = tx.Exec(ctx, `INSERT INTO employee_payrolls(id,company_id,generation_id,payroll_run_id,employee_id,status,
 employee_input_checksum,employee_snapshot,company_snapshot,legislation_snapshot,attendance_snapshot,error_details)
 VALUES($1,$2,$3,$4,$5,'FAILED',$6,$7,$8,$9,$10,$11)`,
				payrollID, session.CurrentCompanyID, generationID, runID, p.EmployeeID, empChecksum,
				empSnapshot, companySnapshot, legislationSnapshot, attendanceSnapshot, errBytes); err != nil {
				return Run{}, mapConstraint(err)
			}
			continue
		}

		if _, err = tx.Exec(ctx, `INSERT INTO employee_payrolls(id,company_id,generation_id,payroll_run_id,employee_id,status,
 gross,sgk_base,employee_sgk,employee_unemployment,income_tax_base,income_tax,stamp_tax,total_deductions,net,employer_contributions,employer_cost,
 paid_days,sgk_days,employee_input_checksum,employee_snapshot,company_snapshot,legislation_snapshot,attendance_snapshot)
 VALUES($1,$2,$3,$4,$5,'CALCULATED',$6::numeric,$7::numeric,$8::numeric,$9::numeric,$10::numeric,$11::numeric,$12::numeric,$13::numeric,$14::numeric,$15::numeric,$16::numeric,
 $17::numeric,$18,$19,$20,$21,$22,$23)`,
			payrollID, session.CurrentCompanyID, generationID, runID, p.EmployeeID,
			result.Gross.String(), result.SGKBase.String(), result.EmployeeSGK.String(), result.EmployeeUnemployment.String(),
			result.IncomeTaxBase.String(), result.IncomeTax.String(), result.StampTax.String(), result.TotalDeductions.String(),
			result.Net.String(), result.EmployerContributions.String(), result.EmployerCost.String(),
			calcCtx.PaidDays.String(), calcCtx.SGKDays, empChecksum, empSnapshot, companySnapshot, legislationSnapshot, attendanceSnapshot); err != nil {
			return Run{}, mapConstraint(err)
		}
		if err = s.insertComponents(ctx, tx, session.CurrentCompanyID, payrollID, result); err != nil {
			return Run{}, mapConstraint(err)
		}
		totalGross = totalGross.Add(result.Gross)
		totalDeductions = totalDeductions.Add(result.TotalDeductions)
		totalNet = totalNet.Add(result.Net)
		totalEmployerCost = totalEmployerCost.Add(result.EmployerCost)
	}

	allOK := len(failures) == 0 && len(population) > 0
	if allOK {
		if _, err = tx.Exec(ctx, `UPDATE payroll_calculation_generations SET status='ACTIVE',completed_at=now() WHERE company_id=$1 AND id=$2`,
			session.CurrentCompanyID, generationID); err != nil {
			return Run{}, mapConstraint(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE payroll_runs SET status='CALCULATED',active_generation_id=$3,
 total_gross=$4::numeric,total_employee_deductions=$5::numeric,total_net=$6::numeric,total_employer_cost=$7::numeric,updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=$2`,
			session.CurrentCompanyID, runID, generationID,
			totalGross.String(), totalDeductions.String(), totalNet.String(), totalEmployerCost.String()); err != nil {
			return Run{}, mapConstraint(err)
		}
	} else {
		summary, _ := json.Marshal(failures)
		if _, err = tx.Exec(ctx, `UPDATE payroll_calculation_generations SET status='FAILED',completed_at=now(),error_summary=$3 WHERE company_id=$1 AND id=$2`,
			session.CurrentCompanyID, generationID, summary); err != nil {
			return Run{}, mapConstraint(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE payroll_runs SET status='CALCULATION_FAILED',updated_at=now(),version=version+1 WHERE company_id=$1 AND id=$2`,
			session.CurrentCompanyID, runID); err != nil {
			return Run{}, err
		}
	}
	if err = writeEvent(ctx, tx, session, meta, "PAYROLL_CALCULATED", "hr.payroll.calculated", runID); err != nil {
		return Run{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Run{}, err
	}
	return s.Get(ctx, session, runID)
}

func (s *Service) calculateEmployee(ctx context.Context, tx pgx.Tx, companyID, runID string, p populationRow, pack *legislation.Pack,
	defs map[string]legislation.ComponentDefinition, year, month, daysInMonth int, periodDate, monthEnd time.Time) (calculation.Result, calculation.Context, error) {

	scheme, err := s.repo.ContributionScheme(ctx, companyID, pack.ID, p.ContributionSchemeCode)
	if err != nil {
		return calculation.Result{}, calculation.Context{}, err
	}

	// Attendance. paidDays is the number of days that actually accrue salary
	// and an SGK day: worked, paid leave, public holiday, or paid weekly rest.
	// unpaidDays are unpaid leave / unexcused absence. Empty rows and days the
	// user removed count as nothing. recordedDays is every row for the period.
	var paidDaysCount, unpaidDays, recordedDays int
	if err := tx.QueryRow(ctx, `SELECT
 COUNT(*) FILTER (WHERE worked_minutes>0 OR paid_leave_minutes>0 OR public_holiday_minutes>0 OR week_rest_minutes>0),
 COUNT(*) FILTER (WHERE unpaid_leave_minutes>0 OR absence_minutes>0),
 COUNT(*)
 FROM timesheet_days d JOIN timesheet_periods tp ON tp.company_id=d.company_id AND tp.id=d.period_id
 JOIN payroll_runs r ON r.company_id=tp.company_id AND r.timesheet_period_id=tp.id
 WHERE d.company_id=$1 AND r.id=$2 AND d.employee_id=$3`, companyID, runID, p.EmployeeID).Scan(&paidDaysCount, &unpaidDays, &recordedDays); err != nil {
		return calculation.Result{}, calculation.Context{}, fmt.Errorf("puantaj devam bilgisi okunamadı: %w", err)
	}
	workDays, fullMonth := attendanceDays(paidDaysCount, unpaidDays, recordedDays, daysInMonth)
	paidDays, _ := money.ParseDecimal(fmt.Sprintf("%d", pickDays(fullMonth, workDays)), 2)
	sgkDays := pickDays(fullMonth, workDays)
	if sgkDays > 30 {
		sgkDays = 30
	}

	grossWage, err := money.ParseDecimal(strings.TrimSpace(p.GrossWage), 4)
	if err != nil {
		return calculation.Result{}, calculation.Context{}, fmt.Errorf("gross wage parse: %w", err)
	}

	// Prior cumulative tax base
	var carryBase, companyBase, priorFinalized string
	carryExists := s.scalarString(ctx, `SELECT cumulative_income_tax_base::text FROM employee_payroll_year_openings
 WHERE company_id=$1 AND employee_id=$2 AND tax_year=$3 AND source='PRIOR_EMPLOYER_CARRY'`, companyID, p.EmployeeID, year)
	carryBase = carryExists
	companyBase = s.scalarString(ctx, `SELECT cumulative_income_tax_base::text FROM employee_payroll_year_openings
 WHERE company_id=$1 AND employee_id=$2 AND tax_year=$3 AND source='COMPANY_MIGRATION'`, companyID, p.EmployeeID, year)
	priorFinalized = s.scalarString(ctx, `SELECT COALESCE(SUM(ep.income_tax_base),0)::text FROM employee_payrolls ep
 JOIN payroll_runs r2 ON r2.company_id=ep.company_id AND r2.id=ep.payroll_run_id
 WHERE ep.company_id=$1 AND ep.employee_id=$2 AND ep.status='FINALIZED' AND r2.status='FINALIZED'
   AND r2.period_year=$3 AND r2.period_month<$4`, companyID, p.EmployeeID, year, month)

	priorBase := sumDecimals(carryBase, companyBase, priorFinalized)
	hasPriorFinalized := !isZeroString(priorFinalized)

	// priorMonths = taxed months of this year that come before the period. It
	// defaults to (month-1) but, when a hire date shows the employee started
	// later this year, only counts from the hire month so the cumulative
	// income-tax and minimum-wage-exemption bases line up with reality.
	priorMonths := month - 1
	if !p.EarliestStart.IsZero() && p.EarliestStart.Year() == year {
		if hireMonth := int(p.EarliestStart.Month()); hireMonth > 1 {
			priorMonths = month - hireMonth
		}
	}
	if priorMonths < 0 {
		priorMonths = 0
	}

	// Minimum-wage prior cumulative taxable base = priorMonths * monthly minimum taxable
	minMonthly := minimumMonthlyTaxable(pack.MinimumMonthlyGross, scheme)
	minPrior := minMonthly.Mul(mustDec(fmt.Sprintf("%d", priorMonths)))

	// The employee has in-company months this year that were never run through
	// payroll and there is no explicit migration opening. Rather than fail the
	// whole run over a missing "işe giriş"/kümülatif matrah figure, estimate
	// those months at the current wage so the year-to-date tax stays close to
	// correct. (PRIOR_EMPLOYER_CARRY still needs an explicit opening — a former
	// employer's base cannot be estimated.)
	if priorMonths > 0 && !hasPriorFinalized && isZeroString(companyBase) && p.PriorEmployerTaxPolicy != "CARRY" {
		est := estimateMonthlyIncomeTaxBase(pack, scheme, grossWage, sgkStatusOrDefault(p.SgkStatus))
		priorBase = sumDecimals(carryBase, est.Mul(mustDec(fmt.Sprintf("%d", priorMonths))).String())
	}

	// "Çözülmemiş rapor": puantajda o dönemde SICK_REQUIRES_REVIEW türüyle
	// işaretli bir gün varsa bordro hesaplanmaz — çözüm puantajdan günü
	// düzenlemek/türünü değiştirmektir (ayrı bir onay akışı yok).
	hasSick := s.exists(ctx, `SELECT 1 FROM timesheet_days d
 JOIN leave_types t ON t.company_id=d.company_id AND t.id=d.leave_type_id
 JOIN timesheet_periods tp ON tp.company_id=d.company_id AND tp.id=d.period_id
 WHERE d.company_id=$1 AND d.employee_id=$2 AND t.payroll_treatment='SICK_REQUIRES_REVIEW'
   AND tp.period_year=$3 AND tp.period_month=$4`,
		companyID, p.EmployeeID, year, month)
	hasLegalDeduction := s.exists(ctx, `SELECT 1 FROM employee_legal_deductions WHERE company_id=$1 AND employee_id=$2 AND status='ACTIVE'`,
		companyID, p.EmployeeID)

	components, err := s.manualComponentsFor(ctx, companyID, runID, p.EmployeeID, defs, paidDays)
	if err != nil {
		return calculation.Result{}, calculation.Context{}, err
	}

	calcCtx := calculation.Context{
		PeriodDate: periodDate, RunType: "REGULAR", WageType: "GROSS", WagePeriod: "MONTHLY",
		Currency: p.Currency, WorkType: p.WorkType, SGKStatus: sgkStatusOrDefault(p.SgkStatus),
		MonthlyGross: grossWage, PaidDays: paidDays, SGKDays: sgkDays, FullMonth: fullMonth,
		PriorEmployerTaxPolicy: p.PriorEmployerTaxPolicy,
		PriorCumulativeTaxBase: priorBase,
		HasCarryOpening:        !isZeroString(carryBase),
		// Prior in-company months are now always covered — by finalized runs, a
		// migration opening, or the estimate above — so this never blocks.
		HasPriorCompanyMonths:  false,
		HasCompanyOpening:      !isZeroString(companyBase) || hasPriorFinalized,
		HasUnresolvedSickLeave: hasSick,
		HasLegalDeduction:      hasLegalDeduction,
		Pack:                   pack,
		ContributionScheme:     scheme,
		MinimumWagePriorBase:   minPrior,
		Components:             components,
	}
	result, err := calculation.PayrollCalculator{}.Calculate(calcCtx)
	return result, calcCtx, err
}

func (s *Service) manualComponentsFor(ctx context.Context, companyID, runID, employeeID string,
	defs map[string]legislation.ComponentDefinition, workedDays money.Decimal) ([]calculation.InputComponent, error) {

	rows, err := s.pool.Query(ctx, `SELECT d.code,d.name,d.component_kind,d.ownership,m.amount::text
 FROM payroll_manual_components m JOIN payroll_component_definitions d ON d.company_id=m.company_id AND d.id=m.component_definition_id
 WHERE m.company_id=$1 AND m.payroll_run_id=$2 AND m.employee_id=$3 AND m.archived_at IS NULL`,
		companyID, runID, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []calculation.InputComponent{}
	for rows.Next() {
		var code, name, kind, ownership, amount string
		if err := rows.Scan(&code, &name, &kind, &ownership, &amount); err != nil {
			return nil, err
		}
		amountDec, err := money.ParseDecimal(strings.TrimSpace(amount), 2)
		if err != nil {
			return nil, err
		}
		def := defs[code]
		items = append(items, calculation.InputComponent{
			Code: code, Name: name, Kind: kind, Ownership: ownership, Amount: amountDec, WorkedDays: workedDays,
			SGK:       legislation.TreatmentFor(def, "SGK"),
			IncomeTax: legislation.TreatmentFor(def, "INCOME_TAX"),
			StampTax:  legislation.TreatmentFor(def, "STAMP_TAX"),
		})
	}
	return items, rows.Err()
}

func (s *Service) insertComponents(ctx context.Context, tx pgx.Tx, companyID, payrollID string, result calculation.Result) error {
	order := 0
	add := func(code, name, kind, amount string) error {
		order++
		_, err := tx.Exec(ctx, `INSERT INTO employee_payroll_components(id,company_id,employee_payroll_id,component_code,component_name,component_kind,amount,calculation_order)
 VALUES($1,$2,$3,$4,$5,$6,$7::numeric,$8)`,
			uuid.NewString(), companyID, payrollID, code, name, kind, amount, order)
		return err
	}
	for _, c := range result.Components {
		if err := add(c.Code, c.Name, c.Kind, c.Amount.String()); err != nil {
			return err
		}
	}
	if err := add("SGK_EMPLOYEE", "SGK işçi payı", "DEDUCTION", result.EmployeeSGK.String()); err != nil {
		return err
	}
	if err := add("UNEMPLOYMENT_EMPLOYEE", "İşsizlik işçi payı", "DEDUCTION", result.EmployeeUnemployment.String()); err != nil {
		return err
	}
	if err := add("INCOME_TAX", "Gelir vergisi", "DEDUCTION", result.IncomeTax.String()); err != nil {
		return err
	}
	if err := add("STAMP_TAX", "Damga vergisi", "DEDUCTION", result.StampTax.String()); err != nil {
		return err
	}
	if err := add("EMPLOYER_SGK", "SGK işveren payı", "EMPLOYER_COST", result.EmployerSGK.String()); err != nil {
		return err
	}
	if err := add("EMPLOYER_UNEMPLOYMENT", "İşsizlik işveren payı", "EMPLOYER_COST", result.EmployerUnemployment.String()); err != nil {
		return err
	}
	return add("NET", "Net ücret", "INFORMATION", result.Net.String())
}

func (s *Service) loadPopulation(ctx context.Context, companyID string, periodDate, monthEnd time.Time) ([]populationRow, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.id::text,e.employee_code,e.first_name||' '||e.last_name,e.position_title,
 t.gross_wage::text,t.currency,t.work_type,t.contribution_scheme_code,t.prior_employer_tax_policy,t.sgk_status,
 (SELECT min(start_date) FROM employments x WHERE x.company_id=$1 AND x.employee_id=e.id
   AND (x.end_date IS NULL OR x.end_date > x.start_date))
 FROM employees e
 JOIN employments emp ON emp.company_id=e.company_id AND emp.employee_id=e.id
   AND daterange(emp.start_date,COALESCE(emp.end_date,'infinity'::date),'[]') && daterange($2::date,$3::date,'[]')
 JOIN LATERAL (
   SELECT term.gross_wage,term.currency,term.work_type,term.contribution_scheme_code,term.prior_employer_tax_policy,term.sgk_status
   FROM employment_terms term
   WHERE term.company_id=e.company_id AND term.employment_id=emp.id
     AND daterange(term.effective_from,COALESCE(term.effective_to,'infinity'::date),'[]')
       @> GREATEST($2::date,emp.start_date)
   ORDER BY term.effective_from DESC
   LIMIT 1
 ) t ON true
 WHERE e.company_id=$1 AND e.status='ACTIVE'
 GROUP BY e.id,e.employee_code,e.first_name,e.last_name,e.position_title,t.gross_wage,t.currency,t.work_type,t.contribution_scheme_code,t.prior_employer_tax_policy,t.sgk_status
 ORDER BY e.id`, companyID, periodDate.Format("2006-01-02"), monthEnd.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []populationRow{}
	for rows.Next() {
		var p populationRow
		var earliest *time.Time
		if err := rows.Scan(&p.EmployeeID, &p.Code, &p.Name, &p.PositionTitle, &p.GrossWage, &p.Currency, &p.WorkType,
			&p.ContributionSchemeCode, &p.PriorEmployerTaxPolicy, &p.SgkStatus, &earliest); err != nil {
			return nil, err
		}
		if earliest != nil {
			p.EarliestStart = *earliest
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

// ---- Finalize ----

func (s *Service) Finalize(ctx context.Context, session identity.Session, runID string, version int64, meta identity.RequestMeta) (Run, error) {
	if !session.HasPermission("hr.payroll.finalize") {
		return Run{}, identity.ErrForbidden
	}
	current, err := s.Get(ctx, session, runID)
	if err != nil {
		return Run{}, err
	}
	if current.Status == "FINALIZED" {
		return current, nil
	}
	if current.Status != "CALCULATED" || current.ActiveGenerationID == nil || *current.ActiveGenerationID == "" {
		return Run{}, ErrNoActiveGeneration
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err = tx.Exec(ctx, `UPDATE employee_payrolls SET status='FINALIZED' WHERE company_id=$1 AND generation_id=$2 AND status='CALCULATED'`,
		session.CurrentCompanyID, *current.ActiveGenerationID); err != nil {
		return Run{}, mapConstraint(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE payroll_calculation_generations SET status='FINALIZED' WHERE company_id=$1 AND id=$2`,
		session.CurrentCompanyID, *current.ActiveGenerationID); err != nil {
		return Run{}, mapConstraint(err)
	}
	var g, d, n, ec string
	_ = tx.QueryRow(ctx, `SELECT COALESCE(SUM(gross),0)::text,COALESCE(SUM(total_deductions),0)::text,COALESCE(SUM(net),0)::text,COALESCE(SUM(employer_cost),0)::text
 FROM employee_payrolls WHERE company_id=$1 AND generation_id=$2 AND status='FINALIZED'`,
		session.CurrentCompanyID, *current.ActiveGenerationID).Scan(&g, &d, &n, &ec)
	tag, err := tx.Exec(ctx, `UPDATE payroll_runs SET status='FINALIZED',finalized_at=now(),finalized_by=$3,
 total_gross=$4::numeric,total_employee_deductions=$5::numeric,total_net=$6::numeric,total_employer_cost=$7::numeric,updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=$2 AND status='CALCULATED' AND version=$8`,
		session.CurrentCompanyID, runID, session.User.ID, g, d, n, ec, version)
	if err != nil {
		return Run{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return Run{}, identity.ErrConflict
	}
	if err = writeEvent(ctx, tx, session, meta, "PAYROLL_FINALIZED", "hr.payroll.finalized", runID); err != nil {
		return Run{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Run{}, err
	}
	return s.Get(ctx, session, runID)
}

// ---- small helpers ----

func (s *Service) manualInputChecksum(ctx context.Context, companyID, runID string) string {
	rows, err := s.pool.Query(ctx, `SELECT employee_id::text,component_definition_id::text,amount::text,explanation
 FROM payroll_manual_components WHERE company_id=$1 AND payroll_run_id=$2 AND archived_at IS NULL ORDER BY employee_id,component_definition_id`,
		companyID, runID)
	if err != nil {
		return sha256Hex("")
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var e, c, a, x string
		if err := rows.Scan(&e, &c, &a, &x); err != nil {
			break
		}
		b.WriteString(e + "|" + c + "|" + a + "|" + x + "\n")
	}
	return sha256Hex(b.String())
}

func (s *Service) scalarString(ctx context.Context, query string, args ...any) string {
	var value string
	if err := s.pool.QueryRow(ctx, query, args...).Scan(&value); err != nil {
		return ""
	}
	return value
}

func (s *Service) exists(ctx context.Context, query string, args ...any) bool {
	var one int
	err := s.pool.QueryRow(ctx, query, args...).Scan(&one)
	return err == nil
}

func sgkStatusOrDefault(value string) string {
	if strings.TrimSpace(value) == "" {
		return "4A"
	}
	return value
}

func pickDays(fullMonth bool, workDays int) int {
	if fullMonth {
		return 30
	}
	return workDays
}

// attendanceDays derives the paid/SGK day count and whether the month counts as
// a full 30-day month. paidDays are days that accrue pay (work, paid leave,
// public holiday, paid weekly rest); unpaidDays are absence / unpaid leave;
// recordedDays is every timesheet row. A month is "full" only when every day is
// recorded, every day is paid and none is unpaid — anything else (a mid-month
// hire with fewer rows, an absence, or a month left mostly blank) is prorated
// over 30 by the actual paid-day count.
func attendanceDays(paidDays, unpaidDays, recordedDays, daysInMonth int) (workDays int, fullMonth bool) {
	workDays = paidDays
	if workDays > 30 {
		workDays = 30
	}
	fullMonth = unpaidDays == 0 && paidDays >= daysInMonth && recordedDays >= daysInMonth
	return workDays, fullMonth
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func mustDec(value string) money.Decimal {
	d, err := money.ParseDecimal(value, 12)
	if err != nil {
		panic(err)
	}
	return d
}

func sumDecimals(values ...string) money.Decimal {
	total, _ := money.ParseDecimal("0", 12)
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		d, err := money.ParseDecimal(v, 12)
		if err != nil {
			continue
		}
		total = total.Add(d)
	}
	return total
}

func isZeroString(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	d, err := money.ParseDecimal(value, 12)
	if err != nil {
		return true
	}
	return d.Sign() == 0
}

func minimumMonthlyTaxable(minGross money.Decimal, scheme *legislation.ContributionScheme) money.Decimal {
	sgk := minGross.Mul(scheme.EmployeeSGKRate)
	unemp := minGross.Mul(scheme.EmployeeUnemploymentRate)
	result := minGross.Sub(sgk).Sub(unemp)
	if result.Sign() < 0 {
		zero, _ := money.ParseDecimal("0", 12)
		return zero
	}
	return result
}

// estimateMonthlyIncomeTaxBase returns the income-tax base a single standalone
// full month at this wage/scheme would produce. Used to fill in unrun prior
// in-company months so the year-to-date cumulative base is not simply zero.
func estimateMonthlyIncomeTaxBase(pack *legislation.Pack, scheme *legislation.ContributionScheme, gross money.Decimal, sgkStatus string) money.Decimal {
	result, err := (calculation.PayrollCalculator{}).Preview(calculation.Context{
		Pack: pack, ContributionScheme: scheme, SGKStatus: sgkStatus,
	}, "GROSS", gross)
	if err != nil {
		return mustDec("0")
	}
	return result.IncomeTaxBase
}

func errorDetail(err error) map[string]any {
	var calcErr *calculation.CalculationError
	if errors.As(err, &calcErr) {
		return map[string]any{"code": string(calcErr.Code), "field": calcErr.Field, "component": calcErr.Component, "message": calcErr.Message}
	}
	return map[string]any{"code": "PAYROLL_CALCULATION_ERROR", "message": err.Error()}
}
