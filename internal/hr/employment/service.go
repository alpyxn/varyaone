// Package employment owns employment periods, effective-dated wage terms and
// payroll-year opening balances for an employee.
package employment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/payroll/legislation"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound           = errors.New("EMPLOYMENT_NOT_FOUND")
	ErrTermNotFound       = errors.New("EMPLOYMENT_TERM_NOT_FOUND")
	ErrOverlap            = errors.New("EMPLOYMENT_PERIOD_OVERLAP")
	ErrEmployeeGone       = errors.New("EMPLOYMENT_EMPLOYEE_NOT_FOUND")
	ErrOpeningExists      = errors.New("PAYROLL_YEAR_OPENING_EXISTS")
	ErrLegislationMissing = errors.New("PAYROLL_LEGISLATION_NOT_FOUND")
)

type Service struct {
	pool        database.Querier
	legislation *legislation.Repository
}

func NewService(pool database.Querier, legislationRepo *legislation.Repository) *Service {
	return &Service{pool: pool, legislation: legislationRepo}
}

// ---- Employment period ----

type Employment struct {
	ID                string    `json:"id"`
	EmployeeID        string    `json:"employee_id"`
	StartDate         string    `json:"start_date"`
	EndDate           *string   `json:"end_date,omitempty"`
	TerminationReason *string   `json:"termination_reason,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	Version           int64     `json:"version"`
}

type EmploymentInput struct {
	StartDate string `json:"start_date"`
}

type TerminateInput struct {
	EndDate           string `json:"end_date"`
	TerminationReason string `json:"termination_reason"`
}

func (s *Service) ListEmployments(ctx context.Context, session identity.Session, employeeID string) ([]Employment, error) {
	if !session.HasPermission("hr.employee.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,employee_id::text,to_char(start_date,'YYYY-MM-DD'),
 CASE WHEN end_date IS NULL THEN NULL ELSE to_char(end_date,'YYYY-MM-DD') END,termination_reason,created_at,version
 FROM employments WHERE company_id=$1 AND employee_id=NULLIF($2,'')::uuid ORDER BY start_date DESC,id`,
		session.CurrentCompanyID, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Employment{}
	for rows.Next() {
		var e Employment
		if err := rows.Scan(&e.ID, &e.EmployeeID, &e.StartDate, &e.EndDate, &e.TerminationReason, &e.CreatedAt, &e.Version); err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return items, rows.Err()
}

func (s *Service) CreateEmployment(ctx context.Context, session identity.Session, employeeID string, input EmploymentInput, meta identity.RequestMeta) (Employment, error) {
	if !session.HasPermission("hr.employee.edit") {
		return Employment{}, identity.ErrForbidden
	}
	if _, err := parseDate(input.StartDate); err != nil {
		return Employment{}, fmt.Errorf("%w: başlangıç tarihi geçersiz", identity.ErrValidation)
	}
	id := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Employment{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employees WHERE company_id=$1 AND id=NULLIF($2,'')::uuid)`, session.CurrentCompanyID, employeeID).Scan(&exists); err != nil {
		return Employment{}, err
	}
	if !exists {
		return Employment{}, ErrEmployeeGone
	}
	_, err = tx.Exec(ctx, `INSERT INTO employments(id,company_id,employee_id,start_date) VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::date)`,
		id, session.CurrentCompanyID, employeeID, input.StartDate)
	if err != nil {
		return Employment{}, mapConstraint(err)
	}
	// Art arda çalışma dönemlerinde ücretin yeniden girilmesi gerekmesin: çalışanın
	// yeni dönem başlangıcına kadar geçerli en güncel ücret tanımını yeni döneme taşı.
	// Geçmiş dönemlerin kaydı korunur; kullanıcı Ücret sekmesinden güncelleyebilir.
	var carriedTermID string
	err = tx.QueryRow(ctx, `INSERT INTO employment_terms(id,company_id,employment_id,employee_id,effective_from,effective_to,
 wage_type,wage_period,gross_wage,currency,work_type,weekly_minutes,contribution_scheme_code,prior_employer_tax_policy,sgk_status,is_minimum_wage)
 SELECT $1,$2,$3,prev.employee_id,$4::date,NULL,
   prev.wage_type,prev.wage_period,prev.gross_wage,prev.currency,prev.work_type,prev.weekly_minutes,
   prev.contribution_scheme_code,prev.prior_employer_tax_policy,prev.sgk_status,prev.is_minimum_wage
 FROM employment_terms prev
 WHERE prev.company_id=$2 AND prev.employee_id=NULLIF($5,'')::uuid AND prev.employment_id<>$3
   AND prev.effective_from <= $4::date
 ORDER BY prev.effective_from DESC, prev.created_at DESC
 LIMIT 1
 RETURNING id::text`,
		uuid.NewString(), session.CurrentCompanyID, id, input.StartDate, employeeID).Scan(&carriedTermID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Employment{}, mapConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "EMPLOYMENT_CREATED", "hr.employment.created", employeeID, map[string]any{"employment_id": id, "carried_term_id": carriedTermID}); err != nil {
		return Employment{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Employment{}, err
	}
	return s.getEmployment(ctx, session, employeeID, id)
}

func (s *Service) TerminateEmployment(ctx context.Context, session identity.Session, employeeID, employmentID string, version int64, input TerminateInput, meta identity.RequestMeta) (Employment, error) {
	if !session.HasPermission("hr.employee.edit") {
		return Employment{}, identity.ErrForbidden
	}
	input.TerminationReason = strings.TrimSpace(input.TerminationReason)
	if _, err := parseDate(input.EndDate); err != nil {
		return Employment{}, fmt.Errorf("%w: bitiş tarihi geçersiz", identity.ErrValidation)
	}
	if input.TerminationReason == "" {
		return Employment{}, fmt.Errorf("%w: sonlandırma gerekçesi zorunlu", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Employment{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE employments SET end_date=NULLIF($4,'')::date,termination_reason=$5,updated_at=now(),version=version+1
 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND employee_id=NULLIF($3,'')::uuid AND version=$6`,
		session.CurrentCompanyID, employmentID, employeeID, input.EndDate, input.TerminationReason, version)
	if err != nil {
		return Employment{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		if s.employmentExists(ctx, session, employeeID, employmentID) {
			return Employment{}, identity.ErrConflict
		}
		return Employment{}, ErrNotFound
	}
	// Dönem kapanınca o döneme ait açık ücret tanımları da kapanır (artık "güncel" olmaz).
	if _, err = tx.Exec(ctx, `UPDATE employment_terms
 SET effective_to = LEAST(effective_to, NULLIF($3,'')::date), version = version + 1
 WHERE company_id=$1 AND employment_id=NULLIF($2,'')::uuid
   AND (effective_to IS NULL OR effective_to > NULLIF($3,'')::date)
   AND effective_from <= NULLIF($3,'')::date`,
		session.CurrentCompanyID, employmentID, input.EndDate); err != nil {
		return Employment{}, mapConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "EMPLOYMENT_TERMINATED", "hr.employment.terminated", employeeID, map[string]any{"employment_id": employmentID}); err != nil {
		return Employment{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Employment{}, err
	}
	return s.getEmployment(ctx, session, employeeID, employmentID)
}

func (s *Service) getEmployment(ctx context.Context, session identity.Session, employeeID, employmentID string) (Employment, error) {
	var e Employment
	err := s.pool.QueryRow(ctx, `SELECT id::text,employee_id::text,to_char(start_date,'YYYY-MM-DD'),
 CASE WHEN end_date IS NULL THEN NULL ELSE to_char(end_date,'YYYY-MM-DD') END,termination_reason,created_at,version
 FROM employments WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND employee_id=NULLIF($3,'')::uuid`,
		session.CurrentCompanyID, employmentID, employeeID).Scan(&e.ID, &e.EmployeeID, &e.StartDate, &e.EndDate, &e.TerminationReason, &e.CreatedAt, &e.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Employment{}, ErrNotFound
	}
	return e, err
}

func (s *Service) employmentExists(ctx context.Context, session identity.Session, employeeID, employmentID string) bool {
	var ok bool
	_ = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employments WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND employee_id=NULLIF($3,'')::uuid)`,
		session.CurrentCompanyID, employmentID, employeeID).Scan(&ok)
	return ok
}

// ---- Employment terms ----

type Term struct {
	ID                     string  `json:"id"`
	EmploymentID           string  `json:"employment_id"`
	EffectiveFrom          string  `json:"effective_from"`
	EffectiveTo            *string `json:"effective_to,omitempty"`
	WageType               string  `json:"wage_type"`
	WagePeriod             string  `json:"wage_period"`
	GrossWage              string  `json:"gross_wage"`
	Currency               string  `json:"currency"`
	WorkType               string  `json:"work_type"`
	WeeklyMinutes          int     `json:"weekly_minutes"`
	ContributionSchemeCode string  `json:"contribution_scheme_code"`
	PriorEmployerTaxPolicy string  `json:"prior_employer_tax_policy"`
	SgkStatus              string  `json:"sgk_status"`
	IsMinimumWage          bool    `json:"is_minimum_wage"`
	Version                int64   `json:"version"`
}

type TermInput struct {
	EffectiveFrom          string `json:"effective_from"`
	EffectiveTo            string `json:"effective_to"`
	GrossWage              string `json:"gross_wage"`
	Currency               string `json:"currency"`
	WorkType               string `json:"work_type"`
	WeeklyMinutes          int    `json:"weekly_minutes"`
	ContributionSchemeCode string `json:"contribution_scheme_code"`
	PriorEmployerTaxPolicy string `json:"prior_employer_tax_policy"`
	SgkStatus              string `json:"sgk_status"`
	IsMinimumWage          bool   `json:"is_minimum_wage"`
}

var validWorkType = map[string]bool{"FULL_TIME": true, "PART_TIME": true, "INTERN": true, "CONTRACT": true}
var validPriorPolicy = map[string]bool{"SEPARATE": true, "CARRY": true}
var validSgkStatus = map[string]bool{
	"4A": true, "4A_SGDP": true, "4A_NO_UNEMPLOYMENT": true,
	"APPRENTICE": true, "4B": true, "4C": true,
}

func (s *Service) ListTerms(ctx context.Context, session identity.Session, employeeID, employmentID string) ([]Term, error) {
	if !session.HasPermission("hr.employee.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT t.id::text,t.employment_id::text,to_char(t.effective_from,'YYYY-MM-DD'),
 CASE WHEN t.effective_to IS NULL THEN NULL ELSE to_char(t.effective_to,'YYYY-MM-DD') END,
 t.wage_type,t.wage_period,t.gross_wage::text,t.currency,t.work_type,t.weekly_minutes,t.contribution_scheme_code,t.prior_employer_tax_policy,t.sgk_status,t.is_minimum_wage,t.version
 FROM employment_terms t
 JOIN employments e ON e.company_id=t.company_id AND e.id=t.employment_id
 WHERE t.company_id=$1 AND e.employee_id=NULLIF($2,'')::uuid AND ($3='' OR t.employment_id=NULLIF($3,'')::uuid)
 ORDER BY t.effective_from DESC,t.id`,
		session.CurrentCompanyID, employeeID, strings.TrimSpace(employmentID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Term{}
	for rows.Next() {
		var t Term
		if err := rows.Scan(&t.ID, &t.EmploymentID, &t.EffectiveFrom, &t.EffectiveTo, &t.WageType, &t.WagePeriod,
			&t.GrossWage, &t.Currency, &t.WorkType, &t.WeeklyMinutes, &t.ContributionSchemeCode, &t.PriorEmployerTaxPolicy, &t.SgkStatus, &t.IsMinimumWage, &t.Version); err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	return items, rows.Err()
}

func (s *Service) CreateTerm(ctx context.Context, session identity.Session, employeeID, employmentID string, input TermInput, meta identity.RequestMeta) (Term, error) {
	if !session.HasPermission("hr.employee.edit") {
		return Term{}, identity.ErrForbidden
	}
	if strings.TrimSpace(input.EffectiveFrom) == "" {
		input.EffectiveFrom = time.Now().UTC().Format("2006-01-02")
	}
	if input.IsMinimumWage {
		// Never trust a client-sent gross for a "minimum wage" term — always
		// pin it to the currently active legislation pack's figure.
		effectiveFrom, err := parseDate(input.EffectiveFrom)
		if err != nil {
			return Term{}, fmt.Errorf("%w: geçerlilik başlangıcı geçersiz", identity.ErrValidation)
		}
		minGross, err := s.minimumMonthlyGross(ctx, session.CurrentCompanyID, effectiveFrom)
		if err != nil {
			return Term{}, err
		}
		input.GrossWage = minGross
	}
	if err := validateTerm(&input); err != nil {
		return Term{}, err
	}
	id := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Term{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var ownerEmployee string
	err = tx.QueryRow(ctx, `SELECT employee_id::text FROM employments WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`,
		session.CurrentCompanyID, employmentID).Scan(&ownerEmployee)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && ownerEmployee != employeeID) {
		return Term{}, ErrNotFound
	}
	if err != nil {
		return Term{}, err
	}
	// Yeni ücret girildiğinde önceki açık ücret otomatik kapanır (geçmiş korunur);
	// aynı gün ya da sonrasında başlayan, henüz geçerli olmamış açık kayıt silinir.
	if _, err = tx.Exec(ctx, `DELETE FROM employment_terms
 WHERE company_id=$1 AND employment_id=NULLIF($2,'')::uuid AND effective_to IS NULL AND effective_from>=$3::date`,
		session.CurrentCompanyID, employmentID, input.EffectiveFrom); err != nil {
		return Term{}, mapConstraint(err)
	}
	// Yeni ücret girildiğinde çalışanın önceki AÇIK ücret tanımları (hangi çalışma
	// dönemine bağlı olursa olsun) kapanır; bir çalışanın aynı anda tek geçerli ücreti olur.
	if _, err = tx.Exec(ctx, `UPDATE employment_terms SET effective_to=($3::date - 1),version=version+1
 WHERE company_id=$1 AND employee_id=NULLIF($2,'')::uuid AND effective_to IS NULL AND effective_from<$3::date`,
		session.CurrentCompanyID, employeeID, input.EffectiveFrom); err != nil {
		return Term{}, mapConstraint(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO employment_terms(id,company_id,employment_id,employee_id,effective_from,effective_to,
 wage_type,wage_period,gross_wage,currency,work_type,weekly_minutes,contribution_scheme_code,prior_employer_tax_policy,sgk_status,is_minimum_wage)
 VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,NULLIF($5,'')::date,NULLIF($6,'')::date,
 'GROSS','MONTHLY',$7::numeric,$8,$9,$10,$11,$12,$13,$14)`,
		id, session.CurrentCompanyID, employmentID, employeeID, input.EffectiveFrom, input.EffectiveTo,
		input.GrossWage, input.Currency, input.WorkType, input.WeeklyMinutes, input.ContributionSchemeCode, input.PriorEmployerTaxPolicy, input.SgkStatus, input.IsMinimumWage)
	if err != nil {
		return Term{}, mapConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "EMPLOYMENT_TERM_CREATED", "hr.employment_term.created", employeeID, map[string]any{"term_id": id, "employment_id": employmentID}); err != nil {
		return Term{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Term{}, err
	}
	return s.getTerm(ctx, session, id)
}

// CloseTerm sets effective_to on an open term so a successor term can begin.
func (s *Service) CloseTerm(ctx context.Context, session identity.Session, employeeID, termID string, version int64, effectiveTo string, meta identity.RequestMeta) (Term, error) {
	if !session.HasPermission("hr.employee.edit") {
		return Term{}, identity.ErrForbidden
	}
	if _, err := parseDate(effectiveTo); err != nil {
		return Term{}, fmt.Errorf("%w: bitiş tarihi geçersiz", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Term{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE employment_terms SET effective_to=NULLIF($4,'')::date,version=version+1
 WHERE company_id=$1 AND id=NULLIF($2,'')::uuid AND employee_id=NULLIF($3,'')::uuid AND version=$5`,
		session.CurrentCompanyID, termID, employeeID, effectiveTo, version)
	if err != nil {
		return Term{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return Term{}, ErrTermNotFound
	}
	if err = writeEvent(ctx, tx, session, meta, "EMPLOYMENT_TERM_CLOSED", "hr.employment_term.closed", employeeID, map[string]any{"term_id": termID}); err != nil {
		return Term{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Term{}, err
	}
	return s.getTerm(ctx, session, termID)
}

func (s *Service) getTerm(ctx context.Context, session identity.Session, termID string) (Term, error) {
	var t Term
	err := s.pool.QueryRow(ctx, `SELECT id::text,employment_id::text,to_char(effective_from,'YYYY-MM-DD'),
 CASE WHEN effective_to IS NULL THEN NULL ELSE to_char(effective_to,'YYYY-MM-DD') END,
 wage_type,wage_period,gross_wage::text,currency,work_type,weekly_minutes,contribution_scheme_code,prior_employer_tax_policy,sgk_status,is_minimum_wage,version
 FROM employment_terms WHERE company_id=$1 AND id=NULLIF($2,'')::uuid`, session.CurrentCompanyID, termID).
		Scan(&t.ID, &t.EmploymentID, &t.EffectiveFrom, &t.EffectiveTo, &t.WageType, &t.WagePeriod, &t.GrossWage,
			&t.Currency, &t.WorkType, &t.WeeklyMinutes, &t.ContributionSchemeCode, &t.PriorEmployerTaxPolicy, &t.SgkStatus, &t.IsMinimumWage, &t.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Term{}, ErrTermNotFound
	}
	return t, err
}

// NormalizeTerm fills in the wage defaults and validates the rest. It is
// exported so a new employee's first wage (written with the card, in one
// transaction) goes through exactly the same rules as one entered later.
func NormalizeTerm(input *TermInput) error { return validateTerm(input) }

func validateTerm(input *TermInput) error {
	// Ücret formu sadeleştirildi: para birimi her zaman TRY, çalışma türü/SGK
	// kodu/önceki işveren politikası/haftalık süre girilmezse makul varsayılan.
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if input.Currency == "" {
		input.Currency = "TRY"
	}
	input.WorkType = strings.ToUpper(strings.TrimSpace(input.WorkType))
	if input.WorkType == "" {
		input.WorkType = "FULL_TIME"
	}
	input.ContributionSchemeCode = strings.TrimSpace(input.ContributionSchemeCode)
	if input.ContributionSchemeCode == "" {
		input.ContributionSchemeCode = "NO_DISCOUNT"
	}
	input.PriorEmployerTaxPolicy = strings.ToUpper(strings.TrimSpace(input.PriorEmployerTaxPolicy))
	if input.PriorEmployerTaxPolicy == "" {
		input.PriorEmployerTaxPolicy = "SEPARATE"
	}
	input.SgkStatus = strings.ToUpper(strings.TrimSpace(input.SgkStatus))
	if input.SgkStatus == "" {
		input.SgkStatus = "4A"
	}
	if !validSgkStatus[input.SgkStatus] {
		return fmt.Errorf("%w: sigortalılık statüsü geçersiz", identity.ErrValidation)
	}
	if input.WeeklyMinutes <= 0 {
		input.WeeklyMinutes = 2700
	}
	input.GrossWage = strings.TrimSpace(input.GrossWage)
	if _, err := parseDate(input.EffectiveFrom); err != nil {
		return fmt.Errorf("%w: geçerlilik başlangıcı geçersiz", identity.ErrValidation)
	}
	if strings.TrimSpace(input.EffectiveTo) != "" {
		if _, err := parseDate(input.EffectiveTo); err != nil {
			return fmt.Errorf("%w: geçerlilik bitişi geçersiz", identity.ErrValidation)
		}
	}
	if len(input.Currency) != 3 {
		return fmt.Errorf("%w: para birimi 3 harf olmalı", identity.ErrValidation)
	}
	if !validWorkType[input.WorkType] {
		return fmt.Errorf("%w: çalışma türü geçersiz", identity.ErrValidation)
	}
	if !validPriorPolicy[input.PriorEmployerTaxPolicy] {
		return fmt.Errorf("%w: önceki işveren politikası geçersiz", identity.ErrValidation)
	}
	if input.WeeklyMinutes <= 0 || input.WeeklyMinutes > 10080 {
		return fmt.Errorf("%w: haftalık dakika geçersiz", identity.ErrValidation)
	}
	if input.ContributionSchemeCode == "" {
		return fmt.Errorf("%w: SGK indirim kodu zorunlu", identity.ErrValidation)
	}
	if input.GrossWage == "" {
		return fmt.Errorf("%w: brüt ücret zorunlu", identity.ErrValidation)
	}
	return nil
}

// ---- Payroll-year openings ----

type Opening struct {
	ID                      string    `json:"id"`
	EmployeeID              string    `json:"employee_id"`
	TaxYear                 int       `json:"tax_year"`
	Source                  string    `json:"source"`
	CumulativeIncomeTaxBase string    `json:"cumulative_income_tax_base"`
	EvidenceNote            string    `json:"evidence_note"`
	CreatedAt               time.Time `json:"created_at"`
	Version                 int64     `json:"version"`
}

type OpeningInput struct {
	TaxYear                 int    `json:"tax_year"`
	Source                  string `json:"source"`
	CumulativeIncomeTaxBase string `json:"cumulative_income_tax_base"`
	EvidenceNote            string `json:"evidence_note"`
}

var validOpeningSource = map[string]bool{"COMPANY_MIGRATION": true, "PRIOR_EMPLOYER_CARRY": true}

func (s *Service) ListOpenings(ctx context.Context, session identity.Session, employeeID string) ([]Opening, error) {
	if !session.HasPermission("hr.payroll.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,employee_id::text,tax_year,source,cumulative_income_tax_base::text,evidence_note,created_at,version
 FROM employee_payroll_year_openings WHERE company_id=$1 AND employee_id=NULLIF($2,'')::uuid ORDER BY tax_year DESC,source`,
		session.CurrentCompanyID, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Opening{}
	for rows.Next() {
		var o Opening
		if err := rows.Scan(&o.ID, &o.EmployeeID, &o.TaxYear, &o.Source, &o.CumulativeIncomeTaxBase, &o.EvidenceNote, &o.CreatedAt, &o.Version); err != nil {
			return nil, err
		}
		items = append(items, o)
	}
	return items, rows.Err()
}

func (s *Service) CreateOpening(ctx context.Context, session identity.Session, employeeID string, input OpeningInput, meta identity.RequestMeta) (Opening, error) {
	if !session.HasPermission("hr.payroll.calculate") {
		return Opening{}, identity.ErrForbidden
	}
	input.Source = strings.ToUpper(strings.TrimSpace(input.Source))
	input.EvidenceNote = strings.TrimSpace(input.EvidenceNote)
	input.CumulativeIncomeTaxBase = strings.TrimSpace(input.CumulativeIncomeTaxBase)
	if input.TaxYear < 2000 || input.TaxYear > 2200 {
		return Opening{}, fmt.Errorf("%w: vergi yılı geçersiz", identity.ErrValidation)
	}
	if !validOpeningSource[input.Source] {
		return Opening{}, fmt.Errorf("%w: açılış kaynağı geçersiz", identity.ErrValidation)
	}
	if input.EvidenceNote == "" {
		return Opening{}, fmt.Errorf("%w: dayanak notu zorunlu", identity.ErrValidation)
	}
	if input.CumulativeIncomeTaxBase == "" {
		return Opening{}, fmt.Errorf("%w: kümülatif matrah zorunlu", identity.ErrValidation)
	}
	id := uuid.NewString()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Opening{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	_, err = tx.Exec(ctx, `INSERT INTO employee_payroll_year_openings(id,company_id,employee_id,tax_year,source,cumulative_income_tax_base,evidence_note,created_by)
 SELECT $1,$2,e.id,$4,$5,$6::numeric,$7,$8 FROM employees e WHERE e.company_id=$2 AND e.id=NULLIF($3,'')::uuid`,
		id, session.CurrentCompanyID, employeeID, input.TaxYear, input.Source, input.CumulativeIncomeTaxBase, input.EvidenceNote, session.User.ID)
	if err != nil {
		return Opening{}, mapConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "PAYROLL_YEAR_OPENING_CREATED", "hr.payroll_year_opening.created", employeeID, map[string]any{"opening_id": id, "tax_year": input.TaxYear}); err != nil {
		return Opening{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Opening{}, err
	}
	var o Opening
	err = s.pool.QueryRow(ctx, `SELECT id::text,employee_id::text,tax_year,source,cumulative_income_tax_base::text,evidence_note,created_at,version
 FROM employee_payroll_year_openings WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, id).
		Scan(&o.ID, &o.EmployeeID, &o.TaxYear, &o.Source, &o.CumulativeIncomeTaxBase, &o.EvidenceNote, &o.CreatedAt, &o.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Opening{}, ErrEmployeeGone
	}
	return o, err
}

// ---- Minimum wage ----

// minimumMonthlyGross returns the currently active TR legislation pack's
// minimum monthly gross wage as a numeric string, for the given date.
func (s *Service) minimumMonthlyGross(ctx context.Context, companyID string, date time.Time) (string, error) {
	pack, err := s.legislation.ActivePack(ctx, companyID, date)
	if errors.Is(err, legislation.ErrPackNotFound) {
		return "", ErrLegislationMissing
	}
	if err != nil {
		return "", err
	}
	return pack.MinimumMonthlyGross.String(), nil
}

// ApplyMinimumWageChange is called once a new minimum-wage legislation pack is
// activated. Every open (effective_to IS NULL) employment term flagged
// is_minimum_wage is closed as of the day before effectiveFrom and replaced by
// a successor term at newGross, otherwise identical. It returns how many
// employees were updated.
func (s *Service) ApplyMinimumWageChange(ctx context.Context, companyID, effectiveFrom, newGross, actorUserID string, meta identity.RequestMeta) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	rows, err := tx.Query(ctx, `SELECT id::text,employment_id::text,employee_id::text,currency,work_type,weekly_minutes,
 contribution_scheme_code,prior_employer_tax_policy,sgk_status
 FROM employment_terms WHERE company_id=$1 AND is_minimum_wage AND effective_to IS NULL AND effective_from<$2::date`,
		companyID, effectiveFrom)
	if err != nil {
		return 0, err
	}
	type openTerm struct {
		id, employmentID, employeeID, currency, workType, scheme, priorPolicy, sgkStatus string
		weeklyMinutes                                                                    int
	}
	terms := []openTerm{}
	for rows.Next() {
		var t openTerm
		if err := rows.Scan(&t.id, &t.employmentID, &t.employeeID, &t.currency, &t.workType, &t.weeklyMinutes, &t.scheme, &t.priorPolicy, &t.sgkStatus); err != nil {
			rows.Close()
			return 0, err
		}
		terms = append(terms, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, t := range terms {
		if _, err = tx.Exec(ctx, `UPDATE employment_terms SET effective_to=($2::date - 1),version=version+1
 WHERE company_id=$1 AND id=$3`, companyID, effectiveFrom, t.id); err != nil {
			return 0, mapConstraint(err)
		}
		newID := uuid.NewString()
		if _, err = tx.Exec(ctx, `INSERT INTO employment_terms(id,company_id,employment_id,employee_id,effective_from,effective_to,
 wage_type,wage_period,gross_wage,currency,work_type,weekly_minutes,contribution_scheme_code,prior_employer_tax_policy,sgk_status,is_minimum_wage)
 VALUES($1,$2,$3,$4,$5::date,NULL,'GROSS','MONTHLY',$6::numeric,$7,$8,$9,$10,$11,$12,true)`,
			newID, companyID, t.employmentID, t.employeeID, effectiveFrom, newGross, t.currency, t.workType, t.weeklyMinutes, t.scheme, t.priorPolicy, t.sgkStatus); err != nil {
			return 0, mapConstraint(err)
		}
	}
	if len(terms) > 0 {
		details, _ := json.Marshal(map[string]any{"effective_from": effectiveFrom, "new_gross": newGross, "employee_count": len(terms)})
		if _, err = tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent)
 VALUES($1,$2,$3,'MINIMUM_WAGE_TERMS_UPDATED','employment_term',NULLIF($2,'')::uuid,$4,$5,$6,$7)`,
			uuid.NewString(), companyID, actorUserID, details, meta.TraceID, meta.IP, meta.UserAgent); err != nil {
			return 0, err
		}
		payload, _ := json.Marshal(map[string]any{"effective_from": effectiveFrom, "employee_count": len(terms)})
		if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,'hr.employment_terms.minimum_wage_updated',1,$2,$3,$4)`,
			uuid.NewString(), companyID, meta.TraceID, payload); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(terms), nil
}

// ---- helpers ----

func parseDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("empty")
	}
	return time.Parse("2006-01-02", value)
}

func mapConstraint(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch {
	case pgErr.Code == "23P01" && strings.Contains(pgErr.ConstraintName, "employments_no_overlap"):
		return fmt.Errorf("%w: çalışma dönemi mevcut bir dönemle çakışıyor", identity.ErrValidation)
	case pgErr.Code == "23P01" && strings.Contains(pgErr.ConstraintName, "employment_terms_no_overlap"):
		return fmt.Errorf("%w: ücret koşulu mevcut bir dönemle çakışıyor", identity.ErrValidation)
	case pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "openings"):
		return ErrOpeningExists
	case pgErr.Code == "23503":
		return ErrEmployeeGone
	case pgErr.Code == "23514":
		return fmt.Errorf("%w: %s", identity.ErrValidation, pgErr.Message)
	}
	return err
}

func writeEvent(ctx context.Context, tx pgx.Tx, session identity.Session, meta identity.RequestMeta, auditType, outboxType, employeeID string, details map[string]any) error {
	detailBytes, _ := json.Marshal(details)
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent)
 VALUES($1,$2,$3,$4,'employee',$5,$6,$7,$8,$9)`,
		uuid.NewString(), session.CurrentCompanyID, session.User.ID, auditType, employeeID, detailBytes, meta.TraceID, meta.IP, meta.UserAgent); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"employee_id": employeeID})
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`,
		uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, payload)
	return err
}
