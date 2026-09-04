package employee

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
)

// Readiness answers one question for one month: is this employee's card complete
// enough to be put on the puantaj, and complete enough for the bordro engine?
//
// Both the timesheet and the payroll run used to answer it silently — an
// employee with no çalışma dönemi was skipped by the generator, and one with no
// ücret tanımı was dropped from the payroll population by an inner join, so a
// half-filled card looked fine right up to the moment somebody noticed a missing
// payslip. The same check now feeds the guards on both sides and the UI.
type Readiness struct {
	EmployeeID   string  `json:"employee_id"`
	EmployeeCode string  `json:"employee_code"`
	Name         string  `json:"name"`
	Issues       []Issue `json:"issues"`
	// Timesheet is true when the employee may be entered on the puantaj for the
	// period; Payroll is true when the bordro engine can also calculate them.
	Timesheet bool `json:"timesheet_ready"`
	Payroll   bool `json:"payroll_ready"`
}

// Issue is one missing or unusable piece of the employee card. Blocks is
// TIMESHEET (the employee cannot be put on the puantaj at all) or PAYROLL (the
// puantaj is fine, the bordro engine cannot calculate them).
type Issue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Blocks  string `json:"blocks"`
	// Tab names the çalışan kartı tab the fix lives on, so the UI can point there.
	Tab string `json:"tab"`
}

const (
	blocksTimesheet = "TIMESHEET"
	blocksPayroll   = "PAYROLL"
)

// supportedSGKStatus lists the statuses the v1 payroll engine can calculate.
// It mirrors payroll/calculation.validateContext.
var supportedSGKStatus = map[string]bool{"4A": true, "4A_SGDP": true, "4A_NO_UNEMPLOYMENT": true}

// employeeFacts is the raw card state for one employee and one month.
type employeeFacts struct {
	EmployeeID    string
	EmployeeCode  string
	Name          string
	HasEmployment bool // any çalışma dönemi at all
	InPeriod      bool // a çalışma dönemi covering the month
	GrossWage     string
	Currency      string
	WorkType      string
	SchemeCode    string
	SgkStatus     string
	WageType      string
	WagePeriod    string
	HasWageTerm   bool
}

const readinessQuery = `SELECT DISTINCT ON (e.id) e.id::text,e.employee_code,e.first_name||' '||e.last_name,
 EXISTS(SELECT 1 FROM employments x WHERE x.company_id=e.company_id AND x.employee_id=e.id),
 emp.id IS NOT NULL,
 t.employment_id IS NOT NULL,
 COALESCE(t.gross_wage::text,''),COALESCE(t.currency,''),COALESCE(t.work_type,''),
 COALESCE(t.contribution_scheme_code,''),COALESCE(t.sgk_status,''),COALESCE(t.wage_type,''),COALESCE(t.wage_period,'')
 FROM employees e
 LEFT JOIN employments emp ON emp.company_id=e.company_id AND emp.employee_id=e.id
   AND daterange(emp.start_date,COALESCE(emp.end_date,'infinity'::date),'[]') && daterange($2::date,$3::date,'[]')
 LEFT JOIN LATERAL (
   SELECT term.employment_id,term.gross_wage,term.currency,term.work_type,term.contribution_scheme_code,
          term.sgk_status,term.wage_type,term.wage_period
   FROM employment_terms term
   WHERE term.company_id=e.company_id AND term.employment_id=emp.id
     AND daterange(term.effective_from,COALESCE(term.effective_to,'infinity'::date),'[]')
       @> GREATEST($2::date,emp.start_date)
   ORDER BY term.effective_from DESC
   LIMIT 1
 ) t ON true
 WHERE e.company_id=$1 AND e.status='ACTIVE' AND ($4='' OR e.id=NULLIF($4,'')::uuid)
 ORDER BY e.id,emp.start_date DESC`

// CheckPeriod returns the readiness of every ACTIVE employee for the month that
// [from,to] spans, keyed in employee-code order.
func CheckPeriod(ctx context.Context, pool database.Querier, companyID string, from, to time.Time) ([]Readiness, error) {
	return checkReadiness(ctx, pool, companyID, from, to, "")
}

// CheckOne returns the readiness of a single employee. A missing (or non-ACTIVE)
// employee comes back as ErrNotFound.
func CheckOne(ctx context.Context, pool database.Querier, companyID, employeeID string, from, to time.Time) (Readiness, error) {
	items, err := checkReadiness(ctx, pool, companyID, from, to, strings.TrimSpace(employeeID))
	if err != nil {
		return Readiness{}, err
	}
	if len(items) == 0 {
		return Readiness{}, ErrNotFound
	}
	return items[0], nil
}

func checkReadiness(ctx context.Context, pool database.Querier, companyID string, from, to time.Time, employeeID string) ([]Readiness, error) {
	rows, err := pool.Query(ctx, readinessQuery, companyID,
		from.Format("2006-01-02"), to.Format("2006-01-02"), employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Readiness{}
	for rows.Next() {
		var f employeeFacts
		if err := rows.Scan(&f.EmployeeID, &f.EmployeeCode, &f.Name, &f.HasEmployment, &f.InPeriod, &f.HasWageTerm,
			&f.GrossWage, &f.Currency, &f.WorkType, &f.SchemeCode, &f.SgkStatus, &f.WageType, &f.WagePeriod); err != nil {
			return nil, err
		}
		items = append(items, readinessOf(f))
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].EmployeeCode < items[j].EmployeeCode })
	return items, nil
}

// readinessOf turns one employee's card state into the list of things that stop
// the puantaj or the bordro. It is pure so the rules can be unit tested.
func readinessOf(f employeeFacts) Readiness {
	r := Readiness{EmployeeID: f.EmployeeID, EmployeeCode: f.EmployeeCode, Name: f.Name, Issues: []Issue{}}
	switch {
	case !f.HasEmployment:
		r.Issues = append(r.Issues, Issue{Code: "EMPLOYEE_NO_EMPLOYMENT", Blocks: blocksTimesheet, Tab: "employment",
			Message: "İşe giriş tarihi girilmemiş. İstihdam sekmesinden çalışma dönemi başlatın."})
	case !f.InPeriod:
		r.Issues = append(r.Issues, Issue{Code: "EMPLOYEE_NOT_EMPLOYED_IN_PERIOD", Blocks: blocksTimesheet, Tab: "employment",
			Message: "Çalışan bu ayda işte görünmüyor. İşe giriş/çıkış tarihlerini İstihdam sekmesinden kontrol edin."})
	}
	if !f.InPeriod {
		// Wage checks would only repeat the same "no employment" story.
		r.Timesheet, r.Payroll = false, false
		return r
	}

	r.Issues = append(r.Issues, WageIssues(Wage{
		Defined: f.HasWageTerm, GrossWage: f.GrossWage, Currency: f.Currency, WorkType: f.WorkType,
		SchemeCode: f.SchemeCode, SgkStatus: f.SgkStatus, WageType: f.WageType, WagePeriod: f.WagePeriod,
	})...)

	r.Timesheet, r.Payroll = true, true
	for _, issue := range r.Issues {
		if issue.Blocks == blocksTimesheet {
			r.Timesheet = false
		}
		r.Payroll = false
	}
	return r
}

// Wage is the ücret tanımı as the payroll engine will read it.
type Wage struct {
	Defined    bool // a term covering the period exists at all
	GrossWage  string
	Currency   string
	WorkType   string
	SchemeCode string
	SgkStatus  string
	WageType   string
	WagePeriod string
}

// WageIssues lists everything about a wage definition that stops the bordro
// engine. The payroll run calls it too, so an employee it cannot calculate gets
// the same sentence on the bordro that the çalışan kartı shows.
func WageIssues(w Wage) []Issue {
	if !w.Defined {
		return []Issue{{Code: "EMPLOYEE_NO_WAGE", Blocks: blocksPayroll, Tab: "wage",
			Message: "Ücret tanımı yok. Ücret sekmesinden brüt ücreti girin."}}
	}
	issues := []Issue{}
	if isZeroWage(w.GrossWage) {
		issues = append(issues, Issue{Code: "EMPLOYEE_WAGE_ZERO", Blocks: blocksPayroll, Tab: "wage",
			Message: "Brüt ücret sıfır. Ücret sekmesinden geçerli bir tutar girin."})
	}
	if w.Currency != "TRY" {
		issues = append(issues, Issue{Code: "EMPLOYEE_WAGE_CURRENCY", Blocks: blocksPayroll, Tab: "wage",
			Message: "Ücret TRY dışında bir para biriminde; bordro yalnız TRY hesaplar."})
	}
	if w.WorkType != "FULL_TIME" {
		issues = append(issues, Issue{Code: "EMPLOYEE_WORK_TYPE", Blocks: blocksPayroll, Tab: "wage",
			Message: "Çalışma türü tam zamanlı değil; bu çalışanın bordrosu otomatik hesaplanmaz."})
	}
	if (w.WageType != "" && w.WageType != "GROSS") || (w.WagePeriod != "" && w.WagePeriod != "MONTHLY") {
		issues = append(issues, Issue{Code: "EMPLOYEE_WAGE_BASIS", Blocks: blocksPayroll, Tab: "wage",
			Message: "Ücret aylık brüt olarak tanımlanmalı."})
	}
	if strings.TrimSpace(w.SchemeCode) == "" {
		issues = append(issues, Issue{Code: "EMPLOYEE_NO_SCHEME", Blocks: blocksPayroll, Tab: "wage",
			Message: "SGK teşvik/indirim kodu seçilmemiş. Ücret sekmesinden seçin."})
	}
	if !supportedSGKStatus[w.SgkStatus] {
		issues = append(issues, Issue{Code: "EMPLOYEE_SGK_STATUS", Blocks: blocksPayroll, Tab: "wage",
			Message: "Sigortalılık statüsü (çırak/stajyer, 4/b, 4/c) otomatik bordro kapsamı dışında; bordro manuel hazırlanmalı."})
	}
	return issues
}

// FirstBlocking returns the message of the first issue blocking the given stage,
// or "" when nothing does.
func (r Readiness) FirstBlocking(stage string) string {
	for _, issue := range r.Issues {
		if issue.Blocks == stage {
			return issue.Message
		}
	}
	return ""
}

// TimesheetBlocker is the reason the employee cannot be entered on the puantaj.
func (r Readiness) TimesheetBlocker() string { return r.FirstBlocking(blocksTimesheet) }

// ListReadiness is the read side of the check, behind the employee read
// permission.
func (s *Service) ListReadiness(ctx context.Context, session identity.Session, year, month int) ([]Readiness, error) {
	if !session.HasPermission("hr.employee.read") {
		return nil, identity.ErrForbidden
	}
	from, to, err := monthBounds(year, month)
	if err != nil {
		return nil, err
	}
	return CheckPeriod(ctx, s.pool, session.CurrentCompanyID, from, to)
}

func monthBounds(year, month int) (time.Time, time.Time, error) {
	if year < 2000 || year > 2200 || month < 1 || month > 12 {
		return time.Time{}, time.Time{}, ErrInvalidPeriod
	}
	from := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	return from, from.AddDate(0, 1, -1), nil
}

func isZeroWage(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	value = strings.TrimLeft(value, "+")
	if strings.HasPrefix(value, "-") {
		return true
	}
	return strings.Trim(value, "0.") == ""
}
